package dns

import (
	"runtime"
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

	// Linux constants
	if linuxBindIP != "127.0.0.2" {
		t.Errorf("linuxBindIP = %q, want %q", linuxBindIP, "127.0.0.2")
	}
	if linuxBindPort != "53" {
		t.Errorf("linuxBindPort = %q, want %q", linuxBindPort, "53")
	}
	if resolvedDropInFile != "obol-stack.conf" {
		t.Errorf("resolvedDropInFile = %q, want %q", resolvedDropInFile, "obol-stack.conf")
	}
}

func TestPortBindings(t *testing.T) {
	bindings := portBindings()
	if len(bindings) != 4 {
		t.Fatalf("portBindings() returned %d elements, want 4", len(bindings))
	}

	switch runtime.GOOS {
	case "darwin":
		if bindings[1] != "5553:53/udp" {
			t.Errorf("macOS UDP binding = %q, want %q", bindings[1], "5553:53/udp")
		}
		if bindings[3] != "5553:53/tcp" {
			t.Errorf("macOS TCP binding = %q, want %q", bindings[3], "5553:53/tcp")
		}
	case "linux":
		if bindings[1] != "127.0.0.2:53:53/udp" {
			t.Errorf("Linux UDP binding = %q, want %q", bindings[1], "127.0.0.2:53:53/udp")
		}
		if bindings[3] != "127.0.0.2:53:53/tcp" {
			t.Errorf("Linux TCP binding = %q, want %q", bindings[3], "127.0.0.2:53:53/tcp")
		}
	}
}
