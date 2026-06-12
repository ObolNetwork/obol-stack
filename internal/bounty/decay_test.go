package bounty

import (
	"math"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const halfLife = 720 * time.Hour

func TestEffectiveCompleted_HalvesAfterOneHalfLife(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	last := now.Add(-halfLife)
	got := EffectiveCompleted(10, &last, now, halfLife)
	if math.Abs(got-5.0) > 1e-9 {
		t.Fatalf("EffectiveCompleted after one half-life = %v, want 5.0", got)
	}
	last2 := now.Add(-2 * halfLife)
	if got := EffectiveCompleted(10, &last2, now, halfLife); math.Abs(got-2.5) > 1e-9 {
		t.Fatalf("EffectiveCompleted after two half-lives = %v, want 2.5", got)
	}
}

func TestEffectiveCompleted_NilLastEvalNoDecay(t *testing.T) {
	now := time.Now()
	if got := EffectiveCompleted(10, nil, now, halfLife); got != 10.0 {
		t.Fatalf("legacy record (nil lastEvalAt) must not decay, got %v", got)
	}
}

func TestEffectiveCompleted_FreshAndZeroHalfLife(t *testing.T) {
	now := time.Now()
	fresh := now
	if got := EffectiveCompleted(7, &fresh, now, halfLife); got != 7.0 {
		t.Fatalf("zero idle must not decay, got %v", got)
	}
	future := now.Add(time.Hour)
	if got := EffectiveCompleted(7, &future, now, halfLife); got != 7.0 {
		t.Fatalf("clock-skewed future lastEvalAt must not decay, got %v", got)
	}
	old := now.Add(-halfLife)
	if got := EffectiveCompleted(7, &old, now, 0); got != 7.0 {
		t.Fatalf("non-positive half-life must disable decay, got %v", got)
	}
}

func TestEffectiveTier_StaleFullDemotedToProbation(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	ladder := Ladder{ProbationEvals: 10, DecayHalfLife: "720h"}
	record := monetizeapi.EvaluatorLadderRecord{
		Tier:       monetizeapi.EvaluatorTierFull,
		Completed:  10,
		LastEvalAt: &metav1.Time{Time: now.Add(-2 * halfLife)}, // effective 2.5 < 10
	}
	if got := EffectiveTier(record, ladder, now); got != monetizeapi.EvaluatorTierProbation {
		t.Fatalf("stale Full must read as Probation, got %s", got)
	}
}

func TestEffectiveTier_FreshFullStaysFull(t *testing.T) {
	now := time.Now()
	ladder := Ladder{ProbationEvals: 10, DecayHalfLife: "720h"}
	record := monetizeapi.EvaluatorLadderRecord{
		Tier:       monetizeapi.EvaluatorTierFull,
		Completed:  10,
		LastEvalAt: &metav1.Time{Time: now.Add(-halfLife / 2)}, // idle under the half-life
	}
	if got := EffectiveTier(record, ladder, now); got != monetizeapi.EvaluatorTierFull {
		t.Fatalf("Full within the half-life must stay Full, got %s", got)
	}
}

func TestEffectiveTier_HighVolumeFullSurvivesIdle(t *testing.T) {
	now := time.Now()
	ladder := Ladder{ProbationEvals: 10, DecayHalfLife: "720h"}
	record := monetizeapi.EvaluatorLadderRecord{
		Tier:       monetizeapi.EvaluatorTierFull,
		Completed:  100, // effective 25 after two half-lives, still ≥ 10
		LastEvalAt: &metav1.Time{Time: now.Add(-2 * halfLife)},
	}
	if got := EffectiveTier(record, ladder, now); got != monetizeapi.EvaluatorTierFull {
		t.Fatalf("high-volume Full must survive the idle window, got %s", got)
	}
}

func TestEffectiveTier_LegacyAndNonFullUntouched(t *testing.T) {
	now := time.Now()
	ladder := Ladder{ProbationEvals: 10, DecayHalfLife: "720h"}
	legacy := monetizeapi.EvaluatorLadderRecord{Tier: monetizeapi.EvaluatorTierFull, Completed: 1}
	if got := EffectiveTier(legacy, ladder, now); got != monetizeapi.EvaluatorTierFull {
		t.Fatalf("legacy record (nil lastEvalAt) must keep its stored tier, got %s", got)
	}
	shadow := monetizeapi.EvaluatorLadderRecord{
		Tier:       monetizeapi.EvaluatorTierShadow,
		LastEvalAt: &metav1.Time{Time: now.Add(-10 * halfLife)},
	}
	if got := EffectiveTier(shadow, ladder, now); got != monetizeapi.EvaluatorTierShadow {
		t.Fatalf("non-Full tiers are never demoted further, got %s", got)
	}
}

func TestDecayHalfLifeDuration(t *testing.T) {
	if got := (Ladder{}).DecayHalfLifeDuration(); got != defaultDecayHalfLife {
		t.Fatalf("zero ladder must default to %v, got %v", defaultDecayHalfLife, got)
	}
	if got := (Ladder{DecayHalfLife: "48h"}).DecayHalfLifeDuration(); got != 48*time.Hour {
		t.Fatalf("parseable half-life = %v, want 48h", got)
	}
	if got := (Ladder{DecayHalfLife: "soon"}).DecayHalfLifeDuration(); got != defaultDecayHalfLife {
		t.Fatalf("unparseable half-life must default, got %v", got)
	}
}
