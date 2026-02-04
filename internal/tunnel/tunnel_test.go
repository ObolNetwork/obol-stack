package tunnel

import "testing"

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
