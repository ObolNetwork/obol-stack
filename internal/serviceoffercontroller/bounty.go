package serviceoffercontroller

// ServiceBounty reconcile — the demand-side sibling pass, following the
// RegistrationRequest/PurchaseRequest precedent: one more informer + queue +
// worker on the same Controller, in the same binary.
//
// Lifecycle: Open → Claimed → Submitted → Verified → Paid, with Expired →
// Refunded on deadline and Rejected on a poster verdict. Machine truth is the
// condition set (TaskValid, EscrowReserved, Claimed, Submitted, Verified,
// Paid); status.phase is the human rollup.
//
// Claim/submit/verdict arrive as ANNOTATIONS on the CR (the k8s-native write
// channel for agents/CLI, validated and promoted into controller-owned
// status). v1 trust posture is the design doc's v0: escrow via the Gateway
// seam (dev-ledger locally until the facilitator routes ship) and
// poster-as-judge acceptance; the OBOL eval market replaces the poster verdict
// in a later slice. The controller signs NOTHING — see internal/x402/escrow.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

const (
	serviceBountyFinalizer = "obol.org/servicebounty-finalizer"

	// Annotation write-channel (agent/CLI → controller).
	bountyClaimAnnotation   = "obol.org/claim"   // fulfiller payout address (0x…)
	bountyCommitAnnotation  = "obol.org/commit"  // commit hash (anti bait-and-switch)
	bountySubmitAnnotation  = "obol.org/submit"  // JSON {"resultHash":"…","reportURI":"…"}
	bountyVerdictAnnotation = "obol.org/verdict" // "accept" or "reject:<reason>"

	bountyPhaseInvalid   = "Invalid"
	bountyPhaseOpen      = "Open"
	bountyPhaseClaimed   = "Claimed"
	bountyPhaseSubmitted = "Submitted"
	bountyPhaseVerified  = "Verified"
	bountyPhasePaid      = "Paid"
	bountyPhaseRejected  = "Rejected"
	bountyPhaseExpired   = "Expired"
	bountyPhaseRefunded  = "Refunded"
)

// bountySubmission is the bountySubmitAnnotation payload.
type bountySubmission struct {
	ResultHash string `json:"resultHash"`
	ReportURI  string `json:"reportURI"`
}

func (c *Controller) enqueueBounty(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build bounty queue key: %v", err)
		return
	}
	c.bountyQueue.Add(key)
}

func (c *Controller) processNextBounty(ctx context.Context) bool {
	key, shutdown := c.bountyQueue.Get()
	if shutdown {
		return false
	}
	defer c.bountyQueue.Done(key)

	if err := c.reconcileBounty(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile bounty %s: %v", key, err)
		c.bountyQueue.AddRateLimited(key)
		return true
	}

	c.bountyQueue.Forget(key)
	return true
}

func (c *Controller) reconcileBounty(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	raw, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var sb monetizeapi.ServiceBounty
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &sb); err != nil {
		return fmt.Errorf("decode ServiceBounty: %w", err)
	}

	// Deletion: best-effort escrow void (poster keeps funds), then release
	// the finalizer. A captured escrow is final — void refuses, and we still
	// remove the finalizer (the reward was legitimately paid).
	if raw.GetDeletionTimestamp() != nil {
		if !slices.Contains(raw.GetFinalizers(), serviceBountyFinalizer) {
			return nil
		}
		if sb.Status.EscrowState == escrow.StateReserved {
			if _, err := c.escrowGateway().Void(ctx, string(sb.UID)); err != nil {
				log.Printf("serviceoffer-controller: void escrow for deleting bounty %s: %v", key, err)
			}
		}
		if sb.Status.BondState == escrow.StateReserved {
			if _, err := c.escrowGateway().Void(ctx, string(sb.UID)+"-bond"); err != nil {
				log.Printf("serviceoffer-controller: void bond for deleting bounty %s: %v", key, err)
			}
		}
		if sb.Status.EvalBudgetState == escrow.StateReserved {
			if _, err := c.escrowGateway().Void(ctx, string(sb.UID)+"-eval"); err != nil {
				log.Printf("serviceoffer-controller: void eval budget for deleting bounty %s: %v", key, err)
			}
		}
		return c.removeBountyFinalizer(ctx, raw)
	}

	if !slices.Contains(raw.GetFinalizers(), serviceBountyFinalizer) {
		patched := raw.DeepCopy()
		patched.SetFinalizers(append(patched.GetFinalizers(), serviceBountyFinalizer))
		_, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(namespace).Update(ctx, patched, metav1.UpdateOptions{})
		return err
	}

	status := sb.Status
	status.ObservedGeneration = sb.Generation

	// 1. Task validity — typeRef must resolve against the embedded registry
	// and params must satisfy the package's schema. Invalid bounties park
	// (no requeue): only a spec change can fix them.
	if err := validateBountyTask(&sb); err != nil {
		setPurchaseCondition(&status.Conditions, "TaskValid", "False", "InvalidTask", truncateMessage(err.Error()))
		status.Phase = bountyPhaseInvalid
		return c.updateBountyStatus(ctx, raw, status)
	}
	setPurchaseCondition(&status.Conditions, "TaskValid", "True", "Resolved", fmt.Sprintf("Task type %s resolved", sb.Spec.Task.TypeRef))

	// 2. Deadline — past it with no accepted verdict, the escrow is returned
	// to the poster. Requeue at expiry so the refund happens on time without
	// any spec mutation (the DrainAt requeue discipline).
	if sb.Spec.Deadline != nil && !bountyConditionIsTrue(status.Conditions, "Verified") {
		now := time.Now()
		if now.After(sb.Spec.Deadline.Time) {
			return c.refundBounty(ctx, raw, &sb, status, "DeadlineExpired",
				fmt.Sprintf("Deadline %s passed without an accepted submission", sb.Spec.Deadline.UTC().Format(time.RFC3339)))
		}
		if delay := time.Until(sb.Spec.Deadline.Time) + time.Second; delay > 0 {
			c.bountyQueue.AddAfter(key, delay)
		}
	}

	// 3. Escrow reserve — hold the reward before any claim is admitted, so a
	// fulfiller never starts work against an unfunded bounty.
	if status.EscrowState == "" {
		receipt, err := c.escrowGateway().Reserve(ctx, escrow.ReserveRequest{
			ID:      string(sb.UID),
			Network: sb.Spec.Reward.Network,
			PayTo:   sb.Spec.Reward.PayTo,
			Asset:   sb.Spec.Reward.Asset.Symbol,
			Amount:  sb.Spec.Reward.Amount,
			Scheme:  sb.Spec.Reward.Escrow.Scheme,
		})
		if err != nil {
			setPurchaseCondition(&status.Conditions, "EscrowReserved", "False", "FacilitatorError", truncateMessage(err.Error()))
			status.Phase = bountyPhaseOpen
			if statusErr := c.updateBountyStatus(ctx, raw, status); statusErr != nil {
				return statusErr
			}
			return err // rate-limited retry
		}
		status.EscrowState = receipt.State
	}
	setPurchaseCondition(&status.Conditions, "EscrowReserved", "True", "Reserved", escrowReason(c.escrowGateway()))

	// 4. Claim — promote the claim annotation into controller-owned status.
	annotations := raw.GetAnnotations()
	if claim := strings.TrimSpace(annotations[bountyClaimAnnotation]); claim != "" && len(status.Claims) == 0 {
		if !common.IsHexAddress(claim) {
			setPurchaseCondition(&status.Conditions, "Claimed", "False", "InvalidAddress",
				fmt.Sprintf("claim annotation %q is not a hex address", claim))
			status.Phase = bountyPhaseOpen
			return c.updateBountyStatus(ctx, raw, status)
		}
		now := metav1.Now()
		status.Claims = []monetizeapi.ServiceBountyClaim{{
			FulfillerAddress: common.HexToAddress(claim).Hex(),
			ClaimedAt:        &now,
			CommitHash:       strings.TrimSpace(annotations[bountyCommitAnnotation]),
			Phase:            bountyPhaseClaimed,
		}}
	}
	if len(status.Claims) > 0 {
		setPurchaseCondition(&status.Conditions, "Claimed", "True", "Claimed",
			fmt.Sprintf("Claimed by %s", status.Claims[0].FulfillerAddress))
		// Late commit: the commit annotation may land after the claim.
		if commit := strings.TrimSpace(annotations[bountyCommitAnnotation]); commit != "" && status.Claims[0].CommitHash == "" {
			status.Claims[0].CommitHash = commit
		}
	} else {
		setPurchaseCondition(&status.Conditions, "Claimed", "False", "Open", "No fulfiller has claimed this bounty")
	}

	// 4b. Self-bond — held at the escrow gateway against the fulfiller's own
	// funds at claim time (anti-griefing: returned on success or honest
	// timeout, forfeited on rejected work to offset the poster's eval spend).
	if sb.Spec.Trust.SelfBond.Required && len(status.Claims) > 0 && status.BondState == "" {
		receipt, err := c.escrowGateway().Reserve(ctx, escrow.ReserveRequest{
			ID:      string(sb.UID) + "-bond",
			Network: sb.Spec.Reward.Network,
			PayTo:   status.Claims[0].FulfillerAddress,
			Asset:   sb.Spec.Trust.SelfBond.Token,
			Amount:  sb.Spec.Trust.SelfBond.Amount,
			Scheme:  sb.Spec.Reward.Escrow.Scheme,
		})
		if err != nil {
			if statusErr := c.updateBountyStatus(ctx, raw, status); statusErr != nil {
				return statusErr
			}
			return err // rate-limited retry
		}
		status.BondState = receipt.State
	}

	// 5. Submit — parse the submission annotation, advance the claim.
	if subRaw := strings.TrimSpace(annotations[bountySubmitAnnotation]); subRaw != "" && len(status.Claims) > 0 {
		var sub bountySubmission
		if err := json.Unmarshal([]byte(subRaw), &sub); err != nil {
			setPurchaseCondition(&status.Conditions, "Submitted", "False", "InvalidSubmission", truncateMessage(err.Error()))
		} else {
			if status.Claims[0].Phase == bountyPhaseClaimed {
				status.Claims[0].Phase = bountyPhaseSubmitted
			}
			status.ReportURI = sub.ReportURI
			setPurchaseCondition(&status.Conditions, "Submitted", "True", "Submitted",
				fmt.Sprintf("Result hash %s", sub.ResultHash))
		}
	} else if !bountyConditionIsTrue(status.Conditions, "Submitted") {
		setPurchaseCondition(&status.Conditions, "Submitted", "False", "AwaitingSubmission", "No submission yet")
	}

	// 5b. Eval market — verification-by-default: once a submission exists and
	// the bounty is not dangerously skipped (nor poster-manual), the
	// commit-reveal quorum drives Verified (reason=EvaluatorQuorum). The
	// poster verdict annotation below still overrides either way.
	if evalMarketActive(&sb) && bountyConditionIsTrue(status.Conditions, "Submitted") {
		if requeue := c.reconcileEvalMarket(ctx, &sb, annotations, &status, time.Now()); requeue > 0 {
			c.bountyQueue.AddAfter(key, requeue)
		}
	}

	// 6. Verdict — the poster verdict annotation. With the eval market active
	// it is an explicit OVERRIDE on top of (or instead of) the quorum; for
	// poster-manual or dangerously-skipped bounties it is the designed path.
	verdict := strings.TrimSpace(annotations[bountyVerdictAnnotation])
	quorumSpoke := conditionReason(status.Conditions, "Verified") == "EvaluatorQuorum"
	switch {
	case verdict == "accept" && bountyConditionIsTrue(status.Conditions, "Submitted"):
		reason := "PosterAccepted"
		if sb.Spec.Acceptance.Method != "poster-manual" && !bountyConditionIsTrue(status.Conditions, "Verified") {
			reason = "PosterOverride"
		}
		if !bountyConditionIsTrue(status.Conditions, "Verified") {
			setPurchaseCondition(&status.Conditions, "Verified", "True", reason, "Submission accepted by poster")
			status.WeightedScore = 100
		}
		if len(status.Claims) > 0 {
			status.Claims[0].Phase = bountyPhaseVerified
		}
	case strings.HasPrefix(verdict, "reject"):
		reason := strings.TrimPrefix(strings.TrimPrefix(verdict, "reject"), ":")
		if reason == "" {
			reason = "rejected by poster"
		}
		setPurchaseCondition(&status.Conditions, "Verified", "False", "PosterRejected", truncateMessage(reason))
		if len(status.Claims) > 0 {
			status.Claims[0].Phase = bountyPhaseRejected
		}
	case bountyConditionIsTrue(status.Conditions, "Submitted") && !bountyConditionIsTrue(status.Conditions, "Verified") && !quorumSpoke:
		setPurchaseCondition(&status.Conditions, "Verified", "False", "AwaitingVerdict",
			awaitingVerdictMessage(sb.Spec.Acceptance.Method, sb.Spec.Eval.Mode))
	case !bountyConditionIsTrue(status.Conditions, "Verified") && !quorumSpoke:
		setPurchaseCondition(&status.Conditions, "Verified", "False", "AwaitingSubmission", "No submission to verify")
	}

	// 6b. Self-bond settlement: returned on an accepted verdict, forfeited on
	// rejected work (poster or quorum). Deadline expiry returns it (honest
	// timeout) via refundBounty.
	if status.BondState == escrow.StateReserved {
		switch {
		case bountyConditionIsTrue(status.Conditions, "Verified"):
			if _, err := c.escrowGateway().Void(ctx, string(sb.UID)+"-bond"); err == nil {
				status.BondState = "Returned"
			}
		case len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseRejected:
			if _, err := c.escrowGateway().Capture(ctx, string(sb.UID)+"-bond"); err == nil {
				status.BondState = "Forfeited"
			}
		}
	}

	// 7. Payout — Verified + a held escrow → capture to the fulfiller.
	if bountyConditionIsTrue(status.Conditions, "Verified") && status.EscrowState == escrow.StateReserved {
		receipt, err := c.escrowGateway().Capture(ctx, string(sb.UID))
		if err != nil {
			setPurchaseCondition(&status.Conditions, "Paid", "False", "CaptureFailed", truncateMessage(err.Error()))
			if statusErr := c.updateBountyStatus(ctx, raw, status); statusErr != nil {
				return statusErr
			}
			return err // verified-but-unpaid is a retryable, worker-protecting state
		}
		status.EscrowState = receipt.State
		status.CaptureTxHash = receipt.TxHash
	}
	if status.EscrowState == escrow.StateCaptured {
		setPurchaseCondition(&status.Conditions, "Paid", "True", "Captured", "Reward released to fulfiller")
		if len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseVerified {
			status.Claims[0].Phase = bountyPhasePaid
		}
	} else if !bountyConditionIsTrue(status.Conditions, "Paid") {
		setPurchaseCondition(&status.Conditions, "Paid", "False", "AwaitingVerification", "Escrow capture follows an accepted verdict")
	}

	status.Phase = bountyPhaseRollup(status)
	return c.updateBountyStatus(ctx, raw, status)
}

// refundBounty voids the escrow and parks the bounty in Expired/Refunded.
// A held self-bond is returned — deadline expiry is an honest timeout, not
// rejected work.
func (c *Controller) refundBounty(ctx context.Context, raw *unstructured.Unstructured, sb *monetizeapi.ServiceBounty, status monetizeapi.ServiceBountyStatus, reason, message string) error {
	if status.BondState == escrow.StateReserved {
		if _, err := c.escrowGateway().Void(ctx, string(sb.UID)+"-bond"); err == nil {
			status.BondState = "Returned"
		}
	}
	if status.EvalBudgetState == escrow.StateReserved {
		if _, err := c.escrowGateway().Void(ctx, string(sb.UID)+"-eval"); err == nil {
			status.EvalBudgetState = escrow.StateVoided
		}
	}
	if status.EscrowState == escrow.StateReserved {
		receipt, err := c.escrowGateway().Void(ctx, string(sb.UID))
		if err != nil {
			setPurchaseCondition(&status.Conditions, "Paid", "False", "RefundFailed", truncateMessage(err.Error()))
			if statusErr := c.updateBountyStatus(ctx, raw, status); statusErr != nil {
				return statusErr
			}
			return err
		}
		status.EscrowState = receipt.State
		status.RefundTxHash = receipt.TxHash
	}
	setPurchaseCondition(&status.Conditions, "Verified", "False", reason, message)
	setPurchaseCondition(&status.Conditions, "Paid", "False", reason, "Escrow returned to poster")
	status.Phase = bountyPhaseRefunded
	if status.EscrowState == "" {
		status.Phase = bountyPhaseExpired
	}
	return c.updateBountyStatus(ctx, raw, status)
}

// bountyPhaseRollup derives the human phase from the condition machine truth.
func bountyPhaseRollup(status monetizeapi.ServiceBountyStatus) string {
	conditions := status.Conditions
	claimRejected := len(status.Claims) > 0 && status.Claims[0].Phase == bountyPhaseRejected
	switch {
	case bountyConditionIsTrue(conditions, "Paid"):
		return bountyPhasePaid
	case bountyConditionIsTrue(conditions, "Verified"):
		return bountyPhaseVerified
	case conditionReason(conditions, "Verified") == "PosterRejected" || claimRejected:
		return bountyPhaseRejected
	case bountyConditionIsTrue(conditions, "Submitted"):
		return bountyPhaseSubmitted
	case bountyConditionIsTrue(conditions, "Claimed"):
		return bountyPhaseClaimed
	default:
		return bountyPhaseOpen
	}
}

// validateBountyTask resolves spec.task.typeRef against the embedded registry
// and validates params + the reward envelope needed to construct the escrow.
// Admission is strict: a gate that silently accepts what it doesn't understand
// is not a gate (unknown params are typo'd intent, not extensibility).
func validateBountyTask(sb *monetizeapi.ServiceBounty) error {
	t, err := bounty.Resolve(sb.Spec.Task.TypeRef)
	if err != nil {
		return err
	}

	known := make(map[string]bool, len(t.Params))
	for _, p := range t.Params {
		known[p.Name] = true
	}
	for name := range sb.Spec.Task.Params {
		if !known[name] {
			return fmt.Errorf("unknown param %q for task type %s", name, t.Ref())
		}
	}

	for _, p := range t.Params {
		v := sb.Spec.Task.Params[p.Name]
		if p.Required && strings.TrimSpace(v) == "" {
			return fmt.Errorf("param %s is required for task type %s", p.Name, t.Ref())
		}
		if v == "" {
			continue
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, v) {
			return fmt.Errorf("param %s=%q is not one of [%s]", p.Name, v, strings.Join(p.Enum, ", "))
		}
	}

	// Single-winner guard: the controller admits one claim at a time. Honoring
	// >1 silently would promise a race/split semantic that does not exist yet.
	if sb.Spec.MaxFulfillers > 1 {
		return fmt.Errorf("maxFulfillers=%d is not supported yet — v1 bounties are single-winner", sb.Spec.MaxFulfillers)
	}

	if strings.TrimSpace(sb.Spec.Reward.Amount) == "" {
		return fmt.Errorf("reward.amount is required")
	}
	if strings.TrimSpace(sb.Spec.Reward.Network) == "" {
		return fmt.Errorf("reward.network is required to construct the escrow authorization")
	}

	return nil
}

func awaitingVerdictMessage(method, evalMode string) string {
	if method == "poster-manual" || evalMode == monetizeapi.EvalModeDangerouslySkipped {
		return "Awaiting poster verdict — accept with `obol bounty accept <name>`"
	}
	return fmt.Sprintf("Eval market for %s is not wired yet; poster may override with `obol bounty accept <name>`", method)
}

func bountyConditionIsTrue(conditions []monetizeapi.Condition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == "True"
		}
	}
	return false
}

func conditionReason(conditions []monetizeapi.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}

// newBountyEscrowGateway selects the escrow backend from controller-level
// configuration, NOT from spec.reward.escrow.facilitator: the gateway carries
// the controller's release-authority bearer token, and honoring an arbitrary
// per-bounty URL would let a poster exfiltrate that token to a server they
// control. The spec field stays advisory/documentary.
func newBountyEscrowGateway() escrow.Gateway {
	if base := strings.TrimSpace(os.Getenv("OBOL_BOUNTY_ESCROW_URL")); base != "" {
		return &escrow.HTTPGateway{
			Base:   base,
			Token:  strings.TrimSpace(os.Getenv("OBOL_BOUNTY_ESCROW_TOKEN")),
			Client: &http.Client{Timeout: 10 * time.Second},
		}
	}
	return escrow.NewLedgerGateway()
}

// defaultBountyLedger backs Controllers constructed without an explicit
// gateway (struct-literal tests); New() always sets bountyEscrow.
var defaultBountyLedger = escrow.NewLedgerGateway()

// escrowGateway returns the configured gateway, defaulting to the dev ledger.
// The dev ledger is escrow theater for local-first stacks — receipts are
// labeled dev-ledger and the EscrowReserved reason says so.
func (c *Controller) escrowGateway() escrow.Gateway {
	if c.bountyEscrow != nil {
		return c.bountyEscrow
	}
	return defaultBountyLedger
}

func escrowReason(g escrow.Gateway) string {
	if _, ok := g.(*escrow.LedgerGateway); ok {
		return "Reward hold recorded in dev ledger (no funds held — local dev mode)"
	}
	return "Reward authorization held at facilitator"
}

func (c *Controller) removeBountyFinalizer(ctx context.Context, raw *unstructured.Unstructured) error {
	patched := raw.DeepCopy()
	patched.SetFinalizers(slices.DeleteFunc(patched.GetFinalizers(), func(s string) bool { return s == serviceBountyFinalizer }))
	_, err := c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(patched.GetNamespace()).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

func (c *Controller) updateBountyStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.ServiceBountyStatus) error {
	patched := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObject) {
		return nil
	}
	patched.Object["status"] = statusObject
	_, err = c.dynClient.Resource(monetizeapi.ServiceBountyGVR).Namespace(patched.GetNamespace()).UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}
