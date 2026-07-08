package x402

import "testing"

func TestMatchRoute_ExactMatch(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/health", Price: "0"},
	}

	if r := matchRoute(routes, "/health"); r == nil {
		t.Fatal("expected match for /health")
	}

	if r := matchRoute(routes, "/healthz"); r != nil {
		t.Fatal("expected no match for /healthz")
	}

	if r := matchRoute(routes, "/health/deep"); r != nil {
		t.Fatal("expected no match for /health/deep")
	}
}

func TestMatchRoute_PrefixMatch(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	}

	tests := []struct {
		uri   string
		match bool
	}{
		{"/rpc/mainnet", true},
		{"/rpc/sepolia", true},
		{"/rpc/a/b/c", true}, // deep sub-path
		{"/rpc/", true},      // trailing slash
		{"/rpc", true},       // exact base path (no trailing slash)
		{"/rpcx/foo", false}, // different prefix
		{"/other", false},    // unrelated
	}

	for _, tt := range tests {
		r := matchRoute(routes, tt.uri)
		if tt.match && r == nil {
			t.Errorf("expected match for %q", tt.uri)
		}

		if !tt.match && r != nil {
			t.Errorf("expected no match for %q", tt.uri)
		}
	}
}

func TestMatchRoute_GlobMatch(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/inference-*/v1/*", Price: "0.001"},
	}

	tests := []struct {
		uri   string
		match bool
	}{
		{"/inference-abc/v1/chat/completions", true},
		{"/inference-prod/v1/models", true},
		{"/inference-test-123/v1/embeddings", true},
		{"/inference-abc/v1/a/b/c", true},   // trailing * is greedy
		{"/inference-abc/v2/models", false}, // v2 not v1
		{"/inference/v1/models", false},     // missing segment after inference-
		{"/other-abc/v1/models", false},     // wrong prefix
	}

	for _, tt := range tests {
		r := matchRoute(routes, tt.uri)
		if tt.match && r == nil {
			t.Errorf("expected match for %q", tt.uri)
		}

		if !tt.match && r != nil {
			t.Errorf("expected no match for %q", tt.uri)
		}
	}
}

func TestMatchRoute_FirstMatchWins(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001", Description: "rpc"},
		{Pattern: "/rpc/premium/*", Price: "0.01", Description: "premium"},
	}

	r := matchRoute(routes, "/rpc/premium/mainnet")
	if r == nil {
		t.Fatal("expected match")
	}

	if r.Description != "rpc" {
		t.Errorf("expected first rule (rpc) to win, got %q", r.Description)
	}
}

func TestMatchRoute_NoMatch(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
		{Pattern: "/inference-*/v1/*", Price: "0.001"},
	}

	if r := matchRoute(routes, "/health"); r != nil {
		t.Error("expected no match for /health")
	}

	if r := matchRoute(routes, "/"); r != nil {
		t.Error("expected no match for /")
	}

	if r := matchRoute(routes, ""); r != nil {
		t.Error("expected no match for empty string")
	}
}

func TestMatchRoute_EmptyRoutes(t *testing.T) {
	if r := matchRoute(nil, "/rpc/mainnet"); r != nil {
		t.Error("expected no match with nil routes")
	}

	if r := matchRoute([]RouteRule{}, "/rpc/mainnet"); r != nil {
		t.Error("expected no match with empty routes")
	}
}

func TestMatchRoute_EthereumNetworkPattern(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/ethereum-*/execution/*", Price: "0.0001"},
		{Pattern: "/ethereum-*/beacon/*", Price: "0.0001"},
	}

	tests := []struct {
		uri   string
		match bool
	}{
		{"/ethereum-nervous-otter/execution/eth/v1/beacon/genesis", true},
		{"/ethereum-prod/execution/", true},
		{"/ethereum-prod/beacon/eth/v1/beacon/headers", true},
		{"/ethereum-prod/consensus/", false},
	}

	for _, tt := range tests {
		r := matchRoute(routes, tt.uri)
		if tt.match && r == nil {
			t.Errorf("expected match for %q", tt.uri)
		}

		if !tt.match && r != nil {
			t.Errorf("expected no match for %q", tt.uri)
		}
	}
}

func TestMatchPattern_GlobSegmentBoundary(t *testing.T) {
	// "*" should not match across segment boundaries in non-trailing position
	if matchPattern("/a-*/b", "/a-x/b") != true {
		t.Error("expected /a-x/b to match /a-*/b")
	}

	if matchPattern("/a-*/b", "/a-x/c") != false {
		t.Error("expected /a-x/c NOT to match /a-*/b")
	}
	// Trailing segments without trailing * should not match
	if matchPattern("/a-*/b", "/a-x/b/extra") != false {
		t.Error("expected /a-x/b/extra NOT to match /a-*/b (no trailing wildcard)")
	}
}

func TestStripQueryFragment(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/rpc", "/rpc"},
		{"/rpc?method=eth_call", "/rpc"},
		{"/services/foo/v1?a=1&b=2", "/services/foo/v1"},
		{"/services/foo#frag", "/services/foo"},
		{"/services/foo?x=1#frag", "/services/foo"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := stripQueryFragment(tt.in); got != tt.want {
			t.Errorf("stripQueryFragment(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestSortRoutesBySpecificity pins the overlap-resolution contract for
// ServiceOffer-derived rules: an offer's exact free carve-out must sort
// before its own paid catch-all, and a nested offer's prefix must sort
// before an enclosing offer's prefix — matchRoute is first-match-wins, so
// order IS the semantics.
func TestSortRoutesBySpecificity(t *testing.T) {
	routes := []RouteRule{
		{Pattern: "/services/foo/*", OfferName: "foo"},
		{Pattern: "/services/foo/bar/*", OfferName: "foo-bar"},
		{Pattern: "/services/foo/healthz", OfferName: "foo", Gate: "free"},
		{Pattern: "/services/foo/v1/*", OfferName: "foo"},
	}
	sortRoutesBySpecificity(routes)

	want := []string{
		"/services/foo/healthz", // exact first
		"/services/foo/bar/*",   // longer literal prefix
		"/services/foo/v1/*",
		"/services/foo/*", // enclosing catch-all last
	}
	for i, w := range want {
		if routes[i].Pattern != w {
			t.Fatalf("routes[%d].Pattern = %q, want %q (full order: %+v)", i, routes[i].Pattern, w, patterns(routes))
		}
	}

	// The exact carve-out must actually win the match.
	if r := matchRoute(routes, "/services/foo/healthz"); r == nil || !r.IsFree() {
		t.Fatalf("matchRoute(/services/foo/healthz) = %+v, want the free exact rule", r)
	}
	// Nested offer's traffic must not be captured by the enclosing offer.
	if r := matchRoute(routes, "/services/foo/bar/x"); r == nil || r.OfferName != "foo-bar" {
		t.Fatalf("matchRoute(/services/foo/bar/x) matched %+v, want offer foo-bar", r)
	}
}

func patterns(routes []RouteRule) []string {
	out := make([]string, len(routes))
	for i := range routes {
		out[i] = routes[i].Pattern
	}
	return out
}
