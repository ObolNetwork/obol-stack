// Package kb is the collective knowledge base for a decentralized
// auto-research program: the workspace AutoScientists agents coordinate
// through (results log, champion, roster), plus the acceptance rule
// (autoresearch's KEEP) and the impact-proportional payout split.
//
// It is pure and concurrency-safe; the HTTP surface (membership gating,
// device-auth) lives in internal/research/server.
package kb

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// Direction is whether lower or higher metric values are better.
type Direction string

const (
	Minimize Direction = "minimize"
	Maximize Direction = "maximize"
)

// AcceptMode is the KEEP rule for a submitted result.
type AcceptMode string

const (
	// BeatsChampion accepts a result that improves on the current champion
	// (or the published Baseline when there is no champion yet).
	BeatsChampion AcceptMode = "beats-champion"
	// Threshold accepts any result that clears Criteria.Threshold.
	Threshold AcceptMode = "threshold"
)

// SplitMode decides how the reward pool is divided at close.
type SplitMode string

const (
	// ByImpact divides the pool proportional to each accepted result's
	// validated impact (the metric improvement it delivered).
	ByImpact SplitMode = "by-impact"
	// ChampionTakesAll pays the whole pool to the final champion's worker.
	ChampionTakesAll SplitMode = "champion-takes-all"
)

// Criteria mirrors AutoScientists' TASK.md frontmatter: an arbitrary
// metric name + a direction + the KEEP rule. Nothing domain-specific.
type Criteria struct {
	Metric    string     `json:"metric"`
	Direction Direction  `json:"direction"`
	Accept    AcceptMode `json:"accept"`
	Threshold *float64   `json:"threshold,omitempty"`
}

// Program is the published research ID.
type Program struct {
	ID        string    `json:"id"`
	Objective string    `json:"objective"`
	Criteria  Criteria  `json:"criteria"`
	Baseline  *float64  `json:"baseline,omitempty"` // reference metric; first improvement's impact is measured against it
	Pool      float64   `json:"pool"`
	Token     string    `json:"token"`
	Network   string    `json:"network"`
	Split     SplitMode `json:"split"`
}

// Result is one submitted experiment outcome.
type Result struct {
	Seq      int       `json:"seq"`
	Worker   string    `json:"worker"`
	Value    float64   `json:"value"`
	Output   string    `json:"output,omitempty"` // raw train.py tail, for audit
	At       time.Time `json:"at"`
	Accepted bool      `json:"accepted"`
	Impact   float64   `json:"impact"`
	Champion bool      `json:"champion"` // true if this result became the champion
}

// KB holds one program's collective state.
type KB struct {
	mu       sync.Mutex
	prog     Program
	results  []Result
	champion *Result
	roster   map[string]time.Time
	seq      int
	now      func() time.Time
}

// New returns a KB for a program.
func New(p Program) *KB {
	return &KB{prog: p, roster: map[string]time.Time{}, now: time.Now}
}

// Program returns the published program (immutable copy).
func (k *KB) Program() Program {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.prog
}

// Join records a worker in the roster (idempotent).
func (k *KB) Join(worker string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.roster[worker]; !ok {
		k.roster[worker] = k.now()
	}
}

// Roster returns the joined workers and when they joined.
func (k *KB) Roster() map[string]time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]time.Time, len(k.roster))
	for w, t := range k.roster {
		out[w] = t
	}
	return out
}

// Champion returns a copy of the current champion result, or nil.
func (k *KB) Champion() *Result {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.champion == nil {
		return nil
	}
	c := *k.champion
	return &c
}

// Results returns the full results log in submission order.
func (k *KB) Results() []Result {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]Result, len(k.results))
	copy(out, k.results)
	return out
}

// Submit applies the KEEP rule to a worker's result: it records the result,
// decides acceptance + impact, and promotes the champion if it improved.
// Submission is serialized, so the FIRST result to beat the current best
// wins it (first-verified-wins). A worker that is not in the roster is
// auto-joined.
func (k *KB) Submit(worker string, value float64, output string) (Result, error) {
	if worker == "" {
		return Result{}, errors.New("kb: worker required")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Result{}, errors.New("kb: value must be finite")
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if _, ok := k.roster[worker]; !ok {
		k.roster[worker] = k.now()
	}

	k.seq++
	r := Result{Seq: k.seq, Worker: worker, Value: value, Output: output, At: k.now()}

	switch k.prog.Criteria.Accept {
	case Threshold:
		if k.prog.Criteria.Threshold != nil {
			th := *k.prog.Criteria.Threshold
			if k.better(value, th) || value == th {
				r.Accepted = true
				r.Impact = k.gain(th, value)
			}
		}
		// The champion under threshold mode is still the best-so-far.
		if r.Accepted && (k.champion == nil || k.better(value, k.champion.Value)) {
			r.Champion = true
		}
	default: // BeatsChampion
		ref, hasRef := k.bestRef()
		if !hasRef || k.better(value, ref) {
			r.Accepted = true
			r.Champion = true
			if hasRef {
				r.Impact = k.gain(ref, value)
			} else {
				// First result with no Baseline sets the baseline only.
				r.Impact = 0
			}
		}
	}

	if r.Champion {
		c := r
		k.champion = &c
	}
	k.results = append(k.results, r)
	return r, nil
}

// Payouts splits the reward pool across workers per the program's SplitMode.
// Returns worker -> amount (rounded to 6 dp). Empty when nothing is owed.
func (k *KB) Payouts() map[string]float64 {
	k.mu.Lock()
	defer k.mu.Unlock()

	out := map[string]float64{}
	if k.prog.Pool <= 0 {
		return out
	}

	if k.prog.Split == ChampionTakesAll {
		if k.champion != nil {
			out[k.champion.Worker] = round6(k.prog.Pool)
		}
		return out
	}

	// ByImpact (default): proportional to accepted impact.
	per := map[string]float64{}
	if k.prog.Criteria.Accept == Threshold {
		// Threshold mode accepts EVERY passing result, so summing per-worker
		// lets a worker inflate their share by resubmitting the same passing
		// result. Credit each worker's BEST accepted impact only. (BeatsChampion
		// is already safe: a duplicate is never better than the champion, so it
		// is not accepted — there the sum reflects genuine cumulative progress.)
		for _, r := range k.results {
			if r.Accepted && r.Impact > per[r.Worker] {
				per[r.Worker] = r.Impact
			}
		}
	} else {
		for _, r := range k.results {
			if r.Accepted && r.Impact > 0 {
				per[r.Worker] += r.Impact
			}
		}
	}
	total := 0.0
	for _, imp := range per {
		total += imp
	}
	if total <= 0 {
		return out
	}
	for w, imp := range per {
		out[w] = round6(k.prog.Pool * imp / total)
	}
	return out
}

// --- helpers ---

// better reports whether a is strictly better than b under the direction.
func (k *KB) better(a, b float64) bool {
	if k.prog.Criteria.Direction == Maximize {
		return a > b
	}
	return a < b
}

// gain is the non-negative improvement of value over ref under the
// program's direction (ref-value when minimizing, value-ref when maximizing).
func (k *KB) gain(ref, value float64) float64 {
	var d float64
	if k.prog.Criteria.Direction == Maximize {
		d = value - ref
	} else {
		d = ref - value
	}
	if d < 0 {
		d = 0
	}
	return d
}

// bestRef returns the current reference to beat (champion value, else
// Baseline) and whether one exists.
func (k *KB) bestRef() (float64, bool) {
	if k.champion != nil {
		return k.champion.Value, true
	}
	if k.prog.Baseline != nil {
		return *k.prog.Baseline, true
	}
	return 0, false
}

// Sorted snapshot helper for stable status output.
func (k *KB) RosterSorted() []string {
	r := k.Roster()
	ws := make([]string, 0, len(r))
	for w := range r {
		ws = append(ws, w)
	}
	sort.Strings(ws)
	return ws
}

func round6(f float64) float64 {
	return math.Round(f*1e6) / 1e6
}
