package x402

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// Verifier is a ForwardAuth-compatible HTTP handler that enforces x402
// micropayments on a per-route basis. Traefik sends every incoming request
// to /verify; the Verifier either returns 200 (allow) or 402 (pay-wall).
type Verifier struct {
	config  atomic.Pointer[PricingConfig]
	chain   atomic.Pointer[ChainInfo]
	chains  atomic.Pointer[map[string]ChainInfo] // pre-resolved: chain name → config
	metrics *verifierMetrics

	// routesLoaded is set true after the first route source apply completes.
	// Until then HandleReadyz returns 503 so kubelet keeps the pod out of
	// the Service Endpoints, preventing the "no rule -> 200 free pass"
	// window during informer warmup (CLAUDE.md pitfall #14).
	routesLoaded atomic.Bool

	// paidPrefixes is the list of URI prefixes the verifier KNOWS are
	// paid routes (derived from cfg.Routes patterns on each load). Used
	// by HandleVerify to fail-closed when a URI is under a paid prefix
	// but no rule matches — the alternative (200 → ForwardAuth allow)
	// would silently make the route free.
	//
	// Sorted by length descending so longer-prefix matches win first
	// (defensive — fixes nothing today but cheap insurance).
	paidPrefixes atomic.Pointer[[]string]

	// siwx authenticates gate:auth routes (EIP-4361 signatures + session
	// tokens). Sessions are signed with a per-process secret: a verifier
	// restart invalidates them (single-replica; clients re-authenticate).
	siwx *SIWXAuthenticator

	// quota tracks per-wallet free-tier usage on paid routes with a
	// freeQuota (in-memory, UTC-day windows).
	quota *freeQuotaCounter
}

// MarkRoutesLoaded signals that the route source has produced its first
// non-error apply. Idempotent. After this, HandleReadyz returns 200
// once config is also loaded.
func (v *Verifier) MarkRoutesLoaded() { v.routesLoaded.Store(true) }

// NewVerifier creates a Verifier with the given initial configuration.
func NewVerifier(cfg *PricingConfig) (*Verifier, error) {
	siwx, err := NewSIWXAuthenticator(0, 0)
	if err != nil {
		return nil, err
	}
	v := &Verifier{metrics: newVerifierMetrics(), siwx: siwx, quota: newFreeQuotaCounter()}
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

	// Pre-resolve all unique chain names (global + every per-route payment
	// option's network) so HandleVerify avoids per-request chain resolution.
	chains := map[string]ChainInfo{cfg.Chain: chain}
	for i := range cfg.Routes {
		for _, opt := range cfg.Routes[i].PaymentOptions() {
			if opt.Network == "" {
				continue
			}
			if _, ok := chains[opt.Network]; ok {
				continue
			}
			rc, err := ResolveChainInfo(opt.Network)
			if err != nil {
				return fmt.Errorf("resolve chain for route %q: %w", cfg.Routes[i].Pattern, err)
			}
			chains[opt.Network] = rc
		}
	}

	v.chain.Store(&chain)
	v.chains.Store(&chains)
	v.config.Store(cfg)

	// Derive paid-prefix tracker from the route patterns. HandleVerify
	// uses this to fail-closed when a URI is under a tracked prefix but
	// no rule matches (see isUnderPaidPrefix for the rationale).
	prefixes := make([]string, 0, len(cfg.Routes))
	seenPrefix := make(map[string]struct{}, len(cfg.Routes))
	addPrefix := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seenPrefix[p]; ok {
			return
		}
		seenPrefix[p] = struct{}{}
		prefixes = append(prefixes, p)
	}
	for _, r := range cfg.Routes {
		// Free carve-outs never establish a paid prefix: a wildcard free
		// route (e.g. /services/foo/jobs/*) must not make unmatched
		// siblings fail closed.
		if r.IsFree() {
			continue
		}
		addPrefix(patternToPrefix(r.Pattern))
		// Fail-closed for exact-only route tables. A wildcard route yields
		// a paid prefix above, but an offer whose non-free routes are ALL
		// exact (e.g. only `/services/foo/submit`) would register none, so
		// an undeclared sibling like `/services/foo/other` — which matched
		// no rule — would 200-free-pass in ForwardAuth mode, contradicting
		// the route-table contract that undeclared paths are refused once
		// any route is declared. Register the offer base as a paid prefix
		// so those siblings fail closed. Declared routes (free/auth/paid)
		// still return from matchRoute before this prefix is ever consulted.
		if base := strings.TrimSuffix(r.StripPrefix, "/"); base != "" {
			addPrefix(base + "/")
		}
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	v.paidPrefixes.Store(&prefixes)

	// Drop metric series for offers that are no longer in the route set.
	// Without this, deleting an offer leaves its counters + last-success
	// gauge in the registry forever, polluting dashboards and silently
	// keeping alerts (e.g. "no settlements after challenge") tied to dead
	// labels.
	live := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.OfferNamespace == "" && r.OfferName == "" {
			continue
		}
		// A multi-payment offer emits one metric series per (chain, asset)
		// it accepts, so register every option's labels — otherwise the
		// prune step below would drop the non-primary series. Build the key
		// from labelsForPaymentOption so it byte-matches the emitted labels
		// (incl. the "unknown" asset fallback).
		for _, opt := range r.PaymentOptions() {
			l := labelsForPaymentOption(r, opt)
			live[l["offer_namespace"]+"\x00"+l["offer_name"]+"\x00"+l["chain"]+"\x00"+l["asset_symbol"]] = struct{}{}
		}
	}
	v.metrics.pruneSeriesNotIn(live)

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

	// Match on the path alone: Traefik forwards the full request URI, and
	// "/rpc?method=x" must hit the "/rpc" rule, not free-pass around it.
	uri = stripQueryFragment(uri)

	cfg := v.config.Load()

	rule := matchRoute(cfg.Routes, uri)
	if rule == nil {
		// Check if this URI is under a tracked paid prefix. If yes,
		// the route was supposed to match but didn't — fail closed
		// rather than silently make it free (Traefik ForwardAuth 200
		// means "allow").
		if v.isUnderPaidPrefix(uri) {
			log.Printf("x402-verifier: URI %q is under a paid prefix but no rule matches — fail closed", uri)
			http.Error(w, "no rule matches; route appears to be a paid prefix with stale or missing rule", http.StatusForbidden)

			return
		}
		// Not under any paid prefix — legitimately free route.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Declared free carve-out (offer route table, gate: free): allow the
	// request through, still authenticating it with the upstream when the
	// offer has upstream auth configured. Identity headers are set to
	// empty so Traefik's authResponseHeaders overwrite any client-forged
	// values on the forwarded request.
	if rule.IsFree() {
		if rule.UpstreamAuth != "" {
			w.Header().Set("Authorization", rule.UpstreamAuth)
		}
		w.Header().Set(HeaderVerifiedWallet, "")
		w.Header().Set(HeaderPaymentPayer, "")
		w.WriteHeader(http.StatusOK)
		return
	}

	// SIWX-gated carve-out (gate: auth): a verified wallet substitutes for
	// payment; the wallet is handed to the upstream via authResponseHeaders.
	if rule.IsAuth() {
		wallet, err := v.siwx.Authenticate(r, requestHost(r), time.Now())
		if err != nil {
			v.writeSIWXChallenge(w, r, rule, err)
			return
		}
		if rule.UpstreamAuth != "" {
			w.Header().Set("Authorization", rule.UpstreamAuth)
		}
		w.Header().Set(HeaderVerifiedWallet, wallet)
		w.Header().Set(HeaderPaymentPayer, "")
		w.WriteHeader(http.StatusOK)
		return
	}

	mr, ok := v.resolvePaidRoute(cfg, rule)
	if !ok {
		// A rule matched but none of its payment options resolve to a
		// known chain — a paid route we cannot price. Fail closed.
		log.Printf("x402-verifier: rule %q matched but no payment option resolves — fail closed", rule.Pattern)
		http.Error(w, "route is payment-gated but has no resolvable payment option", http.StatusForbidden)
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
	primaryLabels := mr.labels
	v.metrics.requestsTotal.With(primaryLabels).Inc()

	// The HTML 402 page shows the primary payment option; the JSON accepts[]
	// (mr.requirements) carries every option for programmatic buyers. Rich
	// multi-option rendering on the human page is deferred to the storefront.
	primary := mr.requirements[0]
	display := buildPaymentDisplay(mr.rule, mr.chain, mr.asset, primary.PayTo, primary.Amount)

	// matchedLabels is updated to the option the buyer actually pays with, so
	// revenue/failure metrics attribute to the right (chain, asset).
	matchedLabels := primaryLabels
	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL:      cfg.FacilitatorURL,
		VerifyOnly:          cfg.VerifyOnly,
		Extensions:          mr.extensions,
		SendPaymentRequired: NewHTMLAwarePaymentRequired(display),
		OnPaymentMatched:    func(req x402types.PaymentRequirements) { matchedLabels = mr.labelsForMatched(req) },
		OnPaymentFailure: func(reason string) {
			v.metrics.paymentFailureReasons.With(withReason(matchedLabels, reason)).Inc()
		},
		// Propagate the verified payer to the upstream. Set even when
		// empty: Traefik's authResponseHeaders only overwrite forwarded
		// request headers when present on the auth response, so an
		// always-set header is what neutralizes client-forged values.
		OnPaymentVerified: func(payer string) {
			w.Header().Set(HeaderPaymentPayer, payer)
		},
	}, mr.requirements)

	upstreamAuth := mr.rule.UpstreamAuth
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upstreamAuth != "" {
			w.Header().Set("Authorization", upstreamAuth)
		}
		if w.Header().Get(HeaderPaymentPayer) == "" {
			w.Header().Set(HeaderPaymentPayer, "")
		}
		w.Header().Set(HeaderVerifiedWallet, "")

		w.WriteHeader(http.StatusOK)
	})

	tracker := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	middleware(inner).ServeHTTP(tracker, r)

	switch {
	case tracker.status == http.StatusOK && r.Header.Get("X-Payment") != "":
		v.metrics.paymentVerified.With(matchedLabels).Inc()
		v.metrics.chargedRequests.With(matchedLabels).Inc()
		v.metrics.lastPaymentSuccess.With(matchedLabels).SetToCurrentTime()
	case tracker.status == http.StatusPaymentRequired && r.Header.Get("X-Payment") != "":
		v.metrics.paymentFailed.With(matchedLabels).Inc()
	case tracker.status == http.StatusPaymentRequired:
		v.metrics.paymentRequired.With(primaryLabels).Inc()
	}
}

// HandleProxy serves the seller-owned paid route directly. It matches the
// incoming path to a ServiceOffer-derived route rule, verifies the payment,
// proxies to the real upstream, and settles only after the upstream succeeds.
func (v *Verifier) HandleProxy(w http.ResponseWriter, r *http.Request) {
	cfg := v.config.Load()

	// The identity headers are verifier-set facts; client-supplied values
	// must never reach an upstream — on any route class.
	stripIdentityHeaders(r.Header)

	// Verifier-served sign-in endpoints (<offer>/auth[/verify]) for offers
	// with gate:auth routes — handled before route matching so they never
	// proxy or gate.
	if v.handleAuthEndpoints(w, r) {
		return
	}

	rule := matchRoute(cfg.Routes, r.URL.Path)
	if rule == nil {
		writeErrorResponse(w, r, http.StatusNotFound, "No service here",
			"This path is not part of any published service. The operator's catalog lists every live endpoint.")
		return
	}
	// Carry the matched rule so 402 resource.URL builders can map the
	// Traefik-rewritten internal path back to the public dedicated-origin path.
	r = r.WithContext(withRouteRule(r.Context(), rule))

	// Declared free carve-out: proxy straight to the upstream, no payment
	// middleware. buildUpstreamProxy still injects upstream auth and strips
	// the offer's route prefix, so free and paid routes reach the upstream
	// identically shaped.
	if rule.IsFree() {
		proxy, err := buildUpstreamProxy(rule)
		if err != nil {
			log.Printf("x402-verifier: build upstream proxy for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
			writeErrorResponse(w, r, http.StatusInternalServerError, "Upstream unavailable",
				"The service behind this route is not reachable right now. Retry shortly.")
			return
		}
		// Opportunistic identity: free routes don't REQUIRE a credential,
		// but a valid one still yields X-Verified-Wallet — this is how the
		// broker's job listing and payer-gated results see who's asking.
		// Invalid or non-SIWX credentials (e.g. a jobToken bearer the
		// broker itself checks) pass through anonymous.
		if wallet, err := v.siwx.Authenticate(r, requestHost(r), time.Now()); err == nil {
			r.Header.Set(HeaderVerifiedWallet, wallet)
		}
		proxy.ServeHTTP(&statusRecorder{ResponseWriter: w, status: http.StatusOK}, r)
		return
	}

	// SIWX-gated carve-out: verified wallet substitutes for payment; the
	// wallet rides to the upstream as X-Verified-Wallet.
	if rule.IsAuth() {
		wallet, err := v.siwx.Authenticate(r, requestHost(r), time.Now())
		if err != nil {
			v.writeSIWXChallenge(w, r, rule, err)
			return
		}
		proxy, err := buildUpstreamProxy(rule)
		if err != nil {
			log.Printf("x402-verifier: build upstream proxy for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
			writeErrorResponse(w, r, http.StatusInternalServerError, "Upstream unavailable",
				"The service behind this route is not reachable right now. Retry shortly.")
			return
		}
		r.Header.Set(HeaderVerifiedWallet, wallet)
		proxy.ServeHTTP(&statusRecorder{ResponseWriter: w, status: http.StatusOK}, r)
		return
	}

	mr, ok := v.resolvePaidRoute(cfg, rule)
	if !ok {
		// Paid route with no resolvable payment option — fail closed, and
		// loudly: silently 404ing here is the CAIP-2/legacy chain-form
		// mismatch failure mode (CLAUDE.md pitfall #10).
		log.Printf("x402-verifier: rule %q matched but no payment option resolves — fail closed", rule.Pattern)
		writeErrorResponse(w, r, http.StatusForbidden, "Route misconfigured",
			"route is payment-gated but has no resolvable payment option")
		return
	}

	primaryLabels := mr.labels
	v.metrics.requestsTotal.With(primaryLabels).Inc()

	proxy, err := buildUpstreamProxy(mr.rule)
	if err != nil {
		log.Printf("x402-verifier: build upstream proxy for %s/%s: %v", mr.rule.OfferNamespace, mr.rule.OfferName, err)
		http.Error(w, "upstream unavailable", http.StatusInternalServerError)
		return
	}

	// Free tier: a paid route with a freeQuota lets a SIWX-verified wallet
	// ride free until its daily allowance is spent, then falls through to
	// the normal 402. The wallet is forwarded so upstreams (and the async
	// broker) still see an owner identity on free-tier calls.
	if mr.rule.FreeQuota > 0 {
		if wallet, err := v.siwx.Authenticate(r, requestHost(r), time.Now()); err == nil &&
			v.quota.consume(mr.rule.Pattern, wallet, mr.rule.FreeQuota, time.Now()) {
			r.Header.Set(HeaderVerifiedWallet, wallet)
			r.Header.Set(HeaderPaymentPayer, wallet)
			proxy.ServeHTTP(&statusRecorder{ResponseWriter: w, status: http.StatusOK}, r)
			return
		}
	}

	primary := mr.requirements[0]
	display := buildPaymentDisplay(mr.rule, mr.chain, mr.asset, primary.PayTo, primary.Amount)

	matchedLabels := primaryLabels
	paymentFailed := false
	middleware := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: cfg.FacilitatorURL,
		// HandleProxy is the in-process seller gateway: it proxies to the real
		// upstream and settles only after a <400 response, so verifyOnly=false
		// is correct here. SettlesInProcess suppresses the (otherwise
		// per-request) verifyOnly=false warning on this safe path.
		VerifyOnly:          false,
		SettlesInProcess:    true,
		Extensions:          mr.extensions,
		SendPaymentRequired: NewHTMLAwarePaymentRequired(display),
		OnPaymentMatched:    func(req x402types.PaymentRequirements) { matchedLabels = mr.labelsForMatched(req) },
		OnPaymentFailure: func(reason string) {
			paymentFailed = true
			v.metrics.paymentFailureReasons.With(withReason(matchedLabels, reason)).Inc()
		},
		// Hand the verified payer to the upstream (client-supplied copies
		// were stripped at the top of HandleProxy). Upstreams use it to
		// bind created resources (e.g. async jobs) to the paying wallet.
		OnPaymentVerified: func(payer string) {
			if payer != "" {
				r.Header.Set(HeaderPaymentPayer, payer)
			}
		},
	}, mr.requirements)

	// A payment can arrive under either wire header: X-PAYMENT (v1) or
	// PAYMENT-SIGNATURE (v2). Gate the funnel metrics on both, else every
	// successful v2 payment (the cohort #689 unblocked) goes unmetered.
	hadPayment := r.Header.Get("X-PAYMENT") != "" || r.Header.Get("PAYMENT-SIGNATURE") != ""
	tracker := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	middleware(proxy).ServeHTTP(tracker, r)

	switch {
	case tracker.status == http.StatusPaymentRequired && !hadPayment:
		v.metrics.paymentRequired.With(primaryLabels).Inc()
	case tracker.status == http.StatusPaymentRequired:
		v.metrics.paymentFailed.With(matchedLabels).Inc()
	case tracker.status < http.StatusBadRequest && hadPayment:
		v.metrics.paymentVerified.With(matchedLabels).Inc()
		if tracker.Header().Get("X-PAYMENT-RESPONSE") != "" {
			v.metrics.chargedRequests.With(matchedLabels).Inc()
			v.metrics.lastPaymentSuccess.With(matchedLabels).SetToCurrentTime()
		}
	case tracker.status >= http.StatusBadRequest && hadPayment && !paymentFailed:
		// Payment verified, upstream errored, no settlement — the buyer was
		// bounced by the seller's own service, not by the payment flow.
		v.metrics.upstreamFailedAfterVerify.With(matchedLabels).Inc()
	}
}

// HandleHealthz returns 200 OK for liveness probes.
func (v *Verifier) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ok"}`)
}

// HandleReadyz returns 200 OK once BOTH pricing config and the first route
// source apply have completed. Until then it returns 503 with a cause-specific
// body so kubelet keeps the pod out of Service Endpoints, preventing the
// "no rule -> 200 free pass" window during informer warmup
// (CLAUDE.md pitfall #14).
func (v *Verifier) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	if v.config.Load() == nil {
		http.Error(w, "not ready: config not loaded", http.StatusServiceUnavailable)
		return
	}
	if !v.routesLoaded.Load() {
		http.Error(w, "not ready: routes not loaded", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, `{"status":"ready"}`)
}

// MetricsHandler exposes Prometheus metrics for the verifier.
func (v *Verifier) MetricsHandler() http.Handler {
	return v.metrics.handler()
}

// matchedRoute is the resolved view of a paid route for one request: the
// rule, the full x402 accepts[] (one PaymentRequirements per accepted payment
// option, requirements[0] = primary), the top-level extensions, and the
// primary option's chain/asset/labels for the HTML 402 display + default
// metrics. optionLabels is parallel to requirements for per-option metric
// attribution once the buyer picks one.
type matchedRoute struct {
	rule         *RouteRule
	requirements []x402types.PaymentRequirements
	optionLabels []prometheus.Labels
	extensions   map[string]any
	chain        ChainInfo
	asset        AssetInfo
	labels       prometheus.Labels
}

// withReason copies a route's metric labels and adds the failure-stage
// reason label for the paymentFailureReasons counter.
func withReason(labels prometheus.Labels, reason string) prometheus.Labels {
	out := make(prometheus.Labels, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out["reason"] = reason
	return out
}

// labelsForMatched returns the metric labels for the payment option the buyer
// actually satisfied, matching by the same fields findMatchingRequirementV1
// uses. Falls back to the primary option's labels if no match is found.
func (m *matchedRoute) labelsForMatched(req x402types.PaymentRequirements) prometheus.Labels {
	for i := range m.requirements {
		r := m.requirements[i]
		if r.Network == req.Network && r.Asset == req.Asset && r.PayTo == req.PayTo && r.Amount == req.Amount {
			return m.optionLabels[i]
		}
	}
	return m.labels
}

// resolvePaidRoute resolves a matched paid rule's full set of accepted
// payment options into x402 PaymentRequirements. Returns nil,false when none
// of the rule's options resolve to a known chain — callers fail closed on
// that (the route is declared paid but cannot be priced).
func (v *Verifier) resolvePaidRoute(cfg *PricingConfig, rule *RouteRule) (*matchedRoute, bool) {
	chains := v.chains.Load()
	opts := rule.PaymentOptions()
	reqs := make([]x402types.PaymentRequirements, 0, len(opts))
	optLabels := make([]prometheus.Labels, 0, len(opts))
	var primaryChain ChainInfo
	var primaryAsset AssetInfo
	var extensions map[string]any
	for i := range opts {
		opt := opts[i]
		chainName := cfg.Chain
		if opt.Network != "" {
			chainName = opt.Network
		}
		chain, ok := (*chains)[chainName]
		if !ok {
			// Skip this option rather than failing the whole route — other
			// options may still be payable. (load() pre-resolves every
			// option's network, so this is defensive.)
			log.Printf("x402-verifier: chain %q not pre-resolved for route %q option %d", chainName, rule.Pattern, i)
			continue
		}
		wallet := cfg.Wallet
		if opt.PayTo != "" {
			wallet = opt.PayTo
		}
		asset := ResolveAssetInfoForPayment(chain, opt)
		req, err := BuildV2RequirementWithAsset(chain, asset, opt.Price, wallet, opt.MaxTimeoutSeconds)
		if err != nil {
			// Skip this option rather than serving it free or panicking —
			// a malformed stored price (e.g. a ServiceOffer applied
			// directly via kubectl, bypassing CLI validation) must never
			// produce a payable route. Other options may still be payable;
			// if none are, the len(reqs)==0 check below fails the route
			// closed.
			log.Printf("x402-verifier: invalid price %q for route %q option %d: %v", opt.Price, rule.Pattern, i, err)
			continue
		}
		mergeAgentExtras(&req, rule)
		reqs = append(reqs, req)
		optLabels = append(optLabels, labelsForPaymentOption(rule, opt))
		if len(reqs) == 1 {
			primaryChain = chain
			primaryAsset = asset
		}
		// Advertise gasless approve at the top level if ANY accepted option
		// supports it (e.g. an OBOL/Permit2 option alongside USDC). The buyer
		// only takes the permit flow when paying with the supporting asset.
		if extensions == nil {
			if ext := BuildExtensionsForAsset(asset); ext != nil {
				extensions = ext
			}
		}
	}
	if len(reqs) == 0 {
		return nil, false
	}
	extensions = WithBazaar(extensions, rule.OfferType, rule.Model)
	return &matchedRoute{
		rule:         rule,
		requirements: reqs,
		optionLabels: optLabels,
		extensions:   extensions,
		chain:        primaryChain,
		asset:        primaryAsset,
		labels:       optLabels[0],
	}, true
}

// isUnderPaidPrefix reports whether uri starts with any of the URI
// prefixes the verifier knows are paid routes. Used by HandleVerify
// to fail-closed when matchRoute returns nil but the URI is still
// under a tracked prefix — i.e. the route was supposed to match but
// didn't (stale route table, code bug, etc.).
func (v *Verifier) isUnderPaidPrefix(uri string) bool {
	prefixes := v.paidPrefixes.Load()
	if prefixes == nil {
		return false
	}
	for _, p := range *prefixes {
		if strings.HasPrefix(uri, p) {
			return true
		}
	}
	return false
}

// patternToPrefix converts a route Pattern like "/services/foo/*"
// into a directory-style prefix "/services/foo/" suitable for
// strings.HasPrefix matching. Returns "" for patterns without a
// trailing glob — exact-match patterns aren't paid prefixes, so
// fail-closed only applies to the broader "any URI under this path"
// semantic. The trailing slash is preserved so HasPrefix
// distinguishes /services/foo/ from /services/foobar/.
func patternToPrefix(pattern string) string {
	if !strings.HasSuffix(pattern, "/*") {
		return ""
	}
	return strings.TrimSuffix(pattern, "*")
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
		Endpoint:         rule.Pattern,
		Network:          chain.Name,
		NetworkLabel:     humanizeNetwork(chain.Name),
		AssetSymbol:      asset.Symbol,
		AssetAddress:     asset.Address,
		PriceDisplay:     FormatPriceDisplay(atomicAmount, asset.Decimals, asset.Symbol),
		PriceAtomic:      atomicAmount,
		PayToFull:        payTo,
		ExplorerURL:      explorerAddressURL(chain.Name, payTo),
		OfferType:        rule.OfferType,
		OfferName:        rule.OfferName,
		OfferDescription: rule.Description,
		AgentModel:       rule.AgentModel,
		Model:            rule.Model,
		AgentSkills:      append([]string(nil), rule.AgentSkills...),
		BrandingPatch:    rule.Branding,
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
	// Async rules proxy to the job broker instead of the upstream; the
	// broker learns everything it needs (real upstream, offer identity,
	// retention, visibility, public prefix) from contract headers the
	// verifier sets here — client-supplied copies are stripped at the
	// HandleProxy entry, so the broker may trust them. Header names are
	// mirrored in internal/jobbroker (not imported: that would pull the
	// broker's SQLite driver into the verifier binary).
	targetURL := rule.UpstreamURL
	if rule.Async && rule.BrokerURL != "" {
		targetURL = rule.BrokerURL
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", targetURL, err)
	}

	// Shared secret for the verifier→broker HMAC (F1 defense in depth over
	// the NetworkPolicy). Read once here; empty disables signing, and the
	// broker likewise only enforces when its own copy is set.
	brokerHMACSecret := os.Getenv("JOB_BROKER_HMAC_SECRET")

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			strippedPath := stripRoutePrefix(rule.StripPrefix, pr.In.URL.Path)
			strippedPath = normalizeChatCompletionsPath(rule.OfferType, pr.In.Method, strippedPath)
			pr.Out.URL.Path = singleJoiningSlash(target.Path, strippedPath)
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			// The verifier is the browser-facing boundary ONLY on routes it
			// actually gates (payment / SIWX): it authenticates the request
			// and re-issues it upstream under its own authority, so browser
			// fetch-context headers must not leak through — an upstream with
			// its own origin allowlist (hermes's API server 403s any Origin
			// it has not allowlisted, and browsers attach Origin to every
			// POST) would otherwise reject paid browser requests after the
			// payment already verified. gate:free routes are the opposite:
			// the verifier performs no auth and doesn't own CSRF for them,
			// so stripping Origin here would instead break the upstream's
			// own Origin-based CSRF defense.
			if !rule.IsFree() {
				pr.Out.Header.Del("Origin")
				pr.Out.Header.Del("Sec-Fetch-Site")
				pr.Out.Header.Del("Sec-Fetch-Mode")
				pr.Out.Header.Del("Sec-Fetch-Dest")
				pr.Out.Header.Del("Sec-Fetch-User")
			}
			if rule.Async && rule.BrokerURL != "" {
				pr.Out.Header.Set(headerBrokerUpstreamURL, rule.UpstreamURL)
				pr.Out.Header.Set(headerBrokerOffer, rule.OfferNamespace+"/"+rule.OfferName)
				pr.Out.Header.Set(headerBrokerPayTo, rule.PayTo)
				pr.Out.Header.Set(headerBrokerJobTTL, rule.AsyncTTL)
				pr.Out.Header.Set(headerBrokerVisibility, rule.AsyncVisibility)
				pr.Out.Header.Set(headerBrokerPublicPrefix, publicPrefix(rule, requestHost(pr.In)))
				// The broker replays with this on the upstream request;
				// the client's own Authorization (SIWX / jobToken bearer)
				// must keep flowing through for result access.
				pr.Out.Header.Set(headerBrokerUpstreamAuth, rule.UpstreamAuth)
				if brokerHMACSecret != "" {
					pr.Out.Header.Set(headerBrokerSig,
						brokerSignature(brokerHMACSecret, rule.UpstreamURL,
							rule.OfferNamespace+"/"+rule.OfferName, rule.UpstreamAuth))
				}
			} else if rule.UpstreamAuth != "" {
				pr.Out.Header.Set("Authorization", rule.UpstreamAuth)
			}
			injectAgentPayerContext(pr, rule)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("x402-verifier: upstream proxy error for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	return proxy, nil
}

// chatCompletionsPath is the OpenAI-compatible path served by inference and
// agent upstreams (LiteLLM, Hermes).
const chatCompletionsPath = "/v1/chat/completions"

// payerContextBodyLimit caps how much of a chat-completions request body the
// verifier buffers to splice in the payer-context system message. Larger
// bodies pass through byte-identical.
const payerContextBodyLimit = 4 << 20

// injectAgentPayerContext rewrites a paid agent-offer chat request so the
// verified payer wallet rides IN-BAND as a leading system message. The
// X-Payment-Payer header alone doesn't reach the model — agent runtimes drop
// HTTP headers before the LLM sees the conversation — so without this a buyer
// must repeat their own address in the prompt. Identity headers are
// verifier-set only (client copies are stripped at HandleProxy entry), so the
// injected value is an authenticated fact.
//
// Injection is strictly best-effort: any shape we don't fully understand
// (non-JSON, no messages array, oversized or unreadable body, compressed
// content) leaves the request byte-identical rather than risking a paid
// request.
func injectAgentPayerContext(pr *httputil.ProxyRequest, rule *RouteRule) {
	if rule.OfferType != "agent" || pr.Out.Method != http.MethodPost ||
		pr.Out.URL.Path != chatCompletionsPath || pr.Out.Body == nil {
		return
	}
	if ce := pr.In.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return
	}
	if ct := pr.In.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		return
	}
	payer := pr.In.Header.Get(HeaderPaymentPayer)
	if payer == "" {
		payer = pr.In.Header.Get(HeaderVerifiedWallet)
	}
	if payer == "" {
		return
	}

	orig := pr.Out.Body
	body, err := io.ReadAll(io.LimitReader(orig, payerContextBodyLimit+1))
	if err != nil || len(body) > payerContextBodyLimit {
		// Splice the consumed bytes back in front of the unread remainder so
		// the upstream still receives the full original body.
		pr.Out.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), orig), orig}
		return
	}
	orig.Close()

	newBody, ok := splicePayerSystemMessage(body, payer)
	if !ok {
		newBody = body
	}
	pr.Out.Body = io.NopCloser(bytes.NewReader(newBody))
	pr.Out.ContentLength = int64(len(newBody))
	pr.Out.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
}

// splicePayerSystemMessage prepends a system message carrying the verified
// payer wallet to an OpenAI chat-completions JSON body. Returns the original
// bytes and false when the body isn't the expected shape.
func splicePayerSystemMessage(body []byte, payer string) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	rawMsgs, ok := doc["messages"]
	if !ok {
		return body, false
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &messages); err != nil {
		return body, false
	}
	note, err := json.Marshal(map[string]string{
		"role": "system",
		"content": "x402 payment context: this request was paid by wallet " + payer +
			" (payer identity verified on-chain by the payment gateway; buyers cannot spoof it). " +
			"When the buyer refers to their own wallet or address without spelling it out, use this address.",
	})
	if err != nil {
		return body, false
	}
	merged, err := json.Marshal(append([]json.RawMessage{note}, messages...))
	if err != nil {
		return body, false
	}
	doc["messages"] = merged
	newBody, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return newBody, true
}

// normalizeChatCompletionsPath forgives the common wrong-path shapes buyers
// send to chat-completions offers. External x402 clients (and the prompts on
// older 402 pages) frequently POST to the bare service base or to
// /chat/completions; the upstream only serves /v1/chat/completions, so a
// verified, paid request would otherwise 404. For inference/agent offers the
// tolerated shapes are rewritten to the canonical path; every other sub-path
// (e.g. /v1/embeddings) passes through untouched, as do all non-POST methods
// and non-chat offer types.
func normalizeChatCompletionsPath(offerType, method, stripped string) string {
	if method != http.MethodPost {
		return stripped
	}
	switch offerType {
	case "inference", "agent":
	default:
		return stripped
	}
	switch strings.TrimSuffix(stripped, "/") {
	case "", "/v1", "/chat/completions":
		return chatCompletionsPath
	}
	return stripped
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

// Flush proxies through to the underlying ResponseWriter's Flush, when
// supported. Without this, embedding the http.ResponseWriter interface
// hides the concrete writer's Flush from settlementInterceptor's
// `i.w.(http.Flusher)` type assertion — SSE streams then sit in the
// buffer until the handler returns, breaking `obol sell agent` for any
// OpenAI-style streaming chat frontend. See TestVerifier_HandleProxy_StreamsSSEChunks.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying ResponseWriter so WebSocket upgrades
// and any future protocol-switching paths keep working through the
// settlement wrapper. Same rationale as Flush above.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}

func prometheusLabels(rule *RouteRule) prometheus.Labels {
	// `route` (= rule.Pattern) was dropped in favor of (offer_namespace,
	// offer_name) which already uniquely identifies a paid route — the
	// pattern was redundant and unbounded by path fragments, which would
	// have ballooned series count for sellers running many granular routes.
	//
	// asset_symbol is included for direct per-token aggregation in PromQL
	// (e.g. "what's my OBOL revenue?") without having to join the metric
	// against the ServiceOffer CR at query time. Cardinality cost is zero
	// because each offer pins exactly one asset — the new dimension is
	// functionally constant within the existing (ns, name) group.
	// The inline fields describe the primary payment option; delegate so
	// primary and per-option labels share one definition.
	return labelsForPaymentOption(rule, RoutePayment{Network: rule.Network, AssetSymbol: rule.AssetSymbol})
}

// labelsForPaymentOption builds the metric label set for one accepted payment
// option of a route. A multi-payment offer emits one series per option so
// revenue/charges attribute to the actual (chain, asset) the buyer used.
func labelsForPaymentOption(rule *RouteRule, opt RoutePayment) prometheus.Labels {
	asset := opt.AssetSymbol
	if asset == "" {
		// Defensive: a missing symbol is operationally ugly in PromQL.
		// Empty-string labels are legal in Prometheus but render as a
		// bare "asset_symbol=" in selectors, which makes dashboard
		// filters harder to write. "unknown" is unambiguous and matches
		// the convention we use elsewhere for under-populated metadata.
		asset = "unknown"
	}
	return prometheus.Labels{
		"offer_namespace": rule.OfferNamespace,
		"offer_name":      rule.OfferName,
		"chain":           opt.Network,
		"asset_symbol":    asset,
	}
}
