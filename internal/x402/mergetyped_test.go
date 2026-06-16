package x402

import (
	"testing"

	x402types "github.com/x402-foundation/x402/go/types"
)

// TestMergeTypedExtras_ProfileDriven verifies the 402 discovery metadata is
// selected by the offer type's integrity profile (offerkind), not by which
// RouteRule fields happen to be populated.
func TestMergeTypedExtras_ProfileDriven(t *testing.T) {
	// A signed-log dataset route surfaces its content commitment.
	req := x402types.PaymentRequirements{}
	mergeTypedExtras(&req, &RouteRule{OfferType: "dataset", DatasetManifestHash: "abc", DatasetFileHash: "def"})
	if _, ok := req.Extra["dataset"]; !ok {
		t.Error("dataset route should surface extra.dataset")
	}

	// A skill route surfaces its bundle identity.
	req = x402types.PaymentRequirements{}
	mergeTypedExtras(&req, &RouteRule{OfferType: "skill", SkillName: "buy-x402", SkillSHA256: "deadbeef"})
	if _, ok := req.Extra["skill"]; !ok {
		t.Error("skill route should surface extra.skill")
	}

	// An agent route surfaces its model.
	req = x402types.PaymentRequirements{}
	mergeTypedExtras(&req, &RouteRule{OfferType: "agent", AgentModel: "qwen"})
	if req.Extra["agentModel"] != "qwen" {
		t.Errorf("agent route should surface agentModel, got %v", req.Extra["agentModel"])
	}

	// An inference route carrying a STRAY dataset field surfaces NOTHING: the
	// dispatch is profile-driven (inference is payment-only, ContentNone), so a
	// leaked field is ignored. This is stronger than the old unconditional
	// merge, yet identical for every real route — serviceoffer_source.go only
	// populates a type's fields when the offer is that type.
	req = x402types.PaymentRequirements{}
	mergeTypedExtras(&req, &RouteRule{OfferType: "inference", DatasetManifestHash: "should-be-ignored"})
	if _, ok := req.Extra["dataset"]; ok {
		t.Error("inference route must not surface dataset extras even if a field leaks")
	}
	if len(req.Extra) != 0 {
		t.Errorf("payment-only route should attach no typed extras, got %v", req.Extra)
	}
}
