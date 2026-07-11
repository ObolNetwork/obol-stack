package main

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

func TestClearProfileFields(t *testing.T) {
	base := schemas.StorefrontProfile{
		DisplayName:  "Acme",
		Tagline:      "Paid APIs.",
		LogoURL:      "https://acme/logo.png",
		ContactEmail: "ops@acme.example",
		Theme:        "dark",
		AccentColor:  "#a1b2c3",
		FaviconURL:   "https://acme/fav.png",
		OGImageURL:   "https://acme/og.png",
		Description:  "We audit things.",
	}
	all := map[string]bool{}
	for _, f := range []string{"display-name", "tagline", "logo-url", "contact-email",
		"theme", "accent", "favicon-url", "og-image-url", "description"} {
		all[f] = true
	}

	tests := []struct {
		name  string
		clear map[string]bool
		want  schemas.StorefrontProfile
	}{
		{"none", nil, base},
		{"tagline only", map[string]bool{"tagline": true}, func() schemas.StorefrontProfile { p := base; p.Tagline = ""; return p }()},
		{"display+logo", map[string]bool{"display-name": true, "logo-url": true}, func() schemas.StorefrontProfile { p := base; p.DisplayName, p.LogoURL = "", ""; return p }()},
		{"contact only", map[string]bool{"contact-email": true}, func() schemas.StorefrontProfile { p := base; p.ContactEmail = ""; return p }()},
		{"theme+accent", map[string]bool{"theme": true, "accent": true}, func() schemas.StorefrontProfile { p := base; p.Theme, p.AccentColor = "", ""; return p }()},
		{"favicon+og+description", map[string]bool{"favicon-url": true, "og-image-url": true, "description": true}, func() schemas.StorefrontProfile {
			p := base
			p.FaviconURL, p.OGImageURL, p.Description = "", "", ""
			return p
		}()},
		{"all", all, schemas.StorefrontProfile{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clearProfileFields(base, tc.clear)
			if got != tc.want {
				t.Fatalf("clearProfileFields = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFindCatalogEntry(t *testing.T) {
	catalog := schemas.ServiceCatalog{Services: []schemas.ServiceCatalogEntry{
		{Name: "alpha", Type: "http"},
		{Name: "beta", Type: "inference"},
	}}

	if e := findCatalogEntry(catalog, "beta"); e == nil || e.Type != "inference" {
		t.Fatalf("expected to find beta/inference, got %+v", e)
	}
	if e := findCatalogEntry(catalog, "  alpha  "); e == nil || e.Name != "alpha" {
		t.Fatalf("expected trimmed lookup to find alpha, got %+v", e)
	}
	if e := findCatalogEntry(catalog, "missing"); e != nil {
		t.Fatalf("expected nil for missing service, got %+v", e)
	}
}

func TestServiceHealth(t *testing.T) {
	tests := []struct {
		name  string
		entry schemas.ServiceCatalogEntry
		want  string
	}{
		{"ready", schemas.ServiceCatalogEntry{}, "ready"},
		{"registration pending", schemas.ServiceCatalogEntry{RegistrationPending: true}, "ready (registration pending)"},
		{"draining wins", schemas.ServiceCatalogEntry{RegistrationPending: true, DrainEndsAt: "2026-07-01T00:00:00Z"}, "draining (until 2026-07-01T00:00:00Z)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceHealth(tc.entry); got != tc.want {
				t.Fatalf("serviceHealth = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEndpointBase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://acme.example/services/foo/v1/chat", "https://acme.example"},
		{"https://acme.example/services/foo", "https://acme.example"},
		{"https://acme.example/", "https://acme.example"},
		{"https://acme.example", "https://acme.example"},
	}
	for _, tc := range tests {
		if got := endpointBase(tc.in); got != tc.want {
			t.Fatalf("endpointBase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHowToBuy(t *testing.T) {
	tests := []struct {
		name     string
		entry    schemas.ServiceCatalogEntry
		contains string
	}{
		{"inference uses obol buy inference with origin", schemas.ServiceCatalogEntry{Type: "inference", Endpoint: "https://acme.example/services/x/v1"}, "obol buy inference https://acme.example"},
		{"agent uses pay-agent with model", schemas.ServiceCatalogEntry{Type: "agent", Endpoint: "https://acme.example/services/x", Model: "qwen"}, "pay-agent https://acme.example/services/x --model qwen"},
		{"http uses pay", schemas.ServiceCatalogEntry{Type: "http", Endpoint: "https://acme.example/services/x"}, "buy.py pay https://acme.example/services/x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := howToBuy(tc.entry)
			if len(lines) == 0 {
				t.Fatal("expected at least one how-to-buy line")
			}
			found := false
			for _, l := range lines {
				if strings.Contains(l, tc.contains) {
					found = true
				}
			}
			if !found {
				t.Fatalf("howToBuy = %v, want a line %q", lines, tc.contains)
			}
		})
	}
}
