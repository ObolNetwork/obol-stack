package bounty

// Reputation decay (design doc §11.4): ladder weight earned by an evaluator
// halves every decayHalfLife of inactivity past lastEvalAt. These are PURE
// read-time functions — nothing here mutates ladder status. Stored records
// keep their raw counters; decay is applied only where reputation is READ
// (selection weights and tier gating), so an evaluator who returns from a
// long idle resumes from their stored counters, just with less pull until
// they participate again.

import (
	"math"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// defaultDecayHalfLife mirrors applyLadderDefaults (registry.go) for callers
// holding a zero/unparseable Ladder.
const defaultDecayHalfLife = 720 * time.Hour

// DecayHalfLifeDuration parses the ladder's decayHalfLife knob, falling back
// to the registry default (720h) when it is missing or unparseable.
func (l Ladder) DecayHalfLifeDuration() time.Duration {
	if d, err := time.ParseDuration(l.DecayHalfLife); err == nil && d > 0 {
		return d
	}
	return defaultDecayHalfLife
}

// EffectiveCompleted is the decayed completion count:
//
//	completed × 2^(−idle/halfLife)
//
// where idle = now − lastEvalAt. A nil lastEvalAt is a legacy record from
// before decay landed — there is no anchor to decay from, so it is taken at
// face value.
func EffectiveCompleted(completed int, lastEvalAt *time.Time, now time.Time, halfLife time.Duration) float64 {
	if lastEvalAt == nil || halfLife <= 0 {
		return float64(completed)
	}
	idle := now.Sub(*lastEvalAt)
	if idle <= 0 {
		return float64(completed)
	}
	return float64(completed) * math.Exp2(-float64(idle)/float64(halfLife))
}

// EffectiveTier is the read-time tier gate: a stored "Full" record whose
// decayed completion count has fallen below the task's probation threshold
// AND whose idle time exceeds the half-life is treated as Probation for
// selection purposes — stale reputation buys a discounted seat, not a full
// one. Every other case returns the stored tier unchanged (legacy records
// with no lastEvalAt anchor are never demoted).
func EffectiveTier(record monetizeapi.EvaluatorLadderRecord, ladder Ladder, now time.Time) string {
	if record.Tier != monetizeapi.EvaluatorTierFull || record.LastEvalAt == nil {
		return record.Tier
	}
	halfLife := ladder.DecayHalfLifeDuration()
	if now.Sub(record.LastEvalAt.Time) <= halfLife {
		return record.Tier
	}
	if EffectiveCompleted(int(record.Completed), &record.LastEvalAt.Time, now, halfLife) < float64(ladder.ProbationEvals) {
		return monetizeapi.EvaluatorTierProbation
	}
	return record.Tier
}
