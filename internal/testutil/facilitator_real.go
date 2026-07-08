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
	"runtime"
	"strconv"
	"testing"
	"time"
)

const defaultX402FacilitatorImage = "ghcr.io/obolnetwork/x402-facilitator-prometheus-overlay:1.4.9"

// x402FacilitatorImage resolves the facilitator image, honoring the same
// X402_FACILITATOR_IMAGE override the shell flows use (flows/lib.sh) so a
// locally-built facilitator (e.g. an upstream-sync candidate) can be smoked
// through the Go integration path without editing the pin.
func x402FacilitatorImage() string {
	if img := os.Getenv("X402_FACILITATOR_IMAGE"); img != "" {
		return img
	}
	return defaultX402FacilitatorImage
}

// RealFacilitator wraps a running x402-rs facilitator process.
// Unlike MockFacilitator, this validates real EIP-712 signatures against
// an Anvil fork of Base Sepolia.
type RealFacilitator struct {
	Port       int
	ClusterURL string // e.g. "http://host.docker.internal:4040"

	cmd           *exec.Cmd
	cancel        context.CancelFunc
	containerName string
}

type RealFacilitatorOptions struct {
	EnableEIP2612GasSponsoring bool
}

// StartRealFacilitator runs the pinned x402-rs facilitator image,
// generates a config pointing at the given Anvil fork, starts the facilitator
// on a free port, and waits for it to become ready.
// Registers t.Cleanup to kill the process and remove temp config.
func StartRealFacilitator(t *testing.T, anvil *AnvilFork) *RealFacilitator {
	return StartRealFacilitatorWithOptions(t, anvil, RealFacilitatorOptions{})
}

func StartRealFacilitatorWithOptions(t *testing.T, anvil *AnvilFork, opts RealFacilitatorOptions) *RealFacilitator {
	t.Helper()

	requireFacilitatorImage(t)

	// Find a free port.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("find free port for facilitator: %v", err)
	}

	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// The facilitator runs in a Docker container. On Linux it gets
	// `--network host`, so the host loopback works for the Anvil URL. On
	// macOS, Docker Desktop's host networking does not share the Mac
	// loopback — mirror flows/lib.sh::start_x402_facilitator_container:
	// publish the port with -p and reach Anvil via host.docker.internal.
	anvilHost := "127.0.0.1"
	if runtime.GOOS == "darwin" {
		anvilHost = "host.docker.internal"
	}
	anvilLocalURL := fmt.Sprintf("http://%s:%d", anvilHost, anvil.Port)

	// Generate config file.
	configPath := writeRealFacilitatorConfig(t, port, anvilLocalURL, anvil.Accounts[0].PrivateKey, opts)

	ctx, cancel := context.WithCancel(context.Background())
	containerName := fmt.Sprintf("obol-test-x402-facilitator-%d", time.Now().UnixNano())

	// Linux: host networking, the facilitator binds the host port directly.
	// macOS: publish the port instead — Docker Desktop's host networking
	// does not share the Mac loopback (same split as flows/lib.sh).
	netArgs := []string{"--network", "host"}
	if runtime.GOOS == "darwin" {
		netArgs = []string{"-p", fmt.Sprintf("%d:%d", port, port)}
	}
	args := append([]string{
		"run", "--rm",
		"--name", containerName,
	}, netArgs...)
	args = append(args,
		"-v", configPath+":/config.json:ro",
		x402FacilitatorImage(),
		"--config", "/config.json",
	)
	cmd := exec.CommandContext(ctx, "docker", args...)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start x402-rs facilitator: %v", err)
	}

	rf := &RealFacilitator{
		Port:          port,
		ClusterURL:    "http://" + net.JoinHostPort(clusterHostURL(), strconv.Itoa(port)),
		cmd:           cmd,
		cancel:        cancel,
		containerName: containerName,
	}

	t.Cleanup(func() {
		cancel()
		_ = exec.Command("docker", "rm", "-f", containerName).Run()

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

// requireFacilitatorImage verifies the facilitator image is available.
// Local facilitator experiments should be packaged as a Docker image instead of
// depending on host checkout paths: build + tag the image, then point
// X402_FACILITATOR_IMAGE at it. An image already present locally is used as-is
// (never pulled), mirroring flows/lib.sh::docker_pull_public_image.
func requireFacilitatorImage(t *testing.T) {
	t.Helper()

	image := x402FacilitatorImage()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker not installed; cannot run %s", image)
	}

	if err := exec.Command("docker", "image", "inspect", image).Run(); err == nil {
		t.Logf("using local x402 facilitator image %s", image)
		return
	}

	pull := exec.Command("docker", "pull", image)
	if out, err := pull.CombinedOutput(); err != nil {
		t.Fatalf("pull %s: %v\n%s", image, err, out)
	}
	t.Logf("using x402 facilitator image %s", image)
}

// writeRealFacilitatorConfig writes a temporary config-test.json for the facilitator.
func writeRealFacilitatorConfig(t *testing.T, port int, anvilRPCURL, signerKey string, opts RealFacilitatorOptions) string {
	t.Helper()

	// Strip 0x prefix from signer key if present.
	if len(signerKey) > 2 && signerKey[:2] == "0x" {
		signerKey = signerKey[2:]
	}

	config := map[string]any{
		"port": port,
		"host": "0.0.0.0",
		"chains": map[string]any{
			"eip155:84532": map[string]any{
				"eip1559":     true,
				"flashblocks": false,
				"signers":     []string{signerKey},
				"rpc": []map[string]any{
					{
						"http":       anvilRPCURL,
						"rate_limit": 50,
					},
				},
			},
		},
		"schemes": []map[string]any{
			{
				"id":     "v1-eip155-exact",
				"chains": "eip155:*",
			},
			{
				"id":     "v2-eip155-exact",
				"chains": "eip155:*",
				"config": map[string]any{
					"eip2612_gas_sponsoring": opts.EnableEIP2612GasSponsoring,
				},
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal facilitator config: %v", err)
	}

	f, err := os.CreateTemp(t.TempDir(), "x402-facilitator-*.json")
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
	url := fmt.Sprintf("http://127.0.0.1:%d/supported", rf.Port)

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
