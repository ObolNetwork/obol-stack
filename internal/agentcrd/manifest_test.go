package agentcrd

import (
	"os"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

func TestManifestStoreRoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	if got := ListPersistedManifests(cfg); got != nil {
		t.Fatalf("empty store should list nothing, got %v", got)
	}

	manifest := BuildAgent("quant", AgentOptions{
		Model:        "qwen3.5:9b",
		Skills:       []string{"gas", "addresses"},
		Objective:    "test agent",
		CreateWallet: true,
	})
	if err := PersistManifest(cfg, "quant", manifest); err != nil {
		t.Fatal(err)
	}
	if err := PersistManifest(cfg, "scout", BuildAgent("scout", AgentOptions{})); err != nil {
		t.Fatal(err)
	}

	names := ListPersistedManifests(cfg)
	if len(names) != 2 || names[0] != "quant" || names[1] != "scout" {
		t.Fatalf("ListPersistedManifests = %v", names)
	}

	// The persisted file must round-trip to an applyable Agent manifest.
	data, err := os.ReadFile(ManifestPath(cfg, "quant"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["kind"] != "Agent" {
		t.Fatalf("persisted kind = %v", doc["kind"])
	}
	meta, _ := doc["metadata"].(map[string]any)
	if meta["name"] != "quant" || meta["namespace"] != Namespace("quant") {
		t.Fatalf("persisted metadata = %v", meta)
	}
	spec, _ := doc["spec"].(map[string]any)
	if spec["model"] != "qwen3.5:9b" {
		t.Fatalf("persisted spec = %v", spec)
	}

	if err := RemoveManifest(cfg, "quant"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveManifest(cfg, "quant"); err != nil {
		t.Fatal("double remove must be a no-op, got", err)
	}
	if names := ListPersistedManifests(cfg); len(names) != 1 || names[0] != "scout" {
		t.Fatalf("after remove: %v", names)
	}
}
