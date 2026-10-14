package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestInstalledDeployments(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	if got := InstalledDeployments(cfg); got == nil || len(got) != 0 {
		t.Fatalf("empty config dir: got %#v, want non-nil empty slice", got)
	}

	write := func(typ, id, values string) {
		dir := filepath.Join(cfg.ConfigDir, "networks", typ, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if values != "" {
			if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	write("ethereum", "hoodi", "network: hoodi\nexecutionClient: reth\nconsensusClient: lighthouse\nmode: full\n")
	// Older values.yaml: no mode key.
	write("ethereum", "legacy", "network: mainnet\nexecutionClient: geth\nconsensusClient: teku\n")
	write("aztec", "seq", "network: testnet\nattesterPrivateKey: x\n")
	write("ethereum", "broken", "")

	got := InstalledDeployments(cfg)
	if len(got) != 4 {
		t.Fatalf("got %d deployments, want 4: %#v", len(got), got)
	}

	byID := map[string]Deployment{}
	for _, d := range got {
		byID[d.Identifier()] = d
	}

	if d := byID["ethereum/hoodi"]; d.Namespace != "ethereum-hoodi" || d.Network != "hoodi" || d.ConsensusClient != "lighthouse" {
		t.Errorf("ethereum/hoodi = %#v", d)
	}
	if d := byID["ethereum/legacy"]; d.Mode != "full" || d.ExecutionClient != "geth" {
		t.Errorf("ethereum/legacy = %#v (want mode defaulted to full)", d)
	}
	if d := byID["aztec/seq"]; d.Network != "testnet" || d.Mode != "" {
		t.Errorf("aztec/seq = %#v", d)
	}
	if d := byID["ethereum/broken"]; d.Error == "" {
		t.Errorf("ethereum/broken: want Error for missing values.yaml, got %#v", d)
	}
}

func TestFormatArchiveScope(t *testing.T) {
	cases := []struct {
		name          string
		network, mode string
		since, kind   string
		block, dist   uint64
		want          string
	}{
		{"full", "mainnet", "full", "", "", 0, 0, "full"},
		{"genesis archive", "mainnet", "archive", "genesis", "all", 0, 0, "archive"},
		{"archive without prune fields", "mainnet", "archive", "", "", 0, 0, "archive"},
		{"hardfork", "mainnet", "archive", "cancun", "before", 19426587, 0, "archive since cancun (block 19,426,587)"},
		{"legacy hardfork block", "mainnet", "archive", "", "before", 19426587, 0, "archive since cancun (block 19,426,587)"},
		{"raw block", "mainnet", "archive", "22500000", "before", 22500000, 0, "archive since block 22,500,000"},
		{"raw block equal to a fork is not renamed", "mainnet", "archive", "19426587", "before", 19426587, 0, "archive since block 19,426,587"},
		{"testnet block not named", "sepolia", "archive", "", "before", 19426587, 0, "archive since block 19,426,587"},
		{"duration", "mainnet", "archive", "365d", "distance", 0, 2628000, "archive, last 365d"},
		{"legacy distance", "mainnet", "archive", "", "distance", 0, 2628000, "archive, last 2,628,000 blocks (~365d)"},
		{"short legacy distance", "mainnet", "archive", "", "distance", 0, 300, "archive, last 300 blocks (~1h)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatArchiveScope(c.network, c.mode, c.since, c.kind, c.block, c.dist)
			if got != c.want {
				t.Fatalf("FormatArchiveScope = %q, want %q", got, c.want)
			}
		})
	}
}

func TestInstalledDeploymentsArchiveScope(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	dir := filepath.Join(cfg.ConfigDir, "networks", "ethereum", "legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A partial archive installed via the TTY picker before `since` was
	// recorded: since is empty, the prune block is cancun's.
	values := "network: mainnet\nexecutionClient: reth\nconsensusClient: lighthouse\nmode: archive\nsince: \n" +
		"pruneKind: \"before\"\npruneBlock: 19426587\npruneDistance: 0\n"
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}

	got := InstalledDeployments(cfg)
	if len(got) != 1 {
		t.Fatalf("got %d deployments, want 1", len(got))
	}
	d := got[0]
	if d.ArchiveScope != "archive since cancun (block 19,426,587)" {
		t.Fatalf("ArchiveScope = %q", d.ArchiveScope)
	}

	for _, v := range []any{d, NodeStatus{Deployment: d}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m["mode"] != "archive" || m["archive_scope"] != "archive since cancun (block 19,426,587)" ||
			m["prune_kind"] != "before" || m["prune_block"] != float64(19426587) {
			t.Errorf("JSON = %s", raw)
		}
		if _, ok := m["since"]; ok {
			t.Errorf("empty since should be omitted: %s", raw)
		}
		if _, ok := m["prune_distance"]; ok {
			t.Errorf("zero prune_distance should be omitted: %s", raw)
		}
	}

	// A raw-block since round-trips from YAML (an unquoted int) as a string.
	values = "network: mainnet\nmode: archive\nsince: 22500000\npruneKind: \"before\"\npruneBlock: 22500000\npruneDistance: 0\n"
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := InstalledDeployments(cfg)[0]; d.Since != "22500000" || d.ArchiveScope != "archive since block 22,500,000" || d.Error != "" {
		t.Errorf("raw block deployment = %#v", d)
	}
}
