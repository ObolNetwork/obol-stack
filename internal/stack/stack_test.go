package stack

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestCheckPortsAvailable_FreePorts(t *testing.T) {
	// Use high ephemeral ports that are almost certainly free
	ports := []int{19876, 19877}
	if err := checkPortsAvailable(ports); err != nil {
		t.Fatalf("expected no error for free ports, got: %v", err)
	}
}

func TestCheckPortsAvailable_BlockedPort(t *testing.T) {
	// Bind a port to simulate a conflict
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	// Extract the port number from the listener address
	addr := ln.Addr().(*net.TCPAddr)
	blockedPort := addr.Port

	err = checkPortsAvailable([]int{blockedPort})
	if err == nil {
		t.Fatal("expected error for blocked port, got nil")
	}

	portStr := fmt.Sprintf("%d", blockedPort)
	if !strings.Contains(err.Error(), portStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should mention 'already in use', got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo lsof") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestCheckPortsAvailable_MixedPorts(t *testing.T) {
	// Bind one port, leave another free
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	blockedPort := ln.Addr().(*net.TCPAddr).Port

	// Pick a free port by briefly binding and releasing
	ln2, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind second ephemeral port: %v", err)
	}
	freePort := ln2.Addr().(*net.TCPAddr).Port
	ln2.Close()

	err = checkPortsAvailable([]int{freePort, blockedPort})
	if err == nil {
		t.Fatal("expected error when one port is blocked, got nil")
	}

	// Should mention only the blocked port
	blockedStr := fmt.Sprintf("%d", blockedPort)
	if !strings.Contains(err.Error(), blockedStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}
}

func TestFormatPorts(t *testing.T) {
	tests := []struct {
		ports    []int
		expected string
	}{
		{[]int{443}, "443"},
		{[]int{80, 443}, "80, 443"},
		{[]int{80, 8080, 443, 8443}, "80, 8080, 443, 8443"},
	}
	for _, tt := range tests {
		got := formatPorts(tt.ports)
		if got != tt.expected {
			t.Errorf("formatPorts(%v) = %q, want %q", tt.ports, got, tt.expected)
		}
	}
}

func TestDestroyOldBackendIfSwitching_CleansStaleConfigs(t *testing.T) {
	// Simulate a k3d → k3s switch: k3d.yaml should be cleaned up
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	// Write k3d as the current backend + its config file
	SaveBackend(cfg, BackendK3d)
	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0644)

	// Switch to k3s — k3d config should be cleaned up
	// (Destroy will fail because no real cluster, but cleanup should still work)
	destroyOldBackendIfSwitching(cfg, BackendK3s, "test-id")

	if _, err := os.Stat(k3dPath); !os.IsNotExist(err) {
		t.Error("k3d.yaml should be removed when switching to k3s")
	}
}

func TestDestroyOldBackendIfSwitching_NoopSameBackend(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	SaveBackend(cfg, BackendK3d)
	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0644)

	// Same backend — nothing should be cleaned up
	destroyOldBackendIfSwitching(cfg, BackendK3d, "test-id")

	if _, err := os.Stat(k3dPath); os.IsNotExist(err) {
		t.Error("k3d.yaml should NOT be removed when re-initing same backend")
	}
}

func TestDestroyOldBackendIfSwitching_K3sToK3d(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	SaveBackend(cfg, BackendK3s)
	// Create k3s state files
	for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
		os.WriteFile(filepath.Join(tmpDir, f), []byte("data"), 0644)
	}

	// Switch to k3d — k3s files should be cleaned up
	destroyOldBackendIfSwitching(cfg, BackendK3d, "test-id")

	for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
		if _, err := os.Stat(filepath.Join(tmpDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed when switching from k3s to k3d", f)
		}
	}
}

func TestDestroyOldBackendIfSwitching_NoBackendFile(t *testing.T) {
	// No .stack-backend file — LoadBackend defaults to k3d.
	// Switching to k3d should be a no-op (same backend).
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	// Should not panic or error
	destroyOldBackendIfSwitching(cfg, BackendK3d, "test-id")
}

func TestOllamaHostIPForBackend_K3s(t *testing.T) {
	// k3s backend should return 127.0.0.1 (already an IP, no DNS resolution needed)
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error for k3s backend: %v", err)
	}
	if ip != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1 for k3s backend, got %s", ip)
	}
}

func TestOllamaHostIPForBackend_K3d(t *testing.T) {
	// k3d backend should return a valid, non-empty IP (or an error if
	// host.docker.internal / host.k3d.internal cannot be resolved, e.g.
	// in CI without Docker Desktop).
	ip, err := ollamaHostIPForBackend(BackendK3d)
	if err != nil {
		t.Skipf("skipping: DNS resolution failed (expected in CI): %v", err)
	}
	if ip == "" {
		t.Fatal("expected non-empty IP for k3d backend")
	}
	// The result must be a parseable IP address (not a hostname)
	if net.ParseIP(ip) == nil {
		t.Errorf("expected a valid IP address for k3d backend, got %q", ip)
	}
}

func TestOllamaHostIPForBackend_AlreadyIP(t *testing.T) {
	// Verify the function passes through an already-numeric IP unchanged.
	// k3s returns "127.0.0.1" from ollamaHostForBackend, so it should
	// short-circuit on net.ParseIP without attempting DNS.
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "127.0.0.1" {
		t.Errorf("expected pass-through of 127.0.0.1, got %s", ip)
	}
}
