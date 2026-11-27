package network

import (
	"fmt"
	"time"
)

// StoppedFor is how long a stopped node has been down, and so roughly how
// far behind the chain it will be when started.
type StoppedFor struct {
	// Since is when obol network stop ran; nil for nodes stopped by an obol
	// that did not record it.
	Since   *time.Time `json:"since,omitempty"`
	Seconds int64      `json:"seconds,omitempty"`
	// BehindSlots estimates the slots (one block each, at most) the chain
	// moved on since: what the node has to catch up after start.
	BehindSlots uint64 `json:"behind_slots_estimate,omitempty"`
}

// StoppedForAt returns how long d has been stopped at now; nil when it is
// not stopped.
func StoppedForAt(d Deployment, now time.Time) *StoppedFor {
	if !d.Stopped {
		return nil
	}

	if d.StoppedAt.IsZero() {
		return &StoppedFor{}
	}

	since := d.StoppedAt.UTC()
	secs := max(int64(now.Sub(since)/time.Second), 0)

	return &StoppedFor{Since: &since, Seconds: secs, BehindSlots: uint64(secs) / slotSeconds}
}

// Text renders the stop time for humans: "since 2026-11-11 10:04 UTC, 16d
// ago, ~115,200 slots behind" or "time unknown".
func (s *StoppedFor) Text() string {
	if s == nil || s.Since == nil {
		return "time unknown"
	}

	return fmt.Sprintf("since %s, %s ago, ~%s slots behind",
		s.Since.Format("2006-01-02 15:04 UTC"), approxAge(time.Duration(s.Seconds)*time.Second), formatThousands(s.BehindSlots))
}

// stoppedSummary is the verdict summary of a stopped node.
func stoppedSummary(s *StoppedFor) string {
	return "stopped (" + s.Text() + "; data kept)"
}

// approxAge renders a duration as its largest unit: 16d, 5h, 12m.
func approxAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	default:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
}
