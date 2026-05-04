package model

import "testing"

func TestRank_PreservesConfiguredOrder(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"llama3.2:1b",
		"qwen3.5:9b",
		"claude-opus-4-7",
	})
	if primary != "llama3.2:1b" {
		t.Fatalf("primary: got %q, want llama3.2:1b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "qwen3.5:9b" || fallbacks[1] != "claude-opus-4-7" {
		t.Fatalf("fallbacks: got %v, want [qwen3.5:9b claude-opus-4-7]", fallbacks)
	}
}

func TestRank_DoesNotInferProviderPrecedence(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"gpt-4o",
		"claude-opus-4-7",
		"o3",
	})
	if primary != "gpt-4o" {
		t.Fatalf("primary: got %q, want gpt-4o", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "claude-opus-4-7" || fallbacks[1] != "o3" {
		t.Fatalf("fallbacks: got %v, want [claude-opus-4-7 o3]", fallbacks)
	}
}

func TestRank_DoesNotInferSizePrecedence(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"llama3.2:1b",
		"qwen3.5:9b",
		"mixtral:8x7b",
	})
	if primary != "llama3.2:1b" {
		t.Fatalf("primary: got %q, want llama3.2:1b", primary)
	}
	if len(fallbacks) != 2 || fallbacks[0] != "qwen3.5:9b" || fallbacks[1] != "mixtral:8x7b" {
		t.Fatalf("fallbacks: got %v, want [qwen3.5:9b mixtral:8x7b]", fallbacks)
	}
}

func TestRank_EmbeddingModelLast(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"nomic-embed-text",
		"llama3.2:1b",
		"text-embedding-3-large",
		"qwen3.5:9b",
	})
	if primary != "llama3.2:1b" {
		t.Fatalf("primary: got %q, want llama3.2:1b", primary)
	}
	want := []string{"qwen3.5:9b", "nomic-embed-text", "text-embedding-3-large"}
	if len(fallbacks) != len(want) {
		t.Fatalf("fallbacks: got %v, want %v", fallbacks, want)
	}
	for i := range want {
		if fallbacks[i] != want[i] {
			t.Fatalf("fallbacks: got %v, want %v", fallbacks, want)
		}
	}
}

func TestRank_AllEmbeddingModelsPreserveOrder(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"nomic-embed-text",
		"text-embedding-3-large",
	})
	if primary != "nomic-embed-text" {
		t.Fatalf("primary: got %q, want nomic-embed-text", primary)
	}
	if len(fallbacks) != 1 || fallbacks[0] != "text-embedding-3-large" {
		t.Fatalf("fallbacks: got %v, want [text-embedding-3-large]", fallbacks)
	}
}

// TestRank_PreservesProviderPrefixOnOutput documents the contract relied on by
// internal/hermes and internal/openclaw: Rank returns input strings unchanged.
// The agent round-trips the returned primary back to LiteLLM as the `model`
// field on chat-completions; stripping here would mismatch LiteLLM model_name
// and surface as 400 "no healthy deployments".
func TestRank_PreservesProviderPrefixOnOutput(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{
			"anthropic/-prefixed first",
			[]string{"anthropic/claude-opus-4-7", "qwen3.5:9b"},
			"anthropic/claude-opus-4-7",
		},
		{
			"openai/-prefixed first",
			[]string{"openai/gpt-4o", "qwen3.5:9b"},
			"openai/gpt-4o",
		},
		{
			"custom namespaced entry round-trips",
			[]string{"custom/qa-vllm/qwen36-fast"},
			"custom/qa-vllm/qwen36-fast",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary, _ := Rank(tc.in)
			if primary != tc.want {
				t.Fatalf("Rank(%v): got %q, want %q (must round-trip unchanged)", tc.in, primary, tc.want)
			}
		})
	}
}

func TestRank_Empty(t *testing.T) {
	primary, fallbacks := Rank(nil)
	if primary != "" || len(fallbacks) != 0 {
		t.Fatalf("Rank(nil): got %q,%v, want empty,nil", primary, fallbacks)
	}
}
