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
//     per-bounty bookkeeping hook the reputation ladder keys on;
//   - a diverged or knife-edge round 0 escalates ONCE to a fresh 2k+1 panel
//     whose median is final (bounty_escalation.go);
//   - reveals carrying a validationTx are grounded against the on-chain
//     ERC-8004 entry before ladder bookkeeping (bounty_grounding.go).
//
// Money legs ferried here: Permit2 vouchers ride in on annotations
// (obol.org/{reward,bond,eval}-voucher[-r1]) and are attached to the matching
// escrow ReserveRequest. The controller still signs NOTHING — a voucher is a
// poster-signed authorization the facilitator executes; the annotation channel
// can never carry escrow endpoint or credential config (that comes ONLY from
// controller env, see newBountyEscrowGateway).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402"
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

// Voucher ferry: annotations carrying a JSON-encoded escrow.Permit2Voucher,
// signed by the poster's agent and attached to the matching ReserveRequest.
const (
	bountyRewardVoucherAnnotation = "obol.org/reward-voucher"
	bountyBondVoucherAnnotation   = "obol.org/bond-voucher"
	bountyEvalVoucherAnnotation   = "obol.org/eval-voucher"
	bountyEvalVoucherR1Annotation = "obol.org/eval-voucher-r1"

	// escrowStateAwaitingVoucher is the facilitator's "request verified,
	// waiting for the signed Permit2 voucher" reservation state. Reserves in
	// this state re-run on later reconciles (idempotent at the facilitator)
	// until the voucher annotation ferries in.
	escrowStateAwaitingVoucher = "AwaitingVoucher"

	// escrowStateUnfunded parks an escalation whose voucher never arrived
	// before the escalation window closed — the round-0 median stands.
	escrowStateUnfunded = "Unfunded"
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

// evalRoundIO bundles one commit-reveal round: the annotation prefixes it
// reads and the status fields it mutates. Round 0 points at the top-level
// status fields; round 1 points into status.escalation. Same engine, same
// semantics (address-bound commits, K-gated reveal window, non-reveal =
// worst-case outlier).
type evalRoundIO struct {
	commitPrefix string
	revealPrefix string
	// seats maps canonical (EIP-55) address → seat kind. With restrict=true
	// only seated addresses are admitted (panel mode); otherwise the door is
	// open (round-0 fallback when the enrolled pool is too small).
	seats    map[string]string
	restrict bool
	// k counting commitments close the commit window and open the reveal
	// window.
	k           int64
	window      time.Duration
	evaluations *[]monetizeapi.ServiceBountyEvaluation
	deadline    **metav1.Time
}

// runEvalRound drives one commit-reveal round over the annotation channel.
// It reports whether the round settled (every commitment graded, or the
// reveal window closed) and a positive requeue duration when the reveal
// window was just opened.
func runEvalRound(annotations map[string]string, round evalRoundIO, now time.Time) (settled bool, requeue time.Duration) {
	// 1. Promote commitments (first write wins per address — a commitment is
	// binding; later annotation edits must not rewrite history).
	for key, value := range annotations {
		addr, ok := strings.CutPrefix(key, round.commitPrefix)
		if !ok || !common.IsHexAddress(addr) {
			continue
		}
		canonical := common.HexToAddress(addr).Hex()
		seat := ""
		if round.restrict {
			s, selected := round.seats[canonical]
			if !selected {
				continue // not on the panel — the open door is closed
			}
			seat = s
		}
		if findEvaluation(*round.evaluations, canonical) != nil {
			continue
		}
		*round.evaluations = append(*round.evaluations, monetizeapi.ServiceBountyEvaluation{
			Address:    canonical,
			CommitHash: strings.TrimSpace(value),
			Phase:      evalPhaseCommitted,
			Seat:       seat,
		})
	}
	evaluations := *round.evaluations
	sort.Slice(evaluations, func(i, j int) bool {
		return evaluations[i].Address < evaluations[j].Address
	})

	// 2. The commit window closes (and the reveal window opens) only when K
	// COUNTING commitments are in (shadows never gate the window). No reveal
	// is graded before that instant.
	if *round.deadline == nil {
		counting := int64(0)
		for _, evaluation := range evaluations {
			if evaluation.Seat != monetizeapi.PanelSeatShadow {
				counting++
			}
		}
		if counting < round.k {
			return false, 0
		}
		deadline := metav1.NewTime(now.Add(round.window))
		*round.deadline = &deadline
		requeue = time.Until(deadline.Time) + time.Second
	}

	// 3. Grade reveals against the address-bound commitment.
	for key, value := range annotations {
		addr, ok := strings.CutPrefix(key, round.revealPrefix)
		if !ok || !common.IsHexAddress(addr) {
			continue
		}
		evaluation := findEvaluation(evaluations, common.HexToAddress(addr).Hex())
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
	deadlinePassed := now.After((*round.deadline).Time)
	if deadlinePassed {
		for i := range evaluations {
			if evaluations[i].Phase == evalPhaseCommitted {
				evaluations[i].Phase = evalPhaseNonReveal
			}
		}
	}

	// 5. The round settles when every commitment is graded (all revealed
	// early) or the reveal window has closed.
	settled = deadlinePassed
	if !settled {
		settled = true
		for _, evaluation := range evaluations {
			if evaluation.Phase == evalPhaseCommitted {
				settled = false
				break
			}
		}
	}
	return settled, requeue
}

// reconcileEvalMarket promotes commit/reveal annotations into status and, once
// the quorum settles (running at most one escalation round first), writes the
// Verified condition with reason EvaluatorQuorum. Returns a positive duration
// when the bounty should be requeued (reveal-window or escalation-window
// expiry).
func (c *Controller) reconcileEvalMarket(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus, now time.Time) time.Duration {
	// 0. Panel selection (once) + eval-budget reservation. The budget is the
	// SEPARATE OBOL leg: k × perEvaluator, poster-funded, paid to evaluators
	// win-or-lose.
	c.ensurePanel(ctx, sb, status)
	c.reserveEvalBudget(ctx, sb, annotations, status)

	// Seat lookup is by CANONICAL (EIP-55) address — enrollments may carry any
	// case, annotations another; HexToAddress.Hex() is the one true form.
	panelSeats := make(map[string]string, len(status.EvaluatorPanel))
	for _, seat := range status.EvaluatorPanel {
		panelSeats[common.HexToAddress(seat.Address).Hex()] = seat.Seat
	}

	k := evalQuorumK(sb)
	settled, requeue := runEvalRound(annotations, evalRoundIO{
		commitPrefix: bountyEvalCommitPrefix,
		revealPrefix: bountyEvalRevealPrefix,
		seats:        panelSeats,
		restrict:     len(panelSeats) > 0,
		k:            k,
		window:       revealWindow(sb),
		evaluations:  &status.Evaluations,
		deadline:     &status.RevealDeadline,
	}, now)
	if !settled {
		return requeue
	}

	// Median over COUNTING reveals only — shadows are graded against it but
	// never move it (the free reputation on-ramp can't sway verdicts).
	scores := countingScores(status.Evaluations)
	if len(scores) == 0 {
		setPurchaseCondition(&status.Conditions, "Verified", "False", "EvaluatorQuorum",
			"No valid reveals — submission unverifiable; poster may override or the deadline refunds")
		return requeue
	}

	median := medianInt64(scores)
	markOutlierBands(status.Evaluations, median)

	// Escalation trigger — checked after every counting reveal is graded and
	// BEFORE the EvaluatorQuorum verdict is spoken. Single-round latch:
	// status.escalation, once set, is never re-opened; a spoken
	// EvaluatorQuorum verdict latches the thin-pool fallthrough so a pool
	// that grows later can never re-open a settled bounty.
	quorumAlreadySpoke := conditionReason(status.Conditions, "Verified") == "EvaluatorQuorum"
	if sb.Spec.Eval.Mode == monetizeapi.EvalModeRequired && status.Escalation == nil && !quorumAlreadySpoke {
		if reason := escalationTrigger(status.Evaluations, k, median, escalationEpsilon(sb)); reason != "" {
			if opened, retry := c.openEscalation(ctx, sb, annotations, status, reason, now); !opened && retry {
				// Transient selection failure — verdict not spoken. The
				// deadline requeue may be 0 here (reveal deadline already
				// passed), so schedule the retry explicitly or a deadline-less
				// bounty would wait for an external event.
				return maxDuration(requeue, seedRetryDelay)
			}
		}
	}

	finalMedian := median
	finalReveals := len(scores)
	escalated := false
	if esc := status.Escalation; esc != nil {
		done, escRequeue := c.runEscalation(ctx, sb, annotations, status, now)
		if !done {
			return maxDuration(requeue, escRequeue)
		}
		if r1Scores := countingScores(esc.Evaluations); len(r1Scores) > 0 {
			// The round-1 median over the 2k+1 panel is FINAL.
			finalMedian = medianInt64(r1Scores)
			finalReveals = len(r1Scores)
			markOutlierBands(esc.Evaluations, finalMedian)
			escalated = true
		} else if len(esc.Evaluations) > 0 {
			markOutlierBands(esc.Evaluations, median)
			setPurchaseCondition(&status.Conditions, "Escalated", "True", "EscalationNoReveals",
				"Round-1 panel produced no valid reveals — the round-0 median stands")
		}
	}

	escalationNote := ""
	if escalated {
		escalationNote = fmt.Sprintf(" — escalated (%s); round-1 median is final", status.Escalation.Reason)
	}
	status.WeightedScore = finalMedian
	if finalMedian >= evalPassThreshold {
		setPurchaseCondition(&status.Conditions, "Verified", "True", "EvaluatorQuorum",
			fmt.Sprintf("Median score %d/100 from %d reveal(s) meets the %d threshold%s", finalMedian, finalReveals, evalPassThreshold, escalationNote))
		if len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseSubmitted {
			status.Claims[0].Phase = bountyPhaseVerified
		}
	} else {
		setPurchaseCondition(&status.Conditions, "Verified", "False", "EvaluatorQuorum",
			fmt.Sprintf("Median score %d/100 from %d reveal(s) is below the %d threshold%s", finalMedian, finalReveals, evalPassThreshold, escalationNote))
		if len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseSubmitted {
			status.Claims[0].Phase = bountyPhaseRejected
		}
	}

	// 6. Settlement side-effects, once per bounty: pay the evaluators
	// (win-or-lose — they did the work), ground reveals against the chain,
	// and record the cross-bounty ladder. Grounding runs BEFORE ladder
	// bookkeeping (recordLadder reads Grounded) and never changes the verdict.
	c.settleEvalBudget(ctx, sb, status)
	c.settleEscalationBudget(ctx, sb, status)
	if !status.LadderRecorded {
		c.groundEvaluations(ctx, sb, status, status.Evaluations)
		if status.Escalation != nil {
			c.groundEvaluations(ctx, sb, status, status.Escalation.Evaluations)
		}
		err := c.recordLadder(ctx, sb, status)
		if err == nil && status.Escalation != nil && len(status.Escalation.Evaluations) > 0 {
			// Ladder bookkeeping covers round-1 participants too, graded
			// against the round-1 median (already banded above).
			roundOne := *status
			roundOne.Evaluations = status.Escalation.Evaluations
			err = c.recordLadder(ctx, sb, &roundOne)
		}
		if err != nil {
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
// reserve retries on the next reconcile. An AwaitingVoucher hold re-reserves
// each reconcile (idempotent) until the obol.org/eval-voucher annotation
// ferries the poster's Permit2 voucher in.
func (c *Controller) reserveEvalBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, annotations map[string]string, status *monetizeapi.ServiceBountyStatus) {
	if sb.Spec.Eval.Payment.PerEvaluator == "" {
		return
	}
	if status.EvalBudgetState != "" && status.EvalBudgetState != escrowStateAwaitingVoucher {
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
		Voucher: voucherFromAnnotations(annotations, bountyEvalVoucherAnnotation),
	})
	if err != nil {
		log.Printf("serviceoffer-controller: reserve eval budget for %s/%s: %v", sb.Namespace, sb.Name, err)
		return
	}
	status.EvalBudgetState = receipt.State
	ferryEscrowSpender(status, receipt)
}

// evalSeatAmounts resolves the per-evaluator eval price into the full and
// probation-half per-seat amount strings used for CaptureBatch recipients.
// When the asset resolves in the token registry the amounts are ATOMIC token
// units — escrow.BuildTransferDetails matches capture recipients against the
// poster's Permit2 voucher seats with exact integer comparison, and the CLI
// (cmd/obol bountyEvalFundRecipients) signs perAtomic / floor(perAtomic/2).
// An unresolvable asset falls back to human-unit strings: the dev ledger
// gateway treats amounts as opaque bookkeeping, and a real facilitator could
// never have verified a voucher for a token the CLI cannot resolve either.
func evalSeatAmounts(sb *monetizeapi.ServiceBounty) (full, half string, ok bool) {
	per := strings.TrimSpace(sb.Spec.Eval.Payment.PerEvaluator)
	perFloat, err := strconv.ParseFloat(per, 64)
	if err != nil || perFloat <= 0 {
		return "", "", false
	}
	full = strconv.FormatFloat(perFloat, 'f', 2, 64)
	half = strconv.FormatFloat(perFloat/2, 'f', 2, 64)
	entry, found := x402.ResolveToken(sb.Spec.Eval.Payment.Asset, sb.Spec.Reward.Network)
	if !found {
		return full, half, true
	}
	atomicStr, err := escrow.HumanToAtomic(per, entry.Decimals)
	if err != nil {
		return full, half, true
	}
	perAtomic, parsed := new(big.Int).SetString(atomicStr, 10)
	if !parsed {
		return full, half, true
	}
	return perAtomic.String(), new(big.Int).Div(perAtomic, big.NewInt(2)).String(), true
}

// settleEvalBudget batch-settles the held eval budget to every counting
// evaluator with a valid reveal (probation seats at half price — the discount
// already went to the poster at reserve time). Shadows evaluate free; non/bad
// reveals earn nothing (the monetary edge of the non-reveal penalty).
func (c *Controller) settleEvalBudget(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	if status.EvalBudgetState != escrow.StateReserved {
		return
	}
	fullAmount, halfAmount, ok := evalSeatAmounts(sb)
	if !ok {
		return
	}

	var recipients []escrow.BatchRecipient
	paid := make(map[string]bool)
	k := evalQuorumK(sb)
	for i := range status.Evaluations {
		evaluation := &status.Evaluations[i]
		if evaluation.Phase != evalPhaseRevealed || evaluation.Seat == monetizeapi.PanelSeatShadow {
			continue
		}
		if int64(len(recipients)) >= k {
			break // open-door can over-subscribe; the budget pays k seats
		}
		amount := fullAmount
		if evaluation.Seat == monetizeapi.PanelSeatProbation {
			amount = halfAmount
		}
		recipients = append(recipients, escrow.BatchRecipient{
			Address: evaluation.Address,
			Amount:  amount,
		})
		paid[evaluation.Address] = true
	}
	if len(recipients) == 0 {
		return // nothing to pay; refund path voids the budget
	}

	var receipt escrow.Receipt
	var err error
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
	ferryEscrowSpender(status, receipt)
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
	total := float64(evalQuorumK(sb)) * per
	for _, seat := range status.EvaluatorPanel {
		if seat.Seat == monetizeapi.PanelSeatProbation {
			total -= per / 2
			break
		}
	}
	return strconv.FormatFloat(total, 'f', 2, 64)
}

// evalQuorumK is spec.eval.k floored at 1 (the median of one is that one).
func evalQuorumK(sb *monetizeapi.ServiceBounty) int64 {
	k := sb.Spec.Eval.K
	if k < 1 {
		k = 1
	}
	return k
}

// countingScores collects the revealed scores of counting (non-shadow) seats.
func countingScores(evaluations []monetizeapi.ServiceBountyEvaluation) []int64 {
	var scores []int64
	for _, evaluation := range evaluations {
		if evaluation.Phase == evalPhaseRevealed && evaluation.Seat != monetizeapi.PanelSeatShadow {
			scores = append(scores, evaluation.Score)
		}
	}
	return scores
}

// markOutlierBands grades every evaluation's divergence from the median:
// revealed scores within evalOutlierBand are in band; non/bad reveals are
// worst-case outliers by definition.
func markOutlierBands(evaluations []monetizeapi.ServiceBountyEvaluation, median int64) {
	for i := range evaluations {
		evaluation := &evaluations[i]
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

// ── voucher ferry helpers ───────────────────────────────────────────────────

// voucherFromAnnotations decodes the JSON Permit2 voucher ferried on the given
// annotation. A voucher carries ONLY poster-signed transfer fields
// (escrow.Permit2Voucher); escrow endpoint/credential configuration comes from
// controller env alone (newBountyEscrowGateway) and can never ride in here.
// Malformed payloads are treated as absent — the facilitator keeps the hold in
// AwaitingVoucher until a valid voucher arrives.
func voucherFromAnnotations(annotations map[string]string, key string) *escrow.Permit2Voucher {
	raw := strings.TrimSpace(annotations[key])
	if raw == "" {
		return nil
	}
	var voucher escrow.Permit2Voucher
	if err := json.Unmarshal([]byte(raw), &voucher); err != nil {
		log.Printf("serviceoffer-controller: invalid %s annotation (ignored): %v", key, err)
		return nil
	}
	return &voucher
}

// ferryEscrowSpender records the FIRST non-empty facilitator spender address
// seen on any escrow receipt into status.escrowSpender, so poster-side signers
// know which executor to bind their Permit2 vouchers to.
func ferryEscrowSpender(status *monetizeapi.ServiceBountyStatus, receipt escrow.Receipt) {
	if status.EscrowSpender == "" && receipt.Spender != "" {
		status.EscrowSpender = receipt.Spender
	}
}

// isEscrowVoucherRefusal classifies a facilitator capture refusal caused by a
// missing/expired voucher (HTTPGateway surfaces the response body inside the
// error text). Such refusals park as a condition + requeue — a poster-side
// signing gap must never fail the reconcile loop. Only the facilitator's 409
// awaiting-voucher refusal parks: a 400 seat-mismatch (recipients not in the
// stored voucher) must surface as a capture failure, not loop as "awaiting".
func isEscrowVoucherRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "facilitator returned 409") && strings.Contains(msg, "voucher")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
