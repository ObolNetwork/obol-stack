package kubectl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// stubProbe returns a probeAPIServerFn that returns the given (stderr, err)
// and counts how many times it was called.
func stubProbe(stderr string, err error, calls *int) func(string, string, time.Duration) (string, error) {
	return func(string, string, time.Duration) (string, error) {
		if calls != nil {
			*calls++
		}
		return stderr, err
	}
}

// withProbe swaps probeAPIServerFn for the duration of the test.
func withProbe(t *testing.T, fn func(string, string, time.Duration) (string, error)) {
	t.Helper()
	orig := probeAPIServerFn
	probeAPIServerFn = fn
	t.Cleanup(func() { probeAPIServerFn = orig })
}

// withRefresh swaps refreshKubeconfigFn for the duration of the test.
func withRefresh(t *testing.T, fn func(*config.Config) bool) {
	t.Helper()
	orig := refreshKubeconfigFn
	refreshKubeconfigFn = fn
	t.Cleanup(func() { refreshKubeconfigFn = orig })
}

func writeKubeconfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig.yaml"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCluster_Missing(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	err := EnsureCluster(cfg)
	if err == nil {
		t.Fatal("expected error when kubeconfig missing")
	}
}

func TestEnsureCluster_ProbeSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, dir)
	cfg := &config.Config{ConfigDir: dir}

	var probeCalls int
	withProbe(t, stubProbe("", nil, &probeCalls))
	withRefresh(t, func(*config.Config) bool {
		t.Fatal("refresh should not run when initial probe succeeds")
		return false
	})

	if err := EnsureCluster(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probeCalls != 1 {
		t.Errorf("expected 1 probe call, got %d", probeCalls)
	}
}

// Authoritative-probe regression: kubeconfig is stale (e.g. after `k3d cluster
// stop && start` and a port change), so `kubectl version` reports connection
// refused, AND k3d refresh recovers it. EnsureCluster must report success
// instead of telling the user the cluster is stopped.
func TestEnsureCluster_RefreshRecoversFromPortDrift(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, dir)
	cfg := &config.Config{ConfigDir: dir}

	var probeCalls, refreshCalls int
	withProbe(t, func(string, string, time.Duration) (string, error) {
		probeCalls++
		if probeCalls == 1 {
			return "dial tcp 127.0.0.1:50839: connect: connection refused",
				errors.New("exit status 1")
		}
		// Post-refresh probe succeeds.
		return "", nil
	})
	withRefresh(t, func(*config.Config) bool {
		refreshCalls++
		return true
	})

	if err := EnsureCluster(cfg); err != nil {
		t.Fatalf("expected nil after kubeconfig refresh, got: %v", err)
	}
	if probeCalls != 2 {
		t.Errorf("expected 2 probe calls (initial + post-refresh), got %d", probeCalls)
	}
	if refreshCalls != 1 {
		t.Errorf("expected exactly 1 refresh call, got %d", refreshCalls)
	}
}

// When the refresh helper cannot run (no k3d binary / no stack id),
// EnsureCluster must still surface ErrClusterDown for a connection-refused
// stderr — i.e. fall back to current behavior, not crash.
func TestEnsureCluster_RefreshSkippedReturnsClusterDown(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, dir)
	cfg := &config.Config{ConfigDir: dir}

	withProbe(t, stubProbe(
		"Unable to connect to the server: dial tcp 127.0.0.1:6443: connection refused",
		errors.New("exit status 1"),
		nil,
	))
	withRefresh(t, func(*config.Config) bool { return false })

	err := EnsureCluster(cfg)
	if !errors.Is(err, ErrClusterDown) {
		t.Fatalf("expected ErrClusterDown, got: %v", err)
	}
}

// When the refresh runs but the post-refresh probe still cannot reach the
// API server, EnsureCluster must report ErrClusterDown (do not loop forever).
func TestEnsureCluster_RefreshRanButProbeStillFails(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, dir)
	cfg := &config.Config{ConfigDir: dir}

	var probeCalls int
	withProbe(t, func(string, string, time.Duration) (string, error) {
		probeCalls++
		return "Unable to connect to the server: dial tcp 127.0.0.1:6443: connection refused",
			errors.New("exit status 1")
	})
	withRefresh(t, func(*config.Config) bool { return true })

	err := EnsureCluster(cfg)
	if !errors.Is(err, ErrClusterDown) {
		t.Fatalf("expected ErrClusterDown after refresh+retry, got: %v", err)
	}
	if probeCalls != 2 {
		t.Errorf("expected exactly 2 probe calls, got %d", probeCalls)
	}
}

// Non-cluster-down probe failures (e.g. kubectl binary missing) must NOT be
// reported as "cluster appears to be stopped". That message has misled
// debugging in the past — the original error should pass through verbatim.
func TestEnsureCluster_NonClusterDownErrorPassesThrough(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, dir)
	cfg := &config.Config{ConfigDir: dir}

	want := errors.New("fork/exec /bin/kubectl: no such file or directory")
	withProbe(t, stubProbe("", want, nil))
	withRefresh(t, func(*config.Config) bool {
		t.Fatal("refresh should not run for non-cluster-down errors")
		return false
	})

	err := EnsureCluster(cfg)
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("expected original error to pass through, got: %v", err)
	}
	if errors.Is(err, ErrClusterDown) {
		t.Fatalf("non-cluster-down error must not be wrapped as ErrClusterDown")
	}
}

func TestRefreshK3dKubeconfig_MissingPrerequisites(t *testing.T) {
	// No k3d binary, no stack id → refresh must decline (return false) rather
	// than panic or shell out to a binary that does not exist.
	dir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: dir,
		BinDir:    filepath.Join(dir, "bin"),
	}
	if refreshK3dKubeconfig(cfg) {
		t.Fatal("expected false when prerequisites are missing")
	}

	// k3d binary present but no stack id file → still declines.
	if err := os.MkdirAll(cfg.BinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BinDir, "k3d"), []byte("#!/bin/sh\nexit 0"), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if refreshK3dKubeconfig(cfg) {
		t.Fatal("expected false when .stack-id is missing")
	}
}

func TestRefreshK3dKubeconfig_NonK3dBackendDeclines(t *testing.T) {
	// When the persisted backend is something other than k3d (e.g. k3s),
	// the refresh must not run — the k3d CLI does not own this cluster.
	dir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: dir,
		BinDir:    filepath.Join(dir, "bin"),
	}
	if err := os.MkdirAll(cfg.BinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BinDir, "k3d"), []byte("#!/bin/sh\nexit 0"), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".stack-id"), []byte("fancy-yak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".stack-backend"), []byte("k3s"), 0o600); err != nil {
		t.Fatal(err)
	}

	if refreshK3dKubeconfig(cfg) {
		t.Fatal("expected false when backend is not k3d")
	}
}

func TestPaths(t *testing.T) {
	cfg := &config.Config{
		BinDir:    "/usr/local/bin",
		ConfigDir: "/home/user/.config/obol",
	}

	bin, kc := Paths(cfg)
	if bin != "/usr/local/bin/kubectl" {
		t.Errorf("binary = %q, want /usr/local/bin/kubectl", bin)
	}

	if kc != "/home/user/.config/obol/kubeconfig.yaml" {
		t.Errorf("kubeconfig = %q, want /home/user/.config/obol/kubeconfig.yaml", kc)
	}
}

func TestOutput_BinaryNotFound(t *testing.T) {
	_, err := Output("/nonexistent/kubectl", "/tmp/kc.yaml", "version")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRunSilent_BinaryNotFound(t *testing.T) {
	err := RunSilent("/nonexistent/kubectl", "/tmp/kc.yaml", "version")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestWrapClusterDown(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		stderr  string
		wrapped bool
	}{
		{
			name:    "connection refused",
			err:     errors.New("exit status 1"),
			stderr:  `dial tcp 127.0.0.1:50839: connect: connection refused`,
			wrapped: true,
		},
		{
			name:    "unable to connect",
			err:     errors.New("exit status 1"),
			stderr:  `Unable to connect to the server: dial tcp 127.0.0.1:6443`,
			wrapped: true,
		},
		{
			name:    "normal error",
			err:     errors.New("resource not found"),
			stderr:  "Error from server (NotFound)",
			wrapped: false,
		},
		{
			name:    "nil error",
			err:     nil,
			stderr:  "",
			wrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapClusterDown(tt.err, tt.stderr)
			if tt.wrapped {
				if got == nil || !strings.Contains(got.Error(), "cluster appears to be stopped") {
					t.Errorf("expected cluster-down wrapper, got: %v", got)
				}
			} else if tt.err == nil {
				if got != nil {
					t.Errorf("expected nil, got: %v", got)
				}
			} else {
				if got == nil || strings.Contains(got.Error(), "cluster appears to be stopped") {
					t.Errorf("should not wrap normal errors, got: %v", got)
				}
			}
		})
	}
}

func TestFormatClusterDownError(t *testing.T) {
	// Contextual message for a known command.
	msg := FormatClusterDownError(ErrClusterDown, []string{"obol", "sell", "list"})
	if !strings.Contains(msg, "before listing services for sale") {
		t.Errorf("expected contextual hint, got: %s", msg)
	}

	// Fallback for an unknown subcommand.
	msg = FormatClusterDownError(ErrClusterDown, []string{"obol", "frobnicate"})
	if msg != ErrClusterDown.Error() {
		t.Errorf("expected fallback message, got: %s", msg)
	}

	// Non-cluster-down error returns empty.
	msg = FormatClusterDownError(errors.New("something else"), []string{"obol", "sell", "list"})
	if msg != "" {
		t.Errorf("expected empty for non-cluster-down error, got: %s", msg)
	}
}
