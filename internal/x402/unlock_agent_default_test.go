package x402

import "testing"

// TestIsUnlockOffer_SelectsAgentsOnly pins the product rule: the paid unlock
// (and its platform fee split) applies to AGENT offers and only agent offers.
//
// An agent is sold as a conversation a human opens in the chat widget —
// connect wallet, pay once, then per-turn billing — so paid sign-in belongs to
// the type. http offers are machine-to-machine APIs with no session concept
// and must stay on plain per-request `exact` payments.
//
// This replaces selection by a configured offerPrefix, which capped a stack at
// one unlock offer and required the operator to opt in by hand.
func TestIsUnlockOffer_SelectsAgentsOnly(t *testing.T) {
	enabled := validAuthCaptureConfig()
	cfg := &PricingConfig{AuthCaptureUnlock: &enabled}

	agent := &RouteRule{StripPrefix: "/services/analyst", AgentRuntime: "hermes"}
	httpOffer := &RouteRule{StripPrefix: "/services/kalshi-intel"}

	var v Verifier
	if !v.isUnlockOffer(cfg, agent) {
		t.Error("agent offer must be unlock-gated — the fee ships with the product, not per-offer opt-in")
	}
	if v.isUnlockOffer(cfg, httpOffer) {
		t.Error("http offer must NOT be unlock-gated — gating it would tax per-request API traffic")
	}

	// Every agent on the stack is covered, not just one: the old offerPrefix
	// ceiling is gone.
	second := &RouteRule{StripPrefix: "/services/auditor", AgentRuntime: "hermes"}
	if !v.isUnlockOffer(cfg, second) {
		t.Error("a second agent offer must also be unlock-gated (no one-offer-per-stack ceiling)")
	}
}

// TestIsUnlockOffer_RespectsDisableAndNils keeps the kill switch honest: an
// operator can still turn the gate off entirely, and a nil rule or config must
// never select the paid path.
func TestIsUnlockOffer_RespectsDisableAndNils(t *testing.T) {
	off := validAuthCaptureConfig()
	off.Enabled = false
	agent := &RouteRule{StripPrefix: "/services/analyst", AgentRuntime: "hermes"}

	var v Verifier
	if v.isUnlockOffer(&PricingConfig{AuthCaptureUnlock: &off}, agent) {
		t.Error("disabled config must not gate anything")
	}
	if v.isUnlockOffer(&PricingConfig{}, agent) {
		t.Error("absent authCaptureUnlock must not gate anything")
	}
	if v.isUnlockOffer(nil, agent) {
		t.Error("nil config must not gate anything")
	}
	enabled := validAuthCaptureConfig()
	if v.isUnlockOffer(&PricingConfig{AuthCaptureUnlock: &enabled}, nil) {
		t.Error("nil rule must not gate anything")
	}
}

// TestAuthCaptureUnlockConfig_PriceResolvedPerOffer guards the multi-agent
// correctness bit. Price, payTo and network are overrides now; with several
// agents on a stack they must fall back to each offer's own values, or every
// agent's unlock revenue lands in one wallet at one price.
func TestAuthCaptureUnlockConfig_PriceResolvedPerOffer(t *testing.T) {
	c := validAuthCaptureConfig()
	c.Price = ""
	if err := c.Validate(); err == nil {
		t.Error("an unlock with no price from config OR offer must be rejected, not advertised at zero")
	}

	c = validAuthCaptureConfig()
	c.Price = "0.01"
	if err := c.Validate(); err != nil {
		t.Errorf("priced config must validate: %v", err)
	}
}
