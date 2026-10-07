package stackbackup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// importHarness stubs every cluster touchpoint of ensureImportCluster.
type importHarness struct {
	reachable  []bool // successive ensureClusterFn results (last value repeats)
	nodes      string
	populated  bool
	deleted    []string
	stackUps   int
	stackUpErr error
}

func (h *importHarness) install(t *testing.T) {
	t.Helper()
	origEnsure, origNodes, origHolds, origDelete := ensureClusterFn, listNodeNamesFn, clusterHoldsWorkloads, deleteK3dClusterFn
	t.Cleanup(func() {
		ensureClusterFn, listNodeNamesFn, clusterHoldsWorkloads, deleteK3dClusterFn = origEnsure, origNodes, origHolds, origDelete
	})
	calls := 0
	ensureClusterFn = func(*config.Config) error {
		r := h.reachable[min(calls, len(h.reachable)-1)]
		calls++
		if r {
			return nil
		}
		return errors.New("unreachable")
	}
	listNodeNamesFn = func(*config.Config) (string, error) { return h.nodes, nil }
	clusterHoldsWorkloads = func(*config.Config) (bool, string) {
		if h.populated {
			return true, "agents.obol.org"
		}
		return false, ""
	}
	deleteK3dClusterFn = func(_ *config.Config, name string) error {
		h.deleted = append(h.deleted, name)
		// After deletion the stale kubeconfig reaches nothing until stack up.
		h.nodes = ""
		return nil
	}
}

func (h *importHarness) stackUp() error {
	h.stackUps++
	h.nodes = "k3d-obol-stack-restored-id-server-0" // stack up creates OUR cluster
	return h.stackUpErr
}

func importCfg(t *testing.T) *config.Config {
	t.Helper()
	return identityTestConfig(t, "restored-id", "k3d") // .stack-id now holds the archive's ID
}

func testUI() *ui.UI { return ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{}) }

func TestEnsureImportCluster(t *testing.T) {
	t.Run("own cluster already running: nothing to do", func(t *testing.T) {
		h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-restored-id-server-0"}
		h.install(t)
		if err := ensureImportCluster(importCfg(t), ImportOptions{StackUp: h.stackUp}, "restored-id", testUI()); err != nil {
			t.Fatal(err)
		}
		if h.stackUps != 0 || len(h.deleted) != 0 {
			t.Errorf("stackUps=%d deleted=%v, want none", h.stackUps, h.deleted)
		}
	})

	t.Run("clean host: brings the stack up", func(t *testing.T) {
		h := &importHarness{reachable: []bool{false, true}}
		h.install(t)
		if err := ensureImportCluster(importCfg(t), ImportOptions{StackUp: h.stackUp}, "", testUI()); err != nil {
			t.Fatal(err)
		}
		if h.stackUps != 1 {
			t.Errorf("stackUps=%d, want 1", h.stackUps)
		}
	})

	t.Run("no StackUp hook: falls back to printed steps", func(t *testing.T) {
		h := &importHarness{reachable: []bool{false}}
		h.install(t)
		err := ensureImportCluster(importCfg(t), ImportOptions{}, "", testUI())
		if !errors.Is(err, errNoStackUp) {
			t.Fatalf("err=%v, want errNoStackUp", err)
		}
	})

	// init + up + import --force: the empty pre-import cluster is removed
	// so the restored stack can claim the ingress ports.
	t.Run("empty pre-import cluster of the replaced stack is removed", func(t *testing.T) {
		h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-fresh-id-server-0"}
		h.install(t)
		if err := ensureImportCluster(importCfg(t), ImportOptions{Force: true, StackUp: h.stackUp}, "fresh-id", testUI()); err != nil {
			t.Fatal(err)
		}
		if len(h.deleted) != 1 || h.deleted[0] != "obol-stack-fresh-id" {
			t.Errorf("deleted=%v, want [obol-stack-fresh-id]", h.deleted)
		}
		if h.stackUps != 1 {
			t.Errorf("stackUps=%d, want 1", h.stackUps)
		}
	})

	t.Run("populated pre-import cluster is never deleted", func(t *testing.T) {
		h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-fresh-id-server-0", populated: true}
		h.install(t)
		err := ensureImportCluster(importCfg(t), ImportOptions{Force: true, StackUp: h.stackUp}, "fresh-id", testUI())
		if err == nil || !strings.Contains(err.Error(), "k3d cluster delete obol-stack-fresh-id") {
			t.Fatalf("err=%v, want guidance naming the cluster", err)
		}
		if len(h.deleted) != 0 || h.stackUps != 0 {
			t.Errorf("deleted=%v stackUps=%d, want none", h.deleted, h.stackUps)
		}
	})

	// The original incident shape: a stale kubeconfig reaching an unrelated
	// stack. Leave it alone and bring up our own.
	t.Run("unrelated stack's cluster is left untouched", func(t *testing.T) {
		h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-someone-else-server-0"}
		h.install(t)
		if err := ensureImportCluster(importCfg(t), ImportOptions{Force: true, StackUp: h.stackUp}, "fresh-id", testUI()); err != nil {
			t.Fatal(err)
		}
		if len(h.deleted) != 0 {
			t.Errorf("deleted=%v, must not touch another stack's cluster", h.deleted)
		}
		if h.stackUps != 1 {
			t.Errorf("stackUps=%d, want 1", h.stackUps)
		}
	})

	t.Run("cluster-only never deletes", func(t *testing.T) {
		h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-fresh-id-server-0"}
		h.install(t)
		_ = ensureImportCluster(importCfg(t), ImportOptions{ClusterOnly: true, StackUp: h.stackUp}, "fresh-id", testUI())
		if len(h.deleted) != 0 {
			t.Errorf("deleted=%v, --cluster-only must not delete clusters", h.deleted)
		}
	})

	t.Run("still the wrong cluster after stack up: refuse", func(t *testing.T) {
		h := &importHarness{reachable: []bool{false, true}}
		h.install(t)
		up := func() error { h.stackUps++; h.nodes = "k3d-obol-stack-someone-else-server-0"; return nil }
		err := ensureImportCluster(importCfg(t), ImportOptions{StackUp: up}, "", testUI())
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Fatalf("err=%v, want refusal", err)
		}
	})
}

func TestRewriteRestoredPaths(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{ConfigDir: filepath.Join(root, "new", "config"), DataDir: filepath.Join(root, "new", "data")}
	m := &Manifest{ConfigDir: "/Users/old/proj/.workspace/config", DataDir: "/Users/old/proj/.workspace/data"}

	write := func(rel, content string) string {
		p := filepath.Join(cfg.ConfigDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	k3d := write("k3d.yaml", "volumes:\n  - volume: /Users/old/proj/.workspace/data:/data\n  - volume: /Users/old/proj/.workspace/config/x:/x\n")
	untouched := write("llm/recorded-models.yaml", "models: [a]\n")
	bin := write("bin.dat", "\x00/Users/old/proj/.workspace/data")

	n, err := rewriteRestoredPaths(cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed=%d, want 1", n)
	}
	got, _ := os.ReadFile(k3d)
	want := "volumes:\n  - volume: " + cfg.DataDir + ":/data\n  - volume: " + cfg.ConfigDir + "/x:/x\n"
	if string(got) != want {
		t.Errorf("k3d.yaml =\n%s\nwant\n%s", got, want)
	}
	if info, _ := os.Stat(k3d); info.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(untouched); string(b) != "models: [a]\n" {
		t.Error("unrelated file modified")
	}
	if b, _ := os.ReadFile(bin); !strings.Contains(string(b), "/Users/old") {
		t.Error("binary file must be skipped")
	}

	// Same paths: no-op.
	if n, _ := rewriteRestoredPaths(cfg, &Manifest{ConfigDir: cfg.ConfigDir, DataDir: cfg.DataDir}); n != 0 {
		t.Errorf("same-path import rewrote %d files", n)
	}
}
