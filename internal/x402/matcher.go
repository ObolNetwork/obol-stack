package x402

import (
	"path"
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
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if !strings.Contains(prefix, "*") {
			return uri == prefix || strings.HasPrefix(uri, prefix+"/")
		}
	}

	// Glob match with wildcards: "/inference-*/v1/*".
	// path.Match handles single-segment wildcards, trailing "*" is greedy.
	return globMatch(pattern, uri)
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
