package serviceoffercontroller

import (
	"html/template"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

// scalarBundleVersion is the pinned NPM version of @scalar/api-reference
// served from jsdelivr. Renovate tracks it via the scalar_html.go custom
// manager in renovate.json; bumps land as reviewable PRs so the bundled JS
// payload never drifts silently. After a bump, refresh the SRI hash below
// with scripts/update-scalar-sri.sh (the renovate PR body links it too).
// renovate: datasource=npm depName=@scalar/api-reference
const scalarBundleVersion = "1.62.9"

// scalarBundleSRI is the Subresource Integrity hash for the pinned bundle.
// The /api page is served over the public tunnel, so the third-party Scalar
// JS it pulls from jsdelivr must be integrity-checked: without this the
// browser executes whatever the CDN returns, unverified. Re-derive on every
// version bump by running scripts/update-scalar-sri.sh (fetches the pinned
// bundle and rewrites this constant). The hash is taken over the exact
// (jsdelivr-minified) bytes that the pinned URL serves; it must be refreshed
// in lockstep with scalarBundleVersion or the browser will block the script.
const scalarBundleSRI = "sha384-oBiNNPts22DP4xagXD0sZE4A/PyTMc+sYcxwx+v692dheBqr5zHQTp58ufiOqJH2"

// scalarHTML returns the static HTML shell served at /api. It loads the
// pinned @scalar/api-reference bundle from jsdelivr, points it at the
// sibling /openapi.json document, and themes the renderer from the
// operator's storefront profile (theme preset + optional accent) via
// Scalar's CSS custom-property interface. OG metadata reuses the
// storefront's existing /og-payment-required.png asset (or the operator's
// custom og image) so link unfurls stay coherent across the tunnel UI
// surface.
//
// The HTML is deliberately small — Scalar handles the actual rendering.
// Any future theming work should land here in pure CSS without pulling in
// a build step.
func scalarHTML(profile schemas.StorefrontProfile) string {
	integrityAttr := ""
	if scalarBundleSRI != "" {
		integrityAttr = ` integrity="` + scalarBundleSRI + `" crossorigin="anonymous"`
	}
	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)
	v := theme.Vars
	esc := template.HTMLEscapeString
	title := strings.TrimSpace(profile.DisplayName)
	if title == "" {
		title = "Obol Stack"
	}
	title += " — API reference"
	ogImage := strings.TrimSpace(profile.OGImageURL)
	if ogImage == "" {
		ogImage = "/og-payment-required.png"
	}
	favicon := ""
	if f := strings.TrimSpace(profile.FaviconURL); f != "" {
		favicon = "\n  <link rel=\"icon\" href=\"" + esc(f) + "\" />"
	}
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>` + esc(title) + `</title>

  <meta name="description" content="x402 payment-gated services advertised by this operator." />
  <meta property="og:title" content="` + esc(title) + `" />
  <meta property="og:description" content="x402 payment-gated services advertised by this operator." />
  <meta property="og:image" content="` + esc(ogImage) + `" />
  <meta property="og:type" content="website" />
  <meta name="twitter:card" content="summary_large_image" />
  <meta name="twitter:title" content="` + esc(title) + `" />
  <meta name="twitter:description" content="x402 payment-gated services advertised by this operator." />
  <meta name="twitter:image" content="` + esc(ogImage) + `" />
  <meta name="theme-color" content="` + v["bg01"] + `" />` + favicon + `

  <style>
    :root {
      --scalar-color-1: ` + v["light"] + `;
      --scalar-color-2: ` + v["body"] + `;
      --scalar-color-3: ` + v["muted"] + `;
      --scalar-color-accent: ` + v["green"] + `;
      --scalar-background-1: ` + v["bg01"] + `;
      --scalar-background-2: ` + v["bg02"] + `;
      --scalar-background-3: ` + v["bg03"] + `;
      --scalar-background-accent: ` + v["green"] + `;
      --scalar-border-color: ` + v["stroke"] + `;
      --scalar-button-1: ` + v["green"] + `;
      --scalar-button-1-color: ` + v["bg01"] + `;
      --scalar-button-1-hover: ` + v["green-dim"] + `;
    }
    html, body { margin: 0; padding: 0; background: ` + v["bg01"] + `; color: ` + v["light"] + `; }
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, system-ui, sans-serif; }
  </style>
</head>
<body>
  <script id="api-reference" data-url="/openapi.json"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@` + scalarBundleVersion + `"` + integrityAttr + `></script>
</body>
</html>
`
}
