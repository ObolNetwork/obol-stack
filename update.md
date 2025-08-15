# Workflow: Convert Later (Next.js + Tailwind) UI to Earlier Design

## 1) Define scope & acceptance
- **Goal:** Recreate the earlier (compact) layout and styling using existing data + icons.
- **Outcomes to demo:**  
  - Split layout: **left** = “Ethereum Mainnet” summary + compact stats + clients; **right** = “Node Overview” (progress bars) + “Ports” list.  
  - Typography, spacing, badges, progress bars, pills match earlier design.  
  - Responsive: single column ≤1024px; two-column >1024px.
- **Non-goals:** Data model changes, backend changes.

## 2) Inventory current code
- List current components and pages:
  - `pages/index.tsx` (dashboard shell)
  - Components like `Header`, `StatCard`, `MetricTile`, `PortsPanel`, `ClientCard`, `ProgressBar`, etc.
- Capture current theme tokens from `tailwind.config.js` and any CSS vars.
- Screenshot current UI for before/after diff.

## 3) Update design tokens (Tailwind)
- In `tailwind.config.js`:
  - **Colors:** introduce subdued slate/ink grays and Obol green accents used in the earlier design:  
    - `bg`: `#0c1311`, `card`: `#111a17`, `muted`: `#0f1714`, `stroke`: `#20332c`, `accent`: `#2be39a`, `accent-weak`: `#8bf2c8`, `warning`: `#f5a623`.
  - **Radii:** `xl: 16px`, `2xl: 20px` (earlier design is rounder than the later).
  - **Shadow:** soft, low elevation: `shadow-[0_1px_0_0_rgba(255,255,255,.04)_inset,0_8px_24px_rgba(0,0,0,.35)]`.
  - **Font sizes:** add compact numeric sizes: `num-lg: 28px/34`, `num-md: 20px/28`, `meta: 11px/16`.
- Expose tokens as CSS vars on `:root` (dark).

## 4) Restructure page layout
- Replace “big-tile grid” with a **two-column grid**:
  - Container: `mx-auto max-w-[1200px] px-6`
  - Grid: `grid gap-6 lg:grid-cols-[1fr_380px]`
- Left column (stacked):
  1) **Network summary card** (title row + status pill)
  2) **Compact metrics row** (Finalized, Block Height, Node Mode, Participation)
  3) **Clients row** (two small cards: Geth, Prysm)
- Right column (stacked):
  1) **Node Overview** (Disk, Memory, CPU, Uptime with thin progress bars)
  2) **Ports** list (P2P, JSON-RPC, Prometheus, Beacon API)

## 5) Header refinements
- Keep brand at top-left. Move **env/mode pills** (“Testnet”, “Full Mode”) to top-right as subtle chips.
- Pills style: `rounded-full px-2.5 py-1 text-[11px] bg-muted text-gray-200 border border-stroke`.

## 6) “Ethereum Mainnet” summary card (left, top)
- Card: `rounded-2xl bg-card border border-stroke p-4`
- Title row:
  - Left: icon + **Ethereum Mainnet**
  - Right: **Synced** green dot pill (`bg-[rgba(43,227,154,.1)] text-accent border border-[rgba(43,227,154,.25)]`)
- Under title: **inline meta strip** like earlier design  
  - Keys: *Block, Headslot, Distance to justify, Peers* rendered as tiny labels with values, separated by faint dividers (`before:content-['•']`).
- Typography:
  - Labels: `text-[11px] uppercase tracking-wide text-gray-400`
  - Values: `font-semibold text-gray-100`

## 7) Compact metrics row
- Four small columns inside same card (or a sub-grid): `grid grid-cols-4 gap-4 mt-3`
  - Each item: label (meta), value (numeric emphasis).
  - Participation shows **green %** (accent) as in earlier design.
- Numbers style: `font-semibold tabular-nums tracking-[.02em]`

## 8) Clients cards (Geth / Prysm)
- Two mini-cards side-by-side: `grid grid-cols-2 gap-4 mt-4`
- Each: icon avatar, title, role (Execution/Consensus), **ports + peers** as tiny line items.
- Status dot + “Synced” in top-right of each mini-card.

## 9) Node Overview (right column, top)
- Card: `rounded-2xl bg-card border border-stroke p-4`
- Title “Node Overview”.
- **List items** (Disk, Memory, CPU, Uptime) each in a thin row:
  - Left: icon + label; Right: **percent chip** (e.g., `32%`) matching earlier design.
  - Under label: small value text (e.g., `1247.8 GB of 2000GB`).
  - **Progress bar** (thin): container `h-[6px] rounded-full bg-muted`, fill `h-full rounded-full bg-accent` with smooth `transition-[width]`.
- CPU row shows **99%** warning color when ≥90% (`bg-warning` for chip and bar).

## 10) Ports panel (right column, bottom)
- Card with simple rows:
  - Columns: **Name** (left), **Port** (right, monospace), **pill** (right-aligned) = “Open/Closed”.
  - Row style: `flex items-center justify-between py-2 border-t border-stroke/60 first:border-t-0`.
  - Pills: green for Open, red for Closed (soft backgrounds).

## 11) Footer actions
- Stick to bottom-left of page: **Export Diagnostics** and **View Logs** minimal buttons (`variant="ghost"`), matching earlier spacing.

## 12) Component mapping (rename + reuse)

| Current (later UI)             | New (earlier UI)                         |
| ---                            | ---                                      |
| `StatCard` (big)               | `SummaryCard` (compact, multi-metric)    |
| `MetricTile`                   | `InlineMeta` + `MetricItem`              |
| `ProgressBar`                  | `ThinProgress` (6px height + chip)       |
| `PortsPanel` (tabley)          | `PortsList` (rows with pills)            |
| `ClientCard` (large)           | `ClientMiniCard`                         |
| `HeaderBadges`                 | `EnvPills` (right-aligned)               |

## 13) Implementation steps (PR checklist)

**PR 1 – Tokens & primitives**
- [ ] Add color/radius/shadow tokens to Tailwind config.  
- [ ] Create primitives: `Pill`, `ThinProgress`, `Card`, `InlineMetaSeparator`.  
- **DoD:** A Storybook (or /sandbox) page shows primitives visually.

**PR 2 – Layout shell**
- [ ] Replace page container with two-column grid + responsive collapse.  
- **DoD:** Empty cards show in correct positions at breakpoints.

**PR 3 – Summary card**
- [ ] Implement title row with status pill + meta strip.  
- [ ] Add compact metrics row (Finalized, Block Height, Node Mode, Participation).  
- **DoD:** Text sizes/spacing roughly match earlier screenshot.

**PR 4 – Client mini-cards**
- [ ] Build `ClientMiniCard` ×2 with peers/ports + synced pill.  
- **DoD:** Visual parity to earlier design’s client tiles.

**PR 5 – Node Overview**
- [ ] Implement Disk, Memory, CPU, Uptime with thin progress bars + value lines + percent chips.  
- [ ] Add warning color threshold logic (≥90% CPU).  
- **DoD:** Matches earlier right-panel aesthetics.

**PR 6 – Ports list**
- [ ] Render rows for P2P, JSON-RPC, Prometheus, Beacon API with right-aligned status pills.  
- **DoD:** Row density and pills look like earlier design.

**PR 7 – Footer actions & header pills**
- [ ] Add `Export Diagnostics` / `View Logs` ghost buttons in footer.  
- [ ] Move env/mode pills to header right.  
- **DoD:** Placement + styles align with earlier.

**PR 8 – Polish**
- [ ] Tabular numerals, consistent letter-spacing, subtle dividers, hover states (2% lighten).  
- [ ] Focus rings: `focus-visible:outline-accent/60`.  
- [ ] Audit spacing: 8/12/16/24 scale; eliminate oversized gutters.  
- [ ] Dark-mode contrast AA for meta text.  

## 14) Example skeletons (Tailwind-focused)

**Thin progress (utility-first)**
```tsx
export function ThinProgress({ value }: { value: number }) {
  return (
    <div className="w-full h-[6px] rounded-full bg-muted/60 overflow-hidden">
      <div
        className="h-full rounded-full bg-accent transition-[width] duration-500"
        style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }}
      />
    </div>
  );
}
```

14) Example skeletons (Tailwind-focused)

Thin progress (utility-first)

```
export function ThinProgress({ value }: { value: number }) {
  return (
    <div className="w-full h-[6px] rounded-full bg-muted/60 overflow-hidden">
      <div
        className="h-full rounded-full bg-accent transition-[width] duration-500"
        style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }}
      />
    </div>
  );
}

```
Status pill

```

export function Pill({
  tone = 'neutral',
  children,
}: {
  tone?: 'neutral' | 'success' | 'warning' | 'danger';
  children: React.ReactNode;
}) {
  const tones = {
    neutral: 'bg-muted text-gray-200 border-stroke',
    success:
      'bg-[rgba(43,227,154,.10)] text-accent border-[rgba(43,227,154,.25)]',
    warning: 'bg-[rgba(245,166,35,.12)] text-[#f5a623] border-[#f5a623]/30',
    danger: 'bg-[rgba(235,87,87,.12)] text-[#eb5757] border-[#eb5757]/30',
  } as const;

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px] ${tones[tone]}`}
    >
      <span className="size-1.5 rounded-full bg-current opacity-80" />
      {children}
    </span>
  );
}

```

Summary metric item

```

function Metric({
  label,
  value,
  accent = false,
}: {
  label: string;
  value: string;
  accent?: boolean;
}) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wide text-gray-400">
        {label}
      </div>
      <div
        className={`font-semibold tabular-nums ${
          accent ? 'text-accent' : 'text-gray-100'
        }`}
      >
        {value}
      </div>
    </div>
  );
}

```

15) Data hook alignment

Reuse existing data selectors; add a formatter layer for compact strings:

Blocks: 18,972,341 (thin thousand separators)

Disk: 1247.8 GB of 2000GB

Uptime: 7d 3h 22mins

Centralize formatters in lib/format.ts to keep components presentational.

16) QA & visual regression

Add three Cypress visual snapshots: lg, md, sm.

Verify contrast ratios for meta text and disabled pills.

Check long numbers wrapping (tabular-nums whitespace-nowrap).

17) Hand-off notes

Keep the later UI components; mark as deprecated or move to /legacy/ for reference.

Document tokens and component usage in a short README.md within /components/ui.