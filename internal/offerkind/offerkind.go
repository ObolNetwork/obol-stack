// Package offerkind is the single source of truth for what a ServiceOffer /
// ServiceRequest "type" means: how it renders (storefront copy, bazaar and
// OpenAPI discovery shapes), which price slot it uses, and — crucially —
// which integrity checks apply to it.
//
// It replaces the type-collapse logic that was previously duplicated across
// packages: internal/x402 (normalizeOfferType) and
// internal/serviceoffercontroller (fallbackOfferType, openAPIPathsForOffer,
// offerPriceRawAndUnit). Each of those re-implemented "given a spec.type,
// what shape is this" with subtly different defaults. Centralizing here means
// adding a 7th service type is a single table entry instead of an 8-file sweep.
//
// Design: this is a ZERO-DEPENDENCY leaf package (stdlib only). Call sites pass
// the raw spec.type string (offer.Spec.Type), never a CRD struct, so both x402
// and the controller can import it with no risk of an import cycle. The data
// table mirrors how internal/bounty/registry.go makes task types data-driven.
//
// Integrity is kept strictly separate from pricing/upstream/rendering: the
// IntegrityProfile declares only authenticity/identity/scope obligations, not
// "is the price valid" (that is a pricing concern, not an integrity one).
package offerkind

// PaymentClass is the payment-integrity obligation for a type. Payment
// verification itself is uniform x402 "exact"; the only real axis is whether a
// payment proof is required at all (it always is, today) — method (card vs
// crypto) is handled separately by the verifier, not here.
type PaymentClass string

const (
	PaymentX402Exact PaymentClass = "x402-exact"
	PaymentNone      PaymentClass = "none"
)

// ContentClass is the data-authenticity obligation: how a buyer proves the
// bytes it received are the bytes the seller committed to.
type ContentClass string

const (
	ContentNone             ContentClass = "none"
	ContentSignedVersionLog ContentClass = "signed-version-log" // dataset / fine-tuning: owner-signed secp256k1 hash-chain (internal/dataset)
	ContentBundleSHA256     ContentClass = "bundle-sha256"      // skill: controller-validated bundle hash+size
)

// IdentityClass is the caller-identity / membership obligation.
type IdentityClass string

const (
	IdentityNone      IdentityClass = "none"
	IdentityGroupAuth IdentityClass = "groupauth" // membership-gated via internal/research/groupauth
)

// ScopeClass is the entitlement-scope obligation layered on top of membership.
type ScopeClass string

const (
	ScopeNone               ScopeClass = "none"
	ScopeVersionEntitlement ScopeClass = "version-entitlement" // dataset: token entitled only up to a paid version
)

// IntegrityProfile declares the integrity checks a service type requires.
// Consumed by the verifier and controller (to enforce) and by the buy-side
// (to know what to verify before trusting a response).
type IntegrityProfile struct {
	Payment  PaymentClass
	Content  ContentClass
	Identity IdentityClass
	Scope    ScopeClass
}

// Kind is the resolved capability + integrity descriptor for one spec.type.
type Kind struct {
	// Type is the canonical spec.type string this entry represents ("" for the
	// unset default). Resolve(unknown) returns the http Kind, so its Type is
	// "http", not the unknown input.
	Type string

	// PaymentCopy collapses the type into the three storefront-copy branches
	// ("inference" | "agent" | "http"). Replaces x402.normalizeOfferType.
	PaymentCopy string
	// BazaarShape is the x402 bazaar discovery shape ("chat" | "generic").
	BazaarShape string
	// OpenAPIShape is the controller's OpenAPI path shape
	// ("chat" | "multipart" | "generic").
	OpenAPIShape string
	// CatalogType is the display/catalog label (fallbackOfferType): the type
	// string, or "http" when unset.
	CatalogType string

	// PriceUnits lists the price slot(s) a type conventionally uses, in
	// precedence order. Informational/validation; the live price reader still
	// keys off whichever Price.* field is populated.
	PriceUnits []string

	// SemanticInference mirrors monetizeapi.(*ServiceOffer).IsInference(): true
	// for "" and "inference". Drives model-reconciliation gating and the
	// OpenAPI empty-type edge (IsInference("")==true → chat shape).
	SemanticInference bool
	// ResolvesAgentRef: upstream comes from an Agent CR status, not spec.
	ResolvesAgentRef bool
	// RendersBundle: controller renders a skill bundle server.
	RendersBundle bool
	// OneShotPurchase: price is a total (e.g. perMB × size), not a rate.
	OneShotPurchase bool

	Integrity IntegrityProfile
}

// paymentOnly is the integrity profile for inference/http/agent: an x402
// payment proof, nothing else.
var paymentOnly = IntegrityProfile{
	Payment:  PaymentX402Exact,
	Content:  ContentNone,
	Identity: IdentityNone,
	Scope:    ScopeNone,
}

// kinds is the table. Keys are spec.type strings; "" is the unset default and
// is deliberately distinct from "inference" because the legacy code treats the
// empty type inconsistently — http-presentational (normalizeOfferType,
// fallbackOfferType) yet inference-semantic (IsInference, openAPIPathsForOffer).
// Encoding both faithfully keeps this refactor behavior-preserving.
var kinds = map[string]Kind{
	"": {
		Type: "", PaymentCopy: "http", BazaarShape: "generic", OpenAPIShape: "chat",
		CatalogType: "http", PriceUnits: []string{"perRequest", "perMTok"},
		SemanticInference: true, Integrity: paymentOnly,
	},
	"inference": {
		Type: "inference", PaymentCopy: "inference", BazaarShape: "chat", OpenAPIShape: "chat",
		CatalogType: "inference", PriceUnits: []string{"perRequest", "perMTok"},
		SemanticInference: true, Integrity: paymentOnly,
	},
	"http": {
		Type: "http", PaymentCopy: "http", BazaarShape: "generic", OpenAPIShape: "generic",
		CatalogType: "http", PriceUnits: []string{"perRequest"},
		Integrity: paymentOnly,
	},
	"agent": {
		Type: "agent", PaymentCopy: "agent", BazaarShape: "chat", OpenAPIShape: "chat",
		CatalogType: "agent", PriceUnits: []string{"perRequest", "perMTok"},
		ResolvesAgentRef: true, Integrity: paymentOnly,
	},
	"dataset": {
		Type: "dataset", PaymentCopy: "http", BazaarShape: "generic", OpenAPIShape: "generic",
		CatalogType: "dataset", PriceUnits: []string{"perMB"},
		OneShotPurchase: true,
		Integrity: IntegrityProfile{
			Payment:  PaymentX402Exact,
			Content:  ContentSignedVersionLog,
			Identity: IdentityGroupAuth,
			Scope:    ScopeVersionEntitlement,
		},
	},
	"fine-tuning": {
		Type: "fine-tuning", PaymentCopy: "http", BazaarShape: "generic", OpenAPIShape: "multipart",
		CatalogType: "fine-tuning", PriceUnits: []string{"perHour"},
		Integrity: IntegrityProfile{
			Payment: PaymentX402Exact,
			Content: ContentSignedVersionLog, // reuses the dataset signed-log primitives
		},
	},
	"skill": {
		Type: "skill", PaymentCopy: "http", BazaarShape: "generic", OpenAPIShape: "generic",
		CatalogType: "skill", PriceUnits: []string{"perRequest"},
		RendersBundle: true,
		Integrity: IntegrityProfile{
			Payment: PaymentX402Exact,
			Content: ContentBundleSHA256,
		},
	},
}

// Resolve returns the Kind for a spec.type string. An unrecognized non-empty
// type falls back to the http Kind (payment-only, generic shapes) — matching
// the legacy normalizeOfferType / openAPIPathsForOffer defaults for unknown
// types. The empty string resolves to its own dedicated entry.
func Resolve(t string) Kind {
	if k, ok := kinds[t]; ok {
		return k
	}
	return kinds["http"]
}

// Types returns the canonical service-type strings the registry knows
// (excluding the "" default), for drift checks against the CRD enum.
func Types() []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	return out
}
