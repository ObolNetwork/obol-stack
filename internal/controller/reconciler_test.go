package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGetSpec_Valid(t *testing.T) {
	so := makeServiceOffer("test-offer", "default", map[string]interface{}{
		"upstream": map[string]interface{}{
			"service":    "my-svc",
			"namespace":  "my-ns",
			"port":       int64(8080),
			"healthPath": "/healthz",
		},
		"payment": map[string]interface{}{
			"network": "base-sepolia",
			"payTo":   "0x1234",
			"price": map[string]interface{}{
				"perRequest": "0.001",
			},
		},
		"path": "/services/test",
	})

	spec, err := getSpec(so)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Upstream.Service != "my-svc" {
		t.Errorf("expected service my-svc, got %s", spec.Upstream.Service)
	}
	if spec.Upstream.Port != 8080 {
		t.Errorf("expected port 8080, got %d", spec.Upstream.Port)
	}
	if spec.Payment.PayTo != "0x1234" {
		t.Errorf("expected payTo 0x1234, got %s", spec.Payment.PayTo)
	}
	if spec.Path != "/services/test" {
		t.Errorf("expected path /services/test, got %s", spec.Path)
	}
}

func TestGetSpec_MissingUpstream(t *testing.T) {
	so := makeServiceOffer("bad", "default", map[string]interface{}{
		"payment": map[string]interface{}{
			"network": "base-sepolia",
			"payTo":   "0x1234",
			"price":   map[string]interface{}{},
		},
	})

	_, err := getSpec(so)
	if err == nil {
		t.Fatal("expected error for missing upstream")
	}
}

func TestSetCondition_NewCondition(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condUpstreamHealthy, true, "healthy")

	conditions, _, _ := nestedSlice(so, "status", "conditions")
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0].(map[string]interface{})
	if cond["type"] != condUpstreamHealthy {
		t.Errorf("expected type %s, got %s", condUpstreamHealthy, cond["type"])
	}
	if cond["status"] != "True" {
		t.Errorf("expected status True, got %s", cond["status"])
	}
}

func TestSetCondition_UpdateExisting(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condUpstreamHealthy, false, "down")
	setCondition(so, condUpstreamHealthy, true, "up")

	conditions, _, _ := nestedSlice(so, "status", "conditions")
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	cond := conditions[0].(map[string]interface{})
	if cond["status"] != "True" {
		t.Errorf("expected status True, got %s", cond["status"])
	}
	if cond["message"] != "up" {
		t.Errorf("expected message 'up', got %s", cond["message"])
	}
}

func TestSetCondition_MultipleConditions(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condUpstreamHealthy, true, "ok")
	setCondition(so, condPaymentGateReady, true, "ok")
	setCondition(so, condRoutePublished, false, "pending")

	conditions, _, _ := nestedSlice(so, "status", "conditions")
	if len(conditions) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conditions))
	}
}

func TestEffectiveRequestPrice(t *testing.T) {
	tests := []struct {
		name  string
		price priceTable
		want  string
	}{
		{"perRequest set", priceTable{PerRequest: "0.001"}, "0.001"},
		{"perMTok fallback", priceTable{PerMTok: "1.0"}, "1.0"},
		{"perHour fallback", priceTable{PerHour: "0.5"}, "0.5"},
		{"all empty", priceTable{}, "0"},
		{"perRequest takes precedence", priceTable{PerRequest: "0.01", PerMTok: "1.0"}, "0.01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.price.effectiveRequestPrice()
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPriceModel(t *testing.T) {
	tests := []struct {
		name  string
		price priceTable
		want  string
	}{
		{"perRequest", priceTable{PerRequest: "0.001"}, "perRequest"},
		{"perMTok", priceTable{PerMTok: "1.0"}, "perMTok"},
		{"perHour", priceTable{PerHour: "0.5"}, "perHour"},
		{"default", priceTable{}, "perRequest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.price.priceModel()
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBuildRouteEntry(t *testing.T) {
	spec := &offerSpec{
		Payment: paymentSpec{
			Network: "base-sepolia",
			PayTo:   "0xABC",
			Price:   priceTable{PerRequest: "0.001"},
		},
	}

	entry := buildRouteEntry("/services/test/*", "0.001", "test", "default", spec)

	if !contains(entry, `pattern: "/services/test/*"`) {
		t.Error("missing pattern")
	}
	if !contains(entry, `price: "0.001"`) {
		t.Error("missing price")
	}
	if !contains(entry, `payTo: "0xABC"`) {
		t.Error("missing payTo")
	}
	if !contains(entry, `network: "base-sepolia"`) {
		t.Error("missing network")
	}
	if !contains(entry, `offerName: "test"`) {
		t.Error("missing offerName")
	}
}

// --- helpers ---

func makeServiceOffer(name, ns string, spec map[string]interface{}) *unstructured.Unstructured { //nolint:unparam
	so := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "ServiceOffer",
			"metadata": map[string]interface{}{
				"name":       name,
				"namespace":  ns,
				"uid":        "test-uid-123",
				"generation": int64(1),
			},
			"status": map[string]interface{}{},
		},
	}
	if spec != nil {
		so.Object["spec"] = spec
	}
	return so
}

func nestedSlice(so *unstructured.Unstructured, fields ...string) ([]interface{}, bool, error) {
	return unstructured.NestedSlice(so.Object, fields...)
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
