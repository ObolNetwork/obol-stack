package x402

import (
	"fmt"
	"math/big"
	"strings"

	x402types "github.com/x402-foundation/x402/go/types"
)

// ChainInfo holds chain-specific configuration for x402 payment gating.
// It maps a human-friendly chain name to the on-wire identifiers the
// facilitator expects (v1 network names, USDC contract address, EIP-3009
// domain parameters).
type ChainInfo struct {
	// Name is the human-friendly identifier used by the CLI (e.g., "base-sepolia").
	Name string

	// NetworkID is the v1 wire-format network name sent to the facilitator.
	NetworkID string

	// CAIP2Network is the v2 wire-format network identifier.
	CAIP2Network string

	// USDCAddress is the USDC token contract address on this chain.
	USDCAddress string

	// Decimals is the token decimal precision (6 for USDC).
	Decimals int

	// EIP3009Name is the EIP-712 domain name for TransferWithAuthorization.
	EIP3009Name string

	// EIP3009Version is the EIP-712 domain version.
	EIP3009Version string
}

// AssetInfo describes the token and EIP-712 metadata used for x402 settlement.
type AssetInfo struct {
	Address        string
	Symbol         string
	Decimals       int
	TransferMethod string
	EIP712Name     string
	EIP712Version  string

	// EIP2612GasSponsoring is true when the token supports ERC20Permit and
	// the facilitator can batch permit() with transferFrom on settle, letting
	// buyers skip the one-time approve(Permit2, max) tx. Surfaced to buyers
	// via the top-level `extensions.eip2612GasSponsoring` field on the 402
	// response.
	EIP2612GasSponsoring bool
}

// Chain constants — USDC addresses verified against x402-foundation/x402/go (formerly coinbase/x402) v2.7.0
// mechanisms/evm/constants.go and on-chain contract deployments.
var (
	ChainBaseMainnet = ChainInfo{
		Name:           "base",
		NetworkID:      "base",
		CAIP2Network:   "eip155:8453",
		USDCAddress:    "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainBaseSepolia = ChainInfo{
		Name:           "base-sepolia",
		NetworkID:      "base-sepolia",
		CAIP2Network:   "eip155:84532",
		USDCAddress:    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainEthereumMainnet = ChainInfo{
		Name:           "ethereum",
		NetworkID:      "ethereum",
		CAIP2Network:   "eip155:1",
		USDCAddress:    "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainPolygonMainnet = ChainInfo{
		Name:           "polygon",
		NetworkID:      "polygon",
		CAIP2Network:   "eip155:137",
		USDCAddress:    "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainPolygonAmoy = ChainInfo{
		Name:           "polygon-amoy",
		NetworkID:      "polygon-amoy",
		CAIP2Network:   "eip155:80002",
		USDCAddress:    "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainAvalancheMainnet = ChainInfo{
		Name:           "avalanche",
		NetworkID:      "avalanche",
		CAIP2Network:   "eip155:43114",
		USDCAddress:    "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainAvalancheFuji = ChainInfo{
		Name:           "avalanche-fuji",
		NetworkID:      "avalanche-fuji",
		CAIP2Network:   "eip155:43113",
		USDCAddress:    "0x5425890298aed601595a70AB815c96711a31Bc65",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainArbitrumOne = ChainInfo{
		Name:           "arbitrum-one",
		NetworkID:      "arbitrum-one",
		CAIP2Network:   "eip155:42161",
		USDCAddress:    "0xaf88d065e77c8cC2239327C5EDb3A432268e5831",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}

	ChainArbitrumSepolia = ChainInfo{
		Name:           "arbitrum-sepolia",
		NetworkID:      "arbitrum-sepolia",
		CAIP2Network:   "eip155:421614",
		USDCAddress:    "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d",
		Decimals:       6,
		EIP3009Name:    "USD Coin",
		EIP3009Version: "2",
	}
)

// NormalizeNetworkID maps a human-friendly chain name to its CAIP-2 network
// identifier. Already-normalized CAIP-2 values are returned as-is.
func NormalizeNetworkID(network string) string {
	lower := strings.ToLower(strings.TrimSpace(network))
	switch lower {
	case "base", "base-mainnet":
		return ChainBaseMainnet.CAIP2Network
	case "base-sepolia":
		return ChainBaseSepolia.CAIP2Network
	case "ethereum", "ethereum-mainnet", "mainnet":
		return ChainEthereumMainnet.CAIP2Network
	case "sepolia":
		return "eip155:11155111"
	case "polygon", "polygon-mainnet":
		return ChainPolygonMainnet.CAIP2Network
	case "polygon-amoy":
		return ChainPolygonAmoy.CAIP2Network
	case "avalanche", "avalanche-mainnet":
		return ChainAvalancheMainnet.CAIP2Network
	case "avalanche-fuji":
		return ChainAvalancheFuji.CAIP2Network
	case "arbitrum", "arbitrum-one":
		return ChainArbitrumOne.CAIP2Network
	case "arbitrum-sepolia":
		return ChainArbitrumSepolia.CAIP2Network
	default:
		return network
	}
}

// ResolveChainInfo maps a human-friendly chain name to its ChainInfo.
// Phase 2 renames this to ResolveChain after deleting the old one in config.go.
func ResolveChainInfo(name string) (ChainInfo, error) {
	// Accept both legacy names ("base-sepolia") and CAIP-2 ids ("eip155:84532").
	// fd95dc5 normalized RouteRule.Network to CAIP-2 form before this map was
	// taught to resolve them, which broke matchPaidRouteFull's chain lookup
	// (silent 404 for every paid request — root cause of flow-11 step 21
	// regression). Both forms must work because cfg.Chain (operator-supplied)
	// stays in the legacy form while RouteRule.Network is normalized.
	switch name {
	case "base", "base-mainnet", ChainBaseMainnet.CAIP2Network:
		return ChainBaseMainnet, nil
	case "base-sepolia", ChainBaseSepolia.CAIP2Network:
		return ChainBaseSepolia, nil
	case "ethereum", "ethereum-mainnet", "mainnet", ChainEthereumMainnet.CAIP2Network:
		return ChainEthereumMainnet, nil
	case "polygon", "polygon-mainnet", ChainPolygonMainnet.CAIP2Network:
		return ChainPolygonMainnet, nil
	case "polygon-amoy", ChainPolygonAmoy.CAIP2Network:
		return ChainPolygonAmoy, nil
	case "avalanche", "avalanche-mainnet", ChainAvalancheMainnet.CAIP2Network:
		return ChainAvalancheMainnet, nil
	case "avalanche-fuji", ChainAvalancheFuji.CAIP2Network:
		return ChainAvalancheFuji, nil
	case "arbitrum-one", "arbitrum", ChainArbitrumOne.CAIP2Network:
		return ChainArbitrumOne, nil
	case "arbitrum-sepolia", ChainArbitrumSepolia.CAIP2Network:
		return ChainArbitrumSepolia, nil
	default:
		return ChainInfo{}, fmt.Errorf(
			"unsupported chain: %s (use: base, base-sepolia, ethereum, polygon, polygon-amoy, avalanche, avalanche-fuji, arbitrum-one, arbitrum-sepolia, or any eip155:<chainId>)",
			name,
		)
	}
}

// decimalToAtomic converts a decimal token amount (e.g. "0.001") to atomic
// units using big.Float with 128-bit precision to avoid floating-point
// truncation (e.g. 0.001 * 1e6 must produce 1000, not 999).
func decimalToAtomic(amount string, decimals int) string {
	amountFloat, _, _ := new(big.Float).SetPrec(128).Parse(amount, 10)
	multiplier := new(big.Float).SetPrec(128).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil),
	)
	atomicFloat := new(big.Float).SetPrec(128).Mul(amountFloat, multiplier)
	// Add 0.5 before truncating to int so we round to nearest.
	atomicFloat.Add(atomicFloat, new(big.Float).SetPrec(128).SetFloat64(0.5))
	atomicInt, _ := atomicFloat.Int(nil)
	return atomicInt.String()
}

// DefaultAsset returns the default settlement asset for a chain.
func (c ChainInfo) DefaultAsset() AssetInfo {
	return AssetInfo{
		Address:        c.USDCAddress,
		Symbol:         "USDC",
		Decimals:       c.Decimals,
		TransferMethod: "eip3009",
		EIP712Name:     c.EIP3009Name,
		EIP712Version:  c.EIP3009Version,
	}
}

// ResolveAssetInfo applies any route-level asset overrides on top of the
// chain's default settlement asset.
func ResolveAssetInfo(chain ChainInfo, rule *RouteRule) AssetInfo {
	asset := chain.DefaultAsset()
	if rule == nil {
		return asset
	}
	if rule.AssetAddress != "" {
		asset.Address = rule.AssetAddress
	}
	if rule.AssetSymbol != "" {
		asset.Symbol = rule.AssetSymbol
	}
	if rule.AssetDecimals > 0 {
		asset.Decimals = rule.AssetDecimals
	}
	if rule.AssetTransferMethod != "" {
		asset.TransferMethod = rule.AssetTransferMethod
	}
	if rule.EIP712Name != "" {
		asset.EIP712Name = rule.EIP712Name
	}
	if rule.EIP712Version != "" {
		asset.EIP712Version = rule.EIP712Version
	}

	// Re-derive registry-driven flags (gasless approve, etc.) from the token
	// registry. The CR doesn't carry these — the token registry is the source
	// of truth — so we look up by (symbol, chain) and require the resolved
	// address to match before honoring the flag, to avoid a malicious offer
	// claiming a known symbol with a foreign contract address.
	if entry, ok := ResolveToken(asset.Symbol, chain.Name); ok {
		if strings.EqualFold(entry.Address, asset.Address) {
			asset.EIP2612GasSponsoring = entry.EIP2612GasSponsoring
		}
	}
	return asset
}

// BuildExtensionsForAsset returns the top-level x402 extensions that should be
// advertised on the 402 response for a given asset. Currently sets
// `eip2612GasSponsoring` when the asset supports gasless approve via Permit2
// + ERC20Permit (e.g. OBOL on mainnet). Returns nil when no extensions apply
// — the caller's `omitempty` JSON tag drops the field entirely.
func BuildExtensionsForAsset(asset AssetInfo) map[string]any {
	if !asset.EIP2612GasSponsoring {
		return nil
	}
	return map[string]any{
		"eip2612GasSponsoring": map[string]any{},
	}
}

// DefaultMaxTimeoutSeconds is the fallback settle window when neither the
// ServiceOffer nor a static route specifies one. Wide enough for any
// non-reasoning chat completion; reasoning models and long agent runs should
// set ServiceOffer.spec.payment.maxTimeoutSeconds explicitly.
const DefaultMaxTimeoutSeconds int64 = 600

// MaxMaxTimeoutSeconds caps operator-set values at 24h. The auth signature is
// nonce-bound to a single recipient + amount, so the replay surface doesn't
// grow with the window — but a runaway value (typo, accidental ms→s) would
// hand a facilitator an effectively unbounded settle window, so we clamp.
const MaxMaxTimeoutSeconds int64 = 24 * 60 * 60

// ClampMaxTimeoutSeconds resolves the effective settle window from a
// possibly-zero spec value. Zero or negative → DefaultMaxTimeoutSeconds;
// values above the 24h cap clamp down (the caller is expected to have logged
// the operator-set value already).
func ClampMaxTimeoutSeconds(n int64) int64 {
	if n <= 0 {
		return DefaultMaxTimeoutSeconds
	}
	if n > MaxMaxTimeoutSeconds {
		return MaxMaxTimeoutSeconds
	}
	return n
}

// BuildV1Requirement creates a v1 PaymentRequirementsV1 for USDC payment on
// the given chain. amount is the decimal USDC amount (e.g., "0.001" = $0.001).
func BuildV1Requirement(chain ChainInfo, amount, recipientAddress string, maxTimeoutSeconds int64) x402types.PaymentRequirementsV1 {
	asset := chain.DefaultAsset()
	return x402types.PaymentRequirementsV1{
		Scheme:            "exact",
		Network:           chain.NetworkID,
		MaxAmountRequired: decimalToAtomic(amount, asset.Decimals),
		Asset:             asset.Address,
		PayTo:             recipientAddress,
		MaxTimeoutSeconds: int(ClampMaxTimeoutSeconds(maxTimeoutSeconds)),
	}
}

// BuildV2Requirement creates a v2 PaymentRequirements for USDC payment on the
// given chain. amount is the decimal USDC amount (e.g. "0.001" = $0.001).
func BuildV2Requirement(chain ChainInfo, amount, recipientAddress string, maxTimeoutSeconds int64) x402types.PaymentRequirements {
	return BuildV2RequirementWithAsset(chain, chain.DefaultAsset(), amount, recipientAddress, maxTimeoutSeconds)
}

// BuildV2RequirementWithAsset creates a v2 PaymentRequirements for the given
// chain and settlement asset. Pass maxTimeoutSeconds=0 to fall back to
// DefaultMaxTimeoutSeconds; operator-set values are clamped to MaxMaxTimeoutSeconds.
func BuildV2RequirementWithAsset(chain ChainInfo, asset AssetInfo, amount, recipientAddress string, maxTimeoutSeconds int64) x402types.PaymentRequirements {
	return x402types.PaymentRequirements{
		Scheme:            "exact",
		Network:           chain.CAIP2Network,
		Amount:            decimalToAtomic(amount, asset.Decimals),
		Asset:             asset.Address,
		PayTo:             recipientAddress,
		MaxTimeoutSeconds: int(ClampMaxTimeoutSeconds(maxTimeoutSeconds)),
		Extra: map[string]interface{}{
			"name":                asset.EIP712Name,
			"version":             asset.EIP712Version,
			"assetTransferMethod": asset.TransferMethod,
		},
	}
}
