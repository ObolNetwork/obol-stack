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
