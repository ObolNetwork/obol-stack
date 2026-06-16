package schemas

import _ "embed"

// ServiceCatalogJSONSchema is the JSON Schema for the public
// /api/services.json catalog served by sellers.
//
//go:embed service-catalog.schema.json
var ServiceCatalogJSONSchema string

// ServiceCatalogEntry is the JSON representation of a ServiceOffer for the
// public storefront and for machine consumers constructing x402 payments.
//
// Stable wire schema: agents rely on asset.eip712Domain.name and
// asset.eip712Domain.version to construct ERC-3009 or Permit2 signatures.
// Do not rename fields without coordinating with buy.py and downstream agents.
type ServiceCatalogEntry struct {
	Name             string               `json:"name"`
	Namespace        string               `json:"namespace"`
	Type             string               `json:"type"`
	Model            string               `json:"model,omitempty"`
	Endpoint         string               `json:"endpoint"`
	Price            string               `json:"price"`
	PriceRaw         string               `json:"priceRaw,omitempty"`
	PriceUnit        string               `json:"priceUnit,omitempty"`
	PriceAtomicUnits string               `json:"priceAtomicUnits,omitempty"`
	PayTo            string               `json:"payTo"`
	Network          string               `json:"network"`
	CAIP2Network     string               `json:"caip2Network,omitempty"`
	ChainID          int64                `json:"chainId,omitempty"`
	Asset            *ServiceCatalogAsset `json:"asset,omitempty"`
	Description      string               `json:"description"`
	// Skills are the OASF / buy-x402 skill names this offer advertises.
	// For type=agent offers it mirrors AgentResolution.Skills (the
	// resolved allow-list from the linked Agent CR); for non-agent
	// offers it mirrors spec.registration.skills. Surfaced as pills on
	// the storefront ServiceCard.
	Skills []string `json:"skills,omitempty"`
	IsDemo bool     `json:"isDemo"`

	// RegistrationPending is true when the offer is operationally ready
	// (route published, payment gate active, upstream healthy) but its
	// ERC-8004 on-chain registration tx hasn't landed yet. The storefront
	// should surface these with a "registration pending" badge so consumers
	// know the offer is usable for x402 payments today, even though
	// ERC-8004 discovery via the chain still resolves to the prior state.
	RegistrationPending bool `json:"registrationPending,omitempty"`

	// DrainEndsAt is the RFC3339 timestamp at which the offer's
	// HTTPRoute will be removed. Set ONLY when the offer is draining.
	// Consumers detect a drain window with `if (entry.drainEndsAt)`:
	// active offers serialize without this field, so the schema stays
	// purely additive vs. pre-drain catalogs. Buyers SHOULD migrate to
	// alternative providers before this time.
	DrainEndsAt string `json:"drainEndsAt,omitempty"`

	// Dataset* fields are populated for type=dataset offers, mirroring how
	// Model is populated for inference/agent. They expose the pinned,
	// content-addressed dataset version on discovery surfaces so buyers
	// know exactly which artifact (and version) an offer sells. Additive
	// only — see the stable-wire-schema note above.
	DatasetManifestHash string `json:"datasetManifestHash,omitempty"`
	DatasetFileHash     string `json:"datasetFileHash,omitempty"`
	DatasetVersion      string `json:"datasetVersion,omitempty"`
	DatasetSizeBytes    int64  `json:"datasetSizeBytes,omitempty"`
}

// ServiceCatalogAsset describes the settlement token resolved for a catalog
// entry. It mirrors the x402 asset metadata consumers need for signing.
type ServiceCatalogAsset struct {
	Address        string                      `json:"address,omitempty"`
	Symbol         string                      `json:"symbol,omitempty"`
	Decimals       int64                       `json:"decimals,omitempty"`
	TransferMethod string                      `json:"transferMethod,omitempty"`
	EIP712Domain   *ServiceCatalogEIP712Domain `json:"eip712Domain,omitempty"`
}

// ServiceCatalogEIP712Domain is the signing domain agents must use when
// pre-signing payment authorizations. This is not always the same as the
// human-readable token name returned by the token contract.
type ServiceCatalogEIP712Domain struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
