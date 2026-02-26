// Package schemas provides shared type definitions aligned with the x402 and
// ERC-8004 ecosystems. These types are the canonical source for ServiceOffer
// CRD fields, verifier config, CLI flag mapping, and reconciler logic.
//
// Field names are chosen to match the x402 PaymentRequirements wire format
// where possible (payTo, network, scheme, maxTimeoutSeconds). Human-friendly
// values (e.g., "base-sepolia" instead of CAIP-2 "eip155:84532") are used in
// CRD specs; the reconciler translates to wire format at runtime.
package schemas

// PaymentTerms defines x402 payment requirements for a ServiceOffer.
// Field names align with x402 PaymentRequirements (V2).
type PaymentTerms struct {
	// Scheme is the x402 payment scheme. Default: "exact".
	Scheme string `json:"scheme,omitempty" yaml:"scheme,omitempty"`

	// Network is the chain identifier (human-friendly, e.g., "base-sepolia").
	// The reconciler resolves to CAIP-2 format (e.g., "eip155:84532").
	Network string `json:"network" yaml:"network"`

	// PayTo is the USDC recipient wallet address (0x-prefixed EVM address).
	PayTo string `json:"payTo" yaml:"payTo"`

	// MaxTimeoutSeconds is the payment validity window. Default: 300.
	MaxTimeoutSeconds int `json:"maxTimeoutSeconds,omitempty" yaml:"maxTimeoutSeconds,omitempty"`

	// Price defines the pricing model (type-specific).
	Price PriceTable `json:"price" yaml:"price"`
}

// PriceTable holds per-unit prices in USDC as human-readable decimal strings.
// Which fields are applicable depends on the ServiceOffer type.
//
// x402 wire format uses amounts in smallest units (e.g., "1000000" = $1.00 USDC
// with 6 decimals). The reconciler converts from human-readable to wire format.
type PriceTable struct {
	// PerRequest is a flat per-request price in USDC. Applicable to all types.
	// This is the amount passed to the x402 verifier as-is.
	PerRequest string `json:"perRequest,omitempty" yaml:"perRequest,omitempty"`

	// PerMTok is the price per million tokens in USDC. Inference only.
	// Metering layer converts token counts to request-level charges.
	PerMTok string `json:"perMTok,omitempty" yaml:"perMTok,omitempty"`

	// PerHour is the price per compute-hour in USDC. Fine-tuning only.
	PerHour string `json:"perHour,omitempty" yaml:"perHour,omitempty"`

	// PerEpoch is the price per training epoch in USDC. Fine-tuning only.
	PerEpoch string `json:"perEpoch,omitempty" yaml:"perEpoch,omitempty"`
}

// EffectiveRequestPrice returns the per-request price to use for x402 gating.
// If PerRequest is set, it is returned directly. Otherwise falls back to
// PerMTok (which requires metering to convert, so returns "0" as a sentinel).
func (p PriceTable) EffectiveRequestPrice() string {
	if p.PerRequest != "" {
		return p.PerRequest
	}
	// When only per-MTok pricing is set, the x402 gate uses a zero amount
	// and metering settles the actual cost post-request. For now, fall back
	// to PerMTok as a direct price (close enough for early implementation).
	if p.PerMTok != "" {
		return p.PerMTok
	}
	if p.PerHour != "" {
		return p.PerHour
	}
	return "0"
}
