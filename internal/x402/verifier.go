package x402

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	x402types "github.com/coinbase/x402/go/types"
	"github.com/prometheus/client_golang/prometheus"
)

// Verifier is a ForwardAuth-compatible HTTP handler that enforces x402
// micropayments on a per-route basis. Traefik sends every incoming request
// to /verify; the Verifier either returns 200 (allow) or 402 (pay-wall).
type Verifier struct {
	config  atomic.Pointer[PricingConfig]
	chain   atomic.Pointer[ChainInfo]
	chains  atomic.Pointer[map[string]ChainInfo] // pre-resolved: chain name → config
	metrics *verifierMetrics
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
	chain, err := ResolveChainInfo(cfg.Chain)
	if err != nil {
		return fmt.Errorf("resolve chain: %w", err)
	}

	// Pre-resolve all unique chain names (global + per-route overrides)
	// so HandleVerify avoids per-request chain resolution.
	chains := map[string]ChainInfo{cfg.Chain: chain}
	for _, r := range cfg.Routes {
		if r.Network != "" {
			if _, ok := chains[r.Network]; !ok {
				rc, err := ResolveChainInfo(r.Network)
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

	rule, requirement, _, ok := v.matchPaidRoute(cfg, uri)
	if !ok {
		// No pricing rule matches — route is free.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Reconstruct the original request context so the middleware generates
	// correct payment requirements (resource URL, host, etc.).
	if host := r.Header.Get("X-Forwarded-Host"); host != "" {
		r.Host = host
	}

	r.URL.Path = uri

	r.RequestURI = uri
	if method := r.Header.Get("X-Forwarded-Method"); method != "" {
		r.Method = method
	}

	// Use the local ForwardAuth middleware wrapping a handler that returns 200.
	// When the inner handler runs (payment approved), it sets the Authorization
	// header if the route has upstreamAuth configured. Traefik's authResponseHeaders
	// copies this to the forwarded request, authenticating it with the upstream.
	labels := prometheusLabels(rule)
	v.metrics.requestsTotal.With(labels).Inc()

	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: cfg.FacilitatorURL,
		VerifyOnly:     cfg.VerifyOnly,
	}, []x402types.PaymentRequirements{requirement})

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

// HandleProxy serves the seller-owned paid route directly. It matches the
// incoming path to a ServiceOffer-derived route rule, verifies the payment,
// proxies to the real upstream, and settles only after the upstream succeeds.
func (v *Verifier) HandleProxy(w http.ResponseWriter, r *http.Request) {
	cfg := v.config.Load()

	rule, requirement, labels, ok := v.matchPaidRoute(cfg, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	v.metrics.requestsTotal.With(labels).Inc()

	proxy, err := buildUpstreamProxy(rule)
	if err != nil {
		log.Printf("x402-verifier: build upstream proxy for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		http.Error(w, "upstream unavailable", http.StatusInternalServerError)
		return
	}

	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: cfg.FacilitatorURL,
		VerifyOnly:     false,
	}, []x402types.PaymentRequirements{requirement})

	hadPayment := r.Header.Get("X-PAYMENT") != ""
	tracker := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	middleware(proxy).ServeHTTP(tracker, r)

	switch {
	case tracker.status == http.StatusPaymentRequired && !hadPayment:
		v.metrics.paymentRequired.With(labels).Inc()
	case tracker.status == http.StatusPaymentRequired:
		v.metrics.paymentFailed.With(labels).Inc()
	case tracker.status < http.StatusBadRequest && hadPayment:
		v.metrics.paymentVerified.With(labels).Inc()
		if tracker.Header().Get("X-PAYMENT-RESPONSE") != "" {
			v.metrics.chargedRequests.With(labels).Inc()
		}
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

// MetricsHandler exposes Prometheus metrics for the verifier.
func (v *Verifier) MetricsHandler() http.Handler {
	return v.metrics.handler()
}

func (v *Verifier) matchPaidRoute(cfg *PricingConfig, uri string) (*RouteRule, x402types.PaymentRequirements, prometheus.Labels, bool) {
	rule := matchRoute(cfg.Routes, uri)
	if rule == nil {
		return nil, x402types.PaymentRequirements{}, nil, false
	}

	wallet := cfg.Wallet
	if rule.PayTo != "" {
		wallet = rule.PayTo
	}

	chainName := cfg.Chain
	if rule.Network != "" {
		chainName = rule.Network
	}

	chains := v.chains.Load()
	chain, ok := (*chains)[chainName]
	if !ok {
		log.Printf("x402-verifier: chain %q not pre-resolved for route %q", chainName, rule.Pattern)
		return nil, x402types.PaymentRequirements{}, nil, false
	}

	requirement := BuildV2Requirement(chain, rule.Price, wallet)
	return rule, requirement, prometheusLabels(rule), true
}

func buildUpstreamProxy(rule *RouteRule) (http.Handler, error) {
	target, err := url.Parse(rule.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", rule.UpstreamURL, err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			strippedPath := stripRoutePrefix(rule.StripPrefix, pr.In.URL.Path)
			pr.Out.URL.Path = singleJoiningSlash(target.Path, strippedPath)
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			if rule.UpstreamAuth != "" {
				pr.Out.Header.Set("Authorization", rule.UpstreamAuth)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("x402-verifier: upstream proxy error for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	return proxy, nil
}

func stripRoutePrefix(prefix, requestPath string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || prefix == "/" {
		if requestPath == "" {
			return "/"
		}
		return requestPath
	}

	switch {
	case requestPath == prefix:
		return "/"
	case strings.HasPrefix(requestPath, prefix+"/"):
		return strings.TrimPrefix(requestPath, prefix)
	default:
		return requestPath
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
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
