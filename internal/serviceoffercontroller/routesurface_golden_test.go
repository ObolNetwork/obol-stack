package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/x402"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// routeTableOffer is the shared fixture for the golden test: one http offer
// exercising every route-table feature — a paid route with a per-route
// price override and summary, a free wildcard carve-out, and a paid
// catch-all at the offer price.
func routeTableOffer() *monetizeapi.ServiceOffer {
	return &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "audit", Namespace: "sec"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:     "http",
			Upstream: monetizeapi.ServiceOfferUpstream{Service: "auditd", Namespace: "sec", Port: 8080},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.1"},
			},
			Routes: []monetizeapi.ServiceOfferRoute{
				{Path: "/submit", Methods: []string{"POST"}, Gate: monetizeapi.GatePaid,
					Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.5"},
					Summary: "Submit source for audit"},
				{Path: "/jobs/*", Gate: monetizeapi.GateFree},
				{Path: "/reports/*", Gate: monetizeapi.GateAuth},
				{Path: "/*", Gate: monetizeapi.GatePaid},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "True"},
				{Type: "PaymentGateReady", Status: "True"},
				{Type: "RoutePublished", Status: "True"},
			},
		},
	}
}

// TestRouteSurface_Golden_VerifierAndOpenAPIAgree is the drift guard for
// the route table's two load-bearing consumers: the x402 verifier's
// RouteRules (what is actually gated, at what price) and the generated
// OpenAPI paths (what is advertised). The two are rendered by different
// packages from the same spec.routes; this test asserts a bijection
// between them and that per-route prices match. If either package's
// path-join or price-override logic drifts, discovery advertises routes
// the gate doesn't serve (or prices it doesn't charge) — the exact drift
// class the route table exists to eliminate.
func TestRouteSurface_Golden_VerifierAndOpenAPIAgree(t *testing.T) {
	offer := routeTableOffer()

	rules, err := x402.RouteRulesForOffer(offer, "")
	if err != nil {
		t.Fatalf("RouteRulesForOffer: %v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("len(rules) = %d, want 4", len(rules))
	}

	paths := buildOpenAPIPaths([]*monetizeapi.ServiceOffer{offer})

	// The siwx securityScheme must exist exactly when an auth route does.
	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "https://example.com", schemas.StorefrontProfile{}))
	if dig(t, doc, "components", "securitySchemes", "siwx") == nil {
		t.Error("offer has an auth route but the document lacks securitySchemes.siwx")
	}

	// Bijection: every verifier rule maps to exactly one OpenAPI path key
	// (wildcard patterns collapse to their literal prefix), and no OpenAPI
	// path exists that the verifier doesn't gate.
	seen := map[string]bool{}
	for _, rule := range rules {
		key := strings.TrimSuffix(rule.Pattern, "/*")
		item, ok := paths[key].(map[string]any)
		if !ok {
			t.Fatalf("verifier rule %q has no OpenAPI path %q (keys: %v)", rule.Pattern, key, mapKeys(paths))
		}
		seen[key] = true

		for method, rawOp := range item {
			op, ok := rawOp.(map[string]any)
			if !ok {
				t.Fatalf("path %q method %q: not an operation object", key, method)
			}
			info, hasPayment := op["x-payment-info"].(map[string]any)
			switch {
			case rule.IsFree():
				if hasPayment {
					t.Errorf("free route %q advertises x-payment-info", rule.Pattern)
				}
				if op["x-gate"] != "free" {
					t.Errorf("free route %q missing x-gate: free marker", rule.Pattern)
				}
				continue
			case rule.IsAuth():
				if hasPayment {
					t.Errorf("auth route %q advertises x-payment-info", rule.Pattern)
				}
				if op["x-gate"] != "auth" {
					t.Errorf("auth route %q missing x-gate: auth marker", rule.Pattern)
				}
				sec, _ := op["security"].([]any)
				if len(sec) != 1 {
					t.Errorf("auth route %q must declare the siwx security requirement, got %v", rule.Pattern, op["security"])
				}
				if _, ok := op["x-auth-info"].(map[string]any); !ok {
					t.Errorf("auth route %q missing x-auth-info", rule.Pattern)
				}
				continue
			}
			if !hasPayment {
				t.Errorf("paid route %q has no x-payment-info", rule.Pattern)
				continue
			}
			// Advertised price must equal the enforced price.
			price, _ := info["price"].(map[string]any)
			if got := price["amount"]; got != rule.Price {
				t.Errorf("route %q: advertised amount %v != enforced price %q", rule.Pattern, got, rule.Price)
			}
		}
	}
	if len(paths) != len(seen) {
		t.Errorf("OpenAPI advertises %d paths but the verifier gates %d: %v", len(paths), len(seen), mapKeys(paths))
	}
}

// TestRouteSurface_Golden_SkillMDListsEveryRoute asserts the human/agent
// discovery surface enumerates the same route set: every route-table entry
// appears in skill.md with its method, full URL, and effective price.
func TestRouteSurface_Golden_SkillMDListsEveryRoute(t *testing.T) {
	offer := routeTableOffer()
	content := buildSkillCatalogMarkdown([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)

	for _, want := range []string{
		"`POST https://example.com/services/audit/submit` — 0.5 USDC/request — Submit source for audit",
		"`GET https://example.com/services/audit/jobs` — free (covers sub-paths)",
		"`GET https://example.com/services/audit/reports` — free, wallet sign-in required (SIWX/EIP-4361 — see the offer's `/auth` page) (covers sub-paths)",
		"`POST https://example.com/services/audit` — 0.1 USDC/request (covers sub-paths)",
		// Buy prompts + try-it must target the primary paid route, not the root.
		"curl -i https://example.com/services/audit/submit",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("skill.md missing %q\n---\n%s", want, content)
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
