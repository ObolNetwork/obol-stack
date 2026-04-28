package hermes

import "testing"

// TestRankModels_HermesWrapper_PrefersLargerLocalModel encodes the regression
// from the colleague's screenshot: Hermes was deploying with `llama3.2:1b` as
// the default model, which then parroted its own tool list back on every
// "hello" prompt. The fix moved capability ranking into model.Rank; this test
// just confirms the Hermes-side wrapper still calls into it correctly.
//
// Contract: bare LiteLLM model_name strings come in, the SAME bare strings
// come back out — no provider-prefix stripping at this layer. The agent must
// be able to round-trip the returned primary back to LiteLLM without
// modification.
func TestRankModels_HermesWrapper_PrefersLargerLocalModel(t *testing.T) {
	primary, fallbacks := rankModels([]string{
		"llama3.2:1b",
		"qwen3.5:9b",
		"llama3.2:3b",
	})
	if primary != "qwen3.5:9b" {
		t.Fatalf("primary: got %q, want qwen3.5:9b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "llama3.2:3b" || fallbacks[1] != "llama3.2:1b" {
		t.Fatalf("fallbacks: got %v, want [llama3.2:3b llama3.2:1b]", fallbacks)
	}
}

// TestRankModels_HermesWrapper_PrefersClaudeOverLocal exercises the cloud
// tier. Cloud entries written by buildModelEntries are bare (e.g.
// `claude-opus-4-7`, not `anthropic/claude-opus-4-7`), and the wrapper must
// preserve that.
func TestRankModels_HermesWrapper_PrefersClaudeOverLocal(t *testing.T) {
	primary, _ := rankModels([]string{
		"qwen3.5:9b",
		"claude-opus-4-7",
		"llama3.2:1b",
	})
	if primary != "claude-opus-4-7" {
		t.Fatalf("primary: got %q, want claude-opus-4-7", primary)
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
		"anthropic/claude-opus-4-7",
		"openai/gpt-4o",
		"qwen3.5:9b",
	})
	if primary != "anthropic/claude-opus-4-7" {
		t.Fatalf("primary: got %q, want anthropic/claude-opus-4-7 (unstripped)", primary)
	}
}

// TestRankModels_HermesWrapper_CustomNamespacedEntryRoundTrips guards the
// specific shape that broke flow-14: a legacy `custom/<name>/<model>` entry
// that double-stripping would mangle to `<model>`, leaving the agent calling
// LiteLLM with a key that no longer matched the registered route.
func TestRankModels_HermesWrapper_CustomNamespacedEntryRoundTrips(t *testing.T) {
	in := []string{"custom/spark1-vllm/qwen36-fast"}
	primary, _ := rankModels(in)
	if primary != in[0] {
		t.Fatalf("primary: got %q, want %q (must round-trip unchanged)", primary, in[0])
	}
}
