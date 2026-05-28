package network

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Hardfork describes a mainnet hardfork activation point used by the
// partial-archive picker. All block numbers are verified for Ethereum
// mainnet — paris from the reth chainspec source, post-merge forks from
// the beacon API by computing slot = (fork_ts - beacon_genesis) / 12 and
// reading execution_payload.block_number from the canonical block at (or
// shortly after) that slot.
//
// Verified 2026-05-27 against:
//   - reth v2.2.0 crates/chainspec/src/spec.rs (paris)
//   - go-ethereum params/config.go (post-merge fork timestamps)
//   - ethereum-beacon-api.publicnode.com (slot → execution block lookups)
type Hardfork struct {
	// Name is the canonical EL fork name used by --since=<name>.
	Name string
	// DisplayName is shown in the interactive picker.
	DisplayName string
	// Block is the first execution block at or after the fork activation.
	Block uint64
	// Date is the activation date, used only for picker labels.
	Date string
	// ApproxArchiveSizeTB is a rough estimate of mainnet archive disk usage
	// when pruning state history before this block (reth). Used in picker
	// labels so users have a realistic expectation. Values rounded.
	ApproxArchiveSizeTB float64
}

// MainnetHardforks is the ordered list of mainnet hardforks supported by
// --since presets, oldest first.
var MainnetHardforks = []Hardfork{
	{
		Name:                "merge",
		DisplayName:         "the merge",
		Block:               15537394,
		Date:                "2022-09-15",
		ApproxArchiveSizeTB: 1.5,
	},
	{
		Name:                "shanghai",
		DisplayName:         "shanghai",
		Block:               17034870,
		Date:                "2023-04-12",
		ApproxArchiveSizeTB: 1.2,
	},
	{
		Name:                "cancun",
		DisplayName:         "cancun",
		Block:               19426587,
		Date:                "2024-03-13",
		ApproxArchiveSizeTB: 0.8,
	},
	{
		Name:                "prague",
		DisplayName:         "prague",
		Block:               22431084,
		Date:                "2025-05-07",
		ApproxArchiveSizeTB: 0.4,
	},
	{
		Name:                "osaka",
		DisplayName:         "osaka",
		Block:               23935694,
		Date:                "2025-12-03",
		ApproxArchiveSizeTB: 0.2,
	},
}

// HardforkByName returns the hardfork with the given name, or nil.
func HardforkByName(name string) *Hardfork {
	for i := range MainnetHardforks {
		if MainnetHardforks[i].Name == strings.ToLower(name) {
			return &MainnetHardforks[i]
		}
	}
	return nil
}

// ArchiveScope is the resolved meaning of --since after parsing.
type ArchiveScope struct {
	// Kind is one of: "all" (full archive from genesis), "before" (prune
	// before a specific block), "distance" (keep last N blocks of history).
	Kind string
	// Block is set when Kind == "before".
	Block uint64
	// Distance is set when Kind == "distance".
	Distance uint64
	// Label is a human-readable description for logs/picker confirmation.
	Label string
}

// ParseSince resolves a --since value into an ArchiveScope. Accepted
// forms:
//   - "genesis", "all": full archive from genesis
//   - "merge", "shanghai", "cancun", "prague", "osaka": EL hardfork name
//   - "<N>d", "<N>mo", "<N>y": duration back from chain head (12s slots)
//   - "<block>": raw block number (any unsigned integer)
//
// Mode is reth-anchored: "before" presets get translated to reth's
// --prune.<segment>.before flags; "distance" presets to --prune.<segment>.distance.
func ParseSince(raw string) (ArchiveScope, error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return ArchiveScope{}, fmt.Errorf("--since cannot be empty")
	}

	if v == "genesis" || v == "all" {
		return ArchiveScope{Kind: "all", Label: "all history (from genesis)"}, nil
	}

	if hf := HardforkByName(v); hf != nil {
		return ArchiveScope{
			Kind:  "before",
			Block: hf.Block,
			Label: fmt.Sprintf("since %s (block %d, %s)", hf.DisplayName, hf.Block, hf.Date),
		}, nil
	}

	// Duration: <N>{d,mo,y}. Translates to block distance using the
	// post-merge slot time of 12s. "mo" is 30 days; "y" is 365 days.
	if blocks, ok := parseDurationBlocks(v); ok {
		return ArchiveScope{
			Kind:     "distance",
			Distance: blocks,
			Label:    fmt.Sprintf("last %s (~%d blocks from tip)", v, blocks),
		}, nil
	}

	// Raw block number.
	if n, err := strconv.ParseUint(v, 10, 64); err == nil {
		return ArchiveScope{
			Kind:  "before",
			Block: n,
			Label: fmt.Sprintf("since block %d", n),
		}, nil
	}

	return ArchiveScope{}, fmt.Errorf("unrecognized --since value %q (try: genesis, merge, shanghai, cancun, prague, osaka, 365d, 1y, or a block number)", raw)
}

// parseDurationBlocks accepts "<N>d|mo|y" and returns the equivalent
// post-merge block distance (12s slots, ignoring missed slots — close
// enough for pruning purposes since reth uses --prune.*.distance which
// is anchored to chain tip at run time).
func parseDurationBlocks(v string) (uint64, bool) {
	var n uint64
	var unit string
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			parsed, err := strconv.ParseUint(v[:i], 10, 64)
			if err != nil || i == 0 {
				return 0, false
			}
			n = parsed
			unit = v[i:]
			break
		}
	}
	if unit == "" {
		return 0, false
	}

	var seconds uint64
	switch unit {
	case "d", "day", "days":
		seconds = n * uint64(24*time.Hour/time.Second)
	case "mo", "month", "months":
		seconds = n * 30 * uint64(24*time.Hour/time.Second)
	case "y", "yr", "year", "years":
		seconds = n * 365 * uint64(24*time.Hour/time.Second)
	default:
		return 0, false
	}

	// 12s per slot post-merge.
	return seconds / 12, true
}
