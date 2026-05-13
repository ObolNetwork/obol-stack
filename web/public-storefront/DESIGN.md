# Public storefront — Design

The **public-facing landing page** that the Cloudflare tunnel serves on a
running Obol Stack. Lists this agent's monetised services and shows buyers
how to pay. Audience: humans landing from a shared URL, AI agents discovering
services via `/skill.md`, and OG/Twitter scrapers.

Canonical design system:
[`@obolnetwork/obol-ui`](../../../obol-packages/packages/obol-ui/DESIGN.md).
Unlike the operator UI, **this app does *not* consume the obol-ui npm
package**. Tokens are mirrored by hand in `src/app/globals.css`. The rationale
and the mirror contract are in § 4 below.

---

## 1. Why this surface is its own app

- **It's served from a different host than the dashboard.** The operator UI is
  hostname-restricted to `obol.stack` (local-only); this storefront is the
  Cloudflare tunnel's public landing, intentionally reachable from the open
  internet.
- **It has zero authenticated state.** No wallet connect, no signing, no
  user identity. Anyone with the URL can browse.
- **It must stay light.** Initial HTML + critical CSS need to render quickly
  even when the tunnel is on a constrained link, and OG scrapers need the
  metadata before any JS executes. That's why there's no Stitches runtime,
  no Radix, no RainbowKit, no SDK — just Next 16 + Tailwind v4.

---

## 2. Stack

- **Framework**: Next.js 16 (App Router, Turbopack), React 19, TypeScript.
- **Styling**: Tailwind v4 only, configured via `@theme` block in
  `src/app/globals.css`. **No CSS-in-JS runtime.**
- **Fonts**: DM Sans via `next/font/google` (loaded once in `layout.tsx`).
- **Data source**: `/api/services.json` from `obol-skill-md` in the cluster.
  Server fetched with `cache: "no-store"` for first paint; client polls every
  10s to surface new ServiceOffers.

---

## 3. Brand contract (mirrored)

Same colour palette, type ramp, radii, spacing, and motion as
[`obol-ui/DESIGN.md § 1`](../../../obol-packages/packages/obol-ui/DESIGN.md#1-brand-contract).
Token names are translated into the Tailwind v4 `@theme` namespace in
`globals.css`:

| obol-ui token | Tailwind class root |
| --- | --- |
| `bg01`–`bg05` | `bg-bg01` … `bg-bg05` |
| `obolGreen` | `bg-obol-green`, `text-obol-green`, `border-obol-green` |
| `obolGreenLight` (hover) | `bg-obol-green/15` (with `/30` border) |
| `obolGreen-dim` | `text-obol-green-dim` / etc — use sparingly, hint state only |
| `obolBlue` | `bg-obol-blue` |
| `obolGold` (amber) | `bg-amber`, `text-amber` |
| `test` (red) | `bg-red`, `text-red` |
| `light` / `body` / `muted` | `text-light` / `text-body` / `text-muted` |
| `stroke` | `border-stroke` |

---

## 4. Token-sync contract with obol-ui

The four hex values that matter — `bg02`, `bg03`, `obolGreen`, `stroke` — and
the font families are **duplicated** between this app's `globals.css` and the
canonical Stitches theme. Any change in one must be mirrored in the other.

To check for drift:

```bash
grep -E 'bg0[1-5]|obol-green|text-(light|body|muted)|stroke' src/app/globals.css
grep -E 'bg0[1-5]|obolGreen|light|body|muted|stroke' ../../../obol-packages/packages/obol-ui/stitches.config.ts
```

Values must match. If you're adding a *new* token here that obol-ui also
needs, add it canonically first (Stitches → bump → release) and only then
mirror down. If a token only makes sense for the storefront, prefix it
(`storefront-…`) so it doesn't masquerade as canonical.

---

## 5. Components in this app

All in `src/components/`. None are exported anywhere; this app is a leaf.

### `Header.tsx`
Centred logo + thin `stroke` bottom border. The centring is intentional and is
the visible difference from the operator UI's left-aligned header — buyers
arriving on this URL should see a marketing-shaped page, not a tool. Width
matches the main column (`max-w-3xl`).

Wordmark source: `public/obol-stack-logo.svg` is canonical; the sibling
`obol-stack-logo.png` is a 3× raster of it for byte-consumers (402 template,
OG image). Regenerate with `scripts/build-wordmark.html` — see
`scripts/build-wordmark.README.md` for the exact command.

### `ServiceCard.tsx`
The repeating primitive on the landing list. Implements the canonical card
+ pill + tabs + code-block shapes:
- Card: `rounded-lg border border-stroke bg-bg02 p-5 hover:bg-bg03`.
- Type pill: `rounded-full px-2.5 py-0.5 text-xs` with type-coloured
  bg/text/border using obol-green / amber / bg03+stroke.
- Tabbed buy flow: `border-b-2` accent in `obol-green`.
- `<Snippet />` (code block): `rounded bg-bg01 p-3 pr-12 text-xs font-mono
  border border-stroke`, copy button absolutely positioned top-right.

When extending: if you find yourself adding a fourth tab type here, lift the
tab bar into its own component first — don't grow `ServiceCard` further.

### `ServicesList.tsx`
The polling wrapper around `ServiceCard`. Empty state lives here, not in
`ServiceCard`.

### `PaymentFlow.tsx`
The collapsible "How x402 payments work" explainer. Same card chrome as
`ServiceCard`, but `cursor-pointer` and a chevron — purely educational.

---

## 6. Specific shared shapes vs the 402 page

The HTTP 402 page rendered by `x402-verifier` (Go template at
`internal/x402/templates/payment_required.html`) is the storefront's narrower
cousin. Patterns must match:

| Shape | Storefront | 402 page | Sync rule |
| --- | --- | --- | --- |
| Code/prompt block | `<Snippet>` (`bg01 + stroke`, copy top-right) | `.prompt` / `pre.json` | Same padding (`16/12`), same `bg01` fill, same copy affordance position |
| Card | `ServiceCard` (`bg02 + stroke + rounded-lg`) | `.card` | Both `12px` radius, `24px` padding, `bg02` fill |
| Address line | (not yet in storefront) | `.grid dd` with two `.icon-btn` | When storefront grows an address row, copy the 402 page's pattern verbatim |
| Footer link row | `text-xs` `obol-green` mono `/skill.md`, `/.well-known/agent-registration.json` | `footer a` `obol-green` text | Always `obol-green`, never `text-body` |

---

## 7. Off-limits

- **No wallet connect.** This is a public surface; the storefront never asks
  for a signature.
- **No SSR-streaming heavy components.** Everything above the fold needs to
  render in the first HTML payload (OG metadata + service list).
- **No client-only navigation.** Each `/...` is a Next route the tunnel can
  share as a link.
- **No obol-ui package import.** If you reach for one, that's a signal the
  primitive should be lifted into obol-ui first.

---

## 8. Bumping

There is nothing to bump here unless the canonical design system shifts.
When that happens, the operator UI's `DESIGN.md § 6` lists the same
storefront file as a sync target — keep the two glued.
