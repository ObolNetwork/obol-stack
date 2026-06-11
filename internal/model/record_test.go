package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func entry(name string) ModelEntry {
	return ModelEntry{ModelName: name, LiteLLMParams: LiteLLMParams{Model: name}}
}

func names(entries []ModelEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ModelName
	}
	return out
}

func TestFilterRecordableEntries(t *testing.T) {
	in := []ModelEntry{
		entry("qwen3.5:9b"),
		entry("paid/*"),
		entry("paid/qwen36-deep"),
		entry("claude-opus-4-6"),
	}
	got := names(filterRecordableEntries(in))
	want := []string{"qwen3.5:9b", "claude-opus-4-6"}
	if len(got) != len(want) {
		t.Fatalf("filterRecordableEntries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filterRecordableEntries = %v, want %v", got, want)
		}
	}
}

func TestMergeRecordedModelList(t *testing.T) {
	// Recorded order is operator intent (custom model preferred to head);
	// current has auto-detect's order plus chart catch-all and a model
	// detected after the record was taken.
	recorded := []ModelEntry{entry("qwen36-deep"), entry("qwen3.5:9b")}
	current := []ModelEntry{
		entry("qwen3.5:9b"),
		entry("qwen3.5:4b"), // auto-detected after record — must survive
		{ModelName: "qwen36-deep", LiteLLMParams: LiteLLMParams{Model: "stale"}},
		entry("paid/*"), // chart catch-all — must survive
	}

	got := MergeRecordedModelList(recorded, current)
	wantOrder := []string{"qwen36-deep", "qwen3.5:9b", "qwen3.5:4b", "paid/*"}
	gotNames := names(got)
	if len(gotNames) != len(wantOrder) {
		t.Fatalf("merge = %v, want %v", gotNames, wantOrder)
	}
	for i := range wantOrder {
		if gotNames[i] != wantOrder[i] {
			t.Fatalf("merge order = %v, want %v", gotNames, wantOrder)
		}
	}
	// Recorded params replace current ones for the same name.
	if got[0].LiteLLMParams.Model != "qwen36-deep" {
		t.Errorf("recorded entry params lost: %+v", got[0])
	}
}

func TestSecretEnvVarsFromEntries(t *testing.T) {
	in := []ModelEntry{
		{ModelName: "a", LiteLLMParams: LiteLLMParams{APIKey: "os.environ/ANTHROPIC_API_KEY"}},
		{ModelName: "b", LiteLLMParams: LiteLLMParams{APIKey: "os.environ/ANTHROPIC_API_KEY"}},
		{ModelName: "c", LiteLLMParams: LiteLLMParams{APIKey: "none"}},
		{ModelName: "d", LiteLLMParams: LiteLLMParams{APIKey: "os.environ/OPENAI_API_KEY"}},
		{ModelName: "e"},
	}
	got := secretEnvVarsFromEntries(in)
	if len(got) != 2 || got[0] != "ANTHROPIC_API_KEY" || got[1] != "OPENAI_API_KEY" {
		t.Fatalf("secretEnvVarsFromEntries = %v", got)
	}
}

func TestRecordedModelStateRoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	// Absent record reads as nil, nil.
	state, err := readRecordedModelState(cfg)
	if err != nil || state != nil {
		t.Fatalf("absent record: %v, %v", state, err)
	}

	in := &RecordedModelState{
		Version:   recordVersion,
		ModelList: []ModelEntry{entry("qwen36-deep")},
		Secrets:   map[string]string{"OPENAI_API_KEY": "sk-test"},
	}
	if err := writeRecordedModelState(cfg, in); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(recordedModelPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("record holds API keys but has mode %v, want 0600", info.Mode().Perm())
	}

	out, err := readRecordedModelState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ModelList) != 1 || out.ModelList[0].ModelName != "qwen36-deep" || out.Secrets["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("round trip mismatch: %+v", out)
	}

	// Future versions must be refused, not misread.
	bad := filepath.Join(cfg.ConfigDir, "llm", "recorded-models.yaml")
	if err := os.WriteFile(bad, []byte("version: 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecordedModelState(cfg); err == nil {
		t.Fatal("future record version accepted")
	}
}
