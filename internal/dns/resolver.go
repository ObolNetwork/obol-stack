// Package dns manages wildcard *.obol.stack resolution on the host machine.
//
// Two OS-specific strategies are used:
//
// macOS: A dnsmasq Docker container on port 5553 + /etc/resolver/obol.stack.
// This is the native macOS approach for per-domain DNS resolution.
//
// Linux: NetworkManager's built-in dnsmasq plugin resolves *.obol.stack →
// 127.0.0.1 directly. Two config files, no Docker container, no bridge/veth
// hacks, no systemd-resolved drop-ins. Works on any distro with NM
// (Ubuntu, Fedora, Debian desktop, Arch, RHEL, openSUSE, Mint, Pop!_OS).
//
// Systems without NetworkManager get instructions to install it.
package dns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	containerName = "obol-dns"
	dnsImage      = "alpine:3.21"
	domain        = "obol.stack"

	// OS name constants for runtime.GOOS comparisons.
	osDarwin = "darwin"
	osLinux  = "linux"

	// macOS: custom port, /etc/resolver handles port directive
	macHostPort = "5553"

	// macOS resolver config
	macResolverDir  = "/etc/resolver"
	macResolverFile = "obol.stack"

	// Linux NM dnsmasq plugin config
	nmConfDir     = "/etc/NetworkManager/conf.d"
	nmConfFile    = "obol-dns.conf"
	nmDnsmasqDir  = "/etc/NetworkManager/dnsmasq.d"
	nmDnsmasqFile = "obol-stack.conf"

	// resolv.conf management — NM dnsmasq takes over from systemd-resolved stub
	nmResolvConf       = "/run/NetworkManager/resolv.conf"
	systemResolvConf   = "/etc/resolv.conf"
	resolvedStubConf   = "/run/systemd/resolve/stub-resolv.conf"
	resolvedDropInDir  = "/etc/systemd/resolved.conf.d"
	resolvedDropInFile = "obol-stack.conf"
)

// --- Public API ---

// EnsureRunning starts the DNS resolver. On macOS, this starts a dnsmasq
// Docker container. On Linux, this is a no-op — NM dnsmasq handles
// resolution without a container.
func EnsureRunning() error {
	if runtime.GOOS == osDarwin {
		return ensureMacOSContainer()
	}

	return nil
}

// Stop removes the DNS resolver container (macOS only).
func Stop() {
	if runtime.GOOS != osDarwin {
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
// Linux: configures NM dnsmasq plugin for wildcard resolution
func ConfigureSystemResolver() error {
	switch runtime.GOOS {
	case osDarwin:
		return configureMacOSResolver()
	case osLinux:
		return configureLinuxResolver()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// RemoveSystemResolver removes the host OS DNS configuration for *.obol.stack.
func RemoveSystemResolver() {
	switch runtime.GOOS {
	case osDarwin:
		removeMacOSResolver()
	case osLinux:
		removeNMDnsmasq()
	}

	RemoveHostsEntries()
}

// IsResolverConfigured checks whether the system resolver is already set up.
func IsResolverConfigured() bool {
	switch runtime.GOOS {
	case osDarwin:
		_, err := os.Stat(filepath.Join(macResolverDir, macResolverFile))
		return err == nil
	case osLinux:
		return hasNMDnsmasqConfig() && isNMResolvConfActive()
	default:
		return false
	}
}

// --- /etc/hosts management ---
//
// macOS Sequoia (15.x) has a known issue where /etc/resolver/ files don't
// reliably forward subdomain queries to custom nameservers. As a fallback,
// we also write entries to /etc/hosts for known hostnames.

const (
	hostsMarkerBegin = "# BEGIN obol-stack managed entries"
	hostsMarkerEnd   = "# END obol-stack managed entries"
	hostsFile        = "/etc/hosts"
)

// EnsureHostsEntries adds /etc/hosts entries for the given hostnames.
// Always includes "obol.stack" plus any additional hostnames (e.g. openclaw subdomains).
// Entries are idempotent — existing managed block is replaced.
func EnsureHostsEntries(hostnames []string) error {
	// Always include the base domain.
	all := []string{domain}

	seen := map[string]bool{domain: true}
	for _, h := range hostnames {
		if h != "" && !seen[h] {
			all = append(all, h)
			seen[h] = true
		}
	}

	// Build the managed block.
	var block strings.Builder
	block.WriteString(hostsMarkerBegin + "\n")

	for _, h := range all {
		fmt.Fprintf(&block, "127.0.0.1 %s\n", h)
	}

	block.WriteString(hostsMarkerEnd + "\n")

	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", hostsFile, err)
	}

	content := string(data)

	newContent := replaceOrAppendBlock(content, block.String())
	if newContent == content {
		return nil // no change needed
	}

	// Ensure sudo credentials are cached before running "sudo tee", which
	// needs stdin for the file content and therefore cannot also read a
	// password. "sudo -v" prompts interactively if needed, then the
	// subsequent "sudo tee" reuses the cached credentials.
	if err := ensureSudoCached(); err != nil {
		return fmt.Errorf("sudo authentication required to update %s: %w", hostsFile, err)
	}

	writeCmd := exec.Command("sudo", "tee", hostsFile)
	writeCmd.Stdin = strings.NewReader(newContent)
	writeCmd.Stdout = nil

	writeCmd.Stderr = os.Stderr
	if err := writeCmd.Run(); err != nil {
		return fmt.Errorf("write %s: %w", hostsFile, err)
	}

	return nil
}

// RemoveHostsEntries removes the obol-stack managed block from /etc/hosts.
func RemoveHostsEntries() {
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return
	}

	content := string(data)

	cleaned := removeBlock(content)
	if cleaned == content {
		return
	}

	writeCmd := exec.Command("sudo", "tee", hostsFile)
	writeCmd.Stdin = strings.NewReader(cleaned)
	writeCmd.Stdout = nil
	writeCmd.Stderr = os.Stderr
	writeCmd.Run() //nolint:errcheck
}

// ensureSudoCached validates cached sudo credentials or prompts the user
// interactively. This must be called before any "sudo <cmd>" that pipes
// stdin from a non-terminal source (e.g. sudo tee with file content).
func ensureSudoCached() error {
	// Fast path: check if credentials are already cached (non-interactive).
	if exec.Command("sudo", "-n", "true").Run() == nil {
		return nil
	}
	if os.Getenv("OBOL_NONINTERACTIVE") == "true" {
		return fmt.Errorf("sudo credentials not cached")
	}
	// Credentials not cached — prompt the user interactively.
	cmd := exec.Command("sudo", "-v")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// replaceOrAppendBlock replaces an existing managed block or appends a new one.
func replaceOrAppendBlock(content, block string) string {
	start := strings.Index(content, hostsMarkerBegin)

	end := strings.Index(content, hostsMarkerEnd)
	if start >= 0 && end > start {
		return content[:start] + block + content[end+len(hostsMarkerEnd)+1:]
	}
	// Append with a blank line separator.
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	return content + "\n" + block
}

// removeBlock strips the managed block from content.
func removeBlock(content string) string {
	start := strings.Index(content, hostsMarkerBegin)

	end := strings.Index(content, hostsMarkerEnd)
	if start < 0 || end <= start {
		return content
	}
	// Remove the block plus trailing newline.
	after := end + len(hostsMarkerEnd)
	if after < len(content) && content[after] == '\n' {
		after++
	}

	return content[:start] + content[after:]
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
	writeCmd.Stdout = nil

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

// --- Linux (NetworkManager dnsmasq plugin) ---

// configureLinuxResolver sets up NM's dnsmasq plugin for *.obol.stack.
// Returns a non-fatal error when NetworkManager is unavailable (e.g. headless
// servers) — the caller falls back to /etc/hosts entries.
func configureLinuxResolver() error {
	if configureNMDnsmasq() {
		return nil
	}

	// NM not available — return quiet error; caller handles the fallback message.
	return fmt.Errorf("NetworkManager not available (headless or server system)")
}

// hasNMDnsmasqConfig checks if the NM dnsmasq config for obol.stack exists.
func hasNMDnsmasqConfig() bool {
	path := filepath.Join(nmDnsmasqDir, nmDnsmasqFile)
	_, err := os.Stat(path)

	return err == nil
}

// isNMResolvConfActive returns true if /etc/resolv.conf is managed by NM's dnsmasq.
// Systems with systemd-resolved as the stub resolver need the symlink updated.
func isNMResolvConfActive() bool {
	data, err := os.ReadFile(systemResolvConf)
	if err != nil {
		return false
	}

	return strings.Contains(string(data), "Generated by NetworkManager")
}

// configureNMDnsmasq sets up NM's built-in dnsmasq plugin for *.obol.stack.
// Returns true if successful, false if NM is not available.
func configureNMDnsmasq() bool {
	// Check if NetworkManager is running
	if err := exec.Command("systemctl", "is-active", "--quiet", "NetworkManager").Run(); err != nil {
		return false
	}

	// Check if dnsmasq binary is available (NM plugin requires it)
	if _, err := exec.LookPath("dnsmasq"); err != nil {
		return false
	}

	// Check if already fully configured (dnsmasq rule + resolv.conf routing)
	if hasNMDnsmasqConfig() && isNMResolvConfActive() {
		return true
	}

	// If dnsmasq config files already exist, skip writing them and jump to
	// resolv.conf update (handles systems where NM was restarted but symlink
	// wasn't updated, e.g. because of a prior partial installation).
	if hasNMDnsmasqConfig() {
		updateResolvConf()
		cleanupResolvedDropIn()
		fmt.Printf("DNS resolver configured: *.%s → 127.0.0.1 (NM dnsmasq plugin)\n", domain)

		return true
	}

	fmt.Println("Configuring NetworkManager DNS for *.obol.stack (requires sudo)...")

	nmDNSMode := getNMDNSMode()

	// Create NM dnsmasq.d directory and write the rule
	mkdirCmd := exec.Command("sudo", "mkdir", "-p", nmDnsmasqDir)
	mkdirCmd.Stdout = os.Stdout

	mkdirCmd.Stderr = os.Stderr
	if err := mkdirCmd.Run(); err != nil {
		fmt.Printf("Warning: failed to create %s: %v\n", nmDnsmasqDir, err)
		return false
	}

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

	// Point /etc/resolv.conf to NM's resolv.conf so applications query NM's
	// dnsmasq directly, bypassing systemd-resolved's DoT stub.
	updateResolvConf()

	// Remove the legacy systemd-resolved drop-in if left from a prior install.
	cleanupResolvedDropIn()

	fmt.Printf("DNS resolver configured: *.%s → 127.0.0.1 (NM dnsmasq plugin)\n", domain)

	return true
}

// updateResolvConf points /etc/resolv.conf at NM's generated resolv.conf.
// NM writes nameserver 127.0.1.1 there when dns=dnsmasq is active.
// This bypasses systemd-resolved's stub (which may have DoT issues with
// local addresses) and sends all DNS queries directly to NM's dnsmasq.
func updateResolvConf() {
	// Wait for NM to generate its resolv.conf after restart (max 3s)
	for range 6 {
		if _, err := os.Stat(nmResolvConf); err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	cmd := exec.Command("sudo", "ln", "-sf", nmResolvConf, systemResolvConf)
	cmd.Stdout = os.Stdout

	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: failed to update %s: %v\n", systemResolvConf, err)
		fmt.Printf("  Run manually: sudo ln -sf %s %s\n", nmResolvConf, systemResolvConf)

		return
	}

	// Wait for DNS to actually resolve after NM restart (max 10s).
	// NM's dnsmasq needs a moment to start accepting queries.
	for range 20 {
		if err := exec.Command("nslookup", "github.com").Run(); err == nil {
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Println("Warning: DNS not yet responding after NM restart; may need a moment to stabilize")
}

// cleanupResolvedDropIn removes the legacy systemd-resolved drop-in that was
// used by earlier obol-stack versions to route obol.stack via the DNS stub.
// With NM dnsmasq owning /etc/resolv.conf, the drop-in is redundant.
func cleanupResolvedDropIn() {
	path := filepath.Join(resolvedDropInDir, resolvedDropInFile)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}

	exec.Command("sudo", "rm", path).Run() //nolint:errcheck
}

// getNMDNSMode returns the current NetworkManager dns= mode.
func getNMDNSMode() string {
	// Check our own conf file first
	path := filepath.Join(nmConfDir, nmConfFile)
	if data, err := os.ReadFile(path); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "dns="); ok {
				return after
			}
		}
	}

	// Check main NM config
	data, err := os.ReadFile("/etc/NetworkManager/NetworkManager.conf")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "dns="); ok {
			return after
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
		// Restore systemd-resolved's stub resolver in /etc/resolv.conf
		if isNMResolvConfActive() {
			restoreCmd := exec.Command("sudo", "ln", "-sf", resolvedStubConf, systemResolvConf)
			if err := restoreCmd.Run(); err != nil {
				fmt.Printf("Warning: failed to restore %s: %v\n", systemResolvConf, err)
				fmt.Printf("  Run manually: sudo ln -sf %s %s\n", resolvedStubConf, systemResolvConf)
			}
		}

		fmt.Println("Removed NM dnsmasq DNS config")
	}
}
