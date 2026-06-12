package monetizeapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// EvalCommitHash binds an evaluator's score commitment to their address:
// sha256("<score>|<salt>|<lowercase address>"). The address inside the
// preimage means a commitment cannot be copied by another evaluator and
// replayed with the original's revealed {score, salt} (Kleros whitepaper
// §4.3). CLI and controller MUST compute this identically.
func EvalCommitHash(score int64, salt, address string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%s|%s", score, salt, strings.ToLower(address)))
	return "0x" + hex.EncodeToString(sum[:])
}

// ── ServiceBounty ───────────────────────────────────────────────────────────
//
// ServiceBounty is the demand-side inverse of a ServiceOffer. A ServiceOffer is
// standing supply that converges to one live route and stays up; a ServiceBounty
// is time-boxed demand that converges to one paid deliverable and closes. Both
// share the same money rail (x402), identity rail (ERC-8004), and controller
// plumbing, run in opposite directions.
//
// Task semantics are deliberately NOT hardcoded in this CRD. spec.task.typeRef
// points at an embedded, versioned task-type package (internal/embed/bountytasks,
// e.g. "benchmark@v1") that owns the param schema, the eval method + tolerance,
// the OBOL eval pricing, the hardware-proof policy, and the A2UI report schema.
// New task types drop in as data — the CRD and controller never change.
//
// Verification is reputation-graded with NO validator set and NO slashing: the
// escrow releases on an accepted, ERC-8004-reputation-weighted verdict produced
// by an OBOL-paid evaluation market. See plans/bounty-ane-marketplace-design.md.

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=sb
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Task",type=string,JSONPath=`.spec.task.typeRef`
// +kubebuilder:printcolumn:name="Reward",type=string,JSONPath=`.spec.reward.amount`
// +kubebuilder:printcolumn:name="Verification",type=string,JSONPath=`.spec.eval.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceBounty declares a unit of paid work (benchmark, fine-tune, serve, …)
// with an escrowed reward released on an accepted verdict.
type ServiceBounty struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceBountySpec   `json:"spec,omitempty"`
	Status            ServiceBountyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceBountyList is the list form for kubectl/list operations.
type ServiceBountyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceBounty `json:"items"`
}

type ServiceBountySpec struct {
	// Task describes the work. spec.task.typeRef selects an embedded,
	// versioned task-type package; spec.task.params is validated against
	// that package's schema at admission.
	// +kubebuilder:validation:Required
	Task ServiceBountyTask `json:"task"`

	// Acceptance is how a submission is judged. Defaults come from the task
	// type; the poster may tighten them.
	Acceptance ServiceBountyAcceptance `json:"acceptance,omitempty"`

	// Reward is the escrowed payment released to the fulfiller on acceptance.
	// +kubebuilder:validation:Required
	Reward ServiceBountyReward `json:"reward"`

	// Eval configures the OBOL-paid evaluation market (a SEPARATE payment leg
	// from the reward — x402 cannot splice a fee out of the reward auth).
	Eval ServiceBountyEval `json:"eval,omitempty"`

	// Trust selects the reputation gate + optional refundable self-bond. No
	// validator stake, no slashing — reputation (lost future income) is the
	// only collateral.
	Trust ServiceBountyTrust `json:"trust,omitempty"`

	// Deadline: past it with no accepted verdict → Expired → Refunded.
	Deadline *metav1.Time `json:"deadline,omitempty"`

	// MaxFulfillers: 1 = single-winner (default); >1 = first-N-valid paid.
	// +kubebuilder:default=1
	MaxFulfillers int64 `json:"maxFulfillers,omitempty"`
}

// ServiceBountyTask carries the task-type reference + opaque params. The
// controller never interprets params beyond validating them against the
// resolved task-type schema.
type ServiceBountyTask struct {
	// TypeRef resolves an embedded task-type package, e.g. "benchmark@v1".
	// +kubebuilder:validation:Required
	TypeRef string `json:"typeRef"`

	// Free-form knobs validated against the task type's param schema.
	Params map[string]string `json:"params,omitempty"`

	// Target model metadata (reuses ServiceOffer's model shape).
	TargetModel ServiceOfferModel `json:"targetModel,omitempty"`

	// DatasetCommit pins the eval dataset (committed root + the fraction kept
	// private so a public re-run can't leak answers / enable train-on-test).
	DatasetCommit ServiceBountyDatasetCommit `json:"datasetCommit,omitempty"`

	// HardwareProof strength required of the fulfiller. self-report is a
	// reputation-backed claim (forgeable); gpu-attestation is cryptographic
	// (NVIDIA CC / enclave-binding); evaluator-measured moves the throughput
	// measurement onto attested evaluator hardware.
	// +kubebuilder:validation:Enum=self-report;gpu-attestation;evaluator-measured
	HardwareProof string `json:"hardwareProof,omitempty"`
}

type ServiceBountyDatasetCommit struct {
	// Root is a Merkle root committing the (partially private) eval dataset.
	Root string `json:"root,omitempty"`
	// PrivateFraction (0..1, as a string to keep schema stable) of rows kept
	// secret and revealed only to sampled evaluators at eval time.
	PrivateFraction string `json:"privateFraction,omitempty"`
}

type ServiceBountyAcceptance struct {
	// Method judges a submission. Benchmarks are NOT bit-exact: rerun-tolerance
	// re-runs the harness and accepts a score within tolerance. The commitHash
	// is integrity (anti bait-and-switch), not a determinism gate.
	// +kubebuilder:validation:Enum=rerun-tolerance;harness-rerun;sla-probe;poster-manual
	Method string `json:"method,omitempty"`

	// Tolerance per metric (e.g. {"mmlu":"0.01"}). Default from the task type.
	Tolerance map[string]string `json:"tolerance,omitempty"`

	// CommitReveal requires evaluators to commit then reveal scores, so they
	// can't pre-agree on a number.
	CommitReveal bool `json:"commitReveal,omitempty"`
}

// ServiceBountyReward mirrors the ServiceOfferPayment envelope (network +
// payTo + asset) so buy/sell/bounty all read the same way, plus the amount and
// the escrow rail. Network + PayTo are required to construct the upto
// authorization: the chain it settles on and the poster's refund address.
type ServiceBountyReward struct {
	// Payment network (e.g. "base", "base-sepolia").
	Network string `json:"network,omitempty"`

	// PayTo is the poster's address: the escrow-return / refund destination.
	// The fulfiller payout address is bound at claim time (witness.to in the
	// upto auth), not here.
	// +kubebuilder:validation:Pattern=`^0x[a-fA-F0-9]{40}$`
	PayTo string `json:"payTo,omitempty"`

	// Asset reuses ServiceOffer's asset shape (USDC eip3009 / OBOL permit2).
	Asset ServiceOfferAsset `json:"asset,omitempty"`

	// Amount is the lump-sum reward (human units, e.g. "500.00").
	Amount string `json:"amount,omitempty"`

	// Escrow selects the x402 settlement rail + reputation-driven mode.
	Escrow ServiceBountyEscrow `json:"escrow,omitempty"`
}

type ServiceBountyEscrow struct {
	// Scheme: 'upto' (live: facilitator holds a recipient-bound auth, settles
	// ≤ max) or 'authCapture' (funds-locked, used above valueCap once the Go
	// impl lands — x402-foundation/x402#2298).
	// +kubebuilder:validation:Enum=upto;authCapture
	Scheme string `json:"scheme,omitempty"`

	// Facilitator URL (our own facilitator acts as the bounded settlement
	// trigger; payTo is signed into the auth so it can never redirect funds).
	Facilitator string `json:"facilitator,omitempty"`

	// Mode is selected by the fulfiller's reputation: 'auto' (optimistic),
	// 'facilitator-check' (deterministic re-run), 'onchain-lock' (authCapture).
	// +kubebuilder:validation:Enum=auto;facilitator-check;onchain-lock
	Mode string `json:"mode,omitempty"`

	// ValueCapMicros: above this the escrow must use an on-chain lock.
	ValueCapMicros string `json:"valueCapMicros,omitempty"`
}

// Eval verification modes. Verification is ON by default; skipping is an
// explicit, labeled act (--dangerously-skip-verification) — a skipped bounty
// emits no ERC-8004 validation entries and its reputation feedback is
// suppressed, so it can never be farmed for reputation.
const (
	EvalModeRequired           = "required"
	EvalModeDangerouslySkipped = "dangerouslySkipped"
)

// ServiceBountyEval is the OBOL-paid evaluation market. Evaluators are paid for
// the WORK (pass or fail), selected by reputation (not stake), and paid in OBOL
// by default via x402 batch-settlement.
type ServiceBountyEval struct {
	// K evaluators: median-of-k quorum; k≥3 whenever a probation seat is
	// occupied (the median absorbs one outlier).
	// +kubebuilder:default=1
	K int64 `json:"k,omitempty"`

	// Mode gates verification. 'required' (default) routes acceptance through
	// the evaluator quorum once the eval market is wired — until then a poster
	// verdict is recorded as PosterOverride. 'dangerouslySkipped' declares
	// poster-as-judge up front: same override path, but the bounty is marked
	// unverified and produces no reputation signal.
	// +kubebuilder:default="required"
	// +kubebuilder:validation:Enum=required;dangerouslySkipped
	Mode string `json:"mode,omitempty"`

	// Selection: VRF-sampled after submission, reputation-weighted; the poster
	// cannot hand-pick.
	// +kubebuilder:validation:Enum=vrf-reputation-weighted;poster-manual
	Selection string `json:"selection,omitempty"`

	// Payment for evaluators — a separate leg from the reward.
	Payment ServiceBountyEvalPayment `json:"payment,omitempty"`
}

type ServiceBountyEvalPayment struct {
	// Asset defaults to OBOL (verification is an OBOL utility sink).
	// +kubebuilder:default="OBOL"
	Asset string `json:"asset,omitempty"`

	// PerEvaluator fee (human units).
	PerEvaluator string `json:"perEvaluator,omitempty"`

	// FundedBy: 'poster' (separate poster-funded eval budget).
	// +kubebuilder:default="poster"
	FundedBy string `json:"fundedBy,omitempty"`

	// Settle: 'batch-settlement' pays all K evaluators in one tx.
	// +kubebuilder:default="batch-settlement"
	Settle string `json:"settle,omitempty"`
}

type ServiceBountyTrust struct {
	// ReputationGate derives the fulfiller's maxBountyValue from ERC-8004
	// getSummary (read with a curated, trusted client filter).
	ReputationGate bool `json:"reputationGate,omitempty"`

	// SelfBond is an OPTIONAL refundable bond the fulfiller posts from their
	// OWN funds (returned on success). It is never slashed to a validator set.
	SelfBond ServiceBountySelfBond `json:"selfBond,omitempty"`
}

type ServiceBountySelfBond struct {
	Required bool   `json:"required,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Token    string `json:"token,omitempty"`
}

// ServiceBountyStatus mirrors the AND-rollup condition idiom used by
// ServiceOffer. Machine truth is the condition set; Phase is the human rollup.
type ServiceBountyStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              string      `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`

	// EscrowState: Reserved | Captured | Voided (held auth at the facilitator).
	EscrowState string `json:"escrowState,omitempty"`

	// WeightedScore is the reputation-weighted eval verdict (0-100).
	WeightedScore int64 `json:"weightedScore,omitempty"`

	// CaptureTxHash / RefundTxHash record the settled reward or refund.
	CaptureTxHash string `json:"captureTxHash,omitempty"`
	RefundTxHash  string `json:"refundTxHash,omitempty"`

	// ReportURI points at the SIWx/local-gated A2UI report (deliverable).
	ReportURI string `json:"reportURI,omitempty"`

	// Claims are observed fulfiller bindings (single-winner is the common case,
	// so claims live in status, not a separate CR).
	Claims []ServiceBountyClaim `json:"claims,omitempty"`

	// EvaluatorPanel is the controller-selected seat assignment (deterministic
	// per-bounty sampling from enrolled evaluators). Empty panel = open-door
	// fallback (insufficient pool) — any address may evaluate, as in early v1.
	EvaluatorPanel []ServiceBountyPanelSeat `json:"evaluatorPanel,omitempty"`

	// Evaluations are the eval-market verdicts promoted from the
	// obol.org/eval-commit-<addr> / eval-reveal-<addr> annotation channel.
	Evaluations []ServiceBountyEvaluation `json:"evaluations,omitempty"`

	// EvalBudgetState tracks the poster-funded OBOL eval budget
	// (k × perEvaluator) at the escrow gateway: Reserved | Captured | Voided.
	// Evaluators are paid for the WORK, pass or fail.
	EvalBudgetState string `json:"evalBudgetState,omitempty"`

	// EvalPayoutTxHash records the batch-settlement receipt for the eval leg.
	EvalPayoutTxHash string `json:"evalPayoutTxHash,omitempty"`

	// LadderRecorded latches the one-shot cross-bounty ladder bookkeeping so
	// repeated reconciles after quorum never double-count.
	LadderRecorded bool `json:"ladderRecorded,omitempty"`

	// RevealDeadline opens once K commitments are in: every commit closes
	// before any reveal opens, and a missing reveal past this instant is
	// graded as a worst-case outlier (nonRevealPenalty).
	RevealDeadline *metav1.Time `json:"revealDeadline,omitempty"`

	// BondState tracks the fulfiller self-bond at the escrow gateway:
	// Reserved | Returned (success/honest timeout) | Forfeited (rejected work,
	// offsets the poster's burned eval budget).
	BondState string `json:"bondState,omitempty"`

	// PanelSeed records the randomness source the evaluator panel was drawn
	// from, so the sampling is auditable (drand round, raw randomness, sig).
	PanelSeed *ServiceBountyPanelSeed `json:"panelSeed,omitempty"`

	// Escalation is the second-round eval state opened when the first-round
	// quorum diverges beyond the task's escalation epsilon.
	Escalation *ServiceBountyEscalation `json:"escalation,omitempty"`

	// EscrowSpender is the facilitator address Permit2 vouchers must name as
	// the only executor (Receipt.Spender echoed into status for signers).
	EscrowSpender string `json:"escrowSpender,omitempty"`
}

// ServiceBountyPanelSeed is the auditable randomness behind a panel draw.
type ServiceBountyPanelSeed struct {
	// Source names the randomness origin (e.g. drand, local-dev).
	Source string `json:"source"`

	// Round is the drand round the randomness came from.
	Round uint64 `json:"round,omitempty"`

	// Randomness is the beacon output the panel sampling was keyed on.
	Randomness string `json:"randomness,omitempty"`

	// Signature is the beacon signature proving the randomness.
	Signature string `json:"signature,omitempty"`
}

// ServiceBountyEscalation is one escalation round: a fresh, larger panel
// re-evaluates the same submission with its own commit-reveal cycle and its
// own eval budget.
type ServiceBountyEscalation struct {
	// Round is the escalation round number (1 = first escalation).
	Round int `json:"round"`

	// Reason records why the escalation opened (e.g. quorum divergence).
	Reason string `json:"reason,omitempty"`

	// Panel is the escalation-round seat assignment.
	Panel []ServiceBountyPanelSeat `json:"panel,omitempty"`

	// Evaluations are the escalation round's commit-reveal records.
	Evaluations []ServiceBountyEvaluation `json:"evaluations,omitempty"`

	// RevealDeadline is the escalation round's commit→reveal cutoff.
	RevealDeadline *metav1.Time `json:"revealDeadline,omitempty"`

	// VoucherDeadline is when the escalation eval-budget voucher expires.
	VoucherDeadline *metav1.Time `json:"voucherDeadline,omitempty"`

	// BudgetState tracks the escalation eval budget at the escrow gateway:
	// Reserved | Captured | Voided.
	BudgetState string `json:"budgetState,omitempty"`
}

// Panel seat kinds (design doc §11.4): full and probation seats count in the
// median-of-k quorum; shadow seats are graded against the median but never
// counted (the free reputation on-ramp).
const (
	PanelSeatFull      = "full"
	PanelSeatProbation = "probation"
	PanelSeatShadow    = "shadow"
)

// ServiceBountyPanelSeat is one selected evaluator seat.
type ServiceBountyPanelSeat struct {
	// Address is the enrolled evaluator's address.
	Address string `json:"address,omitempty"`

	// Seat: full | probation | shadow.
	Seat string `json:"seat,omitempty"`
}

// ServiceBountyEvaluation is one evaluator's commit-reveal record. WithinBand
// is the per-bounty ladder bookkeeping hook: divergence from the quorum median
// (or a missing/invalid reveal) is what future reputation feedback keys on.
type ServiceBountyEvaluation struct {
	// Address is the evaluator's payout/identity address (annotation key suffix).
	Address string `json:"address,omitempty"`

	// CommitHash = EvalCommitHash(score, salt, address), promoted first-write-wins.
	CommitHash string `json:"commitHash,omitempty"`

	// Score is the revealed 0-100 verdict (ERC-8004 validationResponse semantics).
	Score int64 `json:"score,omitempty"`

	// RevealedAt records when a valid reveal was promoted.
	RevealedAt *metav1.Time `json:"revealedAt,omitempty"`

	// WithinBand is false for NonReveal/BadReveal and for revealed scores
	// outside the outlier band around the quorum median.
	WithinBand bool `json:"withinBand,omitempty"`

	// Phase: Committed | Revealed | BadReveal | NonReveal.
	Phase string `json:"phase,omitempty"`

	// Seat mirrors the panel seat kind (full | probation | shadow); empty in
	// open-door mode.
	Seat string `json:"seat,omitempty"`

	// Paid marks inclusion in the eval-budget batch settlement (counting
	// seats that revealed validly; shadows evaluate free).
	Paid bool `json:"paid,omitempty"`

	// ValidationTxHash is the evaluator-submitted ERC-8004 validationResponse
	// transaction, recorded as provenance (the evaluator's OWN wallet signs;
	// the controller never does).
	ValidationTxHash string `json:"validationTxHash,omitempty"`

	// Grounded marks a verdict backed by an on-chain ERC-8004 validation
	// entry observed for this bounty's eval-request hash — the chain-anchored
	// reputation signal, as opposed to an annotation-only reveal.
	Grounded bool `json:"grounded,omitempty"`
}

type ServiceBountyClaim struct {
	FulfillerAddress string       `json:"fulfillerAddress,omitempty"`
	ClaimedAt        *metav1.Time `json:"claimedAt,omitempty"`
	// CommitHash binds the worker to a specific model + outputs (anti
	// bait-and-switch), revealed at submit.
	CommitHash string `json:"commitHash,omitempty"`
	// Phase: Claimed | Submitted | Verified | Rejected.
	Phase string `json:"phase,omitempty"`
}

// ── deepcopy (hand-written to match controller-gen idioms in
//    zz_generated.deepcopy.go; the Reward/Eval/Trust sub-trees are pure value
//    structs so the shallow `*out = *in` is already a deep copy for them) ─────

func (in *ServiceBounty) DeepCopyInto(out *ServiceBounty) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *ServiceBounty) DeepCopy() *ServiceBounty {
	if in == nil {
		return nil
	}
	out := new(ServiceBounty)
	in.DeepCopyInto(out)
	return out
}

func (in *ServiceBounty) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ServiceBountyList) DeepCopyInto(out *ServiceBountyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		l, m := &in.Items, &out.Items
		*m = make([]ServiceBounty, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
}

func (in *ServiceBountyList) DeepCopy() *ServiceBountyList {
	if in == nil {
		return nil
	}
	out := new(ServiceBountyList)
	in.DeepCopyInto(out)
	return out
}

func (in *ServiceBountyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ServiceBountySpec) DeepCopyInto(out *ServiceBountySpec) {
	*out = *in
	in.Task.DeepCopyInto(&out.Task)
	in.Acceptance.DeepCopyInto(&out.Acceptance)
	out.Reward = in.Reward
	out.Eval = in.Eval
	out.Trust = in.Trust
	if in.Deadline != nil {
		l, m := &in.Deadline, &out.Deadline
		*m = (*l).DeepCopy()
	}
}

func (in *ServiceBountyTask) DeepCopyInto(out *ServiceBountyTask) {
	*out = *in
	if in.Params != nil {
		out.Params = make(map[string]string, len(in.Params))
		for k, v := range in.Params {
			out.Params[k] = v
		}
	}
	out.TargetModel = in.TargetModel
	out.DatasetCommit = in.DatasetCommit
}

func (in *ServiceBountyAcceptance) DeepCopyInto(out *ServiceBountyAcceptance) {
	*out = *in
	if in.Tolerance != nil {
		out.Tolerance = make(map[string]string, len(in.Tolerance))
		for k, v := range in.Tolerance {
			out.Tolerance[k] = v
		}
	}
}

func (in *ServiceBountyStatus) DeepCopyInto(out *ServiceBountyStatus) {
	*out = *in
	if in.Conditions != nil {
		l, m := &in.Conditions, &out.Conditions
		*m = make([]Condition, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
	if in.Claims != nil {
		l, m := &in.Claims, &out.Claims
		*m = make([]ServiceBountyClaim, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
	if in.EvaluatorPanel != nil {
		out.EvaluatorPanel = make([]ServiceBountyPanelSeat, len(in.EvaluatorPanel))
		copy(out.EvaluatorPanel, in.EvaluatorPanel)
	}
	if in.Evaluations != nil {
		l, m := &in.Evaluations, &out.Evaluations
		*m = make([]ServiceBountyEvaluation, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
	if in.RevealDeadline != nil {
		l, m := &in.RevealDeadline, &out.RevealDeadline
		*m = (*l).DeepCopy()
	}
	if in.PanelSeed != nil {
		out.PanelSeed = new(ServiceBountyPanelSeed)
		*out.PanelSeed = *in.PanelSeed
	}
	if in.Escalation != nil {
		out.Escalation = new(ServiceBountyEscalation)
		in.Escalation.DeepCopyInto(out.Escalation)
	}
}

func (in *ServiceBountyEscalation) DeepCopyInto(out *ServiceBountyEscalation) {
	*out = *in
	if in.Panel != nil {
		out.Panel = make([]ServiceBountyPanelSeat, len(in.Panel))
		copy(out.Panel, in.Panel)
	}
	if in.Evaluations != nil {
		l, m := &in.Evaluations, &out.Evaluations
		*m = make([]ServiceBountyEvaluation, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
	if in.RevealDeadline != nil {
		l, m := &in.RevealDeadline, &out.RevealDeadline
		*m = (*l).DeepCopy()
	}
	if in.VoucherDeadline != nil {
		l, m := &in.VoucherDeadline, &out.VoucherDeadline
		*m = (*l).DeepCopy()
	}
}

func (in *ServiceBountyEvaluation) DeepCopyInto(out *ServiceBountyEvaluation) {
	*out = *in
	if in.RevealedAt != nil {
		l, m := &in.RevealedAt, &out.RevealedAt
		*m = (*l).DeepCopy()
	}
}

func (in *ServiceBountyClaim) DeepCopyInto(out *ServiceBountyClaim) {
	*out = *in
	if in.ClaimedAt != nil {
		l, m := &in.ClaimedAt, &out.ClaimedAt
		*m = (*l).DeepCopy()
	}
}
