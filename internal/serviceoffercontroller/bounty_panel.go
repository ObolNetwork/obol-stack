package serviceoffercontroller

// Evaluator panel selection + ladder bookkeeping (design doc §11.4).
//
// Selection is controller-side weighted sampling — the honest local-first
// stand-in for VRF (the swap seam is exactly this function). It is
// DETERMINISTIC per bounty: seeded from the controller's seedSource (local:
// sha256(UID); drand: a beacon that does not exist yet at posting time) so
// every reconcile computes the same panel (idempotence), and the poster
// cannot re-roll evaluators by touching the spec. The seed's provenance is
// persisted into status.panelSeed so the draw is auditable.
//
// Seats: k counting seats (Full tier, plus at most ONE Probation seat on
// value-capped bounties — the median absorbs one outlier, which is what makes
// the newcomer seat verdict-safe) + up to two free Shadow seats, randomly
// ASSIGNED (a sybil can't choose where to warm reputation). If the enrolled
// pool can't fill k counting seats the bounty falls back to open-door (any
// address may evaluate), and ladder bookkeeping still applies to enrolled
// participants — open-door participation is how the first evaluators climb
// out of Shadow.
//
// Reputation is read through the decay lens (internal/bounty/decay.go): the
// lottery weight uses the half-life-decayed completion count, a stored Full
// tier reads as Probation once stale, and chain-grounded verdicts earn a
// weight bonus. Stored counters are never mutated by decay.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

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

	// escalationSeedSuffix derives the escalation-round seed from the round-0
	// seed: sha256(round0seed || suffix). Same beacon, distinct lottery.
	escalationSeedSuffix = "escalation-r1"
)

// evaluatorCandidate is one enrolled evaluator considered for selection.
type evaluatorCandidate struct {
	Address string
	Record  monetizeapi.EvaluatorLadderRecord
}

// panelSeedSource returns the controller's seed source, defaulting to the
// local deterministic seed when none was wired (tests construct Controller
// literals).
func (c *Controller) panelSeedSource() seedSource {
	if c.seeds == nil {
		return localSeedSource{}
	}
	return c.seeds
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

// ladderForTask resolves the task package's ladder for taskRef; zero Ladder
// (with parse-time defaults applied by callees) when the type is unknown.
func ladderForTask(taskRef string) bounty.Ladder {
	if t, err := bounty.Resolve(taskRef); err == nil {
		return t.Eval.Ladder
	}
	return bounty.Ladder{}
}

// ladderWeight is THE lottery weight: 1 + 0.1×(effectiveCompleted −
// divergences) floored at 0.1, where effectiveCompleted is the half-life-
// decayed completion count; ×0.25 pair-diversity penalty for a recently
// judged fulfiller; ×(1 + min(1, grounded/completed)) bonus for verdicts
// grounded by on-chain ERC-8004 validation entries.
func ladderWeight(record monetizeapi.EvaluatorLadderRecord, fulfiller string, halfLife time.Duration, now time.Time) float64 {
	var lastEval *time.Time
	if record.LastEvalAt != nil {
		lastEval = &record.LastEvalAt.Time
	}
	effective := bounty.EffectiveCompleted(int(record.Completed), lastEval, now, halfLife)
	w := 1.0 + 0.1*(effective-float64(record.Divergences))
	if w < 0.1 {
		w = 0.1
	}
	if fulfiller != "" && slices.Contains(record.RecentFulfillers, fulfiller) {
		w *= pairDiversityWeight
	}
	denom := record.Completed
	if denom < 1 {
		denom = 1
	}
	bonus := float64(record.GroundedEvals) / float64(denom)
	if bonus > 1 {
		bonus = 1
	}
	return w * (1 + bonus)
}

// rngFromSeed turns the 32-byte panel seed into the deterministic lottery RNG.
func rngFromSeed(seed [32]byte) *rand.Rand {
	return rand.New(rand.NewSource(int64(binary.BigEndian.Uint64(seed[:8])))) //nolint:gosec // deterministic-by-design selection, not crypto
}

// selectEvaluatorPanel performs the deterministic weighted sampling. Returns
// nil when the counting pool (Full+Probation, read through the decay lens)
// cannot fill k seats — the open-door fallback.
func selectEvaluatorPanel(seed [32]byte, pool []monetizeapi.EvaluatorEnrollment, taskRef string, k int64, rewardAmount string, ladder bounty.Ladder, fulfiller string, now time.Time) []monetizeapi.ServiceBountyPanelSeat {
	halfLife := ladder.DecayHalfLifeDuration()

	var full, probation, shadow []evaluatorCandidate
	for i := range pool {
		candidate := evaluatorCandidate{
			Address: pool[i].Spec.Address,
			Record:  ladderRecordFor(&pool[i], taskRef),
		}
		// Tier gating goes through the decay lens: a stale Full reads as
		// Probation here without mutating the stored record.
		switch bounty.EffectiveTier(candidate.Record, ladder, now) {
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

	rng := rngFromSeed(seed)
	weight := func(candidate evaluatorCandidate) float64 {
		return ladderWeight(candidate.Record, fulfiller, halfLife, now)
	}

	var seats []monetizeapi.ServiceBountyPanelSeat

	// One reserved probation seat on value-capped bounties: the median-of-k
	// absorbs one outlier, so the newcomer seat is verdict-safe by
	// construction — and only offered where the value cap allows.
	remaining := k
	if len(probation) > 0 && withinValueCap(rewardAmount, ladder.ProbationValueCap) && k >= 3 {
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
// evaluation already started). A seed-source failure (drand relay down or a
// beacon failing verification) does NOT latch: the panel stays unselected and
// the bounty is requeued — never a silent fallback to the local seed.
func (c *Controller) ensurePanel(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus) {
	for _, condition := range status.Conditions {
		if condition.Type == "PanelSelected" {
			return
		}
	}

	seed, provenance, err := c.panelSeedSource().Seed(ctx, string(sb.UID), sb.CreationTimestamp.Time)
	if err != nil {
		log.Printf("bounty %s/%s: panel seed unavailable, retrying in %s: %v", sb.Namespace, sb.Name, seedRetryDelay, err)
		if c.bountyQueue != nil {
			c.bountyQueue.AddAfter(sb.Namespace+"/"+sb.Name, seedRetryDelay)
		}
		return
	}
	status.PanelSeed = &provenance

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
	fulfiller := ""
	if len(status.Claims) > 0 {
		fulfiller = status.Claims[0].FulfillerAddress
	}

	seats := selectEvaluatorPanel(seed, pool, taskRef, k, sb.Spec.Reward.Amount, ladderForTask(taskRef), fulfiller, time.Now())
	if seats == nil {
		setPurchaseCondition(&status.Conditions, "PanelSelected", "False", "OpenDoor",
			fmt.Sprintf("Enrolled pool has fewer than %d counting evaluators — open-door evaluation", k))
		return
	}
	status.EvaluatorPanel = seats
	setPurchaseCondition(&status.Conditions, "PanelSelected", "True", "Selected",
		fmt.Sprintf("%d counting seat(s) + %d shadow(s) selected from %d enrolled", k, len(seats)-int(k), len(pool)))
}

// selectEscalationPanel draws the second-round panel for an escalated verdict:
// a FRESH, larger panel where every seat counts at full pay (no probation
// discount, no shadows — escalation is the tiebreaker, not the on-ramp), and
// every round-0 participant is excluded (keys of exclude are canonical EIP-55
// addresses). The seed derives deterministically from the same round-0 seed
// ensurePanel used — sha256(round0seed || "escalation-r1") — recomputed via
// the seedSource (the provenance in status guarantees the same beacon), so
// repeated reconciles draw the same escalation panel. A pool smaller than
// size falls back to open-door (nil seats), same semantics as round 0.
func (c *Controller) selectEscalationPanel(ctx context.Context, sb *unstructured.Unstructured, size int, exclude map[string]bool) ([]monetizeapi.ServiceBountyPanelSeat, error) {
	taskRef, _, _ := unstructured.NestedString(sb.Object, "spec", "task", "typeRef")
	pool, err := c.listEnrollmentsForTask(ctx, sb.GetNamespace(), taskRef)
	if err != nil {
		return nil, err
	}

	round0Seed, _, err := c.panelSeedSource().Seed(ctx, string(sb.GetUID()), sb.GetCreationTimestamp().Time)
	if err != nil {
		return nil, err
	}
	seed := sha256.Sum256(append(round0Seed[:], []byte(escalationSeedSuffix)...))

	ladder := ladderForTask(taskRef)
	halfLife := ladder.DecayHalfLifeDuration()
	now := time.Now()

	fulfiller := ""
	if claims, _, _ := unstructured.NestedSlice(sb.Object, "status", "claims"); len(claims) > 0 {
		if claim, ok := claims[0].(map[string]any); ok {
			fulfiller, _ = claim["fulfillerAddress"].(string)
		}
	}

	var counting []evaluatorCandidate
	for i := range pool {
		if exclude[common.HexToAddress(pool[i].Spec.Address).Hex()] {
			continue // round-0 participants never re-judge their own divergence
		}
		candidate := evaluatorCandidate{
			Address: pool[i].Spec.Address,
			Record:  ladderRecordFor(&pool[i], taskRef),
		}
		switch bounty.EffectiveTier(candidate.Record, ladder, now) {
		case monetizeapi.EvaluatorTierFull, monetizeapi.EvaluatorTierProbation:
			counting = append(counting, candidate)
		}
	}
	if len(counting) < size {
		return nil, nil // open-door fallback, same as round 0's thin pool
	}

	rng := rngFromSeed(seed)
	weight := func(candidate evaluatorCandidate) float64 {
		return ladderWeight(candidate.Record, fulfiller, halfLife, now)
	}

	var seats []monetizeapi.ServiceBountyPanelSeat
	for len(seats) < size && len(counting) > 0 {
		pick := weightedPick(rng, counting, weight)
		seats = append(seats, monetizeapi.ServiceBountyPanelSeat{Address: pick.Address, Seat: monetizeapi.PanelSeatFull})
		counting = removeCandidate(counting, pick.Address)
	}
	sort.Slice(seats, func(i, j int) bool { return seats[i].Address < seats[j].Address })
	return seats, nil
}

// recordLadder applies the one-shot cross-bounty bookkeeping after the quorum
// settles: completion/divergence counters, shadow agreements, probation
// progress, tier promotions, the decay anchor (lastEvalAt), grounded-verdict
// counts, and the pair-diversity history.
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
	now := metav1.Now()

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
		record.LastEvalAt = now.DeepCopy() // the decay anchor: every counted participation re-stamps it
		if evaluation.Grounded {
			record.GroundedEvals++
		}
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
