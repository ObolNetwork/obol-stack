package model

import (
	"testing"
)

func TestRank_PrefersLargerLocalModelOver1B(t *testing.T) {
	// The exact regression that produced "hello" → wall-of-text on the
	// colleague's screenshot: a 1B model winning over qwen3.5:9b just because
	// Ollama happened to list it first.
	primary, fallbacks := Rank([]string{
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

func TestRank_CloudOutranksLocal(t *testing.T) {
	primary, fallbacks := Rank([]string{
		"qwen3.5:9b",
		"claude-opus-4-7",
		"llama3.2:1b",
	})
	if primary != "claude-opus-4-7" {
		t.Fatalf("primary: got %q, want claude-opus-4-7", primary)
	}
	// fallbacks: cloud-then-local order preserved
	if len(fallbacks) != 2 || fallbacks[0] != "qwen3.5:9b" || fallbacks[1] != "llama3.2:1b" {
		t.Fatalf("fallbacks: got %v", fallbacks)
	}
}

func TestRank_CloudInternalOrdering(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"opus over sonnet", []string{"claude-sonnet-4-6", "claude-opus-4-7"}, "claude-opus-4-7"},
		{"sonnet over haiku", []string{"claude-haiku-4-5", "claude-sonnet-4-6"}, "claude-sonnet-4-6"},
		{"gpt-5 over gpt-4", []string{"gpt-4o", "gpt-5"}, "gpt-5"},
		{"opus over gpt", []string{"gpt-5", "claude-opus-4-7"}, "claude-opus-4-7"},
		{"provider prefix tolerated", []string{"openai/gpt-4o", "anthropic/claude-opus-4-7"}, "anthropic/claude-opus-4-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Rank(tc.in)
			if got != tc.want {
				t.Fatalf("Rank(%v): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRank_LocalParameterParsing(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"plain b suffix", []string{"llama3.2:1b", "llama3.2:3b"}, "llama3.2:3b"},
		{"two-digit count", []string{"qwen3:14b", "qwen3.5:9b"}, "qwen3:14b"},
		{"mixtral 8x7b", []string{"qwen3.5:9b", "mixtral:8x7b"}, "mixtral:8x7b"},
		{"235b cloud variant", []string{"qwen3.5:9b", "qwen3-vl:235b-cloud"}, "qwen3-vl:235b-cloud"},
		{"untagged family lookup", []string{"qwen3.5:9b", "llama3.3"}, "llama3.3"}, // family default 70 > 9
		// Regression on regression: a `:0.6b` Ollama tag must NOT fall through
		// to the qwen3 family default (~14B) — that would mistakenly outrank
		// qwen3.5:9b. The decimal-aware regex parses it as 0.6 directly.
		{"decimal size below 1b", []string{"qwen3:0.6b", "qwen3.5:9b"}, "qwen3.5:9b"},
		{"decimal size 1.5b", []string{"qwen3.5:9b", "smol:1.5b"}, "qwen3.5:9b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Rank(tc.in)
			if got != tc.want {
				t.Fatalf("Rank(%v): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRank_DeterministicTiebreak(t *testing.T) {
	// Two models the same size — must sort alphabetically so successive runs
	// don't flip the primary.
	primary1, _ := Rank([]string{"foo:7b", "bar:7b"})
	primary2, _ := Rank([]string{"bar:7b", "foo:7b"})
	if primary1 != primary2 {
		t.Fatalf("non-deterministic: %q vs %q", primary1, primary2)
	}
	if primary1 != "bar:7b" {
		t.Fatalf("expected alphabetical tiebreak, got %q", primary1)
	}
}

func TestRank_EmbeddingModelLast(t *testing.T) {
	// nomic-embed-text isn't a chat model — must never become the agent's
	// default if anything else is present.
	primary, _ := Rank([]string{"nomic-embed-text", "llama3.2:1b"})
	if primary != "llama3.2:1b" {
		t.Fatalf("primary: got %q, want llama3.2:1b (embedding model picked instead)", primary)
	}
}

func TestRank_Empty(t *testing.T) {
	primary, fallbacks := Rank(nil)
	if primary != "" || len(fallbacks) != 0 {
		t.Fatalf("Rank(nil): got %q,%v, want empty,nil", primary, fallbacks)
	}
}

func TestIsCloudModel(t *testing.T) {
	cloud := []string{"claude-opus-4-7", "anthropic/claude-3-5-sonnet", "gpt-4o", "openai/gpt-5", "o1-preview", "o3-mini"}
	local := []string{"llama3.2:1b", "qwen3.5:9b", "mixtral:8x7b", "deepseek-r1:14b", "nomic-embed-text"}
	for _, n := range cloud {
		if !IsCloudModel(n) {
			t.Errorf("IsCloudModel(%q) = false, want true", n)
		}
	}
	for _, n := range local {
		if IsCloudModel(n) {
			t.Errorf("IsCloudModel(%q) = true, want false", n)
		}
	}
}
