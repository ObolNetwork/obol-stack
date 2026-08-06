package serviceoffercontroller

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBuildAggregateWellKnownX402_BasicOffer confirms the aggregate document
// carries one resource entry per ready offer, rooted at baseURL +
// EffectivePath() — the same shared-origin alias buildOpenAPIDocument and
// buildServiceCatalogJSON already publish every offer under.
func TestBuildAggregateWellKnownX402_BasicOffer(t *testing.T) {
	offer := readyOfferWithSpec("echo", "demo", monetizeapi.ServiceOfferSpec{
		Type: "http",
		Path: "/services/echo",
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base",
			PayTo:   "0x3333333333333333333333333333333333333333",
			Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.00001"},
		},
	})

	doc := parseOpenAPI(t, buildAggregateWellKnownX402([]*monetizeapi.ServiceOffer{offer}, "https://tunnel.example"))
	resources, _ := doc["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("resources = %d entries, want 1: %v", len(resources), resources)
	}
	res := resources[0].(map[string]any)
	if res["resource"] != "https://tunnel.example/services/echo" {
		t.Errorf("resource = %v, want https://tunnel.example/services/echo", res["resource"])
	}
	if res["method"] != "GET" {
		t.Errorf("method = %v, want GET", res["method"])
	}
	accepts, _ := res["accepts"].([]any)
	if len(accepts) != 1 {
		t.Fatalf("accepts = %d entries, want 1: %v", len(accepts), accepts)
	}
}

// TestBuildAggregateWellKnownX402_ExcludesNotReadyAndDrained mirrors
// buildOpenAPIDocument's readiness filter (TestBuildOpenAPIDocument_ExcludesNotReadyAndDrained).
func TestBuildAggregateWellKnownX402_ExcludesNotReadyAndDrained(t *testing.T) {
	ready := readyOfferWithSpec("alpha", "demo", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xaa", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
	})
	notReady := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "demo"},
		Status:     monetizeapi.ServiceOfferStatus{Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "False"}}},
	}

	doc := parseOpenAPI(t, buildAggregateWellKnownX402([]*monetizeapi.ServiceOffer{ready, notReady}, ""))
	resources, _ := doc["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("resources = %d entries, want only the ready offer: %v", len(resources), resources)
	}
	res := resources[0].(map[string]any)
	if res["resource"] != "/services/alpha/v1/chat/completions" {
		t.Errorf("resource = %v, want /services/alpha/v1/chat/completions", res["resource"])
	}
}

// TestBuildAggregateWellKnownX402_HostnameBoundOfferStillIncluded confirms a
// hostname-bound offer still appears in the aggregate document under its
// shared-origin alias (baseURL + EffectivePath()) — it additionally gets its
// own root-rooted copy at its dedicated origin via buildOfferWellKnownX402,
// which this document does not replace.
func TestBuildAggregateWellKnownX402_HostnameBoundOfferStillIncluded(t *testing.T) {
	offer := readyOfferWithSpec("audit", "demo", monetizeapi.ServiceOfferSpec{
		Type:     "http",
		Path:     "/services/audit",
		Hostname: "audit.acme.example",
		Payment:  monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xaa", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
	})

	doc := parseOpenAPI(t, buildAggregateWellKnownX402([]*monetizeapi.ServiceOffer{offer}, "https://tunnel.example"))
	resources, _ := doc["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("resources = %d entries, want the hostname-bound offer's shared-origin alias: %v", len(resources), resources)
	}
	res := resources[0].(map[string]any)
	if res["resource"] != "https://tunnel.example/services/audit" {
		t.Errorf("resource = %v, want shared-origin alias, not the dedicated hostname", res["resource"])
	}
}

func TestBuildWellKnownX402HTTPRoute(t *testing.T) {
	route := buildWellKnownX402HTTPRoute()
	if route.GetName() != wellKnownX402RouteName {
		t.Fatalf("name = %q, want %q", route.GetName(), wellKnownX402RouteName)
	}
	spec, _ := route.Object["spec"].(map[string]any)
	if _, hasHostnames := spec["hostnames"]; hasHostnames {
		t.Error("well-known x402 route must not have hostnames filter (tunnel-reachable by design)")
	}
	rules, _ := spec["rules"].([]any)
	rule := rules[0].(map[string]any)
	matches, _ := rule["matches"].([]any)
	if got := matches[0].(map[string]any)["path"].(map[string]any)["value"]; got != "/.well-known/x402" {
		t.Errorf("match path = %v, want /.well-known/x402", got)
	}

	// The public path has no extension, but the mounted file must be
	// .json-suffixed for busybox's httpd.conf to serve application/json —
	// the route must rewrite to it, mirroring buildHostHTTPRoute's exactTo.
	filters, _ := rule["filters"].([]any)
	var rewrote bool
	for _, f := range filters {
		fm, _ := f.(map[string]any)
		if fm["type"] != "URLRewrite" {
			continue
		}
		path, _ := fm["urlRewrite"].(map[string]any)["path"].(map[string]any)
		if path["type"] == "ReplaceFullPath" && path["replaceFullPath"] == "/wellknown-x402.json" {
			rewrote = true
		}
	}
	if !rewrote {
		t.Errorf("expected a URLRewrite to /wellknown-x402.json, got filters: %v", filters)
	}
}
