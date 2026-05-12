package main

import "testing"

func TestRedactRPCURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://lb.drpc.live/base-sepolia/ApWCPFppFkkThXTTk72LFO7ixRDuTiIR8aNktiKh6MJI", "https://lb.drpc.live/base-sepolia/[REDACTED]"},
		{"https://base-sepolia.g.alchemy.com/v2/abc123", "https://base-sepolia.g.alchemy.com/v2/[REDACTED]"},
		{"https://mainnet.infura.io/v3/abcdef0123", "https://mainnet.infura.io/v3/[REDACTED]"},
		{"https://lb.drpc.org/ogrpc?network=base-sepolia&dkey=XYZ", "https://lb.drpc.org/ogrpc?network=base-sepolia&dkey=[REDACTED]"},
		{"https://sepolia.base.org", "https://sepolia.base.org"},
		{"", ""},
	}
	for _, c := range cases {
		got := redactRPCURL(c.in)
		if got != c.want {
			t.Errorf("redactRPCURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
