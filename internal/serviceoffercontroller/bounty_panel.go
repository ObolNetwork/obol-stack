package serviceoffercontroller

// Evaluator panel selection + ladder bookkeeping (design doc §11.4).
//
// Selection is controller-side weighted sampling — the honest local-first
// stand-in for VRF (the swap seam is exactly this function). It is
// DETERMINISTIC per bounty: seeded from the bounty UID so every reconcile
// computes the same panel (idempotence), and the poster cannot re-roll
// evaluators by touching the spec.
//
// Seats: k counting seats (Full tier, plus at most ONE Probation seat on
// value-capped bounties — the median absorbs one outlier, which is what makes
// the newcomer seat verdict-safe) + up to two free Shadow seats, randomly
// ASSIGNED (a sybil can't choose where to warm reputation). If the enrolled
// pool can't fill k counting seats the bounty falls back to open-door (any
// address may evaluate), and ladder bookkeeping still applies to enrolled
// participants — open-door participation is how the first evaluators climb
// out of Shadow.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	maxShadowSeats       = 2
	recentFulfillersKept = 5
	// pairDiversityWeight down-weights an evaluator who recently judged the
	// same fulfiller (anti-collusion: break up cozy evaluator↔fulfiller pairs).
	pairDiversityWeight = 0.25
)

// evaluatorCandidate is one enrolled evaluator considered for selection.
type evaluatorCandidate struct {
	Address string
	Record  monetizeapi.EvaluatorLadderRecord
}

// listEnrollmentsForTask returns the enrolled evaluators for a task type in
// the bounty's namespace.
func (c *Controller) listEnrollmentsForTask(ctx context.Context, namespace, taskRef string) ([]monetizeapi.EvaluatorEnrollment, error) {
	raw, err := c.dynClient.Resource(monetizeapi.EvaluatorEnrollmentGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []monetizeapi.EvaluatorEnrollment
	for i := range raw.Items {
		var enrollment monetizeapi.EvaluatorEnrollment
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Items[i].Object, &enrollment); err != nil {
			continue
		}
		if slices.Contains(enrollment.Spec.TaskTypes, taskRef) {
			out = append(out, enrollment)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.Address < out[j].Spec.Address })
	return out, nil
}

// ladderRecordFor returns the enrollment's ladder record for taskRef; new
// enrollments start at Shadow.
func ladderRecordFor(enrollment *monetizeapi.EvaluatorEnrollment, taskRef string) monetizeapi.EvaluatorLadderRecord {
	for _, r := range enrollment.Status.Records {
		if r.TaskType == taskRef {
			return r
		}
	}
	return monetizeapi.EvaluatorLadderRecord{TaskType: taskRef, Tier: monetizeapi.EvaluatorTierShadow}
}

// selectEvaluatorPanel performs the deterministic weighted sampling. Returns
// nil when the counting pool (Full+Probation) cannot fill k seats — the
// open-door fallback.
func selectEvaluatorPanel(uid string, pool []monetizeapi.EvaluatorEnrollment, taskRef string, k int64, rewardAmount, probationValueCap, fulfiller string) []monetizeapi.ServiceBountyPanelSeat {
	var full, probation, shadow []evaluatorCandidate
	for i := range pool {
		candidate := evaluatorCandidate{
			Address: pool[i].Spec.Address,
			Record:  ladderRecordFor(&pool[i], taskRef),
		}
		switch candidate.Record.Tier {
		case monetizeapi.EvaluatorTierFull:
			full = append(full, candidate)
		case monetizeapi.EvaluatorTierProbation:
			probation = append(probation, candidate)
		default:
			shadow = append(shadow, candidate)
		}
	}

	counting := len(full) + len(probation)
	if int64(counting) < k {
		return nil // open-door fallback
	}

	// Deterministic seed: same bounty → same panel, every reconcile.
	sum := sha256.Sum256([]byte(uid))
	rng := rand.New(rand.NewSource(int64(binary.BigEndian.Uint64(sum[:8])))) //nolint:gosec // deterministic-by-design selection, not crypto

	weight := func(candidate evaluatorCandidate) float64 {
		w := 1.0 + 0.1*float64(candidate.Record.Completed-candidate.Record.Divergences)
		if w < 0.1 {
			w = 0.1
		}
		if slices.Contains(candidate.Record.RecentFulfillers, fulfiller) {
			w *= pairDiversityWeight
		}
		return w
	}

	var seats []monetizeapi.ServiceBountyPanelSeat

	// One reserved probation seat on value-capped bounties: the median-of-k
	// absorbs one outlier, so the newcomer seat is verdict-safe by
	// construction — and only offered where the value cap allows.
	remaining := k
	if len(probation) > 0 && withinValueCap(rewardAmount, probationValueCap) && k >= 3 {
		pick := weightedPick(rng, probation, weight)
		seats = append(seats, monetizeapi.ServiceBountyPanelSeat{Address: pick.Address, Seat: monetizeapi.PanelSeatProbation})
		probation = removeCandidate(probation, pick.Address)
		remaining--
	}

	countingPool := append(append([]evaluatorCandidate{}, full...), probation...)
	for remaining > 0 && len(countingPool) > 0 {
		pick := weightedPick(rng, countingPool, weight)
		seats = append(seats, monetizeapi.ServiceBountyPanelSeat{Address: pick.Address, Seat: monetizeapi.PanelSeatFull})
		countingPool = removeCandidate(countingPool, pick.Address)
		remaining--
	}
	if remaining > 0 {
		return nil // pool shrank under us — open-door
	}

	// Shadows are randomly ASSIGNED, never chosen by the evaluator.
	for i := 0; i < maxShadowSeats && len(shadow) > 0; i++ {
		pick := shadow[rng.Intn(len(shadow))]
		seats = append(seats, monetizeapi.ServiceBountyPanelSeat{Address: pick.Address, Seat: monetizeapi.PanelSeatShadow})
		shadow = removeCandidate(shadow, pick.Address)
	}

	sort.Slice(seats, func(i, j int) bool { return seats[i].Address < seats[j].Address })
	return seats
}

func weightedPick(rng *rand.Rand, pool []evaluatorCandidate, weight func(evaluatorCandidate) float64) evaluatorCandidate {
	total := 0.0
	for _, candidate := range pool {
		total += weight(candidate)
	}
	target := rng.Float64() * total
	for _, candidate := range pool {
		target -= weight(candidate)
		if target <= 0 {
			return candidate
		}
	}
	return pool[len(pool)-1]
}

func removeCandidate(pool []evaluatorCandidate, address string) []evaluatorCandidate {
	out := pool[:0]
	for _, candidate := range pool {
		if candidate.Address != address {
			out = append(out, candidate)
		}
	}
	return out
}

func withinValueCap(amount, cap string) bool {
	a, errA := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	c, errC := strconv.ParseFloat(strings.TrimSpace(cap), 64)
	if errA != nil || errC != nil || c <= 0 {
		return false
	}
	return a <= c
}

// ensurePanel runs selection exactly once per bounty (latched by the
// PanelSelected condition so a growing pool can never re-gate a bounty whose
// evaluation already started).
func (c *Controller) ensurePanel(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	for _, condition := range status.Conditions {
		if condition.Type == "PanelSelected" {
			return
		}
	}

	taskRef := sb.Spec.Task.TypeRef
	pool, err := c.listEnrollmentsForTask(ctx, sb.Namespace, taskRef)
	if err != nil {
		// Missing CRD / transient list error → open-door, recorded as such.
		setPurchaseCondition(&status.Conditions, "PanelSelected", "False", "OpenDoor",
			truncateMessage(fmt.Sprintf("enrollment pool unavailable (%v) — open-door evaluation", err)))
		return
	}

	k := sb.Spec.Eval.K
	if k < 1 {
		k = 1
	}
	cap := ""
	if t, err := bounty.Resolve(taskRef); err == nil {
		cap = t.Eval.Ladder.ProbationValueCap
	}
	fulfiller := ""
	if len(status.Claims) > 0 {
		fulfiller = status.Claims[0].FulfillerAddress
	}

	seats := selectEvaluatorPanel(string(sb.UID), pool, taskRef, k, sb.Spec.Reward.Amount, cap, fulfiller)
	if seats == nil {
		setPurchaseCondition(&status.Conditions, "PanelSelected", "False", "OpenDoor",
			fmt.Sprintf("Enrolled pool has fewer than %d counting evaluators — open-door evaluation", k))
		return
	}
	status.EvaluatorPanel = seats
	setPurchaseCondition(&status.Conditions, "PanelSelected", "True", "Selected",
		fmt.Sprintf("%d counting seat(s) + %d shadow(s) selected from %d enrolled", k, len(seats)-int(k), len(pool)))
}

// recordLadder applies the one-shot cross-bounty bookkeeping after the quorum
// settles: completion/divergence counters, shadow agreements, probation
// progress, tier promotions, and the pair-diversity history.
func (c *Controller) recordLadder(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) error {
	taskRef := sb.Spec.Task.TypeRef
	thresholds := bounty.Ladder{ShadowAgreements: 5, ProbationEvals: 10}
	if t, err := bounty.Resolve(taskRef); err == nil && t.Eval.Ladder.ShadowAgreements > 0 {
		thresholds = t.Eval.Ladder
	}
	fulfiller := ""
	if len(status.Claims) > 0 {
		fulfiller = status.Claims[0].FulfillerAddress
	}

	for _, evaluation := range status.Evaluations {
		raw, err := c.findEnrollmentByAddress(ctx, sb.Namespace, evaluation.Address)
		if err != nil || raw == nil {
			continue // unenrolled open-door participant — nothing to record
		}
		var enrollment monetizeapi.EvaluatorEnrollment
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &enrollment); err != nil {
			continue
		}

		record := ladderRecordFor(&enrollment, taskRef)
		record.Completed++
		if !evaluation.WithinBand {
			record.Divergences++
		}
		switch record.Tier {
		case monetizeapi.EvaluatorTierShadow:
			if evaluation.WithinBand {
				record.ShadowAgreements++
			}
			if record.ShadowAgreements >= int64(thresholds.ShadowAgreements) {
				record.Tier = monetizeapi.EvaluatorTierProbation
			}
		case monetizeapi.EvaluatorTierProbation:
			if evaluation.WithinBand {
				record.ProbationEvals++
			}
			if record.ProbationEvals >= int64(thresholds.ProbationEvals) {
				record.Tier = monetizeapi.EvaluatorTierFull
			}
		}
		if fulfiller != "" {
			record.RecentFulfillers = append([]string{fulfiller}, record.RecentFulfillers...)
			if len(record.RecentFulfillers) > recentFulfillersKept {
				record.RecentFulfillers = record.RecentFulfillers[:recentFulfillersKept]
			}
		}

		replaced := false
		for i := range enrollment.Status.Records {
			if enrollment.Status.Records[i].TaskType == taskRef {
				enrollment.Status.Records[i] = record
				replaced = true
			}
		}
		if !replaced {
			enrollment.Status.Records = append(enrollment.Status.Records, record)
		}

		statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&enrollment.Status)
		if err != nil {
			return err
		}
		patched := raw.DeepCopy()
		patched.Object["status"] = statusObject
		if _, err := c.dynClient.Resource(monetizeapi.EvaluatorEnrollmentGVR).Namespace(sb.Namespace).UpdateStatus(ctx, patched, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update ladder for %s: %w", evaluation.Address, err)
		}
	}
	return nil
}

func (c *Controller) findEnrollmentByAddress(ctx context.Context, namespace, address string) (*unstructured.Unstructured, error) {
	list, err := c.dynClient.Resource(monetizeapi.EvaluatorEnrollmentGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if addr, _, _ := unstructured.NestedString(list.Items[i].Object, "spec", "address"); strings.EqualFold(addr, address) {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}
