package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

// Deployment is one installed local node, read from its values.yaml under
// $CONFIG_DIR/networks/<type>/<id>/. Fields that a network type does not
// have (e.g. clients for aztec) stay empty.
type Deployment struct {
	Type            string `json:"type"`
	ID              string `json:"id"`
	Namespace       string `json:"namespace"`
	Network         string `json:"network,omitempty"`
	ExecutionClient string `json:"execution_client,omitempty"`
	ConsensusClient string `json:"consensus_client,omitempty"`
	Mode            string `json:"mode,omitempty"`
	// ArchiveScope is the human-readable mode + history scope, e.g. "full",
	// "archive", "archive since cancun (block 19,426,587)" or
	// "archive, last 365d" (ethereum only; see FormatArchiveScope).
	ArchiveScope string `json:"archive_scope,omitempty"`
	// Raw pruning scope from values.yaml (ethereum only).
	PruneKind     string `json:"prune_kind,omitempty"`
	PruneBlock    uint64 `json:"prune_block,omitempty"`
	PruneDistance uint64 `json:"prune_distance,omitempty"`
	Since         string `json:"since,omitempty"`
	// Stopped is set by `obol network stop`: workloads scaled to 0, data
	// kept, not registered with eRPC (values.yaml `stopped: true`).
	Stopped bool `json:"stopped,omitempty"`
	// Error is set when values.yaml could not be read; the deployment
	// directory still exists, so it is listed rather than hidden.
	Error string `json:"error,omitempty"`
}

// Identifier is the "<type>/<id>" form every network subcommand accepts.
func (d Deployment) Identifier() string { return d.Type + "/" + d.ID }

// InstalledDeployments returns every installed network deployment (see
// ListDeployments) with the settings from its values.yaml. It only reads the
// config dir, so it works with the cluster down. Never returns nil.
func InstalledDeployments(cfg *config.Config) []Deployment {
	ids := ListDeployments(cfg)
	out := make([]Deployment, 0, len(ids))
	for _, ident := range ids {
		typ, id, _ := strings.Cut(ident, "/")
		out = append(out, readDeployment(cfg, typ, id))
	}

	return out
}

func readDeployment(cfg *config.Config, typ, id string) Deployment {
	d := Deployment{Type: typ, ID: id, Namespace: fmt.Sprintf("%s-%s", typ, id)}

	raw, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "networks", typ, id, "values.yaml"))
	if err != nil {
		d.Error = "values.yaml unreadable: " + err.Error()
		return d
	}

	var values struct {
		Network         string `yaml:"network"`
		ExecutionClient string `yaml:"executionClient"`
		ConsensusClient string `yaml:"consensusClient"`
		Mode            string `yaml:"mode"`
		Since           string `yaml:"since"`
		PruneKind       string `yaml:"pruneKind"`
		PruneBlock      uint64 `yaml:"pruneBlock"`
		PruneDistance   uint64 `yaml:"pruneDistance"`
		Stopped         bool   `yaml:"stopped"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		d.Error = "values.yaml invalid: " + err.Error()
		return d
	}

	d.Network = values.Network
	d.ExecutionClient = values.ExecutionClient
	d.ConsensusClient = values.ConsensusClient
	d.Mode = values.Mode
	d.Stopped = values.Stopped
	if typ == TypeEthereum {
		if d.Mode == "" {
			d.Mode = modeFull
		}
		d.Since = values.Since
		d.PruneKind = values.PruneKind
		d.PruneBlock = values.PruneBlock
		d.PruneDistance = values.PruneDistance
		d.ArchiveScope = FormatArchiveScope(d.Network, d.Mode, d.Since, d.PruneKind, d.PruneBlock, d.PruneDistance)
	}

	return d
}

const (
	modeFull       = "full"
	modeArchive    = "archive"
	networkMainnet = "mainnet"
)

// FormatArchiveScope renders an ethereum node's mode and history scope for
// humans, so a partial archive is not mistaken for a genesis archive:
//
//	full
//	archive                                     (genesis / no pruning)
//	archive since cancun (block 19,426,587)
//	archive since block 22,500,000
//	archive, last 365d
//	archive, last 2,628,000 blocks (~365d)      (no since recorded)
//
// Nodes installed before `since` was recorded have it empty; a pruneBlock
// matching a known mainnet hardfork is then named via HardforkByBlock.
func FormatArchiveScope(network, mode, since, pruneKind string, pruneBlock, pruneDistance uint64) string {
	if mode != modeArchive {
		return mode
	}

	since = strings.ToLower(strings.TrimSpace(since))
	switch pruneKind {
	case PruneBefore:
		if pruneBlock == 0 {
			return modeArchive
		}
		name := ""
		if hf := HardforkByName(since); hf != nil && hf.Block == pruneBlock {
			name = hf.Name
		} else if since == "" && (network == "" || network == networkMainnet) {
			if hf := HardforkByBlock(pruneBlock); hf != nil {
				name = hf.Name
			}
		}
		if name != "" {
			return fmt.Sprintf("archive since %s (block %s)", name, formatThousands(pruneBlock))
		}
		return "archive since block " + formatThousands(pruneBlock)
	case PruneDistance:
		if pruneDistance == 0 {
			return modeArchive
		}
		if blocks, ok := parseDurationBlocks(since); ok && blocks == pruneDistance {
			return "archive, last " + since
		}
		return fmt.Sprintf("archive, last %s blocks (~%s)", formatThousands(pruneDistance), approxBlocksDuration(pruneDistance))
	default: // "", "all": no pruning, i.e. genesis archive
		return modeArchive
	}
}

// approxBlocksDuration is the inverse of parseDurationBlocks: the wall time
// covered by n blocks at the post-merge 12s slot rate, in days (or hours
// below a day).
func approxBlocksDuration(n uint64) string {
	seconds := n * 12
	const day = 24 * 60 * 60
	if seconds < day {
		return fmt.Sprintf("%dh", (seconds+1800)/3600)
	}
	return fmt.Sprintf("%dd", (seconds+day/2)/day)
}

// formatThousands renders n with comma thousands separators (19426587 ->
// "19,426,587").
func formatThousands(n uint64) string {
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
