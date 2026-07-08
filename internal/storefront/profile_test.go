package storefront_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
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
		// Inline data URIs: image mime + base64 only, size-capped.
		{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")), true},
		{"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>")), true},
		{"data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte("<html/>")), false},
		{"data:image/png;base64,not-valid-base64!!!", false},
		{"data:image/png;base64", false},                                                             // no comma separator
		{"data:image/svg+xml,<svg/>", false},                                                         // not base64-encoded
		{"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, 300<<10)), false}, // over 256 KiB cap
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

func TestInlineLogoFromFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pngBytes := []byte("\x89PNG\r\n\x1a\n0000000000000000")

	t.Run("png sniffed from bytes", func(t *testing.T) {
		uri, err := storefront.InlineLogoFromFile(write("logo.bin", pngBytes))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(uri, "data:image/png;base64,") {
			t.Fatalf("unexpected URI prefix: %.50s", uri)
		}
		if err := storefront.ValidateLogoURL(uri); err != nil {
			t.Fatalf("inlined logo should validate: %v", err)
		}
	})

	t.Run("svg by extension", func(t *testing.T) {
		uri, err := storefront.InlineLogoFromFile(write("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(uri, "data:image/svg+xml;base64,") {
			t.Fatalf("unexpected URI prefix: %.50s", uri)
		}
	})

	t.Run("rejects non-image", func(t *testing.T) {
		if _, err := storefront.InlineLogoFromFile(write("notes.txt", []byte("hello"))); err == nil {
			t.Fatal("expected error for non-image file")
		}
	})

	t.Run("rejects oversized", func(t *testing.T) {
		big := append(append([]byte{}, pngBytes...), make([]byte, 300<<10)...)
		if _, err := storefront.InlineLogoFromFile(write("big.png", big)); err == nil {
			t.Fatal("expected error for oversized file")
		}
	})

	t.Run("rejects empty and missing", func(t *testing.T) {
		if _, err := storefront.InlineLogoFromFile(write("empty.png", nil)); err == nil {
			t.Fatal("expected error for empty file")
		}
		if _, err := storefront.InlineLogoFromFile(filepath.Join(dir, "nope.png")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestDescribeLogoURL(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(make([]byte, 4<<10))
	for _, tc := range []struct {
		raw, want string
	}{
		{"https://cdn.example/logo.png", "https://cdn.example/logo.png"},
		{"/obol-stack-logo.png", "/obol-stack-logo.png"},
		{"", ""},
		{"data:image/png;base64," + payload, "inline image/png (4 KiB)"},
		{"data:garbage-no-comma", "data:garbage-no-comma"},
	} {
		if got := storefront.DescribeLogoURL(tc.raw); got != tc.want {
			t.Fatalf("DescribeLogoURL(%.40q) = %q, want %q", tc.raw, got, tc.want)
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
