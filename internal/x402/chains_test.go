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

func TestBuildV1Requirement(t *testing.T) {
	req := BuildV1Requirement(ChainBaseSepolia, "0.001", "0xRecipient", 0)

	if req.Scheme != "exact" {
		t.Errorf("Scheme = %q, want %q", req.Scheme, "exact")
	}
	if req.Network != "base-sepolia" {
		t.Errorf("Network = %q, want %q", req.Network, "base-sepolia")
	}
	// 0.001 USDC = 1000 atomic units (6 decimals)
	if req.MaxAmountRequired != "1000" {
		t.Errorf("MaxAmountRequired = %q, want %q", req.MaxAmountRequired, "1000")
	}
	if req.Asset != ChainBaseSepolia.USDCAddress {
		t.Errorf("Asset = %q, want %q", req.Asset, ChainBaseSepolia.USDCAddress)
	}
	if req.PayTo != "0xRecipient" {
		t.Errorf("PayTo = %q, want %q", req.PayTo, "0xRecipient")
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
			asset := ResolveAssetInfo(tc.chain, &RouteRule{
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
	asset := ResolveAssetInfo(ChainEthereumMainnet, &RouteRule{
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

	baseSepoliaOBOL := ResolveAssetInfo(ChainBaseSepolia, &RouteRule{
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

	got := BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", 0)
	if got.MaxTimeoutSeconds != int(DefaultMaxTimeoutSeconds) {
		t.Errorf("zero spec value should map to default %d, got %d", DefaultMaxTimeoutSeconds, got.MaxTimeoutSeconds)
	}

	got = BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", 1800)
	if got.MaxTimeoutSeconds != 1800 {
		t.Errorf("operator-set 1800 should reach the 402 verbatim, got %d", got.MaxTimeoutSeconds)
	}

	got = BuildV2RequirementWithAsset(ChainBaseSepolia, asset, "0.001", "0xRecipient", MaxMaxTimeoutSeconds+1000)
	if got.MaxTimeoutSeconds != int(MaxMaxTimeoutSeconds) {
		t.Errorf("runaway value should clamp to cap %d, got %d", MaxMaxTimeoutSeconds, got.MaxTimeoutSeconds)
	}
}

func TestBuildV2RequirementWithAsset(t *testing.T) {
	req := BuildV2RequirementWithAsset(ChainEthereumMainnet, AssetInfo{
		Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		Symbol:         "OBOL",
		Decimals:       18,
		TransferMethod: "permit2",
		EIP712Name:     "Obol Network",
		EIP712Version:  "1",
	}, "0.001", "0xRecipient", 0)

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
