package network

import (
	"sync"
	"time"
)

// RateTracker turns successive status samples into per-stage rates and
// ETAs over a sliding window (network status --watch).
type RateTracker struct {
	// Window is how far back samples count (default 10m).
	Window time.Duration

	mu      sync.Mutex
	samples map[string][]rateSample
}

type rateSample struct {
	at            time.Time
	block, target uint64
}

// minRateSpan is the minimum time between the oldest and newest sample
// before a rate is reported.
const minRateSpan = 4 * time.Second

// minStallSpan is how long a stage must show no progress before a zero rate
// is reported. reth only moves a stage checkpoint when it commits a batch
// (Execution can sit on one value for minutes while it executes blocks).
const minStallSpan = 5 * time.Minute

// defaultRateWindow spans several reth commits so a rate doesn't swing
// between zero and a burst.
const defaultRateWindow = 10 * time.Minute

// NewRateTracker returns a tracker with a 10 minute window.
func NewRateTracker() *RateTracker {
	return &RateTracker{Window: defaultRateWindow, samples: map[string][]rateSample{}}
}

// Annotate records the current stage of each probed node and sets
// RatePerSec/ETASeconds on it once the window has two samples. The ETA
// accounts for a moving target (a beacon head advances 1 slot / 12s).
func (r *RateTracker) Annotate(nodes []NodeStatus, at time.Time) {
	for i := range nodes {
		n := &nodes[i]
		if el := n.Execution; el != nil {
			r.annotate(n.Namespace+"|el", el.Stages, at)
		}

		if cl := n.Consensus; cl != nil {
			r.annotate(n.Namespace+"|cl", cl.Stages, at)
		}
	}
}

func (r *RateTracker) annotate(prefix string, stages []SyncStage, at time.Time) {
	for i := range stages {
		s := &stages[i]
		if s.State != StageCurrent {
			continue
		}

		rate, closing, ok := r.observe(prefix+"|"+s.Name, at, s.Block, s.Target)
		if !ok {
			continue
		}

		s.RatePerSec = &rate

		if s.Target > s.Block && closing > 0 {
			eta := int64(float64(s.Target-s.Block) / closing)
			s.ETASeconds = &eta
		}
	}
}

// observe adds a sample and returns the progress rate and the rate at
// which the gap to the target closes, both per second.
func (r *RateTracker) observe(key string, at time.Time, block, target uint64) (rate, closing float64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.samples == nil {
		r.samples = map[string][]rateSample{}
	}

	window := r.Window
	if window <= 0 {
		window = defaultRateWindow
	}

	s := r.samples[key]
	s = append(s, rateSample{at: at, block: block, target: target})

	// A stage that went backwards (unwind, restart from scratch) restarts
	// the window.
	if n := len(s); n > 1 && block < s[n-2].block {
		s = s[n-1:]
	}

	for len(s) > 2 && at.Sub(s[0].at) > window {
		s = s[1:]
	}

	r.samples[key] = s

	first, last := s[0], s[len(s)-1]

	span := last.at.Sub(first.at).Seconds()
	if span < minRateSpan.Seconds() {
		return 0, 0, false
	}

	if last.block == first.block && span < minStallSpan.Seconds() {
		return 0, 0, false
	}

	rate = float64(last.block-first.block) / span
	targetRate := (float64(last.target) - float64(first.target)) / span
	closing = rate - targetRate

	return rate, closing, true
}
