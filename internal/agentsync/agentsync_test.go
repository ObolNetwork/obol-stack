package agentsync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestListInstances(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	// Missing applications/<runtime> dir lists nothing (not an error).
	if got := ListInstances(cfg, agentruntime.Hermes); got != nil {
		t.Fatalf("absent runtime dir = %v, want nil", got)
	}

	base := filepath.Join(cfg.ConfigDir, "applications", "hermes")
	for _, id := range []string{"quant", "obol-agent"} {
		if err := os.MkdirAll(filepath.Join(base, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file (not a dir) must be ignored — only instance dirs count.
	if err := os.WriteFile(filepath.Join(base, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := ListInstances(cfg, agentruntime.Hermes), []string{"obol-agent", "quant"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListInstances = %v, want %v", got, want)
	}
}

func TestSyncInstances_OnlyMissingSkipsDeployed(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	for _, p := range []string{"hermes/obol-agent", "hermes/quant", "openclaw/legacy"} {
		if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "applications", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	origSync, origHealth := syncFns, releaseHealthFn
	t.Cleanup(func() { syncFns, releaseHealthFn = origSync, origHealth })

	var synced []string
	rec := func(rt agentruntime.Runtime) func(*config.Config, string, *ui.UI) error {
		return func(_ *config.Config, id string, _ *ui.UI) error {
			synced = append(synced, string(rt)+"/"+id)
			if id == "legacy" {
				return errors.New("boom")
			}
			return nil
		}
	}
	syncFns = map[agentruntime.Runtime]func(*config.Config, string, *ui.UI) error{
		agentruntime.Hermes:   rec(agentruntime.Hermes),
		agentruntime.OpenClaw: rec(agentruntime.OpenClaw),
	}
	releaseHealthFn = func(_ *config.Config, ns string) (helmcmd.ReleaseHealth, error) {
		if ns == "hermes-obol-agent" {
			return helmcmd.ReleasesDeployed, nil
		}
		return helmcmd.ReleasesMissing, nil
	}

	err := SyncInstances(cfg, ui.New(false), Options{OnlyMissing: true})
	if err == nil {
		t.Fatal("expected joined error for the failing openclaw instance")
	}
	if want := []string{"hermes/quant", "openclaw/legacy"}; !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced = %v, want %v (deployed default instance must be skipped)", synced, want)
	}

	synced = nil
	_ = SyncInstances(cfg, ui.New(false), Options{})
	if len(synced) != 3 {
		t.Fatalf("unconditional sync should hit every instance, got %v", synced)
	}
}
