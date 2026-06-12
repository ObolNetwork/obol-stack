package serviceoffercontroller

// Eval-market pass — the verification-by-default slice (design doc §11).
//
// Evaluators interact through per-address annotations (the same k8s-native
// write channel as claim/submit, keyed per evaluator so concurrent writers
// never last-write-wins each other):
//
//	obol.org/eval-commit-<addr> = EvalCommitHash(score, salt, addr)
//	obol.org/eval-reveal-<addr> = {"score":N,"salt":"…"}
//
// Discipline (the research amendments, plans/evaluator-market-research-notes.md):
//   - commitments are ADDRESS-BOUND (Kleros §4.3) — copying another
//     evaluator's commit hash makes your own reveal unverifiable;
//   - no reveal is processed until K commitments are in (commit window
//     closes before any reveal opens);
//   - a missing reveal past the reveal window is graded as a worst-case
//     outlier (nonRevealPenalty) — silent abstention is never the cheap exit;
//   - quorum = MEDIAN of revealed scores (robust to one outlier, which is
//     what makes the future probation seat verdict-safe);
//   - WithinBand records divergence from the median per evaluator — the
//     per-bounty bookkeeping hook the reputation ladder will key on.
//
// Deliberately NOT here yet: evaluator selection (needs an enrollment pool),
// the OBOL eval-payment leg (signed by the poster's agent at selection time,
// batch-settled at the facilitator — never by this controller), and
// cross-bounty ladder state. The controller signs NOTHING.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	bountyEvalCommitPrefix = "obol.org/eval-commit-"
	bountyEvalRevealPrefix = "obol.org/eval-reveal-"

	evalPhaseCommitted = "Committed"
	evalPhaseRevealed  = "Revealed"
	evalPhaseBadReveal = "BadReveal"
	evalPhaseNonReveal = "NonReveal"

	// evalPassThreshold: median revealed score (0-100, ERC-8004
	// validationResponse semantics) at or above this verifies the submission.
	evalPassThreshold = 50

	// evalOutlierBand: a revealed score further than this from the median is
	// marked WithinBand=false (the divergence penalty reputation keys on).
	evalOutlierBand = 20

	// defaultRevealWindow guards against a task package with a missing or
	// unparseable ladder.revealWindow.
	defaultRevealWindow = 10 * time.Minute
)

// bountyEvalReveal is the eval-reveal annotation payload. ValidationTx is the
// optional ERC-8004 validationResponse transaction the evaluator submitted
// with their OWN wallet — recorded as provenance, never required.
type bountyEvalReveal struct {
	Score        int64  `json:"score"`
	Salt         string `json:"salt"`
	ValidationTx string `json:"validationTx,omitempty"`
}

// evalMarketActive reports whether quorum verification applies: skipped mode
// and poster-manual acceptance both leave the poster as the judge.
func evalMarketActive(sb *monetizeapi.ServiceBounty) bool {
	return sb.Spec.Eval.Mode != monetizeapi.EvalModeDangerouslySkipped &&
		sb.Spec.Acceptance.Method != "poster-manual"
}

// reconcileEvalMarket promotes commit/reveal annotations into status and, once
// the quorum settles, writes the Verified condition with reason
// EvaluatorQuorum. Returns a positive duration when the bounty should be
// requeued (reveal-window expiry).
func (c *Controller) reconcileEvalMarket(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus, now time.Time) time.Duration {
	// 0. Panel selection (once) + eval-budget reservation (once). The budget
	// is the SEPARATE OBOL leg: k × perEvaluator, poster-funded, paid to
	// evaluators win-or-lose.
	c.ensurePanel(ctx, sb, status)
	c.reserveEvalBudget(ctx, sb, status)

	// Seat lookup is by CANONICAL (EIP-55) address — enrollments may carry any
	// case, annotations another; HexToAddress.Hex() is the one true form.
	panelSeats := make(map[string]string, len(status.EvaluatorPanel))
	for _, seat := range status.EvaluatorPanel {
		panelSeats[common.HexToAddress(seat.Address).Hex()] = seat.Seat
	}

	// 1. Promote commitments (first write wins per address — a commitment is
	// binding; later annotation edits must not rewrite history). With a panel
	// selected, only panel members are admitted; shadows are admitted but
	// never counted.
	for key, value := range annotations {
		addr, ok := strings.CutPrefix(key, bountyEvalCommitPrefix)
		if !ok || !common.IsHexAddress(addr) {
			continue
		}
		canonical := common.HexToAddress(addr).Hex()
		seat := ""
		if len(panelSeats) > 0 {
			s, selected := panelSeats[canonical]
			if !selected {
				continue // not on the panel — the open door is closed
			}
			seat = s
		}
		if findEvaluation(status.Evaluations, canonical) != nil {
			continue
		}
		status.Evaluations = append(status.Evaluations, monetizeapi.ServiceBountyEvaluation{
			Address:    canonical,
			CommitHash: strings.TrimSpace(value),
			Phase:      evalPhaseCommitted,
			Seat:       seat,
		})
	}
	sort.Slice(status.Evaluations, func(i, j int) bool {
		return status.Evaluations[i].Address < status.Evaluations[j].Address
	})

	k := sb.Spec.Eval.K
	if k < 1 {
		k = 1
	}

	// 2. The commit window closes (and the reveal window opens) only when K
	// COUNTING commitments are in (shadows never gate the window). No reveal
	// is graded before that instant.
	var requeue time.Duration
	if status.RevealDeadline == nil {
		counting := int64(0)
		for _, evaluation := range status.Evaluations {
			if evaluation.Seat != monetizeapi.PanelSeatShadow {
				counting++
			}
		}
		if counting < k {
			return 0
		}
		deadline := metav1.NewTime(now.Add(revealWindow(sb)))
		status.RevealDeadline = &deadline
		requeue = time.Until(deadline.Time) + time.Second
	}

	// 3. Grade reveals against the address-bound commitment.
	for key, value := range annotations {
		addr, ok := strings.CutPrefix(key, bountyEvalRevealPrefix)
		if !ok || !common.IsHexAddress(addr) {
			continue
		}
		evaluation := findEvaluation(status.Evaluations, common.HexToAddress(addr).Hex())
		if evaluation == nil || evaluation.Phase != evalPhaseCommitted {
			continue
		}
		var reveal bountyEvalReveal
		if err := json.Unmarshal([]byte(value), &reveal); err != nil {
			evaluation.Phase = evalPhaseBadReveal
			continue
		}
		if monetizeapi.EvalCommitHash(reveal.Score, reveal.Salt, evaluation.Address) != evaluation.CommitHash {
			evaluation.Phase = evalPhaseBadReveal
			continue
		}
		revealedAt := metav1.NewTime(now)
		evaluation.Phase = evalPhaseRevealed
		evaluation.Score = reveal.Score
		evaluation.RevealedAt = &revealedAt
		evaluation.ValidationTxHash = strings.TrimSpace(reveal.ValidationTx)
	}

	// 4. Past the reveal window, missing reveals become worst-case outliers.
	deadlinePassed := now.After(status.RevealDeadline.Time)
	if deadlinePassed {
		for i := range status.Evaluations {
			if status.Evaluations[i].Phase == evalPhaseCommitted {
				status.Evaluations[i].Phase = evalPhaseNonReveal
			}
		}
	}

	// 5. Quorum settles when every commitment is graded (all revealed early)
	// or the reveal window has closed.
	settled := deadlinePassed
	if !settled {
		settled = true
		for _, evaluation := range status.Evaluations {
			if evaluation.Phase == evalPhaseCommitted {
				settled = false
				break
			}
		}
	}
	if !settled {
		return requeue
	}

	// Median over COUNTING reveals only — shadows are graded against it but
	// never move it (the free reputation on-ramp can't sway verdicts).
	var scores []int64
	for _, evaluation := range status.Evaluations {
		if evaluation.Phase == evalPhaseRevealed && evaluation.Seat != monetizeapi.PanelSeatShadow {
			scores = append(scores, evaluation.Score)
		}
	}
	if len(scores) == 0 {
		setPurchaseCondition(&status.Conditions, "Verified", "False", "EvaluatorQuorum",
			"No valid reveals — submission unverifiable; poster may override or the deadline refunds")
		return requeue
	}

	median := medianInt64(scores)
	for i := range status.Evaluations {
		evaluation := &status.Evaluations[i]
		switch evaluation.Phase {
		case evalPhaseRevealed:
			diff := evaluation.Score - median
			if diff < 0 {
				diff = -diff
			}
			evaluation.WithinBand = diff <= evalOutlierBand
		default:
			evaluation.WithinBand = false
		}
	}

	status.WeightedScore = median
	if median >= evalPassThreshold {
		setPurchaseCondition(&status.Conditions, "Verified", "True", "EvaluatorQuorum",
			fmt.Sprintf("Median score %d/100 from %d reveal(s) meets the %d threshold", median, len(scores), evalPassThreshold))
		if len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseSubmitted {
			status.Claims[0].Phase = bountyPhaseVerified
		}
	} else {
		setPurchaseCondition(&status.Conditions, "Verified", "False", "EvaluatorQuorum",
			fmt.Sprintf("Median score %d/100 from %d reveal(s) is below the %d threshold", median, len(scores), evalPassThreshold))
		if len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseSubmitted {
			status.Claims[0].Phase = bountyPhaseRejected
		}
	}

	// 6. Settlement side-effects, once per bounty: pay the evaluators
	// (win-or-lose — they did the work) and record the cross-bounty ladder.
	c.settleEvalBudget(ctx, sb, status)
	if !status.LadderRecorded {
		if err := c.recordLadder(ctx, sb, status); err != nil {
			log.Printf("serviceoffer-controller: record evaluator ladder for %s/%s: %v", sb.Namespace, sb.Name, err)
		} else {
			status.LadderRecorded = true
		}
	}
	return requeue
}

// reserveEvalBudget holds the poster-funded OBOL eval budget (k × perEvaluator,
// minus the newcomer discount when a probation seat is sitting) at the escrow
// gateway under <uid>-eval. Errors are non-fatal: evaluation proceeds and the
// reserve retries on the next reconcile.
func (c *Controller) reserveEvalBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	if status.EvalBudgetState != "" || sb.Spec.Eval.Payment.PerEvaluator == "" {
		return
	}
	total := evalBudgetTotal(sb, status)
	if total == "" {
		return
	}
	receipt, err := c.escrowGateway().Reserve(ctx, escrow.ReserveRequest{
		ID:      string(sb.UID) + "-eval",
		Network: sb.Spec.Reward.Network,
		PayTo:   sb.Spec.Reward.PayTo, // poster refund address
		Asset:   sb.Spec.Eval.Payment.Asset,
		Amount:  total,
		Scheme:  sb.Spec.Reward.Escrow.Scheme,
	})
	if err != nil {
		log.Printf("serviceoffer-controller: reserve eval budget for %s/%s: %v", sb.Namespace, sb.Name, err)
		return
	}
	status.EvalBudgetState = receipt.State
}

// settleEvalBudget batch-settles the held eval budget to every counting
// evaluator with a valid reveal (probation seats at half price — the discount
// already went to the poster at reserve time). Shadows evaluate free; non/bad
// reveals earn nothing (the monetary edge of the non-reveal penalty).
func (c *Controller) settleEvalBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	if status.EvalBudgetState != escrow.StateReserved {
		return
	}
	per, err := strconv.ParseFloat(strings.TrimSpace(sb.Spec.Eval.Payment.PerEvaluator), 64)
	if err != nil || per <= 0 {
		return
	}

	var recipients []escrow.BatchRecipient
	paid := make(map[string]bool)
	k := sb.Spec.Eval.K
	if k < 1 {
		k = 1
	}
	for i := range status.Evaluations {
		evaluation := &status.Evaluations[i]
		if evaluation.Phase != evalPhaseRevealed || evaluation.Seat == monetizeapi.PanelSeatShadow {
			continue
		}
		if int64(len(recipients)) >= k {
			break // open-door can over-subscribe; the budget pays k seats
		}
		amount := per
		if evaluation.Seat == monetizeapi.PanelSeatProbation {
			amount = per / 2
		}
		recipients = append(recipients, escrow.BatchRecipient{
			Address: evaluation.Address,
			Amount:  strconv.FormatFloat(amount, 'f', 2, 64),
		})
		paid[evaluation.Address] = true
	}
	if len(recipients) == 0 {
		return // nothing to pay; refund path voids the budget
	}

	var receipt escrow.Receipt
	if batch, ok := c.escrowGateway().(escrow.BatchGateway); ok {
		receipt, err = batch.CaptureBatch(ctx, string(sb.UID)+"-eval", recipients)
	} else {
		receipt, err = c.escrowGateway().Capture(ctx, string(sb.UID)+"-eval")
	}
	if err != nil {
		log.Printf("serviceoffer-controller: settle eval budget for %s/%s: %v", sb.Namespace, sb.Name, err)
		return
	}
	status.EvalBudgetState = receipt.State
	status.EvalPayoutTxHash = receipt.TxHash
	for i := range status.Evaluations {
		if paid[status.Evaluations[i].Address] {
			status.Evaluations[i].Paid = true
		}
	}
}

// evalBudgetTotal computes k × perEvaluator with the probation seat at half
// price (the newcomer discount is passed to the poster).
func evalBudgetTotal(sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) string {
	per, err := strconv.ParseFloat(strings.TrimSpace(sb.Spec.Eval.Payment.PerEvaluator), 64)
	if err != nil || per <= 0 {
		return ""
	}
	k := sb.Spec.Eval.K
	if k < 1 {
		k = 1
	}
	total := float64(k) * per
	for _, seat := range status.EvaluatorPanel {
		if seat.Seat == monetizeapi.PanelSeatProbation {
			total -= per / 2
			break
		}
	}
	return strconv.FormatFloat(total, 'f', 2, 64)
}

func findEvaluation(evaluations []monetizeapi.ServiceBountyEvaluation, address string) *monetizeapi.ServiceBountyEvaluation {
	for i := range evaluations {
		if evaluations[i].Address == address {
			return &evaluations[i]
		}
	}
	return nil
}

// revealWindow resolves the task package's ladder.revealWindow.
func revealWindow(sb *monetizeapi.ServiceBounty) time.Duration {
	t, err := bounty.Resolve(sb.Spec.Task.TypeRef)
	if err != nil {
		return defaultRevealWindow
	}
	window, err := time.ParseDuration(t.Eval.Ladder.RevealWindow)
	if err != nil || window <= 0 {
		return defaultRevealWindow
	}
	return window
}

// medianInt64 returns the median (lower-middle average for even counts) —
// robust to one outlier, which is what makes a newcomer seat verdict-safe.
func medianInt64(values []int64) int64 {
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
