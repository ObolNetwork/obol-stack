package agentcrd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stackbackup"
	"github.com/ObolNetwork/obol-stack/internal/ui"
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

// TestStripServerManagedMetadataOnAgentManifest pins the ResumeAll strip path:
// persisted Agent YAML may carry server-managed metadata; stripping must drop
// those fields while keeping name/namespace and spec intact.
func TestStripServerManagedMetadataOnAgentManifest(t *testing.T) {
	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "Agent",
		"metadata": map[string]any{
			"name":              "quant",
			"namespace":         "agent-quant",
			"resourceVersion":   "12345",
			"uid":               "abc-123",
			"creationTimestamp": "2024-01-01T00:00:00Z",
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
		},
		"spec": map[string]any{
			"model":  "qwen3.5:9b",
			"skills": []any{"gas"},
			"wallet": map[string]any{"create": true},
		},
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	stackbackup.StripServerManagedMetadata(doc)

	meta, ok := doc["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing or wrong type: %T", doc["metadata"])
	}
	for _, k := range []string{"resourceVersion", "uid", "creationTimestamp", "managedFields"} {
		if _, present := meta[k]; present {
			t.Errorf("server-managed field %q still present after strip", k)
		}
	}
	if meta["name"] != "quant" || meta["namespace"] != "agent-quant" {
		t.Fatalf("identity fields altered: %v", meta)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		t.Fatalf("spec missing or wrong type: %T", doc["spec"])
	}
	if spec["model"] != "qwen3.5:9b" {
		t.Fatalf("spec.model altered: %v", spec["model"])
	}
}

// A recorded agent must be replayed with server-side apply: client-side apply
// fails ("resourceVersion: Invalid value: 0") on objects whose
// last-applied-configuration carries server-managed metadata.
func TestResumeAllUsesServerSideApply(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "kubectl.log")
	stub := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBOL_KUBECTL", stub)

	cfg := &config.Config{ConfigDir: filepath.Join(dir, "cfg"), BinDir: filepath.Join(dir, "bin")}
	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "rt", "namespace": "agent-rt", "resourceVersion": "7936", "uid": "x"},
		"spec":       map[string]any{"objective": "o"},
	}
	if err := PersistManifest(cfg, "rt", manifest); err != nil {
		t.Fatal(err)
	}

	ResumeAll(cfg, ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{}))

	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "apply --server-side --force-conflicts --field-manager=obol -f -") {
		t.Fatalf("agent not replayed with server-side apply; kubectl calls:\n%s", calls)
	}
}
