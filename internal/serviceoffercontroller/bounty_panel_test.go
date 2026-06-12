package serviceoffercontroller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/util/workqueue"
)

// testPanelLadder mirrors the benchmark@v1 ladder shape used across panel
// tests: probation cap 50.00, default decay knobs.
var testPanelLadder = bounty.Ladder{
	ShadowAgreements:  5,
	ProbationEvals:    10,
	ProbationValueCap: "50.00",
	DecayHalfLife:     "720h",
}

func seedOf(uid string) [32]byte { return sha256.Sum256([]byte(uid)) }

func testEnrollment(t *testing.T, name, address, tier string) *unstructured.Unstructured {
	t.Helper()
	enrollment := monetizeapi.EvaluatorEnrollment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
			Kind:       monetizeapi.EvaluatorEnrollmentKind,
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hermes-obol-agent"},
		Spec: monetizeapi.EvaluatorEnrollmentSpec{
			Address:   address,
			TaskTypes: []string{"benchmark@v1"},
		},
	}
	if tier != "" {
		enrollment.Status.Records = []monetizeapi.EvaluatorLadderRecord{{TaskType: "benchmark@v1", Tier: tier}}
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&enrollment)
	if err != nil {
		t.Fatalf("enrollment to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func newPanelTestController(t *testing.T, sb *monetizeapi.ServiceBounty, enrollments ...*unstructured.Unstructured) *Controller {
	t.Helper()
	objects := []runtime.Object{mustBountyObject(t, sb)}
	for _, e := range enrollments {
		objects = append(objects, e)
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

func TestSelectEvaluatorPanel_DeterministicPerBounty(t *testing.T) {
	pool := []monetizeapi.EvaluatorEnrollment{}
	for i := 0; i < 6; i++ {
		addr := fmt.Sprintf("0x%040d", i)
		pool = append(pool, monetizeapi.EvaluatorEnrollment{
			Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: addr, TaskTypes: []string{"benchmark@v1"}},
			Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{
				{TaskType: "benchmark@v1", Tier: monetizeapi.EvaluatorTierFull},
			}},
		})
	}

	now := time.Now()
	a := selectEvaluatorPanel(seedOf("uid-1"), pool, "benchmark@v1", 3, "5.00", testPanelLadder, "0xf", now)
	b := selectEvaluatorPanel(seedOf("uid-1"), pool, "benchmark@v1", 3, "5.00", testPanelLadder, "0xf", now)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("selection must be deterministic per bounty UID:\n%v\n%v", a, b)
	}
	if len(a) != 3 {
		t.Fatalf("got %d seats, want 3", len(a))
	}
}

func TestSelectEvaluatorPanel_OpenDoorWhenPoolThin(t *testing.T) {
	pool := []monetizeapi.EvaluatorEnrollment{
		{
			Spec:   monetizeapi.EvaluatorEnrollmentSpec{Address: "0x" + strings.Repeat("1", 40), TaskTypes: []string{"benchmark@v1"}},
			Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{{TaskType: "benchmark@v1", Tier: monetizeapi.EvaluatorTierFull}}},
		},
		// Shadows are not counting candidates.
		{Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: "0x" + strings.Repeat("2", 40), TaskTypes: []string{"benchmark@v1"}}},
	}
	if seats := selectEvaluatorPanel(seedOf("uid"), pool, "benchmark@v1", 3, "5.00", testPanelLadder, "", time.Now()); seats != nil {
		t.Fatalf("thin pool must fall back to open-door, got %v", seats)
	}
}

func TestSelectEvaluatorPanel_ProbationSeatValueCapped(t *testing.T) {
	pool := []monetizeapi.EvaluatorEnrollment{}
	for i := 0; i < 4; i++ {
		pool = append(pool, monetizeapi.EvaluatorEnrollment{
			Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: fmt.Sprintf("0x%040d", i), TaskTypes: []string{"benchmark@v1"}},
			Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{
				{TaskType: "benchmark@v1", Tier: monetizeapi.EvaluatorTierFull},
			}},
		})
	}
	pool = append(pool, monetizeapi.EvaluatorEnrollment{
		Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: "0x" + strings.Repeat("9", 40), TaskTypes: []string{"benchmark@v1"}},
		Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{
			{TaskType: "benchmark@v1", Tier: monetizeapi.EvaluatorTierProbation},
		}},
	})

	countProbation := func(seats []monetizeapi.ServiceBountyPanelSeat) int {
		n := 0
		for _, s := range seats {
			if s.Seat == monetizeapi.PanelSeatProbation {
				n++
			}
		}
		return n
	}

	under := selectEvaluatorPanel(seedOf("uid"), pool, "benchmark@v1", 3, "5.00", testPanelLadder, "", time.Now())
	if countProbation(under) != 1 {
		t.Errorf("reward under the cap must seat exactly one probationer, got %d (%v)", countProbation(under), under)
	}
	over := selectEvaluatorPanel(seedOf("uid"), pool, "benchmark@v1", 3, "500.00", testPanelLadder, "", time.Now())
	if countProbation(over) != 0 {
		t.Errorf("reward above the cap must seat no probationer, got %d (%v)", countProbation(over), over)
	}
}

// Full panel-mode lifecycle: panel gates out a non-panel commit, the shadow is
// graded but not counted, evaluators get paid, the ladder records.
func TestEvalMarket_PanelMode(t *testing.T) {
	sb := testEvalBounty("panel")
	sb.Spec.Trust.SelfBond = monetizeapi.ServiceBountySelfBond{}
	pool := []*unstructured.Unstructured{
		testEnrollment(t, "ev-a", evalA, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-b", evalB, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-c", evalC, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-shadow", "0xdddddddddddddddddddddddddddddddddddddddd", ""),
	}
	c := newPanelTestController(t, sb, pool...)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "panel")

	got := getBounty(t, c, ns, "panel")
	if len(got.Status.EvaluatorPanel) != 4 {
		t.Fatalf("panel = %v, want 3 counting + 1 shadow", got.Status.EvaluatorPanel)
	}
	seatOf := map[string]string{}
	for _, seat := range got.Status.EvaluatorPanel {
		seatOf[strings.ToLower(seat.Address)] = seat.Seat
	}
	if seatOf["0xdddddddddddddddddddddddddddddddddddddddd"] != monetizeapi.PanelSeatShadow {
		t.Fatalf("the Shadow-tier enrollee must hold the shadow seat: %v", seatOf)
	}
	if got.Status.EvalBudgetState != escrow.StateReserved {
		t.Fatalf("eval budget state = %q, want Reserved at panel selection", got.Status.EvalBudgetState)
	}

	// A non-panel outsider tries to commit — must be ignored.
	outsider := "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	annotateBounty(t, c, ns, "panel", map[string]string{
		"obol.org/eval-commit-" + outsider: monetizeapi.EvalCommitHash(99, "x", outsider),
	})

	// Panel members (incl. the shadow) commit and reveal.
	scores := map[string]int64{evalA: 90, evalB: 85, evalC: 80, "0xdddddddddddddddddddddddddddddddddddddddd": 10}
	for addr, score := range scores {
		annotateBounty(t, c, ns, "panel", map[string]string{
			"obol.org/eval-commit-" + addr: monetizeapi.EvalCommitHash(score, "salt-"+addr, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, ns+"/panel")
	for addr, score := range scores {
		annotateBounty(t, c, ns, "panel", map[string]string{
			"obol.org/eval-reveal-" + addr: fmt.Sprintf(`{"score":%d,"salt":"salt-%s"}`, score, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, ns+"/panel")

	got = getBounty(t, c, ns, "panel")
	for _, ev := range got.Status.Evaluations {
		if strings.EqualFold(ev.Address, outsider) {
			t.Error("non-panel commit must be ignored in panel mode")
		}
	}
	if got.Status.WeightedScore != 85 {
		t.Errorf("WeightedScore = %d, want 85 (shadow's 10 must not move the median)", got.Status.WeightedScore)
	}
	if got.Status.Phase != bountyPhasePaid {
		t.Fatalf("phase = %q, want Paid", got.Status.Phase)
	}
	if got.Status.EvalBudgetState != escrow.StateCaptured || got.Status.EvalPayoutTxHash == "" {
		t.Errorf("eval budget = %q payout=%q, want Captured with a batch receipt", got.Status.EvalBudgetState, got.Status.EvalPayoutTxHash)
	}
	for _, ev := range got.Status.Evaluations {
		isShadow := ev.Seat == monetizeapi.PanelSeatShadow
		if ev.Paid == isShadow {
			t.Errorf("evaluator %s (seat=%s) paid=%v — counting seats are paid, shadows are free", ev.Address, ev.Seat, ev.Paid)
		}
	}
	if !got.Status.LadderRecorded {
		t.Fatal("ladder bookkeeping must latch after settle")
	}

	// Ladder: the shadow diverged (10 vs median 85, out of band) → no
	// agreement; counting members completed in band.
	shadowRecord := ladderStatusOf(t, c, ns, "ev-shadow")
	if shadowRecord.ShadowAgreements != 0 || shadowRecord.Completed != 1 || shadowRecord.Divergences != 1 {
		t.Errorf("shadow record = %+v, want completed=1 divergences=1 agreements=0", shadowRecord)
	}
	fullRecord := ladderStatusOf(t, c, ns, "ev-a")
	if fullRecord.Completed != 1 || fullRecord.Divergences != 0 {
		t.Errorf("full record = %+v, want completed=1 divergences=0", fullRecord)
	}
	if len(fullRecord.RecentFulfillers) == 0 {
		t.Error("pair-diversity history must record the fulfiller")
	}
}

// A shadow agreeing with the median climbs toward Probation.
func TestEvalMarket_ShadowAgreementClimbs(t *testing.T) {
	sb := testEvalBounty("shadow-climb")
	pool := []*unstructured.Unstructured{
		testEnrollment(t, "ev-a", evalA, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-b", evalB, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-c", evalC, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-shadow", "0xdddddddddddddddddddddddddddddddddddddddd", ""),
	}
	c := newPanelTestController(t, sb, pool...)
	ns := "hermes-obol-agent"

	claimAndSubmit(t, c, ns, "shadow-climb")
	scores := map[string]int64{evalA: 90, evalB: 85, evalC: 80, "0xdddddddddddddddddddddddddddddddddddddddd": 88}
	for addr, score := range scores {
		annotateBounty(t, c, ns, "shadow-climb", map[string]string{
			"obol.org/eval-commit-" + addr: monetizeapi.EvalCommitHash(score, "salt-"+addr, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, ns+"/shadow-climb")
	for addr, score := range scores {
		annotateBounty(t, c, ns, "shadow-climb", map[string]string{
			"obol.org/eval-reveal-" + addr: fmt.Sprintf(`{"score":%d,"salt":"salt-%s"}`, score, addr),
		})
	}
	reconcileBountyUntilSettled(t, c, ns+"/shadow-climb")

	record := ladderStatusOf(t, c, ns, "ev-shadow")
	if record.ShadowAgreements != 1 {
		t.Errorf("shadow within band must earn an agreement, got %+v", record)
	}
	if record.Tier != monetizeapi.EvaluatorTierShadow {
		t.Errorf("one agreement must not yet promote (threshold 5), got tier %s", record.Tier)
	}
}

// The probation seat is half price and the discount goes to the POSTER: the
// reserved budget shrinks by per/2 when a probationer is seated.
func TestEvalBudgetTotal_ProbationDiscount(t *testing.T) {
	sb := testEvalBounty("x")
	sb.Spec.Eval.Payment.PerEvaluator = "2.00"
	sb.Spec.Eval.K = 3

	status := &monetizeapi.ServiceBountyStatus{}
	if got := evalBudgetTotal(sb, status); got != "6.00" {
		t.Errorf("all-full budget = %q, want 6.00", got)
	}

	status.EvaluatorPanel = []monetizeapi.ServiceBountyPanelSeat{
		{Address: evalA, Seat: monetizeapi.PanelSeatFull},
		{Address: evalB, Seat: monetizeapi.PanelSeatFull},
		{Address: evalC, Seat: monetizeapi.PanelSeatProbation},
	}
	if got := evalBudgetTotal(sb, status); got != "5.00" {
		t.Errorf("probation-seated budget = %q, want 5.00 (2+2+1)", got)
	}
}

func TestLedgerGateway_CaptureBatch(t *testing.T) {
	g := escrow.NewLedgerGateway()
	if _, err := g.Reserve(context.Background(), escrow.ReserveRequest{ID: "b-eval", Asset: "OBOL", Amount: "6.00"}); err != nil {
		t.Fatal(err)
	}
	receipt, err := g.CaptureBatch(context.Background(), "b-eval", []escrow.BatchRecipient{
		{Address: evalA, Amount: "2.00"}, {Address: evalB, Amount: "2.00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != escrow.StateCaptured || !strings.Contains(receipt.TxHash, "batch[2]") {
		t.Errorf("receipt = %+v, want Captured dev-ledger batch[2]", receipt)
	}
}

func ladderStatusOf(t *testing.T, c *Controller, namespace, name string) monetizeapi.EvaluatorLadderRecord {
	t.Helper()
	raw, err := c.dynClient.Resource(monetizeapi.EvaluatorEnrollmentGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get enrollment %s: %v", name, err)
	}
	var enrollment monetizeapi.EvaluatorEnrollment
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &enrollment); err != nil {
		t.Fatalf("decode enrollment: %v", err)
	}
	if len(enrollment.Status.Records) == 0 {
		return monetizeapi.EvaluatorLadderRecord{}
	}
	return enrollment.Status.Records[0]
}

// ── decay-aware weighting ───────────────────────────────────────────────────

func TestLadderWeight_DecayAfterHalfLifeIdle(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	halfLife := 720 * time.Hour
	fresh := monetizeapi.EvaluatorLadderRecord{
		Completed:  10,
		LastEvalAt: &metav1.Time{Time: now},
	}
	if got := ladderWeight(fresh, "", halfLife, now); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("fresh weight = %v, want 2.0 (1 + 0.1×10)", got)
	}
	stale := monetizeapi.EvaluatorLadderRecord{
		Completed:  10,
		LastEvalAt: &metav1.Time{Time: now.Add(-halfLife)},
	}
	if got := ladderWeight(stale, "", halfLife, now); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("one-half-life-idle weight = %v, want 1.5 (effective completed halves to 5)", got)
	}
	legacy := monetizeapi.EvaluatorLadderRecord{Completed: 10} // nil LastEvalAt → no decay
	if got := ladderWeight(legacy, "", halfLife, now); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("legacy record weight = %v, want undecayed 2.0", got)
	}
}

func TestLadderWeight_GroundedBonus(t *testing.T) {
	now := time.Now()
	halfLife := 720 * time.Hour
	allGrounded := monetizeapi.EvaluatorLadderRecord{
		Completed:     4,
		GroundedEvals: 4,
		LastEvalAt:    &metav1.Time{Time: now},
	}
	if got := ladderWeight(allGrounded, "", halfLife, now); math.Abs(got-2.8) > 1e-9 {
		t.Fatalf("fully grounded weight = %v, want 2.8 (1.4 × 2)", got)
	}
	halfGrounded := monetizeapi.EvaluatorLadderRecord{
		Completed:     4,
		GroundedEvals: 2,
		LastEvalAt:    &metav1.Time{Time: now},
	}
	if got := ladderWeight(halfGrounded, "", halfLife, now); math.Abs(got-2.1) > 1e-9 {
		t.Fatalf("half grounded weight = %v, want 2.1 (1.4 × 1.5)", got)
	}
	// The bonus is capped at ×2 even if counters drift (grounded > completed).
	overGrounded := monetizeapi.EvaluatorLadderRecord{
		Completed:     1,
		GroundedEvals: 5,
		LastEvalAt:    &metav1.Time{Time: now},
	}
	if got := ladderWeight(overGrounded, "", halfLife, now); math.Abs(got-2.2) > 1e-9 {
		t.Fatalf("over-grounded weight = %v, want capped 2.2 (1.1 × 2)", got)
	}
}

// A stored Full whose reputation decayed below the probation threshold reads
// as Probation at selection time: it takes the reserved probation seat, never
// a full one.
func TestSelectEvaluatorPanel_StaleFullReadsAsProbation(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	staleAddr := "0x" + strings.Repeat("a", 40)
	pool := []monetizeapi.EvaluatorEnrollment{}
	for i := 0; i < 3; i++ {
		pool = append(pool, monetizeapi.EvaluatorEnrollment{
			Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: fmt.Sprintf("0x%040d", i), TaskTypes: []string{"benchmark@v1"}},
			Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{
				{TaskType: "benchmark@v1", Tier: monetizeapi.EvaluatorTierFull, LastEvalAt: &metav1.Time{Time: now}},
			}},
		})
	}
	pool = append(pool, monetizeapi.EvaluatorEnrollment{
		Spec: monetizeapi.EvaluatorEnrollmentSpec{Address: staleAddr, TaskTypes: []string{"benchmark@v1"}},
		Status: monetizeapi.EvaluatorEnrollmentStatus{Records: []monetizeapi.EvaluatorLadderRecord{
			{
				TaskType:   "benchmark@v1",
				Tier:       monetizeapi.EvaluatorTierFull,
				Completed:  10, // effective ≈ 0.01 after 10 half-lives — under ProbationEvals 10
				LastEvalAt: &metav1.Time{Time: now.Add(-10 * 720 * time.Hour)},
			},
		}},
	})

	seats := selectEvaluatorPanel(seedOf("uid"), pool, "benchmark@v1", 3, "5.00", testPanelLadder, "", now)
	if seats == nil {
		t.Fatal("3 fresh Full + 1 demoted probationer must still fill k=3")
	}
	for _, seat := range seats {
		if strings.EqualFold(seat.Address, staleAddr) && seat.Seat != monetizeapi.PanelSeatProbation {
			t.Fatalf("stale Full must hold the probation seat, got %s", seat.Seat)
		}
		if !strings.EqualFold(seat.Address, staleAddr) && seat.Seat == monetizeapi.PanelSeatProbation {
			t.Fatalf("fresh Full %s must not hold the probation seat", seat.Address)
		}
	}
}

// ── escalation panel ────────────────────────────────────────────────────────

func testEscalationBounty(name string) *monetizeapi.ServiceBounty {
	return &monetizeapi.ServiceBounty{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
			Kind:       monetizeapi.ServiceBountyKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "hermes-obol-agent",
			UID:               "esc-uid-1",
			CreationTimestamp: metav1.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Spec: monetizeapi.ServiceBountySpec{
			Task:   monetizeapi.ServiceBountyTask{TypeRef: "benchmark@v1"},
			Reward: monetizeapi.ServiceBountyReward{Amount: "5.00"},
			Eval:   monetizeapi.ServiceBountyEval{K: 3},
		},
	}
}

func TestSelectEscalationPanel_DeterministicAndExcludesRound0(t *testing.T) {
	sb := testEscalationBounty("esc")
	var enrollments []*unstructured.Unstructured
	var addrs []string
	for i := 0; i < 8; i++ {
		addr := fmt.Sprintf("0x%040d", i)
		addrs = append(addrs, addr)
		enrollments = append(enrollments, testEnrollment(t, fmt.Sprintf("ev-%d", i), addr, monetizeapi.EvaluatorTierFull))
	}
	c := newPanelTestController(t, sb, enrollments...)
	sbObj := mustBountyObject(t, sb)

	// Round-0 panel members are excluded by canonical EIP-55 address.
	exclude := map[string]bool{
		canonicalAddress(addrs[0]): true,
		canonicalAddress(addrs[1]): true,
	}

	first, err := c.selectEscalationPanel(context.Background(), sbObj, 5, exclude)
	if err != nil {
		t.Fatalf("selectEscalationPanel: %v", err)
	}
	second, err := c.selectEscalationPanel(context.Background(), sbObj, 5, exclude)
	if err != nil {
		t.Fatalf("selectEscalationPanel (second draw): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("escalation panel must be deterministic:\n%v\n%v", first, second)
	}
	if len(first) != 5 {
		t.Fatalf("got %d escalation seats, want 5", len(first))
	}
	for _, seat := range first {
		if seat.Seat != monetizeapi.PanelSeatFull {
			t.Errorf("escalation seat %s = %q, want all counting/full-pay", seat.Address, seat.Seat)
		}
		if exclude[canonicalAddress(seat.Address)] {
			t.Errorf("round-0 evaluator %s must be excluded from the escalation panel", seat.Address)
		}
	}
}

func TestSelectEscalationPanel_OpenDoorWhenPoolThin(t *testing.T) {
	sb := testEscalationBounty("esc-thin")
	enrollments := []*unstructured.Unstructured{
		testEnrollment(t, "ev-0", "0x"+strings.Repeat("1", 40), monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-1", "0x"+strings.Repeat("2", 40), monetizeapi.EvaluatorTierFull),
	}
	c := newPanelTestController(t, sb, enrollments...)
	sbObj := mustBountyObject(t, sb)
	exclude := map[string]bool{canonicalAddress("0x" + strings.Repeat("1", 40)): true}

	seats, err := c.selectEscalationPanel(context.Background(), sbObj, 5, exclude)
	if err != nil {
		t.Fatalf("selectEscalationPanel: %v", err)
	}
	if seats != nil {
		t.Fatalf("thin escalation pool must fall back to open-door (nil seats), got %v", seats)
	}
}

// ── seed provenance in ensurePanel ──────────────────────────────────────────

func TestEnsurePanel_PersistsLocalSeedProvenance(t *testing.T) {
	sb := testEscalationBounty("seeded")
	c := newPanelTestController(t, sb,
		testEnrollment(t, "ev-a", evalA, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-b", evalB, monetizeapi.EvaluatorTierFull),
		testEnrollment(t, "ev-c", evalC, monetizeapi.EvaluatorTierFull),
	)
	status := &monetizeapi.ServiceBountyStatus{}
	c.ensurePanel(context.Background(), sb, status)

	if status.PanelSeed == nil || status.PanelSeed.Source != "local" {
		t.Fatalf("status.panelSeed = %+v, want Source=local", status.PanelSeed)
	}
	if status.PanelSeed.Round != 0 || status.PanelSeed.Randomness != "" || status.PanelSeed.Signature != "" {
		t.Fatalf("local provenance must carry no beacon fields, got %+v", status.PanelSeed)
	}
	if len(status.EvaluatorPanel) == 0 {
		t.Fatal("panel must be selected from the enrolled Full pool")
	}
}

func TestEnsurePanel_SeedErrorDoesNotLatch(t *testing.T) {
	sb := testEscalationBounty("seed-err")
	c := newPanelTestController(t, sb,
		testEnrollment(t, "ev-a", evalA, monetizeapi.EvaluatorTierFull),
	)
	failing := &failingSeedSource{}
	c.seeds = failing

	status := &monetizeapi.ServiceBountyStatus{}
	c.ensurePanel(context.Background(), sb, status)

	if status.PanelSeed != nil || status.EvaluatorPanel != nil {
		t.Fatalf("seed failure must leave the panel unselected, got seed=%+v panel=%v", status.PanelSeed, status.EvaluatorPanel)
	}
	for _, condition := range status.Conditions {
		if condition.Type == "PanelSelected" {
			t.Fatal("seed failure must NOT latch PanelSelected — the next reconcile retries the beacon")
		}
	}

	// Not latched: the next reconcile consults the seed source again.
	c.ensurePanel(context.Background(), sb, status)
	if failing.calls != 2 {
		t.Fatalf("seed source consulted %d times, want 2 (retry, no latch)", failing.calls)
	}
}
