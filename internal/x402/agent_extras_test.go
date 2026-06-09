package x402

import (
	"reflect"
	"testing"

	x402types "github.com/x402-foundation/x402/go/types"
)

func TestMergeAgentExtras_Noop_NonAgentRule(t *testing.T) {
	req := x402types.PaymentRequirements{Extra: map[string]any{"name": "USDC"}}
	rule := &RouteRule{}

	mergeAgentExtras(&req, rule)

	if _, ok := req.Extra["agentModel"]; ok {
		t.Error("non-agent rule must not add agentModel")
	}
	if _, ok := req.Extra["agentSkills"]; ok {
		t.Error("non-agent rule must not add agentSkills")
	}
	if got := req.Extra["name"]; got != "USDC" {
		t.Errorf("non-agent merge clobbered existing extra.name: %v", got)
	}
}

func TestMergeAgentExtras_AddsAllAgentFields(t *testing.T) {
	req := x402types.PaymentRequirements{Extra: map[string]any{}}
	rule := &RouteRule{
		AgentModel:   "qwen3.5:9b",
		AgentSkills:  []string{"addresses", "gas"},
		AgentRuntime: "hermes",
	}

	mergeAgentExtras(&req, rule)

	if got := req.Extra["agentModel"]; got != "qwen3.5:9b" {
		t.Errorf("agentModel = %v, want qwen3.5:9b", got)
	}
	if got := req.Extra["agentRuntime"]; got != "hermes" {
		t.Errorf("agentRuntime = %v", got)
	}
	skills, ok := req.Extra["agentSkills"].([]any)
	if !ok {
		t.Fatalf("agentSkills wrong type: %T", req.Extra["agentSkills"])
	}
	want := []any{"addresses", "gas"}
	if !reflect.DeepEqual(skills, want) {
		t.Errorf("agentSkills = %v, want %v", skills, want)
	}
}

func TestMergeAgentExtras_InitialisesNilExtra(t *testing.T) {
	// BuildV2RequirementWithAsset always returns a non-nil Extra, but
	// mergeAgentExtras must still cope with a nil map for callers that
	// build PaymentRequirements directly (e.g. tests).
	req := x402types.PaymentRequirements{}
	rule := &RouteRule{AgentModel: "qwen3.5:9b"}

	mergeAgentExtras(&req, rule)

	if req.Extra == nil {
		t.Fatal("Extra not initialised")
	}
	if req.Extra["agentModel"] != "qwen3.5:9b" {
		t.Errorf("agentModel missing: %+v", req.Extra)
	}
}
