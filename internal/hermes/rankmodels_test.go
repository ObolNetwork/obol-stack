package hermes

import "testing"

// TestRankModels_HermesWrapper_PrefersLargerLocalModel encodes the regression
// from the colleague's screenshot: Hermes was deploying with `llama3.2:1b` as
// the default model, which then parroted its own tool list back on every
// "hello" prompt. The fix moved capability ranking into model.Rank; this test
// just confirms the Hermes-side wrapper still calls into it correctly and
// keeps the openai/-prefix-stripping shape intact.
func TestRankModels_HermesWrapper_PrefersLargerLocalModel(t *testing.T) {
	primary, fallbacks := rankModels([]string{
		"openai/llama3.2:1b",
		"openai/qwen3.5:9b",
		"openai/llama3.2:3b",
	})
	if primary != "qwen3.5:9b" {
		t.Fatalf("primary: got %q, want qwen3.5:9b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "llama3.2:3b" || fallbacks[1] != "llama3.2:1b" {
		t.Fatalf("fallbacks: got %v, want [llama3.2:3b llama3.2:1b]", fallbacks)
	}
}

func TestRankModels_HermesWrapper_PrefersClaudeOverLocal(t *testing.T) {
	primary, _ := rankModels([]string{
		"qwen3.5:9b",
		"anthropic/claude-opus-4-7",
		"llama3.2:1b",
	})
	if primary != "claude-opus-4-7" {
		t.Fatalf("primary: got %q, want claude-opus-4-7", primary)
	}
}
