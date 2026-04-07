package schemas

// WorkloadType discriminates between different types of compute services.
type WorkloadType string

const (
	// WorkloadInference is an LLM inference service (synchronous, per-request).
	WorkloadInference WorkloadType = "inference"

	// WorkloadFineTuning is a model fine-tuning service (batch, per-hour/epoch).
	WorkloadFineTuning WorkloadType = "fine-tuning"
)

// ServiceOfferSpec is the Go representation of a ServiceOffer CRD spec.
// Used by the CLI to build manifests and by Go-side reconciliation logic.
type ServiceOfferSpec struct {
	// Type discriminates the workload. Default: "inference".
	Type WorkloadType `json:"type,omitempty" yaml:"type,omitempty"`

	// Model holds LLM model metadata. Required for inference/fine-tuning.
	Model *ModelSpec `json:"model,omitempty" yaml:"model,omitempty"`

	// Upstream identifies the in-cluster service handling the workload.
	Upstream UpstreamSpec `json:"upstream" yaml:"upstream"`

	// Payment defines x402 payment terms. Field names align with x402.
	Payment PaymentTerms `json:"payment" yaml:"payment"`

	// Path is the URL path prefix for the HTTPRoute.
	// Defaults to /services/<name>.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	// Provenance tracks how the service or model was produced.
	Provenance map[string]string `json:"provenance,omitempty" yaml:"provenance,omitempty"`

	// Registration holds ERC-8004 registration metadata.
	Registration *RegistrationSpec `json:"registration,omitempty" yaml:"registration,omitempty"`
}

// ModelSpec describes the LLM model served by the upstream.
type ModelSpec struct {
	// Name is the model identifier (e.g., "qwen3.5:35b").
	Name string `json:"name" yaml:"name"`

	// Runtime is the serving runtime.
	Runtime string `json:"runtime" yaml:"runtime"`
}

// UpstreamSpec identifies the in-cluster Kubernetes Service.
type UpstreamSpec struct {
	// Service is the Kubernetes Service name.
	Service string `json:"service" yaml:"service"`

	// Namespace is the namespace of the upstream Service.
	Namespace string `json:"namespace" yaml:"namespace"`

	// Port is the port on the upstream Service.
	Port int `json:"port" yaml:"port"`

	// HealthPath is the HTTP path for health probes.
	HealthPath string `json:"healthPath,omitempty" yaml:"healthPath,omitempty"`
}

// ServiceOfferStatus is the Go representation of a ServiceOffer status.
type ServiceOfferStatus struct {
	Conditions         []Condition `json:"conditions,omitempty"         yaml:"conditions,omitempty"`
	Endpoint           string      `json:"endpoint,omitempty"           yaml:"endpoint,omitempty"`
	AgentID            string      `json:"agentId,omitempty"            yaml:"agentId,omitempty"`
	RegistrationTxHash string      `json:"registrationTxHash,omitempty" yaml:"registrationTxHash,omitempty"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty"`
}

// Condition represents a ServiceOffer status condition.
type Condition struct {
	Type               string `json:"type"                         yaml:"type"`
	Status             string `json:"status"                       yaml:"status"`
	Reason             string `json:"reason,omitempty"             yaml:"reason,omitempty"`
	Message            string `json:"message,omitempty"            yaml:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty" yaml:"lastTransitionTime,omitempty"`
}
