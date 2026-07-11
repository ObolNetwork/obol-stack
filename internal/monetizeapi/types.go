package monetizeapi

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
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
	NetworkPolicyGVR  = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
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

	// Primary accepted payment. Always set, including for multi-payment
	// offers, where it MUST mirror payments[0] — discovery surfaces that
	// predate multi-payment (the catalog, skill.md, kubectl printcolumns)
	// read this singular block. The x402 verifier reads the full set via
	// EffectivePayments.
	// +kubebuilder:validation:Required
	Payment ServiceOfferPayment `json:"payment"`

	// Payments lists every accepted payment option for this offer (one per
	// currency/network). When non-empty it is the source of truth for the
	// x402 verifier's 402 accepts[] array: the buyer picks one option and
	// the verifier settles whichever was used. payments[0] is the primary
	// option and MUST equal spec.payment. Empty means a single-payment
	// offer described by spec.payment alone. See EffectivePayments.
	Payments []ServiceOfferPayment `json:"payments,omitempty"`

	// Listing controls how the offer is presented on the public storefront
	// (ordering weight and grouping category). Cosmetic only — it does not
	// affect routing, pricing, or payment.
	Listing ServiceOfferListing `json:"listing,omitempty"`

	// Routes declares the offer's route table: per-path gate classes (paid
	// or free) and optional per-route price overrides, all relative to the
	// offer's effective path. It is the single source of truth for the
	// x402 verifier's route rules and for discovery surfaces (openapi,
	// skill.md, services.json, buy prompts). Empty means the pre-route-table
	// behavior: one paid catch-all covering everything under the offer path.
	// See EffectiveRoutes.
	// +kubebuilder:validation:MaxItems=128
	Routes []ServiceOfferRoute `json:"routes,omitempty"`

	// URL path prefix for the HTTPRoute, defaults to /services/<name>.
	// +kubebuilder:validation:Pattern=`^/[a-zA-Z0-9/_.-]*$`
	Path string `json:"path,omitempty"`

	// Hostname gives the offer its own public origin: the controller
	// renders an additional HTTPRoute answering on this hostname alone,
	// with the offer's routes rooted at "/" (rewritten into the shared
	// /services/<name> path-world before the payment gate) plus an
	// offer-scoped discovery bundle (/openapi.json, /.well-known/x402,
	// and a landing page at "/"). The shared-origin path keeps working as
	// a back-compat alias. The x402 market is origin-keyed (x402scan,
	// agentcash crawl per origin) — a dedicated hostname is what makes an
	// offer list as its own product instead of leaking into one shared
	// listing. DNS + tunnel routing for the hostname are the operator's
	// job (obol tunnel hostname add <host> --offer <ns>/<name>).
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`
	Hostname string `json:"hostname,omitempty"`

	// Branding overrides the storefront-wide seller identity on this
	// offer's dedicated origin (the landing page, 402 paywall, sign-in
	// page and discovery surfaces served under spec.hostname).
	// Field-wise merge over the operator's storefront profile: empty
	// fields inherit. Only meaningful when spec.hostname is set —
	// branding is per-origin; path-shared offers ride the storefront
	// identity. Set via `obol sell info set --hostname <host> ...`.
	Branding *ServiceOfferBranding `json:"branding,omitempty"`

	// Optional provenance metadata for the service. Tracks how the model or
	// service was produced (e.g. autoresearch experiment data). Included in
	// the ERC-8004 registration document when present.
	Provenance map[string]string `json:"provenance,omitempty"`

	// ERC-8004 registration metadata. Field names align with the
	// AgentRegistration document schema (ERC-8004 spec).
	Registration ServiceOfferRegistration `json:"registration,omitempty"`

	// Async turns the offer's paid routes into accepted jobs: payment
	// settles at acceptance, the request is replayed against the upstream
	// with no client-facing deadline by the job broker, and the buyer
	// polls a free status page (202 + Location: <offer>/jobs/<id>).
	Async ServiceOfferAsync `json:"async,omitempty"`

	// Limits renders Traefik middleware in front of the offer's routes.
	Limits ServiceOfferLimits `json:"limits,omitempty"`

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
// ServiceOfferBranding is the per-origin seller identity override for a
// hostname-bound offer. Shape mirrors the storefront profile's identity
// fields; every field is optional and empty fields inherit from the
// storefront-wide profile at render time (see ProfilePatch).
type ServiceOfferBranding struct {
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName,omitempty"`
	// +kubebuilder:validation:MaxLength=256
	Tagline string `json:"tagline,omitempty"`
	// https://..., /path on the main origin, or inline data:image/...;base64.
	LogoURL string `json:"logoUrl,omitempty"`
	// +kubebuilder:validation:Enum=light;dark;obol
	Theme string `json:"theme,omitempty"`
	// +kubebuilder:validation:Pattern=`^#[0-9a-fA-F]{3,8}$`
	AccentColor string `json:"accentColor,omitempty"`
	FaviconURL  string `json:"faviconUrl,omitempty"`
	OGImageURL  string `json:"ogImageUrl,omitempty"`
	// Longer seller copy for this origin (markdown subset — rendered
	// through the storefront richtext sanitizer).
	Description string `json:"description,omitempty"`
}

// ProfilePatch converts the branding block into a StorefrontProfile patch
// suitable for storefront.MergeProfile: set fields override, empty inherit.
func (b *ServiceOfferBranding) ProfilePatch() schemas.StorefrontProfile {
	if b == nil {
		return schemas.StorefrontProfile{}
	}
	return schemas.StorefrontProfile{
		DisplayName: b.DisplayName,
		Tagline:     b.Tagline,
		LogoURL:     b.LogoURL,
		Theme:       b.Theme,
		AccentColor: b.AccentColor,
		FaviconURL:  b.FaviconURL,
		OGImageURL:  b.OGImageURL,
		Description: b.Description,
	}
}

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

type ServiceOfferPayment struct {
	// x402 payment scheme.
	// +kubebuilder:default="exact"
	// +kubebuilder:validation:Enum=exact
	Scheme string `json:"scheme,omitempty"`
	// Chain identifier for payments (human-friendly). Reconciler resolves
	// to CAIP-2 format (e.g., "base-sepolia" → "eip155:84532").
	// +kubebuilder:validation:Required
	Network string `json:"network"`
	// USDC recipient wallet address (x402: payTo).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^0x[0-9a-fA-F]{40}$`
	PayTo string `json:"payTo"`
	// Payment validity window in seconds (x402: maxTimeoutSeconds).
	// +kubebuilder:default=300
	MaxTimeoutSeconds int64 `json:"maxTimeoutSeconds,omitempty"`
	// Optional token metadata override for x402 settlement. When omitted,
	// the verifier uses the chain default asset.
	Asset ServiceOfferAsset `json:"asset,omitempty"`
	// Pricing table with per-unit prices in USDC (human-readable decimals).
	// Which fields are applicable depends on the workload type.
	// +kubebuilder:validation:Required
	Price ServiceOfferPriceTable `json:"price"`
}

// Result visibility modes for async offers.
const (
	ResultVisibilityPayer  = "payer"
	ResultVisibilityPublic = "public"
)

// DefaultJobTTL is how long job records + stored results live when
// spec.async.ttl is unset.
const DefaultJobTTL = 72 * time.Hour

// ServiceOfferAsync configures broker-mediated async delivery.
type ServiceOfferAsync struct {
	// Enabled routes the offer's paid requests through the job broker.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// ResultVisibility gates GET <offer>/jobs/<id>/result. "payer"
	// (default) requires the paying wallet (SIWX) or the capability
	// jobToken returned in the 202 body; "public" makes the unguessable
	// job id the capability (shareable results).
	// +kubebuilder:default="payer"
	// +kubebuilder:validation:Enum=payer;public
	ResultVisibility string `json:"resultVisibility,omitempty"`
	// TTL is how long job records + stored results are retained.
	// Defaults to 72h. Status pages return 410 Gone afterwards.
	TTL *metav1.Duration `json:"ttl,omitempty"`
}

// EffectiveResultVisibility defaults to payer — fail closed on paid work.
func (a *ServiceOfferAsync) EffectiveResultVisibility() string {
	if a.ResultVisibility == ResultVisibilityPublic {
		return ResultVisibilityPublic
	}
	return ResultVisibilityPayer
}

// EffectiveTTL returns the configured retention or the default.
func (a *ServiceOfferAsync) EffectiveTTL() time.Duration {
	if a.TTL != nil && a.TTL.Duration > 0 {
		return a.TTL.Duration
	}
	return DefaultJobTTL
}

// ServiceOfferLimits renders Traefik protection middleware for the offer.
type ServiceOfferLimits struct {
	// MaxInFlight caps concurrent in-flight requests (Traefik inFlightReq).
	// 0 = no cap. The unbounded-concurrency hole on paid agents is
	// pentest-proven; paid agent offers should set a small value.
	// +kubebuilder:validation:Minimum=0
	MaxInFlight int64 `json:"maxInFlight,omitempty"`
	// RPS caps average requests/second (Traefik rateLimit; burst 2x).
	// 0 = no cap.
	// +kubebuilder:validation:Minimum=0
	RPS int64 `json:"rps,omitempty"`
}

// Gate classes for ServiceOfferRoute.
const (
	GatePaid = "paid"
	GateFree = "free"
	// GateAuth requires a SIWX-verified wallet (EIP-4361 signature or a
	// verifier-minted session). The verifier injects X-Verified-Wallet
	// upstream; authorization (WHICH wallet may see what) is the
	// upstream's job.
	GateAuth = "auth"
)

// ServiceOfferRoute is one entry in an offer's route table. Path is
// interpreted relative to the offer's effective path (the public prefix),
// so route tables survive a future move to per-offer hostnames unchanged.
type ServiceOfferRoute struct {
	// Path relative to the offer's effective path. Must start with "/".
	// Supports the verifier's pattern syntax: exact ("/healthz"), greedy
	// prefix ("/v1/*"), and segment globs ("/models-*/info").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/[a-zA-Z0-9/_.*-]*$`
	// +kubebuilder:validation:MaxLength=512
	Path string `json:"path"`
	// HTTP methods this route serves. Discovery metadata only in phase 1 —
	// the payment gate applies to every method on a matched path. Empty
	// means all methods.
	// +kubebuilder:validation:MaxItems=9
	// +kubebuilder:validation:items:Enum=GET;POST;PUT;PATCH;DELETE;HEAD;OPTIONS
	Methods []string `json:"methods,omitempty"`
	// Gate class. "paid" gates the route with x402 (default); "free" passes
	// the payment gate entirely (health checks, job status pages, per-offer
	// discovery documents); "auth" requires a SIWX-verified wallet and
	// forwards it upstream as X-Verified-Wallet.
	// +kubebuilder:default="paid"
	// +kubebuilder:validation:Enum=paid;free;auth
	Gate string `json:"gate,omitempty"`
	// Price override for a paid route. When set, it replaces the offer's
	// primary payment option price for this route and the route becomes
	// single-payment (multi-currency price overrides are not supported —
	// secondary spec.payments[] options are not advertised on overridden
	// routes). Empty means the offer's payment pricing applies.
	Price ServiceOfferPriceTable `json:"price,omitempty"`
	// Human-readable one-line summary surfaced in discovery (openapi
	// operation summary, skill.md route list).
	// +kubebuilder:validation:MaxLength=300
	Summary string `json:"summary,omitempty"`
	// FreeQuota grants each SIWX-verified wallet this many free calls per
	// UTC day on a paid route before the 402 applies (the x402scan-style
	// free tier). Counters are verifier-local and reset on verifier
	// restart — a giveaway mechanism, not an entitlement ledger. 0 = none.
	// +kubebuilder:validation:Minimum=0
	FreeQuota int64 `json:"freeQuota,omitempty"`
}

// EffectiveGate returns the route's gate class, defaulting to paid so a
// zero-valued route never opens a free path by accident.
func (r *ServiceOfferRoute) EffectiveGate() string {
	if r.Gate == "" {
		return GatePaid
	}
	return r.Gate
}

// HasPriceOverride reports whether any price slot is set on the route.
func (r *ServiceOfferRoute) HasPriceOverride() bool {
	return r.Price != (ServiceOfferPriceTable{})
}

// ServiceOfferListing carries storefront presentation hints. Both fields
// are optional and purely cosmetic.
type ServiceOfferListing struct {
	// Weight orders offers on the storefront: higher sorts earlier. Offers
	// with equal weight fall back to alphabetical by name. Defaults to 0.
	Weight int `json:"weight,omitempty"`
	// Category groups the offer into a named storefront section (e.g.
	// "demo"). Empty means the default/uncategorized section.
	Category string `json:"category,omitempty"`
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

// reservedPathRoots are shared-origin surfaces an offer path may never
// claim or nest under: the discovery documents, the Scalar API reference,
// the eRPC gateway, and .well-known. Claiming one would shadow it in the
// verifier's first-match route table (or in Traefik, depending on route
// specificity) with no signal to the operator.
var reservedPathRoots = []string{"/api", "/openapi.json", "/skill.md", "/rpc", "/.well-known"}

// ReservedPathConflict returns the reserved root an offer path collides
// with, or "". "/" and the bare "/services" are reserved exactly (they
// would blanket the storefront root / every sibling offer); the roots in
// reservedPathRoots are reserved along with everything nested under them.
// Trailing slashes are ignored.
func ReservedPathConflict(path string) string {
	p := strings.TrimSuffix(path, "/")
	if p == "" || p == "/services" {
		return "/"
	}
	for _, root := range reservedPathRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return root
		}
	}
	return ""
}

// routeReservedPathRoots extends reservedPathRoots with the verifier's own
// sign-in surface for gate:auth offers. A route declared at "/auth" or
// "/auth/verify" would shadow the SIWX challenge/verify endpoints the
// verifier serves at those paths.
var routeReservedPathRoots = append(append([]string{}, reservedPathRoots...), "/auth")

// ReservedRoutePathConflict returns the reserved root a route's
// offer-relative path (spec.routes[].path) collides with, or "". Unlike
// ReservedPathConflict this does not special-case "/" or "/services" — "/"
// is a normal, meaningful relative route path (the offer's own root route)
// — it only guards the shared verifier surfaces plus "/auth".
func ReservedRoutePathConflict(path string) string {
	p := strings.TrimSuffix(path, "/")
	for _, root := range routeReservedPathRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return root
		}
	}
	return ""
}

// EffectiveOrigin returns the offer's dedicated public origin
// ("https://<hostname>") when spec.hostname is set, else "". Discovery
// surfaces use it to advertise hostname-bound offers at their own origin
// while path-only offers stay rooted at the shared tunnel URL.
func (o *ServiceOffer) EffectiveOrigin() string {
	if o.Spec.Hostname == "" {
		return ""
	}
	return "https://" + o.Spec.Hostname
}

// EffectiveRoutes returns the offer's declared route table, or the
// synthesized pre-route-table equivalent when spec.routes is empty: a
// single paid catch-all ("/*") covering everything under the offer's
// effective path. Callers can therefore treat every offer as route-table
// driven. The result is never empty.
func (o *ServiceOffer) EffectiveRoutes() []ServiceOfferRoute {
	if len(o.Spec.Routes) > 0 {
		return o.Spec.Routes
	}
	return []ServiceOfferRoute{{Path: "/*", Gate: GatePaid}}
}

// EffectivePayments returns every accepted payment option for the offer.
// When spec.payments is populated it is returned verbatim (multi-payment
// offers); otherwise a single-element slice is synthesized from spec.payment
// so single-payment offers and pre-multi-payment CRs keep working unchanged.
// payments[0] (or spec.payment) is always the primary option.
func (o *ServiceOffer) EffectivePayments() []ServiceOfferPayment {
	if len(o.Spec.Payments) > 0 {
		return o.Spec.Payments
	}
	return []ServiceOfferPayment{o.Spec.Payment}
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
	// Hermes model provider. Empty or "custom" keeps the cluster LiteLLM
	// path (base_url + api_key). Other values (e.g. "xai-oauth") omit those
	// and resolve credentials in-pod.
	// +kubebuilder:validation:MaxLength=64
	ModelProvider string `json:"modelProvider,omitempty"`
	// Allow-listed skills written to the per-agent skills dir on first
	// reconcile. Agent can edit afterwards; this is a seed, not a sandbox.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9][a-z0-9-]*$`
	// +kubebuilder:validation:items:MaxLength=64
	Skills []string `json:"skills,omitempty"`
	// Operator-supplied objective text. Substituted into the SOUL.md
	// template by the seeder on first write. Agent owns SOUL.md after that.
	// +kubebuilder:validation:MaxLength=4096
	Objective string `json:"objective,omitempty"`
	// MCP servers rendered into Hermes config.yaml under mcp_servers.
	// Empty/omitted: no mcp_servers block (byte-identical to pre-field config).
	// +kubebuilder:validation:MaxItems=32
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
	// Hermes agent.max_turns. Nil = 30 (historical sub-agent default).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	MaxTurns *int `json:"maxTurns,omitempty"`
	// Hermes agent.disabled_toolsets. Nil = ["memory","web"] (historical
	// default). Explicit empty list disables none.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=64
	DisabledToolsets []string    `json:"disabledToolsets,omitempty"`
	Wallet           AgentWallet `json:"wallet,omitempty"`
}

// MCPServer is one Hermes MCP server entry (stdio transport). Env values
// may use ${VAR} interpolation; Hermes resolves them in-pod at runtime —
// the controller does not expand them.
type MCPServer struct {
	// Server name used as the key under mcp_servers in config.yaml.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`
	Name string `json:"name"`
	// Command to launch the MCP server process.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Command string `json:"command"`
	// Arguments passed to Command. Omitted from YAML when empty.
	// +kubebuilder:validation:MaxItems=64
	Args []string `json:"args,omitempty"`
	// Optional process timeout in seconds. Omitted from YAML when nil.
	// +kubebuilder:validation:Minimum=1
	Timeout *int `json:"timeout,omitempty"`
	// Environment variables for the MCP process. Values may contain
	// ${VAR} placeholders resolved by Hermes in-pod (not by the controller).
	// +kubebuilder:validation:MaxProperties=64
	Env map[string]string `json:"env,omitempty"`
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

type AgentIdentitySpec struct {
}

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
