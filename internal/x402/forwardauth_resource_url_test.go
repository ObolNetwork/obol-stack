package x402

import (
	"net/http/httptest"
	"testing"
)

func TestBuildResourceURL_PublicHostDefaultsHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/services/demo/v1", nil)
	r.Host = "store.example.com"
	// Tunnel edge often forwards X-Forwarded-Proto: http after TLS termination.
	r.Header.Set("X-Forwarded-Proto", "http")
	got := buildResourceURL(r)
	want := "https://store.example.com/services/demo/v1"
	if got != want {
		t.Fatalf("buildResourceURL = %q, want %q", got, want)
	}
}

func TestBuildResourceURL_HostnameOfferStripsInternalPrefix(t *testing.T) {
	rule := &RouteRule{
		Hostname:    "audit.example.com",
		StripPrefix: "/services/canary402",
		OfferName:   "canary402",
	}
	// Traefik rewrote /audit → /services/canary402/audit before the verifier.
	r := httptest.NewRequest("GET", "/services/canary402/audit", nil)
	r.Host = "audit.example.com"
	r.Header.Set("X-Forwarded-Host", "audit.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r = r.WithContext(withRouteRule(r.Context(), rule))

	got := buildResourceURL(r)
	want := "https://audit.example.com/audit"
	if got != want {
		t.Fatalf("buildResourceURL = %q, want public path %q", got, want)
	}
}

func TestBuildResourceURL_LocalHostStaysHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "/services/x", nil)
	r.Host = "obol.stack:8080"
	got := buildResourceURL(r)
	want := "http://obol.stack:8080/services/x"
	if got != want {
		t.Fatalf("buildResourceURL = %q, want %q", got, want)
	}
}
