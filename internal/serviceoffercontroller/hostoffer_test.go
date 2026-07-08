package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func hostnameOffer() *monetizeapi.ServiceOffer {
	offer := routeTableOffer()
	offer.Spec.Hostname = "audit.v1337.example"
	return offer
}

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
	if len(rules) != 4 {
		t.Fatalf("rules = %d, want 4 (/, /openapi.json, /.well-known/x402, catch-all)", len(rules))
	}

	// Rule shapes: first three Exact → catalog httpd with full-path
	// rewrite; last PathPrefix / → verifier with prefix rewrite + headers.
	wantExact := map[string]string{
		"/":                 "/offers/sec/audit/index.html",
		"/openapi.json":     "/offers/sec/audit/openapi.json",
		"/.well-known/x402": "/offers/sec/audit/x402.json",
	}
	for i := 0; i < 3; i++ {
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
		if backend["name"] != skillCatalogConfigMapName || backend["namespace"] != skillCatalogNamespace {
			t.Errorf("rule %d backend = %v", i, backend)
		}
	}

	catchall := rules[3].(map[string]any)
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

	if got := buildOfferBundles([]*monetizeapi.ServiceOffer{routeTableOffer()}, profile); len(got) != 0 {
		t.Fatalf("path-only offer produced bundles: %v", got)
	}

	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile)
	if len(bundles) != 3 {
		t.Fatalf("len(bundles) = %d, want 3", len(bundles))
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

	skill := buildSkillCatalogMarkdown([]*monetizeapi.ServiceOffer{offer}, "https://shared.example", nil)
	if !strings.Contains(skill, "`POST https://audit.v1337.example/submit`") {
		t.Errorf("skill.md routes not rooted at dedicated origin:\n%.400s", skill)
	}
}
