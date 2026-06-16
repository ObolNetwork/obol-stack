package serviceoffercontroller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/util/workqueue"
)

func newBountyTestController(t *testing.T, bounties ...*monetizeapi.ServiceBounty) *Controller {
	t.Helper()

	objects := make([]runtime.Object, 0, len(bounties))
	for _, sb := range bounties {
		objects = append(objects, mustBountyObject(t, sb))
	}

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			monetizeapi.ServiceBountyGVR:       "ServiceBountyList",
			monetizeapi.EvaluatorEnrollmentGVR: "EvaluatorEnrollmentList",
		},
		objects...,
	)

	return &Controller{
		dynClient:    dynClient,
		bountyQueue:  workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		bountyEscrow: escrow.NewLedgerGateway(),
	}
}

func mustBountyObject(t *testing.T, sb *monetizeapi.ServiceBounty) *unstructured.Unstructured {
	t.Helper()

	sb.TypeMeta = metav1.TypeMeta{
		APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
		Kind:       monetizeapi.ServiceBountyKind,
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sb)
	if err != nil {
		t.Fatalf("to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func testBounty(name string) *monetizeapi.ServiceBounty {
	return &monetizeapi.ServiceBounty{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "hermes-obol-agent",
			UID:       types.UID("uid-" + name),
		},
		Spec: monetizeapi.ServiceBountySpec{
			Task: monetizeapi.ServiceBountyTask{
				TypeRef: "benchmark@v1",
				Params:  map[string]string{"dtype": "fp16"},
			},
			Acceptance: monetizeapi.ServiceBountyAcceptance{Method: "poster-manual"},
			Reward: monetizeapi.ServiceBountyReward{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Asset:   monetizeapi.ServiceOfferAsset{Symbol: "USDC"},
				Amount:  "500.00",
				Escrow:  monetizeapi.ServiceBountyEscrow{Scheme: "upto"},
			},
			MaxFulfillers: 1,
		},
	}
}

// reconcileBountyUntilSettled runs reconcile twice: the first pass may only
// add the finalizer (it returns early, the informer event re-queues in prod).
func reconcileBountyUntilSettled(t *testing.T, c *Controller, key string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if err := c.reconcileBounty(context.Background(), key); err != nil {
			t.Fatalf("reconcile %s (pass %d): %v", key, i, err)
		}
	}
}

func getBounty(t *testing.T, c *Controller, namespace, name string) *monetizeapi.ServiceBounty {
	t.Helper()

	raw, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get bounty %s/%s: %v", namespace, name, err)
	}
	var sb monetizeapi.ServiceBounty
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &sb); err != nil {
		t.Fatalf("decode bounty: %v", err)
	}
	return &sb
}

func annotateBounty(t *testing.T, c *Controller, namespace, name string, annotations map[string]string) {
	t.Helper()

	raw, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get bounty for annotate: %v", err)
	}
	existing := raw.GetAnnotations()
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range annotations {
		existing[k] = v
	}
	raw.SetAnnotations(existing)
	if _, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(namespace).Update(context.Background(), raw, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("annotate bounty: %v", err)
	}
}

func TestBountyLifecycle_OpenToPaid(t *testing.T) {
	c := newBountyTestController(t, testBounty("bench"))
	key := "hermes-obol-agent/bench"

	// Open: finalizer + task validation + escrow reserve.
	reconcileBountyUntilSettled(t, c, key)
	sb := getBounty(t, c, "hermes-obol-agent", "bench")
	if !bountyConditionIsTrue(sb.Status.Conditions, "TaskValid") {
		t.Fatalf("TaskValid not true: %+v", sb.Status.Conditions)
	}
	if !bountyConditionIsTrue(sb.Status.Conditions, "EscrowReserved") {
		t.Fatalf("EscrowReserved not true: %+v", sb.Status.Conditions)
	}
	if sb.Status.EscrowState != escrow.StateReserved {
		t.Fatalf("EscrowState = %q, want Reserved", sb.Status.EscrowState)
	}
	if sb.Status.Phase != bountyPhaseOpen {
		t.Fatalf("phase = %q, want Open", sb.Status.Phase)
	}

	// Claim.
	annotateBounty(t, c, "hermes-obol-agent", "bench", map[string]string{
		bountyClaimAnnotation:  "0x2222222222222222222222222222222222222222",
		bountyCommitAnnotation: "0xc0ffee",
	})
	reconcileBountyUntilSettled(t, c, key)
	sb = getBounty(t, c, "hermes-obol-agent", "bench")
	if sb.Status.Phase != bountyPhaseClaimed {
		t.Fatalf("phase = %q, want Claimed", sb.Status.Phase)
	}
	if len(sb.Status.Claims) != 1 || sb.Status.Claims[0].CommitHash != "0xc0ffee" {
		t.Fatalf("claims = %+v", sb.Status.Claims)
	}

	// Submit.
	annotateBounty(t, c, "hermes-obol-agent", "bench", map[string]string{
		bountySubmitAnnotation: `{"resultHash":"0xbeef","reportURI":"http://hermes.local/results/bench.a2ui.json"}`,
	})
	reconcileBountyUntilSettled(t, c, key)
	sb = getBounty(t, c, "hermes-obol-agent", "bench")
	if sb.Status.Phase != bountyPhaseSubmitted {
		t.Fatalf("phase = %q, want Submitted", sb.Status.Phase)
	}
	if sb.Status.ReportURI == "" {
		t.Fatal("ReportURI not promoted from submission")
	}

	// Poster accepts → Verified + Paid (ledger capture).
	annotateBounty(t, c, "hermes-obol-agent", "bench", map[string]string{
		bountyVerdictAnnotation: "accept",
	})
	reconcileBountyUntilSettled(t, c, key)
	sb = getBounty(t, c, "hermes-obol-agent", "bench")
	if !bountyConditionIsTrue(sb.Status.Conditions, "Verified") {
		t.Fatalf("Verified not true: %+v", sb.Status.Conditions)
	}
	if !bountyConditionIsTrue(sb.Status.Conditions, "Paid") {
		t.Fatalf("Paid not true: %+v", sb.Status.Conditions)
	}
	if sb.Status.Phase != bountyPhasePaid {
		t.Fatalf("phase = %q, want Paid", sb.Status.Phase)
	}
	if sb.Status.WeightedScore != 100 {
		t.Fatalf("weightedScore = %d, want 100", sb.Status.WeightedScore)
	}
	if !strings.HasPrefix(sb.Status.CaptureTxHash, "dev-ledger:") {
		t.Fatalf("CaptureTxHash = %q, want dev-ledger label (never mistakable for settlement)", sb.Status.CaptureTxHash)
	}
	if len(sb.Status.Claims) != 1 || sb.Status.Claims[0].Phase != bountyPhasePaid {
		t.Fatalf("claim phase = %+v, want Paid", sb.Status.Claims)
	}
}

func TestBountyLifecycle_InvalidTaskParks(t *testing.T) {
	sb := testBounty("bad")
	sb.Spec.Task.TypeRef = "does-not-exist@v9"
	c := newBountyTestController(t, sb)

	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/bad")
	got := getBounty(t, c, "hermes-obol-agent", "bad")
	if got.Status.Phase != bountyPhaseInvalid {
		t.Fatalf("phase = %q, want Invalid", got.Status.Phase)
	}
	if bountyConditionIsTrue(got.Status.Conditions, "TaskValid") {
		t.Fatal("TaskValid should be false for unknown typeRef")
	}
}

func TestBountyLifecycle_BadParamEnumParks(t *testing.T) {
	sb := testBounty("bad-param")
	sb.Spec.Task.Params = map[string]string{"dtype": "fp64"}
	c := newBountyTestController(t, sb)

	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/bad-param")
	got := getBounty(t, c, "hermes-obol-agent", "bad-param")
	if got.Status.Phase != bountyPhaseInvalid {
		t.Fatalf("phase = %q, want Invalid", got.Status.Phase)
	}
}

func TestBountyLifecycle_UnknownParamParks(t *testing.T) {
	sb := testBounty("typo-param")
	sb.Spec.Task.Params = map[string]string{"hardwreClass": "H100"}
	c := newBountyTestController(t, sb)

	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/typo-param")
	got := getBounty(t, c, "hermes-obol-agent", "typo-param")
	if got.Status.Phase != bountyPhaseInvalid {
		t.Fatalf("phase = %q, want Invalid (unknown params are typo'd intent, not extensibility)", got.Status.Phase)
	}
}

func TestBountyLifecycle_MultiFulfillerParks(t *testing.T) {
	sb := testBounty("multi")
	sb.Spec.MaxFulfillers = 3
	c := newBountyTestController(t, sb)

	reconcileBountyUntilSettled(t, c, "hermes-obol-agent/multi")
	got := getBounty(t, c, "hermes-obol-agent", "multi")
	if got.Status.Phase != bountyPhaseInvalid {
		t.Fatalf("phase = %q, want Invalid (v1 is single-winner; silently honoring >1 promises a race semantic that doesn't exist)", got.Status.Phase)
	}
}

func TestBountyLifecycle_DeadlineRefunds(t *testing.T) {
	sb := testBounty("late")
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	sb.Spec.Deadline = &past
	c := newBountyTestController(t, sb)
	key := "hermes-obol-agent/late"

	// First pass adds the finalizer; the next passes reserve then refund.
	for i := 0; i < 3; i++ {
		if err := c.reconcileBounty(context.Background(), key); err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
	}
	got := getBounty(t, c, "hermes-obol-agent", "late")
	if got.Status.Phase != bountyPhaseExpired && got.Status.Phase != bountyPhaseRefunded {
		t.Fatalf("phase = %q, want Expired or Refunded", got.Status.Phase)
	}
	if bountyConditionIsTrue(got.Status.Conditions, "Paid") {
		t.Fatal("expired bounty must not pay")
	}
}

func TestBountyLifecycle_RejectVerdict(t *testing.T) {
	c := newBountyTestController(t, testBounty("rejected"))
	key := "hermes-obol-agent/rejected"

	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, "hermes-obol-agent", "rejected", map[string]string{
		bountyClaimAnnotation:   "0x3333333333333333333333333333333333333333",
		bountySubmitAnnotation:  `{"resultHash":"0x1","reportURI":"http://x"}`,
		bountyVerdictAnnotation: "reject:scores out of tolerance",
	})
	reconcileBountyUntilSettled(t, c, key)

	got := getBounty(t, c, "hermes-obol-agent", "rejected")
	if got.Status.Phase != bountyPhaseRejected {
		t.Fatalf("phase = %q, want Rejected", got.Status.Phase)
	}
	if bountyConditionIsTrue(got.Status.Conditions, "Paid") {
		t.Fatal("rejected bounty must not pay")
	}
	if got.Status.EscrowState != escrow.StateReserved {
		t.Fatalf("EscrowState = %q; rejection keeps the hold until deadline refund or poster delete", got.Status.EscrowState)
	}
}

func TestBountyLifecycle_InvalidClaimAddress(t *testing.T) {
	c := newBountyTestController(t, testBounty("badclaim"))
	key := "hermes-obol-agent/badclaim"

	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, "hermes-obol-agent", "badclaim", map[string]string{
		bountyClaimAnnotation: "not-an-address",
	})
	reconcileBountyUntilSettled(t, c, key)

	got := getBounty(t, c, "hermes-obol-agent", "badclaim")
	if len(got.Status.Claims) != 0 {
		t.Fatalf("claims = %+v, want none for invalid address", got.Status.Claims)
	}
	if got.Status.Phase != bountyPhaseOpen {
		t.Fatalf("phase = %q, want Open", got.Status.Phase)
	}
}

// ── voucher ferry (Permit2 vouchers ride annotations into ReserveRequests) ──

func TestBountyLifecycle_RewardVoucherFerry(t *testing.T) {
	fake := newFakeEscrow()
	fake.spender = "0xFAC0000000000000000000000000000000000FAC"
	fake.requireVoucher["uid-ferry"] = true
	c := newBountyTestController(t, testBounty("ferry"))
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"
	key := ns + "/ferry"

	// No voucher yet: the hold parks in AwaitingVoucher — surfaced as a
	// condition, never a reconcile error — and the facilitator's spender is
	// ferried into status for the poster-side signer.
	reconcileBountyUntilSettled(t, c, key)
	sb := getBounty(t, c, ns, "ferry")
	if sb.Status.EscrowState != escrowStateAwaitingVoucher {
		t.Fatalf("EscrowState = %q, want AwaitingVoucher", sb.Status.EscrowState)
	}
	if reason := conditionReason(sb.Status.Conditions, "EscrowReserved"); reason != "EscrowAwaitingVoucher" {
		t.Fatalf("EscrowReserved reason = %q, want EscrowAwaitingVoucher", reason)
	}
	if sb.Status.EscrowSpender != fake.spender {
		t.Fatalf("EscrowSpender = %q, want %q ferried from the receipt", sb.Status.EscrowSpender, fake.spender)
	}

	// The signed voucher ferries in → re-reserve picks it up → Reserved.
	annotateBounty(t, c, ns, "ferry", map[string]string{
		bountyRewardVoucherAnnotation: `{"owner":"0x1111111111111111111111111111111111111111","token":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","network":"base","spender":"0xFAC0000000000000000000000000000000000FAC","nonce":"7","deadline":1893456000,"recipients":[{"address":"0x2222222222222222222222222222222222222222","amount":"500000000"}],"signature":"0xabcd"}`,
	})
	reconcileBountyUntilSettled(t, c, key)
	sb = getBounty(t, c, ns, "ferry")
	if sb.Status.EscrowState != escrow.StateReserved {
		t.Fatalf("EscrowState = %q, want Reserved after the voucher arrived", sb.Status.EscrowState)
	}
	if !bountyConditionIsTrue(sb.Status.Conditions, "EscrowReserved") {
		t.Fatal("EscrowReserved must be true once the voucher-backed hold lands")
	}
	req := fake.lastReserve(t, "uid-ferry")
	if req.Voucher == nil || req.Voucher.Nonce != "7" || len(req.Voucher.Recipients) != 1 {
		t.Fatalf("voucher not ferried intact: %+v", req.Voucher)
	}

	// Claim → submit → accept → capture: the full transition chain
	// AwaitingVoucher → Reserved → Captured.
	annotateBounty(t, c, ns, "ferry", map[string]string{
		bountyClaimAnnotation: "0x2222222222222222222222222222222222222222",
	})
	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, ns, "ferry", map[string]string{
		bountySubmitAnnotation:  `{"resultHash":"0xbeef","reportURI":"http://x"}`,
		bountyVerdictAnnotation: "accept",
	})
	reconcileBountyUntilSettled(t, c, key)
	sb = getBounty(t, c, ns, "ferry")
	if sb.Status.EscrowState != escrow.StateCaptured {
		t.Fatalf("EscrowState = %q, want Captured", sb.Status.EscrowState)
	}
	if sb.Status.Phase != bountyPhasePaid {
		t.Fatalf("phase = %q, want Paid", sb.Status.Phase)
	}
	// H2: the reward is captured to the ACCEPTED FULFILLER's explicit seat — a
	// bound (address, amount) recipient — not "all voucher seats" to whoever
	// the poster pre-signed.
	batch := fake.lastBatch(t, "uid-ferry")
	if len(batch) != 1 || !strings.EqualFold(batch[0].Address, "0x2222222222222222222222222222222222222222") {
		t.Fatalf("reward capture recipients = %+v, want a single seat bound to the fulfiller 0x2222…", batch)
	}
}

func TestBountyLifecycle_BondAndEvalVoucherFerry(t *testing.T) {
	fake := newFakeEscrow()
	fake.requireVoucher["uid-legs-bond"] = true
	fake.requireVoucher["uid-legs-eval"] = true
	sb := testEvalBounty("legs")
	sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{Required: true, Amount: "10.00", Token: "OBOL"}
	c := newBountyTestController(t, sb)
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"
	key := ns + "/legs"

	claimAndSubmit(t, c, ns, "legs")
	got := getBounty(t, c, ns, "legs")
	if got.Status.BondState != escrowStateAwaitingVoucher {
		t.Fatalf("BondState = %q, want AwaitingVoucher (parked, not an error)", got.Status.BondState)
	}
	if got.Status.EvalBudgetState != escrowStateAwaitingVoucher {
		t.Fatalf("EvalBudgetState = %q, want AwaitingVoucher", got.Status.EvalBudgetState)
	}

	annotateBounty(t, c, ns, "legs", map[string]string{
		bountyBondVoucherAnnotation: `{"owner":"0x2222222222222222222222222222222222222222","token":"0xOB","network":"base","nonce":"1","deadline":1,"signature":"0x01"}`,
		bountyEvalVoucherAnnotation: `{"owner":"0x1111111111111111111111111111111111111111","token":"0xOB","network":"base","nonce":"2","deadline":1,"signature":"0x02"}`,
	})
	reconcileBountyUntilSettled(t, c, key)
	got = getBounty(t, c, ns, "legs")
	if got.Status.BondState != escrow.StateReserved {
		t.Fatalf("BondState = %q, want Reserved after bond voucher", got.Status.BondState)
	}
	if got.Status.EvalBudgetState != escrow.StateReserved {
		t.Fatalf("EvalBudgetState = %q, want Reserved after eval voucher", got.Status.EvalBudgetState)
	}
	if fake.lastReserve(t, "uid-legs-bond").Voucher.Nonce != "1" {
		t.Fatal("bond voucher not attached to the bond reserve")
	}
	if fake.lastReserve(t, "uid-legs-eval").Voucher.Nonce != "2" {
		t.Fatal("eval voucher not attached to the eval-budget reserve")
	}
}

func TestBountyLifecycle_EscrowSpenderFerriedOnce(t *testing.T) {
	fake := newFakeEscrow()
	fake.spender = "0xFAC0000000000000000000000000000000000001"
	sb := testBounty("spender")
	sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{Required: true, Amount: "10.00", Token: "OBOL"}
	c := newBountyTestController(t, sb)
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"
	key := ns + "/spender"

	reconcileBountyUntilSettled(t, c, key)
	got := getBounty(t, c, ns, "spender")
	if got.Status.EscrowSpender != "0xFAC0000000000000000000000000000000000001" {
		t.Fatalf("EscrowSpender = %q, want first receipt's spender", got.Status.EscrowSpender)
	}

	// A later receipt reporting a different spender must NOT overwrite the
	// first — signers bind vouchers to one executor.
	fake.mu.Lock()
	fake.spender = "0xFAC0000000000000000000000000000000000002"
	fake.mu.Unlock()
	annotateBounty(t, c, ns, "spender", map[string]string{
		bountyClaimAnnotation: "0x2222222222222222222222222222222222222222",
	})
	reconcileBountyUntilSettled(t, c, key)
	got = getBounty(t, c, ns, "spender")
	if got.Status.EscrowSpender != "0xFAC0000000000000000000000000000000000001" {
		t.Fatalf("EscrowSpender = %q, want the FIRST spender preserved", got.Status.EscrowSpender)
	}
}

func TestBountyLifecycle_CaptureVoucherRefusalParksNotFails(t *testing.T) {
	fake := newFakeEscrow()
	fake.captureErr["uid-refuse"] = fmt.Errorf("escrow capture uid-refuse: facilitator returned 409: AwaitingVoucher: settlement voucher missing")
	c := newBountyTestController(t, testBounty("refuse"))
	c.bountyEscrow = fake
	ns := "hermes-obol-agent"
	key := ns + "/refuse"

	reconcileBountyUntilSettled(t, c, key)
	annotateBounty(t, c, ns, "refuse", map[string]string{
		bountyClaimAnnotation:   "0x2222222222222222222222222222222222222222",
		bountySubmitAnnotation:  `{"resultHash":"0x1","reportURI":"http://x"}`,
		bountyVerdictAnnotation: "accept",
	})
	// reconcileBountyUntilSettled fails the test on a reconcile error — a
	// voucher-refused capture must park as a condition instead.
	reconcileBountyUntilSettled(t, c, key)

	got := getBounty(t, c, ns, "refuse")
	if reason := conditionReason(got.Status.Conditions, "Paid"); reason != "EscrowAwaitingVoucher" {
		t.Fatalf("Paid reason = %q, want EscrowAwaitingVoucher", reason)
	}
	if got.Status.EscrowState != escrow.StateReserved {
		t.Fatalf("EscrowState = %q, want still Reserved", got.Status.EscrowState)
	}
	if got.Status.Phase != bountyPhaseVerified {
		t.Fatalf("phase = %q, want Verified (accepted, awaiting settlement voucher)", got.Status.Phase)
	}

	// Once the facilitator stops refusing (voucher arrived on its side), the
	// next reconcile captures.
	fake.mu.Lock()
	delete(fake.captureErr, "uid-refuse")
	fake.mu.Unlock()
	reconcileBountyUntilSettled(t, c, key)
	got = getBounty(t, c, ns, "refuse")
	if got.Status.Phase != bountyPhasePaid {
		t.Fatalf("phase = %q, want Paid after the refusal clears", got.Status.Phase)
	}
}

func TestBountyLifecycle_RefundVoidsEscalationBudget(t *testing.T) {
	fake := newFakeEscrow()
	sb := testEvalBounty("evict")
	past := metav1.NewTime(time.Now().Add(time.Hour))
	sb.Spec.Deadline = &past
	c := newBountyTestController(t, sb)
	c.bountyEscrow = fake
	stubEscalationPanel(t, r1Panel(7), nil)
	ns := "hermes-obol-agent"
	key := ns + "/evict"

	claimAndSubmit(t, c, ns, "evict")
	commitAndReveal(t, c, ns, "evict", map[string]int64{evalA: 10, evalB: 45, evalC: 100})

	got := getBounty(t, c, ns, "evict")
	if got.Status.Escalation == nil || got.Status.Escalation.BudgetState != escrow.StateReserved {
		t.Fatalf("escalation = %+v, want a funded escalation", got.Status.Escalation)
	}

	// Deadline passes with the escalation still unresolved → refund returns
	// every held leg, including the round-1 eval budget.
	expired := metav1.NewTime(time.Now().Add(-time.Minute))
	raw, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(ns).Get(context.Background(), "evict", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get bounty: %v", err)
	}
	if err := unstructured.SetNestedField(raw.Object, expired.UTC().Format(time.RFC3339), "spec", "deadline"); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(ns).Update(context.Background(), raw, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update bounty: %v", err)
	}
	reconcileBountyUntilSettled(t, c, key)

	got = getBounty(t, c, ns, "evict")
	if got.Status.Phase != bountyPhaseRefunded {
		t.Fatalf("phase = %q, want Refunded", got.Status.Phase)
	}
	if got.Status.Escalation.BudgetState != escrow.StateVoided {
		t.Fatalf("escalation budget = %q, want Voided on refund", got.Status.Escalation.BudgetState)
	}
	fake.mu.Lock()
	state := fake.states["uid-evict-eval-r1"]
	fake.mu.Unlock()
	if state != escrow.StateVoided {
		t.Fatalf("facilitator state for eval-r1 = %q, want Voided", state)
	}
}
