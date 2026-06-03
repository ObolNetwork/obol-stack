package main

import (
	"strings"
	"testing"
)

func TestIsTestnetChain(t *testing.T) {
	tests := []struct {
		chain string
		want  bool
	}{
		{"ethereum", false},
		{"base", false},
		{"polygon", false},
		{"arbitrum-one", false},
		{"base-sepolia", true},
		{"polygon-amoy", true},
		{"avalanche-fuji", true},
		{"holesky", true},
		{"  Base-Sepolia  ", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			if got := isTestnetChain(tt.chain); got != tt.want {
				t.Errorf("isTestnetChain(%q) = %v; want %v", tt.chain, got, tt.want)
			}
		})
	}
}

func TestRegistrationGasPhrase(t *testing.T) {
	tests := []struct {
		chain string
		want  string
	}{
		{"ethereum", "real funds for gas on ethereum"},
		{"base", "real funds for gas on base"},
		{"base-sepolia", "testnet gas on base-sepolia (use a faucet)"},
		{"", "gas on the target chain"},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			if got := registrationGasPhrase(tt.chain); got != tt.want {
				t.Errorf("registrationGasPhrase(%q) = %q; want %q", tt.chain, got, tt.want)
			}
		})
	}
}

// TestFormatRegistrationNotice pins the operator-facing wording for each mode,
// including the Ready=False consequence, the completing command, the gas
// phrasing, and the conditional delegation (signer != payTo) note.
func TestFormatRegistrationNotice(t *testing.T) {
	const (
		agentWallet = "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		coldPayee   = "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	)

	mustContain := func(t *testing.T, lines []string, subs ...string) {
		t.Helper()
		joined := strings.Join(lines, "\n")
		for _, s := range subs {
			if !strings.Contains(joined, s) {
				t.Errorf("notice missing %q; got:\n%s", s, joined)
			}
		}
	}
	mustNotContain := func(t *testing.T, lines []string, s string) {
		t.Helper()
		if joined := strings.Join(lines, "\n"); strings.Contains(joined, s) {
			t.Errorf("notice unexpectedly contains %q; got:\n%s", s, joined)
		}
	}

	t.Run("declare-only mainnet with cold payee", func(t *testing.T) {
		lines := formatRegistrationNotice(registrationNotice{
			Mode:        regNoticeDeclareOnly,
			Chain:       "ethereum",
			PayTo:       coldPayee,
			AgentWallet: agentWallet,
			OfferName:   "aeon7",
			Namespace:   "hermes-obol-agent",
		})
		mustContain(t, lines,
			"not submitted automatically",
			"Ready=False",
			"AwaitingExternalRegistration",
			"obol sell register aeon7 -n hermes-obol-agent --chain ethereum",
			agentWallet,
			"real funds for gas on ethereum",
			"--no-register",
		)
		// signer != payTo → delegation note present.
		mustContain(t, lines, coldPayee)
	})

	t.Run("auto-failed omits delegation note", func(t *testing.T) {
		lines := formatRegistrationNotice(registrationNotice{
			Mode:        regNoticeAutoFailed,
			Chain:       "base",
			PayTo:       coldPayee,
			AgentWallet: agentWallet,
			OfferName:   "my-api",
			Namespace:   "default",
		})
		mustContain(t, lines, "did not complete", "obol sell register my-api -n default --chain base")
		// The auto-register path already printed the delegation note, so the
		// failure advisory must not repeat the payTo address.
		mustNotContain(t, lines, coldPayee)
	})

	t.Run("auto-skipped testnet", func(t *testing.T) {
		lines := formatRegistrationNotice(registrationNotice{
			Mode:        regNoticeAutoSkipped,
			Chain:       "base-sepolia",
			PayTo:       coldPayee,
			AgentWallet: agentWallet,
			OfferName:   "demo",
			Namespace:   "default",
		})
		mustContain(t, lines, "was not submitted", "testnet gas on base-sepolia (use a faucet)", coldPayee)
	})

	t.Run("empty agent wallet still advises", func(t *testing.T) {
		lines := formatRegistrationNotice(registrationNotice{
			Mode:      regNoticeDeclareOnly,
			Chain:     "ethereum",
			PayTo:     coldPayee,
			OfferName: "x",
			Namespace: "default",
		})
		mustContain(t, lines, "signed by the agent wallet, which needs real funds for gas on ethereum")
		// No signer address known → no delegation note.
		mustNotContain(t, lines, coldPayee)
	})

	t.Run("matching payee omits delegation note", func(t *testing.T) {
		lines := formatRegistrationNotice(registrationNotice{
			Mode:        regNoticeDeclareOnly,
			Chain:       "ethereum",
			PayTo:       agentWallet,
			AgentWallet: agentWallet,
			OfferName:   "x",
			Namespace:   "default",
		})
		// signerPayeeDelegationNote returns "" when signer == payTo, so no
		// "differs from offer payTo" line should appear.
		mustNotContain(t, lines, "differs from offer payTo")
	})
}
