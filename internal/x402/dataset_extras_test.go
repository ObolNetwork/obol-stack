package x402

import (
	"testing"

	x402types "github.com/x402-foundation/x402/go/types"
)

func TestMergeDatasetExtras_Noop_NonDatasetRule(t *testing.T) {
	req := x402types.PaymentRequirements{Extra: map[string]any{"name": "USDC"}}
	rule := &RouteRule{}

	mergeDatasetExtras(&req, rule)

	if _, ok := req.Extra["dataset"]; ok {
		t.Error("non-dataset rule must not add a dataset extra")
	}
	if got := req.Extra["name"]; got != "USDC" {
		t.Errorf("non-dataset merge clobbered existing extra.name: %v", got)
	}
}

func TestMergeDatasetExtras_AddsAllDatasetFields(t *testing.T) {
	// Preserve an asset EIP-712 key already on Extra to prove the merge is
	// additive (mirrors how mergeAgentExtras must not clobber extra.name).
	req := x402types.PaymentRequirements{Extra: map[string]any{"name": "USDC"}}
	rule := &RouteRule{
		DatasetManifestHash: "abc123",
		DatasetVersion:      "2",
		DatasetFileHash:     "def456",
		DatasetSizeBytes:    1048576,
	}

	mergeDatasetExtras(&req, rule)

	dataset, ok := req.Extra["dataset"].(map[string]interface{})
	if !ok {
		t.Fatalf("extra.dataset wrong type: %T", req.Extra["dataset"])
	}
	if got := dataset["manifestHash"]; got != "abc123" {
		t.Errorf("dataset.manifestHash = %v, want abc123", got)
	}
	if got := dataset["version"]; got != "2" {
		t.Errorf("dataset.version = %v, want 2", got)
	}
	if got := dataset["fileHash"]; got != "def456" {
		t.Errorf("dataset.fileHash = %v, want def456", got)
	}
	if got := dataset["sizeBytes"]; got != int64(1048576) {
		t.Errorf("dataset.sizeBytes = %v (%T), want int64(1048576)", got, got)
	}
	if got := req.Extra["name"]; got != "USDC" {
		t.Errorf("dataset merge clobbered existing extra.name: %v", got)
	}
}

func TestMergeDatasetExtras_InitialisesNilExtra(t *testing.T) {
	// BuildV2RequirementWithAsset always returns a non-nil Extra, but
	// mergeDatasetExtras must still cope with a nil map for callers that
	// build PaymentRequirements directly (e.g. tests).
	req := x402types.PaymentRequirements{}
	rule := &RouteRule{DatasetManifestHash: "abc123"}

	mergeDatasetExtras(&req, rule)

	if req.Extra == nil {
		t.Fatal("Extra not initialised")
	}
	dataset, ok := req.Extra["dataset"].(map[string]interface{})
	if !ok || dataset["manifestHash"] != "abc123" {
		t.Errorf("dataset.manifestHash missing: %+v", req.Extra)
	}
}

func TestMergeDatasetExtras_OmitsZeroValuedFields(t *testing.T) {
	// Only a manifestHash is set; the empty version/fileHash and zero
	// sizeBytes must be omitted so buyers don't see blank keys.
	req := x402types.PaymentRequirements{}
	rule := &RouteRule{DatasetManifestHash: "abc123"}

	mergeDatasetExtras(&req, rule)

	dataset := req.Extra["dataset"].(map[string]interface{})
	for _, k := range []string{"version", "fileHash", "sizeBytes"} {
		if _, ok := dataset[k]; ok {
			t.Errorf("dataset.%s should be omitted when unset, got %v", k, dataset[k])
		}
	}
}
