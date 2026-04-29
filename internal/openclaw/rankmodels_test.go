package openclaw

import "testing"

// TestRankModels_OpenClawWrapper_PrefersLargerLocalModel — same regression
// guard as the Hermes side, but exercising the openai/-prefix that OpenClaw
// adds for LiteLLM routing through the openai-compatible provider slot.
func TestRankModels_OpenClawWrapper_PrefersLargerLocalModel(t *testing.T) {
	primary, fallbacks := rankModels([]string{
		"llama3.2:1b",
		"qwen3.5:9b",
		"llama3.2:3b",
	})
	if primary != "openai/qwen3.5:9b" {
		t.Fatalf("primary: got %q, want openai/qwen3.5:9b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "openai/llama3.2:3b" || fallbacks[1] != "openai/llama3.2:1b" {
		t.Fatalf("fallbacks: got %v", fallbacks)
	}
}

func TestRankModels_OpenClawWrapper_KeepsOpenAIPrefixOnCloudPicks(t *testing.T) {
	// Cloud models also get the openai/ prefix in OpenClaw because LiteLLM
	// routes them through its openai-compatible adapter slot. The wrapper
	// must wrap regardless of whether the underlying pick is cloud or local.
	primary, _ := rankModels([]string{
		"qwen3.5:9b",
		"claude-opus-4-7",
	})
	if primary != "openai/claude-opus-4-7" {
		t.Fatalf("primary: got %q, want openai/claude-opus-4-7", primary)
	}
}

func TestRankModels_OpenClawWrapper_EmptyInput(t *testing.T) {
	primary, fallbacks := rankModels(nil)
	if primary != "" || len(fallbacks) != 0 {
		t.Fatalf("rankModels(nil): got %q,%v, want empty,nil", primary, fallbacks)
	}
}
