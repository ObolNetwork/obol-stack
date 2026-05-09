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
}

func TestDecodeAgentJSON_RejectsGarbage(t *testing.T) {
	if _, err := decodeAgentJSON("not json"); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestSellAgentCommand_FlagShape(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	agent := findSubcommand(t, cmd, "agent")
	flags := flagMap(agent)

	requireFlags(t, flags, "pay-to", "wallet", "chain", "token", "price", "per-request", "path", "max-timeout", "no-register")

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
