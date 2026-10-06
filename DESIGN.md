# Operator UI — Design

The **operator dashboard** for a locally-running Obol Stack. Audience: a
single, signed-in operator on `obol.stack` (hostname-restricted via Traefik).
This surface assumes a wallet, lists agents/services/registry state, and is
deliberately denser than the public storefront.

Canonical design system: [`@obolnetwork/obol-ui`](../obol-packages/packages/obol-ui/DESIGN.md).
This app **consumes that package** (currently `1.1.21` per `package.json`), so
tokens and primitives are inherited rather than re-declared. Bumps follow what
the canonical doc spells out.

---

## 1. Stack & boundaries

- **Framework**: Next.js 16.3 (App Router, Turbopack), React 19, TypeScript strict.
- **Styling**: `@stitches/react` via obol-ui (`styled`, `css`, theme tokens). No Tailwind.
- **Component sourcing priority**:
  1. `@obolnetwork/obol-ui` atoms (Box, Button, Text, IconButton, Badge,
     Tooltip, Switch, Skeleton, …) — always first.
  2. obol-ui molecules (Card, Tabs, Accordion, Navbar, Table, …) when the
     shape matches.
  3. Local Stitches composition (`styled(Box, …)`) for dashboard-specific layouts that don't have
     a direct molecule (the section card + collapsible header + count badge
     pattern is currently local — see § 3).
  4. New bespoke components only when nothing above fits.
- **Off-limits for this UI**: a light theme, marketing-style hero pages,
  multi-tenant chrome. The operator sees one stack at a time.

---

## 2. Brand contract (inherited)

Inherits everything in
[`obol-ui/DESIGN.md § 1`](../obol-packages/packages/obol-ui/DESIGN.md#1-brand-contract).
Explicit reminders that bite often:

- `<body>` background is `#091011` (`bg01`). It is currently set inline in
  `src/app/layout.tsx` rather than via Tailwind — keep both in sync.
- DM Sans is loaded from `next/font/google` (`--font-dm-sans`). Don't import
  it again locally.
- All copy uses sentence case — title case is reserved for `Obol Stack`,
  `Obol Agent`, and brand wordmarks.

---

## 3. Surface-specific patterns

These are the dashboard-shaped compositions you see on the index page; they
build on canonical primitives.

### Section card with collapsible header

The "AI Agent Instances" / "Obol Services" / "Agent Registry" panels.

- Outer container: canonical card (`bg02`, stroke, `12px` radius, `lg` padding).
- Header row: `Text variant="h4"` title + green-outline count `Badge` + chevron
  `IconButton` aligned right.
- Body collapses with `Accordion` (obol-ui molecule).

### Status pill

Use canonical `Badge` with the dot variant:

- `Running` → green dot + green text, `obolMidGreen` border.
- `Pending` → amber dot + amber text, faint amber border.
- `Error` → `test` (red) dot + red text.
  The "HERMES" / "AGENT" type tags are the _no-dot_ variant: just outlined text.

### Address row

`<wallet>` truncation + copy `IconButton` + external-link `IconButton`,
identical to the 402 page's `.icon-btn` (28×28 `bg03`, stroke border, hover →
`obolGreen` border + text). Wrap as `<AddressLine />` once we have a second
caller; until then keep inline.

### Asset balance grid

The OBOL/USDC/ETH per-network grid on the agent card. Mono `$3`, three
columns: symbol | amount | network + explorer link. Right-aligned numbers
because that's how operators scan a balance sheet.

### Primary CTA pair

"Chat" filled button + "Dashboard ↗" text-link button — sits at the bottom of
the agent card. Use `<Button variant="primary">` for Chat and `<Link>` for
Dashboard so they live on a single text baseline.

---

## 4. Layout

- Full-bleed body, content max-width ~`max-w-5xl` for the main column.
- Header: logo left, "Connect Wallet" CTA right. Logo is **left-aligned** here
  (the public storefront centres its logo — see that surface's DESIGN.md for
  the rationale).
- Footer: GitHub / Discord / Docs icon-links, bottom-right, `text-body`,
  hover → `obol-green`.
- No sidebar at this stage. If the index grows past two long panels we
  introduce a sticky right-rail of section anchors before a left sidebar.

---

## 5. Loading states

The `Loading dashboard data…` state currently swaps the whole main area for a
centred wordmark + caption. This is intentional: the first render decides
which sections to mount, and skeletons in unmounted sections would lie about
shape. Once a section is mounted, use `Skeleton` (obol-ui) for in-card loading
rather than reverting to the full-page state.

---

## 6. Bumping `@obolnetwork/obol-ui`

1. Read the canonical DESIGN.md changelog notes (or the package's release
   notes if more recent).
2. `pnpm up @obolnetwork/obol-ui@<next>` and run `pnpm dev:local`.
3. Visual smoke: load `/`, expand each section, hover hover-states on the
   address row, toggle a `Switch`. Compare against the screenshots in
   `plans/design-audit/` (committed for this purpose).
4. If a _token value_ (not just a component) shifted, also update:
   - `obol-stack/web/public-storefront/src/app/globals.css`
   - `obol-stack/internal/x402/templates/payment_required.html`
     See those surfaces' DESIGN.md files.

---

## 7. Non-goals

- No public, unauthenticated views — that's the storefront's job.
- No payment-required gating UI — that's the 402 template's job, rendered
  server-side by `x402-verifier`.
- No agent runtime UI (chat, skill management) — those live inside the agent
  pod (Hermes dashboard at `obol-agent.obol.stack`) and are out of scope here.
