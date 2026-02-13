// Package dns manages a local DNS resolver for wildcard *.obol.stack resolution.
//
// It runs a dnsmasq Docker container that answers DNS queries for the obol.stack
// domain with 127.0.0.1, and configures the host OS to use it. This enables
// per-instance hostname routing (e.g., openclaw-myid.obol.stack) without manual
// /etc/hosts entries.
//
// macOS: binds to port 5553, uses /etc/resolver/obol.stack (supports custom port).
// Linux: binds to 127.0.0.2:53, uses systemd-resolved drop-in (requires port 53).
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
	dnsImage      = "alpine:3.21"
	domain        = "obol.stack"

	// macOS: custom port, /etc/resolver handles port directive
	macHostPort = "5553"

	// Linux: systemd-resolved can't forward to non-standard ports, so we bind
	// to a loopback alias (127.0.0.2) on port 53 to avoid conflicting with
	// systemd-resolved's stub listener on 127.0.0.53:53.
	linuxBindIP   = "127.0.0.2"
	linuxBindPort = "53"

	// macOS resolver config
	macResolverDir  = "/etc/resolver"
	macResolverFile = "obol.stack"

	// Linux systemd-resolved drop-in
	resolvedDropInDir  = "/etc/systemd/resolved.conf.d"
	resolvedDropInFile = "obol-stack.conf"
)

// portBindings returns the Docker -p flags for the current OS.
func portBindings() []string {
	if runtime.GOOS == "linux" {
		return []string{
			"-p", linuxBindIP + ":" + linuxBindPort + ":53/udp",
			"-p", linuxBindIP + ":" + linuxBindPort + ":53/tcp",
		}
	}
	// macOS (and fallback)
	return []string{
		"-p", macHostPort + ":53/udp",
		"-p", macHostPort + ":53/tcp",
	}
}

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

	args := []string{"run", "-d", "--name", containerName}
	args = append(args, portBindings()...)
	args = append(args,
		"--restart", "unless-stopped",
		dnsImage,
		"sh", "-c",
		"apk add --no-cache dnsmasq >/dev/null 2>&1 && "+
			"exec dnsmasq --no-daemon "+
			"--conf-file=/dev/null "+
			"--address=/"+domain+"/127.0.0.1 "+
			"--log-facility=-",
	)

	cmd := exec.Command("docker", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start DNS container: %w\n%s", err, output)
	}

	if runtime.GOOS == "linux" {
		fmt.Printf("DNS resolver running (*.obol.stack → 127.0.0.1, %s:%s)\n", linuxBindIP, linuxBindPort)
	} else {
		fmt.Printf("DNS resolver running (*.obol.stack → 127.0.0.1, port %s)\n", macHostPort)
	}
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
// macOS: creates /etc/resolver/obol.stack (port 5553)
// Linux: creates systemd-resolved drop-in pointing to 127.0.0.2
func ConfigureSystemResolver() error {
	switch runtime.GOOS {
	case "darwin":
		return configureMacOSResolver()
	case "linux":
		return configureLinuxResolver()
	default:
		return fmt.Errorf("unsupported OS for DNS resolver: %s", runtime.GOOS)
	}
}

// RemoveSystemResolver removes the host OS DNS configuration for *.obol.stack.
func RemoveSystemResolver() {
	switch runtime.GOOS {
	case "darwin":
		removeMacOSResolver()
	case "linux":
		removeLinuxResolver()
	}
}

// IsResolverConfigured checks whether the system resolver is already set up.
func IsResolverConfigured() bool {
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(macResolverDir, macResolverFile)
		_, err := os.Stat(path)
		return err == nil
	case "linux":
		path := filepath.Join(resolvedDropInDir, resolvedDropInFile)
		_, err := os.Stat(path)
		return err == nil
	default:
		return false
	}
}

// --- macOS ---

// configureMacOSResolver creates /etc/resolver/obol.stack pointing to our DNS.
func configureMacOSResolver() error {
	path := filepath.Join(macResolverDir, macResolverFile)

	// Check if already configured correctly
	if data, err := os.ReadFile(path); err == nil {
		content := string(data)
		if strings.Contains(content, "port "+macHostPort) {
			return nil // Already configured
		}
	}

	content := fmt.Sprintf("# Managed by obol-stack — resolves *.obol.stack to localhost\nnameserver 127.0.0.1\nport %s\n", macHostPort)

	// /etc/resolver/ needs root — try sudo
	fmt.Println("Configuring macOS DNS resolver for *.obol.stack (requires sudo)...")

	mkdirCmd := exec.Command("sudo", "mkdir", "-p", macResolverDir)
	mkdirCmd.Stdout = os.Stdout
	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		return fmt.Errorf("failed to create %s (sudo required): %w", macResolverDir, err)
	}

	writeCmd := exec.Command("sudo", "tee", path)
	writeCmd.Stdin = strings.NewReader(content)
	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	fmt.Printf("Resolver configured: %s → 127.0.0.1:%s\n", path, macHostPort)
	return nil
}

// removeMacOSResolver removes /etc/resolver/obol.stack.
func removeMacOSResolver() {
	path := filepath.Join(macResolverDir, macResolverFile)
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

// --- Linux (systemd-resolved) ---

// configureLinuxResolver creates a systemd-resolved drop-in that forwards
// *.obol.stack queries to our dnsmasq on 127.0.0.2:53.
func configureLinuxResolver() error {
	// Check if systemd-resolved is active
	if err := exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run(); err != nil {
		fmt.Println("Note: systemd-resolved not detected.")
		fmt.Println("To resolve *.obol.stack, configure your DNS resolver to forward the domain:")
		fmt.Printf("  DNS server: %s (port %s) for domain %s\n", linuxBindIP, linuxBindPort, domain)
		return nil
	}

	path := filepath.Join(resolvedDropInDir, resolvedDropInFile)

	// Check if already configured
	if data, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(data), linuxBindIP) {
			return nil // Already configured
		}
	}

	content := fmt.Sprintf("# Managed by obol-stack — resolves *.obol.stack via local dnsmasq\n[Resolve]\nDNS=%s\nDomains=~%s\n", linuxBindIP, domain)

	fmt.Println("Configuring systemd-resolved for *.obol.stack (requires sudo)...")

	mkdirCmd := exec.Command("sudo", "mkdir", "-p", resolvedDropInDir)
	mkdirCmd.Stdout = os.Stdout
	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		return fmt.Errorf("failed to create %s (sudo required): %w", resolvedDropInDir, err)
	}

	writeCmd := exec.Command("sudo", "tee", path)
	writeCmd.Stdin = strings.NewReader(content)
	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	// Restart systemd-resolved to pick up the new config
	restartCmd := exec.Command("sudo", "systemctl", "restart", "systemd-resolved")
	restartCmd.Stdout = os.Stdout
	restartCmd.Stderr = os.Stderr
	if err := restartCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to restart systemd-resolved: %v\n", err)
		fmt.Println("  Run manually: sudo systemctl restart systemd-resolved")
	}

	fmt.Printf("Resolver configured: %s → %s:%s\n", path, linuxBindIP, linuxBindPort)
	return nil
}

// removeLinuxResolver removes the systemd-resolved drop-in and restarts the service.
func removeLinuxResolver() {
	path := filepath.Join(resolvedDropInDir, resolvedDropInFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if err := exec.Command("sudo", "rm", path).Run(); err != nil {
		fmt.Printf("Warning: failed to remove %s: %v\n", path, err)
		fmt.Printf("  Remove manually: sudo rm %s\n", path)
		return
	}

	// Restart systemd-resolved to drop the forwarding rule
	if err := exec.Command("sudo", "systemctl", "restart", "systemd-resolved").Run(); err != nil {
		fmt.Printf("Warning: failed to restart systemd-resolved: %v\n", err)
	}

	fmt.Printf("Removed DNS resolver config: %s\n", path)
}
