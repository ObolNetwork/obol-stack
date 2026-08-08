package serviceoffercontroller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"k8s.io/apimachinery/pkg/types"
)

// maxUpstreamOpenAPIBytes caps both the fetched body and the re-marshaled
// document. The rewritten doc lands in the shared "obol-skill-md" ConfigMap
// alongside every other offer's bundle, which is subject to Kubernetes'
// ~1MiB ConfigMap size limit; a single oversized upstream doc would brick
// static-site publishing for every offer, not just its own. 200KiB leaves
// ample headroom for many offers to coexist.
const maxUpstreamOpenAPIBytes = 200 * 1024

var upstreamOpenAPIClient = &http.Client{
	Timeout: 3 * time.Second,
	// Never follow redirects: the target is offer-author-controlled
	// (service/namespace/port/path) and the fetched document is republished
	// publicly, so a redirect must not be able to silently retarget the
	// fetch.
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// tryUpstreamOpenAPI is the seam tests replace. It performs the actual
// network fetch; callers that need determinism across a slow/flapping
// upstream should go through upstreamOpenAPICache instead of calling this
// directly.
var tryUpstreamOpenAPI = fetchUpstreamOpenAPI

// offerHasProbeableUpstream reports whether this offer could ever serve an
// upstream OpenAPI document. Agent and inference offers describe their own
// wire format, and an offer with no upstream Service has nothing to probe.
// For those a nil fetch is TERMINAL — there is no point retrying — whereas
// for every other offer a nil is a transient miss. upstreamOpenAPICache.refresh
// relies on that distinction to decide what it may cache.
func offerHasProbeableUpstream(offer *monetizeapi.ServiceOffer) bool {
	return offer != nil && !offer.IsAgent() && !offer.IsInference() &&
		strings.TrimSpace(offer.Spec.Upstream.Service) != ""
}

func fetchUpstreamOpenAPI(offer *monetizeapi.ServiceOffer) map[string]any {
	if !offerHasProbeableUpstream(offer) {
		return nil
	}
	base := upstreamOpenAPIBase(offer)
	for _, path := range upstreamOpenAPIPathCandidates(offer) {
		doc, err := getJSONMap(base + path)
		if err != nil || doc == nil {
			continue
		}
		paths, _ := doc["paths"].(map[string]any)
		if len(paths) == 0 {
			continue
		}
		return doc
	}
	return nil
}

// upstreamOpenAPIBase builds the fetch target for one offer's own upstream
// service. Deliberately offer.Namespace, not EffectiveNamespace():
// Upstream.Namespace is offer-author-controlled, and the Gateway API data
// path only trusts a cross-namespace target once a ReferenceGrant
// authorizes it. This controller-side probe has no such check, so it must
// never leave the offer's own namespace.
func upstreamOpenAPIBase(offer *monetizeapi.ServiceOffer) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		offer.Spec.Upstream.Service,
		offer.Namespace,
		offer.EffectivePort(),
	)
}

func upstreamOpenAPIPathCandidates(offer *monetizeapi.ServiceOffer) []string {
	var out []string
	if offer.Spec.Registration.Metadata != nil {
		if p := strings.TrimSpace(offer.Spec.Registration.Metadata["openapiPath"]); p != "" && isSimpleUpstreamPath(p) {
			out = append(out, p)
		}
	}
	out = append(out, "/v1/openapi.json", "/openapi.json")
	return out
}

// isSimpleUpstreamPath rejects anything but a plain, '/'-prefixed path: no
// traversal segments and no embedded scheme/authority that could redirect
// the request off the intended upstream once concatenated onto base.
func isSimpleUpstreamPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "://") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func getJSONMap(url string) (map[string]any, error) {
	resp, err := upstreamOpenAPIClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamOpenAPIBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpstreamOpenAPIBytes {
		return nil, fmt.Errorf("upstream openapi document exceeds %d bytes", maxUpstreamOpenAPIBytes)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// upstreamOpenAPICacheEntry is one offer's last-fetched result.
type upstreamOpenAPICacheEntry struct {
	generation int64
	doc        map[string]any
}

// upstreamOpenAPICache holds the last-good upstream fetch per offer, keyed
// by UID. refresh is called from an offer's own reconcile (outside
// staticSiteMu) at most once per observed generation; get is read-only and
// is all buildOfferBundles ever calls. This keeps a slow or flapping
// upstream from blocking, or flip-flopping the content hash of, the shared
// static-site rebuild that runs for every offer on every offer's reconcile.
type upstreamOpenAPICache struct {
	mu      sync.Mutex
	entries map[types.UID]upstreamOpenAPICacheEntry
}

// get returns the cached doc, or nil if no fetch has completed yet for this
// offer's current generation.
func (c *upstreamOpenAPICache) get(offer *monetizeapi.ServiceOffer) map[string]any {
	doc, _ := c.getSettled(offer)
	return doc
}

// getSettled is get plus whether the cache has a SETTLED answer for this
// offer — i.e. a fetch has completed at least once. A nil doc means two very
// different things and callers that render discovery documents must tell them
// apart:
//
//   - settled=true, doc=nil  → we probed and there is no upstream document.
//     The route-table fallback is the correct, final answer.
//   - settled=false          → we have not probed yet (the controller just
//     started, or this offer has not reconciled since). Rendering the fallback
//     here would publish a thinner document than the one already being served.
//
// The cache is process-local, so every controller restart begins unsettled for
// every offer.
func (c *upstreamOpenAPICache) getSettled(offer *monetizeapi.ServiceOffer) (map[string]any, bool) {
	if offer == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[offer.UID]
	return entry.doc, ok
}

// refresh fetches (via fetch) and caches the result, but only when the
// offer's generation has moved on from what's cached — a requeue with no
// spec change (e.g. the 5s convergence retry, a tunnel URL change) reuses
// the last-good result instead of hitting the upstream again.
//
// A failed probe on an offer that could serve a document is NOT cached; see
// the comment at the nil check below. The cache holds last-GOOD results, so a
// transient miss must not be allowed to pin the degraded fallback.
func (c *upstreamOpenAPICache) refresh(offer *monetizeapi.ServiceOffer, fetch func(*monetizeapi.ServiceOffer) map[string]any) {
	if offer == nil {
		return
	}
	c.mu.Lock()
	cached, ok := c.entries[offer.UID]
	c.mu.Unlock()
	if ok && cached.generation == offer.Generation {
		return
	}
	doc := fetch(offer)
	if doc == nil && offerHasProbeableUpstream(offer) {
		// A miss on an offer that COULD serve a document is transient — the
		// upstream may still be rolling out, or the probe's short timeout may
		// simply have been tight. Recording it would pin this generation to the
		// route-table fallback until someone edits the CR, and because
		// reconcileStaticSite rebuilds the shared bundle from this cache on
		// every offer's reconcile, that one miss would also overwrite a good
		// document for the whole stack. Leave the generation unrecorded so the
		// next reconcile retries, and keep any last-good doc: stale beats
		// silently collapsed. Offers that can never serve one (agent,
		// inference, no upstream Service) still cache their nil below, so they
		// are probed once per generation rather than on every reconcile.
		return
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[types.UID]upstreamOpenAPICacheEntry{}
	}
	c.entries[offer.UID] = upstreamOpenAPICacheEntry{generation: offer.Generation, doc: doc}
	c.mu.Unlock()
}

// forget drops a cache entry (offer deleted).
func (c *upstreamOpenAPICache) forget(uid types.UID) {
	c.mu.Lock()
	delete(c.entries, uid)
	c.mu.Unlock()
}

func rewriteUpstreamOpenAPI(doc map[string]any, offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) (string, bool) {
	if doc == nil {
		return "", false
	}
	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	origin := offer.EffectiveOrigin()
	out["servers"] = []any{map[string]any{"url": origin}}
	info, _ := out["info"].(map[string]any)
	if info == nil {
		info = map[string]any{}
	} else {
		copied := map[string]any{}
		for k, v := range info {
			copied[k] = v
		}
		info = copied
	}
	if title := strings.TrimSpace(offer.Spec.Registration.Name); title != "" {
		info["title"] = title
	}
	if desc := strings.TrimSpace(offer.Spec.Registration.Description); desc != "" {
		info["description"] = desc
	}
	if email := strings.TrimSpace(profile.ContactEmail); email != "" {
		info["contact"] = map[string]any{"name": strings.TrimSpace(profile.DisplayName), "email": email}
	}
	out["info"] = info
	if _, has := out["x-discovery"]; !has {
		out["x-discovery"] = map[string]any{
			"source": "upstream-openapi",
			"note":   "Full first-party API surface; paid ops carry x-payment-info + 402 responses (x402scan OpenAPI-first).",
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(encoded) > maxUpstreamOpenAPIBytes {
		return "", false
	}
	return string(encoded), true
}

func buildOfferWellKnownX402FromOpenAPI(offer *monetizeapi.ServiceOffer, doc map[string]any) string {
	if doc == nil {
		return ""
	}
	origin := strings.TrimRight(offer.EffectiveOrigin(), "/")
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		return ""
	}
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)
	defaultAccepts := []any{}
	for _, p := range offer.EffectivePayments() {
		defaultAccepts = append(defaultAccepts, wellKnownAccept(p))
	}
	if len(defaultAccepts) == 0 {
		return ""
	}
	var resources []any
	for _, p := range pathKeys {
		item, _ := paths[p].(map[string]any)
		if item == nil {
			continue
		}
		for _, m := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
			op, _ := item[m].(map[string]any)
			if op == nil || !openAPIOpIsPaid(op) {
				continue
			}
			desc, _ := op["summary"].(string)
			if desc == "" {
				desc, _ = op["description"].(string)
			}
			if desc == "" {
				desc = offerDescription(offer, "x402 payment-gated service.")
			}
			accepts := defaultAccepts
			if price, ok := routePriceOverride(offer, m, p); ok {
				payment := offer.EffectivePayments()[0]
				payment.Price = price
				accepts = []any{wellKnownAccept(payment)}
			}
			resources = append(resources, map[string]any{
				"resource":    origin + joinOpenAPIPath("/", p),
				"type":        "http",
				"method":      strings.ToUpper(m),
				"description": desc,
				"x402Version": 2,
				"accepts":     accepts,
			})
		}
	}
	if len(resources) == 0 {
		return ""
	}
	if len(resources) > 200 {
		resources = resources[:200]
	}
	encoded, err := json.MarshalIndent(map[string]any{"x402Version": 2, "resources": resources}, "", "  ")
	if err != nil {
		return ""
	}
	return string(encoded)
}

// routePriceOverride returns the price of the most specific spec.routes[]
// entry that both matches the upstream path p and method, and carries a
// price override, mirroring how buildOfferWellKnownX402 (offerbundle.go)
// honors rt.HasPriceOverride()/rt.Price for the non-upstream document.
// Routes without an override are skipped so lookups fall through to the
// offer's default payments instead of pinning to a route that has nothing
// to override.
func routePriceOverride(offer *monetizeapi.ServiceOffer, method, p string) (monetizeapi.ServiceOfferPriceTable, bool) {
	routes := append([]monetizeapi.ServiceOfferRoute(nil), offer.EffectiveRoutes()...)
	sortRouteTableBySpecificity(routes)
	for _, rt := range routes {
		if !rt.HasPriceOverride() || !routeMethodMatches(rt.Methods, method) {
			continue
		}
		if openAPIRoutePatternMatches(rt.Path, p) {
			return rt.Price, true
		}
	}
	return monetizeapi.ServiceOfferPriceTable{}, false
}

// routeMethodMatches reports whether method (any case) is among the route's
// declared methods; an empty list means the route applies to every method.
func routeMethodMatches(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// openAPIRoutePatternMatches mirrors matchPattern in internal/x402/matcher.go
// (unexported there, so duplicated here): exact match, a trailing "/*"
// greedy prefix, or path.Match segment globs. Both packages interpret
// spec.routes[].path against the same offer-rooted path-world, so this must
// agree with what the verifier actually gates.
func openAPIRoutePatternMatches(pattern, p string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == p
	}
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok && !strings.Contains(prefix, "*") {
		return p == prefix || strings.HasPrefix(p, prefix+"/")
	}
	patParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pParts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(pParts) < len(patParts) {
		return false
	}
	for i, pp := range patParts {
		if i == len(patParts)-1 && pp == "*" {
			return true
		}
		if i >= len(pParts) {
			return false
		}
		if matched, err := path.Match(pp, pParts[i]); err != nil || !matched {
			return false
		}
	}
	return len(pParts) == len(patParts)
}

func openAPIOpIsPaid(op map[string]any) bool {
	if op == nil {
		return false
	}
	if gate, _ := op["x-gate"].(string); gate == "free" {
		return false
	}
	if _, hasPay := op["x-payment-info"]; hasPay {
		return true
	}
	if sec, ok := op["security"].([]any); ok && len(sec) == 0 {
		return false
	}
	if responses, ok := op["responses"].(map[string]any); ok {
		if _, has402 := responses["402"]; has402 {
			return true
		}
	}
	return false
}

func buildOfferAgentRegistration(offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) string {
	origin := strings.TrimRight(offer.EffectiveOrigin(), "/")
	name := strings.TrimSpace(offer.Spec.Registration.Name)
	if name == "" {
		name = offer.Name
	}
	desc := strings.TrimSpace(offer.Spec.Registration.Description)
	if desc == "" {
		desc = offerDescription(offer, "x402 payment-gated service.")
	}
	image := strings.TrimSpace(offer.Spec.Registration.Image)
	if image == "" {
		if logo := strings.TrimSpace(profile.LogoURL); logo != "" && (strings.HasPrefix(logo, "http://") || strings.HasPrefix(logo, "https://")) {
			image = logo
		} else {
			image = origin + "/agent-icon.png"
		}
	}
	services := []erc8004.ServiceDef{{Name: "web", Endpoint: origin, Version: "1.0.0"}}
	if len(offer.Spec.Registration.Services) > 0 {
		var scoped []erc8004.ServiceDef
		for _, s := range offer.Spec.Registration.Services {
			ep := strings.TrimSpace(s.Endpoint)
			if ep == "" {
				continue
			}
			if strings.Contains(ep, "://") && ep != origin && !strings.HasPrefix(ep, origin+"/") {
				continue
			}
			scoped = append(scoped, erc8004.ServiceDef{
				Name:     defaultString(s.Name, "web"),
				Endpoint: ep,
				Version:  defaultString(s.Version, "1.0.0"),
			})
		}
		if len(scoped) > 0 {
			services = scoped
		}
	}
	if len(offer.Spec.Registration.Skills) > 0 || len(offer.Spec.Registration.Domains) > 0 {
		services = append(services, erc8004.ServiceDef{
			Name: "OASF", Version: "0.8",
			Skills: offer.Spec.Registration.Skills, Domains: offer.Spec.Registration.Domains,
		})
	}
	doc := erc8004.AgentRegistration{
		Type: erc8004.RegistrationType, Name: name, Description: desc, Image: image,
		Active: offer.Spec.Registration.Enabled, X402Support: true, Services: services,
		SupportedTrust: offer.Spec.Registration.SupportedTrust,
	}
	// ERC-8004 requires registrations[] to have >=1 entry once the offer is
	// on-chain (status.AgentID). Buyers verifying --expected-agent-id
	// (internal/buy/discover.go) fail closed without it.
	if agentID := strings.TrimSpace(offer.Status.AgentID); agentID != "" {
		registry := fmt.Sprintf("eip155:%d:%s", erc8004.BaseSepoliaChainID, erc8004.IdentityRegistryBaseSepolia)
		if net, err := erc8004.ResolveNetwork(offer.Spec.Payment.Network); err == nil {
			registry = net.CAIP10Registry()
		}
		doc.Registrations = []erc8004.OnChainReg{{
			AgentID:       parseInt64(agentID),
			AgentRegistry: registry,
		}}
	}
	if meta := nonEmptyStringMap(offer.Spec.Registration.Metadata); len(meta) > 0 {
		doc.Metadata = meta
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return `{"type":"` + erc8004.RegistrationType + `","name":"` + name + `","active":false,"services":[]}`
	}
	return string(encoded)
}
