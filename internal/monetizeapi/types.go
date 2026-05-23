package monetizeapi

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

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

	PausedAnnotation = "obol.org/paused"

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

type ServiceOffer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceOfferSpec   `json:"spec,omitempty"`
	Status            ServiceOfferStatus `json:"status,omitempty"`
}

type ServiceOfferSpec struct {
	Type         string                   `json:"type,omitempty"`
	Agent        ServiceOfferAgent        `json:"agent,omitempty"`
	Model        ServiceOfferModel        `json:"model,omitempty"`
	Upstream     ServiceOfferUpstream     `json:"upstream,omitempty"`
	Payment      ServiceOfferPayment      `json:"payment,omitempty"`
	Path         string                   `json:"path,omitempty"`
	Provenance   map[string]string        `json:"provenance,omitempty"`
	Registration ServiceOfferRegistration `json:"registration,omitempty"`
}

// ServiceOfferAgent is populated when Spec.Type == "agent". The controller
// resolves Ref → Agent CR, derives Upstream from Agent.status.endpoint, and
// surfaces the agent's model + skills in the 402 response's extra block so
// buyers see what they're paying for.
type ServiceOfferAgent struct {
	Ref ServiceOfferAgentRef `json:"ref,omitempty"`
}

type ServiceOfferAgentRef struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type ServiceOfferModel struct {
	Name    string `json:"name,omitempty"`
	Runtime string `json:"runtime,omitempty"`
}

type ServiceOfferUpstream struct {
	Service    string `json:"service,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Port       int64  `json:"port,omitempty"`
	HealthPath string `json:"healthPath,omitempty"`
}

type ServiceOfferPayment struct {
	Scheme            string                 `json:"scheme,omitempty"`
	Network           string                 `json:"network,omitempty"`
	PayTo             string                 `json:"payTo,omitempty"`
	MaxTimeoutSeconds int64                  `json:"maxTimeoutSeconds,omitempty"`
	Asset             ServiceOfferAsset      `json:"asset,omitempty"`
	Price             ServiceOfferPriceTable `json:"price,omitempty"`
}

type ServiceOfferAsset struct {
	Address        string `json:"address,omitempty"`
	Symbol         string `json:"symbol,omitempty"`
	Decimals       int64  `json:"decimals,omitempty"`
	TransferMethod string `json:"transferMethod,omitempty"`
	EIP712Name     string `json:"eip712Name,omitempty"`
	EIP712Version  string `json:"eip712Version,omitempty"`
}

type ServiceOfferPriceTable struct {
	PerRequest string `json:"perRequest,omitempty"`
	PerMTok    string `json:"perMTok,omitempty"`
	PerHour    string `json:"perHour,omitempty"`
	PerEpoch   string `json:"perEpoch,omitempty"`
}

type ServiceOfferRegistration struct {
	Enabled        bool                  `json:"enabled,omitempty"`
	Name           string                `json:"name,omitempty"`
	Description    string                `json:"description,omitempty"`
	Image          string                `json:"image,omitempty"`
	Services       []ServiceOfferService `json:"services,omitempty"`
	SupportedTrust []string              `json:"supportedTrust,omitempty"`
	Skills         []string              `json:"skills,omitempty"`
	Domains        []string              `json:"domains,omitempty"`
	Metadata       map[string]string     `json:"metadata,omitempty"`
}

type ServiceOfferService struct {
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Version  string `json:"version,omitempty"`
}

type ServiceOfferStatus struct {
	Conditions         []Condition                  `json:"conditions,omitempty"`
	Endpoint           string                       `json:"endpoint,omitempty"`
	AgentID            string                       `json:"agentId,omitempty"`
	RegistrationTxHash string                       `json:"registrationTxHash,omitempty"`
	ObservedGeneration int64                        `json:"observedGeneration,omitempty"`
	AgentResolution    *ServiceOfferAgentResolution `json:"agentResolution,omitempty"`
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
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

type RegistrationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RegistrationRequestSpec   `json:"spec,omitempty"`
	Status            RegistrationRequestStatus `json:"status,omitempty"`
}

type RegistrationRequestSpec struct {
	ServiceOfferName      string `json:"serviceOfferName,omitempty"`
	ServiceOfferNamespace string `json:"serviceOfferNamespace,omitempty"`
	DesiredState          string `json:"desiredState,omitempty"`
	Chain                 string `json:"chain,omitempty"`
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

func (o *ServiceOffer) IsPaused() bool {
	return o.Annotations != nil && o.Annotations[PausedAnnotation] == "true"
}

// ── PurchaseRequest ─────────────────────────────────────────────────────────

type PurchaseRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PurchaseRequestSpec   `json:"spec,omitempty"`
	Status            PurchaseRequestStatus `json:"status,omitempty"`
}

type PurchaseRequestSpec struct {
	Endpoint       string             `json:"endpoint"`
	Model          string             `json:"model"`
	Count          int                `json:"count"`
	PreSignedAuths []PreSignedAuth    `json:"preSignedAuths,omitempty"`
	AutoRefill     PurchaseAutoRefill `json:"autoRefill,omitempty"`
	Payment        PurchasePayment    `json:"payment"`
}

type PreSignedAuth struct {
	ID          string                 `json:"id,omitempty"`
	Payment     map[string]interface{} `json:"payment,omitempty"`
	Signature   string                 `json:"signature"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Value       string                 `json:"value"`
	ValidAfter  string                 `json:"validAfter"`
	ValidBefore string                 `json:"validBefore"`
	Nonce       string                 `json:"nonce"`
}

type PurchaseAutoRefill struct {
	Enabled        bool   `json:"enabled,omitempty"`
	Threshold      int    `json:"threshold,omitempty"`
	Count          int    `json:"count,omitempty"`
	MaxTotal       int    `json:"maxTotal,omitempty"`
	MaxSpendPerDay string `json:"maxSpendPerDay,omitempty"`
}

type PurchasePayment struct {
	Network             string `json:"network"`
	PayTo               string `json:"payTo"`
	Price               string `json:"price"`
	Asset               string `json:"asset"`
	AssetSymbol         string `json:"assetSymbol,omitempty"`
	AssetDecimals       int64  `json:"assetDecimals,omitempty"`
	AssetTransferMethod string `json:"assetTransferMethod,omitempty"`
	EIP712Name          string `json:"eip712Name,omitempty"`
	EIP712Version       string `json:"eip712Version,omitempty"`
}

type PurchaseRequestStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	PublicModel        string      `json:"publicModel,omitempty"`
	Remaining          int         `json:"remaining,omitempty"`
	Spent              int         `json:"spent,omitempty"`
	TotalSigned        int         `json:"totalSigned,omitempty"`
	TotalSpent         string      `json:"totalSpent,omitempty"`
	ProbedAt           string      `json:"probedAt,omitempty"`
	ProbedPrice        string      `json:"probedPrice,omitempty"`
	WalletBalance      string      `json:"walletBalance,omitempty"`
	SignerAddress      string      `json:"signerAddress,omitempty"`
}

func (pr *PurchaseRequest) EffectiveBuyerNamespace() string {
	return "llm"
}

// ── Agent ───────────────────────────────────────────────────────────────────

type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentSpec   `json:"spec,omitempty"`
	Status            AgentStatus `json:"status,omitempty"`
}

type AgentSpec struct {
	Runtime   string      `json:"runtime,omitempty"`
	Model     string      `json:"model,omitempty"`
	Skills    []string    `json:"skills,omitempty"`
	Objective string      `json:"objective,omitempty"`
	Wallet    AgentWallet `json:"wallet,omitempty"`
}

type AgentWallet struct {
	Create bool `json:"create,omitempty"`
}

type AgentStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              string      `json:"phase,omitempty"`
	PinnedModel        string      `json:"pinnedModel,omitempty"`
	WalletAddress      string      `json:"walletAddress,omitempty"`
	Endpoint           string      `json:"endpoint,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
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

// AgentIdentity is the durable, on-chain identity an operator controls in the
// ERC-8004 Identity Registry. A single AgentIdentity outlives ServiceOffers:
// deleting the last ServiceOffer that references it does not delete the NFT,
// the published registration document, or the recorded agentId; instead the
// renderer publishes a tombstone (active:false, x402Support:false) so external
// observers still see the historical record.
type AgentIdentity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentIdentitySpec   `json:"spec,omitempty"`
	Status            AgentIdentityStatus `json:"status,omitempty"`
}

type AgentIdentitySpec struct {
}

type AgentIdentityStatus struct {
	Registrations []AgentIdentityRegistration `json:"registrations,omitempty"`
}

type AgentIdentityRegistration struct {
	Chain   string `json:"chain,omitempty"`
	AgentID string `json:"agentId,omitempty"`
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
