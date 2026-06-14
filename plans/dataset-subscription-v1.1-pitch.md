# V1.1 — Continuous Dataset Subscriptions (escrow), a diagram pitch

**Status:** pitch only — held until the batch-settlement payout leg lands.
**v1 (shipped P1–P6):** a dataset is sold **per version** — one payment buys
exactly the version it was scoped to. That is the right primitive for a
snapshot. It is the *wrong* primitive for a **living** dataset that ships a new
version every week and wants subscribers, not one-off buyers.

---

## 1. The gap v1 leaves

```
   v1 today:  buyer ── pays atomic(v3) ──▶ token{maxVersion=3} ──▶ download v3
              new v4 ships ──▶ buyer must re-probe, re-pay atomic(v4)   ❌ friction
```

A continuously-sold dataset wants: **pay once, keep receiving** — but without
the buyer pre-funding a platform (no custody) and without the buyer paying for
versions that never ship (no "pay upfront and hope").

That is exactly the shape escrow was built for: **authorize ≠ capture.**

---

## 2. The mechanism: reserve → capture-per-epoch → void

```
            SUBSCRIBE (once)
   buyer ── signs ONE Permit2 voucher ─────────────────────────────▶ x402-escrow
            { recipient: seller,                                       (non-custodial:
              max: price_per_epoch × N,                                 holds the SIGNATURE,
              deadlines: [d1, d2, … dN] }                               not the money)
                                                                            │
   ┌───────────────────────── per new version (epoch) ─────────────────────┤
   │  seller ships v_k ─▶ controller appends signed version k              │
   │  escrow CAPTURE(epoch k): transfer price_per_epoch  buyer ─▶ seller   │  (one recipient,
   │  entitlement top-up: token.maxVersion = k                            │   one settlement,
   │  buyer's existing token now downloads v_k — no re-pay                 │   per epoch)
   └───────────────────────────────────────────────────────────────────────┤
                                                                            │
   UNSUBSCRIBE / seller stops shipping                                      │
   buyer (or timeout) ── VOID remaining deadlines ─────────────────────────▶ no further capture
            → buyer paid for delivered versions ONLY                          (funds never moved
                                                                               for undelivered epochs)
```

**The invariant that makes it fair:** money moves **only** when a new version
is actually appended to the signed log. No version, no capture. The buyer's
worst case is bounded to `price_per_epoch × (versions actually shipped)`, and
the seller cannot capture ahead of delivery.

---

## 3. What it reuses vs. what is genuinely new

```
  REUSED (already shipped):
    ├─ version log (P2) ........... defines an "epoch" = a new signed Seq
    ├─ entitlement map (P2) ....... capture's side effect = token.maxVersion += 1
    ├─ x402-escrow facilitator .... reserve / capture / void, Permit2, non-custodial
    └─ ForwardAuth + catalog ...... the priced offer, unchanged

  NEW (the v1.1 cost, stated honestly):
    └─ a per-epoch, single-recipient CAPTURE LOOP keyed by subscription id
       (Reserve a multi-deadline voucher; Capture once per shipped version to
        the one seller; Void the tail on unsubscribe)
```

This is **not** `CaptureBatch` reuse. `CaptureBatch` splits *one* held auth
across *k recipients* in *one* settlement (e.g. an evaluator panel). A
subscription is the orthogonal shape: *one recipient*, *k settlements over
time*. v1.1 therefore needs new escrow wiring — a small loop, but real code, not
"zero-cost reuse." That honesty is why it is held, not hand-waved into v1.

---

## 4. Why this is the right shape (and the moat)

```
   prepaid credits (HF/cloud):  pay upfront ─▶ platform custody ─▶ hope for value
   v1.1 escrow subscription:    authorize  ─▶ NO custody       ─▶ capture on delivery
```

- **No platform float, no custody honeypot** — the facilitator holds a
  signature, never the money; capture moves funds owner→seller directly.
- **Pay for delivered value** — the seller is paid per shipped version; the
  buyer is refunded-by-default (void) for versions never shipped.
- **Same wallet, same identity, same gate** — a subscription is just a
  longer-lived entitlement; the `entitle()` download gate is unchanged.

It is the dataset analogue of the metered-inference escrow pitch: *price the
outcome (a delivered version), not the prepayment.*

---

## 5. Dependency & ask

```
   blocked on ──▶ the batch-settlement PAYOUT leg (open; tracked in the
                  OpenRouter-direction work) — capture needs the same
                  settle-to-recipient path that leg finalizes.

   ask ───────▶ green-light v1.1 as the FIRST consumer of the payout leg once
                it lands: it is a small, well-scoped capture loop on top of the
                version log + entitlement map already shipped in P2.
```

One artifact, two uses, **and now a recurring revenue shape** — without anyone
fronting a deposit or taking custody.
