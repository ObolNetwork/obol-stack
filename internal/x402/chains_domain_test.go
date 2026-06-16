package x402

import (
	"testing"

	gethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// goldenUSDCDomainSeparators pins each chain's USDC EIP-712 DOMAIN_SEPARATOR as
// read from the live token contract:
//
//	cast call <USDCAddress> "DOMAIN_SEPARATOR()(bytes32)" --rpc-url <chain-rpc>
//
// The domain separator is a deterministic function of the four fields a buyer
// signs under — (name, version, chainId, verifyingContract). Pinning it turns
// the recurring base-sepolia "USD Coin" vs "USDC" EIP-712 *name* bug into an
// OFFLINE `go test` failure: a wrong name yields a different separator, so an
// EIP-3009 signature built from this registry would be rejected by a real
// facilitator (FiatToken's SignatureChecker). The bug bit ~repeatedly because
// nothing tied the hand-maintained name string to the on-chain domain; this
// closes that loop. Capture and add a chain's value here as you verify it.
var goldenUSDCDomainSeparators = []struct {
	name   string
	chain  ChainInfo
	golden string
}{
	// Base-Sepolia USDC is FiatTokenV2_2 — domain name "USDC", NOT "USD Coin".
	{"base-sepolia", ChainBaseSepolia, "0x71f17a3b2ff373b803d70a5a07c046c1a2bc8e89c09ef722fcb047abe94c9818"},
}

func TestUSDCDomainSeparatorsMatchOnChain(t *testing.T) {
	for _, tc := range goldenUSDCDomainSeparators {
		t.Run(tc.name, func(t *testing.T) {
			got, err := usdcDomainSeparator(tc.chain)
			if err != nil {
				t.Fatalf("compute domain separator: %v", err)
			}
			if got != tc.golden {
				t.Errorf("%s USDC EIP-712 domain separator = %s, want on-chain %s\n"+
					"  registry has EIP3009Name=%q version=%q addr=%s — the name almost certainly\n"+
					"  disagrees with the on-chain token domain (base-sepolia FiatTokenV2_2 is \"USDC\",\n"+
					"  mainnet USDC is \"USD Coin\"). A real facilitator will reject signatures built here.",
					tc.name, got, tc.golden, tc.chain.EIP3009Name, tc.chain.EIP3009Version, tc.chain.USDCAddress)
			}
		})
	}
}

// usdcDomainSeparator computes the EIP-712 domain separator a buyer signs under
// for ci's USDC via the SAME apitypes path SignExactPayment uses, so this guards
// the exact value that reaches a facilitator — not a re-derivation that could
// drift from the signer.
func usdcDomainSeparator(ci ChainInfo) (string, error) {
	chainID, err := chainIDFromNetwork(ci.CAIP2Network)
	if err != nil {
		return "", err
	}
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		Domain: apitypes.TypedDataDomain{
			Name:              ci.EIP3009Name,
			Version:           ci.EIP3009Version,
			ChainId:           gethmath.NewHexOrDecimal256(chainID),
			VerifyingContract: ci.USDCAddress,
		},
	}
	sep, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		return "", err
	}
	return sep.String(), nil
}
