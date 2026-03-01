package x402

import "testing"

func TestValidateWallet(t *testing.T) {
	valid := []string{
		"0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"0xABCDEF1234567890ABCDEF1234567890ABCDEF12",
		"0x0000000000000000000000000000000000000000",
	}
	for _, addr := range valid {
		if err := ValidateWallet(addr); err != nil {
			t.Errorf("ValidateWallet(%q) = %v, want nil", addr, err)
		}
	}

	invalid := []string{
		"",
		"0x",
		"0xGGGG",
		"not-an-address",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", // missing 0x prefix
		"0xdeadbeef",                                // too short
		"0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefAA", // too long
		`0x"; malicious: true; "`,                   // injection attempt
	}
	for _, addr := range invalid {
		if err := ValidateWallet(addr); err == nil {
			t.Errorf("ValidateWallet(%q) = nil, want error", addr)
		}
	}
}
