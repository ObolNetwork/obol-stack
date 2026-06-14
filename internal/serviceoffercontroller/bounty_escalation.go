package serviceoffercontroller

// Escalation round (design doc §11.6): when round 0 settles diverged
// (dispersion) or knife-edge on the pass threshold, the verdict is NOT spoken.
// Instead a fresh 2k+1 panel — excluding the round-0 panel and the fulfiller —
// re-runs the same commit-reveal cycle on annotation prefixes
// obol.org/eval-commit-r1-<addr> / obol.org/eval-reveal-r1-<addr>, and ITS
// median is final. One escalation per bounty (status.escalation is a latch);
// the round-1 eval budget is a separate poster-funded escrow leg
// (<uid>-eval-r1, voucher annotation obol.org/eval-voucher-r1) that pays every
// round-1 evaluator full price, win-or-lose. If the voucher never arrives
// before the escalation window closes, the escalation is Unfunded and the
// round-0 median stands — evaluators are never asked to work unpaid.

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	bountyEvalCommitR1Prefix = "obol.org/eval-commit-r1-"
	bountyEvalRevealR1Prefix = "obol.org/eval-reveal-r1-"

	// Fallbacks mirror internal/bounty registry ladder defaults for task
	// packages that cannot be resolved.
	defaultEscalationWindow  = 30 * time.Minute
	defaultEscalationEpsilon = 5
)

// selectEscalationPanelFn is the seam to the escalation panel selection
// implemented in bounty_panel.go. A variable so tests inject a deterministic
// fake panel (the selection itself is exercised by the panel tests).
var selectEscalationPanelFn = (*Controller).selectEscalationPanel

// escalationTrigger reports why round 0 must escalate ("" = settle normally):
//
//	(a) dispersion — at least ⌈k/2⌉ counting REVEALS landed out of band
//	    around the median (non-reveals are penalized, not dispersion);
//	(b) knife-edge — the median sits within epsilon of the pass threshold,
//	    where a single re-rolled evaluator could have flipped the verdict.
//	    epsilon <= 0 disables the knife-edge trigger.
func escalationTrigger(evaluations []monetizeapi.ServiceBountyEvaluation, k, median int64, epsilon int) string {
	outOfBand := int64(0)
	for _, evaluation := range evaluations {
		if evaluation.Phase == evalPhaseRevealed && evaluation.Seat != monetizeapi.PanelSeatShadow && !evaluation.WithinBand {
			outOfBand++
		}
	}
	if outOfBand >= (k+1)/2 {
		return fmt.Sprintf("dispersion: %d of %d counting reveal(s) out of band around median %d", outOfBand, k, median)
	}
	if epsilon > 0 {
		diff := median - evalPassThreshold
		if diff < 0 {
			diff = -diff
		}
		if diff <= int64(epsilon) {
			return fmt.Sprintf("knife-edge: median %d within %d of the %d pass threshold", median, epsilon, evalPassThreshold)
		}
	}
	return ""
}

// escalationWindow resolves the task package's ladder.escalationWindow — the
// time the poster has to fund the round-1 eval budget (voucher arrival).
func escalationWindow(sb *monetizeapi.ServiceBounty) time.Duration {
	t, err := bounty.Resolve(sb.Spec.Task.TypeRef)
	if err != nil {
		return defaultEscalationWindow
	}
	window, err := time.ParseDuration(t.Eval.Ladder.EscalationWindow)
	if err != nil || window <= 0 {
		return defaultEscalationWindow
	}
	return window
}

// escalationEpsilon resolves the task package's ladder.escalationEpsilon.
func escalationEpsilon(sb *monetizeapi.ServiceBounty) int {
	t, err := bounty.Resolve(sb.Spec.Task.TypeRef)
	if err != nil {
		return defaultEscalationEpsilon
	}
	return t.Eval.Ladder.EscalationEpsilon
}

// openEscalation latches the single escalation round: a 2k+1 panel excluding
// the round-0 panel, the round-0 (open-door) participants, and the fulfiller.
// opened=false, retry=true is a transient selection failure (seed source /
// enrollment list) — the verdict stays unspoken and the trigger re-checks;
// opened=false, retry=false means the enrolled pool cannot seat a round-1
// panel at all, so the round-0 median stands.
func (c *Controller) openEscalation(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus, reason string, now time.Time) (opened, retry bool) {
	size := int(2*evalQuorumK(sb) + 1)

	exclude := make(map[string]bool)
	for _, seat := range status.EvaluatorPanel {
		exclude[common.HexToAddress(seat.Address).Hex()] = true
	}
	for _, evaluation := range status.Evaluations {
		// Open-door round-0 participants are excluded too: a diverged
		// evaluator must not grade their own divergence.
		exclude[common.HexToAddress(evaluation.Address).Hex()] = true
	}
	if len(status.Claims) > 0 && common.IsHexAddress(status.Claims[0].FulfillerAddress) {
		exclude[common.HexToAddress(status.Claims[0].FulfillerAddress).Hex()] = true
	}

	// Panel selection reads the raw object shape (spec.task.typeRef, UID,
	// creation timestamp, status.claims) — feed it the WORKING status so a
	// claim promoted this reconcile is visible to the pair-diversity weights.
	working := *sb
	working.Status = *status
	rawObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&working)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "Escalated", "False", "PanelUnavailable",
			truncateMessage(fmt.Sprintf("escalation triggered (%s) but the bounty could not be encoded for panel selection: %v", reason, err)))
		return false, true
	}
	panel, err := selectEscalationPanelFn(c, ctx, &unstructured.Unstructured{Object: rawObject}, size, exclude)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "Escalated", "False", "PanelUnavailable",
			truncateMessage(fmt.Sprintf("escalation triggered (%s) but no round-1 panel is available: %v", reason, err)))
		return false, true
	}
	if len(panel) == 0 {
		// Thin enrolled pool — same fallback posture as round 0's open door,
		// except a round-1 open door would re-admit the very addresses the
		// escalation excludes, so the round-0 median stands instead.
		setPurchaseCondition(&status.Conditions, "Escalated", "False", "PanelExhausted",
			truncateMessage(fmt.Sprintf("escalation triggered (%s) but the enrolled pool cannot seat a %d-member round-1 panel — the round-0 median stands", reason, size)))
		return false, false
	}

	voucherDeadline := metav1.NewTime(now.Add(escalationWindow(sb)))
	status.Escalation = &monetizeapi.ServiceBountyEscalation{
		Round:           1,
		Reason:          reason,
		Panel:           panel,
		VoucherDeadline: &voucherDeadline,
		BudgetState:     escrowStateAwaitingVoucher,
	}
	setPurchaseCondition(&status.Conditions, "Escalated", "True", "Escalated", truncateMessage(reason))
	c.reserveEscalationBudget(ctx, sb, annotations, status)
	return true, false
}

// runEscalation drives the open escalation to a conclusion. done=true means
// the escalation is resolved: either the round-1 cycle settled (its median is
// final) or the budget went Unfunded (round-0 median stands). done=false keeps
// the verdict unspoken; requeue covers the voucher/reveal deadlines.
func (c *Controller) runEscalation(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus, now time.Time) (done bool, requeue time.Duration) {
	esc := status.Escalation

	if esc.BudgetState == "" || esc.BudgetState == escrowStateAwaitingVoucher {
		c.reserveEscalationBudget(ctx, sb, annotations, status)
	}

	switch esc.BudgetState {
	case escrowStateUnfunded:
		return true, 0
	case escrow.StateReserved, escrow.StateCaptured:
		// funded — fall through to the round-1 cycle below
	default:
		// Still awaiting the voucher: past the escalation window the round-0
		// median stands; before it, wait (annotation arrival re-reconciles).
		if esc.VoucherDeadline != nil && now.After(esc.VoucherDeadline.Time) {
			esc.BudgetState = escrowStateUnfunded
			setPurchaseCondition(&status.Conditions, "Escalated", "True", "EscalationUnfunded",
				fmt.Sprintf("Escalation eval budget was never funded before %s — the round-0 median stands", esc.VoucherDeadline.UTC().Format(time.RFC3339)))
			return true, 0
		}
		setPurchaseCondition(&status.Conditions, "Escalated", "True", "EscrowAwaitingVoucher",
			fmt.Sprintf("Escalation eval budget awaits the poster's Permit2 voucher (%s annotation)", bountyEvalVoucherR1Annotation))
		if esc.VoucherDeadline != nil {
			return false, time.Until(esc.VoucherDeadline.Time) + time.Second
		}
		return false, 0
	}

	// Funded: full commit-reveal cycle, semantics identical to round 0. All
	// 2k+1 seats are counting (no probation/shadow in round 1) and only panel
	// members are admitted.
	seats := make(map[string]string, len(esc.Panel))
	for _, seat := range esc.Panel {
		seats[common.HexToAddress(seat.Address).Hex()] = seat.Seat
	}
	settled, roundRequeue := runEvalRound(annotations, evalRoundIO{
		commitPrefix: bountyEvalCommitR1Prefix,
		revealPrefix: bountyEvalRevealR1Prefix,
		seats:        seats,
		restrict:     true,
		k:            int64(len(esc.Panel)),
		window:       revealWindow(sb),
		evaluations:  &esc.Evaluations,
		deadline:     &esc.RevealDeadline,
	}, now)
	return settled, roundRequeue
}

// reserveEscalationBudget holds the round-1 eval budget — panel size × FULL
// perEvaluator, no probation discount — under <uid>-eval-r1, attaching the
// obol.org/eval-voucher-r1 Permit2 voucher when it has ferried in. Re-runs
// while AwaitingVoucher (idempotent at the facilitator).
func (c *Controller) reserveEscalationBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus) {
	esc := status.Escalation
	if esc == nil || (esc.BudgetState != "" && esc.BudgetState != escrowStateAwaitingVoucher) {
		return
	}
	per, err := strconv.ParseFloat(strings.TrimSpace(sb.Spec.Eval.Payment.PerEvaluator), 64)
	if err != nil || per <= 0 {
		// No eval-payment leg configured → nothing to fund; the round runs
		// like a round-0 market without perEvaluator pricing (settle is a
		// no-op for the same reason).
		esc.BudgetState = escrow.StateReserved
		return
	}
	total := strconv.FormatFloat(float64(len(esc.Panel))*per, 'f', 2, 64)
	receipt, err := c.escrowGateway().Reserve(ctx, escrow.ReserveRequest{
		ID:      string(sb.UID) + "-eval-r1",
		Network: sb.Spec.Reward.Network,
		PayTo:   sb.Spec.Reward.PayTo, // poster refund address
		Asset:   sb.Spec.Eval.Payment.Asset,
		Amount:  total,
		Scheme:  sb.Spec.Reward.Escrow.Scheme,
		Voucher: voucherFromAnnotations(annotations, bountyEvalVoucherR1Annotation),
	})
	if err != nil {
		log.Printf("serviceoffer-controller: reserve escalation budget for %s/%s: %v", sb.Namespace, sb.Name, err)
		return
	}
	esc.BudgetState = receipt.State
	ferryEscrowSpender(status, receipt)
	if receipt.State == escrow.StateReserved {
		setPurchaseCondition(&status.Conditions, "Escalated", "True", "EscalationFunded",
			fmt.Sprintf("Round-1 panel of %d funded (%s); commit-reveal in progress", len(esc.Panel), total))
	}
}

// settleEscalationBudget batch-settles the round-1 eval budget to every
// round-1 evaluator with a valid reveal — full price, win-or-lose. Non/bad
// reveals earn nothing, exactly like round 0.
func (c *Controller) settleEscalationBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	esc := status.Escalation
	if esc == nil || esc.BudgetState != escrow.StateReserved {
		return
	}
	// Full price per round-1 seat, in atomic units when the asset resolves —
	// capture recipients must match the poster's voucher seats exactly
	// (see evalSeatAmounts).
	amount, _, ok := evalSeatAmounts(sb)
	if !ok {
		return
	}

	var recipients []escrow.BatchRecipient
	var paidIdx []int
	for i := range esc.Evaluations {
		if esc.Evaluations[i].Phase != evalPhaseRevealed {
			continue
		}
		recipients = append(recipients, escrow.BatchRecipient{
			Address: esc.Evaluations[i].Address,
			Amount:  amount,
		})
		paidIdx = append(paidIdx, i)
	}
	if len(recipients) == 0 {
		return // nothing to pay; refund path voids the budget
	}

	var receipt escrow.Receipt
	var err error
	if batch, ok := c.escrowGateway().(escrow.BatchGateway); ok {
		receipt, err = batch.CaptureBatch(ctx, string(sb.UID)+"-eval-r1", recipients)
	} else {
		receipt, err = c.escrowGateway().Capture(ctx, string(sb.UID)+"-eval-r1")
	}
	if err != nil {
		log.Printf("serviceoffer-controller: settle escalation budget for %s/%s: %v", sb.Namespace, sb.Name, err)
		return
	}
	esc.BudgetState = receipt.State
	ferryEscrowSpender(status, receipt)
	for _, i := range paidIdx {
		esc.Evaluations[i].Paid = true
	}
}
