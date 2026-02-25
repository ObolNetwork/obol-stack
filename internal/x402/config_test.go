package x402

import (
	"os"
	"path/filepath"
	"testing"

	x402lib "github.com/mark3labs/x402-go"
)

func TestLoadConfig_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `wallet: "0xABCDEF1234567890ABCDEF1234567890ABCDEF12"
chain: "base-sepolia"
facilitatorURL: "https://custom-facilitator.example.com"
verifyOnly: true
routes:
  - pattern: "/rpc/*"
    price: "0.0001"
    description: "RPC endpoint"
  - pattern: "/inference-*/v1/*"
    price: "0.001"
    description: "Inference gateway"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Wallet != "0xABCDEF1234567890ABCDEF1234567890ABCDEF12" {
		t.Errorf("wallet = %q, want 0xABCDEF...", cfg.Wallet)
	}
	if cfg.Chain != "base-sepolia" {
		t.Errorf("chain = %q, want base-sepolia", cfg.Chain)
	}
	if cfg.FacilitatorURL != "https://custom-facilitator.example.com" {
		t.Errorf("facilitatorURL = %q, want custom URL", cfg.FacilitatorURL)
	}
	if !cfg.VerifyOnly {
		t.Error("verifyOnly should be true")
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("routes count = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[0].Pattern != "/rpc/*" {
		t.Errorf("route[0].pattern = %q, want /rpc/*", cfg.Routes[0].Pattern)
	}
	if cfg.Routes[0].Price != "0.0001" {
		t.Errorf("route[0].price = %q, want 0.0001", cfg.Routes[0].Price)
	}
	if cfg.Routes[1].Description != "Inference gateway" {
		t.Errorf("route[1].description = %q, want 'Inference gateway'", cfg.Routes[1].Description)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Minimal YAML: chain and facilitatorURL omitted.
	yaml := `wallet: "0x1234"
routes:
  - pattern: "/api/*"
    price: "0.01"
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Chain != "base-sepolia" {
		t.Errorf("default chain = %q, want base-sepolia", cfg.Chain)
	}
	if cfg.FacilitatorURL != "https://facilitator.x402.rs" {
		t.Errorf("default facilitatorURL = %q, want https://facilitator.x402.rs", cfg.FacilitatorURL)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")

	if err := os.WriteFile(path, []byte("{{not: valid: yaml:"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveChain_AllSupported(t *testing.T) {
	tests := []struct {
		name     string
		expected x402lib.ChainConfig
	}{
		{"base-sepolia", x402lib.BaseSepolia},
		{"base", x402lib.BaseMainnet},
		{"polygon", x402lib.PolygonMainnet},
		{"polygon-amoy", x402lib.PolygonAmoy},
		{"avalanche", x402lib.AvalancheMainnet},
		{"avalanche-fuji", x402lib.AvalancheFuji},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveChain(tt.name)
			if err != nil {
				t.Fatalf("ResolveChain(%q): %v", tt.name, err)
			}
			if got.NetworkID != tt.expected.NetworkID {
				t.Errorf("ResolveChain(%q).NetworkID = %q, want %q", tt.name, got.NetworkID, tt.expected.NetworkID)
			}
		})
	}
}

func TestResolveChain_Aliases(t *testing.T) {
	tests := []struct {
		alias    string
		canonical string
	}{
		{"base-mainnet", "base"},
		{"polygon-mainnet", "polygon"},
		{"avalanche-mainnet", "avalanche"},
	}

	for _, tt := range tests {
		t.Run(tt.alias+"=="+tt.canonical, func(t *testing.T) {
			aliasResult, err := ResolveChain(tt.alias)
			if err != nil {
				t.Fatalf("ResolveChain(%q): %v", tt.alias, err)
			}
			canonResult, err := ResolveChain(tt.canonical)
			if err != nil {
				t.Fatalf("ResolveChain(%q): %v", tt.canonical, err)
			}
			if aliasResult.NetworkID != canonResult.NetworkID {
				t.Errorf("alias %q NetworkID = %q, canonical %q NetworkID = %q",
					tt.alias, aliasResult.NetworkID, tt.canonical, canonResult.NetworkID)
			}
		})
	}
}

func TestResolveChain_Unsupported(t *testing.T) {
	unsupported := []string{"ethereum", "mainnet", "solana", "unknown-chain", ""}
	for _, name := range unsupported {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveChain(name)
			if err == nil {
				t.Errorf("expected error for unsupported chain %q", name)
			}
		})
	}
}
