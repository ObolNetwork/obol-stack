package validate

import (
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
	valid := []string{"0", "0.001", "1.5", "100"}
	for _, s := range valid {
		if err := Price(s); err != nil {
			t.Errorf("Price(%q) = %v", s, err)
		}
	}

	invalid := []string{"", "abc", "-1"}
	for _, s := range invalid {
		if err := Price(s); err == nil {
			t.Errorf("Price(%q) = nil, want error", s)
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
