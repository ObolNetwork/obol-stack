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
	content := buildSkillMarkdown([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)

	for _, want := range []string{
		"`POST https://example.com/services/audit/submit` — 0.5 USDC/request — Submit source for audit",
		"`GET https://example.com/services/audit/jobs` — free (covers sub-paths)",
		"`GET https://example.com/services/audit/reports` — free, wallet sign-in required (SIWX/EIP-4361 — see the offer's `/auth` page) (covers sub-paths)",
		// Root-priced "/*": POST, because the offer root serves index.html on
		// GET and only POST falls through to the payment gate.
		"`POST https://example.com/services/audit` — 0.1 USDC/request (covers sub-paths)",
		// Buy prompts + try-it must target the primary paid route, not the root.
		"curl -i https://example.com/services/audit/submit",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("skill.md missing %q\n---\n%s", want, content)
		}
	}
}

// TestRouteSurface_Golden_ExactBeatsWildcardRegardlessOfDeclarationOrder is
// the regression test for finding F6: an exact PAID route and a free
// wildcard route can collapse onto the same OpenAPI {path, method} slot
// (openAPIRelPathForRoute strips "/jobs/*" down to "/jobs", same as the
// exact "/jobs"). The verifier always resolves that overlap by specificity
// — exact beats wildcard — regardless of spec.routes declaration order
// (sortRoutesBySpecificity, internal/x402/matcher.go). The OpenAPI builder
// must resolve the same way, or discovery can advertise a route as free
// while the verifier charges it. Here the exact PAID route is declared
// FIRST, free wildcard SECOND: a buggy "last route in spec.routes wins"
// render lets the later free wildcard clobber the paid route's operation
// at the collapsed "/jobs" key.
func TestRouteSurface_Golden_ExactBeatsWildcardRegardlessOfDeclarationOrder(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
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
				// Exact PAID route declared FIRST, free wildcard SECOND.
				{Path: "/jobs", Methods: []string{"GET"}, Gate: monetizeapi.GatePaid,
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.5"}},
				{Path: "/jobs/*", Methods: []string{"GET"}, Gate: monetizeapi.GateFree},
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

	rules, err := x402.RouteRulesForOffer(offer, "")
	if err != nil {
		t.Fatalf("RouteRulesForOffer: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("len(rules) = %d, want 2", len(rules))
	}
	// The verifier resolves the "/jobs" vs "/jobs/*" overlap by specificity
	// (sortRoutesBySpecificity, internal/x402/matcher.go:63-93): exact
	// beats wildcard, regardless of spec.routes declaration order. Find the
	// exact rule directly rather than re-deriving the verifier's matching —
	// this test only needs to know what that rule enforces.
	var exactRule *x402.RouteRule
	for i := range rules {
		if !strings.HasSuffix(rules[i].Pattern, "/*") {
			exactRule = &rules[i]
		}
	}
	if exactRule == nil {
		t.Fatalf("no exact rule among %v — fixture no longer reproduces F6", rules)
	}
	if exactRule.IsFree() {
		t.Fatalf("exact rule %q is free — fixture no longer reproduces F6 (expected the paid override)", exactRule.Pattern)
	}
	if exactRule.Price != "0.5" {
		t.Fatalf("exact rule %q price = %q, want \"0.5\"", exactRule.Pattern, exactRule.Price)
	}

	paths := buildOpenAPIPaths([]*monetizeapi.ServiceOffer{offer})
	const key = "/services/audit/jobs"
	item, ok := paths[key].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI has no %s path (keys: %v)", key, mapKeys(paths))
	}
	op, ok := item["get"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI %s has no GET operation: %v", key, item)
	}
	if op["x-gate"] == "free" {
		t.Errorf("OpenAPI advertises GET %s as free, but the verifier charges it (per-route price override 0.5): %v", key, op)
	}
	info, hasPayment := op["x-payment-info"].(map[string]any)
	if !hasPayment {
		t.Fatalf("OpenAPI GET %s is missing x-payment-info; the verifier gates it paid at the override price", key)
	}
	price, _ := info["price"].(map[string]any)
	if got := price["amount"]; got != exactRule.Price {
		t.Errorf("GET %s: advertised amount %v != enforced price %q", key, got, exactRule.Price)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
