# HTTP 402 page — Design

The HTML body that `x402-verifier` returns with status `402 Payment Required`
when a browser (or social-media link-preview scraper) hits a paid endpoint
without an `X-PAYMENT` header. Rendered by the Go `html/template` in
`payment_required.html`, populated from `paymentrequired.go`.

Canonical design system:
[`@obolnetwork/obol-ui`](../../../../obol-packages/packages/obol-ui/DESIGN.md).
Sister surface that this page is closest to:
[`obol-stack/web/public-storefront`](../../../web/public-storefront/DESIGN.md).
This page is deliberately the **smallest, most constrained** of the four
surfaces.

---

## 1. Why this surface is a single static-ish HTML file

The 402 response is fired from a Go service that knows nothing about Next.js,
React, Tailwind, or npm. The page must:

- **Render in a single response.** No second round-trip to load CSS or JS.
  Every byte is in the HTML body the verifier writes.
- **Stand alone if the storefront is offline.** The verifier may answer 402
  before any other Obol pod is healthy, so the page can't depend on the
  storefront serving (it only references it for the wordmark image and an
  outbound link).
- **Be readable by OG/Twitter scrapers without JS.** The metadata + visible
  copy must be in the initial HTML.
- **Stay tiny.** Currently ≈8 KB pre-compression. Don't grow that without
  weighing what you're adding against the constraint above.

Consequences: no external stylesheet, no JS bundle, no web fonts (DM Sans is
*requested* but falls back to system-ui gracefully), no Stitches, no React.
The page is one inline `<style>` block plus one tiny `<script>` for copy
buttons.

---

## 2. Brand contract (mirrored)

Same palette, type, and shape language as
[`obol-ui/DESIGN.md § 1`](../../../../obol-packages/packages/obol-ui/DESIGN.md#1-brand-contract).
Token values are duplicated inline at the top of the `<style>` block:

```css
:root {
  --bg01: #091011; --bg02: #111f22; --bg03: #182d32; --bg04: #243d42;
  --stroke: #1e3a3f;
  --green: #2fe4ab; --green-dim: #1a7a5c;
  --light: #d9eef3; --body: #9cc2c9; --muted: #475e64;
}
```

These mirror `obol-packages/packages/obol-ui/stitches.config.ts` and
`obol-stack/web/public-storefront/src/app/globals.css`. The mirror is by
hand — see § 5 for the drift check.

---

## 3. Template variables

Populated by `internal/x402/paymentrequired.go → sendPaymentRequiredHTML`:

| Var | Source | Purpose |
| --- | --- | --- |
| `Title` | static | `<title>` + OG title — `"Payment required — Obol Stack"` |
| `Description` | `buildMetaDescription(display)` | meta description + OG description (dynamic price/network when known) |
| `PageURL` | `buildResourceURL(r)` | canonical OG url |
| `StorefrontURL` | `resolveSiteURL(r)` | derived from `X-Forwarded-Host`; used for wordmark + sibling links |
| `WordmarkURL` | `${StorefrontURL}/obol-stack-logo.png` | logo image — 3× raster of `web/public-storefront/public/obol-stack-logo.svg`; rebuild the PNG via the storefront's `scripts/build-wordmark.html` when the SVG changes |
| `OGImageURL` | `${StorefrontURL}/og-payment-required.png` | 1200×630 social card |
| `Endpoint` | `display.Endpoint` | shown in service card |
| `NetworkLabel` | `display.NetworkLabel` | shown in service card |
| `PriceDisplay` | `display.PriceDisplay` (`"0.001 USDC per request"`) | green-highlighted row |
| `PayToDisplay` | `truncateAddress(payTo)` | `0xabcd…1234` |
| `PayToFull` | `display.PayToFull` | clipboard payload + explorer URL anchor |
| `ExplorerURL` | `display.ExplorerURL` | block-explorer link; empty hides the second icon button |
| `PromptObol` | `buildObolPrompt(...)` | prompt for the user's own Obol Agent |
| `PromptOther` | `buildOtherAgentPrompt(...)` | prompt for a generic AI agent |
| `JSONBody` | `json.MarshalIndent(...)` | raw x402 wire-format response |

`display` is a `PaymentDisplay` struct — all fields pre-formatted on the Go
side. The template does no number formatting and no address truncation.

---

## 4. Surface-specific patterns

Five shapes, all inline:

### Header lockup
Wordmark link (left) + `HTTP 402` outline pill (right). The pill is a `1px`
green border, mono `13px 600`, `999px` radius. **This is the canonical pill
shape** — when the storefront grows a status pill it should match.

### Service card
`.card` — `bg02` fill, `1px stroke`, `12px` radius, `24px` padding. Internal
`<dl class="grid">` uses a 120px / 1fr two-column grid with mono values.
The `Pay to` row uses the address-line pattern: truncated address + 28×28
icon button (copy) + optional 28×28 icon button (block explorer).

### Prompt block (`.prompt`)
For the two natural-language prompts. `bg01` fill, `1px stroke`, `8px`
radius, mono `13px`, `pre-wrap`. Copy button absolute top-right (`.copy`).

### JSON block (`pre.json`)
Same chrome as `.prompt` but `white-space: pre`, scrollable horizontally,
slightly smaller font (`12px`) and `body` colour for less visual weight than
the human-facing prompts.

### Icon button (`.icon-btn`)
28×28 square, `bg03` fill, `1px stroke`, `6px` radius. Hover transitions
`color`/`border-color`/`background` over 120ms to `obolGreen` / `obolGreen` /
`bg04`. Used twice — copy address, open in explorer.

---

## 5. Token-sync contract

This file owns *one of three* hand-mirrored copies of the obol-ui token
palette. When any of those move, all three must move together. To verify:

```bash
diff <(grep -E '#[0-9a-f]{6}' internal/x402/templates/payment_required.html | sort -u) \
     <(grep -E '#[0-9a-f]{6}' web/public-storefront/src/app/globals.css | sort -u)
```

Both files must reference identical hex values for `bg01..bg04`, `stroke`,
`green`, `green-dim`, `light`, `body`, `muted`. The fifth value `bg05` is
optional here (not yet used by this page; add when the storefront introduces
a card that needs a third elevation).

Whenever you edit token CSS in this file, also update the storefront and
verify the canonical Stitches theme is the authoritative source.

---

## 6. Accessibility & no-JS behaviour

- The `<noscript>` rule hides every copy button (`.copy`, `.icon-btn[data-copy]`)
  so users without JS aren't shown affordances that don't work.
- The block-explorer link is a real `<a>`, so it works without JS.
- All interactive elements have `aria-label` or `title`; the copy buttons
  flash a "Copied" state via `aria-label` swap + `.copied` class.
- Colour contrast: `text-light` on `bg01` is the only body pairing; muted
  copy is reserved for non-essential labels.
- The page targets `viewport: device-width, initial-scale=1` and the
  `.wrap` max-width of 760px keeps line-length readable on every viewport
  without media queries.

---

## 7. Off-limits

- **No JS framework.** Don't introduce React, Preact, Alpine, htmx, etc.
- **No external stylesheet.** Don't add a `<link rel="stylesheet">`.
- **No web font payload.** DM Sans is *named* in the font stack but the page
  must remain readable when it isn't loaded — verify with a no-DM-Sans
  preview before merging changes that affect text sizing.
- **No client-side data fetch.** All copy is server-rendered via Go template.
