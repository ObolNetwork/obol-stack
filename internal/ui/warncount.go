package ui

import (
	"sync"
	"sync/atomic"
)

// warnCounts tracks how many warnings each UI has printed. Kept outside the
// UI struct (keyed by pointer) so the counter is a pure add-on: callers that
// want "did this step warn?" semantics (internal/replay) diff WarnCount()
// around a step without every best-effort helper having to return an error.
var warnCounts sync.Map // *UI -> *atomic.Int64

func (u *UI) countWarning() {
	v, _ := warnCounts.LoadOrStore(u, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

// WarnCount returns the number of warnings printed through u so far.
func (u *UI) WarnCount() int64 {
	if v, ok := warnCounts.Load(u); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}
