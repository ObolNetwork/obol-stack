// Package schemas provides shared type definitions aligned with the x402 and
// ERC-8004 ecosystems. These types are the canonical source for ServiceOffer
// CRD fields, verifier config, CLI flag mapping, and reconciler logic.
//
// Field names are chosen to match the x402 PaymentRequirements wire format
// where possible (payTo, network, scheme, maxTimeoutSeconds). Human-friendly
// values (e.g., "base-sepolia" instead of CAIP-2 "eip155:84532") are used in
// CRD specs; the reconciler translates to wire format at runtime.
package schemas

import (
	"strings"

	"github.com/shopspring/decimal"
)

// ApproxTokensPerRequest is the temporary phase-1 token estimate used to
// convert per-million-token pricing into an x402 per-request charge.
const ApproxTokensPerRequest = 1000

// ApproxMinutesPerRequest is the temporary phase-1 time estimate used to
// convert per-hour pricing into an x402 per-request charge.  Based on the
// autoresearch 5-minute experiment budget.
const ApproxMinutesPerRequest = 5

var (
	approxTokensPerRequestDecimal  = decimal.NewFromInt(ApproxTokensPerRequest)
	minutesPerHour                 = decimal.NewFromInt(60)
	approxMinutesPerRequestDecimal = decimal.NewFromInt(ApproxMinutesPerRequest)
)

// PaymentMethodCrypto gates the offer with x402 on-chain stablecoin
// settlement. It is the default when PaymentTerms.Method is empty.
const PaymentMethodCrypto = "crypto"

// PaymentMethodCard gates the offer with an MPP credit-card method
// (Stripe stripe.charge), settled off-chain into PaymentTerms.Card.Account.
const PaymentMethodCard = "card"

// PaymentTerms defines payment requirements for a ServiceOffer. Field
// names align with x402 PaymentRequirements (V2) for the crypto method;
// the Card block carries the off-chain credit-card (MPP/Stripe) terms.
type PaymentTerms struct {
	// Method selects the payment method: "crypto" (x402 on-chain, default)
	// or "card" (MPP Stripe). Empty is treated as "crypto".
	Method string `json:"method,omitempty" yaml:"method,omitempty"`

	// Scheme is the x402 payment scheme. Default: "exact". Crypto only.
	Scheme string `json:"scheme,omitempty" yaml:"scheme,omitempty"`

	// Network is the chain identifier (human-friendly, e.g., "base-sepolia").
	// The reconciler resolves to CAIP-2 format (e.g., "eip155:84532").
	Network string `json:"network" yaml:"network"`

	// PayTo is the USDC recipient wallet address (0x-prefixed EVM address).
	PayTo string `json:"payTo" yaml:"payTo"`

	// MaxTimeoutSeconds is the payment validity window. Default: 300.
	MaxTimeoutSeconds int `json:"maxTimeoutSeconds,omitempty" yaml:"maxTimeoutSeconds,omitempty"`

	// Asset defines the token metadata used for x402 settlement. When omitted,
	// the verifier falls back to the chain default asset (currently USDC).
	// Crypto only.
	Asset AssetTerms `json:"asset,omitempty" yaml:"asset,omitempty"`

	// Card holds off-chain credit-card settlement terms when Method=="card".
	Card *CardTerms `json:"card,omitempty" yaml:"card,omitempty"`

	// Price defines the pricing model (type-specific).
	Price PriceTable `json:"price" yaml:"price"`
}

// EffectiveMethod returns the payment method, defaulting an empty value to
// PaymentMethodCrypto so existing crypto offers keep working unchanged.
func (p PaymentTerms) EffectiveMethod() string {
	if p.Method == "" {
		return PaymentMethodCrypto
	}
	return p.Method
}

// CardTerms defines off-chain credit-card settlement terms used when
// PaymentTerms.Method == "card". It mirrors monetizeapi.ServiceOfferCardPayment.
type CardTerms struct {
	// Provider is the card payment provider (only "stripe" today).
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`

	// Account is the destination account that receives settled card funds.
	// For Stripe this is the connected/destination account id (acct_...).
	Account string `json:"account,omitempty" yaml:"account,omitempty"`

	// Currency is the ISO-4217 currency the card is charged in (e.g. "usd").
	Currency string `json:"currency,omitempty" yaml:"currency,omitempty"`

	// NetworkID is the optional Stripe "machine payments" network id,
	// surfaced in the 402 challenge so MPP card clients can mint a token.
	NetworkID string `json:"networkId,omitempty" yaml:"networkId,omitempty"`

	// PaymentMethodTypes are the accepted payment-method types advertised to
	// the client. Defaults to ["card"] at the gateway when empty.
	PaymentMethodTypes []string `json:"paymentMethodTypes,omitempty" yaml:"paymentMethodTypes,omitempty"`
}

const (
	AssetTransferMethodEIP3009 = "eip3009"
	AssetTransferMethodPermit2 = "permit2"
)

// AssetTerms defines token metadata for x402 payment requirements.
type AssetTerms struct {
	// Address is the ERC-20 contract address.
	Address string `json:"address,omitempty" yaml:"address,omitempty"`

	// Symbol is a human-friendly token symbol (e.g. USDC, OBOL).
	Symbol string `json:"symbol,omitempty" yaml:"symbol,omitempty"`

	// Decimals is the token precision in atomic units.
	Decimals int `json:"decimals,omitempty" yaml:"decimals,omitempty"`

	// TransferMethod is the x402 asset transfer method (eip3009 or permit2).
	TransferMethod string `json:"transferMethod,omitempty" yaml:"transferMethod,omitempty"`

	// EIP712Name is the EIP-712 domain name for the token or permit flow.
	EIP712Name string `json:"eip712Name,omitempty" yaml:"eip712Name,omitempty"`

	// EIP712Version is the EIP-712 domain version for the token or permit flow.
	EIP712Version string `json:"eip712Version,omitempty" yaml:"eip712Version,omitempty"`
}

func (a AssetTerms) IsZero() bool {
	return a.Address == "" &&
		a.Symbol == "" &&
		a.Decimals == 0 &&
		a.TransferMethod == "" &&
		a.EIP712Name == "" &&
		a.EIP712Version == ""
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
// If PerRequest is set, it is returned directly. If only PerMTok is set, it is
// temporarily approximated using ApproxTokensPerRequest until exact metering
// exists. Invalid decimal inputs fall back to "0".
func (p PriceTable) EffectiveRequestPrice() string {
	price, err := p.EffectiveRequestPriceE()
	if err != nil {
		return "0"
	}

	return price
}

// EffectiveRequestPriceE returns the per-request price to use for x402 gating.
// It performs the same conversion as EffectiveRequestPrice but returns parsing
// errors to callers that need to validate input.
func (p PriceTable) EffectiveRequestPriceE() (string, error) {
	if p.PerRequest != "" {
		return p.PerRequest, nil
	}

	if p.PerMTok != "" {
		return ApproximateRequestPriceFromPerMTok(p.PerMTok)
	}

	if p.PerHour != "" {
		return ApproximateRequestPriceFromPerHour(p.PerHour)
	}

	return "0", nil
}

// ApproximateRequestPriceFromPerMTok converts a per-million-token price into a
// temporary per-request x402 charge using ApproxTokensPerRequest.
func ApproximateRequestPriceFromPerMTok(perMTok string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(perMTok))
	if err != nil {
		return "", err
	}

	return value.Div(approxTokensPerRequestDecimal).String(), nil
}

// ApproximateRequestPriceFromPerHour converts a per-hour price into a temporary
// per-request x402 charge using ApproxMinutesPerRequest (default 5 min,
// matching the autoresearch experiment budget).
// Formula: perRequest = perHour * (minutesPerRequest / 60)
func ApproximateRequestPriceFromPerHour(perHour string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(perHour))
	if err != nil {
		return "", err
	}

	return value.Mul(approxMinutesPerRequestDecimal).Div(minutesPerHour).String(), nil
}
