package serviceoffercontroller

import "testing"

// Invariant oracle: every child resource created in a namespace SHARED across
// offers must have a name that is injective over the full identity of the
// offer that owns it. A collision means two live offers fight over one object
// — the exact HTTP-500 failure mode of the original Canary402 ReferenceGrant
// report (offers Ready, requests 500).
//
// The ReferenceGrant is the only child the controller creates in the shared
// "x402" namespace (every other child — Middleware, HTTPRoute,
// RegistrationRequest — lives in the offer's own namespace, where the offer
// name is already unique). So its name must be injective over the pair
// (offer.Namespace, offer.Name).
//
// This is a property test, not an example test: it enumerates a space of
// identity pairs and asserts no two distinct pairs map to the same name. It is
// written to be reused for any future shared-namespace child by adding its
// builder to sharedNamespaceNameBuilders.

// sharedNamespaceNameBuilders lists every name builder for a child resource
// that lives in a namespace shared across offers, keyed by (namespace, name).
var sharedNamespaceNameBuilders = map[string]func(namespace, name string) string{
	"backendReferenceGrant": backendReferenceGrantName,
}

// labelFragments are pieces that appear in real Kubernetes namespaces and
// object names. Both namespaces and object names are DNS subdomains that may
// contain internal dashes, so any pair-join that leans on a dash separator is
// where injectivity breaks. These fragments are deliberately chosen so several
// distinct (namespace, name) pairs share the same dash-joined string.
var labelFragments = []string{"foo", "bar", "baz", "foo-bar", "bar-baz", "a", "a-b", "b", "team-a", "team", "a-team"}

func TestSharedNamespaceNames_Injective(t *testing.T) {
	for builderName, build := range sharedNamespaceNameBuilders {
		t.Run(builderName, func(t *testing.T) {
			seen := map[string][2]string{} // generated name -> (namespace, name) that first produced it
			for _, ns := range labelFragments {
				for _, name := range labelFragments {
					got := build(ns, name)
					if prev, clash := seen[got]; clash && prev != [2]string{ns, name} {
						t.Errorf("collision: (ns=%q, name=%q) and (ns=%q, name=%q) both map to %q",
							prev[0], prev[1], ns, name, got)
					}
					seen[got] = [2]string{ns, name}
				}
			}
		})
	}
}

// TestBackendReferenceGrantName_KnownCollision pins the exact adversarial pair
// from the injectivity property so a regression is unambiguous, not just a
// probabilistic property failure.
func TestBackendReferenceGrantName_KnownCollision(t *testing.T) {
	a := backendReferenceGrantName("foo-bar", "baz")
	b := backendReferenceGrantName("foo", "bar-baz")
	if a == b {
		t.Fatalf("(ns=foo-bar, name=baz) and (ns=foo, name=bar-baz) collide on grant name %q — "+
			"two live offers in different namespaces share one ReferenceGrant in x402 (HTTP 500)", a)
	}
}

// TestBackendReferenceGrantName_StableForSameOffer guards the other direction:
// the disambiguation must be deterministic, so the same offer always resolves
// to the same grant (server-side apply is by name — a nondeterministic name
// would orphan the previous grant on every reconcile).
func TestBackendReferenceGrantName_StableForSameOffer(t *testing.T) {
	for i := 0; i < 4; i++ {
		if got := backendReferenceGrantName("prod", "hyperliquid-analyst"); got != backendReferenceGrantName("prod", "hyperliquid-analyst") {
			t.Fatalf("grant name not stable across calls: %q", got)
		}
	}
	// And distinct offers that happen to dash-collide on the readable prefix
	// still resolve to distinct, stable names.
	seen := map[string]bool{}
	for _, p := range [][2]string{{"foo-bar", "baz"}, {"foo", "bar-baz"}, {"foo-bar-baz", ""}} {
		if p[1] == "" {
			continue // empty name is not a real offer; skip
		}
		n := backendReferenceGrantName(p[0], p[1])
		if seen[n] {
			t.Fatalf("stable-but-colliding name %q for %v", n, p)
		}
		seen[n] = true
	}
}
