package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

func TestParseERPCOverlay_RequiresContent(t *testing.T) {
	_, err := parseERPCOverlay([]byte("version: 1\n"))
	if err == nil {
		t.Fatal("expected empty overlay to fail")
	}
}

func TestParseERPCOverlay_RequiresUpstreamID(t *testing.T) {
	_, err := parseERPCOverlay([]byte(`
version: 1
upstreams:
  - endpoint: https://example.com
`))
	if err == nil {
		t.Fatal("expected missing id to fail")
	}
}

func TestERPCOverlayRoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	ov := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{
				"alias":        "hyperevm",
				"architecture": "evm",
				"evm":          map[string]any{"chainId": 999},
			},
		},
		Upstreams: []map[string]any{
			{
				"id":       "local-hl-node",
				"endpoint": "http://192.168.50.21:3001/evm",
				"evm":      map[string]any{"chainId": 999},
			},
			{
				"id":       "hyperevm-official",
				"endpoint": "https://rpc.hyperliquid.xyz/evm",
				"evm":      map[string]any{"chainId": 999},
			},
		},
		RateLimiters: map[string]any{
			"budgets": []any{
				map[string]any{
					"id": "hyperevm-official",
					"rules": []any{
						map[string]any{"method": "*", "maxCount": 100, "period": "second"},
					},
				},
			},
		},
		CachePoliciesAdd: []map[string]any{
			{"network": "*", "method": "eth_call", "finality": "realtime", "connector": "memory-cache", "ttl": "2s"},
		},
	}
	if err := writeERPCOverlay(cfg, ov); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(erpcOverlayPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}

	got, err := readERPCOverlay(cfg)
	if err != nil || got == nil {
		t.Fatalf("read: %v %v", got, err)
	}
	if len(got.Networks) != 1 || len(got.Upstreams) != 2 {
		t.Fatalf("round-trip sizes: nets=%d ups=%d", len(got.Networks), len(got.Upstreams))
	}
	if got.Upstreams[0]["id"] != "local-hl-node" {
		t.Errorf("upstream id = %v", got.Upstreams[0]["id"])
	}

	st, err := StatusERPCOverlay(cfg)
	if err != nil || !st.Present {
		t.Fatalf("status: %+v err=%v", st, err)
	}
	if st.UpstreamCount != 2 || st.NetworkCount != 1 || st.ContentHash == "" {
		t.Errorf("status incomplete: %+v", st)
	}
}

func TestMergeERPCOverlay_Idempotent(t *testing.T) {
	baseYAML := `
logLevel: debug
database:
  evmJsonRpcCache:
    connectors:
      - id: memory-cache
        driver: memory
    policies:
      - network: "*"
        method: "*"
        finality: unfinalized
        connector: memory-cache
        ttl: 10s
projects:
  - id: rpc
    networks:
      - alias: base
        architecture: evm
        evm:
          chainId: 8453
    upstreams:
      - id: obol-rpc-base
        endpoint: https://example.com/base
        evm:
          chainId: 8453
`
	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(baseYAML), &erpcConfig); err != nil {
		t.Fatal(err)
	}

	ov := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{
				"alias":        "hyperevm",
				"architecture": "evm",
				"evm":          map[string]any{"chainId": 999},
				"failsafe":     map[string]any{"timeout": map[string]any{"duration": "10s"}},
			},
		},
		Upstreams: []map[string]any{
			{
				"id":       "local-hl-node",
				"endpoint": "http://192.168.50.21:3001/evm",
				"evm":      map[string]any{"chainId": 999},
			},
			{
				"id":               "hyperevm-official",
				"endpoint":         "https://rpc.hyperliquid.xyz/evm",
				"evm":              map[string]any{"chainId": 999},
				"rateLimitBudget":  "hyperevm-official",
			},
		},
		RateLimiters: map[string]any{
			"budgets": []any{
				map[string]any{"id": "hyperevm-official", "rules": []any{}},
			},
		},
		CachePoliciesAdd: []map[string]any{
			{"network": "*", "method": "eth_call", "finality": "realtime", "ttl": "2s"},
		},
	}

	if err := mergeERPCOverlay(erpcConfig, ov); err != nil {
		t.Fatal(err)
	}
	// Second apply must be idempotent (no duplicate ids)
	if err := mergeERPCOverlay(erpcConfig, ov); err != nil {
		t.Fatal(err)
	}

	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	networks := project["networks"].([]any)
	upstreams := project["upstreams"].([]any)

	if len(networks) != 2 {
		t.Fatalf("networks = %d, want 2 (base + hyperevm)", len(networks))
	}
	if len(upstreams) != 3 {
		t.Fatalf("upstreams = %d, want 3 (base + 2 overlay)", len(upstreams))
	}

	// base preserved
	ids := map[string]bool{}
	for _, u := range upstreams {
		ids[u.(map[string]any)["id"].(string)] = true
	}
	for _, want := range []string{"obol-rpc-base", "local-hl-node", "hyperevm-official"} {
		if !ids[want] {
			t.Errorf("missing upstream %s", want)
		}
	}

	// hyperevm network present
	foundHyper := false
	for _, n := range networks {
		nm := n.(map[string]any)
		if nm["alias"] == "hyperevm" && yamlInt(nm["evm"].(map[string]any)["chainId"]) == 999 {
			foundHyper = true
		}
	}
	if !foundHyper {
		t.Error("hyperevm network missing")
	}

	// rate limiters
	rl := erpcConfig["rateLimiters"].(map[string]any)
	budgets := rl["budgets"].([]any)
	if len(budgets) != 1 {
		t.Fatalf("budgets = %d, want 1", len(budgets))
	}

	// cache policy added once
	policies := erpcConfig["database"].(map[string]any)["evmJsonRpcCache"].(map[string]any)["policies"].([]any)
	if len(policies) != 2 {
		t.Fatalf("cache policies = %d, want 2 (base unfinalized + realtime eth_call)", len(policies))
	}
}

func TestMergeERPCOverlay_ReplacesByChainIDAndUpstreamID(t *testing.T) {
	erpcConfig := map[string]any{
		"projects": []any{
			map[string]any{
				"id": "rpc",
				"networks": []any{
					map[string]any{
						"alias": "hyperevm",
						"evm":   map[string]any{"chainId": 999},
						"failsafe": map[string]any{
							"timeout": map[string]any{"duration": "30s"},
						},
					},
				},
				"upstreams": []any{
					map[string]any{
						"id":       "local-hl-node",
						"endpoint": "http://old:3001/evm",
						"evm":      map[string]any{"chainId": 999},
					},
				},
			},
		},
	}
	ov := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{
				"alias": "hyperevm",
				"evm":   map[string]any{"chainId": 999},
				"failsafe": map[string]any{
					"timeout": map[string]any{"duration": "10s"},
				},
			},
		},
		Upstreams: []map[string]any{
			{
				"id":       "local-hl-node",
				"endpoint": "http://192.168.50.21:3001/evm",
				"evm":      map[string]any{"chainId": 999},
			},
		},
	}
	if err := mergeERPCOverlay(erpcConfig, ov); err != nil {
		t.Fatal(err)
	}
	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	networks := project["networks"].([]any)
	upstreams := project["upstreams"].([]any)
	if len(networks) != 1 || len(upstreams) != 1 {
		t.Fatalf("replace must not duplicate: nets=%d ups=%d", len(networks), len(upstreams))
	}
	ep := upstreams[0].(map[string]any)["endpoint"]
	if ep != "http://192.168.50.21:3001/evm" {
		t.Errorf("endpoint not replaced: %v", ep)
	}
	to := networks[0].(map[string]any)["failsafe"].(map[string]any)["timeout"].(map[string]any)["duration"]
	if to != "10s" {
		t.Errorf("network failsafe not replaced: %v", to)
	}
}

func TestStripERPCOverlay(t *testing.T) {
	erpcConfig := map[string]any{
		"projects": []any{
			map[string]any{
				"id": "rpc",
				"networks": []any{
					map[string]any{"alias": "base", "evm": map[string]any{"chainId": 8453}},
					map[string]any{"alias": "hyperevm", "evm": map[string]any{"chainId": 999}},
				},
				"upstreams": []any{
					map[string]any{"id": "obol-rpc-base", "evm": map[string]any{"chainId": 8453}},
					map[string]any{"id": "local-hl-node", "evm": map[string]any{"chainId": 999}},
				},
			},
		},
	}
	ov := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{"alias": "hyperevm", "evm": map[string]any{"chainId": 999}},
		},
		Upstreams: []map[string]any{
			{"id": "local-hl-node", "endpoint": "http://x", "evm": map[string]any{"chainId": 999}},
		},
	}
	if err := stripERPCOverlay(erpcConfig, ov); err != nil {
		t.Fatal(err)
	}
	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	if len(project["networks"].([]any)) != 1 {
		t.Fatalf("networks after strip: %v", project["networks"])
	}
	if len(project["upstreams"].([]any)) != 1 {
		t.Fatalf("upstreams after strip: %v", project["upstreams"])
	}
	if project["upstreams"].([]any)[0].(map[string]any)["id"] != "obol-rpc-base" {
		t.Error("base upstream must remain")
	}
}

func TestApplyERPCOverlayFile_PersistsThenReadable(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "basket.yaml")
	body := []byte(`
version: 1
networks:
  - alias: hyperevm
    architecture: evm
    evm:
      chainId: 999
upstreams:
  - id: hyperevm-official
    endpoint: https://rpc.hyperliquid.xyz/evm
    evm:
      chainId: 999
`)
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// applyOverlayToCluster needs a cluster — only test parse+persist via write path
	ov, err := parseERPCOverlay(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeERPCOverlay(cfg, ov); err != nil {
		t.Fatal(err)
	}
	got, err := readERPCOverlay(cfg)
	if err != nil || got == nil || len(got.Upstreams) != 1 {
		t.Fatalf("persist failed: %+v %v", got, err)
	}
}
