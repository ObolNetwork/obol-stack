# NetworkRpc Card — Layout & Data Design

## Context

The `NetworkRpc` card represents an eRPC proxy instance. Unlike the other network cards
(which show a single chain's node metrics), eRPC is a **multi-chain RPC router** — it may
serve several chains simultaneously, each backed by one or more upstream providers.

The primary job of the card is to communicate at a glance:

1. Which chains are being served (and whether each is healthy)
2. What upstream(s) back each chain, and how they're performing

Real data shape (single upstream, single network today — but designed to scale):

- `status`: "OK" | error string
- `upstreams`: keyed by ID, each with `network: "evm:1"` and rich metrics
- Metrics per upstream: `cordoned`, `errorRate`, `requestsTotal`, `responseQuantiles` (p50–p99),
  `blockHeadLag`, `throttledRate`, `selfRateLimitedTotal`, `remoteRateLimitedTotal`

---

## Option A — Chain-first rows with inline upstream chips

Each chain gets a compact row. Upstream providers are shown as small status chips inline.
Good for scanning many chains quickly; upstream detail is secondary.

```
┌─────────────────────────────────────────────────────────────────┐
│  [≡] Network RPC          default          ● Operational        │
├─────────────────────────────────────────────────────────────────┤
│  1 chain  ·  1 upstream                                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  MAINNET                                          ● synced      │
│  ┌──────────────┐                                               │
│  │ ● erpc-gcp   │  148ms  ·  55 reqs  ·  0% err                │
│  └──────────────┘                                               │
│                                                                 │
│  SEPOLIA                                          ○ unsynced    │
│  ┌──────────────┐                                               │
│  │ ○ erpc-gcp   │  —                                            │
│  └──────────────┘                                               │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Highlighted data**: chain status, upstream name + health dot, p50 latency, req count, error %.
**Hidden data**: p90/p99, throttle rate, block lag (available on hover/expand).
**Pros**: Very scannable. Chain health is immediately visible.
**Cons**: Upstream detail is thin inline; hard to compare two upstreams on the same chain.

---

## Option B — Compact grid: chains as column headers, upstreams as rows

Treats it like a matrix. Works well when upstreams serve multiple chains.

```
┌─────────────────────────────────────────────────────────────────┐
│  [≡] Network RPC          default          ● Operational        │
├─────────────────────────────────────────────────────────────────┤
│                  MAINNET        SEPOLIA        HOODI             │
│                  ● synced       ○ —            ○ —              │
├────────────────┬────────────────┬──────────────┬────────────────┤
│  erpc-gcp      │ ●  148ms  55r  │     —        │     —          │
│  erpc-fallback │ ○  cordoned    │     —        │     —          │
└────────────────┴────────────────┴──────────────┴────────────────┘
```

**Highlighted data**: chain health per column, upstream health + p50 + req count per cell.
**Pros**: Best layout when upstreams span multiple chains. True matrix view.
**Cons**: Wastes horizontal space for unconfigured chain/upstream combos.
More complex to implement with dynamic column count.

---

## Option C — Chain accordion, upstream cards inside (current approach, refined)

Each configured chain is a section. Upstream cards sit inside with richer metrics.
This is closest to the current implementation but with tighter, more purposeful data selection.

```
┌─────────────────────────────────────────────────────────────────┐
│  [≡] Network RPC          default          ● Operational        │
├─────────────────────────────────────────────────────────────────┤
│  1 network  ·  1 upstream  ·  all systems operational           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ▸ MAINNET  ─────────────────────────────────  ● synced         │
│                                                                 │
│    ┌──────────────────────────────────────────┐                 │
│    │  erpc-gcp                   ● Healthy    │                 │
│    │  ─────────────────────────────────────   │                 │
│    │  Latency   p50: 148ms  p90: 181ms        │                 │
│    │  Traffic   55 reqs   · 0% errors         │                 │
│    │  Lag       block: 0  · finalization: 0   │                 │
│    └──────────────────────────────────────────┘                 │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Highlighted data**: chain status in section header; per upstream: p50+p90, req count,
error %, block lag + finalization lag.
**Pros**: Most room for per-upstream detail. Easy to add more metrics.
**Cons**: Takes more vertical space per chain. Summary harder to scan across many chains.

---

## Option D — Dense header bar per chain + expandable upstream detail

Chain health shown as a tight colored bar/row. Clicking expands to upstream detail.
Best for dashboards where quick-scan is primary and detail is secondary.

```
┌─────────────────────────────────────────────────────────────────┐
│  [≡] Network RPC          default          ● Operational        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ████ MAINNET     1 upstream   148ms avg   55 reqs   ● OK  [v]  │
│                                                                 │
│    erpc-gcp  ●  p50 148ms · p90 181ms · 55 reqs · 0 err        │
│                                                                 │
│  ░░░░ SEPOLIA     no upstreams configured              ○   [v]  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

Chain row shows: chain name, upstream count, **aggregated** p50, total reqs, status.
Expanded row shows per-upstream breakdown.

**Highlighted data (collapsed)**: chain-level aggregates — avg latency, total reqs, ok/error.
**Highlighted data (expanded)**: per-upstream p50/p90, req count, error count.
**Pros**: Most information-dense. Scales well to many chains. Aggregates feel meaningful.
**Cons**: Requires expand/collapse state. Aggregating latency across upstreams needs thought.

---

## Data priority recommendation

Regardless of layout choice, these are the fields ranked by user value:

| Priority | Field                          | Why                                          |
| -------- | ------------------------------ | -------------------------------------------- |
| 1        | Chain health (synced/cordoned) | Is this chain usable right now?              |
| 2        | p50 latency                    | Is it fast?                                  |
| 3        | Error rate / errors total      | Is it reliable?                              |
| 4        | Request count                  | Is it being used / is data fresh?            |
| 5        | p90 latency                    | Tail latency for power users                 |
| 6        | Block head lag                 | Is it keeping up with the chain?             |
| 7        | Cordoned reason                | What went wrong? (only if cordoned)          |
| 8        | Throttle rate                  | Rate limit pressure                          |
| 9        | p99 latency                    | Outlier latency                              |
| 10       | Finalization lag               | Niche — relevant for finality-sensitive apps |

Fields to **omit by default** (clutter without context): `selfRateLimitedTotal`,
`remoteRateLimitedTotal`, `misbehaviorsTotal`, `providers`.

---

## Recommendation

**Option A** is the best fit for the current state of the product:

- Most chains will have 1–3 upstreams; the inline chip approach handles this cleanly
- Chain-first scanning matches the mental model of the rest of the dashboard
- Avoids vertical bloat of Option C
- Simpler than Option B/D to implement and maintain

If eRPC grows to 5+ chains with multiple upstream types (GCP, local, third-party),
**Option D** (dense bar + expand) becomes the right call.

---

## Next steps

Once layout is confirmed, implement:

1. Replace current `NetworkRpc` body with chosen layout
2. Parse `evm:{chainId}` → chain name + show unconfigured chains as greyed rows
   (requires knowing which chains are "expected" — could come from configmap or be hardcoded)
3. Derive chain-level health from upstream health (all healthy → synced, any cordoned → error,
   any degraded → syncing)
4. Add `ChainChip` to chain row header for visual consistency with `NetworkStatus`
