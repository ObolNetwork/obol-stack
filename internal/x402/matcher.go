package x402

import (
	"path"
	"sort"
	"strings"
)

// matchRoute finds the first RouteRule whose pattern matches the given URI path.
// Returns nil if no rule matches (the route is free / unmetered).
//
// Pattern types (evaluated per rule, first match wins):
//
//   - Exact:  "/health" matches only "/health"
//   - Prefix: "/rpc/*" matches any path starting with "/rpc/"
//   - Glob:   "/inference-*/v1/*" uses path.Match for segment-level wildcards
//
// The "*" at the end of a prefix pattern is greedy — it matches any depth
// of sub-path (e.g., "/rpc/*" matches "/rpc/a/b/c").
func matchRoute(routes []RouteRule, uri string) *RouteRule {
	for i := range routes {
		if matchPattern(routes[i].Pattern, uri) {
			return &routes[i]
		}
	}

	return nil
}

// matchPattern tests whether uri matches the given pattern.
func matchPattern(pattern, uri string) bool {
	// Exact match — no wildcards at all.
	if !strings.Contains(pattern, "*") {
		return pattern == uri
	}

	// Simple prefix match: pattern ends with "/*" and has no other wildcards.
	// "/rpc/*" matches "/rpc", "/rpc/", "/rpc/anything", and "/rpc/a/b/c".
	if before, ok := strings.CutSuffix(pattern, "/*"); ok {
		prefix := before
		if !strings.Contains(prefix, "*") {
			return uri == prefix || strings.HasPrefix(uri, prefix+"/")
		}
	}

	// Glob match with wildcards: "/inference-*/v1/*".
	// path.Match handles single-segment wildcards, trailing "*" is greedy.
	return globMatch(pattern, uri)
}

// stripQueryFragment reduces a forwarded URI to its path component.
// Traefik's X-Forwarded-Uri includes the query string, so "/rpc?method=x"
// would silently miss the "/rpc" rule (a free pass in ForwardAuth mode)
// without this. Fragments never reach the server in practice but are
// stripped defensively for the same reason.
func stripQueryFragment(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		return uri[:i]
	}
	return uri
}

// sortRoutesBySpecificity orders rules most-specific-first so matchRoute's
// first-match-wins semantics resolve overlaps correctly: an exact
// "/services/foo/healthz" must beat its own offer's "/services/foo/*"
// catch-all, and a nested offer at "/services/foo/bar/*" must beat
// "/services/foo/*". Specificity: exact patterns first, then wildcard
// patterns by longest literal prefix, then by segment count; ties break on
// (pattern, offer ns/name) for determinism. Static pricing.yaml configs are
// NOT sorted — their documented contract is first-match in file order.
func sortRoutesBySpecificity(routes []RouteRule) {
	sort.SliceStable(routes, func(i, j int) bool {
		ei, li := patternSpecificity(routes[i].Pattern)
		ej, lj := patternSpecificity(routes[j].Pattern)
		if ei != ej {
			return ei // exact before wildcard
		}
		if li != lj {
			return li > lj // longer literal prefix first
		}
		si := strings.Count(routes[i].Pattern, "/")
		sj := strings.Count(routes[j].Pattern, "/")
		if si != sj {
			return si > sj // deeper pattern first
		}
		if routes[i].Pattern != routes[j].Pattern {
			return routes[i].Pattern < routes[j].Pattern
		}
		if routes[i].OfferNamespace != routes[j].OfferNamespace {
			return routes[i].OfferNamespace < routes[j].OfferNamespace
		}
		return routes[i].OfferName < routes[j].OfferName
	})
}

// patternSpecificity returns whether the pattern is an exact match (no
// wildcards) and the length of its literal prefix before the first "*".
func patternSpecificity(pattern string) (exact bool, literalLen int) {
	i := strings.IndexByte(pattern, '*')
	if i < 0 {
		return true, len(pattern)
	}
	return false, i
}

// globMatch matches a pattern containing "*" wildcards against a URI path.
// Each "*" in a non-trailing position matches a single path segment.
// A trailing "/*" matches any remaining segments.
func globMatch(pattern, uri string) bool {
	patParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	uriParts := strings.Split(strings.TrimPrefix(uri, "/"), "/")

	if len(uriParts) < len(patParts) {
		return false
	}

	for i, pp := range patParts {
		// Last pattern segment is "*" — matches everything remaining.
		if i == len(patParts)-1 && pp == "*" {
			return true
		}

		if i >= len(uriParts) {
			return false
		}

		matched, err := path.Match(pp, uriParts[i])
		if err != nil || !matched {
			return false
		}
	}

	// Pattern consumed — URI must be exactly the same length (no trailing segments).
	return len(uriParts) == len(patParts)
}
