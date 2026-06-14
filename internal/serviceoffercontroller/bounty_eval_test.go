package serviceoffercontroller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
)

const (
	evalA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	evalB = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	evalC = "0xcccccccccccccccccccccccccccccccccccccccc"
)

// testEvalBounty is a quorum-verified (mode=required, rerun-tolerance) bounty
// with k=3, claimed and submitted.
func testEvalBounty(name string) *monetizeapi.ServiceBounty {
	sb := testBounty(name)
	sb.Spec.Acceptance.Method = "rerun-tolerance"
	sb.Spec.Eval = monetizeapi.ServiceBountyEval{
		K:    3,
		Mode: monetizeapi.EvalModeRequired,
		Payment: monetizeapi.ServiceBountyEvalPayment{
			Asset: "OBOL", PerEvaluator: "2.00", FundedBy: "poster", Settle: "batch-settlement",
		},
	}
	return sb
}

func claimAndSubmit(t *testing.T, c *Controller, ns, name string) {
	t.Helper()
	key := ns + "/" + name
	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, ns, name, map[string]string{
		"obol.org/claim": "0x2222222222222222222222222222222222222222",
	})
	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, ns, name, map[string]string{
		"obol.org/submit": `{"resultHash":"0xbeef","reportURI":"file:///r.json"}`,
	})
	reconcileBountyUntilSettled(t, c, key)
}

func commitAndReveal(t *testing.T, c *Controller, ns, name string, scores map[string]int64) {
	t.Helper()
	key := ns + "/" + name
	// Commit phase: all evaluators commit before anyone reveals.
	for addr, score := range scores {
		annotateBounty(t, c, ns, name, map[string]string{
			"obol.org/eval-commit-" + addr: monetizeapi.EvalCommitHash(score, "salt-"+addr, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, key)
	// Reveal phase.
	for addr, score := range scores {
		annotateBounty(t, c, ns, name, map[string]string{
			"obol.org/eval-reveal-" + addr: fmt.Sprintf(`{"score":%d,"salt":"salt-%s"}`, score, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, key)
}

func TestEvalMarket_QuorumPassToPaid(t *testing.T) {
	sb := testEvalBounty("quorum-pass")
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "quorum-pass")
	commitAndReveal(t, c, ns, "quorum-pass", map[string]int64{evalA: 90, evalB: 85, evalC: 40})

	got := getBounty(t, c, ns, "quorum-pass")
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason != "EvaluatorQuorum" {
		t.Fatalf("Verified reason = %q, want EvaluatorQuorum", reason)
	}
	if !bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Fatal("median 85 >= 50 must verify")
	}
	if got.Status.WeightedScore != 85 {
		t.Errorf("WeightedScore = %d, want median 85", got.Status.WeightedScore)
	}
	if got.Status.Phase != bountyPhasePaid {
		t.Errorf("phase = %q, want Paid (quorum verdict releases the escrow)", got.Status.Phase)
	}
	// The 40 is >20 from the median 85 → out of band; the others in band.
	for _, ev := range got.Status.Evaluations {
		wantBand := ev.Score >= 65
		if ev.WithinBand != wantBand {
			t.Errorf("evaluator %s score %d withinBand = %v, want %v", ev.Address, ev.Score, ev.WithinBand, wantBand)
		}
	}
}

func TestEvalMarket_QuorumRejects(t *testing.T) {
	sb := testEvalBounty("quorum-reject")
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "quorum-reject")
	commitAndReveal(t, c, ns, "quorum-reject", map[string]int64{evalA: 10, evalB: 20, evalC: 90})

	got := getBounty(t, c, ns, "quorum-reject")
	if bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Fatal("median 20 < 50 must not verify")
	}
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason != "EvaluatorQuorum" {
		t.Fatalf("Verified reason = %q, want EvaluatorQuorum", reason)
	}
	if got.Status.Phase != bountyPhaseRejected {
		t.Errorf("phase = %q, want Rejected", got.Status.Phase)
	}
	if bountyConditionIsTrue(got.Status.Conditions, "Paid") {
		t.Fatal("rejected bounty must not pay")
	}
}

// The Kleros address-binding steal: evaluator C copies B's commitment hash,
// then replays B's revealed {score, salt}. The hash binds B's address, so C's
// reveal cannot verify — C grades BadReveal and is excluded from the median.
func TestEvalMarket_CommitBoundToAddress(t *testing.T) {
	sb := testEvalBounty("copycat")
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "copycat")

	honest := map[string]int64{evalA: 80, evalB: 75}
	for addr, score := range honest {
		annotateBounty(t, c, ns, "copycat", map[string]string{
			"obol.org/eval-commit-" + addr: monetizeapi.EvalCommitHash(score, "salt-"+addr, addr),
		})
	}
	// C copies B's commitment verbatim.
	annotateBounty(t, c, ns, "copycat", map[string]string{
		"obol.org/eval-commit-" + evalC: monetizeapi.EvalCommitHash(75, "salt-"+evalB, evalB),
	})
	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/copycat")

	for addr, score := range honest {
		annotateBounty(t, c, ns, "copycat", map[string]string{
			"obol.org/eval-reveal-" + addr: fmt.Sprintf(`{"score":%d,"salt":"salt-%s"}`, score, addr),
		})
	}
	// C replays B's reveal.
	annotateBounty(t, c, ns, "copycat", map[string]string{
		"obol.org/eval-reveal-" + evalC: fmt.Sprintf(`{"score":75,"salt":"salt-%s"}`, evalB),
	})
	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/copycat")

	got := getBounty(t, c, ns, "copycat")
	var copycat *monetizeapi.ServiceBountyEvaluation
	for i := range got.Status.Evaluations {
		if strings.EqualFold(got.Status.Evaluations[i].Address, evalC) {
			copycat = &got.Status.Evaluations[i]
		}
	}
	if copycat == nil {
		t.Fatal("copycat evaluation not found")
	}
	if copycat.Phase != evalPhaseBadReveal {
		t.Fatalf("copycat phase = %q, want BadReveal (commitment is address-bound)", copycat.Phase)
	}
	if !bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Error("honest median (80,75 → 77) must still verify")
	}
}

// Reveals posted before K commitments are in must be ignored: every commit
// closes before any reveal opens.
func TestEvalMarket_RevealBeforeWindowIgnored(t *testing.T) {
	sb := testEvalBounty("early-reveal")
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "early-reveal")
	annotateBounty(t, c, ns, "early-reveal", map[string]string{
		"obol.org/eval-commit-" + evalA: monetizeapi.EvalCommitHash(90, "salt-"+evalA, evalA),
		"obol.org/eval-reveal-" + evalA: fmt.Sprintf(`{"score":90,"salt":"salt-%s"}`, evalA),
	})
	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/early-reveal")

	got := getBounty(t, c, ns, "early-reveal")
	if got.Status.RevealDeadline != nil {
		t.Fatal("reveal window must not open before k=3 commitments")
	}
	for _, ev := range got.Status.Evaluations {
		if ev.Phase != evalPhaseCommitted {
			t.Errorf("evaluation %s phase = %q, want Committed (reveal ignored before the window opens)", ev.Address, ev.Phase)
		}
	}
	if bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Fatal("no quorum yet")
	}
}

func TestEvalMarket_SelfBondReturnedOnPass(t *testing.T) {
	sb := testEvalBounty("bonded-pass")
	sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{Required: true, Amount: "10.00", Token: "OBOL"}
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "bonded-pass")
	if got := getBounty(t, c, ns, "bonded-pass"); got.Status.BondState != escrow.StateReserved {
		t.Fatalf("bond state after claim = %q, want Reserved", got.Status.BondState)
	}

	commitAndReveal(t, c, ns, "bonded-pass", map[string]int64{evalA: 90, evalB: 85, evalC: 80})
	got := getBounty(t, c, ns, "bonded-pass")
	if got.Status.BondState != "Returned" {
		t.Errorf("bond state = %q, want Returned (accepted work returns the bond)", got.Status.BondState)
	}
	if got.Status.Phase != bountyPhasePaid {
		t.Errorf("phase = %q, want Paid", got.Status.Phase)
	}
}

func TestEvalMarket_SelfBondForfeitedOnReject(t *testing.T) {
	sb := testEvalBounty("bonded-reject")
	sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{Required: true, Amount: "10.00", Token: "OBOL"}
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "bonded-reject")
	commitAndReveal(t, c, ns, "bonded-reject", map[string]int64{evalA: 10, evalB: 15, evalC: 20})

	got := getBounty(t, c, ns, "bonded-reject")
	if got.Status.BondState != "Forfeited" {
		t.Errorf("bond state = %q, want Forfeited (rejected work forfeits the bond)", got.Status.BondState)
	}
	if got.Status.Phase != bountyPhaseRejected {
		t.Errorf("phase = %q, want Rejected", got.Status.Phase)
	}
}

// Poster override on top of an active eval market: an explicit accept verdict
// wins even before the quorum settles.
func TestEvalMarket_PosterOverrideStillWins(t *testing.T) {
	sb := testEvalBounty("override")
	c := newBountyTestController(t, sb)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "override")
	annotateBounty(t, c, ns, "override", map[string]string{"obol.org/verdict": "accept"})
	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/override")

	got := getBounty(t, c, ns, "override")
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason != "PosterOverride" {
		t.Fatalf("Verified reason = %q, want PosterOverride", reason)
	}
	if got.Status.Phase != bountyPhasePaid {
		t.Errorf("phase = %q, want Paid", got.Status.Phase)
	}
}

func TestMedianInt64(t *testing.T) {
	cases := []struct {
		in   []int64
		want int64
	}{
		{[]int64{90}, 90},
		{[]int64{90, 40}, 65},
		{[]int64{90, 85, 40}, 85},
		{[]int64{1, 2, 3, 100}, 2},
	}
	for _, tc := range cases {
		if got := medianInt64(tc.in); got != tc.want {
			t.Errorf("median(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ── eval payment units: capture recipients must match the voucher seats ────

// The poster's Permit2 voucher seats are signed in ATOMIC token units
// (cmd/obol bountyEvalFundRecipients: perAtomic, probation floor(perAtomic/2));
// escrow.BuildTransferDetails matches CaptureBatch recipients against those
// seats with exact integer comparison. The controller's settle paths must
// therefore speak atomic units whenever the asset resolves in the token
// registry — a human-unit "2.00" recipient would 4xx every real capture.
func TestEvalSeatAmounts_AtomicMatchesVoucherSeatMath(t *testing.T) {
	sb := testEvalBounty("atomic-units") // Asset OBOL, PerEvaluator 2.00
	sb.Spec.Reward.Network = "base-sepolia"

	full, half, ok := evalSeatAmounts(sb)
	if !ok {
		t.Fatal("evalSeatAmounts must resolve a positive perEvaluator price")
	}
	wantFull, err := escrow.HumanToAtomic("2.00", 18) // OBOL is 18 decimals on base-sepolia
	if err != nil {
		t.Fatalf("HumanToAtomic: %v", err)
	}
	if full != wantFull || full != "2000000000000000000" {
		t.Fatalf("full seat = %q, want atomic %q", full, wantFull)
	}
	if half != "1000000000000000000" {
		t.Fatalf("probation seat = %q, want floor(perAtomic/2) = 1000000000000000000", half)
	}

	// An asset/network pair outside the token registry (OBOL is not
	// registered on base mainnet) falls back to human-unit bookkeeping
	// strings — the dev ledger gateway treats amounts as opaque, and no
	// CLI-signed voucher can exist for an unresolvable token anyway.
	sb.Spec.Reward.Network = "base"
	full, half, ok = evalSeatAmounts(sb)
	if !ok || full != "2.00" || half != "1.00" {
		t.Fatalf("unresolvable token fallback = (%q, %q, %v), want (2.00, 1.00, true)", full, half, ok)
	}

	sb.Spec.Eval.Payment.PerEvaluator = "not-a-number"
	if _, _, ok := evalSeatAmounts(sb); ok {
		t.Fatal("a non-numeric perEvaluator price must not settle")
	}
}

func TestEvalSettle_CaptureRecipientsAreAtomic(t *testing.T) {
	sb := testEvalBounty("atomic-settle")
	sb.Spec.Reward.Network = "base-sepolia" // OBOL resolves → atomic units
	c := newBountyTestController(t, sb)
	fake := newFakeEscrow()
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "atomic-settle")
	// All in band (median 85) — no escalation, straight to settle.
	commitAndReveal(t, c, ns, "atomic-settle", map[string]int64{evalA: 90, evalB: 85, evalC: 80})

	got := getBounty(t, c, ns, "atomic-settle")
	if got.Status.EvalBudgetState != escrow.StateCaptured {
		t.Fatalf("eval budget = %q, want Captured", got.Status.EvalBudgetState)
	}
	recipients := fake.batches["uid-atomic-settle-eval"]
	if len(recipients) != 3 {
		t.Fatalf("capture recipients = %d, want 3", len(recipients))
	}
	for _, r := range recipients {
		if r.Amount != "2000000000000000000" {
			t.Fatalf("recipient %s amount = %q, want atomic 2000000000000000000 (matches the CLI voucher seat)", r.Address, r.Amount)
		}
	}
}
