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
		"../etc/passwd", // path traversal
		"a" + string(make([]byte, 63)), // too long (64 chars)
	}
	for _, s := range invalid {
		if err := Name(s); err == nil {
			t.Errorf("Name(%q) = nil, want error", s)
		}
	}
}

func TestNamespace(t *testing.T) {
	if err := Namespace("default"); err != nil {
		t.Errorf("Namespace(default) = %v", err)
	}
	if err := Namespace(""); err == nil {
		t.Error("Namespace('') should error")
	}
}

func TestWalletAddress(t *testing.T) {
	valid := "0xAbCd1234567890abcdef1234567890abcdef1234"
	if err := WalletAddress(valid); err != nil {
		t.Errorf("WalletAddress(%q) = %v", valid, err)
	}

	invalid := []string{
		"",
		"not-a-wallet",
		"AbCd1234567890abcdef1234567890abcdef1234", // no 0x
		"0xshort",
		"0xZZZZ1234567890abcdef1234567890abcdef1234", // invalid hex
	}
	for _, s := range invalid {
		if err := WalletAddress(s); err == nil {
			t.Errorf("WalletAddress(%q) = nil, want error", s)
		}
	}
}

func TestChainName(t *testing.T) {
	valid := []string{"base-sepolia", "base", "ethereum", "mainnet", "base-mainnet"}
	for _, s := range valid {
		if err := ChainName(s); err != nil {
			t.Errorf("ChainName(%q) = %v", s, err)
		}
	}

	invalid := []string{"", "polygon", "unknown", "solana"}
	for _, s := range invalid {
		if err := ChainName(s); err == nil {
			t.Errorf("ChainName(%q) = nil, want error", s)
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

func TestPath(t *testing.T) {
	valid := []string{"", "/services/my-api", "/rpc/mainnet"}
	for _, s := range valid {
		if err := Path(s); err != nil {
			t.Errorf("Path(%q) = %v", s, err)
		}
	}

	invalid := []string{"../etc/passwd", "/foo/%2e%2e/bar", "/has\x00null"}
	for _, s := range invalid {
		if err := Path(s); err == nil {
			t.Errorf("Path(%q) = nil, want error", s)
		}
	}
}

func TestNoControlChars(t *testing.T) {
	if err := NoControlChars("normal text\nwith\ttabs"); err != nil {
		t.Errorf("should allow newlines and tabs: %v", err)
	}
	if err := NoControlChars("has\x00null"); err == nil {
		t.Error("should reject null byte")
	}
	if err := NoControlChars("has\x01soh"); err == nil {
		t.Error("should reject SOH")
	}
}
