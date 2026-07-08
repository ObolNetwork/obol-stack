package x402

import (
	"sync"
	"time"
)

// freeQuotaCounter tracks per-wallet free-tier usage on paid routes with a
// freeQuota. Counters live in memory keyed by (UTC day, route, wallet) —
// deliberately NOT durable: this is a giveaway mechanism (x402scan-style
// free tier), not an entitlement ledger, and a verifier restart handing out
// a fresh day's quota is an acceptable failure mode. Durable per-wallet
// entitlements belong to the broker's store when card/subscription gates
// arrive.
type freeQuotaCounter struct {
	mu     sync.Mutex
	day    string
	counts map[string]int64 // "<pattern>\x00<wallet>" → used today
}

func newFreeQuotaCounter() *freeQuotaCounter {
	return &freeQuotaCounter{counts: map[string]int64{}}
}

// consume records one free call if the wallet is under limit for the
// route's UTC day, and reports whether the call rides free.
func (q *freeQuotaCounter) consume(pattern, wallet string, limit int64, now time.Time) bool {
	if limit <= 0 || wallet == "" {
		return false
	}
	day := now.UTC().Format("2006-01-02")
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.day != day {
		q.day = day
		q.counts = map[string]int64{}
	}
	key := pattern + "\x00" + wallet
	if q.counts[key] >= limit {
		return false
	}
	q.counts[key]++
	return true
}
