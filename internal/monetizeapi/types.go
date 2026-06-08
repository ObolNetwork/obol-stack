package monetizeapi

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// DefaultDrainGracePeriod is the grace period applied to a draining
// ServiceOffer when spec.drainGracePeriod is unset. Buyers using the
// offer can complete in-flight payments and migrate to alternative
// providers within this window before the HTTPRoute is torn down.
const DefaultDrainGracePeriod = time.Hour

const (
	Group   = "obol.org"
	Version = "v1alpha1"

	ServiceOfferKind        = "ServiceOffer"
	RegistrationRequestKind = "RegistrationRequest"
	PurchaseRequestKind     = "PurchaseRequest"
	AgentKind               = "Agent"
	AgentIdentityKind       = "AgentIdentity"

	ServiceOfferResource        = "serviceoffers"
	RegistrationRequestResource = "registrationrequests"
	PurchaseRequestResource     = "purchaserequests"
	AgentResource               = "agents"
	AgentIdentityResource       = "agentidentities"

	// Default identity used for the operator's public ERC-8004 registration
	// file. The registration file can contain multiple per-chain registrations.
	AgentIdentityDefaultNamespace = "x402"
	AgentIdentityDefaultName      = "default"

	AgentRuntimeHermes = "hermes"

	AgentPhasePending      = "Pending"
	AgentPhaseProvisioning = "Provisioning"
	AgentPhaseReady        = "Ready"
	AgentPhaseFailed       = "Failed"
)

var (
	ServiceOfferGVR        = schema.GroupVersionResource{Group: Group, Version: Version, Resource: ServiceOfferResource}
	RegistrationRequestGVR = schema.GroupVersionResource{Group: Group, Version: Version, Resource: RegistrationRequestResource}
	PurchaseRequestGVR     = schema.GroupVersionResource{Group: Group, Version: Version, Resource: PurchaseRequestResource}
	AgentGVR               = schema.GroupVersionResource{Group: Group, Version: Version, Resource: AgentResource}
	AgentIdentityGVR       = schema.GroupVersionResource{Group: Group, Version: Version, Resource: AgentIdentityResource}

	ServiceGVR        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	SecretGVR         = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	ConfigMapGVR      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	DeploymentGVR     = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	MiddlewareGVR     = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}
	HTTPRouteGVR      = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	ReferenceGrantGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "referencegrants"}
	// Used by the agent reconciler when provisioning per-namespace
	// runtime primitives. Keeping them next to the existing GVRs avoids
	// scattering schema.GroupVersionResource literals across the package.
	NamespaceGVR      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	ServiceAccountGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
	PVCGVR            = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}
)

// ── ServiceOffer ────────────────────────────────────────────────────────────

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=so
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model.name`
// +kubebuilder:printcolumn:name="Price",type=string,JSONPath=`.spec.payment.price.perRequest`
// +kubebuilder:printcolumn:name="Network",type=string,JSONPath=`.spec.payment.network`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceOffer declares a compute service that can be exposed publicly,
// gated with x402 payments, and optionally registered on an ERC-8004
// service registry. Field names align with x402 and ERC-8004 standards.
type ServiceOffer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceOfferSpec   `json:"spec,omitempty"`
	Status            ServiceOfferStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceOfferList is the list form for kubectl/list operations.
type ServiceOfferList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceOffer `json:"items"`
}

type ServiceOfferSpec struct {
	// Service type. 'inference' enables model management; 'http' for any HTTP
	// service; 'agent' references an Agent CR via spec.agent.ref and the
	// controller derives upstream + model + skills from the agent's status.
	// +kubebuilder:default="http"
	// +kubebuilder:validation:Enum=inference;fine-tuning;http;agent
	Type string `json:"type,omitempty"`

	// Required when type='agent'. The controller resolves spec.agent.ref to
	// the referenced Agent CR, derives upstream from Agent.status.endpoint,
	// and surfaces the agent's pinned model + skills in the 402 response.
	Agent ServiceOfferAgent `json:"agent,omitempty"`

	// LLM model metadata. Required when the upstream serves an LLM.
	Model ServiceOfferModel `json:"model,omitempty"`

	// In-cluster service that handles the actual workload.
	Upstream ServiceOfferUpstream `json:"upstream,omitempty"`

	// +kubebuilder:validation:Required
	Payment ServiceOfferPayment `json:"payment"`

	// URL path prefix for the HTTPRoute, defaults to /services/<name>.
	// +kubebuilder:validation:Pattern=`^/[a-zA-Z0-9/_.-]*$`
	Path string `json:"path,omitempty"`

	// Optional provenance metadata for the service. Tracks how the model or
	// service was produced (e.g. autoresearch experiment data). Included in
	// the ERC-8004 registration document when present.
	Provenance map[string]string `json:"provenance,omitempty"`

	// ERC-8004 registration metadata. Field names align with the
	// AgentRegistration document schema (ERC-8004 spec).
	Registration ServiceOfferRegistration `json:"registration,omitempty"`

	// DrainAt marks the offer as draining when non-nil. While the offer
	// is in the drain window, discovery surfaces (/skill.md and
	// /.well-known/agent-registration.json) advertise the offer with
	// available=false and drainEndsAt set, so buyers can migrate before
	// the route is torn down. The route + payment gate stay up until
	// DrainEndsAt() so in-flight payments can complete. Replaces the
	// legacy obol.org/paused annotation.
	DrainAt *metav1.Time `json:"drainAt,omitempty"`

	// DrainGracePeriod is how long after DrainAt the HTTPRoute remains
	// up. Defaults to DefaultDrainGracePeriod when nil. A zero duration
	// is honored as "tear down immediately on the next reconcile" (the
	// equivalent of `obol sell stop --force`).
	DrainGracePeriod *metav1.Duration `json:"drainGracePeriod,omitempty"`
}

// ServiceOfferAgent is populated when Spec.Type == "agent". The controller
// resolves Ref → Agent CR, derives Upstream from Agent.status.endpoint, and
// surfaces the agent's model + skills in the 402 response's extra block so
// buyers see what they're paying for.
type ServiceOfferAgent struct {
	Ref ServiceOfferAgentRef `json:"ref,omitempty"`
}

type ServiceOfferAgentRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

type ServiceOfferModel struct {
	// Model identifier (e.g. qwen3.5:35b).
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Runtime serving the model.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ollama;vllm;tgi
	Runtime string `json:"runtime"`
}

type ServiceOfferUpstream struct {
	// Kubernetes Service name.
	// +kubebuilder:validation:Required
	Service string `json:"service"`
	// Namespace of the upstream Service.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	// Port on the upstream Service.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=11434
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int64 `json:"port"`
	// HTTP path used for health probes against the upstream.
	// +kubebuilder:default="/health"
	HealthPath string `json:"healthPath,omitempty"`
}

// ServiceOfferPayment describes how buyers pay for the offer. Two methods
// are supported, selected by Method:
//
//   - "crypto" (default): x402 on-chain stablecoin settlement. Network and
//     PayTo are required and PayTo must be a 0x EVM address.
//   - "card": an MPP credit-card method (Stripe stripe.charge). Card is
//     required; funds settle off-chain into the configured Stripe account
//     and Network/PayTo do not apply.
//
// The per-method required fields are enforced by the XValidation rules
// below so the API server rejects malformed offers at admission time,
// independent of the CLI. The CEL guards short-circuit on Method so the
// 0x/account checks are only evaluated for the relevant method.
//
// +kubebuilder:validation:XValidation:rule="self.method != 'card' ? has(self.payTo) : true",message="payment.payTo is required when payment.method is crypto"
// +kubebuilder:validation:XValidation:rule="self.method != 'card' ? (has(self.network) && size(self.network) > 0) : true",message="payment.network is required when payment.method is crypto"
// +kubebuilder:validation:XValidation:rule="self.method == 'card' ? (has(self.card) && has(self.card.account)) : true",message="payment.card.account is required when payment.method is card"
type ServiceOfferPayment struct {
	// Payment method. "crypto" gates with x402 on-chain stablecoin
	// settlement (default; preserves existing behavior). "card" gates with
	// an MPP credit-card method (Stripe) that settles off-chain into
	// spec.payment.card.account.
	// +kubebuilder:default="crypto"
	// +kubebuilder:validation:Enum=crypto;card
	Method string `json:"method,omitempty"`
	// x402 payment scheme. Only meaningful when method=crypto.
	// +kubebuilder:default="exact"
	// +kubebuilder:validation:Enum=exact
	Scheme string `json:"scheme,omitempty"`
	// Chain identifier for payments (human-friendly). Reconciler resolves
	// to CAIP-2 format (e.g., "base-sepolia" → "eip155:84532"). Required
	// when method=crypto (enforced by the payment XValidation rules);
	// unused for card payments.
	Network string `json:"network,omitempty"`
	// USDC recipient wallet address (x402: payTo). Required and 0x-format
	// when method=crypto (enforced by the payment XValidation rules);
	// unused for card payments.
	// +kubebuilder:validation:Pattern=`^0x[0-9a-fA-F]{40}$`
	PayTo string `json:"payTo,omitempty"`
	// Payment validity window in seconds (x402: maxTimeoutSeconds).
	// +kubebuilder:default=300
	MaxTimeoutSeconds int64 `json:"maxTimeoutSeconds,omitempty"`
	// Optional token metadata override for x402 settlement. When omitted,
	// the verifier uses the chain default asset. Crypto only.
	Asset ServiceOfferAsset `json:"asset,omitempty"`
	// Card payment terms. Required when method=card; ignored otherwise.
	Card *ServiceOfferCardPayment `json:"card,omitempty"`
	// Pricing table with per-unit prices (human-readable decimals). For
	// crypto the unit is the settlement token (USDC by default); for card
	// the unit is payment.card.currency. Which fields are applicable
	// depends on the workload type.
	// +kubebuilder:validation:Required
	Price ServiceOfferPriceTable `json:"price"`
}

// ServiceOfferCardPayment holds the off-chain credit-card settlement terms
// used when ServiceOfferPayment.Method == "card". It is the card-method
// analog of Network/PayTo: instead of a chain plus a 0x recipient, funds
// settle through a payment provider (Stripe today, via the MPP
// stripe.charge method) into Account.
type ServiceOfferCardPayment struct {
	// Card payment provider. Only "stripe" is supported today (MPP
	// stripe.charge via Shared Payment Tokens).
	// +kubebuilder:default="stripe"
	// +kubebuilder:validation:Enum=stripe
	Provider string `json:"provider,omitempty"`
	// Destination account that receives settled card funds. For Stripe this
	// is the connected/destination account id (e.g. "acct_1A2b3C4d5E6f7G").
	// +kubebuilder:validation:Pattern=`^acct_[A-Za-z0-9]+$`
	Account string `json:"account,omitempty"`
	// ISO-4217 currency the card is charged in. Default "usd".
	// +kubebuilder:default="usd"
	// +kubebuilder:validation:Pattern=`^[a-z]{3}$`
	Currency string `json:"currency,omitempty"`
	// Optional Stripe "machine payments" network id, surfaced in the 402
	// challenge's extra block so MPP card clients know where to mint a
	// Shared Payment Token.
	NetworkID string `json:"networkId,omitempty"`
	// Accepted payment-method types advertised to the client. Defaults to
	// ["card"] at the gateway when empty.
	// +kubebuilder:validation:MaxItems=16
	PaymentMethodTypes []string `json:"paymentMethodTypes,omitempty"`
}

type ServiceOfferAsset struct {
	// ERC-20 contract address.
	// +kubebuilder:validation:Pattern=`^0x[0-9a-fA-F]{40}$`
	Address string `json:"address,omitempty"`
	// Human-friendly token symbol (e.g. USDC, OBOL).
	Symbol string `json:"symbol,omitempty"`
	// Token decimals in atomic units.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=255
	Decimals int64 `json:"decimals,omitempty"`
	// x402 transfer method for the asset.
	// +kubebuilder:validation:Enum=eip3009;permit2
	TransferMethod string `json:"transferMethod,omitempty"`
	// EIP-712 domain name used by the token.
	EIP712Name string `json:"eip712Name,omitempty"`
	// EIP-712 domain version used by the token.
	EIP712Version string `json:"eip712Version,omitempty"`
}

type ServiceOfferPriceTable struct {
	// Flat per-request price in USDC. Applicable to all types.
	PerRequest string `json:"perRequest,omitempty"`
	// Per-million-tokens price in USDC. Inference only.
	PerMTok string `json:"perMTok,omitempty"`
	// Per-compute-hour price in USDC. Fine-tuning only.
	PerHour string `json:"perHour,omitempty"`
	// Per-training-epoch price in USDC. Fine-tuning only.
	PerEpoch string `json:"perEpoch,omitempty"`
}

type ServiceOfferRegistration struct {
	// If true, register on ERC-8004 after routing is live.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// Agent name (ERC-8004: AgentRegistration.name).
	Name string `json:"name,omitempty"`
	// Agent description (ERC-8004: AgentRegistration.description).
	Description string `json:"description,omitempty"`
	// Agent icon URL (ERC-8004: AgentRegistration.image).
	Image string `json:"image,omitempty"`
	// Service endpoints (ERC-8004: AgentRegistration.services[]).
	Services []ServiceOfferService `json:"services,omitempty"`
	// Trust verification methods (ERC-8004: AgentRegistration.supportedTrust[]).
	// Valid values: reputation, crypto-economic, tee-attestation.
	SupportedTrust []string `json:"supportedTrust,omitempty"`
	// OASF skills for discovery (e.g.
	// natural_language_processing/text_generation). Mapped to an OASF
	// service entry in the registration JSON.
	Skills []string `json:"skills,omitempty"`
	// OASF domains for discovery (e.g. technology/artificial_intelligence).
	// Mapped to an OASF service entry in the registration JSON.
	Domains []string `json:"domains,omitempty"`
	// Additional registration metadata published into the generated
	// agent-registration.json for discovery and ranking.
	Metadata map[string]string `json:"metadata,omitempty"`
}

type ServiceOfferService struct {
	// Service type: web, A2A, MCP, OASF, ENS, DID, email.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Service URL. Auto-filled from tunnel URL if empty.
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`
	// Protocol version (SHOULD per ERC-8004 spec).
	Version string `json:"version,omitempty"`
}

type ServiceOfferStatus struct {
	// Condition types: ModelReady, UpstreamHealthy, PaymentGateReady,
	// RoutePublished, Registered, Ready.
	Conditions []Condition `json:"conditions,omitempty"`
	// The public endpoint URL once the route is published.
	Endpoint string `json:"endpoint,omitempty"`
	// ERC-8004 agent NFT token ID after on-chain registration.
	AgentID string `json:"agentId,omitempty"`
	// Transaction hash of the ERC-8004 registration.
	RegistrationTxHash string `json:"registrationTxHash,omitempty"`
	// The generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Controller's resolved view of an agent-type offer's referenced Agent.
	// Populated only when type=agent and the Agent is Ready.
	AgentResolution *ServiceOfferAgentResolution `json:"agentResolution,omitempty"`
}

// ServiceOfferAgentResolution is the controller's resolved view of an
// agent-type offer's referenced Agent. Populated only when Spec.Type ==
// "agent" and the Agent CR is Ready. Read by the route source when
// building RouteRules so the 402 extra block surfaces what's actually
// running.
type ServiceOfferAgentResolution struct {
	Model    string   `json:"model,omitempty"`
	Skills   []string `json:"skills,omitempty"`
	Runtime  string   `json:"runtime,omitempty"`
	Endpoint string   `json:"endpoint,omitempty"`
}

type Condition struct {
	// Condition type.
	// +kubebuilder:validation:Required
	Type string `json:"type"`
	// Status of the condition.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`
	// Machine-readable reason for the condition.
	Reason string `json:"reason,omitempty"`
	// Human-readable message with details.
	Message string `json:"message,omitempty"`
	// Last time the condition transitioned.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// ── RegistrationRequest ─────────────────────────────────────────────────────

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=rr
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Offer",type=string,JSONPath=`.spec.serviceOfferName`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.spec.desiredState`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="AgentID",type=string,JSONPath=`.status.agentId`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RegistrationRequest isolates ERC-8004 publication and on-chain side
// effects from the main ServiceOffer reconciliation loop. ServiceOffer
// remains the source of truth.
type RegistrationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RegistrationRequestSpec   `json:"spec,omitempty"`
	Status            RegistrationRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegistrationRequestList is the list form for kubectl/list operations.
type RegistrationRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RegistrationRequest `json:"items"`
}

type RegistrationRequestSpec struct {
	// +kubebuilder:validation:Required
	ServiceOfferName string `json:"serviceOfferName"`
	// +kubebuilder:validation:Required
	ServiceOfferNamespace string `json:"serviceOfferNamespace"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Active;Tombstoned
	DesiredState string `json:"desiredState"`
	// ERC-8004 registration chain alias for this request.
	Chain string `json:"chain,omitempty"`
}

type RegistrationRequestStatus struct {
	Phase                       string `json:"phase,omitempty"`
	Message                     string `json:"message,omitempty"`
	PublishedURL                string `json:"publishedUrl,omitempty"`
	AgentID                     string `json:"agentId,omitempty"`
	RegistrationTxHash          string `json:"registrationTxHash,omitempty"`
	RegistrationOwner           string `json:"registrationOwner,omitempty"`
	RegistrationURI             string `json:"registrationUri,omitempty"`
	RegistrationSearchFromBlock int64  `json:"registrationSearchFromBlock,omitempty"`
	MetadataSynced              bool   `json:"metadataSynced,omitempty"`
}

func (o *ServiceOffer) EffectiveNamespace() string {
	if o.Spec.Upstream.Namespace != "" {
		return o.Spec.Upstream.Namespace
	}
	return o.Namespace
}

func (o *ServiceOffer) EffectivePort() int64 {
	if o.Spec.Upstream.Port > 0 {
		return o.Spec.Upstream.Port
	}
	return 11434
}

func (o *ServiceOffer) EffectiveHealthPath() string {
	if o.Spec.Upstream.HealthPath != "" {
		return o.Spec.Upstream.HealthPath
	}
	return "/"
}

func (o *ServiceOffer) EffectivePath() string {
	if o.Spec.Path != "" {
		return o.Spec.Path
	}
	return fmt.Sprintf("/services/%s", o.Name)
}

func (o *ServiceOffer) IsInference() bool {
	return o.Spec.Type == "" || o.Spec.Type == "inference"
}

// IsAgent reports whether the offer references an Agent CR for its
// upstream. Type=="agent" is the only signal — Ref must also be non-empty
// for a usable offer, but admission validation enforces that.
func (o *ServiceOffer) IsAgent() bool {
	return o.Spec.Type == "agent"
}

// IsDraining reports whether spec.drainAt has been set. Drained offers
// transition through three phases: pre-drain (DrainAt nil), draining
// (DrainAt set, now < DrainEndsAt), and drain-expired (DrainAt set,
// now >= DrainEndsAt). The controller keeps the route up during
// "draining" and tears it down once "drain-expired" is reached.
func (o *ServiceOffer) IsDraining() bool {
	return o.Spec.DrainAt != nil
}

// DrainEndsAt returns DrainAt + DrainGracePeriod. When DrainAt is nil
// the zero time is returned (caller should gate on IsDraining first).
// When DrainGracePeriod is nil the default grace period is applied; a
// zero grace period is honored as "drain ends at DrainAt", i.e. tear
// down on the next reconcile (the --force/--now path).
func (o *ServiceOffer) DrainEndsAt() time.Time {
	if o.Spec.DrainAt == nil {
		return time.Time{}
	}
	grace := DefaultDrainGracePeriod
	if o.Spec.DrainGracePeriod != nil {
		grace = o.Spec.DrainGracePeriod.Duration
	}
	return o.Spec.DrainAt.Time.Add(grace)
}

// DrainExpired reports whether the drain grace period has elapsed.
// Returns false when the offer is not draining at all. Callers should
// use this rather than IsDraining when deciding whether to tear down
// the HTTPRoute or filter the offer from the live x402 verifier rules.
func (o *ServiceOffer) DrainExpired(now time.Time) bool {
	if !o.IsDraining() {
		return false
	}
	end := o.DrainEndsAt()
	return !now.Before(end)
}

// ── PurchaseRequest ─────────────────────────────────────────────────────────

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=pr
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Price",type=string,JSONPath=`.spec.payment.price`
// +kubebuilder:printcolumn:name="Remaining",type=integer,JSONPath=`.status.remaining`
// +kubebuilder:printcolumn:name="Spent",type=integer,JSONPath=`.status.spent`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PurchaseRequest is the buyer-side request for pre-signed x402 auths
// against a remote inference endpoint.
type PurchaseRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PurchaseRequestSpec   `json:"spec,omitempty"`
	Status            PurchaseRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PurchaseRequestList is the list form for kubectl/list operations.
type PurchaseRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PurchaseRequest `json:"items"`
}

type PurchaseRequestSpec struct {
	// Full URL to the x402-gated inference endpoint.
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`
	// Remote model ID (used as paid/<model> in LiteLLM).
	// +kubebuilder:validation:Required
	Model string `json:"model"`
	// Number of pre-signed auths to create.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2500
	Count int `json:"count"`
	// Pre-signed x402 payments (legacy ERC-3009 auths still supported).
	PreSignedAuths []PreSignedAuth    `json:"preSignedAuths,omitempty"`
	AutoRefill     PurchaseAutoRefill `json:"autoRefill,omitempty"`
	// +kubebuilder:validation:Required
	Payment PurchasePayment `json:"payment"`
}

// +kubebuilder:object:generate=false

// PreSignedAuth carries a pre-signed x402 payment authorization. The
// Payment map is opaque (forwarded verbatim to the buyer sidecar / x402
// facilitator) which can't be deep-copied by controller-gen; DeepCopy
// methods for this type are hand-written in deepcopy_manual.go.
type PreSignedAuth struct {
	ID string `json:"id,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	Payment     map[string]interface{} `json:"payment,omitempty"`
	Signature   string                 `json:"signature,omitempty"`
	From        string                 `json:"from,omitempty"`
	To          string                 `json:"to,omitempty"`
	Value       string                 `json:"value,omitempty"`
	ValidAfter  string                 `json:"validAfter,omitempty"`
	ValidBefore string                 `json:"validBefore,omitempty"`
	Nonce       string                 `json:"nonce,omitempty"`
}

// PurchaseAutoRefill drives the agent-managed auto-refill policy for a
// PurchaseRequest. The reconciler reads MaxTotal + MaxSpendPerDay as
// budget caps before signing more auths; without these fields populated
// the agent will not auto-refill beyond the initial Count.
type PurchaseAutoRefill struct {
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// Refill when remaining < threshold.
	// +kubebuilder:validation:Minimum=0
	Threshold int `json:"threshold,omitempty"`
	// Number of auths to sign on refill.
	// +kubebuilder:validation:Minimum=1
	Count int `json:"count,omitempty"`
	// Cap total auths ever signed.
	MaxTotal int `json:"maxTotal,omitempty"`
	// Max micro-USDC spend per day.
	MaxSpendPerDay string `json:"maxSpendPerDay,omitempty"`
}

type PurchasePayment struct {
	// +kubebuilder:validation:Required
	Network string `json:"network"`
	// +kubebuilder:validation:Required
	PayTo string `json:"payTo"`
	// Atomic token units per request.
	// +kubebuilder:validation:Required
	Price string `json:"price"`
	// ERC-20 contract address.
	// +kubebuilder:validation:Required
	Asset string `json:"asset"`
	// Human-friendly token symbol (e.g. USDC, OBOL).
	AssetSymbol string `json:"assetSymbol,omitempty"`
	// Token decimals in atomic units.
	AssetDecimals int64 `json:"assetDecimals,omitempty"`
	// x402 transfer method used for this asset.
	AssetTransferMethod string `json:"assetTransferMethod,omitempty"`
	// EIP-712 domain name used for signing.
	EIP712Name string `json:"eip712Name,omitempty"`
	// EIP-712 domain version used for signing.
	EIP712Version string `json:"eip712Version,omitempty"`
}

type PurchaseRequestStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// LiteLLM model name (paid/<model>).
	PublicModel string `json:"publicModel,omitempty"`
	Remaining   int    `json:"remaining,omitempty"`
	Spent       int    `json:"spent,omitempty"`
	TotalSigned int    `json:"totalSigned,omitempty"`
	TotalSpent  string `json:"totalSpent,omitempty"`
	// +kubebuilder:validation:Format=date-time
	ProbedAt      string `json:"probedAt,omitempty"`
	ProbedPrice   string `json:"probedPrice,omitempty"`
	WalletBalance string `json:"walletBalance,omitempty"`
	SignerAddress string `json:"signerAddress,omitempty"`
}

func (pr *PurchaseRequest) EffectiveBuyerNamespace() string {
	return "llm"
}

// ── Agent ───────────────────────────────────────────────────────────────────

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ag
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.spec.runtime`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.pinnedModel`
// +kubebuilder:printcolumn:name="Wallet",type=string,JSONPath=`.status.walletAddress`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent is the declarative spec for an Obol Stack agent (Hermes today,
// OpenClaw later). Decouples agent lifecycle from selling: `obol sell
// agent <name>` references an existing Agent rather than provisioning
// one inline. Internal manager agents with RBAC can also create Agent
// resources to spawn sub-agents.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentSpec   `json:"spec,omitempty"`
	Status            AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList is the list form for kubectl/list operations.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

type AgentSpec struct {
	// Agent runtime (only hermes today; openclaw planned).
	// +kubebuilder:default=hermes
	// +kubebuilder:validation:Enum=hermes
	Runtime string `json:"runtime,omitempty"`
	// LiteLLM model name to pin. Empty = controller picks cluster
	// top-of-rank on first deploy and writes status.pinnedModel.
	// +kubebuilder:validation:MaxLength=256
	Model string `json:"model,omitempty"`
	// Allow-listed skills written to the per-agent skills dir on first
	// reconcile. Agent can edit afterwards; this is a seed, not a sandbox.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9][a-z0-9-]*$`
	// +kubebuilder:validation:items:MaxLength=64
	Skills []string `json:"skills,omitempty"`
	// Operator-supplied objective text. Substituted into the SOUL.md
	// template by the seeder on first write. Agent owns SOUL.md after that.
	// +kubebuilder:validation:MaxLength=4096
	Objective string      `json:"objective,omitempty"`
	Wallet    AgentWallet `json:"wallet,omitempty"`
}

type AgentWallet struct {
	// Provision a per-namespace remote-signer keystore. Address is
	// published in status.walletAddress.
	// +kubebuilder:default=false
	Create bool `json:"create,omitempty"`
}

type AgentStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Pending | Provisioning | Ready | Failed
	Phase string `json:"phase,omitempty"`
	// Actual model the agent is using (= spec.model when set, otherwise
	// the auto-picked top-of-rank).
	PinnedModel string `json:"pinnedModel,omitempty"`
	// Agent's signing address when wallet.create=true. Empty otherwise.
	// +kubebuilder:validation:Pattern=`^(0x[0-9a-fA-F]{40})?$`
	WalletAddress string `json:"walletAddress,omitempty"`
	// Cluster-internal URL for the agent runtime (e.g.
	// http://hermes.agent-quant.svc.cluster.local:8642).
	Endpoint   string      `json:"endpoint,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

func (a *Agent) EffectiveRuntime() string {
	if a.Spec.Runtime != "" {
		return a.Spec.Runtime
	}
	return AgentRuntimeHermes
}

// EffectiveModel returns the model the controller should use right now:
// the user-pinned spec.model when set, falling back to the previously
// resolved status.pinnedModel. Returns "" if neither is set, signalling
// "first reconcile, pick top-of-rank from LiteLLM and write back to status".
func (a *Agent) EffectiveModel() string {
	if a.Spec.Model != "" {
		return a.Spec.Model
	}
	return a.Status.PinnedModel
}

func (a *Agent) IsReady() bool {
	return a.Status.Phase == AgentPhaseReady
}

// ── AgentIdentity ───────────────────────────────────────────────────────────

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=aid
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Chains",type=string,JSONPath=`.status.registrations[*].chain`
// +kubebuilder:printcolumn:name="AgentIDs",type=string,JSONPath=`.status.registrations[*].agentId`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentIdentity is the durable, on-chain identity an operator controls in
// the ERC-8004 Identity Registry. A single AgentIdentity outlives
// ServiceOffers: deleting the last ServiceOffer that references it does
// not delete the NFT, the published registration document, or the
// recorded agentId; instead the renderer publishes a tombstone
// (active:false, x402Support:false) so external observers still see the
// historical record.
type AgentIdentity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentIdentitySpec   `json:"spec,omitempty"`
	Status            AgentIdentityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentIdentityList is the list form for kubectl/list operations.
type AgentIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentIdentity `json:"items"`
}

type AgentIdentitySpec struct{}

type AgentIdentityStatus struct {
	// Per-chain ERC-8004 registrations for this identity document.
	Registrations []AgentIdentityRegistration `json:"registrations,omitempty"`
}

type AgentIdentityRegistration struct {
	// ERC-8004 registration chain alias.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=64
	Chain string `json:"chain"`
	// On-chain ERC-721 tokenId on the given chain.
	// +kubebuilder:validation:Required
	AgentID string `json:"agentId"`
}

func AgentIdentityAgentIDForChain(status AgentIdentityStatus, chain string) string {
	chain = strings.TrimSpace(chain)
	for _, registration := range status.Registrations {
		if strings.EqualFold(strings.TrimSpace(registration.Chain), chain) && strings.TrimSpace(registration.AgentID) != "" {
			return registration.AgentID
		}
	}
	return ""
}

func UpsertAgentIdentityRegistration(status AgentIdentityStatus, chain, agentID string) AgentIdentityStatus {
	chain = strings.TrimSpace(chain)
	agentID = strings.TrimSpace(agentID)
	if chain == "" || agentID == "" {
		return status
	}
	updated := false
	out := status.Registrations[:0]
	for _, registration := range status.Registrations {
		if strings.EqualFold(strings.TrimSpace(registration.Chain), chain) {
			if !updated {
				registration.Chain = chain
				registration.AgentID = agentID
				out = append(out, registration)
				updated = true
			}
			continue
		}
		out = append(out, registration)
	}
	if !updated {
		out = append(out, AgentIdentityRegistration{Chain: chain, AgentID: agentID})
	}
	status.Registrations = out
	return status
}

func HasAgentIdentityRegistrations(status AgentIdentityStatus) bool {
	for _, registration := range status.Registrations {
		if strings.TrimSpace(registration.Chain) != "" && strings.TrimSpace(registration.AgentID) != "" {
			return true
		}
	}
	return false
}
