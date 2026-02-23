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
)

// Verifier is a ForwardAuth-compatible HTTP handler that enforces x402
// micropayments on a per-route basis. Traefik sends every incoming request
// to /verify; the Verifier either returns 200 (allow) or 402 (pay-wall).
type Verifier struct {
	config       atomic.Pointer[PricingConfig]
	chain        atomic.Pointer[x402lib.ChainConfig]
	registration atomic.Pointer[erc8004.AgentRegistration]
}

// NewVerifier creates a Verifier with the given initial configuration.
func NewVerifier(cfg *PricingConfig) (*Verifier, error) {
	v := &Verifier{}
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
	v.chain.Store(&chain)
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
		// No forwarded URI — shouldn't happen in ForwardAuth. Allow through.
		w.WriteHeader(http.StatusOK)
		return
	}

	cfg := v.config.Load()
	rule := matchRoute(cfg.Routes, uri)
	if rule == nil {
		// No pricing rule matches — route is free.
		w.WriteHeader(http.StatusOK)
		return
	}

	chain := v.chain.Load()

	requirement, err := x402lib.NewUSDCPaymentRequirement(x402lib.USDCRequirementConfig{
		Chain:            *chain,
		Amount:           rule.Price,
		RecipientAddress: cfg.Wallet,
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

	// Reuse x402-go's middleware wrapping a dummy handler that returns 200.
	// The middleware either:
	//   - Returns 402 (no/invalid payment) — Traefik forwards this to the client
	//   - Calls the inner handler (valid payment) → 200 → Traefik allows the request
	middleware := x402http.NewX402Middleware(&x402http.Config{
		FacilitatorURL:      cfg.FacilitatorURL,
		PaymentRequirements: []x402lib.PaymentRequirement{requirement},
		VerifyOnly:          cfg.VerifyOnly,
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware(inner).ServeHTTP(w, r)
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
	json.NewEncoder(w).Encode(reg)
}
