package network

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// partialArchiveClients lists execution clients that obol-stack knows how
// to configure for partial-archive (non-full, non-genesis-archive) mode.
// Other clients are warned that --since / --mode=archive will fall back
// to upstream defaults — the chart's behavior is not modified.
var partialArchiveClients = map[string]bool{
	"reth": true,
}

// warnIfClientUnsupported emits a one-line warning when the user picked
// a partial-archive scope (or just --mode=archive) on a client whose
// prune surface obol-stack does not wire through. The install continues;
// the resulting node will use upstream chart defaults.
func warnIfClientUnsupported(u *ui.UI, client, mode string, scope ArchiveScope) {
	if partialArchiveClients[client] {
		return
	}
	switch {
	case scope.Kind == "before" || scope.Kind == "distance":
		u.Warnf("--since is currently wired only for reth; %s will run with the chart's default archive behavior", client)
	case mode == "archive":
		u.Warnf("--mode=archive is currently fine-tuned only for reth; %s will run with the chart's default behavior", client)
	}
}

// resolveEthereumArchiveScope resolves the (mode, since) pair to an
// ArchiveScope, prompting interactively when the user didn't pass enough
// flags and a TTY is attached. Returns a zero ArchiveScope for full-node
// installs (mode != "archive").
//
// Resolution rules:
//   - --since wins. If set, mode is forced to "archive" and the scope
//     reflects the resolved since-value.
//   - If --mode is unset and we're on a TTY, prompt full vs archive.
//   - If --mode is "archive" and --since is unset and we're on a TTY,
//     prompt the scope picker.
//   - Non-interactive (no TTY, JSON output): defaults are mode=full,
//     since=genesis (all history). Scripts get deterministic behavior.
func resolveEthereumArchiveScope(u *ui.UI, templateData map[string]string, overrides map[string]string) (ArchiveScope, string, error) {
	network := templateData["Network"]
	if network == "" {
		network = "mainnet"
	}

	modeSet := overrides["mode"] != ""
	sinceRaw := overrides["since"]

	client := templateData["ExecutionClient"]
	if client == "" {
		client = "reth"
	}

	// --since wins outright; mode is forced to archive.
	if sinceRaw != "" {
		// Hardfork-name presets reference mainnet block numbers. Using
		// them on a testnet would silently prune at the wrong height
		// (e.g. mainnet's merge block 15537394 is far past sepolia's
		// tip). Reject with a clear error so the user knows to pass a
		// network-appropriate raw block number or a duration instead.
		if network != "mainnet" && HardforkByName(sinceRaw) != nil {
			return ArchiveScope{}, "", fmt.Errorf("--since=%s is a mainnet hardfork preset and is not valid for %s; use a raw block number or a duration like '365d' instead", sinceRaw, network)
		}
		scope, err := ParseSince(sinceRaw)
		if err != nil {
			return ArchiveScope{}, "", err
		}
		u.Detail("Archive scope", scope.Label+" (from --since)")
		warnIfClientUnsupported(u, client, "archive", scope)
		return scope, "archive", nil
	}

	mode := templateData["Mode"]
	if mode == "" {
		mode = "full"
	}

	// Interactive mode picker when --mode wasn't passed.
	if !modeSet && u.IsTTY() && !u.IsJSON() {
		chosen, err := promptMode(u, network)
		if err != nil {
			return ArchiveScope{}, "", err
		}
		mode = chosen
	}

	if mode != "archive" {
		return ArchiveScope{}, mode, nil
	}

	warnIfClientUnsupported(u, client, mode, ArchiveScope{})

	// Archive selected but no --since. Prompt on TTY; default to genesis
	// otherwise.
	if !u.IsTTY() || u.IsJSON() {
		return ArchiveScope{Kind: "all", Label: "all history (from genesis)"}, mode, nil
	}

	// Non-mainnet archives are small enough that the preset picker isn't
	// worth maintaining a per-testnet hardfork block table. Offer only
	// "all" + custom block.
	if network != "mainnet" {
		scope, err := promptArchiveScopeMinimal(u)
		if err != nil {
			return ArchiveScope{}, "", err
		}
		return scope, mode, nil
	}

	scope, err := promptArchiveScopeMainnet(u)
	if err != nil {
		return ArchiveScope{}, "", err
	}
	return scope, mode, nil
}

func promptMode(u *ui.UI, network string) (string, error) {
	options := []string{
		"full     (~500 GB on mainnet) — prunes historical state, suitable for most uses",
		"archive  (varies)              — keeps historical state for replay / indexers",
	}
	if network != "mainnet" {
		options = []string{
			"full     (~100 GB) — prunes historical state, suitable for most uses",
			"archive  (~300 GB) — keeps historical state for replay / indexers",
		}
	}

	idx, err := u.Select("Node mode:", options, 0)
	if err != nil {
		return "", err
	}
	if idx == 0 {
		return "full", nil
	}
	return "archive", nil
}

func promptArchiveScopeMainnet(u *ui.UI) (ArchiveScope, error) {
	var labels []string
	for _, hf := range MainnetHardforks {
		labels = append(labels,
			fmt.Sprintf("since %-8s (block %d, %s, ~%.1f TB)",
				hf.DisplayName, hf.Block, hf.Date, hf.ApproxArchiveSizeTB))
	}
	labels = append(labels,
		"last 365 days     (~2.6M blocks from tip, ~0.6 TB)",
		"all history       (from genesis, ~4 TB+)",
		"custom block number")

	// Default index = "the merge" (first entry).
	idx, err := u.Select("Archive scope:", labels, 0)
	if err != nil {
		return ArchiveScope{}, err
	}

	if idx < len(MainnetHardforks) {
		hf := MainnetHardforks[idx]
		return ArchiveScope{
			Kind:  "before",
			Block: hf.Block,
			Label: fmt.Sprintf("since %s (block %d, %s)", hf.DisplayName, hf.Block, hf.Date),
		}, nil
	}

	switch idx - len(MainnetHardforks) {
	case 0: // last 365 days
		return ParseSince("365d")
	case 1: // all history
		return ArchiveScope{Kind: "all", Label: "all history (from genesis)"}, nil
	case 2: // custom
		return promptCustomBlock(u)
	}
	return ArchiveScope{}, fmt.Errorf("unexpected picker index %d", idx)
}

func promptArchiveScopeMinimal(u *ui.UI) (ArchiveScope, error) {
	options := []string{
		"all history (from genesis)",
		"custom block number",
	}
	idx, err := u.Select("Archive scope:", options, 0)
	if err != nil {
		return ArchiveScope{}, err
	}
	if idx == 0 {
		return ArchiveScope{Kind: "all", Label: "all history (from genesis)"}, nil
	}
	return promptCustomBlock(u)
}

func promptCustomBlock(u *ui.UI) (ArchiveScope, error) {
	raw, err := u.Input("Block number (keep history from this block forward)", "")
	if err != nil {
		return ArchiveScope{}, err
	}
	scope, err := ParseSince(strings.TrimSpace(raw))
	if err != nil {
		return ArchiveScope{}, err
	}
	if scope.Kind != "before" {
		return ArchiveScope{}, fmt.Errorf("expected a block number, got %q", raw)
	}
	return scope, nil
}

// appendArchiveScopeYAML serializes the resolved scope into a YAML
// fragment for values.yaml, in a form that helmfile reads at deploy time
// to emit per-client prune args.
func appendArchiveScopeYAML(b *strings.Builder, network, mode, executionClient string, scope ArchiveScope) {
	profile := resolveEthereumStorageProfile(network, mode, executionClient, scope)

	b.WriteString("\n# Pruning scope, resolved by `obol network install` from --mode/--since.\n")
	b.WriteString("# Edit via the CLI flags rather than this file; helmfile reads these\n")
	b.WriteString("# verbatim and emits client-specific prune args at deploy time.\n")
	switch scope.Kind {
	case "":
		// mode == "full" — no pruning fields, helmfile's mode branch handles it.
		b.WriteString("pruneKind: \"\"\n")
		b.WriteString("pruneBlock: 0\n")
		b.WriteString("pruneDistance: 0\n")
	case "all":
		b.WriteString("pruneKind: \"all\"\n")
		b.WriteString("pruneBlock: 0\n")
		b.WriteString("pruneDistance: 0\n")
	case "before":
		fmt.Fprintf(b, "pruneKind: \"before\"\npruneBlock: %d\npruneDistance: 0\n", scope.Block)
	case "distance":
		fmt.Fprintf(b, "pruneKind: \"distance\"\npruneBlock: 0\npruneDistance: %d\n", scope.Distance)
	}

	b.WriteString("\n# Storage profile derived from the resolved archive scope.\n")
	fmt.Fprintf(b, "executionStorageSize: %s\n", profile.ExecutionSize)
	fmt.Fprintf(b, "consensusStorageSize: %s\n", profile.ConsensusSize)
	fmt.Fprintf(b, "diskRequirementGB: %d\n", profile.DiskRequirementGB)
}
