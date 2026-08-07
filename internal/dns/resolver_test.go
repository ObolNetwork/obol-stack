package dns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConstants(t *testing.T) {
	if containerName != "obol-dns" {
		t.Errorf("containerName = %q, want %q", containerName, "obol-dns")
	}

	if domain != "obol.stack" {
		t.Errorf("domain = %q, want %q", domain, "obol.stack")
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

func TestIsValidHostname(t *testing.T) {
	valid := []string{"obol.stack", "hermes-abc.obol.stack", "openclaw-my-agent.obol.stack", "a"}
	for _, h := range valid {
		if !isValidHostname(h) {
			t.Errorf("isValidHostname(%q) = false, want true", h)
		}
	}

	// Belt-and-suspenders guard for the Canary402 audit finding: a hostname
	// carrying a newline (e.g. from an unsanitized agent --id) must never be
	// written to /etc/hosts, even if it slipped past upstream validation.
	invalid := []string{
		"",
		"evil\n127.0.0.1 attacker.com",
		"has space",
		"has/slash",
	}
	for _, h := range invalid {
		if isValidHostname(h) {
			t.Errorf("isValidHostname(%q) = true, want false", h)
		}
	}
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

// EnsureHostsEntries replaces the managed block wholesale, and most callers
// (internal/hermes, internal/openclaw) only know about agent hostnames. If the
// storefront preview origin were merely appended by one caller, whichever call
// ran last would silently drop it — which is exactly what happened on a real
// stack: `obol stack up` added it, then the agent-resume path overwrote the
// block without it, leaving the /storefront iframe unable to resolve its
// preview origin. It must be emitted unconditionally, like the base domain.
func TestEnsureHostsBlockAlwaysCarriesPreviewOrigin(t *testing.T) {
	for name, hostnames := range map[string][]string{
		"no caller hostnames":    nil,
		"agent hostnames only":   {"hermes-obol-agent.obol.stack", "obol-agent.obol.stack"},
		"preview passed by hand": {StorefrontPreviewHostname},
	} {
		t.Run(name, func(t *testing.T) {
			block := buildHostsBlock(hostnames)

			if !strings.Contains(block, "127.0.0.1 "+StorefrontPreviewHostname) {
				t.Fatalf("managed block is missing the preview origin:\n%s", block)
			}

			if got := strings.Count(block, StorefrontPreviewHostname); got != 1 {
				t.Fatalf("preview origin appears %d times, want exactly 1:\n%s", got, block)
			}

			if !strings.Contains(block, "127.0.0.1 "+domain) {
				t.Fatalf("managed block is missing the base domain:\n%s", block)
			}
		})
	}
}
