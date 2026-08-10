package network

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
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

func TestParseERPCOverlay_RejectsNetworkWithoutMergeKey(t *testing.T) {
	_, err := parseERPCOverlay([]byte(`
version: 1
networks:
  - architecture: evm
    failsafe:
      timeout:
        duration: 10s
`))
	if err == nil {
		t.Fatal("expected network without evm.chainId or alias to fail")
	}
}

func TestParseERPCOverlay_RejectsBadChainIDType(t *testing.T) {
	_, err := parseERPCOverlay([]byte(`
version: 1
networks:
  - alias: hyperevm
    evm:
      chainId: "not-a-number"
`))
	if err == nil {
		t.Fatal("expected non-numeric chainId to fail")
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

	st, err := StatusERPC(cfg)
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
				"id":              "hyperevm-official",
				"endpoint":        "https://rpc.hyperliquid.xyz/evm",
				"evm":             map[string]any{"chainId": 999},
				"rateLimitBudget": "hyperevm-official",
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
	if err := stripERPCOverlay(erpcConfig, ov, nil); err != nil {
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

// TestResetERPC_RestoresBaseEntriesOverlayReplaced covers the reset
// provenance fix: an overlay entry whose key collides with a chart-base
// entry must be RESTORED (not deleted) on reset, while an entry the
// overlay purely added is still dropped.
func TestResetERPC_RestoresBaseEntriesOverlayReplaced(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	// Chart-base cluster state: one network/upstream at the SAME key the
	// operator overlay is about to replace.
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
						"id":       "hyperevm-official",
						"endpoint": "https://base-rpc.example.com/evm",
						"evm":      map[string]any{"chainId": 999},
					},
				},
			},
		},
	}

	ov := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{ // replaces the base "hyperevm" network
				"alias": "hyperevm",
				"evm":   map[string]any{"chainId": 999},
				"failsafe": map[string]any{
					"timeout": map[string]any{"duration": "5s"},
				},
			},
			{ // purely additive — no base collision
				"alias": "brandnew",
				"evm":   map[string]any{"chainId": 111},
			},
		},
		Upstreams: []map[string]any{
			{ // replaces the base "hyperevm-official" upstream
				"id":       "hyperevm-official",
				"endpoint": "https://overlay-rpc.example.com/evm",
				"evm":      map[string]any{"chainId": 999},
			},
			{ // purely additive
				"id":       "brand-new-id",
				"endpoint": "https://new.example.com",
			},
		},
	}

	// Mirrors applyOverlayToCluster: snapshot provenance BEFORE merging.
	if err := captureERPCProvenance(cfg, erpcConfig, ov); err != nil {
		t.Fatal(err)
	}
	if err := mergeERPCOverlay(erpcConfig, ov); err != nil {
		t.Fatal(err)
	}

	project := erpcConfig["projects"].([]any)[0].(map[string]any)
	if len(project["networks"].([]any)) != 2 || len(project["upstreams"].([]any)) != 2 {
		t.Fatalf("merge shape unexpected: networks=%v upstreams=%v", project["networks"], project["upstreams"])
	}

	// Mirrors removeOverlayFromCluster: read provenance back and strip.
	prov, err := readERPCProvenance(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := stripERPCOverlay(erpcConfig, ov, prov); err != nil {
		t.Fatal(err)
	}

	project = erpcConfig["projects"].([]any)[0].(map[string]any)
	upstreams := project["upstreams"].([]any)
	if len(upstreams) != 1 {
		t.Fatalf("upstreams after reset = %d, want 1 (base restored, addition dropped)", len(upstreams))
	}
	um := upstreams[0].(map[string]any)
	if um["id"] != "hyperevm-official" || um["endpoint"] != "https://base-rpc.example.com/evm" {
		t.Errorf("base upstream not restored, got %+v", um)
	}

	networks := project["networks"].([]any)
	if len(networks) != 1 {
		t.Fatalf("networks after reset = %d, want 1 (base restored, addition dropped)", len(networks))
	}
	nm := networks[0].(map[string]any)
	to := nm["failsafe"].(map[string]any)["timeout"].(map[string]any)["duration"]
	if to != "30s" {
		t.Errorf("base network failsafe not restored, got duration=%v", to)
	}
}

func TestReconcileERPCOverlayConfig_ReplacesDisjointOverlay(t *testing.T) {
	erpcConfig := map[string]any{
		"projects": []any{
			map[string]any{
				"id": "rpc",
				"networks": []any{
					map[string]any{"alias": "base", "evm": map[string]any{"chainId": 8453}},
				},
				"upstreams": []any{
					map[string]any{"id": "chart-base", "endpoint": "https://base.example.com"},
				},
			},
		},
	}
	prov := &erpcProvenance{}
	overlayA := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{"alias": "network-a", "evm": map[string]any{"chainId": 111}},
		},
		Upstreams: []map[string]any{
			{"id": "upstream-a", "endpoint": "https://a.example.com"},
		},
	}
	if err := reconcileERPCOverlayConfig(erpcConfig, overlayA, prov); err != nil {
		t.Fatal(err)
	}
	pruneERPCProvenance(prov, overlayA)

	overlayB := &ERPCOverlay{
		Version: 1,
		Networks: []map[string]any{
			{"alias": "network-b", "evm": map[string]any{"chainId": 222}},
		},
		Upstreams: []map[string]any{
			{"id": "upstream-b", "endpoint": "https://b.example.com"},
		},
	}
	if err := reconcileERPCOverlayConfig(erpcConfig, overlayB, prov); err != nil {
		t.Fatal(err)
	}
	pruneERPCProvenance(prov, overlayB)

	project := erpcConfigProject(erpcConfig)
	upstreamIDs := map[string]bool{}
	for _, upstream := range asMapSlice(project["upstreams"]) {
		id, _ := upstream["id"].(string)
		upstreamIDs[id] = true
	}
	if upstreamIDs["upstream-a"] {
		t.Fatal("upstream-a from the previous overlay remained live")
	}
	if !upstreamIDs["chart-base"] || !upstreamIDs["upstream-b"] {
		t.Fatalf("upstreams after replacement = %v, want chart-base + upstream-b", upstreamIDs)
	}

	networkKeys := map[string]bool{}
	for _, network := range asMapSlice(project["networks"]) {
		networkKeys[networkMergeKey(network)] = true
	}
	if networkKeys["chain:111"] {
		t.Fatal("network-a from the previous overlay remained live")
	}
	if !networkKeys["chain:8453"] || !networkKeys["chain:222"] {
		t.Fatalf("networks after replacement = %v, want base + network-b", networkKeys)
	}
	if _, tracked := prov.Upstreams["upstream-a"]; tracked {
		t.Fatal("retired upstream remained in pruned provenance")
	}
	if _, tracked := prov.Networks["chain:111"]; tracked {
		t.Fatal("retired network remained in pruned provenance")
	}
}

func TestReconcileERPCOverlayConfig_RestoresBaseEntryOmittedByReplacement(t *testing.T) {
	erpcConfig := map[string]any{
		"projects": []any{
			map[string]any{
				"id":       "rpc",
				"networks": []any{},
				"upstreams": []any{
					map[string]any{"id": "shared", "endpoint": "https://chart.example.com"},
				},
			},
		},
	}
	prov := &erpcProvenance{}
	overlayA := &ERPCOverlay{
		Version: 1,
		Upstreams: []map[string]any{
			{"id": "shared", "endpoint": "https://overlay.example.com"},
		},
	}
	if err := reconcileERPCOverlayConfig(erpcConfig, overlayA, prov); err != nil {
		t.Fatal(err)
	}
	pruneERPCProvenance(prov, overlayA)

	overlayB := &ERPCOverlay{
		Version: 1,
		Upstreams: []map[string]any{
			{"id": "new", "endpoint": "https://new.example.com"},
		},
	}
	if err := reconcileERPCOverlayConfig(erpcConfig, overlayB, prov); err != nil {
		t.Fatal(err)
	}

	project := erpcConfigProject(erpcConfig)
	got := map[string]string{}
	for _, upstream := range asMapSlice(project["upstreams"]) {
		id, _ := upstream["id"].(string)
		endpoint, _ := upstream["endpoint"].(string)
		got[id] = endpoint
	}
	if got["shared"] != "https://chart.example.com" {
		t.Fatalf("shared upstream = %q, want restored chart value", got["shared"])
	}
	if got["new"] != "https://new.example.com" {
		t.Fatalf("new upstream = %q, want replacement overlay value", got["new"])
	}
}

func TestResetERPC_ClusterFailureRetainsRecoveryFiles(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir(), BinDir: t.TempDir()}
	ov := &ERPCOverlay{
		Version: 1,
		Upstreams: []map[string]any{
			{"id": "overlay-upstream", "endpoint": "https://overlay.example.com"},
		},
	}
	if err := writeERPCOverlay(cfg, ov); err != nil {
		t.Fatal(err)
	}
	if err := writeERPCProvenance(cfg, &erpcProvenance{
		Upstreams: map[string]*map[string]any{"overlay-upstream": nil},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := ResetERPC(cfg, ui.NewForTest(&stdout, &stderr))
	if err == nil {
		t.Fatal("ResetERPC succeeded without a running cluster")
	}
	for _, path := range []string{erpcOverlayPath(cfg), erpcProvenancePath(cfg)} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("recovery file %s was removed after cleanup failure: %v", path, statErr)
		}
	}
}

func TestERPCOverlayDriftStatus(t *testing.T) {
	cases := []struct {
		name        string
		localHash   string
		clusterHash string
		clusterErr  error
		want        string
	}{
		{"in sync", "abc123", "abc123", nil, ERPCSyncInSync},
		{"drifted", "abc123", "def456", nil, ERPCSyncDrifted},
		{"not applied", "abc123", "", nil, ERPCSyncNotApplied},
		{"cluster unreachable", "abc123", "", errors.New("no cluster"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := erpcOverlayDriftStatus(tc.localHash, tc.clusterHash, tc.clusterErr)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
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
