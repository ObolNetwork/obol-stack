package tunnel

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"stack.example.com", "stack.example.com"},
		{"https://stack.example.com", "stack.example.com"},
		{"http://stack.example.com/", "stack.example.com"},
		{"https://stack.example.com/foo?bar=baz#x", "stack.example.com"},
		{"  stack.example.com  ", "stack.example.com"},
	}

	for _, tt := range tests {
		if got := normalizeHostname(tt.in); got != tt.want {
			t.Fatalf("normalizeHostname(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseQuickTunnelURL(t *testing.T) {
	logs := `
2026-01-14T12:00:00Z INF | Your quick tunnel URL is:                   |
2026-01-14T12:00:00Z INF | https://seasonal-deck-organisms-sf.trycloudflare.com |
`

	url, ok := parseQuickTunnelURL(logs)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if url != "https://seasonal-deck-organisms-sf.trycloudflare.com" {
		t.Fatalf("unexpected url: %q", url)
	}
}

func TestParseFirstUUID(t *testing.T) {
	in := "Tunnel ID: 9E2E6F3D-1F78-4A8D-A2FA-7B2A7E00B8B3 (created)"
	got, err := parseFirstUUID(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "9e2e6f3d-1f78-4a8d-a2fa-7b2a7e00b8b3" {
		t.Fatalf("unexpected uuid: %q", got)
	}
}

func TestBuildRemoteManagedSecretYAML(t *testing.T) {
	token := "tok_123"
	manifest := string(buildRemoteManagedSecretYAML(token))

	if !strings.Contains(manifest, "kind: Secret") {
		t.Fatalf("expected Secret manifest, got:\n%s", manifest)
	}
	if !strings.Contains(manifest, "name: "+tunnelTokenSecretName) {
		t.Fatalf("expected secret name %q, got:\n%s", tunnelTokenSecretName, manifest)
	}
	if !strings.Contains(manifest, "namespace: "+tunnelNamespace) {
		t.Fatalf("expected namespace %q, got:\n%s", tunnelNamespace, manifest)
	}

	wantB64 := base64.StdEncoding.EncodeToString([]byte(token))
	if !strings.Contains(manifest, tunnelTokenSecretKey+": "+wantB64) {
		t.Fatalf("expected base64 token for key %q, got:\n%s", tunnelTokenSecretKey, manifest)
	}
}
