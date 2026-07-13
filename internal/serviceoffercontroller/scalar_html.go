package serviceoffercontroller

// scalarBundleVersion is the pinned NPM version of @scalar/api-reference
// served from jsdelivr. Renovate tracks it via the scalar_html.go custom
// manager in renovate.json; bumps land as reviewable PRs so the bundled JS
// payload never drifts silently. After a bump, refresh the SRI hash below
// with scripts/update-scalar-sri.sh (the renovate PR body links it too).
// renovate: datasource=npm depName=@scalar/api-reference
const scalarBundleVersion = "1.62.5"

// scalarBundleSRI is the Subresource Integrity hash for the pinned bundle.
// The /api page is served over the public tunnel, so the third-party Scalar
// JS it pulls from jsdelivr must be integrity-checked: without this the
// browser executes whatever the CDN returns, unverified. Re-derive on every
// version bump by running scripts/update-scalar-sri.sh (fetches the pinned
// bundle and rewrites this constant). The hash is taken over the exact
// (jsdelivr-minified) bytes that the pinned URL serves; it must be refreshed
// in lockstep with scalarBundleVersion or the browser will block the script.
const scalarBundleSRI = "sha384-jVBCKhcCfx34USN27x4iQK1SBNdL/HxKq3KuBAxTS4WPaP5w80K4fjpwB+DezJL5"

// scalarHTML returns the static HTML shell served at /api. It loads the
// pinned @scalar/api-reference bundle from jsdelivr, points it at the
// sibling /openapi.json document, and lightly themes the renderer in Obol
// greens (#2FE4AB accent on a #091011 background) via Scalar's CSS
// custom-property interface. OG metadata reuses the storefront's existing
// /og-payment-required.png asset so link unfurls stay coherent across the
// tunnel UI surface.
//
// The HTML is deliberately small — Scalar handles the actual rendering.
// Any future theming work should land here in pure CSS without pulling in
// a build step.
func scalarHTML() string {
	integrityAttr := ""
	if scalarBundleSRI != "" {
		integrityAttr = ` integrity="` + scalarBundleSRI + `" crossorigin="anonymous"`
	}
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Obol Stack — API reference</title>

  <meta name="description" content="x402 payment-gated services advertised by this Obol Stack operator." />
  <meta property="og:title" content="Obol Stack — API reference" />
  <meta property="og:description" content="x402 payment-gated services advertised by this Obol Stack operator." />
  <meta property="og:image" content="/og-payment-required.png" />
  <meta property="og:type" content="website" />
  <meta name="twitter:card" content="summary_large_image" />
  <meta name="twitter:title" content="Obol Stack — API reference" />
  <meta name="twitter:description" content="x402 payment-gated services advertised by this Obol Stack operator." />
  <meta name="twitter:image" content="/og-payment-required.png" />
  <meta name="theme-color" content="#091011" />

  <style>
    :root {
      --scalar-color-1: #e5f9f1;
      --scalar-color-2: #b5ecd3;
      --scalar-color-3: #6fd0a8;
      --scalar-color-accent: #2FE4AB;
      --scalar-background-1: #091011;
      --scalar-background-2: #0d1618;
      --scalar-background-3: #11201f;
      --scalar-background-accent: #2FE4AB;
      --scalar-border-color: #1a2426;
      --scalar-button-1: #2FE4AB;
      --scalar-button-1-color: #091011;
      --scalar-button-1-hover: #5af0c0;
    }
    html, body { margin: 0; padding: 0; background: #091011; color: #e5f9f1; }
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
