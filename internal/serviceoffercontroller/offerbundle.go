package serviceoffercontroller

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

// Per-offer discovery bundles for hostname-bound offers.
//
// The x402 market is origin-keyed: x402scan and agentcash crawl
// `GET <origin>/openapi.json` (falling back to /.well-known/x402) and group
// resources per origin. An offer with spec.hostname therefore gets its own
// discovery documents — an openapi.json scoped to just that offer with
// paths rooted at "/", a /.well-known/x402 resource list, and a minimal
// landing page — served by the same static-site httpd via per-offer ConfigMap
// keys and Exact-match rewrite routes on the offer's hostname.

// offerBundleFile is one generated file: Key is the ConfigMap data key,
// Path is where the httpd serves it under /www.
type offerBundleFile struct {
	Key     string
	Path    string
	Content string
}

// offerBundleDir returns the /www subdirectory for one offer's bundle.
func offerBundleDir(offer *monetizeapi.ServiceOffer) string {
	return fmt.Sprintf("offers/%s/%s", offer.Namespace, offer.Name)
}

func offerBundleKey(offer *monetizeapi.ServiceOffer, file string) string {
	return fmt.Sprintf("offer_%s_%s_%s", offer.Namespace, offer.Name, file)
}

// buildOfferBundles renders the discovery bundle for every hostname-bound,
// operationally-included offer. Deterministic order (bundle keys sorted) so
// content hashing is stable.
//
// upstreamOpenAPI supplies each offer's (possibly nil) upstream OpenAPI
// document. It must be a cache read, never a live fetch: this function runs
// under staticSiteMu on every offer's reconcile (see reconcileStaticSite),
// so a live HTTP call here would serialize every reconcile behind the
// slowest upstream and — on a flapping upstream — flip-flop the rebuilt
// content hash and roll the shared discovery pod. The production caller
// passes the Controller's upstreamOpenAPICache.get, refreshed independently
// from each offer's own reconcile.
func buildOfferBundles(offers []*monetizeapi.ServiceOffer, profile schemas.StorefrontProfile, upstreamOpenAPI func(*monetizeapi.ServiceOffer) map[string]any) []offerBundleFile {
	var bundles []offerBundleFile
	for _, offer := range offers {
		if offer == nil || offer.Spec.Hostname == "" {
			continue
		}
		// The dedicated origin carries its own identity: the offer's
		// branding block overrides the storefront profile field-wise
		// (empty fields inherit).
		originProfile := storefront.MergeProfile(profile, offer.Spec.Branding.ProfilePatch())
		upstreamDoc := upstreamOpenAPI(offer)
		openapiContent := buildOfferScopedOpenAPI(offer, originProfile)
		x402Content := buildOfferWellKnownX402(offer)
		if upstreamDoc != nil {
			if rewritten, ok := rewriteUpstreamOpenAPI(upstreamDoc, offer, originProfile); ok {
				openapiContent = rewritten
			}
			if expanded := buildOfferWellKnownX402FromOpenAPI(offer, upstreamDoc); expanded != "" {
				x402Content = expanded
			}
		}
		bundles = append(bundles,
			offerBundleFile{
				Key:     offerBundleKey(offer, "openapi.json"),
				Path:    offerBundleDir(offer) + "/openapi.json",
				Content: openapiContent,
			},
			offerBundleFile{
				Key:     offerBundleKey(offer, "x402.json"),
				Path:    offerBundleDir(offer) + "/x402.json",
				Content: x402Content,
			},
			offerBundleFile{
				Key:     offerBundleKey(offer, "agent-registration.json"),
				Path:    offerBundleDir(offer) + "/agent-registration.json",
				Content: buildOfferAgentRegistration(offer, originProfile),
			},
			offerBundleFile{
				Key:     offerBundleKey(offer, "index.html"),
				Path:    offerBundleDir(offer) + "/index.html",
				Content: buildOfferLandingHTML(offer, originProfile),
			},
		)
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Key < bundles[j].Key })
	return bundles
}

func bundleDigestInput(bundles []offerBundleFile) string {
	var b strings.Builder
	for _, f := range bundles {
		b.WriteString(f.Key)
		b.WriteString("\x00")
		b.WriteString(f.Content)
		b.WriteString("\x00")
	}
	return b.String()
}

// buildOfferScopedOpenAPI renders the offer's own openapi.json: servers is
// the dedicated origin, paths are rooted at "/" (the hostname path-world),
// and only this offer's operations appear — this is what makes a per-origin
// crawler see exactly one product.
func buildOfferScopedOpenAPI(offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) string {
	origin := offer.EffectiveOrigin()

	paths := map[string]any{}
	for rel, item := range openAPIPathsForOffer(offer) {
		paths[joinOpenAPIPath("/", rel)] = rerootAuthInfo(item)
	}

	title := strings.TrimSpace(offer.Spec.Registration.Name)
	if title == "" {
		title = offer.Name
	}
	info := map[string]any{
		"title":       title,
		"version":     "1",
		"description": offerDescription(offer, "x402 payment-gated service."),
	}
	if email := strings.TrimSpace(profile.ContactEmail); email != "" {
		info["contact"] = map[string]any{"name": strings.TrimSpace(profile.DisplayName), "email": email}
	}

	components := map[string]any{
		"schemas":   openAPIComponentSchemas(),
		"responses": openAPIComponentResponses(),
	}
	if anyOfferHasAuthRoute([]*monetizeapi.ServiceOffer{offer}) {
		components["securitySchemes"] = map[string]any{
			"siwx": map[string]any{
				"type":        "http",
				"scheme":      "bearer",
				"description": "Sign-In With X (EIP-4361): `Authorization: SIWX <b64 message>.<b64 signature>`, or mint a session via POST /auth/verify. See x-auth-info on gated operations.",
			},
		}
	}

	doc := map[string]any{
		"openapi":    openAPISpecVersion,
		"info":       info,
		"servers":    []any{map[string]any{"url": origin}},
		"paths":      paths,
		"components": components,
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return `{"openapi":"` + openAPISpecVersion + `","info":{"title":"service","version":"1"},"paths":{}}`
	}
	return string(encoded)
}

// rerootAuthInfo rewrites x-auth-info URLs from the shared-origin path-world
// (/services/<name>/auth) to the hostname root (/auth). The operations are
// generated once by openAPIPathsForOffer; only this extension embeds the
// offer prefix.
func rerootAuthInfo(item map[string]any) map[string]any {
	out := map[string]any{}
	for method, rawOp := range item {
		op, ok := rawOp.(map[string]any)
		if !ok {
			out[method] = rawOp
			continue
		}
		info, ok := op["x-auth-info"].(map[string]any)
		if !ok {
			out[method] = op
			continue
		}
		reset := map[string]any{}
		for k, v := range info {
			reset[k] = v
		}
		reset["signInUrl"] = "/auth"
		reset["verifyUrl"] = "/auth/verify"
		opCopy := map[string]any{}
		for k, v := range op {
			opCopy[k] = v
		}
		opCopy["x-auth-info"] = reset
		out[method] = opCopy
	}
	return out
}

// openAPIRelPathForOfferRoute resolves the OpenAPI-relative path a single
// route maps to, matching openAPIPathsForOffer's per-type path selection so
// /.well-known/x402 and openapi.json agree on the same paid operation path.
// Inference/agent offers always expose the fixed OpenAI-compatible endpoint
// regardless of the declared route table (openAPIPathsForOffer ignores
// spec.routes for these types); every other type maps the route's own path
// through openAPIRelPathForRoute exactly as the OpenAPI builder does.
func openAPIRelPathForOfferRoute(offer *monetizeapi.ServiceOffer, routePath string) string {
	if offer.IsInference() || offer.IsAgent() {
		return "/v1/chat/completions"
	}
	return openAPIRelPathForRoute(routePath)
}

// buildOfferWellKnownX402 renders the /.well-known/x402 discovery document:
// one resource entry per paid route, each carrying the signable payment
// requirements (mirrors the 402 accepts[] fields so a crawler can price the
// offer without probing). Tracks the x402 discovery-list shape used by
// facilitator /discovery/resources feeds.
func buildOfferWellKnownX402(offer *monetizeapi.ServiceOffer) string {
	origin := offer.EffectiveOrigin()
	var resources []any
	for _, rt := range offer.EffectiveRoutes() {
		if rt.EffectiveGate() != monetizeapi.GatePaid {
			continue
		}
		method := "POST"
		if len(rt.Methods) > 0 {
			method = strings.ToUpper(rt.Methods[0])
		}
		accepts := []any{}
		if rt.HasPriceOverride() {
			p := offer.EffectivePayments()[0]
			p.Price = rt.Price
			accepts = append(accepts, wellKnownAccept(p))
		} else {
			for _, p := range offer.EffectivePayments() {
				accepts = append(accepts, wellKnownAccept(p))
			}
		}
		desc := rt.Summary
		if desc == "" {
			desc = offerDescription(offer, "x402 payment-gated service.")
		}
		resources = append(resources, map[string]any{
			"resource":    origin + joinOpenAPIPath("/", openAPIRelPathForOfferRoute(offer, rt.Path)),
			"type":        "http",
			"method":      method,
			"description": desc,
			"x402Version": 2,
			"accepts":     accepts,
		})
	}
	doc := map[string]any{
		"x402Version": 2,
		"resources":   resources,
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return `{"x402Version":2,"resources":[]}`
	}
	return string(encoded)
}

// wellKnownAccept renders one payment option in the 402-requirement shape
// (scheme/network/asset/amount atomic units) rather than the display shape.
func wellKnownAccept(p monetizeapi.ServiceOfferPayment) map[string]any {
	entry := map[string]any{
		"scheme": "exact",
	}
	if caip, _ := caip2ForNetwork(p.Network); caip != "" {
		entry["network"] = caip
	} else {
		entry["network"] = p.Network
	}
	if p.PayTo != "" {
		entry["payTo"] = p.PayTo
	}
	timeout := p.MaxTimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}
	entry["maxTimeoutSeconds"] = timeout
	asset := paymentAssetJSON(p)
	if asset != nil {
		if asset.Address != "" {
			entry["asset"] = asset.Address
		}
		if raw, _ := paymentPriceRawAndUnit(p); raw != "" && catalogAssetHasKnownDecimals(asset) {
			entry["amount"] = decimalToAtomicString(raw, int(asset.Decimals))
		}
		extra := map[string]any{}
		if asset.TransferMethod != "" {
			extra["assetTransferMethod"] = asset.TransferMethod
		}
		if asset.EIP712Domain != nil {
			extra["name"] = asset.EIP712Domain.Name
			extra["version"] = asset.EIP712Domain.Version
		}
		if len(extra) > 0 {
			entry["extra"] = extra
		}
	}
	return entry
}

//go:embed templates/offer_landing.html
var offerLandingHTMLSrc string

var offerLandingTmpl = template.Must(
	template.New("offer_landing").Parse(offerLandingHTMLSrc),
)

// buildOfferLandingHTML renders the hostname's "/" landing page: enough for
// a human to understand the product and for previews/scrapers to get sane
// metadata, with the machine surfaces one link away.
func buildOfferLandingHTML(offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) string {
	title := strings.TrimSpace(offer.Spec.Registration.Name)
	if title == "" {
		title = offer.Name
	}
	operator := strings.TrimSpace(profile.DisplayName)
	if operator == "" {
		operator = "an Obol Stack operator"
	}
	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)

	// This page lives on the offer's dedicated origin, so only absolute
	// URLs (or data: URIs) resolve reliably; the resolved profile's default
	// logo is already absolute against the main storefront origin.
	logo := strings.TrimSpace(profile.LogoURL)
	custom := !storefront.IsDefaultLogoURL(logo)
	showName := custom
	if !custom && !theme.Dark {
		// Default wordmark is light-on-dark: swap in the dark square mark
		// (same origin as the wordmark) and spell out the operator name.
		logo = strings.TrimSuffix(logo, storefront.DefaultLogoPath) + storefront.DefaultMarkPath
		showName = true
	}
	favicon := crossOriginAssetURL(profile.FaviconURL)
	if favicon == "" && custom {
		favicon = crossOriginAssetURL(logo)
	}

	desc := offerDescription(offer, "x402 payment-gated service.")
	var out strings.Builder
	err := offerLandingTmpl.Execute(&out, map[string]any{
		"Title": title,
		// Meta/OG tags keep the plain text; the body renders the markdown.
		"Description":     desc,
		"DescriptionHTML": storefront.RenderRichText(desc),
		"AboutHTML":       storefront.RenderRichText(profile.Description),
		"CustomCSS":       template.CSS(storefront.SafeCustomCSS(profile.CustomCSS)),
		"Price":           describeOfferPrice(offer),
		// SafeAssetURL: inline data:image logos are a supported profile
		// form that html/template's URL filter would otherwise reject.
		"LogoURL":    storefront.SafeAssetURL(logo),
		"ShowName":   showName,
		"Operator":   operator,
		"ThemeCSS":   template.CSS(theme.CSSVars()),
		"ThemeColor": theme.ThemeColor(),
		"FaviconURL": storefront.SafeAssetURL(favicon),
		"OGImageURL": storefront.SafeAssetURL(crossOriginAssetURL(profile.OGImageURL)),
	})
	if err != nil {
		return "<!doctype html><title>" + template.HTMLEscapeString(title) + "</title>"
	}
	return out.String()
}

// crossOriginAssetURL returns raw only when it resolves from a different
// origin than it was authored against: absolute http(s) URLs and inline
// data: URIs pass, site-relative paths (which would 404 on a dedicated
// offer hostname) drop to "".
func crossOriginAssetURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "data:") {
		return raw
	}
	return ""
}
