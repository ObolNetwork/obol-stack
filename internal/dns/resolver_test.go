package dns

import "testing"

func TestConstants(t *testing.T) {
	// Verify constants haven't drifted — these are referenced by both the
	// Docker container config and the macOS resolver file.
	if containerName != "obol-dns" {
		t.Errorf("containerName = %q, want %q", containerName, "obol-dns")
	}
	if hostPort != "5553" {
		t.Errorf("hostPort = %q, want %q", hostPort, "5553")
	}
	if domain != "obol.stack" {
		t.Errorf("domain = %q, want %q", domain, "obol.stack")
	}
	if resolverFile != "obol.stack" {
		t.Errorf("resolverFile = %q, want %q", resolverFile, "obol.stack")
	}
}
