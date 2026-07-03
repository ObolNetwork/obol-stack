package storefront_test

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

func TestResolvePublished_ExplicitOverridesDefaults(t *testing.T) {
	explicit := &schemas.StorefrontProfile{
		DisplayName: "Acme",
		Tagline:     "Paid APIs",
		LogoURL:     "https://acme.example/logo.png",
	}
	got := storefront.ResolvePublished(explicit, "https://seller.example")
	if got.DisplayName != "Acme" || got.Tagline != "Paid APIs" || got.LogoURL != "https://acme.example/logo.png" {
		t.Fatalf("unexpected profile: %+v", got)
	}
}

func TestMergeProfile_PartialUpdate(t *testing.T) {
	base := schemas.StorefrontProfile{DisplayName: "Acme", Tagline: "Old", LogoURL: "https://a/logo.png"}
	got := storefront.MergeProfile(base, schemas.StorefrontProfile{Tagline: "New"})
	if got.DisplayName != "Acme" || got.Tagline != "New" || got.LogoURL != "https://a/logo.png" {
		t.Fatalf("unexpected merge: %+v", got)
	}
}

func TestValidateLogoURL(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"https://cdn.example/logo.png", true},
		{"/obol-stack-logo.png", true},
		{"logo.png", false},
	} {
		err := storefront.ValidateLogoURL(tc.raw)
		if tc.ok && err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q: expected error", tc.raw)
		}
	}
}

func TestValidateContactEmail(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"ops@acme.example", true},
		{"", true},
		{"not-an-email", false},
	} {
		err := storefront.ValidateContactEmail(tc.raw)
		if tc.ok && err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q: expected error", tc.raw)
		}
	}
}

func TestIsDefaultLogoURL(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		deflt bool
	}{
		{"/obol-stack-logo.png", true},
		{"https://seller.example/obol-stack-logo.png", true},
		{"https://cdn.example/logo.png", false},
	} {
		if got := storefront.IsDefaultLogoURL(tc.raw); got != tc.deflt {
			t.Fatalf("IsDefaultLogoURL(%q) = %v, want %v", tc.raw, got, tc.deflt)
		}
	}
}
