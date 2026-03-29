package x402

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	x402lib "github.com/mark3labs/x402-go"
	x402http "github.com/mark3labs/x402-go/http"
	"github.com/prometheus/client_golang/prometheus"
)

// Verifier is a ForwardAuth-compatible HTTP handler that enforces x402
// micropayments on a per-route basis. Traefik sends every incoming request
// to /verify; the Verifier either returns 200 (allow) or 402 (pay-wall).
type Verifier struct {
	config       atomic.Pointer[PricingConfig]
	chain        atomic.Pointer[x402lib.ChainConfig]
	chains       atomic.Pointer[map[string]x402lib.ChainConfig] // pre-resolved: chain name → config
	registration atomic.Pointer[erc8004.AgentRegistration]
	metrics      *verifierMetrics
}

// NewVerifier creates a Verifier with the given initial configuration.
func NewVerifier(cfg *PricingConfig) (*Verifier, error) {
	v := &Verifier{metrics: newVerifierMetrics()}
	if err := v.load(cfg); err != nil {
		return nil, err
	}

	return v, nil
}

// Reload atomically swaps the pricing configuration.
func (v *Verifier) Reload(cfg *PricingConfig) error {
	return v.load(cfg)
}

func (v *Verifier) load(cfg *PricingConfig) error {
	chain, err := ResolveChain(cfg.Chain)
	if err != nil {
		return fmt.Errorf("resolve chain: %w", err)
	}

	// Pre-resolve all unique chain names (global + per-route overrides)
	// so HandleVerify avoids per-request chain resolution.
	chains := map[string]x402lib.ChainConfig{cfg.Chain: chain}
	for _, r := range cfg.Routes {
		if r.Network != "" {
			if _, ok := chains[r.Network]; !ok {
				rc, err := ResolveChain(r.Network)
				if err != nil {
					return fmt.Errorf("resolve chain for route %q: %w", r.Pattern, err)
				}

				chains[r.Network] = rc
			}
		}
	}

	v.chain.Store(&chain)
	v.chains.Store(&chains)
	v.config.Store(cfg)

	return nil
}

// HandleVerify is the ForwardAuth endpoint. Traefik forwards the original
// request headers; the Verifier inspects X-Forwarded-Uri to determine which
// pricing rule applies.
//
// Response semantics (ForwardAuth contract):
//   - 200: allow the request through to the backend
//   - 402: deny with x402 payment requirements in the response body
//   - 500: internal error (Traefik returns 500 to the client)
func (v *Verifier) HandleVerify(w http.ResponseWriter, r *http.Request) {
	uri := r.Header.Get("X-Forwarded-Uri")
	if uri == "" {
		// No forwarded URI — signals misconfiguration or direct access.
		// Fail-closed: deny rather than silently allowing through.
		log.Printf("x402-verifier: missing X-Forwarded-Uri header (misconfiguration or direct access)")
		http.Error(w, "forbidden: missing forwarded URI", http.StatusForbidden)

		return
	}

	cfg := v.config.Load()

	rule := matchRoute(cfg.Routes, uri)
	if rule == nil {
		// No pricing rule matches — route is free.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Per-route payTo and network override global config.
	wallet := cfg.Wallet
	if rule.PayTo != "" {
		wallet = rule.PayTo
	}

	chainName := cfg.Chain
	if rule.Network != "" {
		chainName = rule.Network
	}

	// Look up pre-resolved chain (populated during config load).
	chains := v.chains.Load()

	chain, ok := (*chains)[chainName]
	if !ok {
		log.Printf("x402-verifier: chain %q not pre-resolved for route %q", chainName, rule.Pattern)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	requirement, err := x402lib.NewUSDCPaymentRequirement(x402lib.USDCRequirementConfig{
		Chain:            chain,
		Amount:           rule.Price,
		RecipientAddress: wallet,
	})
	if err != nil {
		log.Printf("x402-verifier: failed to create payment requirement: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// Reconstruct the original request context so x402-go generates correct
	// payment requirements (resource URL, host, etc.).
	if host := r.Header.Get("X-Forwarded-Host"); host != "" {
		r.Host = host
	}

	r.URL.Path = uri

	r.RequestURI = uri
	if method := r.Header.Get("X-Forwarded-Method"); method != "" {
		r.Method = method
	}

	// Reuse x402-go's middleware wrapping a handler that returns 200.
	// When the inner handler runs (payment approved), it sets the Authorization
	// header if the route has upstreamAuth configured. Traefik's authResponseHeaders
	// copies this to the forwarded request, authenticating it with the upstream.
	labels := prometheusLabels(rule)
	v.metrics.requestsTotal.With(labels).Inc()

	middleware := x402http.NewX402Middleware(&x402http.Config{
		FacilitatorURL:      cfg.FacilitatorURL,
		PaymentRequirements: []x402lib.PaymentRequirement{requirement},
		VerifyOnly:          cfg.VerifyOnly,
	})

	upstreamAuth := rule.UpstreamAuth
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upstreamAuth != "" {
			w.Header().Set("Authorization", upstreamAuth)
		}

		w.WriteHeader(http.StatusOK)
	})

	tracker := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	middleware(inner).ServeHTTP(tracker, r)

	switch {
	case tracker.status == http.StatusOK && r.Header.Get("X-Payment") != "":
		v.metrics.paymentVerified.With(labels).Inc()
		v.metrics.chargedRequests.With(labels).Inc()
	case tracker.status == http.StatusPaymentRequired && r.Header.Get("X-Payment") != "":
		v.metrics.paymentFailed.With(labels).Inc()
	case tracker.status == http.StatusPaymentRequired:
		v.metrics.paymentRequired.With(labels).Inc()
	}
}

// HandleHealthz returns 200 OK for liveness probes.
func (v *Verifier) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ok"}`)
}

// HandleReadyz returns 200 OK if pricing config is loaded, 503 otherwise.
func (v *Verifier) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	if v.config.Load() == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ready"}`)
}

// SetRegistration atomically sets the ERC-8004 agent registration data
// served at /.well-known/agent-registration.json.
func (v *Verifier) SetRegistration(reg *erc8004.AgentRegistration) {
	v.registration.Store(reg)
}

// HandleWellKnown serves the ERC-8004 agent registration document.
func (v *Verifier) HandleWellKnown(w http.ResponseWriter, r *http.Request) {
	reg := v.registration.Load()
	if reg == nil {
		http.Error(w, "no registration configured", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reg) //nolint:errchkjson // controlled registration struct
}

// MetricsHandler exposes Prometheus metrics for the verifier.
func (v *Verifier) MetricsHandler() http.Handler {
	return v.metrics.handler()
}

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func prometheusLabels(rule *RouteRule) prometheus.Labels {
	return prometheus.Labels{
		"route":           rule.Pattern,
		"offer_namespace": rule.OfferNamespace,
		"offer_name":      rule.OfferName,
	}
}
