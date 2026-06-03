package main

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// registrationNoticeMode selects the wording for a post-publish registration
// advisory, based on how the publishing command handles the on-chain tx.
type registrationNoticeMode int

const (
	// regNoticeDeclareOnly: the command only declares spec.registration and
	// relies on the controller + a manual `obol sell register` (e.g. sell agent).
	regNoticeDeclareOnly registrationNoticeMode = iota
	// regNoticeAutoSkipped: registration is enabled but the auto-register step
	// was not attempted (e.g. the tunnel URL was not ready in time).
	regNoticeAutoSkipped
	// regNoticeAutoFailed: the auto-register step ran but returned an error
	// (e.g. an unfunded agent wallet on mainnet, or RPC connectivity).
	regNoticeAutoFailed
)

// registrationNotice carries everything needed to advise an operator about the
// consequences of registration being enabled but not yet completed on-chain.
type registrationNotice struct {
	Mode        registrationNoticeMode
	Chain       string // payment/registration chain (e.g. "ethereum", "base-sepolia")
	PayTo       string // offer payout address (--wallet/--pay-to)
	AgentWallet string // address that signs the on-chain registration tx; may be ""
	OfferName   string
	Namespace   string
}

// isTestnetChain reports whether a chain name denotes a test network, used only
// to pick gas wording. Unknown names default to mainnet (the cautious choice —
// "real funds" over "use a faucet").
func isTestnetChain(chain string) bool {
	c := strings.ToLower(strings.TrimSpace(chain))
	for _, marker := range []string{"sepolia", "amoy", "fuji", "goerli", "holesky", "mumbai", "testnet", "devnet"} {
		if strings.Contains(c, marker) {
			return true
		}
	}
	return false
}

// registrationGasPhrase describes what the on-chain registration tx costs on
// the given chain, so operators understand why an unfunded wallet stalls it.
func registrationGasPhrase(chain string) string {
	chain = strings.TrimSpace(chain)
	if chain == "" {
		return "gas on the target chain"
	}
	if isTestnetChain(chain) {
		return "testnet gas on " + chain + " (use a faucet)"
	}
	return "real funds for gas on " + chain
}

// formatRegistrationNotice builds the advisory lines shown after a ServiceOffer
// is published with registration enabled but the on-chain tx not yet recorded.
// It is pure (no UI/IO) so the wording is unit-testable. The first returned
// line is the headline; the remainder are detail lines.
//
// Behaviour it makes legible (previously silent):
//   - The offer serves paid traffic immediately, yet `obol sell status` reports
//     Ready=False until the ERC-8004 registration tx lands, because Registered
//     gates Ready (the storefront catalog uses a looser check and still lists
//     the offer with a RegistrationPending badge).
//   - The tx is signed by the agent's remote-signer wallet and needs gas — on
//     mainnet that is real funds, which is the usual reason it stalls.
//   - The agent wallet (registration owner) and the payout wallet are allowed
//     to differ (hot signer / cold payee); the split is surfaced, not blocked.
//   - `--no-register` is the escape hatch for a clean Ready=True with no tx.
func formatRegistrationNotice(n registrationNotice) []string {
	var lines []string

	switch n.Mode {
	case regNoticeAutoFailed:
		lines = append(lines, "On-chain ERC-8004 registration did not complete.")
	case regNoticeAutoSkipped:
		lines = append(lines, "Registration is enabled but the on-chain ERC-8004 tx was not submitted (a prerequisite was not ready).")
	default: // regNoticeDeclareOnly
		lines = append(lines, "Registration is enabled; the on-chain ERC-8004 tx is not submitted automatically.")
	}

	lines = append(lines,
		"The offer already serves paid traffic, but `obol sell status` reports Ready=False (AwaitingExternalRegistration) until the registration tx lands.")

	complete := "Complete it with: obol sell register"
	if name := strings.TrimSpace(n.OfferName); name != "" {
		complete += " " + name
	}
	if ns := strings.TrimSpace(n.Namespace); ns != "" {
		complete += " -n " + ns
	}
	if chain := strings.TrimSpace(n.Chain); chain != "" {
		complete += " --chain " + chain
	}
	if wallet := strings.TrimSpace(n.AgentWallet); wallet != "" {
		complete += fmt.Sprintf(" — signed by the agent wallet %s, which needs %s.", wallet, registrationGasPhrase(n.Chain))
	} else {
		complete += " — signed by the agent wallet, which needs " + registrationGasPhrase(n.Chain) + "."
	}
	lines = append(lines, complete)

	// The auto-register path already prints the delegation note before it
	// attempts the tx, so only the paths that never reached that point repeat it.
	if n.Mode != regNoticeAutoFailed {
		if note := signerPayeeDelegationNote(n.AgentWallet, n.PayTo); note != "" {
			lines = append(lines, note)
		}
	}

	lines = append(lines, "Don't need on-chain discovery? Re-run the sell command with --no-register for a clean Ready=True (no tx, no gas).")
	return lines
}

// printRegistrationNotice renders a registrationNotice via the UI. Best-effort
// and never returns an error: the headline draws attention as a warning, the
// remaining lines are dimmed detail.
func printRegistrationNotice(u *ui.UI, n registrationNotice) {
	lines := formatRegistrationNotice(n)
	if len(lines) == 0 {
		return
	}
	u.Blank()
	u.Warnf("%s", lines[0])
	for _, line := range lines[1:] {
		u.Dim("  " + line)
	}
}
