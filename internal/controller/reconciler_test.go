package controller

import (
	"strings"
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
		t.Errorf("service: got %s, want my-svc", spec.Upstream.Service)
	}
	if spec.Upstream.Port != 8080 {
		t.Errorf("port: got %d, want 8080", spec.Upstream.Port)
	}
	if spec.Payment.PayTo != "0x1234" {
		t.Errorf("payTo: got %s, want 0x1234", spec.Payment.PayTo)
	}
	if spec.Path != "/services/test" {
		t.Errorf("path: got %s, want /services/test", spec.Path)
	}
}

func TestGetSpec_DefaultPath(t *testing.T) {
	so := makeServiceOffer("myapi", "llm", map[string]interface{}{
		"upstream": map[string]interface{}{
			"service":   "ollama",
			"namespace": "llm",
			"port":      int64(11434),
		},
		"payment": map[string]interface{}{
			"network": "base-sepolia",
			"payTo":   "0xABC",
			"price":   map[string]interface{}{"perRequest": "0.01"},
		},
	})

	spec, err := getSpec(so)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Path != "" {
		t.Errorf("expected empty path (default), got %s", spec.Path)
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

	conditions := getConditions(t, so)
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	assertCondition(t, conditions[0], condUpstreamHealthy, "True", "healthy")
}

func TestSetCondition_UpdateExisting(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condUpstreamHealthy, false, "down")
	setCondition(so, condUpstreamHealthy, true, "up")

	conditions := getConditions(t, so)
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition after update, got %d", len(conditions))
	}

	assertCondition(t, conditions[0], condUpstreamHealthy, "True", "up")
}

func TestSetCondition_PreservesOtherConditions(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condUpstreamHealthy, true, "ok")
	setCondition(so, condPaymentGateReady, true, "ok")
	setCondition(so, condRoutePublished, false, "pending")

	conditions := getConditions(t, so)
	if len(conditions) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conditions))
	}
}

func TestSetCondition_PreservesLastTransitionTimeOnNoChange(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	setCondition(so, condReady, true, "first")
	conds1 := getConditions(t, so)
	ts1 := conds1[0].(map[string]interface{})["lastTransitionTime"]

	setCondition(so, condReady, true, "second")
	conds2 := getConditions(t, so)
	ts2 := conds2[0].(map[string]interface{})["lastTransitionTime"]

	if ts1 != ts2 {
		t.Errorf("lastTransitionTime should not change when status unchanged")
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
		{"perRequest precedence", priceTable{PerRequest: "0.01", PerMTok: "1.0"}, "0.01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.price.effectiveRequestPrice(); got != tt.want {
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
		{"perRequest", priceTable{PerRequest: "0.001"}, "per-request"},
		{"perMTok", priceTable{PerMTok: "1.0"}, "per-mtok"},
		{"perHour", priceTable{PerHour: "0.5"}, "per-hour"},
		{"default", priceTable{}, "per-request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.price.priceModel(); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSetOwnerRef(t *testing.T) {
	owner := makeServiceOffer("my-offer", "llm", nil)
	child := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "PaymentRoute",
		"metadata":   map[string]interface{}{"name": "test-pr", "namespace": "x402"},
	}}

	setOwnerRef(child, owner)

	refs, ok, _ := unstructured.NestedSlice(child.Object, "metadata", "ownerReferences")
	if !ok || len(refs) != 1 {
		t.Fatalf("expected 1 ownerRef, got %d", len(refs))
	}

	ref := refs[0].(map[string]interface{})
	if ref["kind"] != "ServiceOffer" {
		t.Errorf("ownerRef kind: got %s, want ServiceOffer", ref["kind"])
	}
	if ref["name"] != "my-offer" {
		t.Errorf("ownerRef name: got %s, want my-offer", ref["name"])
	}
	if ref["controller"] != true {
		t.Error("ownerRef should be controller")
	}
	if ref["blockOwnerDeletion"] != true {
		t.Error("ownerRef should block owner deletion")
	}
}

func TestComputePhase(t *testing.T) {
	so := makeServiceOffer("test", "default", nil)

	if phase := computePhase(so); phase != "Reconciling" {
		t.Errorf("empty conditions: got %s, want Reconciling", phase)
	}

	setCondition(so, condUpstreamHealthy, true, "ok")
	if phase := computePhase(so); phase != "Reconciling" {
		t.Errorf("partial conditions: got %s, want Reconciling", phase)
	}

	setCondition(so, condReady, true, "all good")
	if phase := computePhase(so); phase != "Ready" {
		t.Errorf("ready: got %s, want Ready", phase)
	}
}

func TestFinalizerName(t *testing.T) {
	if !strings.Contains(finalizerName, "obol.org") {
		t.Errorf("finalizer should be in obol.org domain, got %s", finalizerName)
	}
}

// --- helpers ---

func makeServiceOffer(name, ns string, spec map[string]interface{}) *unstructured.Unstructured {
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

func getConditions(t *testing.T, so *unstructured.Unstructured) []interface{} {
	t.Helper()
	conditions, _, _ := unstructured.NestedSlice(so.Object, "status", "conditions")
	return conditions
}

func assertCondition(t *testing.T, raw interface{}, wantType, wantStatus, wantMsg string) {
	t.Helper()
	cond, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatal("condition is not a map")
	}
	if cond["type"] != wantType {
		t.Errorf("type: got %s, want %s", cond["type"], wantType)
	}
	if cond["status"] != wantStatus {
		t.Errorf("status: got %s, want %s", cond["status"], wantStatus)
	}
	if cond["message"] != wantMsg {
		t.Errorf("message: got %s, want %s", cond["message"], wantMsg)
	}
}
