package hermes

import "testing"

// Contract: LiteLLM model_name strings come in, the SAME strings come back
// out in configured order. The agent must be able to round-trip the returned
// primary back to LiteLLM without modification.
func TestRankModels_HermesWrapper_PreservesConfiguredOrder(t *testing.T) {
	primary, fallbacks := rankModels([]string{
		"llama3.2:1b",
		"llama3.1:8b",
		"claude-opus-4-7",
	})
	if primary != "llama3.2:1b" {
		t.Fatalf("primary: got %q, want llama3.2:1b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "llama3.1:8b" || fallbacks[1] != "claude-opus-4-7" {
		t.Fatalf("fallbacks: got %v, want [llama3.1:8b claude-opus-4-7]", fallbacks)
	}
}

// TestRankModels_HermesWrapper_PreservesProviderPrefixIfPresent guards the
// round-trip property. If something upstream (a wildcard expansion, a
// hand-edited ConfigMap, an older release) writes a `provider/model` shape
// into LiteLLM, we still need to return the EXACT string so the agent's
// chat-completion call matches by literal string. Stripping at this layer
// was the double-strip bug fixed in ca820c9 — this test guards against
// reintroducing it.
func TestRankModels_HermesWrapper_PreservesProviderPrefixIfPresent(t *testing.T) {
	primary, _ := rankModels([]string{
		"llama3.1:8b",
		"anthropic/claude-opus-4-7",
		"openai/gpt-4o",
	})
	if primary != "llama3.1:8b" {
		t.Fatalf("primary: got %q, want llama3.1:8b", primary)
	}
}

// TestRankModels_HermesWrapper_CustomNamespacedEntryRoundTrips guards the
// specific shape that broke flow-14: a legacy `custom/<name>/<model>` entry
// that double-stripping would mangle to `<model>`, leaving the agent calling
// LiteLLM with a key that no longer matched the registered route.
func TestRankModels_HermesWrapper_CustomNamespacedEntryRoundTrips(t *testing.T) {
	in := []string{"custom/qa-vllm/qwen36-fast"}
	primary, _ := rankModels(in)
	if primary != in[0] {
		t.Fatalf("primary: got %q, want %q (must round-trip unchanged)", primary, in[0])
	}
}
