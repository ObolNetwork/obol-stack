package x402

import (
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// SchemeAuthCapture is the x402 v2 scheme whose escrow splits a fee to a third
// party at charge() time. It is the only scheme that can carry a platform fee:
// "exact" moves the full amount to payTo and has nowhere to put one.
const SchemeAuthCapture = "auth-capture"

// platformFeeForRule returns the per-request fee configuration that applies to
// rule, or nil when the platform fee does not apply.
//
// AGENT OFFERS ONLY. An agent is sold as a conversation billed per turn, so a
// per-request fee tracks the value delivered. http offers are machine-to-machine
// APIs whose buyers are third-party clients we do not ship, and moving them off
// "exact" would break them for no proportionate gain.
//
// The returned config is a COPY: Validate applies deadline defaults in place and
// Price is resolved per payment option by the caller.
func platformFeeForRule(cfg *PricingConfig, rule *RouteRule) *AuthCaptureUnlockConfig {
	if cfg == nil || cfg.AuthCaptureUnlock == nil || !cfg.AuthCaptureUnlock.Enabled {
		return nil
	}
	// rule.AgentRuntime is populated from the ServiceOffer in
	// serviceoffer_source.go and is the same signal mergeAgentExtras uses to
	// decide a rule is an agent, so this adds no new plumbing.
	if rule == nil || rule.AgentRuntime == "" {
		return nil
	}
	c := *cfg.AuthCaptureUnlock
	c.applyDeadlineDefaults()
	return &c
}

// platformFeeHooks returns the ForwardAuth hooks that carry the fee through a
// paid request: substituting the client-signed auth-capture requirement before
// verify, and attributing revenue after settle. Both are nil when the fee does
// not apply to this route, which leaves the payment path byte-for-byte what it
// was before the fee existed.
func (v *Verifier) platformFeeHooks(cfg *PricingConfig, mr *matchedRoute) (
	func(x402types.PaymentPayload, x402types.PaymentRequirements) (x402types.PaymentRequirements, error),
	func(x402types.PaymentRequirements, string),
) {
	fee := platformFeeForRule(cfg, mr.rule)
	if fee == nil {
		return nil, nil
	}
	resolve := func(payload x402types.PaymentPayload, matched x402types.PaymentRequirements) (x402types.PaymentRequirements, error) {
		// The exact twin settles against our own requirement, as it always has.
		if matched.Scheme != SchemeAuthCapture {
			return matched, nil
		}
		if err := validateSignedAuthCapture(payload.Accepted, matched, fee.CaptureDeadlineSecs, time.Now().Unix()); err != nil {
			return matched, err
		}
		return payload.Accepted, nil
	}
	settled := func(req x402types.PaymentRequirements, _ string) {
		if req.Scheme != SchemeAuthCapture {
			return
		}
		v.recordFeeRevenue(req, fee.FeeRecipient, fee.MaxFeeBps)
	}
	return resolve, settled
}

// buildPlatformFeeRequirement builds the auth-capture twin of an already-built
// exact requirement: same chain, asset, payTo, price, and maxTimeoutSeconds
// (signing window), but routed through the escrow so minFeeBps..maxFeeBps
// reaches feeRecipient on-chain at charge time.
//
// maxTimeoutSeconds must match the exact twin's so dual-scheme 402s advertise
// identical client signing windows; it is independent of CaptureDeadlineSecs.
//
// Returns nil (and logs) when the fee cannot be priced or the config is
// unusable. Callers keep serving the exact requirement in that case — a
// misconfigured fee must never take down a payable route.
func buildPlatformFeeRequirement(fee *AuthCaptureUnlockConfig, chain ChainInfo, asset AssetInfo, price, payTo string, maxTimeoutSeconds int64, pattern string) *x402types.PaymentRequirements {
	f := *fee
	// Per-request fee: the price is the OFFER's per-turn price, never the
	// config's. The config field only exists for the standalone unlock gate.
	f.Price = price
	req, err := BuildAuthCaptureRequirement(chain, asset, &f, payTo, maxTimeoutSeconds, time.Now())
	if err != nil {
		log.Printf("x402-verifier: platform fee unavailable for route %q, serving exact only: %v", pattern, err)
		return nil
	}
	// ponytail: no mergeAgentExtras here. The exact twin carries the agent
	// metadata; auth-capture's Extra is the client-signed payment struct and the
	// proven-on-chain unlock path never added non-scheme keys to it.
	return &req
}

// validateSignedAuthCapture checks the requirement the client actually SIGNED
// against the one we offered, and returns the signed copy for verify+settle.
//
// Auth-capture must be settled against payload.accepted VERBATIM: the signed
// PaymentInfo hash commits the server-issued captureDeadline/refundDeadline,
// which are now-relative and therefore drift between the 402 that issued them
// and this request. Forwarding our freshly-rebuilt requirement instead would
// invalidate every signature.
//
// Settling a client-supplied struct is only safe because every economically
// meaningful field is pinned here first — the facilitator does not know our
// intended feeRecipient, payTo or amount, so a blind forward would let a client
// redirect the fee to itself or underpay.
func validateSignedAuthCapture(signed, offered x402types.PaymentRequirements, maxCaptureSecs uint64, now int64) error {
	if signed.Scheme != SchemeAuthCapture {
		return fmt.Errorf("scheme %q, want %s", signed.Scheme, SchemeAuthCapture)
	}
	if signed.Network != offered.Network {
		return fmt.Errorf("network %q, want %q", signed.Network, offered.Network)
	}
	if !strings.EqualFold(signed.Asset, offered.Asset) {
		return fmt.Errorf("asset %q, want %q", signed.Asset, offered.Asset)
	}
	if !strings.EqualFold(signed.PayTo, offered.PayTo) {
		return fmt.Errorf("payTo %q, want %q", signed.PayTo, offered.PayTo)
	}
	if signed.Amount != offered.Amount {
		return fmt.Errorf("amount %q, want %q", signed.Amount, offered.Amount)
	}
	if signed.Extra == nil {
		return fmt.Errorf("missing extra")
	}
	// Every key we offered must come back unchanged except the two deadlines,
	// which are bounded below. Comparing the whole map (rather than an
	// enumerated allowlist) means a field added to the requirement builder is
	// pinned automatically instead of silently becoming client-controlled.
	for k, want := range offered.Extra {
		if k == "captureDeadline" || k == "refundDeadline" {
			continue
		}
		if got := signed.Extra[k]; !sameExtraValue(got, want) {
			return fmt.Errorf("extra[%q] = %v, want %v", k, got, want)
		}
	}
	cd, rd := exI64(signed.Extra, "captureDeadline"), exI64(signed.Extra, "refundDeadline")
	if cd <= now+6 {
		return fmt.Errorf("captureDeadline %d not > now+6 (%d)", cd, now+6)
	}
	// Upper bound: the client echoes a deadline we issued, so it can only have
	// aged. Without this a client could sign an arbitrarily distant deadline and
	// hold an authorization against the payer far beyond the configured window.
	if maxCaptureSecs > 0 && cd > now+int64(maxCaptureSecs) {
		return fmt.Errorf("captureDeadline %d exceeds now+%d (%d)", cd, maxCaptureSecs, now+int64(maxCaptureSecs))
	}
	if rd < cd {
		return fmt.Errorf("refundDeadline %d < captureDeadline %d", rd, cd)
	}
	return nil
}

// sameExtraValue compares two JSON-decoded Extra values. Numbers survive a
// round-trip as float64 while ours are typed (uint16, int64, …), so numeric
// values are compared by value and everything else by its JSON rendering.
func sameExtraValue(got, want any) bool {
	if wantNum, ok := numericExtra(want); ok {
		gotNum, ok := numericExtra(got)
		return ok && gotNum == wantNum
	}
	if wantStr, ok := want.(string); ok {
		gotStr, ok := got.(string)
		// Addresses round-trip through the client with arbitrary casing.
		return ok && strings.EqualFold(gotStr, wantStr)
	}
	return fmt.Sprintf("%v", got) == fmt.Sprintf("%v", want)
}

func numericExtra(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// recordFeeRevenue attributes a settled auth-capture payment to the fee
// metrics. maxFeeBps is what the escrow is authorized to take and what the
// facilitator's capture uses, so it is the fee actually collected.
func (v *Verifier) recordFeeRevenue(req x402types.PaymentRequirements, feeRecipient string, feeBps uint16) {
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		log.Printf("x402-verifier: parse settled amount %q for fee metrics", req.Amount)
		return
	}
	fee := new(big.Int).Div(new(big.Int).Mul(amount, big.NewInt(int64(feeBps))), big.NewInt(10000))
	labels := prometheus.Labels{"network": req.Network, "asset": req.Asset, "fee_recipient": feeRecipient}
	v.metrics.settledVolumeAtomic.With(labels).Add(bigIntToFloat(amount))
	v.metrics.feeRevenueAtomic.With(labels).Add(bigIntToFloat(fee))
}
