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
	"time"

	x402types "github.com/coinbase/x402/go/types"
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
	reloadCh    chan struct{}
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
		reloadCh:    make(chan struct{}, 1),
	}

	p.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	p.mux.HandleFunc("GET /status", p.handleStatus)
	p.mux.HandleFunc("POST /admin/reload", p.handleAdminReload)
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
		Transport: &replayableX402Transport{
			Base:     http.DefaultTransport,
			Signers:  []Signer{signer},
			Selector: NewDefaultPaymentSelector(),
			OnPaymentAttempt: func(event PaymentEvent) {
				p.metrics.paymentAttempts.With(labels).Inc()
			},
			OnPaymentFailure: func(event PaymentEvent) {
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

// replayableX402Transport mirrors the x402-go retry flow, but rebuilds the
// request body from GetBody for each attempt so retries stay valid under
// httputil.ReverseProxy on newer Go versions.
type replayableX402Transport struct {
	Base             http.RoundTripper
	Signers          []Signer
	Selector         PaymentSelector
	OnPaymentAttempt PaymentCallback
	OnPaymentSuccess PaymentCallback
	OnPaymentFailure PaymentCallback
}

func (t *replayableX402Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Base == nil {
		t.Base = http.DefaultTransport
	}
	if err := ensureReplayableBody(req); err != nil {
		return nil, err
	}

	firstReq, err := cloneRequestWithFreshBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := t.Base.RoundTrip(firstReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		return resp, nil
	}

	requirements, err := parsePaymentRequirements(resp)
	if err != nil {
		resp.Body.Close()
		return nil, NewPaymentError(ErrCodeInvalidRequirements, "failed to parse payment requirements", err)
	}
	resp.Body.Close()

	payment, err := t.Selector.SelectAndSign(requirements, t.Signers)
	if err != nil {
		return nil, err
	}

	var selectedRequirement *x402types.PaymentRequirements
	for i := range requirements {
		if requirements[i].Network == payment.Accepted.Network &&
			requirements[i].Scheme == payment.Accepted.Scheme &&
			requirements[i].Amount == payment.Accepted.Amount &&
			requirements[i].Asset == payment.Accepted.Asset &&
			requirements[i].PayTo == payment.Accepted.PayTo {
			selectedRequirement = &requirements[i]
			break
		}
	}

	startTime := time.Now()
	if t.OnPaymentAttempt != nil && selectedRequirement != nil {
		t.OnPaymentAttempt(PaymentEvent{
			Type:      PaymentEventAttempt,
			Timestamp: startTime,
			Method:    "HTTP",
			URL:       req.URL.String(),
			Network:   payment.Accepted.Network,
			Scheme:    payment.Accepted.Scheme,
			Amount:    selectedRequirement.Amount,
			Asset:     selectedRequirement.Asset,
			Recipient: selectedRequirement.PayTo,
		})
	}

	paymentHeader, err := EncodePayment(*payment)
	if err != nil {
		if t.OnPaymentFailure != nil {
			t.OnPaymentFailure(PaymentEvent{
				Type:      PaymentEventFailure,
				Timestamp: time.Now(),
				Method:    "HTTP",
				URL:       req.URL.String(),
				Error:     err,
				Duration:  time.Since(startTime),
			})
		}
		return nil, NewPaymentError(ErrCodeSigningFailed, "failed to build payment header", err)
	}

	retryReq, err := cloneRequestWithFreshBody(req)
	if err != nil {
		return nil, err
	}
	retryReq.Header.Set("X-PAYMENT", paymentHeader)

	respRetry, err := t.Base.RoundTrip(retryReq)
	duration := time.Since(startTime)
	if err != nil {
		if t.OnPaymentFailure != nil {
			t.OnPaymentFailure(PaymentEvent{
				Type:      PaymentEventFailure,
				Timestamp: time.Now(),
				Method:    "HTTP",
				URL:       req.URL.String(),
				Error:     err,
				Duration:  duration,
			})
		}
		return nil, err
	}

	settlement, _ := DecodeSettlement(respRetry.Header.Get("X-PAYMENT-RESPONSE"))
	if settlement.Success && t.OnPaymentSuccess != nil {
		event := PaymentEvent{
			Type:        PaymentEventSuccess,
			Timestamp:   time.Now(),
			Method:      "HTTP",
			URL:         req.URL.String(),
			Transaction: settlement.Transaction,
			Payer:       settlement.Payer,
			Duration:    duration,
		}
		if selectedRequirement != nil {
			event.Network = selectedRequirement.Network
			event.Scheme = selectedRequirement.Scheme
			event.Amount = selectedRequirement.Amount
			event.Asset = selectedRequirement.Asset
			event.Recipient = selectedRequirement.PayTo
		}
		t.OnPaymentSuccess(event)
	}

	return respRetry, nil
}

func ensureReplayableBody(req *http.Request) error {
	if req.Body == nil || req.Body == http.NoBody || req.GetBody != nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
}

func cloneRequestWithFreshBody(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	switch {
	case req.Body == nil:
		clone.Body = nil
	case req.Body == http.NoBody:
		clone.Body = http.NoBody
	default:
		if req.GetBody == nil {
			return nil, fmt.Errorf("request body is not replayable")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("clone request body: %w", err)
		}
		clone.Body = body
		clone.ContentLength = req.ContentLength
	}
	return clone, nil
}

func parsePaymentRequirements(resp *http.Response) ([]x402types.PaymentRequirements, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var paymentReqResp struct {
		X402Version int `json:"x402Version"`
		Accepts     []struct {
			Scheme            string                 `json:"scheme"`
			Network           string                 `json:"network"`
			MaxAmountRequired string                 `json:"maxAmountRequired,omitempty"`
			Amount            string                 `json:"amount,omitempty"`
			Asset             string                 `json:"asset"`
			PayTo             string                 `json:"payTo"`
			MaxTimeoutSeconds int                    `json:"maxTimeoutSeconds"`
			Extra             map[string]interface{} `json:"extra,omitempty"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(body, &paymentReqResp); err != nil {
		return nil, fmt.Errorf("failed to parse payment requirements JSON: %w", err)
	}
	if len(paymentReqResp.Accepts) == 0 {
		return nil, fmt.Errorf("no payment requirements in response")
	}

	requirements := make([]x402types.PaymentRequirements, len(paymentReqResp.Accepts))
	for i, req := range paymentReqResp.Accepts {
		amount := req.Amount
		if amount == "" {
			amount = req.MaxAmountRequired
		}
		requirements[i] = x402types.PaymentRequirements{
			Scheme:            req.Scheme,
			Network:           req.Network,
			Amount:            amount,
			Asset:             req.Asset,
			PayTo:             req.PayTo,
			MaxTimeoutSeconds: req.MaxTimeoutSeconds,
			Extra:             req.Extra,
		}
	}

	return requirements, nil
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

func (p *Proxy) handleAdminReload(w http.ResponseWriter, _ *http.Request) {
	select {
	case p.reloadCh <- struct{}{}:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"reload triggered"}`)
	default:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"reload already pending"}`)
	}
}

func (p *Proxy) ReloadCh() <-chan struct{} {
	return p.reloadCh
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
