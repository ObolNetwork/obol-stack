package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// RealFacilitator wraps a running x402-rs facilitator process.
// Unlike MockFacilitator, this validates real EIP-712 signatures against
// an Anvil fork of Base Sepolia.
type RealFacilitator struct {
	Port       int
	ClusterURL string // e.g. "http://host.docker.internal:4040"

	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// StartRealFacilitator discovers/builds the x402-rs facilitator binary,
// generates a config pointing at the given Anvil fork, starts the facilitator
// on a free port, and waits for it to become ready.
//
// Binary discovery order:
//  1. X402_FACILITATOR_BIN env var (explicit path to binary)
//  2. Pre-built binary at $X402_RS_DIR/target/release/x402-facilitator
//     (or the legacy $X402_RS_DIR/target/release/facilitator)
//  3. cargo build --release in $X402_RS_DIR (if Cargo.toml exists)
//  4. Skip test
//
// Registers t.Cleanup to kill the process and remove temp config.
func StartRealFacilitator(t *testing.T, anvil *AnvilFork) *RealFacilitator {
	t.Helper()

	bin := discoverFacilitatorBinary(t)

	// Find a free port.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("find free port for facilitator: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// The facilitator runs on the host, so it needs the localhost Anvil URL
	// (not host.docker.internal which only resolves inside Docker/k3d).
	anvilLocalURL := fmt.Sprintf("http://127.0.0.1:%d", anvil.Port)

	// Generate config file.
	configPath := writeRealFacilitatorConfig(t, port, anvilLocalURL, anvil.Accounts[0].PrivateKey)

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, bin, "--config", configPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start x402-rs facilitator: %v", err)
	}

	rf := &RealFacilitator{
		Port:       port,
		ClusterURL: fmt.Sprintf("http://%s:%d", clusterHostURL(), port),
		cmd:        cmd,
		cancel:     cancel,
	}

	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		os.Remove(configPath)
	})

	// Wait for /supported to return 200.
	if err := rf.waitReady(30 * time.Second); err != nil {
		t.Fatalf("x402-rs facilitator failed to become ready: %v\nstderr: %s", err, stderr.String())
	}

	t.Logf("x402-rs facilitator running on port %d (cluster URL: %s)", port, rf.ClusterURL)
	return rf
}

// discoverFacilitatorBinary finds or builds the x402-rs facilitator binary.
func discoverFacilitatorBinary(t *testing.T) string {
	t.Helper()

	// 1. Explicit binary path.
	if bin := os.Getenv("X402_FACILITATOR_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			t.Logf("using X402_FACILITATOR_BIN=%s", bin)
			return bin
		}
		t.Fatalf("X402_FACILITATOR_BIN=%s does not exist", bin)
	}

	// Resolve x402-rs directory.
	rsDir := os.Getenv("X402_RS_DIR")
	if rsDir == "" {
		// Default local checkout path.
		home, _ := os.UserHomeDir()
		rsDir = filepath.Join(home, "Development", "R&D", "x402-rs")
	}

	// 2. Pre-built binary.
	prebuiltCandidates := []string{
		filepath.Join(rsDir, "target", "release", "x402-facilitator"),
		filepath.Join(rsDir, "target", "release", "facilitator"),
	}
	for _, prebuilt := range prebuiltCandidates {
		if _, err := os.Stat(prebuilt); err == nil {
			t.Logf("using pre-built facilitator at %s", prebuilt)
			return prebuilt
		}
	}

	// 3. Build from source.
	cargoToml := filepath.Join(rsDir, "Cargo.toml")
	if _, err := os.Stat(cargoToml); err == nil {
		if _, err := exec.LookPath("cargo"); err != nil {
			t.Skip("x402-rs source found but cargo not installed")
		}
		t.Logf("building x402-rs facilitator from %s (this may take a while)...", rsDir)
		buildCommands := [][]string{
			{"build", "--release", "-p", "x402-facilitator"},
			{"build", "--release", "-p", "facilitator"},
		}
		var buildErr error
		for _, args := range buildCommands {
			build := exec.Command("cargo", args...)
			build.Dir = rsDir
			build.Stdout = os.Stderr
			build.Stderr = os.Stderr
			if err := build.Run(); err == nil {
				buildErr = nil
				break
			} else {
				buildErr = err
			}
		}
		if buildErr != nil {
			t.Fatalf("cargo build --release failed: %v", buildErr)
		}
		for _, prebuilt := range prebuiltCandidates {
			if _, err := os.Stat(prebuilt); err == nil {
				return prebuilt
			}
		}
		t.Fatalf("cargo build succeeded but binary not found at any expected path: %v", prebuiltCandidates)
	}

	t.Skip("x402-rs facilitator not available — set X402_FACILITATOR_BIN or X402_RS_DIR, " +
		"or clone https://github.com/x402-rs/x402-rs to ~/Development/R&D/x402-rs")
	return ""
}

// writeRealFacilitatorConfig writes a temporary config-test.json for the facilitator.
func writeRealFacilitatorConfig(t *testing.T, port int, anvilRPCURL, signerKey string) string {
	t.Helper()

	// Strip 0x prefix from signer key if present.
	if len(signerKey) > 2 && signerKey[:2] == "0x" {
		signerKey = signerKey[2:]
	}

	config := map[string]interface{}{
		"port": port,
		"host": "0.0.0.0",
		"chains": map[string]interface{}{
			"eip155:84532": map[string]interface{}{
				"eip1559":     true,
				"flashblocks": false,
				"signers":     []string{signerKey},
				"rpc": []map[string]interface{}{
					{
						"http":       anvilRPCURL,
						"rate_limit": 50,
					},
				},
			},
		},
		"schemes": []map[string]interface{}{
			{
				"id":     "v1-eip155-exact",
				"chains": "eip155:*",
			},
			{
				"id":     "v2-eip155-exact",
				"chains": "eip155:*",
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal facilitator config: %v", err)
	}

	f, err := os.CreateTemp("", "x402-facilitator-*.json")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		t.Fatalf("write facilitator config: %v", err)
	}
	f.Close()

	t.Logf("wrote facilitator config to %s", f.Name())
	return f.Name()
}

// waitReady polls the facilitator's /supported endpoint until it returns 200.
func (rf *RealFacilitator) waitReady(timeout time.Duration) error {
	// Use localhost URL for readiness check (not cluster URL).
	var url string
	if runtime.GOOS == "darwin" {
		url = fmt.Sprintf("http://127.0.0.1:%d/supported", rf.Port)
	} else {
		url = fmt.Sprintf("http://127.0.0.1:%d/supported", rf.Port)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("facilitator not ready after %v on port %d", timeout, rf.Port)
}
