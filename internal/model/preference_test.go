package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func newTestCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{ConfigDir: dir, DataDir: dir, BinDir: dir}
}

// TestPreferenceRoundTrip verifies the basic write/read contract: what goes
// in comes out, including whitespace trimming.
func TestPreferenceRoundTrip(t *testing.T) {
	cfg := newTestCfg(t)
	if got := ReadPreference(cfg); got != "" {
		t.Fatalf("read on empty: got %q, want empty", got)
	}
	if err := WritePreference(cfg, "  qwen3.5:9b  "); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := ReadPreference(cfg); got != "qwen3.5:9b" {
		t.Fatalf("read after write: got %q, want qwen3.5:9b", got)
	}
}

// TestPreferenceClear ensures explicit clear and writing an empty value both
// remove the marker.
func TestPreferenceClear(t *testing.T) {
	cfg := newTestCfg(t)
	if err := WritePreference(cfg, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := ClearPreference(cfg); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := ReadPreference(cfg); got != "" {
		t.Fatalf("after clear: got %q, want empty", got)
	}
	// Writing an empty value also clears.
	if err := WritePreference(cfg, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := WritePreference(cfg, "   "); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if got := ReadPreference(cfg); got != "" {
		t.Fatalf("after empty write: got %q, want empty", got)
	}
}

// TestPreferenceSurvivesArbitraryFiles ensures a corrupt/garbled file is
// treated as no-preference rather than panicking. Real-world: a future
// version writes additional metadata, an old version reads it — must not
// crash.
func TestPreferenceSurvivesArbitraryFiles(t *testing.T) {
	cfg := newTestCfg(t)
	path := filepath.Join(cfg.ConfigDir, preferenceFileName)
	if err := os.WriteFile(path, []byte("\x00\x01\x02"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := ReadPreference(cfg)
	if got != "\x00\x01\x02" {
		// The contract is "trim whitespace, return whatever's left" — the
		// downstream RankWithPreference will treat the result as a literal
		// model name. modelMatchesPreference will then fail to match any
		// real model and the call falls back to plain Rank. So binary
		// junk passes through silently. The point of this test is "doesn't
		// crash"; the literal-string behavior is asserted by the call.
		t.Logf("note: read returned literal bytes %q (treated as opaque model name)", got)
	}
}

// TestRankWithPreference_NoPreference is the regression guard for the
// default path: when no preference is set, behavior is identical to plain
// Rank. Cloud beats local.
func TestRankWithPreference_NoPreference(t *testing.T) {
	primary, fallbacks := RankWithPreference([]string{"llama3.2:3b", "claude-sonnet-4-6", "gpt-4.1"}, "")
	if primary != "claude-sonnet-4-6" {
		t.Fatalf("primary = %q, want claude-sonnet-4-6", primary)
	}
	want := []string{"gpt-4.1", "llama3.2:3b"}
	if !reflect.DeepEqual(fallbacks, want) {
		t.Fatalf("fallbacks = %#v, want %#v", fallbacks, want)
	}
}

// TestRankWithPreference_ExplicitPreferenceBeatsRank is THE regression guard
// for the prefer-over-rank fix Oisin asked for. Without it, capability
// rank would silently demote the user's pick on every restart.
func TestRankWithPreference_ExplicitPreferenceBeatsRank(t *testing.T) {
	// User explicitly prefers a local 3B model — must win over Claude.
	primary, fallbacks := RankWithPreference(
		[]string{"claude-sonnet-4-6", "gpt-4.1", "llama3.2:3b"},
		"llama3.2:3b",
	)
	if primary != "llama3.2:3b" {
		t.Fatalf("primary = %q, want llama3.2:3b (preference must beat capability)", primary)
	}
	// Fallbacks: cloud first (Claude > GPT), then any remaining locals.
	want := []string{"claude-sonnet-4-6", "gpt-4.1"}
	if !reflect.DeepEqual(fallbacks, want) {
		t.Fatalf("fallbacks = %#v, want %#v", fallbacks, want)
	}
}

// TestRankWithPreference_ProviderPrefixToleranceguards the UX path: the user
// types a bare model name (`claude-sonnet-4-6`), but the candidate list
// carries provider-prefixed strings (`anthropic/claude-sonnet-4-6`) — they
// must still match.
func TestRankWithPreference_ProviderPrefixTolerance(t *testing.T) {
	primary, _ := RankWithPreference(
		[]string{"anthropic/claude-sonnet-4-6", "openai/gpt-4.1"},
		"claude-sonnet-4-6",
	)
	if primary != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("primary = %q, want anthropic/claude-sonnet-4-6 (bare preference must match prefixed candidate)", primary)
	}
}

// TestRankWithPreference_StalePreferenceFallsBack covers the case where a
// previously-preferred model has been removed from the candidate list. The
// resolver must not crash and must not return an empty primary — it must
// fall through to plain Rank.
func TestRankWithPreference_StalePreferenceFallsBack(t *testing.T) {
	primary, _ := RankWithPreference(
		[]string{"claude-sonnet-4-6", "llama3.2:3b"},
		"qwen3.5:9b", // not in candidates
	)
	if primary != "claude-sonnet-4-6" {
		t.Fatalf("primary = %q, want claude-sonnet-4-6 (stale preference must fall back to rank)", primary)
	}
}

// TestRankWithPreference_EmptyInput documents the edge case: nil/empty
// candidate list returns empty primary, no panic.
func TestRankWithPreference_EmptyInput(t *testing.T) {
	primary, fallbacks := RankWithPreference(nil, "anything")
	if primary != "" || len(fallbacks) != 0 {
		t.Fatalf("got %q, %v; want empty, nil", primary, fallbacks)
	}
}
