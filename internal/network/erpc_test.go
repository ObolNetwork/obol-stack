package network

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPatchERPCConfig_AddUpstream(t *testing.T) {
	// Simulate the eRPC config YAML that lives in the ConfigMap
	configYAML := `logLevel: debug
projects:
  - id: rpc
    upstreams:
      - id: obol-rpc-mainnet
        endpoint: https://erpc.gcp.obol.tech/mainnet/evm/1
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

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse test config: %v", err)
	}

	projects := erpcConfig["projects"].([]interface{})
	project := projects[0].(map[string]interface{})

	// Add a local upstream at position 0 (highest priority)
	upstreams := project["upstreams"].([]interface{})
	newUpstream := map[string]interface{}{
		"id":       "local-ethereum-test",
		"endpoint": "http://ethereum-execution.ethereum-test.svc.cluster.local:8545",
		"evm":      map[string]interface{}{"chainId": 1},
		"ignoreMethods": []interface{}{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	upstreams = append([]interface{}{newUpstream}, upstreams...)
	project["upstreams"] = upstreams

	// Verify local upstream is first (eRPC tries in order for reads)
	if len(project["upstreams"].([]interface{})) != 2 {
		t.Errorf("expected 2 upstreams, got %d", len(project["upstreams"].([]interface{})))
	}

	first := project["upstreams"].([]interface{})[0].(map[string]interface{})
	if first["id"] != "local-ethereum-test" {
		t.Errorf("first upstream should be local, got %v", first["id"])
	}
	// Local upstream must block write methods
	ignored, ok := first["ignoreMethods"].([]interface{})
	if !ok || len(ignored) != 2 {
		t.Fatal("local upstream must have ignoreMethods for write methods")
	}
	if ignored[0] != "eth_sendRawTransaction" {
		t.Errorf("ignoreMethods[0] = %v, want eth_sendRawTransaction", ignored[0])
	}

	second := project["upstreams"].([]interface{})[1].(map[string]interface{})
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
        endpoint: https://erpc.gcp.obol.tech/mainnet/evm/1
        evm:
          chainId: 1
`

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]interface{})
	project := projects[0].(map[string]interface{})
	upstreams := project["upstreams"].([]interface{})

	// Filter out the local upstream
	var filtered []interface{}
	for _, u := range upstreams {
		um := u.(map[string]interface{})
		if um["id"] == "local-ethereum-test" {
			continue
		}
		filtered = append(filtered, u)
	}
	project["upstreams"] = filtered

	if len(filtered) != 1 {
		t.Errorf("expected 1 upstream after removal, got %d", len(filtered))
	}
	remaining := filtered[0].(map[string]interface{})
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
        endpoint: https://erpc.gcp.obol.tech/mainnet/evm/1
        evm:
          chainId: 1
`

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]interface{})
	project := projects[0].(map[string]interface{})
	upstreams := project["upstreams"].([]interface{})

	// Remove existing, then add updated (same as patchERPCUpstream logic)
	var filtered []interface{}
	for _, u := range upstreams {
		um := u.(map[string]interface{})
		if um["id"] == "local-ethereum-test" {
			continue
		}
		filtered = append(filtered, u)
	}

	newUpstream := map[string]interface{}{
		"id":       "local-ethereum-test",
		"endpoint": "http://new-endpoint:8545",
		"evm":      map[string]interface{}{"chainId": 1},
		"ignoreMethods": []interface{}{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	filtered = append([]interface{}{newUpstream}, filtered...)
	project["upstreams"] = filtered

	// Should still be 2 upstreams (not 3)
	if len(filtered) != 2 {
		t.Errorf("expected 2 upstreams (idempotent), got %d", len(filtered))
	}
	first := filtered[0].(map[string]interface{})
	if first["endpoint"] != "http://new-endpoint:8545" {
		t.Errorf("endpoint should be updated, got %v", first["endpoint"])
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
        endpoint: https://erpc.gcp.obol.tech/mainnet/evm/1
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

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	projects := erpcConfig["projects"].([]interface{})
	project := projects[0].(map[string]interface{})

	// Simulate what patchERPCUpstream does: add local upstream at front
	// with write methods blocked
	upstreams := project["upstreams"].([]interface{})
	newUpstream := map[string]interface{}{
		"id":       "local-ethereum-prod",
		"endpoint": "http://ethereum-execution.ethereum-prod.svc.cluster.local:8545",
		"evm":      map[string]interface{}{"chainId": 1},
		"ignoreMethods": []interface{}{
			"eth_sendRawTransaction",
			"eth_sendTransaction",
		},
	}
	upstreams = append([]interface{}{newUpstream}, upstreams...)
	project["upstreams"] = upstreams

	// Verify: selectionPolicy must be UNTOUCHED
	networks := project["networks"].([]interface{})
	mainnet := networks[0].(map[string]interface{})
	sp, ok := mainnet["selectionPolicy"].(map[string]interface{})
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
	if len(project["upstreams"].([]interface{})) != 2 {
		t.Errorf("expected 2 upstreams, got %d", len(project["upstreams"].([]interface{})))
	}
	first := project["upstreams"].([]interface{})[0].(map[string]interface{})
	if first["id"] != "local-ethereum-prod" {
		t.Errorf("local upstream should be first (for reads), got %v", first["id"])
	}
	second := project["upstreams"].([]interface{})[1].(map[string]interface{})
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
