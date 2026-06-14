package serviceoffercontroller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// fakeEscrowGateway is a voucher-aware escrow fake: ids listed in
// requireVoucher answer AwaitingVoucher until a Reserve carries a voucher,
// captures can be forced to fail, and every request/batch is recorded.
type fakeEscrowGateway struct {
	mu             sync.Mutex
	spender        string
	requireVoucher map[string]bool
	captureErr     map[string]error
	reserves       map[string][]escrow.ReserveRequest
	states         map[string]string
	batches        map[string][]escrow.BatchRecipient
}

func newFakeEscrow() *fakeEscrowGateway {
	return &fakeEscrowGateway{
		requireVoucher: map[string]bool{},
		captureErr:     map[string]error{},
		reserves:       map[string][]escrow.ReserveRequest{},
		states:         map[string]string{},
		batches:        map[string][]escrow.BatchRecipient{},
	}
}

func (f *fakeEscrowGateway) Reserve(_ context.Context, req escrow.ReserveRequest) (escrow.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserves[req.ID] = append(f.reserves[req.ID], req)
	if state := f.states[req.ID]; state == escrow.StateCaptured || state == escrow.StateVoided {
		return escrow.Receipt{State: state, Spender: f.spender}, nil
	}
	if f.requireVoucher[req.ID] && req.Voucher == nil {
		f.states[req.ID] = escrowStateAwaitingVoucher
		return escrow.Receipt{State: escrowStateAwaitingVoucher, Spender: f.spender}, nil
	}
	f.states[req.ID] = escrow.StateReserved
	return escrow.Receipt{State: escrow.StateReserved, Spender: f.spender}, nil
}

func (f *fakeEscrowGateway) capture(id string) (escrow.Receipt, error) {
	if err := f.captureErr[id]; err != nil {
		return escrow.Receipt{}, err
	}
	f.states[id] = escrow.StateCaptured
	return escrow.Receipt{State: escrow.StateCaptured, TxHash: "fake-capture:" + id, Spender: f.spender}, nil
}

func (f *fakeEscrowGateway) Capture(_ context.Context, id string) (escrow.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capture(id)
}

func (f *fakeEscrowGateway) CaptureBatch(_ context.Context, id string, recipients []escrow.BatchRecipient) (escrow.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	receipt, err := f.capture(id)
	if err != nil {
		return escrow.Receipt{}, err
	}
	f.batches[id] = recipients
	return receipt, nil
}

func (f *fakeEscrowGateway) Void(_ context.Context, id string) (escrow.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id] = escrow.StateVoided
	return escrow.Receipt{State: escrow.StateVoided, Spender: f.spender}, nil
}

func (f *fakeEscrowGateway) lastReserve(t *testing.T, id string) escrow.ReserveRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	reqs := f.reserves[id]
	if len(reqs) == 0 {
		t.Fatalf("no Reserve recorded for %s", id)
	}
	return reqs[len(reqs)-1]
}

// fakeValidationReader is the grounding chain fake.
type fakeValidationReader struct {
	statuses map[common.Hash]erc8004.ValidationStatus
	readErr  error
}

func (f *fakeValidationReader) ValidationStatus(_ context.Context, h common.Hash) (erc8004.ValidationStatus, error) {
	if f.readErr != nil {
		return erc8004.ValidationStatus{}, f.readErr
	}
	return f.statuses[h], nil
}

func stubValidationReader(t *testing.T, reader bountyValidationReader, dialErr error) {
	t.Helper()
	orig := bountyValidationReaderFactory
	bountyValidationReaderFactory = func(context.Context, string, string) (bountyValidationReader, func(), error) {
		if dialErr != nil {
			return nil, nil, dialErr
		}
		return reader, func() {}, nil
	}
	t.Cleanup(func() { bountyValidationReaderFactory = orig })
}

func stubEscalationPanel(t *testing.T, panel []monetizeapi.ServiceBountyPanelSeat, err error) {
	t.Helper()
	orig := selectEscalationPanelFn
	selectEscalationPanelFn = func(*Controller, context.Context, *unstructured.Unstructured, int, map[string]bool) ([]monetizeapi.ServiceBountyPanelSeat, error) {
		return panel, err
	}
	t.Cleanup(func() { selectEscalationPanelFn = orig })
}

// ── helpers ─────────────────────────────────────────────────────────────────

func r1Addr(i int) string {
	return common.HexToAddress(fmt.Sprintf("0x%040x", 0xe100+i)).Hex()
}

func r1Panel(size int) []monetizeapi.ServiceBountyPanelSeat {
	seats := make([]monetizeapi.ServiceBountyPanelSeat, 0, size)
	for i := 0; i < size; i++ {
		seats = append(seats, monetizeapi.ServiceBountyPanelSeat{Address: r1Addr(i), Seat: monetizeapi.PanelSeatFull})
	}
	return seats
}

// addRound0 writes commit+reveal annotation pairs for a direct
// reconcileEvalMarket invocation (commits promote and reveals grade in the
// same pass once K commitments are present).
func addRound0(annotations map[string]string, scores map[string]int64) {
	for addr, score := range scores {
		annotations[bountyEvalCommitPrefix+addr] = monetizeapi.EvalCommitHash(score, "salt-"+addr, addr)
		annotations[bountyEvalRevealPrefix+addr] = fmt.Sprintf(`{"score":%d,"salt":"salt-%s"}`, score, addr)
	}
}

func addRound1Commits(annotations map[string]string, scores map[string]int64) {
	for addr, score := range scores {
		annotations[bountyEvalCommitR1Prefix+addr] = monetizeapi.EvalCommitHash(score, "r1salt-"+addr, addr)
	}
}

func addRound1Reveals(annotations map[string]string, scores map[string]int64) {
	for addr, score := range scores {
		annotations[bountyEvalRevealR1Prefix+addr] = fmt.Sprintf(`{"score":%d,"salt":"r1salt-%s"}`, score, addr)
	}
}

func commitAndRevealR1(t *testing.T, c *Controller, ns, name string, scores map[string]int64) {
	t.Helper()
	key := ns + "/" + name
	for addr, score := range scores {
		annotateBounty(t, c, ns, name, map[string]string{
			bountyEvalCommitR1Prefix + addr: monetizeapi.EvalCommitHash(score, "r1salt-"+addr, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, key)
	for addr, score := range scores {
		annotateBounty(t, c, ns, name, map[string]string{
			bountyEvalRevealR1Prefix + addr: fmt.Sprintf(`{"score":%d,"salt":"r1salt-%s"}`, score, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, key)
}

func bountyConditionMessage(conditions []monetizeapi.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Message
		}
	}
	return ""
}

// ── trigger (pure) ──────────────────────────────────────────────────────────

func revealedEval(addr string, score int64, withinBand bool) monetizeapi.ServiceBountyEvaluation {
	return monetizeapi.ServiceBountyEvaluation{Address: addr, Phase: evalPhaseRevealed, Score: score, WithinBand: withinBand}
}

func TestEscalationTrigger_Dispersion(t *testing.T) {
	// ceil(3/2)=2 out-of-band reveals trigger; 1 does not.
	one := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 85, true), revealedEval(evalB, 90, true), revealedEval(evalC, 20, false),
	}
	if got := escalationTrigger(one, 3, 85, 5); got != "" {
		t.Fatalf("1 of 3 out of band must not trigger, got %q", got)
	}
	two := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 0, false), revealedEval(evalB, 75, true), revealedEval(evalC, 100, false),
	}
	got := escalationTrigger(two, 3, 75, 5)
	if !strings.Contains(got, "dispersion") {
		t.Fatalf("2 of 3 out of band must trigger dispersion, got %q", got)
	}

	// Non-reveals are penalized, not dispersion: they never count.
	nonReveals := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 85, true),
		{Address: evalB, Phase: evalPhaseNonReveal, WithinBand: false},
		{Address: evalC, Phase: evalPhaseNonReveal, WithinBand: false},
	}
	if got := escalationTrigger(nonReveals, 3, 85, 5); got != "" {
		t.Fatalf("non-reveals must not count toward dispersion, got %q", got)
	}

	// Shadow seats never count either.
	shadow := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 85, true),
		{Address: evalB, Phase: evalPhaseRevealed, Score: 0, WithinBand: false, Seat: monetizeapi.PanelSeatShadow},
		{Address: evalC, Phase: evalPhaseRevealed, Score: 100, WithinBand: false, Seat: monetizeapi.PanelSeatShadow},
	}
	if got := escalationTrigger(shadow, 3, 85, 5); got != "" {
		t.Fatalf("shadow divergence must not trigger dispersion, got %q", got)
	}
}

func TestEscalationTrigger_KnifeEdge(t *testing.T) {
	inBand := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 52, true), revealedEval(evalB, 53, true), revealedEval(evalC, 54, true),
	}
	if got := escalationTrigger(inBand, 3, 53, 5); !strings.Contains(got, "knife-edge") {
		t.Fatalf("median 53 within 5 of 50 must trigger knife-edge, got %q", got)
	}
	if got := escalationTrigger(inBand, 3, 56, 5); got != "" {
		t.Fatalf("median 56 is outside epsilon 5, got %q", got)
	}
	// |median-threshold| == epsilon is inclusive.
	if got := escalationTrigger(inBand, 3, 45, 5); !strings.Contains(got, "knife-edge") {
		t.Fatalf("median 45 at exactly epsilon 5 must trigger, got %q", got)
	}
}

func TestEscalationTrigger_EpsilonZeroDisablesKnifeEdge(t *testing.T) {
	dead := []monetizeapi.ServiceBountyEvaluation{
		revealedEval(evalA, 50, true), revealedEval(evalB, 50, true), revealedEval(evalC, 50, true),
	}
	if got := escalationTrigger(dead, 3, 50, 0); got != "" {
		t.Fatalf("epsilon 0 must disable the knife-edge trigger, got %q", got)
	}
	if got := escalationTrigger(dead, 3, 50, 5); got == "" {
		t.Fatal("epsilon 5 with a dead-center median must trigger")
	}
}

// ── escalation lifecycle (e2e through reconcileBounty) ─────────────────────

func TestEscalation_DispersionTriggersAndRound1MedianIsFinal(t *testing.T) {
	sb := testEvalBounty("escalate")
	c := newBountyTestController(t, sb)
	fake := newFakeEscrow()
	c.bountyEscrow = fake
	stubEscalationPanel(t, r1Panel(7), nil)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "escalate")
	// Round 0: median 75 (would PASS), but 0 and 100 are out of band →
	// dispersion (2 ≥ ⌈3/2⌉).
	commitAndReveal(t, c, ns, "escalate", map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	got := getBounty(t, c, ns, "escalate")
	esc := got.Status.Escalation
	if esc == nil {
		t.Fatal("escalation must open on dispersion")
	}
	if esc.Round != 1 || !strings.Contains(esc.Reason, "dispersion") {
		t.Fatalf("escalation = round %d reason %q, want round 1 dispersion", esc.Round, esc.Reason)
	}
	if len(esc.Panel) != 7 {
		t.Fatalf("round-1 panel size = %d, want 2k+1 = 7", len(esc.Panel))
	}
	if esc.BudgetState != escrow.StateReserved {
		t.Fatalf("escalation budget = %q, want Reserved (fake funds without voucher)", esc.BudgetState)
	}
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason == "EvaluatorQuorum" {
		t.Fatal("the EvaluatorQuorum verdict must NOT be spoken while the escalation is open")
	}
	// 7 seats × full 2.00 — no probation discount in round 1.
	if req := fake.lastReserve(t, "uid-escalate-eval-r1"); req.Amount != "14.00" {
		t.Fatalf("round-1 reserve amount = %q, want 14.00", req.Amount)
	}

	// Round 1: median 30 → the ROUND-0 pass is overridden; round-1 is final.
	r1Scores := map[string]int64{}
	for i, score := range []int64{10, 20, 30, 30, 30, 90, 95} {
		r1Scores[r1Addr(i)] = score
	}
	commitAndRevealR1(t, c, ns, "escalate", r1Scores)

	got = getBounty(t, c, ns, "escalate")
	if bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Fatal("round-1 median 30 < 50 must reject even though round-0 median was 75")
	}
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason != "EvaluatorQuorum" {
		t.Fatalf("Verified reason = %q, want EvaluatorQuorum (escalation keeps the quorum reason)", reason)
	}
	if msg := bountyConditionMessage(got.Status.Conditions, "Verified"); !strings.Contains(msg, "escalated") {
		t.Fatalf("Verified message must note the escalation, got %q", msg)
	}
	if got.Status.WeightedScore != 30 {
		t.Fatalf("WeightedScore = %d, want round-1 median 30", got.Status.WeightedScore)
	}
	if got.Status.Phase != bountyPhaseRejected {
		t.Fatalf("phase = %q, want Rejected", got.Status.Phase)
	}
	if got.Status.Escalation.BudgetState != escrow.StateCaptured {
		t.Fatalf("escalation budget = %q, want Captured (evaluators paid win-or-lose)", got.Status.Escalation.BudgetState)
	}
	recipients := fake.batches["uid-escalate-eval-r1"]
	if len(recipients) != 7 {
		t.Fatalf("round-1 batch recipients = %d, want 7", len(recipients))
	}
	for _, recipient := range recipients {
		if recipient.Amount != "2.00" {
			t.Fatalf("round-1 evaluator %s paid %q, want full 2.00", recipient.Address, recipient.Amount)
		}
	}
	for _, evaluation := range got.Status.Escalation.Evaluations {
		if !evaluation.Paid {
			t.Fatalf("round-1 evaluator %s not marked Paid", evaluation.Address)
		}
	}
}

func TestEscalation_KnifeEdgeTriggers(t *testing.T) {
	sb := testEvalBounty("knife")
	c := newBountyTestController(t, sb)
	c.bountyEscrow = newFakeEscrow()
	stubEscalationPanel(t, r1Panel(7), nil)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "knife")
	// Median 53 — all in band (no dispersion) but within epsilon 5 of 50.
	commitAndReveal(t, c, ns, "knife", map[string]int64{evalA: 52, evalB: 53, evalC: 54})

	got := getBounty(t, c, ns, "knife")
	if got.Status.Escalation == nil {
		t.Fatal("knife-edge median must escalate")
	}
	if !strings.Contains(got.Status.Escalation.Reason, "knife-edge") {
		t.Fatalf("escalation reason = %q, want knife-edge", got.Status.Escalation.Reason)
	}
	if reason := conditionReason(got.Status.Conditions, "Verified"); reason == "EvaluatorQuorum" {
		t.Fatal("verdict must wait for the escalation round")
	}
}

func TestEscalation_SingleRoundLatch(t *testing.T) {
	sb := testEvalBounty("latch")
	c := newBountyTestController(t, sb)
	c.bountyEscrow = newFakeEscrow()
	stubEscalationPanel(t, r1Panel(7), nil)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "latch")
	commitAndReveal(t, c, ns, "latch", map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	// Round 1 lands knife-edge AND dispersed — conditions that would trigger
	// again — but escalation is a single-round latch: its median is FINAL.
	r1Scores := map[string]int64{}
	for i, score := range []int64{0, 10, 50, 50, 52, 90, 100} {
		r1Scores[r1Addr(i)] = score
	}
	commitAndRevealR1(t, c, ns, "latch", r1Scores)

	got := getBounty(t, c, ns, "latch")
	if got.Status.Escalation == nil || got.Status.Escalation.Round != 1 {
		t.Fatalf("escalation = %+v, want the single round 1", got.Status.Escalation)
	}
	if !bountyConditionIsTrue(got.Status.Conditions, "Verified") {
		t.Fatal("round-1 median 50 >= 50 must verify")
	}
	if got.Status.WeightedScore != 50 {
		t.Fatalf("WeightedScore = %d, want round-1 median 50", got.Status.WeightedScore)
	}
	if len(got.Status.Escalation.Evaluations) != 7 {
		t.Fatalf("round-1 evaluations = %d, want 7", len(got.Status.Escalation.Evaluations))
	}

	// Extra reconciles never re-open a second round or move the verdict.
	reconcileBountyUntilSettled(t, c, ns+"/latch")
	again := getBounty(t, c, ns, "latch")
	if again.Status.Escalation.Round != 1 || len(again.Status.Escalation.Evaluations) != 7 {
		t.Fatalf("escalation re-opened: %+v", again.Status.Escalation)
	}
	if again.Status.WeightedScore != 50 {
		t.Fatalf("verdict moved after latch: WeightedScore = %d", again.Status.WeightedScore)
	}
}

func TestEscalation_ExcludesRound0PanelAndFulfiller(t *testing.T) {
	sb := testEvalBounty("exclude")
	c := newBountyTestController(t, sb)
	c.bountyEscrow = newFakeEscrow()

	var gotSize int
	var gotExclude map[string]bool
	orig := selectEscalationPanelFn
	selectEscalationPanelFn = func(_ *Controller, _ context.Context, _ *unstructured.Unstructured, size int, exclude map[string]bool) ([]monetizeapi.ServiceBountyPanelSeat, error) {
		gotSize = size
		gotExclude = exclude
		return r1Panel(7), nil
	}
	t.Cleanup(func() { selectEscalationPanelFn = orig })

	ns := "hermes-obol-agent"
	claimAndSubmit(t, c, ns, "exclude")
	commitAndReveal(t, c, ns, "exclude", map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	if gotSize != 7 {
		t.Fatalf("escalation panel size = %d, want 2k+1 = 7", gotSize)
	}
	for _, addr := range []string{evalA, evalB, evalC, "0x2222222222222222222222222222222222222222"} {
		if !gotExclude[common.HexToAddress(addr).Hex()] {
			t.Errorf("exclude set must contain %s (round-0 participant or fulfiller)", addr)
		}
	}
}

// ── escalation funding (direct invocation for clock control) ───────────────

func TestEscalation_UnfundedFallbackPreservesRound0Verdict(t *testing.T) {
	c := newBountyTestController(t)
	fake := newFakeEscrow()
	fake.requireVoucher["uid-unfunded-eval-r1"] = true
	c.bountyEscrow = fake
	stubEscalationPanel(t, r1Panel(7), nil)

	sb := testEvalBounty("unfunded")
	status := &monetizeapi.ServiceBountyStatus{}
	annotations := map[string]string{}
	addRound0(annotations, map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	now0 := time.Now()
	requeue := c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0)
	if status.Escalation == nil || status.Escalation.BudgetState != escrowStateAwaitingVoucher {
		t.Fatalf("escalation = %+v, want AwaitingVoucher", status.Escalation)
	}
	if reason := conditionReason(status.Conditions, "Verified"); reason != "" {
		t.Fatalf("no verdict may be spoken while the escalation awaits funding, got reason %q", reason)
	}
	if reason := conditionReason(status.Conditions, "Escalated"); reason != "EscrowAwaitingVoucher" {
		t.Fatalf("Escalated reason = %q, want EscrowAwaitingVoucher", reason)
	}
	if requeue <= 0 {
		t.Fatal("an awaiting-voucher escalation must requeue for its deadline")
	}

	// Past the escalation window (benchmark@v1 ladder: 30m) with no voucher:
	// Unfunded, and the round-0 median (75 → pass) stands.
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0.Add(31*time.Minute))
	if status.Escalation.BudgetState != escrowStateUnfunded {
		t.Fatalf("escalation budget = %q, want Unfunded", status.Escalation.BudgetState)
	}
	if reason := conditionReason(status.Conditions, "Escalated"); reason != "EscalationUnfunded" {
		t.Fatalf("Escalated reason = %q, want EscalationUnfunded", reason)
	}
	if !bountyConditionIsTrue(status.Conditions, "Verified") {
		t.Fatal("round-0 median 75 must verify when the escalation goes unfunded")
	}
	if reason := conditionReason(status.Conditions, "Verified"); reason != "EvaluatorQuorum" {
		t.Fatalf("Verified reason = %q, want EvaluatorQuorum", reason)
	}
	if status.WeightedScore != 75 {
		t.Fatalf("WeightedScore = %d, want round-0 median 75", status.WeightedScore)
	}
	if msg := bountyConditionMessage(status.Conditions, "Verified"); strings.Contains(msg, "escalated") {
		t.Fatalf("an unfunded escalation must not claim a round-1 verdict: %q", msg)
	}
	if len(status.Escalation.Evaluations) != 0 {
		t.Fatal("an unfunded escalation must never run a round-1 cycle")
	}
}

func TestEscalation_LateVoucherFundsRound1(t *testing.T) {
	c := newBountyTestController(t)
	fake := newFakeEscrow()
	fake.requireVoucher["uid-late-eval-r1"] = true
	c.bountyEscrow = fake
	stubEscalationPanel(t, r1Panel(7), nil)

	sb := testEvalBounty("late")
	status := &monetizeapi.ServiceBountyStatus{}
	annotations := map[string]string{}
	addRound0(annotations, map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	now0 := time.Now()
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0)
	if status.Escalation.BudgetState != escrowStateAwaitingVoucher {
		t.Fatalf("budget = %q, want AwaitingVoucher", status.Escalation.BudgetState)
	}

	// The voucher annotation ferries in before the deadline → RE-reserve
	// picks it up and the budget funds.
	annotations[bountyEvalVoucherR1Annotation] = `{"owner":"0x1111111111111111111111111111111111111111","token":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","network":"base","spender":"0xFAC0000000000000000000000000000000000FAC","nonce":"42","deadline":1893456000,"recipients":[{"address":"` + r1Addr(0) + `","amount":"2000000"}],"signature":"0xsig"}`
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0.Add(5*time.Minute))
	if status.Escalation.BudgetState != escrow.StateReserved {
		t.Fatalf("budget = %q, want Reserved after the voucher arrives", status.Escalation.BudgetState)
	}
	req := fake.lastReserve(t, "uid-late-eval-r1")
	if req.Voucher == nil {
		t.Fatal("re-reserve must attach the ferried voucher")
	}
	if req.Voucher.Nonce != "42" || req.Voucher.Owner != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("voucher fields not ferried intact: %+v", req.Voucher)
	}
	if reason := conditionReason(status.Conditions, "Escalated"); reason != "EscalationFunded" {
		t.Fatalf("Escalated reason = %q, want EscalationFunded", reason)
	}
}

func TestEscalation_Round1NonRevealPenalty(t *testing.T) {
	c := newBountyTestController(t)
	fake := newFakeEscrow()
	c.bountyEscrow = fake
	stubEscalationPanel(t, r1Panel(7), nil)

	sb := testEvalBounty("r1silent")
	status := &monetizeapi.ServiceBountyStatus{}
	annotations := map[string]string{}
	addRound0(annotations, map[string]int64{evalA: 0, evalB: 75, evalC: 100})

	now0 := time.Now()
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0)
	if status.Escalation == nil || status.Escalation.BudgetState != escrow.StateReserved {
		t.Fatalf("escalation = %+v, want funded", status.Escalation)
	}

	// All 7 commit; the reveal window opens.
	r1Scores := map[string]int64{}
	for i := 0; i < 7; i++ {
		r1Scores[r1Addr(i)] = 80
	}
	addRound1Commits(annotations, r1Scores)
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0.Add(time.Minute))
	if status.Escalation.RevealDeadline == nil {
		t.Fatal("round-1 reveal window must open once all 2k+1 commitments are in")
	}

	// Only 6 reveal. Before the deadline the round is not settled.
	silent := r1Addr(6)
	revealed := map[string]int64{}
	for addr, score := range r1Scores {
		if addr != silent {
			revealed[addr] = score
		}
	}
	addRound1Reveals(annotations, revealed)
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0.Add(2*time.Minute))
	if reason := conditionReason(status.Conditions, "Verified"); reason == "EvaluatorQuorum" {
		t.Fatal("round 1 must not settle while a commitment is unrevealed inside the window")
	}

	// Past the round-1 reveal window: the silent seat grades NonReveal —
	// worst-case outlier, unpaid — and the median settles over the 6 reveals.
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, now0.Add(20*time.Minute))
	if !bountyConditionIsTrue(status.Conditions, "Verified") {
		t.Fatal("round-1 median 80 must verify")
	}
	if status.WeightedScore != 80 {
		t.Fatalf("WeightedScore = %d, want 80", status.WeightedScore)
	}
	var silentEval *monetizeapi.ServiceBountyEvaluation
	for i := range status.Escalation.Evaluations {
		if status.Escalation.Evaluations[i].Address == silent {
			silentEval = &status.Escalation.Evaluations[i]
		}
	}
	if silentEval == nil {
		t.Fatal("silent round-1 evaluator missing from escalation evaluations")
	}
	if silentEval.Phase != evalPhaseNonReveal {
		t.Fatalf("silent evaluator phase = %q, want NonReveal", silentEval.Phase)
	}
	if silentEval.WithinBand {
		t.Fatal("a round-1 non-reveal must grade as a worst-case outlier")
	}
	if silentEval.Paid {
		t.Fatal("a round-1 non-reveal must not be paid")
	}
	if len(fake.batches["uid-r1silent-eval-r1"]) != 6 {
		t.Fatalf("round-1 batch = %d recipients, want 6 (non-reveal earns nothing)", len(fake.batches["uid-r1silent-eval-r1"]))
	}
}

// ── grounding ───────────────────────────────────────────────────────────────

func TestGrounding_Matrix(t *testing.T) {
	const score = int64(90)
	canonical := common.HexToAddress(evalA)

	cases := []struct {
		name       string
		statuses   map[common.Hash]erc8004.ValidationStatus
		dialErr    error
		readErr    error
		grounded   bool
		wantReason string
		wantInMsg  string
	}{
		{
			name: "match grounds",
			statuses: map[common.Hash]erc8004.ValidationStatus{
				erc8004.BountyEvalRequestHash("uid-ground", canonical.Hex()): {ValidatorAddress: canonical, Response: 90},
			},
			grounded:   true,
			wantReason: "Grounded",
		},
		{
			name: "wrong responder stays ungrounded",
			statuses: map[common.Hash]erc8004.ValidationStatus{
				erc8004.BountyEvalRequestHash("uid-ground", canonical.Hex()): {ValidatorAddress: common.HexToAddress(evalB), Response: 90},
			},
			wantReason: "NotGrounded",
			wantInMsg:  "not the evaluator",
		},
		{
			name: "wrong score stays ungrounded",
			statuses: map[common.Hash]erc8004.ValidationStatus{
				erc8004.BountyEvalRequestHash("uid-ground", canonical.Hex()): {ValidatorAddress: canonical, Response: 10},
			},
			wantReason: "NotGrounded",
			wantInMsg:  "on-chain response 10",
		},
		{
			name:       "no on-chain entry stays ungrounded",
			statuses:   map[common.Hash]erc8004.ValidationStatus{},
			wantReason: "NotGrounded",
			wantInMsg:  "no on-chain validation entry",
		},
		{
			name:       "chain down stays ungrounded",
			dialErr:    errors.New("erpc unreachable"),
			wantReason: "ChainUnreachable",
			wantInMsg:  "unreachable",
		},
		{
			name:       "chain read error stays ungrounded",
			statuses:   nil,
			readErr:    errors.New("rpc timeout"),
			wantReason: "NotGrounded",
			wantInMsg:  "chain read failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubValidationReader(t, &fakeValidationReader{statuses: tc.statuses, readErr: tc.readErr}, tc.dialErr)

			c := newBountyTestController(t)
			c.bountyEscrow = newFakeEscrow()
			sb := testEvalBounty("ground")
			sb.Spec.Eval.K = 1
			status := &monetizeapi.ServiceBountyStatus{}
			annotations := map[string]string{
				bountyEvalCommitPrefix + evalA: monetizeapi.EvalCommitHash(score, "salt-g", evalA),
				bountyEvalRevealPrefix + evalA: fmt.Sprintf(`{"score":%d,"salt":"salt-g","validationTx":"0xfeed"}`, score),
			}
			c.reconcileEvalMarket(context.Background(), sb, annotations, status, time.Now())

			if len(status.Evaluations) != 1 {
				t.Fatalf("evaluations = %d, want 1", len(status.Evaluations))
			}
			if status.Evaluations[0].Grounded != tc.grounded {
				t.Fatalf("Grounded = %v, want %v", status.Evaluations[0].Grounded, tc.grounded)
			}
			if reason := conditionReason(status.Conditions, "EvalGrounded"); reason != tc.wantReason {
				t.Fatalf("EvalGrounded reason = %q, want %q", reason, tc.wantReason)
			}
			if tc.wantInMsg != "" {
				if msg := bountyConditionMessage(status.Conditions, "EvalGrounded"); !strings.Contains(msg, tc.wantInMsg) {
					t.Fatalf("EvalGrounded message %q must contain %q", msg, tc.wantInMsg)
				}
			}
			// Grounding NEVER blocks or changes the verdict: median 90 passes
			// in every case.
			if !bountyConditionIsTrue(status.Conditions, "Verified") {
				t.Fatal("verdict must not depend on grounding")
			}
			if status.WeightedScore != score {
				t.Fatalf("WeightedScore = %d, want %d", status.WeightedScore, score)
			}
		})
	}
}

func TestGrounding_NoValidationTxDialsNothing(t *testing.T) {
	// A reveal without validationTx must never dial the chain: the factory
	// stub fails the test if invoked.
	orig := bountyValidationReaderFactory
	bountyValidationReaderFactory = func(context.Context, string, string) (bountyValidationReader, func(), error) {
		t.Fatal("grounding must not dial the chain when no reveal carries validationTx")
		return nil, nil, nil
	}
	t.Cleanup(func() { bountyValidationReaderFactory = orig })

	c := newBountyTestController(t)
	c.bountyEscrow = newFakeEscrow()
	sb := testEvalBounty("nodial")
	sb.Spec.Eval.K = 1
	status := &monetizeapi.ServiceBountyStatus{}
	annotations := map[string]string{
		bountyEvalCommitPrefix + evalA: monetizeapi.EvalCommitHash(90, "s", evalA),
		bountyEvalRevealPrefix + evalA: `{"score":90,"salt":"s"}`,
	}
	c.reconcileEvalMarket(context.Background(), sb, annotations, status, time.Now())
	if reason := conditionReason(status.Conditions, "EvalGrounded"); reason != "" {
		t.Fatalf("EvalGrounded condition must not exist without validationTx claims, got %q", reason)
	}
}

// ── escrow config provenance ────────────────────────────────────────────────

// TestBountyEscrowGateway_ConfigFromEnvOnly re-asserts the seam invariant:
// the escrow endpoint + bearer token come ONLY from controller env. Nothing
// in a bounty's spec or annotations selects or redirects the gateway.
func TestBountyEscrowGateway_ConfigFromEnvOnly(t *testing.T) {
	t.Setenv("OBOL_BOUNTY_ESCROW_URL", "https://facilitator.internal.example")
	t.Setenv("OBOL_BOUNTY_ESCROW_TOKEN", "release-authority-token")
	gateway := newBountyEscrowGateway()
	httpGateway, ok := gateway.(*escrow.HTTPGateway)
	if !ok {
		t.Fatalf("gateway = %T, want *escrow.HTTPGateway when env is set", gateway)
	}
	if httpGateway.Base != "https://facilitator.internal.example" || httpGateway.Token != "release-authority-token" {
		t.Fatalf("gateway config = %q/%q, want env values", httpGateway.Base, httpGateway.Token)
	}

	t.Setenv("OBOL_BOUNTY_ESCROW_URL", "")
	if _, ok := newBountyEscrowGateway().(*escrow.LedgerGateway); !ok {
		t.Fatal("no env URL must fall back to the dev ledger")
	}
}

func TestBountyEscrow_AnnotationsCannotRedirectGateway(t *testing.T) {
	fake := newFakeEscrow()
	sb := testBounty("hostile")
	c := newBountyTestController(t, sb)
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"

	// Hostile annotations trying to smuggle endpoint/credential config (and a
	// voucher whose unknown fields are ignored by the typed decode).
	annotateBounty(t, c, ns, "hostile", map[string]string{
		"obol.org/escrow-url":         "http://attacker.example",
		"obol.org/escrow-token":       "stolen",
		"obol.org/escrow-facilitator": "http://attacker.example",
		bountyRewardVoucherAnnotation: `{"owner":"0x1111111111111111111111111111111111111111","base":"http://attacker.example","token":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","nonce":"1","deadline":1,"signature":"0x00"}`,
	})
	reconcileBountyUntilSettled(t, c, ns+"/hostile")

	// The injected gateway received the reserve — the annotations selected
	// nothing. The voucher decoded only its typed Permit2 fields.
	req := fake.lastReserve(t, "uid-hostile")
	if req.Voucher == nil || req.Voucher.Owner != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("voucher not ferried: %+v", req.Voucher)
	}
	got := getBounty(t, c, ns, "hostile")
	if got.Status.EscrowState != escrow.StateReserved {
		t.Fatalf("EscrowState = %q, want Reserved via the env-configured gateway", got.Status.EscrowState)
	}
}
