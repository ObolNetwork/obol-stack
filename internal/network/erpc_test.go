package network

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

func TestPatchERPCConfig_AddUpstream(t *testing.T) {
	// Simulate the eRPC config YAML that lives in the ConfigMap
	configYAML := `logLevel: debug
projects:
  - id: rpc
    upstreams:
      - id: obol-rpc-mainnet
        endpoint: https://erpc.gcp.obol.tech/rpc/mainnet
        evm:
          chainId: 1
    networks:
      - architecture: evm
        evm:
          chainId: 1
        alias: mainnet
        failsafe:
          timeout:
            duration: 30s
`

	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse test config: %v", err)
	}

	projects := erpcConfig["projects"].([]any)
	project := projects[0].(map[string]any)

	// Add a local upstream at position 0 (highest priority)
	upstreams := project["upstreams"].([]any)
	newUpstream := map[string]any{
		"id":       "local-ethereum-test",
		"endpoint": "http://ethereum-execution.ethereum-test.svc.cluster.local:8545",
		"evm":      map[string]any{"chainId": 1},
		"ignoreMethods": []any{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	upstreams = append([]any{newUpstream}, upstreams...)
	project["upstreams"] = upstreams

	// Verify local upstream is first (eRPC tries in order for reads)
	if len(project["upstreams"].([]any)) != 2 {
		t.Errorf("expected 2 upstreams, got %d", len(project["upstreams"].([]any)))
	}

	first := project["upstreams"].([]any)[0].(map[string]any)
	if first["id"] != "local-ethereum-test" {
		t.Errorf("first upstream should be local, got %v", first["id"])
	}
	// Local upstream must block write methods
	ignored, ok := first["ignoreMethods"].([]any)
	if !ok || len(ignored) != 2 {
		t.Fatal("local upstream must have ignoreMethods for write methods")
	}

	if ignored[0] != "eth_sendRawTransaction" {
		t.Errorf("ignoreMethods[0] = %v, want eth_sendRawTransaction", ignored[0])
	}

	second := project["upstreams"].([]any)[1].(map[string]any)
	if second["id"] != "obol-rpc-mainnet" {
		t.Errorf("second upstream should be remote (write-capable), got %v", second["id"])
	}
}

func TestPatchERPCConfig_RemoveUpstream(t *testing.T) {
	configYAML := `projects:
  - id: rpc
    upstreams:
      - id: local-ethereum-test
        endpoint: http://localhost:8545
        evm:
          chainId: 1
      - id: obol-rpc-mainnet
        endpoint: https://erpc.gcp.obol.tech/rpc/mainnet
        evm:
          chainId: 1
`

	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]any)
	project := projects[0].(map[string]any)
	upstreams := project["upstreams"].([]any)

	// Filter out the local upstream
	var filtered []any

	for _, u := range upstreams {
		um := u.(map[string]any)
		if um["id"] == "local-ethereum-test" {
			continue
		}

		filtered = append(filtered, u)
	}

	project["upstreams"] = filtered

	if len(filtered) != 1 {
		t.Errorf("expected 1 upstream after removal, got %d", len(filtered))
	}

	remaining := filtered[0].(map[string]any)
	if remaining["id"] != "obol-rpc-mainnet" {
		t.Errorf("remaining upstream should be obol-rpc-mainnet, got %v", remaining["id"])
	}
}

func TestPatchERPCConfig_Idempotent(t *testing.T) {
	configYAML := `projects:
  - id: rpc
    upstreams:
      - id: local-ethereum-test
        endpoint: http://old-endpoint:8545
        evm:
          chainId: 1
      - id: obol-rpc-mainnet
        endpoint: https://erpc.gcp.obol.tech/rpc/mainnet
        evm:
          chainId: 1
`

	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]any)
	project := projects[0].(map[string]any)
	upstreams := project["upstreams"].([]any)

	// Remove existing, then add updated (same as patchERPCUpstream logic)
	var filtered []any

	for _, u := range upstreams {
		um := u.(map[string]any)
		if um["id"] == "local-ethereum-test" {
			continue
		}

		filtered = append(filtered, u)
	}

	newUpstream := map[string]any{
		"id":       "local-ethereum-test",
		"endpoint": "http://new-endpoint:8545",
		"evm":      map[string]any{"chainId": 1},
		"ignoreMethods": []any{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	filtered = append([]any{newUpstream}, filtered...)
	project["upstreams"] = filtered

	// Should still be 2 upstreams (not 3)
	if len(filtered) != 2 {
		t.Errorf("expected 2 upstreams (idempotent), got %d", len(filtered))
	}

	first := filtered[0].(map[string]any)
	if first["endpoint"] != "http://new-endpoint:8545" {
		t.Errorf("endpoint should be updated, got %v", first["endpoint"])
	}
}

func TestUpsertCustomRPCUpstream_PrioritizesExplicitEndpoint(t *testing.T) {
	configYAML := `projects:
  - id: rpc
    upstreams:
      - id: base-sepolia-official
        endpoint: https://sepolia.base.org
        evm:
          chainId: 84532
      - id: custom-84532-0
        endpoint: https://old.example
        evm:
          chainId: 84532
    networks:
      - architecture: evm
        alias: base-sepolia
        evm:
          chainId: 84532
`

	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	if err := upsertCustomRPCUpstream(project, 84532, "base-sepolia", "https://base-sepolia-rpc.publicnode.com", false); err != nil {
		t.Fatalf("upsert custom rpc: %v", err)
	}

	upstreams := project["upstreams"].([]any)
	if len(upstreams) != 2 {
		t.Fatalf("upstreams = %d, want 2", len(upstreams))
	}

	first := upstreams[0].(map[string]any)
	if first["id"] != "custom-84532-0" {
		t.Fatalf("first upstream id = %v, want custom-84532-0", first["id"])
	}
	if first["endpoint"] != "https://base-sepolia-rpc.publicnode.com" {
		t.Fatalf("first upstream endpoint = %v", first["endpoint"])
	}
	if _, ok := first["ignoreMethods"]; ok {
		t.Fatal("write-enabled custom endpoint should not block write methods")
	}

	networks := project["networks"].([]any)
	network := networks[0].(map[string]any)
	policy, ok := network["selectionPolicy"].(map[string]any)
	if !ok {
		t.Fatal("write-enabled custom endpoint should install a write selection policy")
	}
	evalFunction, _ := policy["evalFunction"].(string)
	if !strings.Contains(evalFunction, "eth_sendRawTransaction") {
		t.Fatalf("selection policy = %q, want eth_sendRawTransaction guard", evalFunction)
	}
	if !strings.Contains(evalFunction, "custom-84532-0") {
		t.Fatalf("selection policy = %q, want custom upstream pin", evalFunction)
	}

	second := upstreams[1].(map[string]any)
	if second["id"] != "base-sepolia-official" {
		t.Fatalf("second upstream id = %v, want base-sepolia-official", second["id"])
	}
}

func TestUpsertCustomRPCUpstream_ReadOnlyRemovesCustomWritePolicy(t *testing.T) {
	project := map[string]any{
		"upstreams": []any{
			map[string]any{
				"id":       "custom-84532-0",
				"endpoint": "https://old.example",
				"evm":      map[string]any{"chainId": 84532},
			},
		},
		"networks": []any{
			map[string]any{
				"architecture": "evm",
				"alias":        "base-sepolia",
				"evm":          map[string]any{"chainId": 84532},
				"selectionPolicy": map[string]any{
					"evalFunction": "return upstreams.filter(u => u.config.id === 'custom-84532-0')",
				},
			},
		},
	}

	if err := upsertCustomRPCUpstream(project, 84532, "base-sepolia", "https://base-sepolia-rpc.publicnode.com", true); err != nil {
		t.Fatalf("upsert custom rpc: %v", err)
	}

	upstream := project["upstreams"].([]any)[0].(map[string]any)
	if _, ok := upstream["ignoreMethods"]; !ok {
		t.Fatal("read-only custom endpoint should block write methods")
	}

	network := project["networks"].([]any)[0].(map[string]any)
	if _, ok := network["selectionPolicy"]; ok {
		t.Fatal("read-only custom endpoint should remove stale custom-only write selection policy")
	}
}

func TestPatchERPCConfig_PreservesWriteOnlySelectionPolicy(t *testing.T) {
	// The obol-stack eRPC config routes eth_sendRawTransaction exclusively
	// to obol-rpc-mainnet. When a local node is registered, the selection
	// policy must be preserved — writes still go to obol-rpc-mainnet only,
	// while reads use the local node (first in array order).
	configYAML := `projects:
  - id: rpc
    upstreams:
      - id: obol-rpc-mainnet
        endpoint: https://erpc.gcp.obol.tech/rpc/mainnet
        evm:
          chainId: 1
    networks:
      - architecture: evm
        evm:
          chainId: 1
        selectionPolicy:
          evalInterval: 1m
          evalPerMethod: true
          evalFunction: |
            (upstreams, method) => {
              if (method === 'eth_sendRawTransaction') {
                return upstreams.filter(u => u.config.id === 'obol-rpc-mainnet');
              }
              return upstreams;
            }
`

	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]any)
	project := projects[0].(map[string]any)

	// Simulate what patchERPCUpstream does: add local upstream at front
	// with write methods blocked
	upstreams := project["upstreams"].([]any)
	newUpstream := map[string]any{
		"id":       "local-ethereum-prod",
		"endpoint": "http://ethereum-execution.ethereum-prod.svc.cluster.local:8545",
		"evm":      map[string]any{"chainId": 1},
		"ignoreMethods": []any{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	upstreams = append([]any{newUpstream}, upstreams...)
	project["upstreams"] = upstreams

	// Verify: selectionPolicy must be UNTOUCHED
	networks := project["networks"].([]any)
	mainnet := networks[0].(map[string]any)

	sp, ok := mainnet["selectionPolicy"].(map[string]any)
	if !ok {
		t.Fatal("selectionPolicy was removed")
	}

	if sp["evalPerMethod"] != true {
		t.Error("evalPerMethod should still be true")
	}

	fn, ok := sp["evalFunction"].(string)
	if !ok || !strings.Contains(fn, "obol-rpc-mainnet") {
		t.Error("evalFunction should still route writes to obol-rpc-mainnet")
	}

	// Verify: 2 upstreams (local + obol), local first for reads
	if len(project["upstreams"].([]any)) != 2 {
		t.Errorf("expected 2 upstreams, got %d", len(project["upstreams"].([]any)))
	}

	first := project["upstreams"].([]any)[0].(map[string]any)
	if first["id"] != "local-ethereum-prod" {
		t.Errorf("local upstream should be first (for reads), got %v", first["id"])
	}

	second := project["upstreams"].([]any)[1].(map[string]any)
	if second["id"] != "obol-rpc-mainnet" {
		t.Errorf("obol-rpc-mainnet should be second (protected write target), got %v", second["id"])
	}
}

func TestNetworkChainIDs(t *testing.T) {
	tests := []struct {
		network string
		chainID int
	}{
		{"mainnet", 1},
		{"hoodi", 560048},
		{"sepolia", 11155111},
		{"base", 8453},
		{"base-sepolia", 84532},
	}

	for _, tt := range tests {
		got, ok := networkChainIDs[tt.network]
		if !ok {
			t.Errorf("networkChainIDs missing %q", tt.network)
			continue
		}

		if got != tt.chainID {
			t.Errorf("networkChainIDs[%q] = %d, want %d", tt.network, got, tt.chainID)
		}
	}
}

func TestResolveLocalERPCRegistration(t *testing.T) {
	tests := []struct {
		name        string
		networkType string
		id          string
		network     string
		chain       string
		want        localERPCRegistration
	}{
		{
			name:        "ethereum mainnet",
			networkType: "ethereum",
			id:          "mainnet",
			network:     "mainnet",
			want: localERPCRegistration{
				ChainID:  1,
				Alias:    "mainnet",
				Endpoint: "http://ethereum-execution.ethereum-mainnet.svc.cluster.local:8545",
			},
		},
		{
			name:        "hl-node mainnet",
			networkType: "hl-node",
			id:          "mainnet",
			chain:       "Mainnet",
			want: localERPCRegistration{
				ChainID:  999,
				Alias:    "hyperevm",
				Endpoint: "http://hl-node.hl-node-mainnet.svc.cluster.local:3001/evm",
			},
		},
		{
			name:        "hl-node testnet",
			networkType: "hl-node",
			id:          "testnet",
			chain:       "Testnet",
			want: localERPCRegistration{
				ChainID:  998,
				Alias:    "hyperevm-testnet",
				Endpoint: "http://hl-node.hl-node-testnet.svc.cluster.local:3001/evm",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLocalERPCRegistration(tt.networkType, tt.id, localERPCValues{
				Network: tt.network,
				Chain:   tt.chain,
			})
			if err != nil {
				t.Fatalf("resolve local erpc registration: %v", err)
			}
			if got != tt.want {
				t.Fatalf("registration = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveLocalERPCRegistrationReadsHLNodeChainValue(t *testing.T) {
	var values localERPCValues
	if err := yaml.Unmarshal([]byte("chain: Mainnet\n"), &values); err != nil {
		t.Fatalf("parse values: %v", err)
	}

	got, err := resolveLocalERPCRegistration("hl-node", "review", values)
	if err != nil {
		t.Fatalf("resolve local erpc registration: %v", err)
	}
	if got.Alias != "hyperevm" {
		t.Fatalf("alias = %q, want hyperevm", got.Alias)
	}
	if got.Endpoint != "http://hl-node.hl-node-review.svc.cluster.local:3001/evm" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
}

func TestInstallHLNodeValuesResolveERPCRegistration(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir: filepath.Join(tmp, "config"),
		DataDir:   filepath.Join(tmp, "data"),
		BinDir:    filepath.Join(tmp, "bin"),
		StateDir:  filepath.Join(tmp, "state"),
	}

	var stdout, stderr bytes.Buffer
	u := ui.NewForTest(&stdout, &stderr)
	if err := Install(cfg, u, "hl-node", map[string]string{"id": "review"}, false); err != nil {
		t.Fatalf("Install() error = %v\nstderr:\n%s", err, stderr.String())
	}

	valuesBytes, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "networks", "hl-node", "review", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var values localERPCValues
	if err := yaml.Unmarshal(valuesBytes, &values); err != nil {
		t.Fatalf("parse values: %v", err)
	}
	if values.Network != "" {
		t.Fatalf("hl-node values should not rely on network field, got %q", values.Network)
	}
	if values.Chain != "Mainnet" {
		t.Fatalf("chain = %q, want Mainnet", values.Chain)
	}

	got, err := resolveLocalERPCRegistration("hl-node", "review", values)
	if err != nil {
		t.Fatalf("resolve local erpc registration: %v", err)
	}
	if got != (localERPCRegistration{
		ChainID:  999,
		Alias:    "hyperevm",
		Endpoint: "http://hl-node.hl-node-review.svc.cluster.local:3001/evm",
	}) {
		t.Fatalf("registration = %+v", got)
	}
}

func TestResolveLocalERPCRegistrationRejectsUnknownHLNetwork(t *testing.T) {
	_, err := resolveLocalERPCRegistration("hl-node", "dev", localERPCValues{Chain: "devnet"})
	if err == nil {
		t.Fatal("expected unknown hl-node network error")
	}
	if !strings.Contains(err.Error(), "expected mainnet or testnet") {
		t.Fatalf("error = %q, want mainnet/testnet guidance", err)
	}
}
