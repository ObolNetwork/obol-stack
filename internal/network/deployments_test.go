package network

import (
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
