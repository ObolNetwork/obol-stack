package network

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestResolveEthereumStorageProfile(t *testing.T) {
	distanceScope, err := ParseSince("365d")
	if err != nil {
		t.Fatal(err)
	}
	mergeScope, err := ParseSince("merge")
	if err != nil {
		t.Fatal(err)
	}
	cancunScope, err := ParseSince("cancun")
	if err != nil {
		t.Fatal(err)
	}
	rawPragueScope, err := ParseSince("22500000")
	if err != nil {
		t.Fatal(err)
	}
	rawPreMergeScope, err := ParseSince("1000000")
	if err != nil {
		t.Fatal(err)
	}
	longDistanceScope, err := ParseSince("20y")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name          string
		network       string
		mode          string
		client        string
		scope         ArchiveScope
		wantExec      string
		wantConsensus string
		wantDisk      uint64
	}{
		{
			name:          "full mainnet",
			network:       "mainnet",
			mode:          "full",
			client:        "reth",
			wantExec:      "500Gi",
			wantConsensus: "200Gi",
			wantDisk:      700,
		},
		{
			name:          "genesis archive mainnet",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         ArchiveScope{Kind: "all"},
			wantExec:      "4500Gi",
			wantConsensus: "500Gi",
			wantDisk:      5000,
		},
		{
			name:          "merge partial archive mainnet",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         mergeScope,
			wantExec:      "1900Gi",
			wantConsensus: "500Gi",
			wantDisk:      2600,
		},
		{
			name:          "cancun partial archive mainnet",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         cancunScope,
			wantExec:      "1000Gi",
			wantConsensus: "500Gi",
			wantDisk:      1700,
		},
		{
			name:          "distance partial archive mainnet",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         distanceScope,
			wantExec:      "800Gi",
			wantConsensus: "500Gi",
			wantDisk:      1500,
		},
		{
			name:          "raw block uses nearest conservative hardfork profile",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         rawPragueScope,
			wantExec:      "500Gi",
			wantConsensus: "500Gi",
			wantDisk:      1200,
		},
		{
			name:          "raw pre-merge block stays genesis sized",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         rawPreMergeScope,
			wantExec:      "4500Gi",
			wantConsensus: "500Gi",
			wantDisk:      5000,
		},
		{
			name:          "long duration caps at genesis archive size",
			network:       "mainnet",
			mode:          "archive",
			client:        "reth",
			scope:         longDistanceScope,
			wantExec:      "4500Gi",
			wantConsensus: "500Gi",
			wantDisk:      5000,
		},
		{
			name:          "unsupported client stays genesis sized",
			network:       "mainnet",
			mode:          "archive",
			client:        "geth",
			scope:         distanceScope,
			wantExec:      "4500Gi",
			wantConsensus: "500Gi",
			wantDisk:      5000,
		},
		{
			name:          "testnet archive",
			network:       "hoodi",
			mode:          "archive",
			client:        "reth",
			scope:         ArchiveScope{Kind: "all"},
			wantExec:      "300Gi",
			wantConsensus: "100Gi",
			wantDisk:      400,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveEthereumStorageProfile(c.network, c.mode, c.client, c.scope)
			if got.ExecutionSize != c.wantExec {
				t.Fatalf("ExecutionSize = %q, want %q", got.ExecutionSize, c.wantExec)
			}
			if got.ConsensusSize != c.wantConsensus {
				t.Fatalf("ConsensusSize = %q, want %q", got.ConsensusSize, c.wantConsensus)
			}
			if got.DiskRequirementGB != c.wantDisk {
				t.Fatalf("DiskRequirementGB = %d, want %d", got.DiskRequirementGB, c.wantDisk)
			}
		})
	}
}

func TestInstallEthereumArchiveScopeWritesStorageProfile(t *testing.T) {
	cases := []struct {
		name       string
		overrides  map[string]string
		wantValues []string
		wantStderr []string
	}{
		{
			name: "full mainnet",
			overrides: map[string]string{
				"mode": "full",
			},
			wantValues: []string{
				`mode: full`,
				`pruneKind: ""`,
				`pruneBlock: 0`,
				`pruneDistance: 0`,
				`executionStorageSize: 500Gi`,
				`consensusStorageSize: 200Gi`,
				`diskRequirementGB: 700`,
			},
		},
		{
			name: "genesis archive",
			overrides: map[string]string{
				"mode":  "archive",
				"since": "genesis",
			},
			wantValues: []string{
				`mode: archive`,
				`since: genesis`,
				`pruneKind: "all"`,
				`executionStorageSize: 4500Gi`,
				`consensusStorageSize: 500Gi`,
				`diskRequirementGB: 5000`,
			},
		},
		{
			name: "non-interactive archive without since defaults to genesis",
			overrides: map[string]string{
				"mode": "archive",
			},
			wantValues: []string{
				`mode: archive`,
				`since: `,
				`pruneKind: "all"`,
				`executionStorageSize: 4500Gi`,
				`consensusStorageSize: 500Gi`,
				`diskRequirementGB: 5000`,
			},
		},
		{
			name: "merge partial archive",
			overrides: map[string]string{
				"mode":  "archive",
				"since": "merge",
			},
			wantValues: []string{
				`mode: archive`,
				`since: merge`,
				`pruneKind: "before"`,
				`pruneBlock: 15537394`,
				`executionStorageSize: 1900Gi`,
				`consensusStorageSize: 500Gi`,
				`diskRequirementGB: 2600`,
			},
		},
		{
			name: "duration partial archive",
			overrides: map[string]string{
				"mode":  "archive",
				"since": "365d",
			},
			wantValues: []string{
				`mode: archive`,
				`since: 365d`,
				`pruneKind: "distance"`,
				`pruneDistance: 2628000`,
				`executionStorageSize: 800Gi`,
				`consensusStorageSize: 500Gi`,
				`diskRequirementGB: 1500`,
			},
		},
		{
			name: "raw pre-merge block stays full sized",
			overrides: map[string]string{
				"mode":  "archive",
				"since": "1000000",
			},
			wantValues: []string{
				`mode: archive`,
				`since: 1000000`,
				`pruneKind: "before"`,
				`pruneBlock: 1000000`,
				`executionStorageSize: 4500Gi`,
				`diskRequirementGB: 5000`,
			},
		},
		{
			name: "unsupported client with since stays full sized",
			overrides: map[string]string{
				"execution-client": "geth",
				"mode":             "archive",
				"since":            "365d",
			},
			wantValues: []string{
				`executionClient: geth`,
				`mode: archive`,
				`since: 365d`,
				`pruneKind: "distance"`,
				`executionStorageSize: 4500Gi`,
				`diskRequirementGB: 5000`,
			},
			wantStderr: []string{
				`--since is currently wired only for reth`,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values, stderr := installEthereumValues(t, c.overrides)
			for _, want := range c.wantValues {
				if !strings.Contains(values, want) {
					t.Fatalf("values.yaml missing %q:\n%s", want, values)
				}
			}
			for _, want := range c.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}

func installEthereumValues(t *testing.T, overrides map[string]string) (string, string) {
	t.Helper()

	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir: filepath.Join(tmp, "config"),
		DataDir:   filepath.Join(tmp, "data"),
		BinDir:    filepath.Join(tmp, "bin"),
		StateDir:  filepath.Join(tmp, "state"),
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	installOverrides := map[string]string{"id": "test"}
	for k, v := range overrides {
		installOverrides[k] = v
	}

	var stdout, stderr bytes.Buffer
	u := ui.NewForTest(&stdout, &stderr)
	err := Install(cfg, u, "ethereum", installOverrides, false)
	if err != nil {
		t.Fatalf("Install() error = %v\nstderr:\n%s", err, stderr.String())
	}

	valuesPath := filepath.Join(cfg.ConfigDir, "networks", "ethereum", "test", "values.yaml")
	valuesBytes, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(valuesBytes), stderr.String()
}

func TestEthereumHelmfileRethPruneFlagsCoverAllHistorySegments(t *testing.T) {
	content, err := embed.ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}
	helmfile := string(content)

	for _, want := range []string{
		"--prune.account-history.before={{ .Values.pruneBlock }}",
		"--prune.storage-history.before={{ .Values.pruneBlock }}",
		"--prune.receipts.before={{ .Values.pruneBlock }}",
		"--prune.bodies.before={{ .Values.pruneBlock }}",
		"--prune.account-history.distance={{ .Values.pruneDistance }}",
		"--prune.storage-history.distance={{ .Values.pruneDistance }}",
		"--prune.receipts.distance={{ .Values.pruneDistance }}",
		"--prune.bodies.distance={{ .Values.pruneDistance }}",
	} {
		if !strings.Contains(helmfile, want) {
			t.Fatalf("helmfile missing %q", want)
		}
	}

	for _, notWant := range []string{
		"--prune.receipts.pre-merge",
		"--prune.bodies.pre-merge",
	} {
		if strings.Contains(helmfile, notWant) {
			t.Fatalf("helmfile still uses coarse pre-merge flag %q", notWant)
		}
	}
}
