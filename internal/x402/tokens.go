package x402

import (
	"sort"
	"strings"
)

// TokenEntry defines a whitelisted token's per-chain metadata.
// Each entry carries everything the sell-side, verifier, and buy-side need
// to construct and validate x402 payment requirements for this token.
type TokenEntry struct {
	// Address is the ERC-20 contract address on the target chain.
	Address string

	// Symbol is the human-friendly token symbol (e.g. "USDC", "OBOL").
	Symbol string

	// Decimals is the token precision in atomic units (6 for USDC, 18 for OBOL).
	Decimals int

	// TransferMethod is the x402 asset transfer method.
	// "eip3009" — token natively implements transferWithAuthorization (USDC, EURC).
	// "permit2" — uses Uniswap Permit2 for authorization (any ERC-20).
	TransferMethod string

	// EIP712Name is the EIP-712 domain name for signing.
	EIP712Name string

	// EIP712Version is the EIP-712 domain version for signing.
	EIP712Version string

	// EIP2612GasSponsoring is true when the token implements EIP-2612
	// (ERC20Permit) and the configured facilitator can batch the permit() call
	// with the on-chain transferFrom during settlement, sponsoring gas. When
	// true, the seller's 402 advertises `eip2612GasSponsoring` in extensions
	// so buyers skip the one-time approve(Permit2, max) step. Only relevant
	// for permit2 transfer methods.
	EIP2612GasSponsoring bool
}

// tokenRegistry maps (uppercased token symbol, canonical chain name) → TokenEntry.
// To add a new token, add entries here and tests in tokens_test.go.
// See .agents/skills/obol-stack-dev/SKILL.md "Adding a New Payment Token" for the full checklist.
var tokenRegistry = map[string]map[string]TokenEntry{
	"USDC": {
		"base":             {Address: ChainBaseMainnet.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"base-sepolia":     {Address: ChainBaseSepolia.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USDC", EIP712Version: "2"},
		"ethereum":         {Address: ChainEthereumMainnet.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"polygon":          {Address: ChainPolygonMainnet.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"polygon-amoy":     {Address: ChainPolygonAmoy.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"avalanche":        {Address: ChainAvalancheMainnet.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"avalanche-fuji":   {Address: ChainAvalancheFuji.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"arbitrum-one":     {Address: ChainArbitrumOne.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
		"arbitrum-sepolia": {Address: ChainArbitrumSepolia.USDCAddress, Symbol: "USDC", Decimals: 6, TransferMethod: "eip3009", EIP712Name: "USD Coin", EIP712Version: "2"},
	},
	"OBOL": {
		// OBOL implements ERC20Permit ("Obol Network", v1). The Obol-operated
		// facilitator at https://x402.gcp.obol.tech batches permit() with
		// transferFrom on settle, so buyers don't need a one-time approve.
		"ethereum":     {Address: "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7", Symbol: "OBOL", Decimals: 18, TransferMethod: "permit2", EIP712Name: "Obol Network", EIP712Version: "1", EIP2612GasSponsoring: true},
		"base-sepolia": {Address: "0x0a09371a8b011d5110656ceBCc70603e53FD2c78", Symbol: "OBOL", Decimals: 18, TransferMethod: "permit2", EIP712Name: "Obol Network", EIP712Version: "1", EIP2612GasSponsoring: true},
	},
}

// normalizeChainAlias maps chain name aliases to the canonical names used as
// registry keys. This mirrors the alias handling in ResolveChainInfo.
func normalizeChainAlias(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "base-mainnet":
		return "base"
	case "ethereum-mainnet", "mainnet":
		return "ethereum"
	case "polygon-mainnet":
		return "polygon"
	case "avalanche-mainnet":
		return "avalanche"
	case "arbitrum":
		return "arbitrum-one"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// ResolveToken looks up a token by name and chain. Returns the token entry
// and true if found, or a zero entry and false if the token is not registered
// for the given chain. Token name is matched case-insensitively.
func ResolveToken(tokenName, chainName string) (TokenEntry, bool) {
	chains, ok := tokenRegistry[strings.ToUpper(strings.TrimSpace(tokenName))]
	if !ok {
		return TokenEntry{}, false
	}
	entry, ok := chains[normalizeChainAlias(chainName)]
	return entry, ok
}

// SupportedTokens returns a sorted slice of all registered token symbols.
func SupportedTokens() []string {
	tokens := make([]string, 0, len(tokenRegistry))
	for name := range tokenRegistry {
		tokens = append(tokens, name)
	}
	sort.Strings(tokens)
	return tokens
}

// TokensOnChain returns a sorted slice of token symbols registered for the
// given chain. Returns an empty slice when no tokens are registered for that
// chain (or the chain is unknown).
func TokensOnChain(chainName string) []string {
	canonical := normalizeChainAlias(chainName)
	tokens := make([]string, 0, len(tokenRegistry))
	for name, chains := range tokenRegistry {
		if _, ok := chains[canonical]; ok {
			tokens = append(tokens, name)
		}
	}
	sort.Strings(tokens)
	return tokens
}

// ChainsForToken returns a sorted slice of canonical chain names on which the
// given token is registered. Returns an empty slice for unknown tokens.
func ChainsForToken(tokenName string) []string {
	chains, ok := tokenRegistry[strings.ToUpper(strings.TrimSpace(tokenName))]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(chains))
	for name := range chains {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
