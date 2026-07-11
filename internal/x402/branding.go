package x402

import (
	"html/template"
	"strings"
	"sync/atomic"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

// currentStorefrontProfile is the operator profile the verifier renders its
// human-facing pages (402 paywall, SIWX sign-in, error pages) with. It is fed
// by the obol-storefront-profile ConfigMap watcher (WatchStorefrontProfile)
// in kube mode; nil means "stack defaults" (standalone gateway, file mode,
// tests, or no operator override set).
var currentStorefrontProfile atomic.Pointer[schemas.StorefrontProfile]

// SetStorefrontProfile swaps the profile used for page branding. Pass nil to
// revert to stack defaults. Safe for concurrent use.
func SetStorefrontProfile(p *schemas.StorefrontProfile) {
	currentStorefrontProfile.Store(p)
}

// Branding is the resolved, render-ready seller identity every verifier HTML
// surface shares: who the seller is, their logo/favicon/OG image (absolute
// URLs), and the theme emitted as CSS custom properties.
type Branding struct {
	// SiteName is the seller display name ("Obol Stack" by default).
	SiteName string
	// LogoURL is the header logo (absolute URL or data: URI).
	LogoURL string
	// ShowName is true when templates should render the display name as
	// text beside the logo: always for operator-set logos (usually square
	// marks), and for the default brand on the light theme (the default
	// wordmark is light-on-dark, so light pages get the dark square mark
	// plus the name instead).
	ShowName bool
	// FaviconURL is the tab icon (absolute URL or data: URI).
	FaviconURL string
	// OGImageURL is the link-preview image (absolute URL), already
	// defaulted to the storefront's generated preview when unset.
	OGImageURL string
	// ThemeCSS is the ordered "--bg01:#fff;--bg02:...;" declaration list
	// for the :root block. Values come from hardcoded presets or a
	// hex-validated accent, so the CSS-context interpolation is safe.
	ThemeCSS template.CSS
	// ThemeColor feeds <meta name="theme-color">.
	ThemeColor string
	// CustomCSS is the operator stylesheet, injected in its own <style>
	// element after the theme. Already re-validated by SafeCustomCSS
	// (size cap + style-element breakout) — empty when absent or unsafe.
	CustomCSS template.CSS
}

// resolveBranding merges the current operator profile over defaults,
// overlays the optional per-origin patch (a hostname-bound offer's
// spec.branding — nil for storefront-wide rendering), and absolutizes
// relative asset paths against siteURL (the public origin of the request
// being served, e.g. "https://seller.example.com").
func resolveBranding(siteURL string, patch *schemas.StorefrontProfile) Branding {
	siteURL = strings.TrimRight(siteURL, "/")
	profile := storefront.ResolvePublished(currentStorefrontProfile.Load(), siteURL)
	if patch != nil {
		profile = storefront.MergeProfile(profile, *patch)
	}
	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)

	logo := absolutizeAssetURL(profile.LogoURL, siteURL)
	favicon := absolutizeAssetURL(profile.FaviconURL, siteURL)
	custom := !storefront.IsDefaultLogoURL(profile.LogoURL)
	showName := custom
	if !custom && !theme.Dark {
		// The default wordmark is light-on-dark; on light pages swap in
		// the dark square mark and spell the name out beside it.
		logo = siteURL + storefront.DefaultMarkPath
		showName = true
	}
	if favicon == "" {
		if custom {
			// Operator logo doubles as the favicon (matches the public
			// storefront's icon behaviour).
			favicon = logo
		} else {
			favicon = siteURL + "/favicon.png"
		}
	}
	ogImage := absolutizeAssetURL(profile.OGImageURL, siteURL)
	if ogImage == "" {
		ogImage = siteURL + "/og-payment-required.png"
	}

	return Branding{
		SiteName:   profile.DisplayName,
		LogoURL:    logo,
		ShowName:   showName,
		FaviconURL: favicon,
		OGImageURL: ogImage,
		ThemeCSS:   template.CSS(theme.CSSVars()),
		ThemeColor: theme.ThemeColor(),
		CustomCSS:  template.CSS(storefront.SafeCustomCSS(profile.CustomCSS)),
	}
}

// absolutizeAssetURL prefixes site-relative asset paths with the public
// origin; absolute http(s) URLs and data: URIs pass through, everything else
// (including empty) returns "".
func absolutizeAssetURL(raw, siteURL string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "data:"):
		return raw
	case strings.HasPrefix(raw, "/"):
		return siteURL + raw
	default:
		return ""
	}
}
