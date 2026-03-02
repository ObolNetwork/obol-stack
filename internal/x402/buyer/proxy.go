package buyer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	x402 "github.com/mark3labs/x402-go"
	x402http "github.com/mark3labs/x402-go/http"
)

// Proxy is an OpenAI-compatible reverse proxy that routes requests to upstream
// x402-gated endpoints, attaching pre-signed payment headers automatically.
//
// Routing: /upstream/<name>/v1/... → upstream URL + /v1/...
type Proxy struct {
	mu       sync.RWMutex
	signers  map[string]*PreSignedSigner
	upstreams map[string]*upstreamEntry
	mux      *http.ServeMux
}

type upstreamEntry struct {
	config  UpstreamConfig
	handler http.Handler
}

// NewProxy creates a proxy from the given config and auth pools.
func NewProxy(cfg *Config, auths AuthsFile) (*Proxy, error) {
	p := &Proxy{
		signers:   make(map[string]*PreSignedSigner),
		upstreams: make(map[string]*upstreamEntry),
		mux:       http.NewServeMux(),
	}

	for name, upstream := range cfg.Upstreams {
		authPool := auths[name]
		if len(authPool) == 0 {
			log.Printf("WARNING: upstream %q has 0 pre-signed auths", name)
		}

		signer := NewPreSignedSigner(
			upstream.Network,
			upstream.PayTo,
			upstream.Asset,
			upstream.Price,
			authPool,
		)
		p.signers[name] = signer

		handler, err := p.buildUpstreamHandler(name, upstream, signer)
		if err != nil {
			return nil, fmt.Errorf("build handler for %q: %w", name, err)
		}
		p.upstreams[name] = &upstreamEntry{config: upstream, handler: handler}

		// Register route: /upstream/<name>/ catches all sub-paths.
		prefix := fmt.Sprintf("/upstream/%s/", name)
		p.mux.Handle(prefix, http.StripPrefix(
			strings.TrimSuffix(prefix, "/"),
			handler,
		))
	}

	// Health and status endpoints.
	p.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	p.mux.HandleFunc("GET /status", p.handleStatus)

	return p, nil
}

// ServeHTTP dispatches to the internal mux.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mux.ServeHTTP(w, r)
}

// buildUpstreamHandler creates a reverse proxy for one upstream with X402Transport.
// It wraps the handler with body buffering so X402Transport can replay requests
// after receiving a 402 response.
func (p *Proxy) buildUpstreamHandler(name string, cfg UpstreamConfig, signer *PreSignedSigner) (http.Handler, error) {
	target, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", cfg.URL, err)
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Preserve the sub-path from the original request.
			// After StripPrefix, r.URL.Path is e.g. "/v1/chat/completions".
			pr.Out.URL.Path = singleJoiningSlash(target.Path, pr.In.URL.Path)
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
		},
		Transport: &x402http.X402Transport{
			Base:     http.DefaultTransport,
			Signers:  []x402.Signer{signer},
			Selector: x402.NewDefaultPaymentSelector(),
			OnPaymentSuccess: func(event x402.PaymentEvent) {
				log.Printf("[%s] payment success: tx=%s amount=%s", name, event.Transaction, event.Amount)
			},
			OnPaymentFailure: func(event x402.PaymentEvent) {
				log.Printf("[%s] payment failed: %v", name, event.Error)
			},
		},
	}

	// Wrap with body buffering middleware. X402Transport.RoundTrip clones the
	// request after a 402, but http.Request.Clone() shares the same body reader.
	// By buffering the body and setting GetBody before the reverse proxy sees it,
	// Clone() can re-create the body for the retry attempt.
	return bodyBufferMiddleware(rp), nil
}

// bodyBufferMiddleware reads the incoming request body into memory and sets
// GetBody so any downstream Clone() calls can re-read it. This is required
// for X402Transport's 402 retry logic.
func bodyBufferMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleStatus returns JSON with remaining auths and spend per upstream.
func (p *Proxy) handleStatus(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type upstreamStatus struct {
		URL       string `json:"url"`
		Remaining int    `json:"remaining"`
		Spent     int    `json:"spent"`
		Network   string `json:"network"`
	}

	result := make(map[string]upstreamStatus)
	for name, signer := range p.signers {
		us := p.upstreams[name]
		result[name] = upstreamStatus{
			URL:       us.config.URL,
			Remaining: signer.Remaining(),
			Spent:     signer.Spent(),
			Network:   us.config.Network,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// singleJoiningSlash joins a base and suffix path with exactly one slash.
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
