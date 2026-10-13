package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// A LiteLLM-backed http offer serves the seller-pinned spec.model.name; an
// offer without one is held at ModelReady=False with the fix in the message
// (the verifier would refuse every request anyway).
func TestReconcileModel_LiteLLMOfferNeedsPinnedModel(t *testing.T) {
	c := &Controller{}
	offer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type:     "http",
		Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm", Namespace: "llm", Port: 4000},
	}}
	offer.Name, offer.Namespace = "my-llm", "llm"

	var status monetizeapi.ServiceOfferStatus
	if err := c.reconcileModel(&status, offer); err != nil {
		t.Fatal(err)
	}
	if isConditionTrue(status, "ModelReady") {
		t.Fatal("LiteLLM offer without a model must not be ModelReady")
	}
	if msg := status.Conditions[0].Message; !strings.Contains(msg, "obol sell update my-llm -n llm --model") {
		t.Fatalf("ModelReady message should carry the fix, got %q", msg)
	}

	offer.Spec.Model = monetizeapi.ServiceOfferModel{Name: "qwen3.5:9b", Runtime: "litellm"}
	status = monetizeapi.ServiceOfferStatus{}
	if err := c.reconcileModel(&status, offer); err != nil {
		t.Fatal(err)
	}
	if !isConditionTrue(status, "ModelReady") {
		t.Fatalf("pinned LiteLLM offer should be ModelReady, got %+v", status.Conditions)
	}

	// Other http upstreams don't need a model.
	offer.Spec.Model = monetizeapi.ServiceOfferModel{}
	offer.Spec.Upstream.Service = "httpbin"
	status = monetizeapi.ServiceOfferStatus{}
	if err := c.reconcileModel(&status, offer); err != nil {
		t.Fatal(err)
	}
	if !isConditionTrue(status, "ModelReady") {
		t.Fatal("non-LiteLLM http offer should skip model preparation")
	}
}
