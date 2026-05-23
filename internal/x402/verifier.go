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

	rule, requirement, extensions, _, chain, asset, ok := v.matchPaidRouteFull(cfg, uri)
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

	wallet := cfg.Wallet
	if rule.PayTo != "" {
		wallet = rule.PayTo
	}
	display := buildPaymentDisplay(rule, chain, asset, wallet, requirement.Amount)

	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL:      cfg.FacilitatorURL,
		VerifyOnly:          cfg.VerifyOnly,
		Extensions:          extensions,
		SendPaymentRequired: NewHTMLAwarePaymentRequired(display),
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
		v.metrics.lastPaymentSuccess.With(labels).SetToCurrentTime()
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

	rule, requirement, extensions, labels, chain, asset, ok := v.matchPaidRouteFull(cfg, r.URL.Path)
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

	wallet := cfg.Wallet
	if rule.PayTo != "" {
		wallet = rule.PayTo
	}
	display := buildPaymentDisplay(rule, chain, asset, wallet, requirement.Amount)

	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL:      cfg.FacilitatorURL,
		VerifyOnly:          false,
		Extensions:          extensions,
		SendPaymentRequired: NewHTMLAwarePaymentRequired(display),
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
			v.metrics.lastPaymentSuccess.With(labels).SetToCurrentTime()
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

func (v *Verifier) matchPaidRoute(cfg *PricingConfig, uri string) (*RouteRule, x402types.PaymentRequirements, map[string]any, prometheus.Labels, bool) {
	rule, req, ext, labels, _, _, ok := v.matchPaidRouteFull(cfg, uri)
	return rule, req, ext, labels, ok
}

// matchPaidRouteFull is matchPaidRoute plus the resolved chain and asset,
// which the HTML 402 renderer needs for display copy. Internal-only.
func (v *Verifier) matchPaidRouteFull(cfg *PricingConfig, uri string) (*RouteRule, x402types.PaymentRequirements, map[string]any, prometheus.Labels, ChainInfo, AssetInfo, bool) {
	rule := matchRoute(cfg.Routes, uri)
	if rule == nil {
		return nil, x402types.PaymentRequirements{}, nil, nil, ChainInfo{}, AssetInfo{}, false
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
		return nil, x402types.PaymentRequirements{}, nil, nil, ChainInfo{}, AssetInfo{}, false
	}

	asset := ResolveAssetInfo(chain, rule)
	requirement := BuildV2RequirementWithAsset(chain, asset, rule.Price, wallet)
	mergeAgentExtras(&requirement, rule)
	extensions := BuildExtensionsForAsset(asset)
	return rule, requirement, extensions, prometheusLabels(rule), chain, asset, true
}

// mergeAgentExtras adds the agent fields from a RouteRule to the
// requirement's Extra map so buyers probing a 402 see which model and
// skills are powering the offer. No-op for non-agent rules.
func mergeAgentExtras(req *x402types.PaymentRequirements, rule *RouteRule) {
	if rule.AgentModel == "" && len(rule.AgentSkills) == 0 && rule.AgentRuntime == "" {
		return
	}
	if req.Extra == nil {
		req.Extra = make(map[string]interface{})
	}
	if rule.AgentModel != "" {
		req.Extra["agentModel"] = rule.AgentModel
	}
	if len(rule.AgentSkills) > 0 {
		// Materialise as []any so JSON marshalling produces a proper array
		// regardless of whether the source loaded it from yaml or
		// constructed it in-memory.
		skills := make([]any, len(rule.AgentSkills))
		for i, s := range rule.AgentSkills {
			skills[i] = s
		}
		req.Extra["agentSkills"] = skills
	}
	if rule.AgentRuntime != "" {
		req.Extra["agentRuntime"] = rule.AgentRuntime
	}
}

// buildPaymentDisplay turns the matched rule + chain + asset into pre-formatted
// strings for the HTML 402 page. The atomic-amount input is the value already
// computed for the wire requirement (rule.Price * 10^decimals), so passing
// requirement.Amount keeps display and JSON in lockstep — the previous version
// fed the decimal rule.Price into formatAmount and produced the inverse
// (1 OBOL → 0.000000000000000001 OBOL).
func buildPaymentDisplay(rule *RouteRule, chain ChainInfo, asset AssetInfo, payTo, atomicAmount string) PaymentDisplay {
	return PaymentDisplay{
		Endpoint:     rule.Pattern,
		Network:      chain.Name,
		NetworkLabel: humanizeNetwork(chain.Name),
		AssetSymbol:  asset.Symbol,
		AssetAddress: asset.Address,
		PriceDisplay: FormatPriceDisplay(atomicAmount, asset.Decimals, asset.Symbol),
		PriceAtomic:  atomicAmount,
		PayToFull:    payTo,
		ExplorerURL:  explorerAddressURL(chain.Name, payTo),
	}
}

// explorerAddressURL returns the canonical block-explorer URL for the given
// recipient address on the given chain. Empty string when the chain isn't in
// the known set or the address looks malformed — callers must treat empty as
// "no explorer link, just show plain text".
func explorerAddressURL(network, addr string) string {
	if !strings.HasPrefix(addr, "0x") || len(addr) < 10 {
		return ""
	}
	base := ""
	switch network {
	case "base":
		base = "https://basescan.org"
	case "base-sepolia":
		base = "https://sepolia.basescan.org"
	case "ethereum", "mainnet":
		base = "https://etherscan.io"
	case "polygon":
		base = "https://polygonscan.com"
	case "polygon-amoy":
		base = "https://amoy.polygonscan.com"
	case "avalanche":
		base = "https://snowtrace.io"
	case "avalanche-fuji":
		base = "https://testnet.snowtrace.io"
	case "arbitrum":
		base = "https://arbiscan.io"
	case "arbitrum-sepolia":
		base = "https://sepolia.arbiscan.io"
	default:
		return ""
	}
	return base + "/address/" + addr
}

// humanizeNetwork converts an internal chain name ("base-sepolia") to the
// display label users actually recognise ("Base Sepolia"). Anything not in
// the table is returned title-cased best-effort.
func humanizeNetwork(name string) string {
	switch name {
	case "base":
		return "Base"
	case "base-sepolia":
		return "Base Sepolia"
	case "ethereum", "mainnet":
		return "Ethereum"
	case "polygon":
		return "Polygon"
	case "polygon-amoy":
		return "Polygon Amoy"
	case "avalanche":
		return "Avalanche"
	case "avalanche-fuji":
		return "Avalanche Fuji"
	case "arbitrum":
		return "Arbitrum One"
	case "arbitrum-sepolia":
		return "Arbitrum Sepolia"
	}
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
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
		"chain":           rule.Network,
	}
}
