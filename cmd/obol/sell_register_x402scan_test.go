package main

import (
	"strings"
	"testing"
)

func TestResolveX402scanOrigin_Explicit(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		errPart string
	}{
		{in: "https://store.example.com", want: "https://store.example.com"},
		{in: "https://store.example.com/some/path", want: "https://store.example.com"},
		{in: "http://store.example.com", errPart: "must be https"},
		{in: "https://obol.stack:8080", errPart: "no public hostname"},
		{in: "https://localhost:8080", errPart: "no public hostname"},
		{in: "https://abc-def.trycloudflare.com", errPart: "quick-tunnel"},
		{in: "not a url", errPart: "invalid origin"},
	} {
		got, err := resolveX402scanOrigin(nil, tc.in)
		if tc.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("%q: expected error containing %q, got %v", tc.in, tc.errPart, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestServicesOfferName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/services/foo/v1/chat/completions", "foo"},
		{"/services/bar", "bar"},
		{"/services/baz/", "baz"},
		{"/openapi.json", ""}, // per-offer subdomain root — no /services/ prefix
		{"/audits/{id}", ""},  // app path on a dedicated origin
		{"/", ""},
		{"/servicesfoo", ""}, // not the /services/ prefix
	} {
		if got := servicesOfferName(tc.in); got != tc.want {
			t.Errorf("servicesOfferName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// The leak signal: two distinct /services/<name> prefixes = a shared
	// origin serving multiple offers (what the preflight warns about).
	paths := []string{"/services/foo/v1", "/services/foo/v1/models", "/services/bar", "/openapi.json"}
	offers := map[string]struct{}{}
	for _, p := range paths {
		if n := servicesOfferName(p); n != "" {
			offers[n] = struct{}{}
		}
	}
	if len(offers) != 2 {
		t.Fatalf("distinct offers = %d, want 2 (foo, bar)", len(offers))
	}
}
