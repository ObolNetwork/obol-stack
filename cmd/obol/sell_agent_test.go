package main

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestDecodeAgentJSON_FullDocument(t *testing.T) {
	raw := `{
  "apiVersion": "obol.org/v1alpha1",
  "kind": "Agent",
  "metadata": {"name": "quant", "namespace": "agent-quant", "generation": 4},
  "spec": {
    "runtime": "hermes",
    "model": "qwen3.5:9b",
    "skills": ["addresses", "gas"],
    "objective": "EVM analyst"
  },
  "status": {
    "phase": "Ready",
    "walletAddress": "0xabcdef0123456789abcdef0123456789abcdef01",
    "endpoint": "http://hermes.agent-quant.svc.cluster.local:8642"
  }
}`
	got, err := decodeAgentJSON(raw)
	if err != nil {
		t.Fatalf("decodeAgentJSON: %v", err)
	}
	if got.Name != "quant" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Namespace != "agent-quant" {
		t.Errorf("namespace = %q", got.Namespace)
	}
	if got.Objective != "EVM analyst" {
		t.Errorf("objective = %q", got.Objective)
	}
	if got.WalletAddress != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("walletAddress = %q", got.WalletAddress)
	}
	if got.Runtime != "hermes" {
		t.Errorf("runtime = %q", got.Runtime)
	}
	if got.Model != "qwen3.5:9b" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "addresses" {
		t.Errorf("skills = %v", got.Skills)
	}
}

func TestDecodeAgentJSON_StatusFieldsAreOptional(t *testing.T) {
	// Brand-new agents may not have status.walletAddress yet (controller
	// hasn't reconciled). Decoder must tolerate that and let the caller
	// decide on a fallback (host remote-signer, --pay-to flag, etc).
	raw := `{"metadata":{"name":"new","namespace":"agent-new"},"spec":{"skills":["addresses"]}}`
	got, err := decodeAgentJSON(raw)
	if err != nil {
		t.Fatalf("decodeAgentJSON: %v", err)
	}
	if got.WalletAddress != "" {
		t.Errorf("expected empty walletAddress, got %q", got.WalletAddress)
	}
	if got.Objective != "" {
		t.Errorf("expected empty objective, got %q", got.Objective)
	}
	if got.Runtime != "hermes" {
		t.Errorf("runtime default = %q, want hermes", got.Runtime)
	}
}

func TestDecodeAgentJSON_ModelFallsBackToStatusPinnedModel(t *testing.T) {
	raw := `{
  "metadata": {"name": "quant", "namespace": "agent-quant"},
  "spec": {"skills": ["addresses"]},
  "status": {"pinnedModel": "paid/aeon"}
}`
	got, err := decodeAgentJSON(raw)
	if err != nil {
		t.Fatalf("decodeAgentJSON: %v", err)
	}
	if got.Model != "paid/aeon" {
		t.Errorf("model = %q, want paid/aeon", got.Model)
	}
}

func TestDecodeAgentJSON_RejectsGarbage(t *testing.T) {
	if _, err := decodeAgentJSON("not json"); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestPickAgentDefault(t *testing.T) {
	tests := []struct {
		name       string
		configured []string
		wantModel  string
		wantErr    bool
	}{
		{
			name:       "concrete paid entry at head wins",
			configured: []string{"paid/aeon", "qwen36-deep", "paid/*"},
			wantModel:  "paid/aeon",
		},
		{
			name:       "wildcard skipped, falls through to unpaid",
			configured: []string{"paid/*", "qwen36-deep"},
			wantModel:  "qwen36-deep",
		},
		{
			name:       "wildcard-only list is an error",
			configured: []string{"paid/*"},
			wantErr:    true,
		},
		{
			name:       "concrete paid entry is the only entry",
			configured: []string{"paid/aeon"},
			wantModel:  "paid/aeon",
		},
		{
			name:       "empty list is an error",
			configured: []string{},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickAgentDefault(tt.configured)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got model %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantModel {
				t.Errorf("pickAgentDefault = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestAgentOfferRegistrationMetadata_AdvertisesRuntimeModelAndPrice(t *testing.T) {
	got := agentOfferRegistrationMetadata(&agentRefForSale{
		Runtime: "hermes",
		Model:   "qwen3.5:9b",
	}, "10", "OBOL", "ethereum")

	want := map[string]string{
		"runtime":     "hermes",
		"model":       "qwen3.5:9b",
		"pricingUnit": "agent-turn",
		"x402Price":   "10",
		"x402Asset":   "OBOL",
		"x402Network": "ethereum",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("metadata[%s] = %q, want %q (full=%v)", k, got[k], v, got)
		}
	}
}

func TestAgentOfferRegistrationMetadata_DefaultsRuntimeHermes(t *testing.T) {
	got := agentOfferRegistrationMetadata(nil, "0.001", "usdc", "base-sepolia")
	if got["runtime"] != "hermes" {
		t.Errorf("runtime = %q, want hermes", got["runtime"])
	}
	if got["x402Asset"] != "USDC" {
		t.Errorf("x402Asset = %q, want USDC", got["x402Asset"])
	}
	if _, ok := got["model"]; ok {
		t.Errorf("model should be omitted when unknown: %v", got)
	}
}

func TestResolveAgentOfferDescription_NoDescriptionFlagStaysEmpty(t *testing.T) {
	// Regression: sell_agent.go used to default an omitted --description to
	// agent.Objective — leaking operator-authored system-prompt text (tool
	// addresses, wallet details, operational instructions) into the public
	// storefront/registration description. resolveAgentOfferDescription must
	// never manufacture a non-empty description; it stays empty and the
	// controller's own generic fallback applies.
	objective := "You are a focused sub-agent. Trade using wallet 0xDEADBEEF via tool http://internal.example/mcp"
	got := resolveAgentOfferDescription("", objective)
	if got != "" {
		t.Fatalf("resolveAgentOfferDescription(\"\", objective) = %q, want empty (must not fall back to the objective)", got)
	}
}

func TestResolveAgentOfferDescription_PassesThroughExplicitFlag(t *testing.T) {
	got := resolveAgentOfferDescription("  Quant trading agent  ", "unrelated objective text")
	if got != "Quant trading agent" {
		t.Errorf("resolveAgentOfferDescription = %q, want trimmed explicit value", got)
	}
}

func TestSellAgentCommand_FlagShape(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	agent := findSubcommand(t, cmd, "agent")
	flags := flagMap(agent)

	requireFlags(t, flags, "pay-to", "wallet", "chain", "token", "price", "per-request", "path", "max-timeout", "no-register")

	// The --description Usage text used to advertise the leaky
	// objective-fallback default; it must not document that anymore.
	if desc, ok := flags["description"].(*cli.StringFlag); ok && strings.Contains(desc.Usage, "objective") {
		t.Errorf("description usage still documents the objective fallback: %q", desc.Usage)
	}

	if chain, ok := flags["chain"].(*cli.StringFlag); ok && chain.Value != "base" {
		t.Errorf("chain default = %q, want base", chain.Value)
	}
	if timeout, ok := flags["max-timeout"].(*cli.IntFlag); ok && timeout.Value != 300 {
		t.Errorf("max-timeout default = %d, want 300", timeout.Value)
	}

	// Description should mention chat-completions / agent-typed offers so a
	// user grepping `obol sell agent --help` quickly grasps how this differs
	// from `obol sell http`.
	if !strings.Contains(agent.Description, "type=agent") {
		t.Errorf("description should mention type=agent: %q", agent.Description)
	}
}
