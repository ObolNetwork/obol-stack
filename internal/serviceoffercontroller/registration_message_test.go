package serviceoffercontroller

import (
	"strings"
	"testing"
)

// TestAwaitingExternalRegistrationMessage verifies the operator-facing pending
// message names the completing command, embeds the chain when known, reassures
// that the offer already serves traffic, and stays within the k8s condition
// message budget enforced by truncateMessage.
func TestAwaitingExternalRegistrationMessage(t *testing.T) {
	tests := []struct {
		name        string
		chain       string
		wantChain   string // substring that must appear ("" => no --network flag)
		wantNoChain bool   // when true, "--network" must NOT appear
	}{
		{name: "mainnet chain", chain: "ethereum", wantChain: "--network ethereum"},
		{name: "testnet chain", chain: "base-sepolia", wantChain: "--network base-sepolia"},
		{name: "whitespace trimmed", chain: "  base  ", wantChain: "--network base"},
		{name: "empty chain omits flag", chain: "", wantNoChain: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := awaitingExternalRegistrationMessage(tt.chain)

			if !strings.Contains(got, "obol sell register") {
				t.Errorf("message must name the completing command; got %q", got)
			}
			if !strings.Contains(got, "serves paid traffic") {
				t.Errorf("message must reassure the offer still serves traffic; got %q", got)
			}
			if tt.wantNoChain {
				if strings.Contains(got, "--network") {
					t.Errorf("empty chain must omit the --network flag; got %q", got)
				}
			} else if !strings.Contains(got, tt.wantChain) {
				t.Errorf("message must embed %q; got %q", tt.wantChain, got)
			}
			if len(got) > 200 {
				t.Errorf("message must stay within truncateMessage budget (<=200); got %d chars", len(got))
			}
		})
	}
}
