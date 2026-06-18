package serviceoffercontroller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/util/workqueue"
)

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

	a := selectEvaluatorPanel("uid-1", pool, "benchmark@v1", 3, "5.00", "50.00", "0xf")
	b := selectEvaluatorPanel("uid-1", pool, "benchmark@v1", 3, "5.00", "50.00", "0xf")
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
	if seats := selectEvaluatorPanel("uid", pool, "benchmark@v1", 3, "5.00", "50.00", ""); seats != nil {
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

	under := selectEvaluatorPanel("uid", pool, "benchmark@v1", 3, "5.00", "50.00", "")
	if countProbation(under) != 1 {
		t.Errorf("reward under the cap must seat exactly one probationer, got %d (%v)", countProbation(under), under)
	}
	over := selectEvaluatorPanel("uid", pool, "benchmark@v1", 3, "500.00", "50.00", "")
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
