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
// Routing:
//   - OpenAI-compatible chat/responses paths resolve the upstream from the
//     requested model.
//   - /upstream/<name>/... remains available for compatibility.
type Proxy struct {
	mu          sync.RWMutex
	signers     map[string]*PreSignedSigner
	upstreams   map[string]*upstreamEntry
	modelRoutes map[string]string
	mux         *http.ServeMux
	metrics     *metrics
	state       *StateStore
}

type upstreamEntry struct {
	name        string
	remoteModel string
	config      UpstreamConfig
	handler     http.Handler
}

type upstreamStatus struct {
	URL         string `json:"url"`
	RemoteModel string `json:"remote_model,omitempty"`
	PublicModel string `json:"public_model,omitempty"`
	Remaining   int    `json:"remaining"`
	Spent       int    `json:"spent"`
	Network     string `json:"network"`
}

// NewProxy creates a proxy from the given config and auth pools.
func NewProxy(cfg *Config, auths AuthsFile, state *StateStore) (*Proxy, error) {
	if state == nil {
		state = &StateStore{}
	}

	if state.consumed == nil {
		state.consumed = make(map[string]map[string]struct{})
	}

	p := &Proxy{
		signers:     make(map[string]*PreSignedSigner),
		upstreams:   make(map[string]*upstreamEntry),
		modelRoutes: make(map[string]string),
		mux:         http.NewServeMux(),
		metrics:     newMetrics(),
		state:       state,
	}

	p.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	p.mux.HandleFunc("GET /status", p.handleStatus)
	p.mux.Handle("GET /metrics", p.metrics.handler())
	registerOpenAIRoutes(p.mux, p.handleModelRequest)

	if err := p.Reload(cfg, auths); err != nil {
		return nil, err
	}

	return p, nil
}

// Reload atomically rebuilds the upstream handlers from config/auth sources.
func (p *Proxy) Reload(cfg *Config, auths AuthsFile) error {
	newSigners := make(map[string]*PreSignedSigner)
	newUpstreams := make(map[string]*upstreamEntry)
	newModelRoutes := make(map[string]string)

	for name, upstream := range cfg.Upstreams {
		authPool := auths[name]

		filtered := make([]*PreSignedAuth, 0, len(authPool))
		for _, auth := range authPool {
			if auth == nil || p.state.IsConsumed(name, auth.Nonce) {
				continue
			}

			filtered = append(filtered, auth)
		}

		if len(filtered) == 0 {
			log.Printf("WARNING: upstream %q has 0 remaining pre-signed auths", name)
		}

		remoteModel := normalizeRemoteModel(upstream.RemoteModel)
		if remoteModel == "" {
			remoteModel = normalizeRemoteModel(name)
		}

		signer := NewPreSignedSigner(
			upstream.Network,
			upstream.PayTo,
			upstream.Asset,
			upstream.Price,
			filtered,
			p.state.ConsumedCount(name),
			func(auth *PreSignedAuth) error {
				return p.state.MarkConsumed(name, auth.Nonce)
			},
		)

		handler, err := p.buildUpstreamHandler(name, remoteModel, upstream, signer)
		if err != nil {
			return fmt.Errorf("build handler for %q: %w", name, err)
		}

		newSigners[name] = signer
		newUpstreams[name] = &upstreamEntry{
			name:        name,
			remoteModel: remoteModel,
			config:      upstream,
			handler:     handler,
		}
		newModelRoutes[remoteModel] = name
	}

	p.mu.Lock()
	p.signers = newSigners
	p.upstreams = newUpstreams
	p.modelRoutes = newModelRoutes
	p.syncCompatibilityRoutesLocked()
	p.syncMetricsLocked()
	p.mu.Unlock()

	return nil
}

// ServeHTTP dispatches to the internal mux.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mux.ServeHTTP(w, r)
}

func (p *Proxy) syncCompatibilityRoutesLocked() {
	p.mux = http.NewServeMux()
	p.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	p.mux.HandleFunc("GET /status", p.handleStatus)
	p.mux.Handle("GET /metrics", p.metrics.handler())
	registerOpenAIRoutes(p.mux, p.handleModelRequest)

	for name, upstream := range p.upstreams {
		prefix := fmt.Sprintf("/upstream/%s/", name)
		p.mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), upstream.handler))
	}
}

func (p *Proxy) syncMetricsLocked() {
	p.metrics.authRemaining.Reset()
	p.metrics.authSpent.Reset()
	p.metrics.activeModelMappings.Reset()

	for name, upstream := range p.upstreams {
		signer := p.signers[name]
		labels := prometheusLabels(name, upstream.remoteModel)
		p.metrics.activeModelMappings.With(labels).Set(1)
		p.metrics.authRemaining.With(labels).Set(float64(signer.Remaining()))
		p.metrics.authSpent.With(labels).Set(float64(signer.Spent()))
	}
}

func prometheusLabels(name, remoteModel string) map[string]string {
	return map[string]string{
		"upstream":     name,
		"remote_model": remoteModel,
	}
}

// buildUpstreamHandler creates a reverse proxy for one upstream with X402Transport.
// It wraps the handler with body buffering so X402Transport can replay requests
// after receiving a 402 response.
func (p *Proxy) buildUpstreamHandler(name, remoteModel string, cfg UpstreamConfig, signer *PreSignedSigner) (http.Handler, error) {
	target, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", cfg.URL, err)
	}

	labels := prometheusLabels(name, remoteModel)
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = singleJoiningSlash(target.Path, pr.In.URL.Path)
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp != nil && resp.Request != nil && resp.Request.Header.Get("X-Payment") != "" {
				switch {
				case resp.StatusCode < http.StatusBadRequest:
					p.metrics.paymentSuccessTotal.With(labels).Inc()
				default:
					p.metrics.paymentFailureTotal.With(labels).Inc()
				}

				p.metrics.authRemaining.With(labels).Set(float64(signer.Remaining()))
				p.metrics.authSpent.With(labels).Set(float64(signer.Spent()))
			}

			return nil
		},
		Transport: &x402http.X402Transport{
			Base:     http.DefaultTransport,
			Signers:  []x402.Signer{signer},
			Selector: x402.NewDefaultPaymentSelector(),
			OnPaymentAttempt: func(event x402.PaymentEvent) {
				p.metrics.paymentAttempts.With(labels).Inc()
			},
			OnPaymentFailure: func(event x402.PaymentEvent) {
				p.metrics.paymentFailureTotal.With(labels).Inc()
				p.metrics.authRemaining.With(labels).Set(float64(signer.Remaining()))
				p.metrics.authSpent.With(labels).Set(float64(signer.Spent()))
				log.Printf("[%s] payment failed: %v", name, event.Error)
			},
		},
	}

	return bodyBufferMiddleware(rp), nil
}

func (p *Proxy) handleModelRequest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	r.Body.Close()

	remoteModel, rewrittenBody, entry := p.resolveModelRequest(body)
	if entry == nil {
		http.Error(w, "no purchased upstream mapped for requested model", http.StatusNotFound)
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(rewrittenBody))
	r.ContentLength = int64(len(rewrittenBody))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewrittenBody)), nil
	}

	labels := prometheusLabels(entry.name, remoteModel)
	p.metrics.requestsTotal.With(labels).Inc()
	entry.handler.ServeHTTP(w, r)
}

func registerOpenAIRoutes(mux *http.ServeMux, handler http.HandlerFunc) {
	for _, route := range []string{
		"POST /v1/chat/completions",
		"POST /chat/completions",
		"POST /v1/responses",
		"POST /responses",
	} {
		mux.HandleFunc(route, handler)
	}
}

func (p *Proxy) resolveModelRequest(body []byte) (string, []byte, *upstreamEntry) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, nil
	}

	modelValue, _ := payload["model"].(string)

	remoteModel := normalizeRemoteModel(modelValue)
	if remoteModel == "" {
		return "", nil, nil
	}

	p.mu.RLock()
	upstreamName, ok := p.modelRoutes[remoteModel]
	entry := p.upstreams[upstreamName]
	p.mu.RUnlock()

	if !ok || entry == nil {
		return "", nil, nil
	}

	payload["model"] = remoteModel

	rewrittenBody, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil
	}

	return remoteModel, rewrittenBody, entry
}

func normalizeRemoteModel(model string) string {
	normalized := strings.TrimSpace(model)

	for {
		switch {
		case strings.HasPrefix(normalized, "paid/"):
			normalized = strings.TrimPrefix(normalized, "paid/")
		case strings.HasPrefix(normalized, "openai/"):
			normalized = strings.TrimPrefix(normalized, "openai/")
		default:
			return normalized
		}
	}
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

	result := make(map[string]upstreamStatus)

	for name, signer := range p.signers {
		us := p.upstreams[name]
		result[name] = upstreamStatus{
			URL:         us.config.URL,
			RemoteModel: us.remoteModel,
			PublicModel: "paid/" + us.remoteModel,
			Remaining:   signer.Remaining(),
			Spent:       signer.Spent(),
			Network:     us.config.Network,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result) //nolint:errchkjson // controlled status map
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
