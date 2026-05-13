package main

import "testing"

func TestRedactRPCURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Subdomain + path + key — collapse to "[REDACTED].<tld>/[REDACTED]".
		{"https://lb.drpc.live/base-sepolia/ApWCPFppFkkThXTTk72LFO7ixRDuTiIR8aNktiKh6MJI", "https://[REDACTED].drpc.live/[REDACTED]"},
		{"https://base-sepolia.g.alchemy.com/v2/abc123", "https://[REDACTED].alchemy.com/[REDACTED]"},
		{"https://mainnet.infura.io/v3/abcdef0123", "https://[REDACTED].infura.io/[REDACTED]"},
		{"https://my-endpoint.quiknode.pro/abcDEF/path", "https://[REDACTED].quiknode.pro/[REDACTED]"},
		// Query-string token (drpc paid form) — query collapsed entirely.
		{"https://lb.drpc.org/ogrpc?network=base-sepolia&dkey=XYZ", "https://[REDACTED].drpc.org/[REDACTED]?[REDACTED]"},
		// Apex host (no subdomain) — leave host untouched, redact path.
		{"https://alchemy.com/v2/key", "https://alchemy.com/[REDACTED]"},
		// Port preserved.
		{"https://base-sepolia.g.alchemy.com:8545/v2/key", "https://[REDACTED].alchemy.com:8545/[REDACTED]"},
		// Userinfo redacted.
		{"https://user:pass@mainnet.infura.io/v3/key", "https://[REDACTED]@[REDACTED].infura.io/[REDACTED]"},
		// Fragment redacted.
		{"https://mainnet.infura.io/v3/key#anchor", "https://[REDACTED].infura.io/[REDACTED]#[REDACTED]"},
		// Non-paid host left alone.
		{"https://sepolia.base.org", "https://sepolia.base.org"},
		{"https://example.com/some/path?token=abc", "https://example.com/some/path?token=abc"},
		// Empty string passthrough.
		{"", ""},
	}
	for _, c := range cases {
		got := redactRPCURL(c.in)
		if got != c.want {
			t.Errorf("redactRPCURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
