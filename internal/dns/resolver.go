// Package dns manages a local DNS resolver for wildcard *.obol.stack resolution.
//
// It runs a dnsmasq Docker container that answers DNS queries for the obol.stack
// domain with 127.0.0.1, and configures the host OS to use it. This enables
// per-instance hostname routing (e.g., openclaw-myid.obol.stack) without manual
// /etc/hosts entries.
package dns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	containerName = "obol-dns"
	hostPort      = "5553"
	dnsImage      = "alpine:3.21"
	resolverDir   = "/etc/resolver"
	resolverFile  = "obol.stack"
	domain        = "obol.stack"
)

// EnsureRunning starts the DNS resolver container if not already running.
// Idempotent: no-ops if the container is already healthy.
func EnsureRunning() error {
	// Check if container exists and is running
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil // Already running
	}

	// Remove stale container if exists (ignore errors)
	exec.Command("docker", "rm", "-f", containerName).Run() //nolint:errcheck

	fmt.Println("Starting DNS resolver for *.obol.stack...")

	cmd := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"-p", hostPort+":53/udp",
		"-p", hostPort+":53/tcp",
		"--restart", "unless-stopped",
		dnsImage,
		"sh", "-c",
		"apk add --no-cache dnsmasq >/dev/null 2>&1 && "+
			"exec dnsmasq --no-daemon "+
			"--conf-file=/dev/null "+
			"--address=/"+domain+"/127.0.0.1 "+
			"--log-facility=-",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start DNS container: %w\n%s", err, output)
	}

	fmt.Printf("DNS resolver running (*.obol.stack → 127.0.0.1, port %s)\n", hostPort)
	return nil
}

// Stop removes the DNS resolver container.
func Stop() {
	if out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return // Not running
	}
	exec.Command("docker", "rm", "-f", containerName).Run() //nolint:errcheck
	fmt.Println("DNS resolver stopped")
}

// ConfigureSystemResolver sets up the host OS to route *.obol.stack queries
// to our local DNS container. Requires sudo on first run.
//
// macOS: creates /etc/resolver/obol.stack
// Linux: prints manual instructions (TODO: systemd-resolved integration)
func ConfigureSystemResolver() error {
	switch runtime.GOOS {
	case "darwin":
		return configureMacOSResolver()
	case "linux":
		fmt.Println("Note: automatic DNS resolver setup not yet supported on Linux.")
		fmt.Printf("To resolve *.obol.stack, add to your DNS config:\n")
		fmt.Printf("  server=/%s/127.0.0.1#%s\n", domain, hostPort)
		return nil
	default:
		return fmt.Errorf("unsupported OS for DNS resolver: %s", runtime.GOOS)
	}
}

// RemoveSystemResolver removes the host OS DNS configuration for *.obol.stack.
func RemoveSystemResolver() {
	switch runtime.GOOS {
	case "darwin":
		removeMacOSResolver()
	}
}

// IsResolverConfigured checks whether the system resolver is already set up.
func IsResolverConfigured() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	path := filepath.Join(resolverDir, resolverFile)
	_, err := os.Stat(path)
	return err == nil
}

// configureMacOSResolver creates /etc/resolver/obol.stack pointing to our DNS.
func configureMacOSResolver() error {
	path := filepath.Join(resolverDir, resolverFile)

	// Check if already configured correctly
	if data, err := os.ReadFile(path); err == nil {
		content := string(data)
		if strings.Contains(content, "port "+hostPort) {
			return nil // Already configured
		}
	}

	content := fmt.Sprintf("# Managed by obol-stack — resolves *.obol.stack to localhost\nnameserver 127.0.0.1\nport %s\n", hostPort)

	// /etc/resolver/ needs root — try sudo
	fmt.Println("Configuring macOS DNS resolver for *.obol.stack (requires sudo)...")

	mkdirCmd := exec.Command("sudo", "mkdir", "-p", resolverDir)
	mkdirCmd.Stdout = os.Stdout
	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		return fmt.Errorf("failed to create %s (sudo required): %w", resolverDir, err)
	}

	writeCmd := exec.Command("sudo", "tee", path)
	writeCmd.Stdin = strings.NewReader(content)
	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	fmt.Printf("Resolver configured: %s → 127.0.0.1:%s\n", path, hostPort)
	return nil
}

// removeMacOSResolver removes /etc/resolver/obol.stack.
func removeMacOSResolver() {
	path := filepath.Join(resolverDir, resolverFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if err := exec.Command("sudo", "rm", path).Run(); err != nil {
		fmt.Printf("Warning: failed to remove %s: %v\n", path, err)
		fmt.Printf("  Remove manually: sudo rm %s\n", path)
		return
	}
	fmt.Printf("Removed DNS resolver config: %s\n", path)
}
