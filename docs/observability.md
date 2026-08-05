# Observability architecture

Operator-facing reference for how Obol Stack records, queries, and reasons about
payment-flow telemetry. Read this before adding a new metric, a new recording
rule, or proposing "let's persist the counter to a PVC."

## TL;DR

- **Prometheus is for recent operational telemetry**, bounded by TSDB retention
  (currently 8d in our cluster values).
- **On-chain settlement TXs are the canonical record for lifetime financial
  state.** Every settled x402 payment leaves an immutable on-chain trace via
  `X-PAYMENT-RESPONSE` (settle tx hash, asset, amount, payer, payee).
- **Counters reset on every pod restart. That is intentional.** Prometheus
  counters are per-process by design. Use `increase()` / `rate()` at query
  time — they detect resets in the TSDB and stitch ranges back together.
- Recording rules use `<level>:<metric>:<operations>` (Prometheus convention)
  and **name the window** in the rule (`7d_by_offer`, not `lifetime_by_offer`).
- Div-by-zero guards use a small epsilon (`1e-9`), **never `1.0`**.
- CRDs stay on `v1alpha1` during active dev — the alpha promise IS "no compat",
  and we have no external operators yet.

If you find yourself asking "how do we compute lifetime revenue for offer X
since the project started," the answer is **not** a recording rule — it is a
chain indexer over settle TXs.

---

## Why counters reset (and why that's fine)

Prometheus counters are stored per-process. When a verifier pod restarts (rollout,
node drain, OOM, image bump), the in-memory counter goes back to zero. This is
**not** a bug to engineer around at write time. The Prometheus query engine
already knows about it:

- `rate(counter[5m])` and `increase(counter[5m])` perform **reset detection**:
  if the last sample is less than the previous sample inside the window, the
  engine assumes a reset and stitches the two ranges together rather than
  emitting a negative delta.
- This is the well-documented "counter reset semantics." See Robust Perception:
  *Avoiding the counter-reset undercount* — the canonical writeup of why you
  must always range-query counters rather than `sum()`'ing them raw.

The corollary is the rule that bit us in PR #530:

> Never write a recording rule of the form `sum(my_counter_total) by (...)`.
> Always write `sum(increase(my_counter_total[<window>])) by (...)`.

`sum(counter)` collapses to "whatever value the live samples currently hold,"
which means **every pod restart silently zeros the recorded series**. The
expert review caught a recording rule shipped in that exact broken form;
PR #530 swapped it to `increase()` over an explicit window.

---

## The thin-layer architecture

```
                +------------------------------+
                | x402-verifier (stateless)    |
                |   - in-memory counters       |
                |   - labels:                  |
                |     offer_namespace,         |
                |     offer_name, chain,       |
                |     asset_symbol             |
                +---------------+--------------+
                                |
                                | /metrics scrape (Prometheus)
                                v
                +------------------------------+
                | Prometheus TSDB (retention)  |
                |   - 8d rolling window        |
                |   - reset detection built in |
                +---------------+--------------+
                                |
                                | recording rules with increase()
                                v
                +------------------------------+
                | Pre-aggregated series        |
                |   x402:revenue:7d_by_offer
                |   x402:revenue:7d_by_offer_chain
                +---------------+--------------+
                                |
                                | PromQL queries
                                v
                +------------------------------+
                | Frontend / dashboards        |
                |   - reads pre-aggregated     |
                |   - cheap, scoped to window  |
                +------------------------------+


   Parallel canonical path (for lifetime financial truth):

                +------------------------------+
                | x402-buyer / facilitator     |
                +---------------+--------------+
                                |
                                | settle tx (on-chain)
                                v
                +------------------------------+
                | Base / Base Sepolia          |
                |   ERC-20 Transfer events     |
                |   X-PAYMENT-RESPONSE header  |
                |   carries settle tx hash     |
                +------------------------------+
                                |
                                | chain indexer / explorer
                                v
                +------------------------------+
                | Lifetime per-offer revenue   |
                | "since first deploy" answer  |
                +------------------------------+
```

The two paths answer **different questions**:

- Prometheus answers "what is the system doing in the last N hours/days?" with
  cheap, second-resolution queries and label-faceting.
- On-chain answers "what was every payment that ever settled for offer X?" with
  immutability and full historical depth, at the cost of being slower and
  requiring an indexer.

Mixing them is a category error. Don't try to make Prometheus answer the
lifetime question, and don't try to make the chain answer "what is the current
402-rate this minute?"

---

## When NOT to add persistence to the counter itself

Three options come up repeatedly in design discussions. We rejected all three
for the current use case:

### PVC-backed verifier state
**Why it's tempting**: counters survive restart, no `increase()` gymnastics.

**Why we rejected it**: it bolts a stateful primitive onto a stateless
component. `x402-verifier` is currently safe to scale, rollout, evict, and
re-image freely. A PVC turns every restart into a sequence-recovery problem
(double-counting on a torn write, undercounting on a crash before flush).
Prometheus already solves reset detection correctly; we'd be reimplementing
it badly and introducing a new failure mode.

### Pushgateway
**Why it's tempting**: "decouple short-lived job state from scrape."

**Why we rejected it**: Pushgateway is for batch-job final values, not for
long-running services. Using it for a live verifier inverts the ownership
model (Pushgateway becomes the source of truth, verifier becomes a writer),
loses per-pod identity, and adds a single-point-of-failure that, if it
restarts, **also** zeros the counter — without `rate()` knowing about it.

### OTel collector with `cumulativetodelta`
**Why it's tempting**: collector-side reset stitching, hand off deltas to a
downstream store.

**Why we rejected it**: it solves a problem we don't have (we're not sending
deltas to a backend that needs them), at the cost of a new infrastructure
component to operate. For a single-operator local-k3d stack, this is over-
engineering. If we ever export to an OTel-native backend, revisit.

---

## When you WOULD want persistence

The only legitimate driver is an explicit **billing or compliance requirement
to report "totals since first deploy" that exceeds Prometheus retention.**

We do not have this requirement today. If we ever do:

1. **Derive it from on-chain TXs**, not from metrics. Every paid request leaves
   an `X-PAYMENT-RESPONSE` with a settle tx hash; an indexer over those is the
   canonical answer.
2. Only fall back to a persisted counter if for some reason the chain trace is
   unavailable for the offer in question — and even then, treat the indexed
   chain data as the source of truth and the counter as a soft mirror.

The architecture review's framing was right: if you find yourself wanting
Prometheus to answer a lifetime question, you've picked the wrong tool.

---

## Verify settlement against the chain, never the sidecar snapshot

The same "chain is canonical, metrics are derived" rule applies to **live
debugging**, not just lifetime aggregates. When a paid request errors and
the buyer reports `remaining=N, spent=0`, that is **not** evidence that no
money moved — it is evidence that the sidecar's local counter, the
`PurchaseRequest.status` snapshot, and the verifier's logs all agree with each
other. The on-chain transfer event can still tell you otherwise.

**This is not theoretical.** The rc13 mainnet OBOL self-test (2026-06-09)
recorded a 0.001 OBOL on-chain debit from a request that 503'd with
`"Payment settlement failed"`, while the buyer sidecar reported `0 spent / 2
remaining` and the verifier logged `facilitator settle failed (500)`. The
failure happened in the facilitator's post-submit step — the Permit2 settle
tx had already mined. By every signal the stack gave the operator, nothing
happened. The chain disagreed.

The defenses that landed (this PR):

- **Verifier**: when `facilitatorSettle` returns non-200 with a parseable body
  that includes a `transaction` field, the tx hash is surfaced via
  `X-PAYMENT-RESPONSE` *before* the 503 is written. Without this the on-chain
  hash is invisible to the buyer. See `internal/x402/forwardauth.go` and
  `TestForwardAuth_SettleErrorPreservesTxHashInHeader`.
- **Buyer sidecar**: any error response (>= 400) with `X-PAYMENT-RESPONSE` carrying a tx hash is
  treated as "spent on-chain" — the held auth is `ConfirmSpend`-ed (not
  released back to the pool), `OnPaymentUnsettled` fires, and the operator
  warning logs the hash. See `internal/x402/buyer/proxy.go` and
  `TestProxy_UpstreamErrorWithTxHash_PersistsConsume`.
- **buy.py CLI**: `_print_paid_request_failure` decodes the settle header on
  any failed paid call (>= 400) and prints a loud `⚠️  SETTLEMENT MAY HAVE COMPLETED ON-CHAIN` warning
  with the exact balance-check command.

The defenses that are **deferred** (and worth flagging in any future debugging
session):

- Full receipt verification (verifier queries an RPC for the receipt status
  before deciding 200 vs 503). The forensic fix surfaces enough for an
  operator to reconcile manually; programmatic reconciliation is a bigger
  plumbing change.
- Settle idempotency on retry (today guarded only by Permit2 nonce reuse
  reverting on-chain — that surfaces as cascading 503s, but burns gas).
- Facilitator-side fix for the 500-after-on-chain-submit failure mode on
  mainnet OBOL specifically. That's a hosted-service bug, not in this repo.

**Debugging checklist**, when a buyer-reported "0 spent" disagrees with a
suspected debit:

```bash
RPC=https://ethereum-rpc.publicnode.com
blk=$(curl -s -X POST $RPC -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' \
  | python3 -c "import json,sys;print(int(json.load(sys.stdin)['result'],16))")
from=$(printf '0x%x' $((blk-50000)))
# Topic 0 = ERC-20 Transfer(address,address,uint256)
# Topic 2 = recipient (32-byte left-padded)
PAD=000000000000000000000000<RECIPIENT_HEX_NO_0x>
curl -s -X POST $RPC -H 'content-type: application/json' -d "{
  \"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getLogs\",\"params\":[{
    \"fromBlock\":\"$from\",\"toBlock\":\"latest\",
    \"address\":\"<TOKEN_CONTRACT>\",
    \"topics\":[\"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef\",null,\"0x$PAD\"]
  }]}"
```

A `Transfer` to the expected recipient that exists while the buyer reports
`0 spent` is the bug. Reference: rc13 mainnet OBOL tx
[`0xb5122d818a058e8bf529380260fa2584ba3d50bfc800f1e906faca34d3932307`](https://etherscan.io/tx/0xb5122d818a058e8bf529380260fa2584ba3d50bfc800f1e906faca34d3932307).

---

## External buyers (Bankr): two failure modes, one canonical ledger

Bankr chat / `bankr x402 call` against Obol agent offers confused operators
because the UI error and the on-chain outcome often disagreed.

**Root cause (2026-08-05 investigation): Bankr chat/CLI auto-pay is not
built to pay arbitrary third-party x402 endpoints at all.** Per Bankr's own
docs, auto-pay is scoped to endpoints deployed through `bankr x402 deploy`
and then approved into Bankr's own discovery index (criteria undocumented) —
[docs.bankr.bot/x402-cloud/quick-start](https://docs.bankr.bot/x402-cloud/quick-start/).
Their Apps SDK (`bankr.x402.fetch`) separately requires a pre-declared
`allowedHosts` allowlist —
[docs.bankr.bot/apps/overview](https://docs.bankr.bot/apps/overview/). Every
documented example targets `"network":"base"` (mainnet); no Base Sepolia
support was found anywhere in their docs or the `BankrBot/skills` repo. An
arbitrary seller like ours is out of scope for chat auto-pay independent of
the two wire-level failure modes below — `bankr wallet sign`
([docs.bankr.bot/cli](https://docs.bankr.bot/cli/)) is Bankr's own
officially documented manual-signing primitive, not a workaround hack, and
is the only proven path today.

When Bankr auto-pay *does* attempt a call anyway, there are **two separate
wire-level failure modes**:

| Mode | What the buyer sees | What happened | Charge? |
|---|---|---|---|
| **A — Voucher** | JSON `503` with `reason:facilitator_error`, `detail:unexpected_error` | Bankr auto-pay signed EIP-3009 with `validAfter=wall-clock now`. Base USDC requires `block.timestamp > validAfter`, so facilitator `/verify` rejects. | Usually **no** (verify never succeeded). |
| **B — Timeout / zombie** | Timeout, 504, or a generic “payment failed” after ~30s | Verify **succeeded**, agent was still running (often 30–120s to first SSE byte). Bankr’s short client timeout aborted the UI. Cloudflare often does **not** cancel the seller’s request context, so older seller builds still called `/settle` after upstream finished → BaseScan shows 0.001 USDC Transfers in a triple-retry burst. | **Yes** on older builds. |

**Seller hardenings** (HandleProxy settlement interceptor):

- Settle SSE responses only in `finalize()` after the stream completes —
  never on the first `WriteHeader(200)`.
- Skip `/settle` when `r.Context()` is canceled, when a body `Write` to the
  client fails (broken pipe — the reliable signal when cancel does not
  propagate), or when zero body bytes were written.
- Upstream proxy errors after verify return structured JSON with
  `paymentVerified:true`, `paymentSettled:false`, `retriable:false` so buyers
  do not auto-retry storms.

**Buyer guidance** (storefront Bankr prompts are type-specific):
- **http** — prefer Bankr chat auto-pay (fast enough for the ~30s window).
  Do not ask chat to run `bankr wallet sign` (it cannot).
- **agent / inference** — do not use Bankr chat/Apps auto-pay (`rpc timeout`);
  use `bankr wallet sign` with a past `validAfter` buffer and HTTP timeout ≥180s.
  After any timeout, check BaseScan before retrying.

**Still true:** chain Transfers are canonical. Seller `paymentSettled:false`
and Bankr UI copy are best-effort signals.

---

## Recording rule conventions

Naming follows the standard Prometheus pattern:

```
<level>:<metric>:<operations>
```

Examples we ship:

- `x402:revenue:7d_by_offer` — paid request count aggregated to the offer
  level over the last 7d. The frontend multiplies this by the ServiceOffer
  price table to display revenue.
- `x402:revenue:7d_by_offer_chain_asset_symbol` — same window, retaining
  chain and settlement-token facets for per-token and per-chain views.

Rules:

1. **Name the window in the rule.** `7d_by_offer` is honest; `lifetime_by_offer`
   is a lie (Prometheus has no "lifetime"). The window in the name must match
   the window in the expression.
2. **Use `increase()` over an explicit range, not `sum()` of the raw counter.**
   See PR #530 — the original rule did `sum(by offer) (charged_requests_total)`
   and silently zeroed every time the verifier pod restarted. The fixed rule is
   `sum by (offer_namespace, offer_name) (increase(obol_x402_verifier_charged_requests_total[7d]))`.
3. **Keep the window aligned with retention.** Recording a `30d` rule with 8d
   retention is a footgun: the rule sees nulls and silently produces nothing.

---

## Label conventions

Labels are the query interface. The rule of thumb:

- **Add a label if it's an attribute you'd want to facet by directly and the
  cardinality is bounded.** "Bounded" means you can write down all possible
  values: chains, asset symbols, offer names. Not user addresses, not request
  IDs, not arbitrary route paths beyond what the offer CR enumerates.
- **Don't add a label that multiplies cardinality.** Every unique combination
  of label values is a separate time series in TSDB. A label that adds 100
  values multiplies storage by 100×.

Concrete examples:

| Label             | Source           | Why include it                            |
|-------------------|------------------|-------------------------------------------|
| `offer_namespace` | offer CR meta    | Tenancy facet                             |
| `offer_name`      | offer CR meta    | Per-offer breakdown                       |
| `chain`           | offer CR payment | "Revenue by chain" is a real question     |
| `asset_symbol`    | offer CR payment | Added in PR #531 — per-token facet        |

`chain` and `asset_symbol` are both CR-derived (operator-set, bounded) and
query-meaningful ("how much USDC vs OBOL did we earn on Base last week?"). They
both belong. PR #531 added `asset_symbol` for exactly this reason — the prior
schema collapsed all asset types into one bucket.

Anti-pattern: labeling by `payer_address` or `tx_hash`. Those are unbounded and
belong on the chain trace, not on the metric.

---

## CRD versioning: stay on `v1alpha1` during active dev

For the current single-operator local-stack development, the alpha-stays-alpha
approach matches the design intent. Concretely:

- **While in active dev with no external operators, stay on `v1alpha1` and edit
  the schema in place.** The alpha promise IS "no compat" — that's the whole
  point of the version channel. Renaming a field, dropping a field, tightening
  validation: all fair game at `v1alpha1`.
- **Bump to `v1alpha2` only when** you need both versions to coexist briefly to
  validate a conversion path (which requires standing up a conversion webhook),
  or to checkpoint a major redesign you want to land alongside the old shape.
- **Graduate to `v1beta1` only when** all three are true:
  1. The schema has been stable for ~2 releases (no breaking edits).
  2. An external operator has committed to depending on it.
  3. You're committing to backwards-compat for at least one release, with
     deprecation warnings for any field you eventually want to remove.

The architecture review surfaced "should we graduate to `v1beta1`?" as a flag.
That was a "what if we ship externally" hypothetical, not an action item — and
graduating prematurely locks us into compat overhead before the schema has
earned it. The current ServiceOffer / RegistrationRequest / PurchaseRequest
CRDs all stay on `v1alpha1` until the three conditions above hold.

---

## `clamp_min(..., 1)` is an anti-pattern

Div-by-zero guards in PromQL exist because dividing by an empty counter
produces a `NaN`. The naive fix is:

```promql
# WRONG
my_success_rate
  /
ignoring(...) clamp_min(my_request_total, 1)
```

That `1` is poison under low traffic. Suppose the real request rate over 5m is
3 successful out of 4 total (75%). With `clamp_min(..., 1)` and a window in
which the counter shows `0` total requests (e.g. between scrapes), the formula
returns `3/1 = 3.0` — a 300% success rate that breaks any alert downstream of
it. More commonly: the **denominator is clamped to 1 when it should be e.g.
0.5**, and your "success rate" reports half its real value, **causing
low-traffic alerts to under-report and stay silent during exactly the windows
when traffic is degraded**.

The fix is to use an epsilon that's small enough never to dominate the real
denominator:

```promql
# RIGHT
my_success_rate
  /
ignoring(...) clamp_min(my_request_total, 1e-9)
```

`1e-9` keeps the division finite without distorting the result. Pick `1e-9` (or
smaller) as the project-wide epsilon and use it consistently. **Never `1.0`,
never `0.001`, never "a reasonable small number" — pick the smallest value that
avoids NaN and stick with it.**

This was fixed in the same review pass that produced this doc. Future
contributors: if you write a guarded division, the epsilon is `1e-9`.

---

## Cross-references

### Code

- `internal/x402/metrics.go` — verifier metric definitions
  (`obol_x402_verifier_requests_total`, `_payment_required_total`,
  `_payment_verified_total`, `_payment_failed_total`, `_charged_requests_total`,
  `_payment_failure_reasons_total`, `_upstream_failed_after_verify_total`).
  `_payment_failure_reasons_total` facets failures by a bounded `reason`
  label (`invalid_payment_header`, `no_matching_requirement`,
  `facilitator_unreachable`, `payment_invalid`, `settlement_failed`,
  `settlement_rejected` — the set enumerated in
  `internal/x402/forwardauth.go`), turning "the buy funnel leaks" into
  "this stage eats the buyers". `_upstream_failed_after_verify_total`
  counts paid requests bounced by the seller's own upstream after the
  payment verified (never settled) — a seller-side problem, not a
  payment-flow one.
- `internal/x402/verifier.go` — `prometheusLabels()` controls the verifier
  label set; this is the canonical place to add a new bounded label.
- `internal/x402/buyer/metrics.go` — buyer-side counters
  (`payment_attempts`, `payment_success_total`, `payment_failure_total`,
  `confirm_spend_failure_total`, `payment_unsettled_confirmations`) plus
  gauges (`auth_remaining`, `auth_spent`, `active_model_mappings`).
- `internal/x402/buyer/proxy.go` — `prometheusLabels()` for the buyer side.

### Infrastructure

- `internal/embed/infrastructure/values/monitoring.yaml.gotmpl` — Prometheus
  values, including retention and recording rule wiring.
- `internal/embed/infrastructure/base/templates/x402.yaml` — verifier
  Deployment, ServiceMonitor / PodMonitor.

### Pull requests that shaped this

- **PR #527** — `fix(prometheus-rules): escape PromQL $labels for Helm
  rendering`. Helm was interpreting `$labels` as a Helm template variable and
  blanking it; the fix is to escape so the literal `$labels` reaches the
  Prometheus rule engine.
- **PR #530** — `fix(prometheus-rules): use increase() for the per-offer
  revenue rule`. The original rule did `sum(counter)`, which silently zeroed
  on verifier restart. Now uses `sum(increase(counter[7d]))` per the rules
  above.
- **PR #531** — `feat(x402-metrics): add asset_symbol label for per-token
  queries`. Unlocks "USDC vs OBOL revenue by chain" without needing a
  downstream join.

### Reports

- The OBOL parity integration test reports (metric audits behind PRs #527 /
  #530 / #531) lived in `plans/release-smoke-hardening-*.md` and
  `plans/post-490-integration-*.md`; retrieve them from git history.

---

## Quick checklist for the next change

Before opening a PR that touches metrics:

- [ ] New label is bounded and CR-derived (or otherwise enumerable).
- [ ] No label that could grow unbounded (payer address, tx hash, free-form
      path beyond CR enumeration).
- [ ] New recording rule uses `increase()` over an explicit window.
- [ ] Window in the rule name matches window in the expression
      (no `lifetime_*`).
- [ ] Window is within Prometheus retention.
- [ ] Any guarded division uses `1e-9` as the clamp floor.
- [ ] If the new metric tries to answer a "lifetime" question, you've stopped
      and reconsidered using on-chain data instead.
