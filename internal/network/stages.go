package network

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Stage states.
const (
	StageDone    = "done"
	StageCurrent = "current"
	StagePending = "pending"
	StageStalled = "stalled"
)

// Units of SyncStage.Block/Target.
const (
	UnitBlocks = "blocks"
	UnitSlots  = "slots"
)

// SyncStage is one step of a client's sync: a reth pipeline stage, or a
// beacon node phase (finalized sync, head sync, backfill).
type SyncStage struct {
	Name string `json:"name"`
	// Block is the progress reached (a block, a slot, or for backfill the
	// number of slots filled), in Unit.
	Block  uint64 `json:"block"`
	Target uint64 `json:"target,omitempty"`
	State  string `json:"state"`
	Unit   string `json:"unit,omitempty"`
	// RatePerSec and ETASeconds are set by --watch once a few samples exist.
	RatePerSec *float64 `json:"rate_per_sec,omitempty"`
	ETASeconds *int64   `json:"eta_seconds,omitempty"`
}

// Pct is Block/Target in percent (one decimal), or false without a target.
func (s SyncStage) Pct() (float64, bool) {
	if s.Target == 0 {
		return 0, false
	}

	return percent(s.Block, s.Target), true
}

// annotateExecutionStages marks reth's pipeline stages done/current/pending
// against the sync target: the highest stage checkpoint (Headers, once the
// header download finished) or highestBlock if that is further. A stage is
// done once it reached the target, or once a later stage has gone past it
// (stages the node's prune config skips can keep a 0 checkpoint).
//
// reth reports its stages alphabetically, so they are first put back in
// pipeline order; "passed by a later stage" only means something there.
func annotateExecutionStages(el *ExecutionStatus) {
	if len(el.Stages) == 0 {
		return
	}

	sortRethStages(el.Stages)

	target := el.HighestBlock
	for _, s := range el.Stages {
		target = max(target, s.Block)
	}

	current := false
	for i := range el.Stages {
		s := &el.Stages[i]
		s.Target = target

		passed := false
		for _, later := range el.Stages[i+1:] {
			if later.Block > s.Block {
				passed = true
				break
			}
		}

		switch {
		case target > 0 && (s.Block >= target || passed):
			s.State = StageDone
		case !current:
			s.State = StageCurrent
			current = true
		default:
			s.State = StagePending
		}
	}
}

// rethPipelineOrder is reth's default pipeline (crates/stages/stages/src/sets.rs).
var rethPipelineOrder = []string{
	"Era", "Headers", "Bodies", "SenderRecovery", "Execution",
	"PruneSenderRecovery", "MerkleUnwind", "AccountHashing", "StorageHashing",
	"MerkleExecute", "TransactionLookup", "IndexStorageHistory",
	"IndexAccountHistory", "Prune", "Finish",
}

// sortRethStages orders stages by rethPipelineOrder; unknown stages keep
// their relative order after the known ones.
func sortRethStages(stages []SyncStage) {
	rank := func(name string) int {
		if i := slices.Index(rethPipelineOrder, name); i >= 0 {
			return i
		}

		return len(rethPipelineOrder)
	}

	slices.SortStableFunc(stages, func(a, b SyncStage) int {
		return rank(a.Name) - rank(b.Name)
	})
}

// executionStage returns reth's "Execution" stage and the sync target.
func executionStage(el *ExecutionStatus) (SyncStage, uint64, bool) {
	for _, s := range el.Stages {
		if s.Name == "Execution" {
			return s, s.Target, true
		}
	}

	return SyncStage{}, 0, false
}

// parseLighthouseSyncing reads Lighthouse's /lighthouse/syncing, an
// externally tagged enum: "Synced" | "Stalled" | "SyncTransition" |
// {"SyncingFinalized": {start_slot, target_slot}} |
// {"SyncingHead": {start_slot, target_slot}} |
// {"BackFillSyncing": {completed, remaining}}.
func parseLighthouseSyncing(body []byte, cl *ConsensusStatus) error {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("/lighthouse/syncing: %w", err)
	}

	var plain string
	if json.Unmarshal(env.Data, &plain) == nil {
		cl.SyncState = plain
		return nil
	}

	var tagged map[string]struct {
		StartSlot  beaconUint `json:"start_slot"`
		TargetSlot beaconUint `json:"target_slot"`
		Completed  beaconUint `json:"completed"`
		Remaining  beaconUint `json:"remaining"`
	}
	if err := json.Unmarshal(env.Data, &tagged); err != nil {
		return fmt.Errorf("/lighthouse/syncing: %w", err)
	}

	for state, v := range tagged {
		cl.SyncState = state
		cl.SyncTargetSlot = uint64(v.TargetSlot)
		cl.BackfillCompleted = uint64(v.Completed)
		cl.BackfillRemaining = uint64(v.Remaining)
	}

	return nil
}

// Consensus stage names.
const (
	clStageFinalized = "Finalized sync"
	clStageHead      = "Head sync"
	clStageBackfill  = "Backfill"
	clStageSynced    = "At head"
	clStageOptimist  = "At head (optimistic)"
	clStageStalled   = "Stalled"
)

// consensusStages maps the beacon node's sync state to human stages. With
// Lighthouse's own state it shows finalized sync → head sync → backfill
// (history download after a checkpoint sync); other clients get head sync
// vs. at head from the standard API.
func consensusStages(cl *ConsensusStatus) []SyncStage {
	head := cl.HeadSlot
	headTarget := max(cl.SyncTargetSlot, head+cl.SyncDistance)
	slot := func(name, state string, block, target uint64) SyncStage {
		return SyncStage{Name: name, State: state, Block: block, Target: target, Unit: UnitSlots}
	}

	switch cl.SyncState {
	case "SyncingFinalized":
		return []SyncStage{
			slot(clStageFinalized, StageCurrent, head, headTarget),
			slot(clStageHead, StagePending, 0, 0),
			slot(clStageBackfill, StagePending, 0, 0),
		}
	case "SyncingHead":
		return []SyncStage{
			slot(clStageFinalized, StageDone, 0, 0),
			slot(clStageHead, StageCurrent, head, headTarget),
			slot(clStageBackfill, StagePending, 0, 0),
		}
	case "BackFillSyncing":
		return []SyncStage{
			slot(clStageSynced, StageDone, head, 0),
			slot(clStageBackfill, StageCurrent, cl.BackfillCompleted, cl.BackfillCompleted+cl.BackfillRemaining),
		}
	case "Stalled":
		return []SyncStage{slot(clStageStalled, StageStalled, head, 0)}
	}

	switch {
	case cl.IsSyncing:
		return []SyncStage{slot(clStageHead, StageCurrent, head, headTarget)}
	case cl.IsOptimistic:
		return []SyncStage{slot(clStageOptimist, StageCurrent, head, 0)}
	default:
		return []SyncStage{slot(clStageSynced, StageDone, head, 0)}
	}
}
