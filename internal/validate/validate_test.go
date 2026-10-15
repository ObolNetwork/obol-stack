package validate

import (
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	valid := []string{"my-service", "a", "abc123", "test-inference-1"}
	for _, s := range valid {
		if err := Name(s); err != nil {
			t.Errorf("Name(%q) = %v, want nil", s, err)
		}
	}

	invalid := []string{
		"",
		"MyService",       // uppercase
		"-leading-hyphen", // starts with hyphen
		"has spaces",
		"has_underscore",
		"../etc/passwd",                // path traversal
		"a" + string(make([]byte, 63)), // too long (64 chars)
		"has\nnewline",                 // /etc/hosts injection (Canary402 agent --id finding)
		"has/slash",
	}
	for _, s := range invalid {
		if err := Name(s); err == nil {
			t.Errorf("Name(%q) = nil, want error", s)
		}
	}
}

func TestPrice(t *testing.T) {
	valid := []string{"0.001", "1.5", "100", ".5", "5.", "0.000000000000000001", "1000000000000"}
	for _, s := range valid {
		if err := Price(s); err != nil {
			t.Errorf("Price(%q) = %v", s, err)
		}
	}

	invalid := []string{
		"", "abc", "-1", "+1", "0", "0.0", "Inf", "+Inf", "NaN", "1e3", "1E-3",
		"1,5", "$1", " 1", "1 ", "1.2.3", ".", "0x10",
		"0.0000000000000000001",                  // 19 decimal places
		"1000000000000.000000000000000001",       // above the 1e12 cap
		"99999999999999999999999999999999999999", // huge
	}
	for _, s := range invalid {
		if err := Price(s); err == nil {
			t.Errorf("Price(%q) = nil, want error", s)
		}
	}
}

func TestPriceForToken(t *testing.T) {
	tests := []struct {
		price    string
		decimals int
		symbol   string
		wantErr  string
	}{
		{"0.000001", 6, "USDC", ""},
		{"0.0000001", 6, "USDC", "max 6 decimal places for USDC"},
		{"0.000000000000000001", 18, "OBOL", ""},
		{"0.0000000000000000001", 18, "OBOL", "max 18 decimal places for OBOL"},
		{"0", 6, "USDC", "greater than zero"},
	}
	for _, tc := range tests {
		err := PriceForToken(tc.price, tc.decimals, tc.symbol)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("PriceForToken(%q, %d) = %v, want nil", tc.price, tc.decimals, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("PriceForToken(%q, %d) = %v, want error containing %q", tc.price, tc.decimals, err, tc.wantErr)
		}
	}
}

func TestURL(t *testing.T) {
	valid := []string{"http://localhost:8080", "https://example.com/path"}
	for _, s := range valid {
		if err := URL(s); err != nil {
			t.Errorf("URL(%q) = %v", s, err)
		}
	}

	invalid := []string{"", "not-a-url", "/just/a/path"}
	for _, s := range invalid {
		if err := URL(s); err == nil {
			t.Errorf("URL(%q) = nil, want error", s)
		}
	}
}
