package tunnel

import (
	"os"
	"path/filepath"
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

func TestParseQuickTunnelURL_PicksLatest(t *testing.T) {
	logs := `
2026-01-14T12:00:00Z INF | https://old-quick-tunnel.trycloudflare.com |
2026-01-14T12:05:00Z INF | https://new-quick-tunnel.trycloudflare.com |
`

	url, ok := parseQuickTunnelURL(logs)
	if !ok {
		t.Fatalf("expected ok=true")
	}

	if url != "https://new-quick-tunnel.trycloudflare.com" {
		t.Fatalf("unexpected url: %q", url)
	}
}

func TestBuildLocalManagedConfigYAMLRoutesOnlyRequestedHostname(t *testing.T) {
	out := string(buildLocalManagedConfigYAML("stack.example.com", "00000000-0000-0000-0000-000000000000"))

	for _, want := range []string{
		"tunnel: 00000000-0000-0000-0000-000000000000",
		"- hostname: stack.example.com",
		"service: http://traefik.traefik.svc.cluster.local:80",
		"- service: http_status:404",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}

	if strings.Count(out, "hostname:") != 1 {
		t.Fatalf("persistent tunnel config should expose exactly one hostname:\n%s", out)
	}
	for _, unexpected := range []string{"obol-agent.obol.stack", "hermes-obol-agent.obol.stack", "*.obol.stack"} {
		if strings.Contains(out, unexpected) {
			t.Fatalf("persistent tunnel config exposes local agent hostname %q:\n%s", unexpected, out)
		}
	}
}

func TestPatchAgentBaseURL_Insert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-obol.yaml")

	original := `extraEnv:
  - name: REMOTE_SIGNER_URL
    value: http://remote-signer:9000

skills:
  enabled: false
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://mystack.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "name: AGENT_BASE_URL") {
		t.Errorf("patched file missing AGENT_BASE_URL:\n%s", content)
	}

	if !strings.Contains(content, "value: https://mystack.example.com") {
		t.Errorf("patched file missing tunnel URL value:\n%s", content)
	}

	if !strings.Contains(content, "REMOTE_SIGNER_URL") {
		t.Errorf("patched file lost REMOTE_SIGNER_URL:\n%s", content)
	}
}

func TestPatchAgentBaseURL_Update(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-obol.yaml")

	original := `extraEnv:
  - name: REMOTE_SIGNER_URL
    value: http://remote-signer:9000
  - name: AGENT_BASE_URL
    value: https://old.example.com

skills:
  enabled: false
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://new.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "value: https://new.example.com") {
		t.Errorf("patched file missing updated URL:\n%s", content)
	}

	if strings.Contains(content, "old.example.com") {
		t.Errorf("patched file still has old URL:\n%s", content)
	}
	// Should only have one AGENT_BASE_URL (no duplicate insertion).
	if strings.Count(content, "AGENT_BASE_URL") != 1 {
		t.Errorf("expected exactly 1 AGENT_BASE_URL entry:\n%s", content)
	}
}

func TestPatchAgentBaseURL_InsertHermesManifestIndentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-hermes.yaml")

	original := `resources:
  - apiVersion: apps/v1
    kind: Deployment
    spec:
      template:
        spec:
          containers:
            - name: openclaw
              env:
                - name: REMOTE_SIGNER_URL
                  value: http://remote-signer:9000
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://mystack.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "                - name: AGENT_BASE_URL") {
		t.Fatalf("patched Hermes manifest missing preserved indent for name:\n%s", content)
	}

	if !strings.Contains(content, "                  value: https://mystack.example.com") {
		t.Fatalf("patched Hermes manifest missing preserved indent for value:\n%s", content)
	}
}
