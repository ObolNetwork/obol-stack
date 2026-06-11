package serviceoffercontroller

import (
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func offerAt(ns, name, path string, created time.Time) *monetizeapi.ServiceOffer {
	o := &monetizeapi.ServiceOffer{}
	o.Namespace = ns
	o.Name = name
	o.Spec.Path = path
	o.CreationTimestamp = metav1.NewTime(created)
	return o
}

func TestPickPathConflict(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	older := offerAt("agent-a", "alpha", "/services/shared", t0)
	newer := offerAt("agent-b", "beta", "/services/shared", t1)

	// The newer claimant loses to the older one...
	if got := pickPathConflict(newer, []*monetizeapi.ServiceOffer{older, newer}); got != "agent-a/alpha" {
		t.Errorf("newer offer should conflict with older claimant, got %q", got)
	}
	// ...and the older claimant keeps its path.
	if got := pickPathConflict(older, []*monetizeapi.ServiceOffer{older, newer}); got != "" {
		t.Errorf("older offer must keep the path, got conflict %q", got)
	}

	// Trailing-slash variants are the same path.
	slashed := offerAt("agent-c", "gamma", "/services/shared/", t1.Add(time.Hour))
	if got := pickPathConflict(slashed, []*monetizeapi.ServiceOffer{older}); got != "agent-a/alpha" {
		t.Errorf("trailing-slash path must collide, got %q", got)
	}

	// Default paths (spec.path empty → /services/<name>) collide with
	// explicit ones.
	defaulted := offerAt("agent-d", "shared", "", t1)
	if got := pickPathConflict(defaulted, []*monetizeapi.ServiceOffer{older}); got != "agent-a/alpha" {
		t.Errorf("defaulted path must collide with explicit /services/shared, got %q", got)
	}

	// Deleting offers free their path immediately.
	deleting := offerAt("agent-a", "alpha", "/services/shared", t0)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	if got := pickPathConflict(newer, []*monetizeapi.ServiceOffer{deleting}); got != "" {
		t.Errorf("deleting offer must not hold the path, got %q", got)
	}

	// Different paths never conflict; self never conflicts.
	other := offerAt("agent-e", "eps", "/services/other", t0)
	if got := pickPathConflict(newer, []*monetizeapi.ServiceOffer{other, newer}); got != "" {
		t.Errorf("unrelated/self offers must not conflict, got %q", got)
	}

	// Equal timestamps: namespace/name ordering breaks the tie the same
	// way on both sides (exactly one of the pair wins).
	twinA := offerAt("agent-a", "twin", "/services/twin", t0)
	twinB := offerAt("agent-b", "twin", "/services/twin", t0)
	aConf := pickPathConflict(twinA, []*monetizeapi.ServiceOffer{twinB})
	bConf := pickPathConflict(twinB, []*monetizeapi.ServiceOffer{twinA})
	if (aConf == "") == (bConf == "") {
		t.Errorf("tie-break must pick exactly one winner: aConf=%q bConf=%q", aConf, bConf)
	}
}
