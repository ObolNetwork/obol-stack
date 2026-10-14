package buy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"base URL", "https://inference.v1337.org/", "https://inference.v1337.org/api/services.json"},
		{"service URL stripped", "https://inference.v1337.org/services/aeon", "https://inference.v1337.org/api/services.json"},
		{"query dropped", "https://x.example/services/foo?x=1", "https://x.example/api/services.json"},
		{"no trailing slash", "https://x.example", "https://x.example/api/services.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := catalogURL(tc.in)
			if err != nil {
				t.Fatalf("catalogURL(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("catalogURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCatalogURL_Invalid(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "not-a-url", "/relative/only"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if _, err := catalogURL(in); err == nil {
				t.Fatalf("catalogURL(%q) returned nil err, want error", in)
			}
		})
	}
}

func TestEndpointPath(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/services/aeon":                          "/services/aeon",
		"/services/aeon/v1/chat/completions":      "/services/aeon",
		"/services/aeon/chat/completions":         "/services/aeon",
		"https://x.example/services/aeon":         "/services/aeon",
		"https://x.example/services/aeon/v1/chat": "/services/aeon", // trailing chat is not stripped fully but path still picks aeon
		"":                "",
		"/":               "",
		"/something-else": "",
	}
	for in, want := range cases {
		got := endpointPath(in)
		if got != want {
			t.Errorf("endpointPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServicePathFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"storefront base", "https://inference.v1337.org/", ""},
		{"service URL", "https://inference.v1337.org/services/aeon", "/services/aeon"},
		{"service URL with subpath", "https://x.example/services/foo/v1/chat/completions", "/services/foo"},
		{"trailing slash", "https://x.example/services/foo/", "/services/foo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := servicePathFromURL(tc.in)
			if err != nil {
				t.Fatalf("servicePathFromURL(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("servicePathFromURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// FetchServiceCatalog hits /api/services.json and parses the entries.
func TestFetchServiceCatalog(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"displayName": "Seller",
			"tagline":     "Paid APIs",
			"logoUrl":     "https://example/logo.png",
			"services": []CatalogEntry{
				{Name: "aeon", Type: "inference", Model: "aeon7", Endpoint: "/services/aeon/v1/chat/completions"},
				{Name: "http-thing", Type: "http", Endpoint: "/services/http-thing"},
			},
		})
	}))
	defer srv.Close()

	got, err := FetchServiceCatalog(context.Background(), srv.URL+"/services/aeon")
	if err != nil {
		t.Fatalf("FetchServiceCatalog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Name != "aeon" {
		t.Fatalf("entry[0].Name = %q, want %q", got[0].Name, "aeon")
	}
}

// PickCatalogEntry: storefront base + single inference offer picks it.
// Multiple offers without a service path errors. Wrong type errors.
func TestPickCatalogEntry(t *testing.T) {
	t.Parallel()
	entries := []CatalogEntry{
		{Name: "aeon", Type: "inference", Model: "aeon7", Endpoint: "/services/aeon"},
		{Name: "http-thing", Type: "http", Endpoint: "/services/http-thing"},
	}
	multipleInf := []CatalogEntry{
		{Name: "aeon", Type: "inference", Endpoint: "/services/aeon"},
		{Name: "bravo", Type: "inference", Endpoint: "/services/bravo"},
	}

	tests := []struct {
		name    string
		entries []CatalogEntry
		seller  string
		want    string
		wantErr string
	}{
		{"storefront base, single inference", entries, "https://x.example/", "aeon", ""},
		{"explicit service URL matches", entries, "https://x.example/services/aeon", "aeon", ""},
		{"explicit service URL rejects http type", entries, "https://x.example/services/http-thing", "", "type=\"http\""},
		{"unknown service URL", entries, "https://x.example/services/ghost", "", "no entry for endpoint"},
		{"multiple inference offers, no service URL", multipleInf, "https://x.example/", "", "multiple inference offers"},
		{"no inference offers", []CatalogEntry{{Name: "x", Type: "http", Endpoint: "/services/x"}}, "https://x.example/", "", "no inference offers"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := PickCatalogEntry(tc.entries, tc.seller)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("PickCatalogEntry err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("PickCatalogEntry unexpected err: %v", err)
			}
			if got == nil || got.Name != tc.want {
				t.Fatalf("PickCatalogEntry name = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveCatalogEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		seller   string
		endpoint string
		want     string
		wantErr  bool
	}{
		{"relative path", "https://seller.example/", "/services/foo", "https://seller.example/services/foo", false},
		{"relative without slash", "https://seller.example", "services/foo", "https://seller.example/services/foo", false},
		{"relative ignores seller path", "https://seller.example/shop/", "/services/foo", "https://seller.example/services/foo", false},
		{"relative keeps seller port", "http://localhost:8080", "/services/foo", "http://localhost:8080/services/foo", false},
		{"absolute same origin", "https://seller.example", "https://seller.example/services/foo", "https://seller.example/services/foo", false},
		{"absolute same origin explicit default port", "https://seller.example", "https://seller.example:443/services/foo", "https://seller.example:443/services/foo", false},
		{"absolute host case-insensitive", "https://Seller.Example", "https://seller.example/services/foo", "https://seller.example/services/foo", false},
		{"http seller allows http endpoint", "http://obol.stack:8080", "http://obol.stack:8080/services/foo", "http://obol.stack:8080/services/foo", false},
		{"http seller allows https upgrade", "http://seller.example", "https://seller.example/services/foo", "https://seller.example/services/foo", false},
		{"hostname-bound offer on another public https host", "https://seller.example", "https://audit.acme.io", "https://audit.acme.io", false},
		{"public https subdomain", "https://seller.example", "https://api.seller.example/services/foo", "https://api.seller.example/services/foo", false},
		{"public https other port", "https://seller.example", "https://seller.example:8443/services/foo", "https://seller.example:8443/services/foo", false},
		{"local buyer, tunnel-host https endpoint", "http://obol.stack:8080", "https://shop.example.com/services/foo", "https://shop.example.com/services/foo", false},
		{"http other host", "https://seller.example", "http://other.example/services/foo", "", true},
		{"http other port same host", "http://seller.example", "http://seller.example:8080/services/foo", "", true},
		{"ipv4 literal", "https://seller.example", "https://10.0.0.5/services/foo", "", true},
		{"public ipv4 literal", "https://seller.example", "https://203.0.113.7/services/foo", "", true},
		{"ipv6 literal", "https://seller.example", "https://[::1]/services/foo", "", true},
		{"ipv6 public literal", "https://seller.example", "https://[2001:db8::1]:443/services/foo", "", true},
		{"localhost", "https://seller.example", "https://localhost/services/foo", "", true},
		{"dot localhost", "https://seller.example", "https://api.localhost/services/foo", "", true},
		{"mdns local", "https://seller.example", "https://printer.local/services/foo", "", true},
		{"internal", "https://seller.example", "https://host.k3d.internal/services/foo", "", true},
		{"cluster service", "https://seller.example", "https://litellm.llm.svc.cluster.local:4000/", "", true},
		{"short svc", "https://seller.example", "https://litellm.llm.svc/", "", true},
		{"obol.stack", "https://seller.example", "https://obol.stack/services/foo", "", true},
		{"single label", "https://seller.example", "https://intranet/services/foo", "", true},
		{"https seller refuses http endpoint", "https://seller.example", "http://seller.example/services/foo", "", true},
		{"scheme-relative other host", "http://seller.example", "//other.example/services/foo", "", true},
		{"non-http scheme", "https://seller.example", "ftp://seller.example/services/foo", "", true},
		{"empty endpoint", "https://seller.example", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveCatalogEndpoint(tc.seller, tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveCatalogEndpoint(%q, %q) = %q, want error", tc.seller, tc.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveCatalogEndpoint(%q, %q) err = %v", tc.seller, tc.endpoint, err)
			}
			if got != tc.want {
				t.Fatalf("ResolveCatalogEndpoint(%q, %q) = %q, want %q", tc.seller, tc.endpoint, got, tc.want)
			}
		})
	}
}

func TestPinCatalogToSeller(t *testing.T) {
	t.Parallel()
	entries := []CatalogEntry{
		{Name: "local", Type: "inference", Endpoint: "/services/local"},
		{Name: "same", Type: "inference", Endpoint: "https://seller.example/services/same"},
		{Name: "elsewhere", Type: "inference", Endpoint: "http://10.0.0.5/services/elsewhere"},
	}
	pinned, skipped := PinCatalogToSeller(entries, "https://seller.example/")
	if len(pinned) != 2 || pinned[0].Endpoint != "https://seller.example/services/local" || pinned[1].Name != "same" {
		t.Fatalf("pinned = %+v", pinned)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], `"elsewhere"`) {
		t.Fatalf("skipped = %v", skipped)
	}
	if entries[0].Endpoint != "/services/local" {
		t.Fatal("input entries must not be mutated")
	}

	// A cross-origin entry selected by its service path is not picked.
	if _, err := PickCatalogEntry(pinned, "https://seller.example/services/elsewhere"); err == nil {
		t.Fatal("expected no match for a skipped cross-origin entry")
	}
	// Only-entry case: nothing left to pick.
	only, skippedOnly := PinCatalogToSeller(entries[2:], "https://seller.example/")
	if len(only) != 0 || len(skippedOnly) != 1 {
		t.Fatalf("pinned=%v skipped=%v", only, skippedOnly)
	}
	if _, err := PickCatalogEntry(only, "https://seller.example/"); err == nil {
		t.Fatal("expected an error when every entry was skipped")
	}
}
