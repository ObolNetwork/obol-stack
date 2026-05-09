package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPinERPCConfigYAML(t *testing.T) {
	input := []byte(`projects:
  - id: rpc
    upstreams:
      - id: chainlist-84532-0
        endpoint: https://public.example
        evm:
          chainId: 84532
      - id: custom-84532-0
        endpoint: http://host.k3d.internal:8545
        evm:
          chainId: 84532
      - id: obol-rpc-mainnet
        endpoint: https://mainnet.example
        evm:
          chainId: 1
    networks:
      - architecture: evm
        alias: base-sepolia
        evm:
          chainId: 84532
`)

	output, err := pinERPCConfigYAML(input, 84532, "custom-84532-0")
	if err != nil {
		t.Fatalf("pinERPCConfigYAML: %v", err)
	}

	var erpcConfig map[string]any
	if err := yaml.Unmarshal(output, &erpcConfig); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	upstreams := project["upstreams"].([]any)

	if len(upstreams) != 2 {
		t.Fatalf("upstreams = %d, want 2", len(upstreams))
	}
	first := upstreams[0].(map[string]any)
	if first["id"] != "custom-84532-0" {
		t.Fatalf("first upstream = %q, want custom-84532-0", first["id"])
	}
	second := upstreams[1].(map[string]any)
	if second["id"] != "obol-rpc-mainnet" {
		t.Fatalf("second upstream = %q, want obol-rpc-mainnet", second["id"])
	}
}

func TestPinERPCConfigYAMLSelectedMissing(t *testing.T) {
	input := []byte(`projects:
  - id: rpc
    upstreams:
      - id: chainlist-84532-0
        evm:
          chainId: 84532
`)

	_, err := pinERPCConfigYAML(input, 84532, "custom-84532-0")
	if err == nil {
		t.Fatal("pinERPCConfigYAML succeeded, want error")
	}
	if !strings.Contains(err.Error(), `custom-84532-0`) {
		t.Fatalf("error = %q, want upstream id", err)
	}
}
