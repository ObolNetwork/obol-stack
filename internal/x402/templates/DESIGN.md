# HTTP 402 page — Design

The HTML body that `x402-verifier` returns with status `402 Payment Required`
when a browser (or social-media link-preview scraper) hits a paid endpoint
without an `X-PAYMENT` header. Rendered by the Go `html/template` in
`payment_required.html`, populated from `paymentrequired.go`.

Canonical design system:
[`@obolnetwork/obol-ui`](../../../../obol-packages/packages/obol-ui/DESIGN.md).
Sister surface that this page is closest to:
[`obol-stack/web/public-storefront`](../../../web/public-storefront/DESIGN.md).

## 0. The five public surfaces

Each is identified by a `data-obol="page-*"` marker on its root element —
the stable hook custom CSS targets. All but the storefront are Go-rendered.

| Marker | Surface | Rendered by |
| --- | --- | --- |
| `page-storefront` | Catalog storefront — lists every offer | `web/public-storefront` (Next.js) |
| `page-landing` | Per-offer landing, on the offer's own hostname | `internal/serviceoffercontroller/offerbundle.go` |
| `page-402` | **This page** — the payment gate | `internal/x402/paymentrequired.go` |
| `page-signin` | SIWX challenge | `internal/x402/authgate.go` |
| `page-error` | Auth/verifier error | `internal/x402/authgate.go` |

Two distinctions worth holding onto, because both are easy to get backwards:

- **`page-402` is not the storefront and not the landing page.** It is served
  on a *paid resource path*, and only when `Accept` advertises `text/html`
  (`prefersHTML`, `paymentrequired.go`); everything else gets the JSON
  challenge. For a root-priced offer the paid resource is `POST /`, so a
  browser visiting that origin's `/` gets `page-landing`, never this page.
- **`data-obol="checkout"` is not this page.** It is a reserved mount div that
  appears on *both* `page-402` and `page-landing`. Custom CSS targeting it
  hits two surfaces at once.

This page is deliberately the **smallest, most constrained** of the five.

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

## 2. Brand contract (server-resolved)

Same palette, type, and shape language as
[`obol-ui/DESIGN.md § 1`](../../../../obol-packages/packages/obol-ui/DESIGN.md#1-brand-contract).

This page hardcodes **no** token values. The `:root` block is a single
template action:

```html
<style>
  :root {
    {{.Branding.ThemeCSS}}
```

`ThemeCSS` is produced by `internal/storefront/theme.go` —
`ResolveTheme(profile.Theme, profile.AccentColor).CSSVars()` — wired in at
`internal/x402/branding.go`. That package is the **single owner** of the
token palette for every Go-rendered surface; the seller's resolved profile
(theme preset + accent override + per-offer `spec.branding` patch) therefore
reaches this page and the offer landing page identically, with no per-file
copy to keep in step.

Do not paste hex values into this template. Add or change tokens in
`theme.go`'s `themePresets` and they render here automatically.

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

This file owns **no** copy of the palette (§ 2), so it cannot drift. Neither
can the offer landing page — both render `storefront.ResolveTheme(...).CSSVars()`
at request/reconcile time from the same Go source.

One hand-mirror survives, and it is on the TypeScript side:
`web/public-storefront/src/lib/theme.ts` → `LIGHT_THEME_VARS`. It is the
storefront's **fallback** only (`theme.ts` → `LIGHT_THEME_VARS[token] ?? "#000000"`),
used when `/api/services.json` doesn't carry `themeVars`; the happy path takes
its tokens from the feed, which the controller renders from `theme.go`. So
drift here degrades the no-feed fallback, it does not affect a healthy page —
which is precisely why it can rot unnoticed. To verify:

```bash
diff <(sed -n '/ThemeLight: {/,/^\t},$/p' internal/storefront/theme.go \
        | grep -oE '"[a-z0-9-]+": *"#[0-9a-f]{6}"' | tr -d '" ' | sort) \
     <(sed -n '/^export const LIGHT_THEME_VARS/,/^};$/p' web/public-storefront/src/lib/theme.ts \
        | grep -oE '"?[a-z0-9-]+"?: *"#[0-9a-f]{6}"' | tr -d '" ' | sort)
```

Silence means they agree. Both sides must yield 13 `token:#hex` pairs — if
either yields zero the check is vacuously passing and the extractor needs
fixing, not the palette. (The previous version of this section diffed hex out
of `payment_required.html`, which stopped containing any once theming moved to
`theme.go`; it compared an empty set forever.)

`internal/storefront/theme.go` is the authoritative source. Changing a preset
there is sufficient for every Go-rendered surface; update `theme.ts` in the
same commit.

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
