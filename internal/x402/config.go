package x402

import (
	"fmt"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

// PricingConfig is the top-level configuration for the x402 ForwardAuth verifier.
// It defines global payment parameters and per-route pricing rules.
type PricingConfig struct {
	// Wallet is the USDC recipient address for all payments.
	Wallet string `yaml:"wallet"`

	// Chain is the blockchain network name (e.g., "base-sepolia", "base").
	Chain string `yaml:"chain"`

	// FacilitatorURL is the x402 facilitator service URL.
	FacilitatorURL string `yaml:"facilitatorURL"`

	// VerifyOnly skips blockchain settlement after successful verification.
	VerifyOnly bool `yaml:"verifyOnly"`

	// Routes defines per-route pricing rules. First match wins.
	Routes []RouteRule `yaml:"routes"`
}

// RouteRule maps a URL pattern to x402 payment requirements.
// Per-route fields (PayTo, Network) override the global PricingConfig values
// when set, enabling multiple ServiceOffers with different wallets/chains.
type RouteRule struct {
	// Pattern is a path matching pattern. Supports:
	//   - Exact match: "/health"
	//   - Prefix match: "/rpc/*" (matches /rpc/anything)
	//   - Glob match: "/inference-*/v1/*"
	Pattern string `yaml:"pattern"`

	// Price is the USDC amount per request (e.g., "0.0001").
	Price string `yaml:"price"`

	// Description is a human-readable label for this route (optional).
	Description string `yaml:"description"`

	// PayTo overrides the global wallet for this route (x402: payTo).
	// If empty, falls back to PricingConfig.Wallet.
	PayTo string `yaml:"payTo,omitempty"`

	// Network overrides the global chain for this route (human-friendly).
	// If empty, falls back to PricingConfig.Chain.
	Network string `yaml:"network,omitempty"`

	// AssetAddress overrides the default token contract address for this route.
	AssetAddress string `yaml:"assetAddress,omitempty"`

	// AssetSymbol records the token symbol used for this route.
	AssetSymbol string `yaml:"assetSymbol,omitempty"`

	// AssetDecimals overrides the token decimals for this route.
	AssetDecimals int `yaml:"assetDecimals,omitempty"`

	// AssetTransferMethod overrides the x402 transfer method for this route.
	AssetTransferMethod string `yaml:"assetTransferMethod,omitempty"`

	// EIP712Name overrides the EIP-712 domain name for this route's asset.
	EIP712Name string `yaml:"eip712Name,omitempty"`

	// EIP712Version overrides the EIP-712 domain version for this route's asset.
	EIP712Version string `yaml:"eip712Version,omitempty"`

	// UpstreamAuth is injected as the Authorization header on approved requests.
	// The x402-verifier sets this header in its 200 response; Traefik copies it
	// to the forwarded request via authResponseHeaders. This lets the upstream
	// (e.g., LiteLLM) authenticate the request without exposing the key to buyers.
	UpstreamAuth string `yaml:"upstreamAuth,omitempty"`

	// PriceModel records which price field produced the enforced request price.
	// It is metadata only; the verifier always enforces Price.
	PriceModel string `yaml:"priceModel,omitempty"`

	// PerMTok stores the original per-million-token price when Price was
	// approximated for phase 1 request-based gating.
	PerMTok string `yaml:"perMTok,omitempty"`

	// ApproxTokensPerRequest stores the fixed token estimate used to derive
	// Price from PerMTok during phase 1.
	ApproxTokensPerRequest int `yaml:"approxTokensPerRequest,omitempty"`

	// OfferNamespace identifies the originating ServiceOffer namespace.
	OfferNamespace string `yaml:"offerNamespace,omitempty"`

	// OfferName identifies the originating ServiceOffer name.
	OfferName string `yaml:"offerName,omitempty"`
}

// LoadConfig reads and parses a pricing configuration YAML file.
func LoadConfig(path string) (*PricingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg PricingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Apply defaults.
	if cfg.FacilitatorURL == "" {
		cfg.FacilitatorURL = DefaultFacilitatorURL
	}

	if cfg.Chain == "" {
		cfg.Chain = "base-sepolia"
	}

	if err := ValidateFacilitatorURL(cfg.FacilitatorURL); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateFacilitatorURL checks that the facilitator URL uses HTTPS.
// Payment proofs sent over plain HTTP could be intercepted.
// Loopback addresses (localhost, 127.0.0.1, [::1]) and k3d/Docker internal
// addresses are exempted for local development and testing.
func ValidateFacilitatorURL(u string) error {
	parsed, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("invalid facilitator URL %q: %w", u, err)
	}

	if parsed.Scheme == "https" {
		return nil
	}

	if parsed.Scheme != "http" {
		return fmt.Errorf("facilitator URL must use HTTPS (except localhost): %q", u)
	}

	// Allow loopback and container-internal hostnames for local dev/testing.
	host := parsed.Hostname()
	switch host {
	case "localhost", "127.0.0.1", "::1",
		"host.k3d.internal", "host.docker.internal":
		return nil
	}

	return fmt.Errorf("facilitator URL must use HTTPS (except localhost): %q", u)
}
