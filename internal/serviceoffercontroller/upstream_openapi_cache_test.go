package serviceoffercontroller

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func probeableOffer(gen int64) *monetizeapi.ServiceOffer {
	return &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{UID: "uid-probeable", Generation: gen},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:     "http",
			Hostname: "svc.example.org",
			Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc", Port: 8080},
		},
	}
}

func goodDoc() map[string]any {
	return map[string]any{"paths": map[string]any{"/v1/thing": map[string]any{}}}
}

// TestUpstreamOpenAPICache_FailedProbeIsNotPinned is the regression test for
// the sticky-nil cache.
//
// refresh keys on offer.Generation and short-circuits once an entry exists for
// that generation. Caching a FAILED probe therefore pinned the offer to the
// route-table fallback until someone edited the CR — and because
// reconcileStaticSite rebuilds the shared bundle from this cache on every
// offer's reconcile, one miss could overwrite a good document for the whole
// stack. A miss must leave the generation unrecorded so the next reconcile
// retries.
func TestUpstreamOpenAPICache_FailedProbeIsNotPinned(t *testing.T) {
	c := &upstreamOpenAPICache{}
	offer := probeableOffer(1)

	calls := 0
	failing := func(*monetizeapi.ServiceOffer) map[string]any { calls++; return nil }

	c.refresh(offer, failing)
	if got := c.get(offer); got != nil {
		t.Errorf("after a failed probe, get = %v, want nil", got)
	}
	if calls != 1 {
		t.Fatalf("fetch called %d times, want 1", calls)
	}

	// Same generation, no spec change: the miss must NOT have been pinned, so
	// this reconcile retries rather than short-circuiting.
	c.refresh(offer, failing)
	if calls != 2 {
		t.Errorf("fetch called %d times after second refresh, want 2 — the failed probe was pinned", calls)
	}

	// Upstream comes up; the retry now succeeds and is cached.
	c.refresh(offer, func(*monetizeapi.ServiceOffer) map[string]any { return goodDoc() })
	if got := c.get(offer); got == nil {
		t.Fatal("after a successful probe, get = nil, want the document")
	}

	// And a LATER failure must not evict the last-good document.
	c.refresh(probeableOffer(2), failing)
	if got := c.get(offer); got == nil {
		t.Error("a later failed probe evicted the last-good document; stale beats collapsed")
	}
}

// TestUpstreamOpenAPICache_SuccessIsCachedPerGeneration keeps the original
// contract intact: a good result is fetched once per generation, not per
// reconcile.
func TestUpstreamOpenAPICache_SuccessIsCachedPerGeneration(t *testing.T) {
	c := &upstreamOpenAPICache{}
	offer := probeableOffer(1)

	calls := 0
	ok := func(*monetizeapi.ServiceOffer) map[string]any { calls++; return goodDoc() }

	c.refresh(offer, ok)
	c.refresh(offer, ok)
	c.refresh(offer, ok)
	if calls != 1 {
		t.Errorf("fetch called %d times for one generation, want 1", calls)
	}

	c.refresh(probeableOffer(2), ok)
	if calls != 2 {
		t.Errorf("fetch called %d times after a generation bump, want 2", calls)
	}
}

// TestUpstreamOpenAPICache_TerminalNilIsCached guards the other half: offers
// that can NEVER serve an upstream document (agent, inference, or no upstream
// Service) must still cache their nil, or they would be probed on every single
// reconcile forever.
func TestUpstreamOpenAPICache_TerminalNilIsCached(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec monetizeapi.ServiceOfferSpec
	}{
		{"agent", monetizeapi.ServiceOfferSpec{Type: "agent", Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"}}},
		{"inference", monetizeapi.ServiceOfferSpec{Type: "inference", Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"}}},
		{"no upstream service", monetizeapi.ServiceOfferSpec{Type: "http"}},
	} {
		c := &upstreamOpenAPICache{}
		offer := &monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{UID: "uid-terminal", Generation: 1},
			Spec:       tc.spec,
		}

		calls := 0
		fetch := func(o *monetizeapi.ServiceOffer) map[string]any { calls++; return fetchUpstreamOpenAPI(o) }

		c.refresh(offer, fetch)
		c.refresh(offer, fetch)
		if calls != 1 {
			t.Errorf("%s: fetch called %d times, want 1 — a terminal nil must be cached", tc.name, calls)
		}
	}
}

// TestOfferHasProbeableUpstream pins the terminal/transient split itself, since
// both fetchUpstreamOpenAPI and refresh depend on it agreeing.
func TestOfferHasProbeableUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec monetizeapi.ServiceOfferSpec
		want bool
	}{
		{"http with upstream", monetizeapi.ServiceOfferSpec{Type: "http", Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"}}, true},
		{"http without upstream", monetizeapi.ServiceOfferSpec{Type: "http"}, false},
		{"agent", monetizeapi.ServiceOfferSpec{Type: "agent", Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"}}, false},
		{"inference", monetizeapi.ServiceOfferSpec{Type: "inference", Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"}}, false},
	} {
		got := offerHasProbeableUpstream(&monetizeapi.ServiceOffer{Spec: tc.spec})
		if got != tc.want {
			t.Errorf("%s: offerHasProbeableUpstream = %v, want %v", tc.name, got, tc.want)
		}
	}
	if offerHasProbeableUpstream(nil) {
		t.Error("nil offer must not be probeable")
	}
}
