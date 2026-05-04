package openclaw

import "testing"

func TestRankModels_OpenClawWrapper_PreservesConfiguredOrder(t *testing.T) {
	primary, fallbacks := rankModels([]string{
		"llama3.2:1b",
		"qwen3.5:9b",
		"claude-opus-4-7",
	})
	if primary != "openai/llama3.2:1b" {
		t.Fatalf("primary: got %q, want openai/llama3.2:1b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "openai/qwen3.5:9b" || fallbacks[1] != "openai/claude-opus-4-7" {
		t.Fatalf("fallbacks: got %v", fallbacks)
	}
}

func TestRankModels_OpenClawWrapper_PrefixesConfiguredCloudPrimary(t *testing.T) {
	primary, _ := rankModels([]string{
		"claude-opus-4-7",
		"qwen3.5:9b",
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
