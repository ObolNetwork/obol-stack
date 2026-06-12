package monetizeapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// ── EvaluatorEnrollment ─────────────────────────────────────────────────────
//
// EvaluatorEnrollment is an evaluator's opt-in to the OBOL eval market: an
// address + the task types it can re-run + an optional device attestation.
// The spec is evaluator-written; the LADDER STATE in status is controller-
// owned (Shadow → Probation → Full, per task type — design doc §11.4). No
// staking: the only collateral is the future income a reputation earns.

// Evaluator ladder tiers.
const (
	EvaluatorTierShadow    = "Shadow"
	EvaluatorTierProbation = "Probation"
	EvaluatorTierFull      = "Full"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ee
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EvaluatorEnrollment opts an evaluator into the eval market.
type EvaluatorEnrollment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EvaluatorEnrollmentSpec   `json:"spec,omitempty"`
	Status            EvaluatorEnrollmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EvaluatorEnrollmentList is the list form.
type EvaluatorEnrollmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvaluatorEnrollment `json:"items"`
}

type EvaluatorEnrollmentSpec struct {
	// Address is the evaluator's payout/identity address — the same address
	// used in eval-commit/eval-reveal annotations and bound into commitments.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^0x[a-fA-F0-9]{40}$`
	Address string `json:"address"`

	// TaskTypes this evaluator can re-run (versioned refs, e.g. benchmark@v1).
	// +kubebuilder:validation:Required
	TaskTypes []string `json:"taskTypes"`

	// Attestation is the device-binding claim. v1 RECORDS it (sybil cost is
	// real hardware per identity once verification lands with the Secure
	// Enclave wiring); scheme "none" is honest-unattested.
	Attestation EvaluatorAttestation `json:"attestation,omitempty"`
}

type EvaluatorAttestation struct {
	// Scheme: none (unattested) | secure-enclave (device-bound P-256 key).
	// +kubebuilder:validation:Enum=none;secure-enclave
	Scheme string `json:"scheme,omitempty"`

	// PublicKey is the attestation public key (secure-enclave scheme).
	PublicKey string `json:"publicKey,omitempty"`

	// Signature is the enrollment signature over the address (scheme-defined).
	Signature string `json:"signature,omitempty"`
}

// EvaluatorEnrollmentStatus is controller-owned ladder state.
type EvaluatorEnrollmentStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Records hold per-task-type ladder progress (reputation is per task
	// type — benchmark@v1 rep says nothing about finetune@v1).
	Records []EvaluatorLadderRecord `json:"records,omitempty"`
}

// EvaluatorLadderRecord is one task type's ladder progress.
type EvaluatorLadderRecord struct {
	TaskType string `json:"taskType,omitempty"`

	// Tier: Shadow | Probation | Full. New enrollments start Shadow.
	Tier string `json:"tier,omitempty"`

	// ShadowAgreements counts shadow verdicts within tolerance of the quorum
	// median (promotion to Probation at the task package's threshold).
	ShadowAgreements int64 `json:"shadowAgreements,omitempty"`

	// ProbationEvals counts paid in-band evals while on Probation (promotion
	// to Full at the package threshold).
	ProbationEvals int64 `json:"probationEvals,omitempty"`

	// Completed counts all settled panel seats (any tier).
	Completed int64 `json:"completed,omitempty"`

	// Divergences counts settled seats graded out of band (incl. non/bad
	// reveals) — the negative reputation signal.
	Divergences int64 `json:"divergences,omitempty"`

	// RecentFulfillers are the last few fulfiller addresses this evaluator
	// judged — the pair-diversity rule down-weights repeat pairings.
	RecentFulfillers []string `json:"recentFulfillers,omitempty"`

	// LastEvalAt is when this evaluator's most recent seat settled — the
	// anchor for reputation decay (decayHalfLife).
	LastEvalAt *metav1.Time `json:"lastEvalAt,omitempty"`

	// GroundedEvals counts settled seats whose verdict was grounded by an
	// on-chain ERC-8004 validation entry.
	GroundedEvals int `json:"groundedEvals,omitempty"`
}

// ── deepcopy (hand-written, matching the package idiom) ─────────────────────

func (in *EvaluatorEnrollment) DeepCopyInto(out *EvaluatorEnrollment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *EvaluatorEnrollment) DeepCopy() *EvaluatorEnrollment {
	if in == nil {
		return nil
	}
	out := new(EvaluatorEnrollment)
	in.DeepCopyInto(out)
	return out
}

func (in *EvaluatorEnrollment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *EvaluatorEnrollmentList) DeepCopyInto(out *EvaluatorEnrollmentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		l, m := &in.Items, &out.Items
		*m = make([]EvaluatorEnrollment, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
}

func (in *EvaluatorEnrollmentList) DeepCopy() *EvaluatorEnrollmentList {
	if in == nil {
		return nil
	}
	out := new(EvaluatorEnrollmentList)
	in.DeepCopyInto(out)
	return out
}

func (in *EvaluatorEnrollmentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *EvaluatorEnrollmentSpec) DeepCopyInto(out *EvaluatorEnrollmentSpec) {
	*out = *in
	if in.TaskTypes != nil {
		out.TaskTypes = make([]string, len(in.TaskTypes))
		copy(out.TaskTypes, in.TaskTypes)
	}
	out.Attestation = in.Attestation
}

func (in *EvaluatorEnrollmentStatus) DeepCopyInto(out *EvaluatorEnrollmentStatus) {
	*out = *in
	if in.Records != nil {
		l, m := &in.Records, &out.Records
		*m = make([]EvaluatorLadderRecord, len(*l))
		for i := range *l {
			(*l)[i].DeepCopyInto(&(*m)[i])
		}
	}
}

func (in *EvaluatorLadderRecord) DeepCopyInto(out *EvaluatorLadderRecord) {
	*out = *in
	if in.RecentFulfillers != nil {
		out.RecentFulfillers = make([]string, len(in.RecentFulfillers))
		copy(out.RecentFulfillers, in.RecentFulfillers)
	}
	if in.LastEvalAt != nil {
		l, m := &in.LastEvalAt, &out.LastEvalAt
		*m = (*l).DeepCopy()
	}
}
