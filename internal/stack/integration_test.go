//go:build integration

package stack_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Integration tests for the k3s backend user flows.
// Requires: sudo access, k3s binary, OBOL_DEVELOPMENT=true.
//
// Run with:
//   go test -tags integration -timeout 15m -v ./internal/stack/

func TestK3sUserFlows(t *testing.T) {
	if os.Getenv("OBOL_DEVELOPMENT") != "true" {
		t.Skip("OBOL_DEVELOPMENT not set, skipping integration test")
	}
	if runtime.GOOS != "linux" {
		t.Skip("k3s backend integration test only runs on Linux")
	}

	projectRoot := findProjectRoot(t)
	obol := filepath.Join(projectRoot, ".workspace", "bin", "obol")
	if _, err := os.Stat(obol); os.IsNotExist(err) {
		t.Fatalf("obol binary not found at %s — build it first", obol)
	}

	configDir := filepath.Join(projectRoot, ".workspace", "config")
	binDir := filepath.Join(projectRoot, ".workspace", "bin")

	// Helper to run obol commands
	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(obol, args...)
		cmd.Env = append(os.Environ(),
			"OBOL_DEVELOPMENT=true",
			"PATH="+binDir+":"+os.Getenv("PATH"),
		)
		cmd.Dir = projectRoot
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Cleanup before tests
	run(t, "stack", "purge", "--force")

	// Cleanup after all tests
	t.Cleanup(func() {
		run(t, "stack", "purge", "--force")
	})

	t.Run("init", func(t *testing.T) {
		out, err := run(t, "stack", "init", "--backend", "k3s")
		if err != nil {
			t.Fatalf("stack init failed: %v\n%s", err, out)
		}

		// Verify config files created
		for _, f := range []string{"k3s-config.yaml", ".stack-id", ".stack-backend"} {
			if _, err := os.Stat(filepath.Join(configDir, f)); os.IsNotExist(err) {
				t.Errorf("expected %s to exist after init", f)
			}
		}

		// Verify defaults directory
		if _, err := os.Stat(filepath.Join(configDir, "defaults")); os.IsNotExist(err) {
			t.Error("expected defaults/ directory after init")
		}

		// Verify backend is k3s
		data, _ := os.ReadFile(filepath.Join(configDir, ".stack-backend"))
		if got := strings.TrimSpace(string(data)); got != "k3s" {
			t.Errorf("backend = %q, want k3s", got)
		}
	})

	t.Run("init_rejects_without_force", func(t *testing.T) {
		_, err := run(t, "stack", "init", "--backend", "k3s")
		if err == nil {
			t.Error("init without --force should fail when config exists")
		}
	})

	t.Run("init_force_preserves_stack_id", func(t *testing.T) {
		idBefore, _ := os.ReadFile(filepath.Join(configDir, ".stack-id"))
		out, err := run(t, "stack", "init", "--backend", "k3s", "--force")
		if err != nil {
			t.Fatalf("stack init --force failed: %v\n%s", err, out)
		}
		idAfter, _ := os.ReadFile(filepath.Join(configDir, ".stack-id"))
		if string(idBefore) != string(idAfter) {
			t.Errorf("stack ID changed: %q → %q", string(idBefore), string(idAfter))
		}
	})

	t.Run("up", func(t *testing.T) {
		out, err := run(t, "stack", "up")
		if err != nil {
			t.Fatalf("stack up failed: %v\n%s", err, out)
		}

		// Verify PID file and kubeconfig exist
		if _, err := os.Stat(filepath.Join(configDir, ".k3s.pid")); os.IsNotExist(err) {
			t.Error("PID file not found after stack up")
		}
		if _, err := os.Stat(filepath.Join(configDir, "kubeconfig.yaml")); os.IsNotExist(err) {
			t.Error("kubeconfig not found after stack up")
		}
	})

	t.Run("kubectl_passthrough", func(t *testing.T) {
		out, err := run(t, "kubectl", "get", "nodes", "--no-headers")
		if err != nil {
			t.Fatalf("kubectl passthrough failed: %v\n%s", err, out)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 1 {
			t.Error("kubectl get nodes returned no nodes")
		}

		out, err = run(t, "kubectl", "get", "namespaces", "--no-headers")
		if err != nil {
			t.Fatalf("kubectl get namespaces failed: %v\n%s", err, out)
		}
		lines = strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 1 {
			t.Error("kubectl get namespaces returned no namespaces")
		}
	})

	t.Run("up_idempotent", func(t *testing.T) {
		pidBefore, _ := os.ReadFile(filepath.Join(configDir, ".k3s.pid"))

		out, err := run(t, "stack", "up")
		if err != nil {
			t.Fatalf("stack up (idempotent) failed: %v\n%s", err, out)
		}

		pidAfter, _ := os.ReadFile(filepath.Join(configDir, ".k3s.pid"))
		if string(pidBefore) != string(pidAfter) {
			t.Errorf("PID changed on idempotent up: %q → %q", string(pidBefore), string(pidAfter))
		}
	})

	t.Run("down", func(t *testing.T) {
		out, err := run(t, "stack", "down")
		if err != nil {
			t.Fatalf("stack down failed: %v\n%s", err, out)
		}

		// PID file should be cleaned up
		if _, err := os.Stat(filepath.Join(configDir, ".k3s.pid")); !os.IsNotExist(err) {
			t.Error("PID file should be removed after down")
		}

		// Config should be preserved
		if _, err := os.Stat(filepath.Join(configDir, ".stack-id")); os.IsNotExist(err) {
			t.Error("stack ID should be preserved after down")
		}
	})

	t.Run("down_already_stopped", func(t *testing.T) {
		out, err := run(t, "stack", "down")
		if err != nil {
			t.Fatalf("stack down (already stopped) failed: %v\n%s", err, out)
		}
	})

	t.Run("up_restart_after_down", func(t *testing.T) {
		out, err := run(t, "stack", "up")
		if err != nil {
			t.Fatalf("stack up (restart) failed: %v\n%s", err, out)
		}

		// Verify PID file exists
		if _, err := os.Stat(filepath.Join(configDir, ".k3s.pid")); os.IsNotExist(err) {
			t.Error("PID file not found after restart")
		}

		// Wait for node to be ready
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			out, err := run(t, "kubectl", "get", "nodes", "--no-headers")
			if err == nil && strings.Contains(out, "Ready") {
				break
			}
			time.Sleep(3 * time.Second)
		}

		out, _ = run(t, "kubectl", "get", "nodes", "--no-headers")
		if !strings.Contains(out, "Ready") {
			t.Error("node not ready after restart")
		}
	})

	t.Run("purge", func(t *testing.T) {
		out, err := run(t, "stack", "purge")
		if err != nil {
			t.Fatalf("stack purge failed: %v\n%s", err, out)
		}

		time.Sleep(2 * time.Second)

		if _, err := os.Stat(filepath.Join(configDir, ".stack-id")); !os.IsNotExist(err) {
			t.Error("stack ID should be removed after purge")
		}
		if _, err := os.Stat(filepath.Join(configDir, ".k3s.pid")); !os.IsNotExist(err) {
			t.Error("PID file should be removed after purge")
		}
	})

	t.Run("full_cycle_purge_force", func(t *testing.T) {
		out, err := run(t, "stack", "init", "--backend", "k3s")
		if err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}

		out, err = run(t, "stack", "up")
		if err != nil {
			t.Fatalf("up: %v\n%s", err, out)
		}

		out, err = run(t, "stack", "purge", "--force")
		if err != nil {
			t.Fatalf("purge --force: %v\n%s", err, out)
		}

		time.Sleep(2 * time.Second)

		if _, err := os.Stat(filepath.Join(configDir, ".stack-id")); !os.IsNotExist(err) {
			t.Error("config should be removed after purge --force")
		}
	})
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (no go.mod)")
		}
		dir = parent
	}
}
