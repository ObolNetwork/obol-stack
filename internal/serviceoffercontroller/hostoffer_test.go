package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func hostnameOffer() *monetizeapi.ServiceOffer {
	offer := routeTableOffer()
	offer.Spec.Hostname = "audit.v1337.example"
	return offer
}

// noUpstreamOpenAPI is the buildOfferBundles cache-lookup stub for tests
// that don't exercise the upstream-OpenAPI path.
func noUpstreamOpenAPI(*monetizeapi.ServiceOffer) map[string]any { return nil }

// TestBuildHostHTTPRoute pins the dedicated-origin route topology: Exact
// discovery rules rewriting into the offer's bundle files on the catalog
// httpd, and a PathPrefix / rule rewriting the public path-world into
// /services/<name> with X-Forwarded-Host pinned — the generalized form of
// the hand-built manifests that ran the live multistore.
func TestBuildHostHTTPRoute(t *testing.T) {
	route := buildHostHTTPRoute(hostnameOffer())

	hosts, _, _ := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
	if len(hosts) != 1 || hosts[0] != "audit.v1337.example" {
		t.Fatalf("hostnames = %v", hosts)
	}
	if route.GetName() != "so-audit-host" {
		t.Errorf("route name = %q", route.GetName())
	}

	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	if len(rules) != 5 {
		t.Fatalf("rules = %d, want 5 (/, openapi, x402, agent-registration, catch-all)", len(rules))
	}

	// Rule shapes: first three Exact → catalog httpd with full-path
	// rewrite; last PathPrefix / → verifier with prefix rewrite + headers.
	wantExact := map[string]string{
		"/":                                    "/offers/sec/audit/index.html",
		"/openapi.json":                        "/offers/sec/audit/openapi.json",
		"/.well-known/x402":                    "/offers/sec/audit/x402.json",
		"/.well-known/agent-registration.json": "/offers/sec/audit/agent-registration.json",
	}
	for i := 0; i < 4; i++ {
		rule := rules[i].(map[string]any)
		match := rule["matches"].([]any)[0].(map[string]any)["path"].(map[string]any)
		if match["type"] != "Exact" {
			t.Errorf("rule %d match type = %v, want Exact", i, match["type"])
		}
		public := match["value"].(string)
		filter := rule["filters"].([]any)[0].(map[string]any)
		rewrite := filter["urlRewrite"].(map[string]any)["path"].(map[string]any)
		if got := rewrite["replaceFullPath"]; got != wantExact[public] {
			t.Errorf("rule %d (%s) rewrites to %v, want %s", i, public, got, wantExact[public])
		}
		backend := rule["backendRefs"].([]any)[0].(map[string]any)
		if backend["name"] != staticSiteConfigMapName || backend["namespace"] != staticSiteNamespace {
			t.Errorf("rule %d backend = %v", i, backend)
		}
	}

	catchall := rules[4].(map[string]any)
	match := catchall["matches"].([]any)[0].(map[string]any)["path"].(map[string]any)
	if match["type"] != "PathPrefix" || match["value"] != "/" {
		t.Fatalf("catch-all match = %v", match)
	}
	var sawRewrite, sawHostHeader bool
	for _, rawFilter := range catchall["filters"].([]any) {
		filter := rawFilter.(map[string]any)
		switch filter["type"] {
		case "URLRewrite":
			p := filter["urlRewrite"].(map[string]any)["path"].(map[string]any)
			if p["type"] != "ReplacePrefixMatch" || p["replacePrefixMatch"] != "/services/audit" {
				t.Errorf("catch-all rewrite = %v", p)
			}
			sawRewrite = true
		case "RequestHeaderModifier":
			for _, s := range filter["requestHeaderModifier"].(map[string]any)["set"].([]any) {
				h := s.(map[string]any)
				if h["name"] == "X-Forwarded-Host" && h["value"] == "audit.v1337.example" {
					sawHostHeader = true
				}
			}
		}
	}
	if !sawRewrite || !sawHostHeader {
		t.Errorf("catch-all missing rewrite (%v) or host header (%v)", sawRewrite, sawHostHeader)
	}
	backend := catchall["backendRefs"].([]any)[0].(map[string]any)
	if backend["name"] != "x402-verifier" {
		t.Errorf("catch-all backend = %v", backend)
	}
}

// TestBuildOfferBundles pins the per-offer discovery bundle: an offer-scoped
// openapi.json rooted at "/" with the dedicated origin as its only server,
// a /.well-known/x402 resource list carrying signable requirements for the
// paid routes only, and a landing page. Path-only offers contribute nothing.
func TestBuildOfferBundles(t *testing.T) {
	profile := schemas.StorefrontProfile{DisplayName: "Acme", ContactEmail: "ops@acme.example"}
	offer := hostnameOffer()

	if got := buildOfferBundles([]*monetizeapi.ServiceOffer{routeTableOffer()}, profile, noUpstreamOpenAPI); len(got) != 0 {
		t.Fatalf("path-only offer produced bundles: %v", got)
	}

	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, noUpstreamOpenAPI)
	if len(bundles) != 4 {
		t.Fatalf("len(bundles) = %d, want 4", len(bundles))
	}
	byPath := map[string]string{}
	for _, f := range bundles {
		byPath[f.Path] = f.Content
	}

	// Offer-scoped openapi: origin server, root-rooted paths, rerooted
	// auth URLs, contact email.
	var doc map[string]any
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/openapi.json"]), &doc); err != nil {
		t.Fatalf("openapi bundle: %v", err)
	}
	servers := doc["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["url"] != "https://audit.v1337.example" {
		t.Errorf("servers = %v", servers)
	}
	paths := doc["paths"].(map[string]any)
	if _, ok := paths["/submit"]; !ok {
		t.Errorf("paths = %v, want /submit rooted at /", mapKeys(paths))
	}
	for p := range paths {
		if strings.HasPrefix(p, "/services/") {
			t.Errorf("bundle path %q leaks the shared-origin prefix", p)
		}
	}
	reports := paths["/reports"].(map[string]any)["get"].(map[string]any)
	authInfo := reports["x-auth-info"].(map[string]any)
	if authInfo["signInUrl"] != "/auth" || authInfo["verifyUrl"] != "/auth/verify" {
		t.Errorf("x-auth-info not rerooted: %v", authInfo)
	}
	contact := doc["info"].(map[string]any)["contact"].(map[string]any)
	if contact["email"] != "ops@acme.example" {
		t.Errorf("contact = %v", contact)
	}

	// Well-known x402: paid routes only, atomic amounts, CAIP-2 network.
	var wk struct {
		X402Version int `json:"x402Version"`
		Resources   []struct {
			Resource string           `json:"resource"`
			Accepts  []map[string]any `json:"accepts"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/x402.json"]), &wk); err != nil {
		t.Fatalf("x402 bundle: %v", err)
	}
	if wk.X402Version != 2 || len(wk.Resources) != 2 {
		t.Fatalf("well-known = version %d, %d resources (want 2: /submit + catch-all)", wk.X402Version, len(wk.Resources))
	}
	if wk.Resources[0].Resource != "https://audit.v1337.example/submit" {
		t.Errorf("resources[0] = %q", wk.Resources[0].Resource)
	}
	submit := wk.Resources[0].Accepts[0]
	if submit["network"] != "eip155:84532" || submit["amount"] != "500000" || submit["scheme"] != "exact" {
		t.Errorf("submit accepts = %v, want overridden 0.5 USDC in atomic units on CAIP-2 network", submit)
	}

	// Landing page: branded, links to the machine surfaces.
	landing := byPath["offers/sec/audit/index.html"]
	for _, want := range []string{"/openapi.json", "/.well-known/x402", "Sold by Acme"} {
		if !strings.Contains(landing, want) {
			t.Errorf("landing missing %q", want)
		}
	}
}

// TestBuildOfferBundles_BrandingOverride pins the per-origin identity merge:
// spec.branding fields override the storefront profile on the dedicated
// origin's surfaces, empty fields inherit.
func TestBuildOfferBundles_BrandingOverride(t *testing.T) {
	profile := storefront.ResolvePublished(&schemas.StorefrontProfile{
		DisplayName:  "Acme",
		ContactEmail: "ops@acme.example",
	}, "https://main.example")
	offer := hostnameOffer()
	offer.Spec.Branding = &monetizeapi.ServiceOfferBranding{
		DisplayName: "AuditCo",
		Theme:       "obol",
		AccentColor: "#a1b2c3",
		LogoURL:     "https://cdn.example.com/auditco.png",
		Description: "**Deep** audits by AuditCo.",
	}

	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, noUpstreamOpenAPI)
	byPath := map[string]string{}
	for _, f := range bundles {
		byPath[f.Path] = f.Content
	}

	landing := byPath["offers/sec/audit/index.html"]
	for _, want := range []string{
		"Sold by AuditCo",                          // displayName override
		"--bg01:#05201a;",                          // obol preset background
		"--green:#a1b2c3;",                         // accent override
		`src="https://cdn.example.com/auditco.png`, // logo override
		"About AuditCo",                            // origin description section
		"<strong>Deep</strong>",                    // markdown through richtext
	} {
		if !strings.Contains(landing, want) {
			t.Errorf("branded landing missing %q", want)
		}
	}

	// Contact email is not a branding field — it inherits from the profile.
	var doc map[string]any
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/openapi.json"]), &doc); err != nil {
		t.Fatalf("openapi bundle: %v", err)
	}
	contact := doc["info"].(map[string]any)["contact"].(map[string]any)
	if contact["email"] != "ops@acme.example" || contact["name"] != "AuditCo" {
		t.Errorf("contact = %v, want inherited email + overridden name", contact)
	}
}

// TestPickHostnameConflict pins one-offer-per-origin, first claimant wins.
func TestPickHostnameConflict(t *testing.T) {
	older := hostnameOffer()
	older.Name, older.Namespace = "alpha", "agent-a"
	older.CreationTimestamp = metav1.Time{Time: metav1.Now().Add(-3600e9)}
	newer := hostnameOffer()
	newer.Name, newer.Namespace = "beta", "agent-b"
	newer.CreationTimestamp = metav1.Now()

	if got := pickHostnameConflict(newer, []*monetizeapi.ServiceOffer{older, newer}); got != "agent-a/alpha" {
		t.Errorf("newer claimant conflict = %q, want agent-a/alpha", got)
	}
	if got := pickHostnameConflict(older, []*monetizeapi.ServiceOffer{older, newer}); got != "" {
		t.Errorf("older claimant conflict = %q, want none", got)
	}

	// Different hostname or no hostname → no conflict.
	newer.Spec.Hostname = "other.v1337.example"
	if got := pickHostnameConflict(newer, []*monetizeapi.ServiceOffer{older, newer}); got != "" {
		t.Errorf("different hostnames conflict = %q", got)
	}
}

// TestCatalogAdvertisesDedicatedOrigin: hostname offers list at their own
// origin in /api/services.json and skill.md.
func TestCatalogAdvertisesDedicatedOrigin(t *testing.T) {
	offer := hostnameOffer()
	catalogJSON := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://shared.example", nil)
	var catalog schemas.ServiceCatalog
	if err := json.Unmarshal([]byte(catalogJSON), &catalog); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(catalog.Services) != 1 || catalog.Services[0].Endpoint != "https://audit.v1337.example" {
		t.Fatalf("catalog endpoint = %+v, want dedicated origin", catalog.Services)
	}

	skill := buildSkillMarkdown([]*monetizeapi.ServiceOffer{offer}, "https://shared.example", nil)
	if !strings.Contains(skill, "`POST https://audit.v1337.example/submit`") {
		t.Errorf("skill.md routes not rooted at dedicated origin:\n%.400s", skill)
	}
}

// TestHostRouteDiscoveryRulesAreGETOnly pins the method scoping: a
// root-priced offer advertises POST <origin>/ as its paid resource, so the
// Exact "/" discovery rule must only capture GETs — POSTs fall through to
// the PathPrefix payment gate.
func TestHostRouteDiscoveryRulesAreGETOnly(t *testing.T) {
	offer := hostnameOffer()
	offer.Spec.Type = "agent"
	route := buildHostHTTPRoute(offer)
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	for i, raw := range rules[:len(rules)-1] { // all but the catch-all
		match := raw.(map[string]any)["matches"].([]any)[0].(map[string]any)
		if match["method"] != "GET" {
			t.Errorf("rule %d (%v) method = %v, want GET", i, match["path"], match["method"])
		}
	}
	last := rules[len(rules)-1].(map[string]any)["matches"].([]any)[0].(map[string]any)
	if _, scoped := last["method"]; scoped {
		t.Errorf("catch-all must match every method")
	}
}

func TestBuildOfferBundles_UpstreamOpenAPI(t *testing.T) {
	profile := schemas.StorefrontProfile{DisplayName: "Acme", ContactEmail: "ops@acme.example"}
	offer := hostnameOffer()
	offer.Spec.Registration.Name = "Hyperliquid Trading Intelligence"
	offer.Spec.Registration.Description = "Full first-party catalog."
	upstream := func(*monetizeapi.ServiceOffer) map[string]any {
		return map[string]any{
			"openapi": "3.1.0",
			"info":    map[string]any{"title": "upstream-title", "version": "1.1.0"},
			"paths": map[string]any{
				"/v1/leaderboard": map[string]any{
					"get": map[string]any{
						"summary": "Leaderboard", "security": []any{map[string]any{"x402": []any{}}},
						"x-payment-info": map[string]any{"price": map[string]any{"amount": "0.001"}},
						"responses":      map[string]any{"200": map[string]any{}, "402": map[string]any{}},
					},
				},
				"/v1/markets/overview": map[string]any{
					"get": map[string]any{"summary": "Free overview", "security": []any{}, "responses": map[string]any{"200": map[string]any{}}},
				},
			},
		}
	}
	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, upstream)
	byPath := map[string]string{}
	for _, f := range bundles {
		byPath[f.Path] = f.Content
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/openapi.json"]), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["info"].(map[string]any)["title"] != "Hyperliquid Trading Intelligence" {
		t.Errorf("title = %v", doc["info"])
	}
	if _, ok := doc["paths"].(map[string]any)["/v1/leaderboard"]; !ok {
		t.Fatalf("missing leaderboard in paths: %v", doc["paths"])
	}
	var wk struct {
		Resources []struct {
			Resource string `json:"resource"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/x402.json"]), &wk); err != nil {
		t.Fatal(err)
	}
	if len(wk.Resources) != 1 || !strings.Contains(wk.Resources[0].Resource, "/v1/leaderboard") {
		t.Fatalf("x402 resources = %+v", wk.Resources)
	}
	var reg map[string]any
	if err := json.Unmarshal([]byte(byPath["offers/sec/audit/agent-registration.json"]), &reg); err != nil {
		t.Fatal(err)
	}
	if reg["name"] != "Hyperliquid Trading Intelligence" {
		t.Errorf("reg name = %v", reg["name"])
	}
}
