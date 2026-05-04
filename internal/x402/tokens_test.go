package x402

import (
	"testing"
)

func TestResolveToken_USDC_AllChains(t *testing.T) {
	chains := []struct {
		name    string
		wantAddr string
	}{
		{"base", ChainBaseMainnet.USDCAddress},
		{"base-sepolia", ChainBaseSepolia.USDCAddress},
		{"ethereum", ChainEthereumMainnet.USDCAddress},
		{"polygon", ChainPolygonMainnet.USDCAddress},
		{"polygon-amoy", ChainPolygonAmoy.USDCAddress},
		{"avalanche", ChainAvalancheMainnet.USDCAddress},
		{"avalanche-fuji", ChainAvalancheFuji.USDCAddress},
		{"arbitrum-one", ChainArbitrumOne.USDCAddress},
		{"arbitrum-sepolia", ChainArbitrumSepolia.USDCAddress},
	}

	for _, tc := range chains {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := ResolveToken("USDC", tc.name)
			if !ok {
				t.Fatalf("ResolveToken(USDC, %s) not found", tc.name)
			}
			if entry.Address != tc.wantAddr {
				t.Errorf("Address = %q, want %q", entry.Address, tc.wantAddr)
			}
			if entry.Decimals != 6 {
				t.Errorf("Decimals = %d, want 6", entry.Decimals)
			}
			if entry.TransferMethod != "eip3009" {
				t.Errorf("TransferMethod = %q, want eip3009", entry.TransferMethod)
			}
		})
	}
}

func TestResolveToken_OBOL_Ethereum(t *testing.T) {
	entry, ok := ResolveToken("OBOL", "ethereum")
	if !ok {
		t.Fatal("ResolveToken(OBOL, ethereum) not found")
	}
	if entry.Address != "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7" {
		t.Errorf("Address = %q", entry.Address)
	}
	if entry.Decimals != 18 {
		t.Errorf("Decimals = %d, want 18", entry.Decimals)
	}
	if entry.TransferMethod != "permit2" {
		t.Errorf("TransferMethod = %q, want permit2", entry.TransferMethod)
	}
	if entry.EIP712Name != "Obol Network" {
		t.Errorf("EIP712Name = %q", entry.EIP712Name)
	}
	if entry.EIP712Version != "1" {
		t.Errorf("EIP712Version = %q", entry.EIP712Version)
	}
	if !entry.EIP2612GasSponsoring {
		t.Error("EIP2612GasSponsoring = false, want true (OBOL implements ERC20Permit and the Obol facilitator batches permit() with transferFrom)")
	}
}

func TestResolveToken_USDC_NoGasSponsoring(t *testing.T) {
	// USDC settles via EIP-3009 (transferWithAuthorization), not Permit2,
	// so EIP2612GasSponsoring is irrelevant and must stay false to avoid
	// advertising a no-op extension on USDC routes.
	for _, chain := range []string{"ethereum", "base", "base-sepolia"} {
		entry, ok := ResolveToken("USDC", chain)
		if !ok {
			t.Fatalf("ResolveToken(USDC, %s) not found", chain)
		}
		if entry.EIP2612GasSponsoring {
			t.Errorf("USDC on %s: EIP2612GasSponsoring = true, want false", chain)
		}
	}
}

func TestResolveToken_OBOL_NotOnBase(t *testing.T) {
	_, ok := ResolveToken("OBOL", "base")
	if ok {
		t.Error("ResolveToken(OBOL, base) should return false — OBOL not deployed on Base")
	}
}

func TestResolveToken_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"usdc", "Usdc", "USDC", " usdc "} {
		if _, ok := ResolveToken(name, "base"); !ok {
			t.Errorf("ResolveToken(%q, base) not found", name)
		}
	}
	for _, name := range []string{"obol", "Obol", "OBOL"} {
		if _, ok := ResolveToken(name, "ethereum"); !ok {
			t.Errorf("ResolveToken(%q, ethereum) not found", name)
		}
	}
}

func TestResolveToken_ChainAliases(t *testing.T) {
	tests := []struct {
		token, chain string
		wantOK       bool
	}{
		{"USDC", "base-mainnet", true},
		{"USDC", "ethereum-mainnet", true},
		{"USDC", "mainnet", true},
		{"USDC", "polygon-mainnet", true},
		{"USDC", "avalanche-mainnet", true},
		{"USDC", "arbitrum", true},
		{"OBOL", "mainnet", true},
		{"OBOL", "ethereum-mainnet", true},
		{"OBOL", "base-mainnet", false},
	}
	for _, tc := range tests {
		t.Run(tc.token+"_"+tc.chain, func(t *testing.T) {
			_, ok := ResolveToken(tc.token, tc.chain)
			if ok != tc.wantOK {
				t.Errorf("ResolveToken(%s, %s) = %v, want %v", tc.token, tc.chain, ok, tc.wantOK)
			}
		})
	}
}

func TestResolveToken_Unknown(t *testing.T) {
	_, ok := ResolveToken("WETH", "base")
	if ok {
		t.Error("ResolveToken(WETH, base) should return false — not registered")
	}
}

func TestSupportedTokens(t *testing.T) {
	tokens := SupportedTokens()
	if len(tokens) < 2 {
		t.Fatalf("SupportedTokens() returned %d tokens, want >= 2", len(tokens))
	}
	// Should be sorted.
	for i := 1; i < len(tokens); i++ {
		if tokens[i] < tokens[i-1] {
			t.Errorf("SupportedTokens() not sorted: %v", tokens)
			break
		}
	}
	// Must contain OBOL and USDC.
	found := map[string]bool{}
	for _, tok := range tokens {
		found[tok] = true
	}
	for _, want := range []string{"OBOL", "USDC"} {
		if !found[want] {
			t.Errorf("SupportedTokens() missing %q: %v", want, tokens)
		}
	}
}

func TestTokenSupportedOnChain(t *testing.T) {
	if !TokenSupportedOnChain("USDC", "base") {
		t.Error("USDC should be supported on base")
	}
	if !TokenSupportedOnChain("OBOL", "ethereum") {
		t.Error("OBOL should be supported on ethereum")
	}
	if TokenSupportedOnChain("OBOL", "polygon") {
		t.Error("OBOL should not be supported on polygon")
	}
}
