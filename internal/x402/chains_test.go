package x402

import (
	"testing"
)

func TestResolveChainInfo(t *testing.T) {
	tests := []struct {
		name    string
		want    string // expected NetworkID
		wantErr bool
	}{
		{"base", "base", false},
		{"base-mainnet", "base", false},
		{"base-sepolia", "base-sepolia", false},
		{"ethereum", "ethereum", false},
		{"ethereum-mainnet", "ethereum", false},
		{"mainnet", "ethereum", false},
		{"polygon", "polygon", false},
		{"polygon-mainnet", "polygon", false},
		{"polygon-amoy", "polygon-amoy", false},
		{"avalanche", "avalanche", false},
		{"avalanche-mainnet", "avalanche", false},
		{"avalanche-fuji", "avalanche-fuji", false},
		{"arbitrum-one", "arbitrum-one", false},
		{"arbitrum", "arbitrum-one", false},
		{"arbitrum-sepolia", "arbitrum-sepolia", false},
		{"unknown-chain", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain, err := ResolveChainInfo(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveChainInfo(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if !tt.wantErr && chain.NetworkID != tt.want {
				t.Errorf("ResolveChainInfo(%q).NetworkID = %q, want %q", tt.name, chain.NetworkID, tt.want)
			}
		})
	}
}

func TestIsTestnetCAIP2(t *testing.T) {
	tests := []struct {
		caip2 string
		want  bool
	}{
		{ChainBaseSepolia.CAIP2Network, true},
		{ChainPolygonAmoy.CAIP2Network, true},
		{ChainAvalancheFuji.CAIP2Network, true},
		{ChainArbitrumSepolia.CAIP2Network, true},
		{ChainBaseMainnet.CAIP2Network, false},
		{ChainEthereumMainnet.CAIP2Network, false},
		{ChainPolygonMainnet.CAIP2Network, false},
		{ChainAvalancheMainnet.CAIP2Network, false},
		{ChainArbitrumOne.CAIP2Network, false},
		{"eip155:999999", false}, // unknown chain — not a known testnet
	}

	for _, tt := range tests {
		t.Run(tt.caip2, func(t *testing.T) {
			if got := IsTestnetCAIP2(tt.caip2); got != tt.want {
				t.Errorf("IsTestnetCAIP2(%q) = %v, want %v", tt.caip2, got, tt.want)
			}
		})
	}
}

func TestChainUSDCAddresses(t *testing.T) {
	// Verify USDC addresses are non-empty and start with 0x.
	chains := []ChainInfo{
		ChainBaseMainnet, ChainBaseSepolia, ChainEthereumMainnet,
		ChainPolygonMainnet, ChainPolygonAmoy,
		ChainAvalancheMainnet, ChainAvalancheFuji,
		ChainArbitrumOne, ChainArbitrumSepolia,
	}

	for _, c := range chains {
		if c.USDCAddress == "" || c.USDCAddress[:2] != "0x" {
			t.Errorf("chain %q: invalid USDC address %q", c.Name, c.USDCAddress)
		}
		if c.Decimals != 6 {
			t.Errorf("chain %q: expected 6 decimals, got %d", c.Name, c.Decimals)
		}
	}
}

func TestResolveAssetInfo_Override(t *testing.T) {
	tests := []struct {
		name         string
		chain        ChainInfo
		assetAddress string
	}{
		{
			name:         "ethereum",
			chain:        ChainEthereumMainnet,
			assetAddress: "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		},
		{
			name:         "base-sepolia",
			chain:        ChainBaseSepolia,
			assetAddress: "0x0a09371a8b011d5110656ceBCc70603e53FD2c78",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asset := ResolveAssetInfoForPayment(tc.chain, RoutePayment{
				AssetAddress:        tc.assetAddress,
				AssetSymbol:         "OBOL",
				AssetDecimals:       18,
				AssetTransferMethod: "permit2",
				EIP712Name:          "Obol Network",
				EIP712Version:       "1",
			})

			if asset.Address != tc.assetAddress {
				t.Fatalf("asset.Address = %q", asset.Address)
			}
			if asset.TransferMethod != "permit2" {
				t.Fatalf("asset.TransferMethod = %q", asset.TransferMethod)
			}
			if asset.Decimals != 18 {
				t.Fatalf("asset.Decimals = %d", asset.Decimals)
			}
			if asset.EIP712Name != "Obol Network" || asset.EIP712Version != "1" {
				t.Fatalf("asset EIP-712 metadata = %q/%q", asset.EIP712Name, asset.EIP712Version)
			}
			if !asset.EIP2612GasSponsoring {
				t.Fatalf("EIP2612GasSponsoring = false, want true (re-derived from registry for OBOL on %s)", tc.name)
			}
		})
	}
}

// A ServiceOffer that claims symbol OBOL with a foreign contract address must
// NOT inherit the gasless-approve flag from the registry, otherwise a buyer
// would skip the on-chain approve and the payment would fail at settlement.
func TestResolveAssetInfo_RejectAddressMismatch(t *testing.T) {
	asset := ResolveAssetInfoForPayment(ChainEthereumMainnet, RoutePayment{
		AssetAddress:        "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		AssetSymbol:         "OBOL",
		AssetDecimals:       18,
		AssetTransferMethod: "permit2",
		EIP712Name:          "Obol Network",
		EIP712Version:       "1",
	})

	if asset.EIP2612GasSponsoring {
		t.Fatal("EIP2612GasSponsoring leaked to a foreign address claiming the OBOL symbol")
	}
}

func TestBuildExtensionsForAsset(t *testing.T) {
	if got := BuildExtensionsForAsset(AssetInfo{EIP2612GasSponsoring: false}); got != nil {
		t.Errorf("expected nil extensions when flag is false, got %v", got)
	}
	got := BuildExtensionsForAsset(AssetInfo{EIP2612GasSponsoring: true})
	adv, ok := got["eip2612GasSponsoring"].(map[string]any)
	if !ok {
		t.Fatalf("expected eip2612GasSponsoring key, got %v", got)
	}
	// The spec'd v2 extension pattern is {info, schema}: info describes the
	// capability, schema describes the fields the client must fill into its
	// PaymentPayload echo. buy.py keys only on the key's presence, so this
	// shape is load-bearing for spec conformance, not the buy flow.
	info, _ := adv["info"].(map[string]any)
	if info == nil || info["version"] != "1" {
		t.Errorf("eip2612GasSponsoring.info missing or unversioned: %v", adv["info"])
	}
	schema, _ := adv["schema"].(map[string]any)
	if schema == nil {
		t.Fatalf("eip2612GasSponsoring.schema missing: %v", adv)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"from", "asset", "spender", "amount", "nonce", "deadline", "signature", "version"} {
		if _, ok := props[field]; !ok {
			t.Errorf("eip2612GasSponsoring.schema.properties missing %q", field)
		}
	}

	baseSepoliaOBOL := ResolveAssetInfoForPayment(ChainBaseSepolia, RoutePayment{
		AssetAddress:        "0x0a09371a8b011d5110656ceBCc70603e53FD2c78",
		AssetSymbol:         "OBOL",
		AssetDecimals:       18,
		AssetTransferMethod: "permit2",
		EIP712Name:          "Obol Network",
		EIP712Version:       "1",
	})
	got = BuildExtensionsForAsset(baseSepoliaOBOL)
	if _, ok := got["eip2612GasSponsoring"]; !ok {
		t.Errorf("Base Sepolia OBOL should advertise eip2612GasSponsoring, got %v", got)
	}
}

func TestClampMaxTimeoutSeconds(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want int64
	}{
		{"zero falls back to default", 0, DefaultMaxTimeoutSeconds},
		{"negative falls back to default", -1, DefaultMaxTimeoutSeconds},
		{"operator-set under cap honored verbatim", 1800, 1800},
		{"operator-set at cap honored verbatim", MaxMaxTimeoutSeconds, MaxMaxTimeoutSeconds},
		{"operator-set above cap clamps down", MaxMaxTimeoutSeconds + 1, MaxMaxTimeoutSeconds},
		{"runaway value clamps to cap", 99999999, MaxMaxTimeoutSeconds},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClampMaxTimeoutSeconds(tc.in); got != tc.want {
				t.Errorf("ClampMaxTimeoutSeconds(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildV2RequirementWithAsset_HonorsMaxTimeoutSeconds(t *testing.T) {
	asset := AssetInfo{
		Address:        "0x0000000000000000000000000000000000000000",
		Symbol:         "USDC",
		Decimals:       6,
		TransferMethod: "eip3009",
		EIP712Name:     "USD Coin",
		EIP712Version:  "2",
	}

	got, err := BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTimeoutSeconds != int(DefaultMaxTimeoutSeconds) {
		t.Errorf("zero spec value should map to default %d, got %d", DefaultMaxTimeoutSeconds, got.MaxTimeoutSeconds)
	}

	got, err = BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", 1800)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTimeoutSeconds != 1800 {
		t.Errorf("operator-set 1800 should reach the 402 verbatim, got %d", got.MaxTimeoutSeconds)
	}

	got, err = BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", MaxMaxTimeoutSeconds+1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MaxTimeoutSeconds != int(MaxMaxTimeoutSeconds) {
		t.Errorf("runaway value should clamp to cap %d, got %d", MaxMaxTimeoutSeconds, got.MaxTimeoutSeconds)
	}
}

func TestBuildV2RequirementWithAsset(t *testing.T) {
	req, err := BuildV2RequirementWithAsset(ChainEthereumMainnet, AssetInfo{
		Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		Symbol:         "OBOL",
		Decimals:       18,
		TransferMethod: "permit2",
		EIP712Name:     "Obol Network",
		EIP712Version:  "1",
	}, "0.001", "0xRecipient", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Amount != "1000000000000000" {
		t.Fatalf("Amount = %q, want 1000000000000000", req.Amount)
	}
	if req.Asset != "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7" {
		t.Fatalf("Asset = %q", req.Asset)
	}
	if req.Extra["assetTransferMethod"] != "permit2" {
		t.Fatalf("assetTransferMethod = %v", req.Extra["assetTransferMethod"])
	}
	if req.Extra["name"] != "Obol Network" || req.Extra["version"] != "1" {
		t.Fatalf("name/version = %v/%v", req.Extra["name"], req.Extra["version"])
	}
}

// TestDecimalToAtomic_RejectsMalformedInput is the regression guard for the
// Canary402 finding: decimalToAtomic used to discard the big.Float Parse
// error, so "0,01" (EU comma decimal) silently parsed as "0" (mispricing
// the route at $0) and "abc"/""/"  " left amountFloat nil, panicking the
// next line's Mul(nil, ...) on every request in the verifier hot path.
func TestDecimalToAtomic_RejectsMalformedInput(t *testing.T) {
	for _, amount := range []string{"abc", "", "  ", "0,01", "$0.01", "-1"} {
		t.Run(amount, func(t *testing.T) {
			if _, err := decimalToAtomic(amount, 6); err == nil {
				t.Fatalf("decimalToAtomic(%q) expected error, got nil", amount)
			}
		})
	}
}

func TestDecimalToAtomic_ValidInput(t *testing.T) {
	tests := []struct {
		amount   string
		decimals int
		want     string
	}{
		{"0.01", 6, "10000"},
		{"1.5", 6, "1500000"},
		{"0.001", 18, "1000000000000000"},
	}
	for _, tc := range tests {
		t.Run(tc.amount, func(t *testing.T) {
			got, err := decimalToAtomic(tc.amount, tc.decimals)
			if err != nil {
				t.Fatalf("decimalToAtomic(%q, %d): unexpected error %v", tc.amount, tc.decimals, err)
			}
			if got != tc.want {
				t.Fatalf("decimalToAtomic(%q, %d) = %q, want %q", tc.amount, tc.decimals, got, tc.want)
			}
		})
	}
}
