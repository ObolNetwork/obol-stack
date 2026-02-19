package dns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConstants(t *testing.T) {
	if containerName != "obol-dns" {
		t.Errorf("containerName = %q, want %q", containerName, "obol-dns")
	}
	if domain != "obol.stack" {
		t.Errorf("domain = %q, want %q", domain, "obol.stack")
	}
	if hostsMarker != "# obol-stack-managed" {
		t.Errorf("hostsMarker = %q, want %q", hostsMarker, "# obol-stack-managed")
	}

	// macOS constants
	if macHostPort != "5553" {
		t.Errorf("macHostPort = %q, want %q", macHostPort, "5553")
	}
	if macResolverFile != "obol.stack" {
		t.Errorf("macResolverFile = %q, want %q", macResolverFile, "obol.stack")
	}

	// Linux NM constants
	if nmConfFile != "obol-dns.conf" {
		t.Errorf("nmConfFile = %q, want %q", nmConfFile, "obol-dns.conf")
	}
	if nmDnsmasqFile != "obol-stack.conf" {
		t.Errorf("nmDnsmasqFile = %q, want %q", nmDnsmasqFile, "obol-stack.conf")
	}
}

func TestGetNMDNSMode(t *testing.T) {
	// Test with non-existent files — should return empty
	mode := getNMDNSMode()
	// We can't guarantee what the system returns, so just verify it doesn't panic
	_ = mode
}

func TestHasNMDnsmasqConfig(t *testing.T) {
	// On a clean system without obol config, this should return false
	// unless the test system has it installed
	result := hasNMDnsmasqConfig()
	path := filepath.Join(nmDnsmasqDir, nmDnsmasqFile)
	_, fileExists := os.Stat(path)
	if result != (fileExists == nil) {
		t.Errorf("hasNMDnsmasqConfig() = %v, but file exists = %v", result, fileExists == nil)
	}
}

func TestHostsEntryExists(t *testing.T) {
	// Test with a hostname that shouldn't exist in /etc/hosts with our marker
	if hostsEntryExists("nonexistent-test-host-12345.obol.stack") {
		t.Error("hostsEntryExists returned true for nonexistent host")
	}
}
