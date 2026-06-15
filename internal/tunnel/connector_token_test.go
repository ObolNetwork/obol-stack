package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func makeConnectorToken(t *testing.T) string {
	t.Helper()
	payload := map[string]string{
		"a": "0123456789abcdef0123456789abcdef",
		"t": "11111111-2222-3333-4444-555555555555",
		"s": base64.StdEncoding.EncodeToString([]byte("super-secret")),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestParseConnectorToken(t *testing.T) {
	tok := makeConnectorToken(t)
	claims, err := parseConnectorToken(tok)
	if err != nil {
		t.Fatalf("parseConnectorToken: %v", err)
	}
	if claims.TunnelID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("tunnel id = %q", claims.TunnelID)
	}
	if claims.AccountTag != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("account tag = %q", claims.AccountTag)
	}

	for _, bad := range []string{"", "not-base64-@@@", base64.StdEncoding.EncodeToString([]byte(`{"a":"x"}`))} {
		if _, err := parseConnectorToken(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestExtractConnectorToken(t *testing.T) {
	tok := makeConnectorToken(t)
	cases := map[string]string{
		"bare":            tok,
		"run command":     "cloudflared tunnel run --token " + tok,
		"equals form":     "cloudflared tunnel run --token=" + tok,
		"service install": "cloudflared service install " + tok,
		"with newlines":   "  cloudflared tunnel run --token " + tok + "\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if got := extractConnectorToken(input); got != tok {
				t.Fatalf("extractConnectorToken(%q) = %q, want token", input, got)
			}
		})
	}

	if got := extractConnectorToken("cloudflared tunnel run --help"); got != "" {
		t.Fatalf("expected empty for non-token input, got %q", got)
	}
}
