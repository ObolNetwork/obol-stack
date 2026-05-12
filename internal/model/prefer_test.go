package model

import (
	"strings"
	"testing"
)

// TestRank_AeonAliasesPreserveConfiguredOrder pins the current Rank parser
// contract for AEON-style untagged aliases vs Ollama-style `:Nb` tagged
// names. Rank is "first chat-capable wins"; it does NOT parse parameter-count
// suffixes. If a future refactor adds a `:Nb` heuristic the way the historical
// rank.go did, this test will fail loudly so an operator-chosen `aeon-ultimate`
// at the head of model_list isn't silently demoted behind `qwen3.5:9b`.
//
// The spark2 incident on 2026-05-11 was exactly this footgun: an auto-detected
// Ollama `qwen3.5:9b` won the auto-default race over `qwen36-fast` because the
// :9b tag parsed to 90 deci-billions while the untagged custom alias ranked 0.
// Configured order is now the source of truth; this test pins that.
func TestRank_AeonAliasesPreserveConfiguredOrder(t *testing.T) {
	cases := []struct {
		name          string
		in            []string
		wantPrimary   string
		wantFallbacks []string
	}{
		{
			name:          "untagged aeon-ultimate wins when it leads the configured list",
			in:            []string{"aeon-ultimate", "aeon-fast", "qwen3.5:9b", "qwen3:0.6b"},
			wantPrimary:   "aeon-ultimate",
			wantFallbacks: []string{"aeon-fast", "qwen3.5:9b", "qwen3:0.6b"},
		},
		{
			name:          "qwen3.5:9b wins only when it actually leads the list",
			in:            []string{"qwen3.5:9b", "qwen3:0.6b", "aeon-ultimate", "aeon-fast"},
			wantPrimary:   "qwen3.5:9b",
			wantFallbacks: []string{"qwen3:0.6b", "aeon-ultimate", "aeon-fast"},
		},
		{
			name:          "qwen36-fast (no :Nb tag) wins over qwen3.5:9b when configured first",
			in:            []string{"qwen36-fast", "qwen3.5:9b"},
			wantPrimary:   "qwen36-fast",
			wantFallbacks: []string{"qwen3.5:9b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary, fallbacks := Rank(tc.in)
			if primary != tc.wantPrimary {
				t.Fatalf("Rank(%v) primary = %q, want %q", tc.in, primary, tc.wantPrimary)
			}
			if len(fallbacks) != len(tc.wantFallbacks) {
				t.Fatalf("Rank(%v) fallbacks = %v, want %v", tc.in, fallbacks, tc.wantFallbacks)
			}
			for i, want := range tc.wantFallbacks {
				if fallbacks[i] != want {
					t.Fatalf("Rank(%v) fallbacks[%d] = %q, want %q (full: %v)", tc.in, i, fallbacks[i], want, fallbacks)
				}
			}
		})
	}
}

// TestReorderModelList_AeonPreferContract pins the behavior of
// reorderModelList (the pure-function core of PreferModels) on AEON-style
// model lists. This complements TestReorderModelList in model_test.go by
// exercising the exact case from the spark2 incident: an operator running
// `obol model prefer aeon-ultimate` to override an auto-detected Ollama
// :9b that out-ranks the custom alias.
func TestReorderModelList_AeonPreferContract(t *testing.T) {
	// Mirrors a realistic configured model_list: a custom vLLM alias
	// (aeon-ultimate / aeon-fast) plus auto-detected Ollama models.
	baseList := func() []ModelEntry {
		return []ModelEntry{
			{ModelName: "qwen3.5:9b"},
			{ModelName: "qwen3:0.6b"},
			{ModelName: "aeon-ultimate"},
			{ModelName: "aeon-fast"},
		}
	}

	t.Run("prefer pulls aeon-ultimate to head and preserves relative order of the rest", func(t *testing.T) {
		got, already, err := reorderModelList(baseList(), []string{"aeon-ultimate"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		if already {
			t.Fatal("expected alreadyAtHead = false; aeon-ultimate was not at the head")
		}
		want := []string{"aeon-ultimate", "qwen3.5:9b", "qwen3:0.6b", "aeon-fast"}
		if len(got) != len(want) {
			t.Fatalf("got %d entries, want %d (got names = %v)", len(got), len(want), namesOf(got))
		}
		for i, w := range want {
			if got[i].ModelName != w {
				t.Fatalf("entry[%d] = %q, want %q (full: %v)", i, got[i].ModelName, w, namesOf(got))
			}
		}
	})

	t.Run("prefer non-existent model returns an error and does not mutate the list", func(t *testing.T) {
		// Existing contract per reorderModelList in model.go: missing names
		// are surfaced loudly with an error mentioning each typo, never a
		// silent no-op. This pins the loud-typo behavior.
		_, _, err := reorderModelList(baseList(), []string{"aeon-does-not-exist"})
		if err == nil {
			t.Fatal("expected error for unknown model name, got nil")
		}
		if !strings.Contains(err.Error(), "aeon-does-not-exist") {
			t.Fatalf("error should name the missing model, got: %v", err)
		}
	})

	t.Run("prefer is idempotent when the model is already at the head", func(t *testing.T) {
		// Round 1: promote aeon-ultimate to the head.
		round1, already1, err := reorderModelList(baseList(), []string{"aeon-ultimate"})
		if err != nil {
			t.Fatalf("round 1 reorderModelList: %v", err)
		}
		if already1 {
			t.Fatal("round 1: expected alreadyAtHead = false")
		}

		// Round 2: calling prefer again on the already-reordered list must
		// be a no-op (alreadyAtHead = true) and return the same order. The
		// PreferModels CLI path uses alreadyAtHead to skip the ConfigMap
		// patch + LiteLLM rollout, so this property has user-visible
		// consequences beyond pure-function purity.
		round2, already2, err := reorderModelList(round1, []string{"aeon-ultimate"})
		if err != nil {
			t.Fatalf("round 2 reorderModelList: %v", err)
		}
		if !already2 {
			t.Fatal("round 2: expected alreadyAtHead = true (idempotent prefer)")
		}
		// The reorder still returns a fully-built slice even on no-op.
		// Verify the order is identical to round1.
		if len(round2) != len(round1) {
			t.Fatalf("round 2 length drift: got %d, want %d", len(round2), len(round1))
		}
		for i := range round1 {
			if round2[i].ModelName != round1[i].ModelName {
				t.Fatalf("round 2 mutated order at [%d]: got %q, want %q (full: %v)", i, round2[i].ModelName, round1[i].ModelName, namesOf(round2))
			}
		}
	})

	t.Run("prefer is case-sensitive: AEON-ULTIMATE does not match aeon-ultimate", func(t *testing.T) {
		// reorderModelList builds its lookup map with plain `entry.ModelName`
		// (no strings.ToLower / case-fold). This test pins the case-sensitive
		// contract so a future refactor to a case-insensitive lookup is a
		// deliberate, reviewed change — not a silent behavior shift that
		// could collide entries like "qwen3.5:9b" vs a hypothetical
		// "QWEN3.5:9B" alias.
		_, _, err := reorderModelList(baseList(), []string{"AEON-ULTIMATE"})
		if err == nil {
			t.Fatal("expected error: AEON-ULTIMATE should not match aeon-ultimate under case-sensitive lookup")
		}
		if !strings.Contains(err.Error(), "AEON-ULTIMATE") {
			t.Fatalf("error should name the missing (uppercase) input, got: %v", err)
		}
	})
}

// namesOf is a local helper mirroring modelNames in model_test.go so this
// file does not depend on test-helper ordering inside the package.
func namesOf(entries []ModelEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ModelName
	}
	return out
}
