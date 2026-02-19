// Package dns manages wildcard *.obol.stack resolution on the host machine.
//
// Three OS-specific strategies are used:
//
// macOS: A dnsmasq Docker container on port 5553 + /etc/resolver/obol.stack.
// This is the native macOS approach for per-domain DNS resolution.
//
// Linux (with NetworkManager): NM's built-in dnsmasq plugin resolves
// *.obol.stack → 127.0.0.1 directly. Two config files, no Docker container,
// no bridge/veth hacks, no systemd-resolved drop-ins.
//
// Linux (without NetworkManager): Managed /etc/hosts entries for each
// deployment. No wildcard support, but entries are added/removed
// programmatically as services are deployed.
package dns

import (
	"bufio"
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
	hostsMarker   = "# obol-stack-managed"

	// macOS: custom port, /etc/resolver handles port directive
	macHostPort = "5553"

	// macOS resolver config
	macResolverDir  = "/etc/resolver"
	macResolverFile = "obol.stack"

	// Linux NM dnsmasq plugin config
	nmConfDir      = "/etc/NetworkManager/conf.d"
	nmConfFile     = "obol-dns.conf"
	nmDnsmasqDir   = "/etc/NetworkManager/dnsmasq.d"
	nmDnsmasqFile  = "obol-stack.conf"
	hostsFile      = "/etc/hosts"
)

// --- Public API ---

// EnsureRunning starts the DNS resolver. On macOS, this starts a dnsmasq
// Docker container. On Linux, this is a no-op (NM dnsmasq or /etc/hosts
// handle resolution without a container).
func EnsureRunning() error {
	if runtime.GOOS == "darwin" {
		return ensureMacOSContainer()
	}
	// Linux: no container needed — NM dnsmasq plugin or /etc/hosts
	return nil
}

// Stop removes the DNS resolver container (macOS only).
func Stop() {
	if runtime.GOOS != "darwin" {
		return
	}
	if out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return
	}
	exec.Command("docker", "rm", "-f", containerName).Run() //nolint:errcheck
	fmt.Println("DNS resolver stopped")
}

// ConfigureSystemResolver sets up the host OS to route *.obol.stack queries
// to localhost. Requires sudo on first run.
//
// macOS: creates /etc/resolver/obol.stack
// Linux: configures NM dnsmasq plugin, or falls back to /etc/hosts
func ConfigureSystemResolver() error {
	switch runtime.GOOS {
	case "darwin":
		return configureMacOSResolver()
	case "linux":
		return configureLinuxResolver()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
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
		_, err := os.Stat(filepath.Join(macResolverDir, macResolverFile))
		return err == nil
	case "linux":
		if hasNMDnsmasqConfig() {
			return true
		}
		return hasHostsEntries()
	default:
		return false
	}
}

// AddHostEntry ensures a hostname entry exists in /etc/hosts for the given
// subdomain (e.g., "openclaw-default"). This is used as a fallback when NM
// dnsmasq is not available, and is always safe to call — it no-ops if NM
// dnsmasq is handling wildcard resolution.
func AddHostEntry(subdomain string) error {
	if runtime.GOOS == "darwin" || hasNMDnsmasqConfig() {
		return nil // Wildcard resolution handles this
	}
	hostname := subdomain + "." + domain
	return addHostsEntry(hostname)
}

// RemoveHostEntry removes a hostname entry from /etc/hosts for the given
// subdomain. Safe to call even if the entry doesn't exist.
func RemoveHostEntry(subdomain string) {
	if runtime.GOOS == "darwin" || hasNMDnsmasqConfig() {
		return // Wildcard resolution handles this
	}
	hostname := subdomain + "." + domain
	removeHostsEntry(hostname)
}

// --- macOS ---

func ensureMacOSContainer() error {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil
	}

	exec.Command("docker", "rm", "-f", containerName).Run() //nolint:errcheck

	fmt.Println("Starting DNS resolver for *.obol.stack...")

	cmd := exec.Command("docker", "run", "-d", "--name", containerName,
		"-p", macHostPort+":53/udp",
		"-p", macHostPort+":53/tcp",
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

	fmt.Printf("DNS resolver running (*.obol.stack → 127.0.0.1, port %s)\n", macHostPort)
	return nil
}

func configureMacOSResolver() error {
	path := filepath.Join(macResolverDir, macResolverFile)

	if data, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(data), "port "+macHostPort) {
			return nil
		}
	}

	content := fmt.Sprintf("# Managed by obol-stack — resolves *.obol.stack to localhost\nnameserver 127.0.0.1\nport %s\n", macHostPort)

	fmt.Println("Configuring macOS DNS resolver for *.obol.stack (requires sudo)...")

	mkdirCmd := exec.Command("sudo", "mkdir", "-p", macResolverDir)
	mkdirCmd.Stdout = os.Stdout
	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		return fmt.Errorf("failed to create %s (sudo required): %w", macResolverDir, err)
	}

	writeCmd := exec.Command("sudo", "tee", path)
	writeCmd.Stdin = strings.NewReader(content)
	writeCmd.Stdout = nil // suppress tee stdout
	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	fmt.Printf("Resolver configured: %s → 127.0.0.1:%s\n", path, macHostPort)
	return nil
}

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

// --- Linux ---

// configureLinuxResolver tries NM dnsmasq first, falls back to /etc/hosts.
func configureLinuxResolver() error {
	// Tier 1: NetworkManager dnsmasq plugin
	if configureNMDnsmasq() {
		return nil
	}

	// Tier 2: /etc/hosts fallback (add base domain entry)
	fmt.Println("NetworkManager not available — using /etc/hosts for *.obol.stack resolution")
	fmt.Println("Note: wildcard DNS is not supported in this mode.")
	fmt.Println("      Entries will be added per deployment.")
	return addHostsEntry(domain)
}

// removeLinuxResolver removes NM dnsmasq config and/or /etc/hosts entries.
func removeLinuxResolver() {
	removeNMDnsmasq()
	removeAllHostsEntries()
}

// --- Linux Tier 1: NetworkManager dnsmasq plugin ---

// hasNMDnsmasqConfig checks if the NM dnsmasq config for obol.stack exists.
func hasNMDnsmasqConfig() bool {
	path := filepath.Join(nmDnsmasqDir, nmDnsmasqFile)
	_, err := os.Stat(path)
	return err == nil
}

// configureNMDnsmasq sets up NM's built-in dnsmasq plugin for *.obol.stack.
// Returns true if successful, false if NM is not available.
func configureNMDnsmasq() bool {
	// Check if NetworkManager is running
	if err := exec.Command("systemctl", "is-active", "--quiet", "NetworkManager").Run(); err != nil {
		return false
	}

	// Check if dnsmasq is available (NM plugin requires it)
	if _, err := exec.LookPath("dnsmasq"); err != nil {
		// Try to detect if it's available as an NM plugin without the binary
		// On some distros, NM's dnsmasq support is built-in
		if _, err := os.Stat("/usr/lib/NetworkManager"); err != nil {
			fmt.Println("Note: dnsmasq not found. Install it for wildcard DNS support:")
			fmt.Println("  sudo apt install dnsmasq-base  # Debian/Ubuntu")
			fmt.Println("  sudo dnf install dnsmasq       # Fedora/RHEL")
			return false
		}
	}

	// Check if already configured
	if hasNMDnsmasqConfig() {
		return true
	}

	fmt.Println("Configuring NetworkManager DNS for *.obol.stack (requires sudo)...")

	// Check if NM already uses dns=dnsmasq
	nmDNSMode := getNMDNSMode()

	// Create NM dnsmasq conf.d directory
	mkdirCmd := exec.Command("sudo", "mkdir", "-p", nmDnsmasqDir)
	mkdirCmd.Stdout = os.Stdout
	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to create %s: %v\n", nmDnsmasqDir, err)
		return false
	}

	// Write dnsmasq rule: *.obol.stack → 127.0.0.1
	dnsmasqConf := "# Managed by obol-stack\naddress=/obol.stack/127.0.0.1\n"
	writeCmd := exec.Command("sudo", "tee", filepath.Join(nmDnsmasqDir, nmDnsmasqFile))
	writeCmd.Stdin = strings.NewReader(dnsmasqConf)
	writeCmd.Stdout = nil
	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to write dnsmasq config: %v\n", err)
		return false
	}

	// If NM is not already using dns=dnsmasq, configure it
	if nmDNSMode != "dnsmasq" {
		mkdirCmd2 := exec.Command("sudo", "mkdir", "-p", nmConfDir)
		mkdirCmd2.Stdout = os.Stdout
		mkdirCmd2.Stderr = os.Stderr
		if err := mkdirCmd2.Run(); err != nil {
			fmt.Printf("Warning: failed to create %s: %v\n", nmConfDir, err)
			return false
		}

		nmConf := "# Managed by obol-stack — enables dnsmasq for wildcard DNS\n[main]\ndns=dnsmasq\n"
		writeCmd2 := exec.Command("sudo", "tee", filepath.Join(nmConfDir, nmConfFile))
		writeCmd2.Stdin = strings.NewReader(nmConf)
		writeCmd2.Stdout = nil
		writeCmd2.Stderr = os.Stderr
		if err := writeCmd2.Run(); err != nil {
			fmt.Printf("Warning: failed to write NM config: %v\n", err)
			return false
		}
	}

	// Restart NetworkManager to pick up changes
	restartCmd := exec.Command("sudo", "systemctl", "restart", "NetworkManager")
	restartCmd.Stdout = os.Stdout
	restartCmd.Stderr = os.Stderr
	if err := restartCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to restart NetworkManager: %v\n", err)
		fmt.Println("  Run manually: sudo systemctl restart NetworkManager")
		return true // Config files are in place, will work after manual restart
	}

	fmt.Printf("DNS resolver configured: *.%s → 127.0.0.1 (NM dnsmasq plugin)\n", domain)
	return true
}

// getNMDNSMode returns the current NetworkManager dns= mode.
func getNMDNSMode() string {
	// Check our own conf file first
	path := filepath.Join(nmConfDir, nmConfFile)
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "dns=") {
				return strings.TrimPrefix(strings.TrimSpace(line), "dns=")
			}
		}
	}

	// Check main NM config
	data, err := os.ReadFile("/etc/NetworkManager/NetworkManager.conf")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "dns=") {
			return strings.TrimPrefix(strings.TrimSpace(line), "dns=")
		}
	}
	return ""
}

// removeNMDnsmasq removes the NM dnsmasq config files and restarts NM.
func removeNMDnsmasq() {
	dnsmasqPath := filepath.Join(nmDnsmasqDir, nmDnsmasqFile)
	nmPath := filepath.Join(nmConfDir, nmConfFile)

	removedAny := false

	if _, err := os.Stat(dnsmasqPath); err == nil {
		if err := exec.Command("sudo", "rm", dnsmasqPath).Run(); err != nil {
			fmt.Printf("Warning: failed to remove %s: %v\n", dnsmasqPath, err)
		} else {
			removedAny = true
		}
	}

	if _, err := os.Stat(nmPath); err == nil {
		if err := exec.Command("sudo", "rm", nmPath).Run(); err != nil {
			fmt.Printf("Warning: failed to remove %s: %v\n", nmPath, err)
		} else {
			removedAny = true
		}
	}

	if removedAny {
		if err := exec.Command("sudo", "systemctl", "restart", "NetworkManager").Run(); err != nil {
			fmt.Printf("Warning: failed to restart NetworkManager: %v\n", err)
		}
		fmt.Println("Removed NM dnsmasq DNS config")
	}
}

// --- Linux Tier 2: /etc/hosts fallback ---

// hasHostsEntries checks if any obol-stack-managed entries exist in /etc/hosts.
func hasHostsEntries() bool {
	f, err := os.Open(hostsFile)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), hostsMarker) {
			return true
		}
	}
	return false
}

// addHostsEntry adds a single hostname entry to /etc/hosts if not present.
func addHostsEntry(hostname string) error {
	// Check if entry already exists
	if hostsEntryExists(hostname) {
		return nil
	}

	entry := fmt.Sprintf("127.0.0.1 %s %s", hostname, hostsMarker)

	appendCmd := exec.Command("sudo", "sh", "-c", fmt.Sprintf("echo '%s' >> %s", entry, hostsFile))
	appendCmd.Stdout = os.Stdout
	appendCmd.Stderr = os.Stderr
	if err := appendCmd.Run(); err != nil {
		return fmt.Errorf("failed to add %s to /etc/hosts (sudo required): %w", hostname, err)
	}

	fmt.Printf("Added /etc/hosts entry: %s → 127.0.0.1\n", hostname)
	return nil
}

// removeHostsEntry removes a single hostname entry from /etc/hosts.
func removeHostsEntry(hostname string) {
	if !hostsEntryExists(hostname) {
		return
	}

	// Use sed to remove the specific line
	pattern := fmt.Sprintf("/127\\.0\\.0\\.1.*%s.*%s/d", strings.ReplaceAll(hostname, ".", "\\."), hostsMarker)
	if err := exec.Command("sudo", "sed", "-i", pattern, hostsFile).Run(); err != nil {
		fmt.Printf("Warning: failed to remove %s from /etc/hosts: %v\n", hostname, err)
	}
}

// removeAllHostsEntries removes all obol-stack-managed entries from /etc/hosts.
func removeAllHostsEntries() {
	if !hasHostsEntries() {
		return
	}

	pattern := fmt.Sprintf("/%s/d", strings.ReplaceAll(hostsMarker, "/", "\\/"))
	if err := exec.Command("sudo", "sed", "-i", pattern, hostsFile).Run(); err != nil {
		fmt.Printf("Warning: failed to clean /etc/hosts: %v\n", err)
		fmt.Printf("  Remove manually: sudo sed -i '/%s/d' %s\n", hostsMarker, hostsFile)
		return
	}
	fmt.Println("Removed /etc/hosts entries for obol.stack")
}

// hostsEntryExists checks if a specific hostname is in /etc/hosts with our marker.
func hostsEntryExists(hostname string) bool {
	f, err := os.Open(hostsFile)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, hostname) && strings.Contains(line, hostsMarker) {
			return true
		}
	}
	return false
}
