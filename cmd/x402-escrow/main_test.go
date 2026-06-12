package main

import (
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadConfig(func(string) string { return "" })

	if cfg.StateDir != "/data" {
		t.Errorf("StateDir = %q, want /data", cfg.StateDir)
	}
	if cfg.RPCBase != erc8004.DefaultRPCBase {
		t.Errorf("RPCBase = %q, want %q", cfg.RPCBase, erc8004.DefaultRPCBase)
	}
	if !reflect.DeepEqual(cfg.Networks, []string{"base", "base-sepolia"}) {
		t.Errorf("Networks = %v, want [base base-sepolia]", cfg.Networks)
	}
	if cfg.Token != "" || cfg.KeyHex != "" || cfg.SignerURL != "" {
		t.Errorf("credentials should default empty: %+v", cfg)
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	env := map[string]string{
		"OBOL_ESCROW_TOKEN":      "tok",
		"OBOL_ESCROW_STATE_DIR":  "/var/lib/escrow",
		"OBOL_ESCROW_KEY":        "0xabc123",
		"OBOL_ESCROW_SIGNER_URL": "http://remote-signer:9000",
		"OBOL_ESCROW_RPC_BASE":   "http://127.0.0.1:8545",
		"OBOL_ESCROW_NETWORKS":   " base-sepolia , , polygon ",
	}
	cfg := loadConfig(func(k string) string { return env[k] })

	if cfg.Token != "tok" || cfg.StateDir != "/var/lib/escrow" || cfg.KeyHex != "0xabc123" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.SignerURL != "http://remote-signer:9000" || cfg.RPCBase != "http://127.0.0.1:8545" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Networks, []string{"base-sepolia", "polygon"}) {
		t.Errorf("Networks = %v, want trimmed csv with empties dropped", cfg.Networks)
	}
}
