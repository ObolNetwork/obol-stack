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
	req := BuildV1Requirement(ChainBaseSepolia, "0.001", "0xRecipient")

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
	asset := ResolveAssetInfo(ChainEthereumMainnet, &RouteRule{
		AssetAddress:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		AssetSymbol:         "OBOL",
		AssetDecimals:       18,
		AssetTransferMethod: "permit2",
		EIP712Name:          "Obol Network",
		EIP712Version:       "1",
	})

	if asset.Address != "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7" {
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
}

func TestBuildV2RequirementWithAsset(t *testing.T) {
	req := BuildV2RequirementWithAsset(ChainEthereumMainnet, AssetInfo{
		Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		Symbol:         "OBOL",
		Decimals:       18,
		TransferMethod: "permit2",
		EIP712Name:     "Obol Network",
		EIP712Version:  "1",
	}, "0.001", "0xRecipient")

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
