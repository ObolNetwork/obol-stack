package inference

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultContainerImage    = "ollama/ollama:latest"
	defaultContainerCPUs     = 4
	defaultContainerMemoryMB = 8192
	defaultContainerHostPort = 11435 // avoid colliding with host Ollama on 11434

	containerReadyTimeout = 120 * time.Second
	containerReadyPoll    = 2 * time.Second
)

// ContainerManager manages an Ollama Linux container using the apple/container
// CLI (github.com/apple/container v0.9.0+).
//
// The container runs Ollama on its internal port 11434, mapped to a
// host-local port (default 11435) so only the gateway process can reach it.
// No bridge to the external network is provided — the container can receive
// inference requests from the gateway but cannot initiate outbound connections.
//
// Install the CLI before use:
//
//	curl -L -o /tmp/container-installer-signed.pkg \
//	  https://github.com/apple/container/releases/download/0.9.0/container-installer-signed.pkg
//	sudo installer -pkg /tmp/container-installer-signed.pkg -target /
type ContainerManager struct {
	binary string // path to `container` CLI, default "container" (PATH lookup)
	name   string // container instance name, e.g. "obol-inference-my-node"
	port   int    // host-local port mapped to container's 11434
}

// sanitizeContainerName strips unsafe characters from a deployment name and
// returns a valid container name. Only lowercase alphanumeric and hyphens are
// kept; the result is truncated to 63 chars.
func sanitizeContainerName(deploymentName string) string {
	name := strings.ToLower(deploymentName)
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	s := b.String()
	// Trim leading hyphens.
	s = strings.TrimLeft(s, "-")
	if s == "" {
		s = "default"
	}
	full := "obol-inference-" + s
	if len(full) > 63 {
		full = full[:63]
	}
	// Trim trailing hyphens after truncation.
	full = strings.TrimRight(full, "-")
	return full
}

// newContainerManager creates a ContainerManager for the named deployment.
// binary may be empty to use "container" from PATH.
func newContainerManager(binary, deploymentName string, hostPort int) *ContainerManager {
	if binary == "" {
		binary = "container"
	}
	if hostPort == 0 {
		hostPort = defaultContainerHostPort
	}
	return &ContainerManager{
		binary: binary,
		name:   sanitizeContainerName(deploymentName),
		port:   hostPort,
	}
}

// UpstreamURL returns the URL where the running Ollama can be reached from the host.
func (m *ContainerManager) UpstreamURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.port)
}

// EnsureSystemRunning starts the container system daemon if it is not already active.
// Safe to call when the daemon is already running.
func (m *ContainerManager) EnsureSystemRunning(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, m.binary, "system", "start", "--enable-kernel-install")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := strings.ToLower(string(out))
		if strings.Contains(s, "already running") || strings.Contains(s, "already started") {
			return nil
		}
		return fmt.Errorf("container system start: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Start pulls the OCI image (if not cached) and runs the Ollama container,
// then blocks until the Ollama API responds or ctx is cancelled.
//
// Any stale container with the same name is removed before starting.
func (m *ContainerManager) Start(ctx context.Context, image string, cpus, memoryMB int) error {
	if image == "" {
		image = defaultContainerImage
	}
	if cpus == 0 {
		cpus = defaultContainerCPUs
	}
	if memoryMB == 0 {
		memoryMB = defaultContainerMemoryMB
	}

	// Ensure the container system daemon is running.
	if err := m.EnsureSystemRunning(ctx); err != nil {
		return fmt.Errorf("container system: %w", err)
	}

	// Remove any stale container with this name (ignore errors — may not exist).
	_ = m.Stop(ctx)

	// Pull the image first so the user sees download progress.
	// On cache hit the pull completes in milliseconds; on a cold pull (first
	// run) it can take several minutes for large images like ollama/ollama.
	log.Printf("container: pulling image %s (first run may take several minutes)...", image)
	pullCmd := exec.CommandContext(ctx, m.binary, "pull", image)
	pullCmd.Stdout = os.Stdout
	pullCmd.Stderr = os.Stderr
	if err := pullCmd.Run(); err != nil {
		return fmt.Errorf("container pull %s: %w", image, err)
	}

	log.Printf("container: starting %q (%d CPUs, %d MiB RAM)...", m.name, cpus, memoryMB)

	args := []string{
		"run",
		"--name", m.name,
		"--detach",
		"--publish", fmt.Sprintf("127.0.0.1:%d:11434", m.port),
		"--cpus", fmt.Sprintf("%d", cpus),
		"--memory", fmt.Sprintf("%dM", memoryMB),
		image,
	}

	cmd := exec.CommandContext(ctx, m.binary, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("container run: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if err := m.waitReady(ctx); err != nil {
		return err
	}

	log.Printf("container: %q ready at %s", m.name, m.UpstreamURL())
	return nil
}

// Stop gracefully stops and removes the named container.
// Returns nil if the container does not exist.
func (m *ContainerManager) Stop(ctx context.Context) error {
	stopCmd := exec.CommandContext(ctx, m.binary, "stop", m.name)
	out, err := stopCmd.CombinedOutput()
	if err != nil {
		s := strings.ToLower(string(out))
		if strings.Contains(s, "not found") || strings.Contains(s, "does not exist") || strings.Contains(s, "no such container") {
			return nil
		}
		// Log but don't fail — we always try to rm next.
		log.Printf("container stop %s: %v: %s", m.name, err, strings.TrimSpace(string(out)))
	}

	rmCmd := exec.CommandContext(ctx, m.binary, "rm", "--force", m.name)
	if out, err := rmCmd.CombinedOutput(); err != nil {
		s := strings.ToLower(string(out))
		if strings.Contains(s, "not found") || strings.Contains(s, "does not exist") {
			return nil
		}
		return fmt.Errorf("container rm %s: %w: %s", m.name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitReady polls the Ollama health endpoint until it returns HTTP 200 or
// ctx / the ready timeout expires.
func (m *ContainerManager) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(containerReadyTimeout)
	client := &http.Client{Timeout: 5 * time.Second}
	url := m.UpstreamURL() + "/"

	log.Printf("container: waiting for Ollama to be ready (up to %s)...", containerReadyTimeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("ollama in container %q not ready after %s", m.name, containerReadyTimeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(containerReadyPoll):
		}
	}
}
