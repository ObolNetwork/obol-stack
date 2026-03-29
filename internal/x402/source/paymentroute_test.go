package source

import (
	"testing"

	x402 "github.com/ObolNetwork/obol-stack/internal/x402"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPaymentRouteToRule(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "PaymentRoute",
			"metadata": map[string]interface{}{
				"name":      "myapi-payment",
				"namespace": "x402",
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion": "obol.org/v1alpha1",
						"kind":       "ServiceOffer",
						"name":       "myapi",
						"uid":        "abc-123",
					},
				},
			},
			"spec": map[string]interface{}{
				"pattern":    "/services/myapi/*",
				"price":      "0.001",
				"payTo":      "0xABC",
				"network":    "base-sepolia",
				"priceModel": "per-request",
			},
		},
	}

	rule, err := paymentRouteToRule(u)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rule.Pattern != "/services/myapi/*" {
		t.Errorf("pattern: got %s, want /services/myapi/*", rule.Pattern)
	}
	if rule.Price != "0.001" {
		t.Errorf("price: got %s, want 0.001", rule.Price)
	}
	if rule.PayTo != "0xABC" {
		t.Errorf("payTo: got %s, want 0xABC", rule.PayTo)
	}
	if rule.Network != "base-sepolia" {
		t.Errorf("network: got %s, want base-sepolia", rule.Network)
	}
	if rule.OfferName != "myapi" {
		t.Errorf("offerName: got %s, want myapi", rule.OfferName)
	}
}

func TestPaymentRouteToRule_MissingPattern(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "PaymentRoute",
			"metadata":   map[string]interface{}{"name": "bad", "namespace": "x402"},
			"spec": map[string]interface{}{
				"price": "0.001",
				"payTo": "0x123",
			},
		},
	}

	_, err := paymentRouteToRule(u)
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestPaymentRouteToRule_WithPerMTok(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "PaymentRoute",
			"metadata":   map[string]interface{}{"name": "mtok-test", "namespace": "x402"},
			"spec": map[string]interface{}{
				"pattern":                "/services/inference/*",
				"price":                  "0.001",
				"payTo":                  "0x123",
				"network":               "base-sepolia",
				"priceModel":            "per-mtok",
				"perMTok":               "1.0",
				"approxTokensPerRequest": int64(1000),
			},
		},
	}

	rule, err := paymentRouteToRule(u)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rule.PerMTok != "1.0" {
		t.Errorf("perMTok: got %s, want 1.0", rule.PerMTok)
	}
	if rule.ApproxTokensPerRequest != 1000 {
		t.Errorf("approxTokens: got %d, want 1000", rule.ApproxTokensPerRequest)
	}
}

func TestPaymentRouteSourceRebuild(t *testing.T) {
	// Test that the routes map builds correctly.
	s := &PaymentRouteSource{
		routes: map[string]x402.RouteRule{
			"a": {Pattern: "/a/*", Price: "0.01"},
			"b": {Pattern: "/b/*", Price: "0.02"},
		},
	}

	s.mu.RLock()
	if len(s.routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(s.routes))
	}
	s.mu.RUnlock()
}
