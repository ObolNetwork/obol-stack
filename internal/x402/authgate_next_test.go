package x402

import "testing"

func TestSanitizeNextPath(t *testing.T) {
	for next, want := range map[string]string{
		"":                         "",
		"/":                        "/",
		"/services/foo?x=1":        "/services/foo?x=1",
		"//evil.com":               "",
		"/\\evil.com":              "",
		"/a\\b":                    "",
		"/\t/evil.com":             "",
		"/\n/evil.com":             "",
		"https://evil.com":         "",
		"javascript:alert(1)":      "",
		"evil.com":                 "",
		"/ok/../still-same-origin": "/ok/../still-same-origin",
	} {
		if got := sanitizeNextPath(next); got != want {
			t.Errorf("sanitizeNextPath(%q) = %q, want %q", next, got, want)
		}
	}
}

func TestLogSafe(t *testing.T) {
	if got := logSafe("bad sig\nx402-verifier: forged"); got != "bad sig x402-verifier: forged" {
		t.Fatalf("logSafe = %q", got)
	}
}
