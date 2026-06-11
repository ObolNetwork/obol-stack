package serviceoffercontroller

import (
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Path collisions between ServiceOffers were previously silent: the x402
// verifier's route table is first-match-wins, so a second offer claiming an
// already-taken path shadowed (or was shadowed by) the first with no signal
// to either seller. First-claimant-wins is now explicit: the OLDER offer
// (creationTimestamp, ties broken by namespace/name) keeps the path; newer
// claimants get RoutePublished=False/PathConflict and no route children.
// The CLI runs the same check as a preflight so `obol sell ...` fails fast
// before anything is created (a ValidatingAdmissionPolicy cannot do this —
// VAPs cannot list other cluster objects).

// findPathConflict returns "namespace/name" of the offer that out-claims
// this offer's public path, or "" when the path is free (or this offer is
// the first claimant).
func (c *Controller) findPathConflict(offer *monetizeapi.ServiceOffer) string {
	var others []*monetizeapi.ServiceOffer
	for _, item := range c.offerInformer.GetStore().List() {
		raw, ok := item.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		other, err := decodeServiceOffer(raw)
		if err != nil {
			continue
		}
		others = append(others, other)
	}
	return pickPathConflict(offer, others)
}

// pickPathConflict is the pure core of findPathConflict: it returns the
// "namespace/name" of an offer in others that precedes offer on the same
// effective path, or "".
func pickPathConflict(offer *monetizeapi.ServiceOffer, others []*monetizeapi.ServiceOffer) string {
	path := normalizeOfferPath(offer.EffectivePath())
	for _, other := range others {
		if other == nil {
			continue
		}
		if other.Namespace == offer.Namespace && other.Name == offer.Name {
			continue
		}
		if other.DeletionTimestamp != nil {
			continue
		}
		if normalizeOfferPath(other.EffectivePath()) != path {
			continue
		}
		if claimPrecedes(other, offer) {
			return other.Namespace + "/" + other.Name
		}
	}
	return ""
}

func normalizeOfferPath(p string) string {
	return strings.TrimSuffix(p, "/")
}

// claimPrecedes reports whether a's claim on a path beats b's: older
// creationTimestamp wins; equal timestamps fall back to namespace/name
// ordering so the outcome is deterministic on both sides.
func claimPrecedes(a, b *monetizeapi.ServiceOffer) bool {
	at, bt := a.CreationTimestamp.Time, b.CreationTimestamp.Time
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
}
