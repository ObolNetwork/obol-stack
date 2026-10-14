package stack

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

// stubDiscovery replaces live-service discovery for the duration of a test.
func stubDiscovery(t *testing.T, s LiveServices, err error) {
	t.Helper()
	orig := discoverLiveServicesFn
	discoverLiveServicesFn = func(*config.Config) (LiveServices, error) { return s, err }
	t.Cleanup(func() { discoverLiveServicesFn = orig })
}

// stubDestroy records backend destroys instead of running k3d/k3s.
func stubDestroy(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := destroyBackendFn
	destroyBackendFn = func(Backend, *config.Config, *ui.UI, string) error { calls++; return nil }
	t.Cleanup(func() { destroyBackendFn = orig })
	return &calls
}

func TestConfirmClusterDestroy_AlwaysRequiresConfirmationWhenIdle(t *testing.T) {
	cfg := newTestCfg(t)
	stubDiscovery(t, LiveServices{}, nil)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmClusterDestroy(cfg, u, "stack init --force (backend switch)", false, true)
	if err == nil || proceed {
		t.Fatalf("idle cluster must still need confirmation non-interactively: proceed=%v err=%v", proceed, err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error must name --yes: %v", err)
	}
}

func TestConfirmClusterDestroy_IdleWithoutAlwaysPassesThrough(t *testing.T) {
	cfg := newTestCfg(t)
	stubDiscovery(t, LiveServices{}, nil)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmClusterDestroy(cfg, u, "obol stack purge", false, false)
	if err != nil || !proceed {
		t.Fatalf("checked idle cluster should pass for purge: proceed=%v err=%v", proceed, err)
	}
}

func TestConfirmClusterDestroy_DiscoveryErrorFailsClosed(t *testing.T) {
	cfg := newTestCfg(t)
	stubDiscovery(t, LiveServices{}, errors.New("cluster not running"))
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmClusterDestroy(cfg, u, "obol stack purge", false, false)
	if err == nil || proceed {
		t.Fatalf("discovery error must not be treated as nothing live: proceed=%v err=%v", proceed, err)
	}
	if !strings.Contains(err.Error(), "could not check what is running") || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should explain the unchecked cluster and name --yes: %v", err)
	}
}

func TestConfirmClusterDestroy_YesProceedsWithWarning(t *testing.T) {
	cfg := newTestCfg(t)
	stubDiscovery(t, LiveServices{}, errors.New("cluster not running"))
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmClusterDestroy(cfg, u, "obol stack purge", true, true)
	if err != nil || !proceed {
		t.Fatalf("--yes must proceed: proceed=%v err=%v", proceed, err)
	}
	if !strings.Contains(buf.String(), "could not check") {
		t.Errorf("expected an unchecked-cluster warning, got %q", buf.String())
	}
}

func TestConfirmRunningServicesLoss_DiscoveryErrorDoesNotBlockStop(t *testing.T) {
	cfg := newTestCfg(t)
	stubDiscovery(t, LiveServices{}, errors.New("cluster not running"))
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmRunningServicesLoss(cfg, u, "obol stack down", false)
	if err != nil || !proceed {
		t.Fatalf("stack down (stop, not destroy) should not be blocked by an unreachable cluster: proceed=%v err=%v", proceed, err)
	}
}

func switchTestCfg(t *testing.T) (*config.Config, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
		StateDir:  filepath.Join(tmpDir, "state"),
	}
	if err := SaveBackend(cfg, BackendK3d); err != nil {
		t.Fatal(err)
	}
	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	if err := os.WriteFile(k3dPath, []byte("k3d config"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, k3dPath
}

func TestDestroyOldBackendIfSwitching_IdleClusterStillNeedsYes(t *testing.T) {
	cfg, k3dPath := switchTestCfg(t)
	stubDiscovery(t, LiveServices{}, nil)
	calls := stubDestroy(t)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	err := destroyOldBackendIfSwitching(cfg, u, BackendK3s, "test-id", false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("backend switch must require --yes non-interactively even with nothing live: %v", err)
	}
	if *calls != 0 {
		t.Error("old cluster destroyed without confirmation")
	}
	if _, statErr := os.Stat(k3dPath); statErr != nil {
		t.Errorf("old backend config must survive a refused switch: %v", statErr)
	}
}

func TestDestroyOldBackendIfSwitching_UnreachableClusterStillNeedsYes(t *testing.T) {
	cfg, _ := switchTestCfg(t)
	stubDiscovery(t, LiveServices{}, errors.New("cluster not running"))
	calls := stubDestroy(t)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	err := destroyOldBackendIfSwitching(cfg, u, BackendK3s, "test-id", false)
	if err == nil || !strings.Contains(err.Error(), "could not check") {
		t.Fatalf("unreachable cluster must be reported and refused: %v", err)
	}
	if *calls != 0 {
		t.Error("old cluster destroyed without confirmation")
	}
}

func TestDestroyOldBackendIfSwitching_YesDestroys(t *testing.T) {
	cfg, k3dPath := switchTestCfg(t)
	stubDiscovery(t, LiveServices{}, nil)
	calls := stubDestroy(t)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	if err := destroyOldBackendIfSwitching(cfg, u, BackendK3s, "test-id", true); err != nil {
		t.Fatalf("--yes: %v", err)
	}
	if *calls != 1 {
		t.Errorf("expected one destroy, got %d", *calls)
	}
	if _, statErr := os.Stat(k3dPath); !os.IsNotExist(statErr) {
		t.Error("old backend config should be cleaned up after a confirmed switch")
	}
}

func TestDestroyOldBackendIfSwitching_SameBackendNoPrompt(t *testing.T) {
	cfg, _ := switchTestCfg(t)
	stubDiscovery(t, LiveServices{}, errors.New("must not be called"))
	calls := stubDestroy(t)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	if err := destroyOldBackendIfSwitching(cfg, u, BackendK3d, "test-id", false); err != nil {
		t.Fatalf("same-backend --force must not prompt: %v", err)
	}
	if *calls != 0 || buf.Len() != 0 {
		t.Errorf("same-backend --force must be silent and destroy nothing (calls=%d, out=%q)", *calls, buf.String())
	}
}
