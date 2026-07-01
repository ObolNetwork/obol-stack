package serviceoffercontroller

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/x402"
)

// TestCatalogUSDCMatchesVerifierChain guards against the EIP-712 USDC domain
// name (and version) drifting between the TWO independent Go sources that must
// agree: the catalog renderer's defaultUSDCForNetwork (what /api/services.json
// advertises) and x402's chain registry (what the 402 advertises and the buyer
// signs under). They disagreed once — chains.go said "USD Coin" for base-sepolia
// while the catalog already said the correct "USDC" — which silently broke
// host-side EIP-3009 signatures against a real facilitator and kept recurring
// because each source was hand-maintained.
//
// x402's TestUSDCDomainSeparatorsMatchOnChain pins the registry to the on-chain
// value; this test pins the catalog and the registry to EACH OTHER, so a future
// edit to one without the other fails offline at `go test`.
func TestCatalogUSDCMatchesVerifierChain(t *testing.T) {
	for _, net := range []string{"base", "base-sepolia", "ethereum"} {
		t.Run(net, func(t *testing.T) {
			cat, ok := defaultUSDCForNetwork(net)
			if !ok || cat.EIP712Domain == nil {
				t.Fatalf("catalog has no USDC EIP-712 domain for %q", net)
			}
			ci, err := x402.ResolveChainInfo(net)
			if err != nil {
				t.Fatalf("x402.ResolveChainInfo(%q): %v", net, err)
			}
			if cat.EIP712Domain.Name != ci.EIP3009Name {
				t.Errorf("%s EIP-712 name drift: catalog=%q vs verifier=%q — both must equal the on-chain token domain (base-sepolia is \"USDC\", mainnet is \"USD Coin\")",
					net, cat.EIP712Domain.Name, ci.EIP3009Name)
			}
			if cat.EIP712Domain.Version != ci.EIP3009Version {
				t.Errorf("%s EIP-712 version drift: catalog=%q vs verifier=%q",
					net, cat.EIP712Domain.Version, ci.EIP3009Version)
			}
		})
	}
}
