# ServiceBounty + Evaluator Market + Real Escrow — Technical Specification

> Status: implemented and live-smoked on a local k3d stack (sandbox branch).
> Scope: the demand-side bounty marketplace, the evaluator verification
> market, and the non-custodial Permit2 escrow leg, as built on obol-stack.
> Audience: engineers reviewing the design.

## 1. System overview

The obol stack already had a supply side: `obol sell` publishes x402
payment-gated services (HTTP 402 micropayments, Traefik ForwardAuth,
ERC-8004 discovery). This work adds the demand side and the trust layer:

```
 DISCOVERY   ERC-8004 identity · /api/services.json · /skill.md
                              │
   ┌──────────────────────────┼──────────────────────────┐
   ▼                          ▼                          ▼
 SUPPLY                    DEMAND                     TRUST
 ServiceOffer CRD          ServiceBounty CRD          EvaluatorEnrollment CRD
 obol sell …               obol bounty post           commit-reveal quorums
 x402 per-call             escrowed reward            reputation ladder
   └──────────────────────────┼──────────────────────────┘
                              ▼
                     MONEY RAIL (shared)
        x402-escrow facilitator: Permit2 vouchers, batch capture
```

One controller (`serviceoffer-controller`, `x402` namespace) reconciles all
three CRDs. The controller **never holds keys and never signs** — every
signature comes from an agent wallet (remote-signer) or the operator.

## 2. CRDs (group `obol.org/v1alpha1`)

### 2.1 ServiceBounty (`sb`)

Demand-side inverse of ServiceOffer.

Spec (abridged):
- `task.typeRef` — references an embedded task package (`benchmark@v1`,
  `benchlocal@v1`, `finetune@v1` staged/disabled) with typed params
  (unknown params are admission-rejected; `required` enforced).
- `reward` — amount + asset (USDC via EIP-3009 or OBOL via Permit2; asset
  carries `eip712Name`/`eip712Version`), `payment.network` chain alias.
- `eval.mode` — `required` (default) | `dangerouslySkipped`
  (CLI: `--dangerously-skip-verification`). Skipped bounties settle on the
  poster's verdict only, produce no ERC-8004 entries, and suppress
  reputation effects.
- `deliverable.report.variants[]` — A2UI report variants (see §8).

Status: `conditions[]` are machine truth; `phase` is a rollup. Key fields:
`evaluatorPanel[]{address,seat}`, `evaluations[]{address, commitHash,
score, revealedAt, withinBand, phase, seat, paid, validationTxHash,
grounded}`, `revealDeadline`, `panelSeed{source,round,randomness,signature}`,
`escalation{…}`, `bondState`, `evalBudgetState`, `escrowSpender`,
`ladderRecorded`.

Write channel: all participant input rides **annotations** (RBAC-scopeable,
no controller API surface):

| Annotation | Writer | Payload |
|---|---|---|
| `obol.org/claim` | fulfiller | claim intent (address) |
| `obol.org/submit` | fulfiller | submission ref |
| `obol.org/verdict` | poster | explicit override verdict |
| `obol.org/eval-commit-<addr>` | evaluator | commit hash (round 0) |
| `obol.org/eval-reveal-<addr>` | evaluator | `{score, salt, validationTx?}` |
| `obol.org/eval-commit-r1-<addr>` / `eval-reveal-r1-<addr>` | evaluator | escalation round |
| `obol.org/reward-voucher` | poster agent | Permit2 voucher JSON (§5) |
| `obol.org/bond-voucher` | fulfiller agent | Permit2 voucher JSON |
| `obol.org/eval-voucher`, `obol.org/eval-voucher-r1` | poster agent | Permit2 voucher JSON |

Bounty reconcile is structurally pinned (test-enforced) to **never create
HTTPRoute / Middleware / ReferenceGrant / Secret / Namespace** — a bounty
can never become ingress or broker credentials.

### 2.2 EvaluatorEnrollment (`ee`)

Per-evaluator registration: `spec{address (0x40-hex), taskTypes[],
attestation{scheme: none|secure-enclave, publicKey, signature}}`.
Controller is **read-only on spec** (no create/delete — test-pinned);
it owns only `status.records[]` (per task type):
`{taskType, tier: shadow|probation|full, shadowAgreements, probationEvals,
completed, divergences, groundedEvals, lastEvalAt, recentFulfillers[≤5]}`.

### 2.3 Task packages (embedded, versioned)

`task.yaml` per package declares params, eval policy and ladder constants:

```yaml
eval:
  defaultK: 3                  # counting quorum size
  ladder:
    shadowAgreements: 5        # in-band shadow verdicts → Probation
    probationEvals: 10         # clean paid evals → Full
    probationValueCap: "50.00" # no probation seat above this reward
    revealWindow: 10m          # commits close before any reveal opens
    nonRevealPenalty: outlier  # non-reveal graded as worst-case outlier
    decayHalfLife: 720h        # reputation half-life on inactivity
    escalationWindow: 30m      # poster funding window for round 1
    escalationEpsilon: 5       # knife-edge band; negative disables
```

## 3. Lifecycle and verdicts

```
 post ──► claim ──► submit ──► [eval market] ──► Verified | Rejected ──► Paid
  │          │                      │                  │
  │          │                      │                  ├─ Verified: capture reward → fulfiller
  │          │                      │                  │   batch-capture eval leg → panel
  │          │                      │                  │   void bond ("Returned")
  │          │                      │                  └─ Rejected: capture bond → poster
  │          │                      │                      void reward; eval leg still paid
  │          ├─ self-bond reserved (<uid>-bond)            (win-or-lose)
  │          └─ reward voucher signable (fulfiller known)
  └─ escrow Reserve(<uid>) — intent only, "AwaitingVoucher"
```

Verdict sources, in precedence order: poster override annotation
(`PosterOverride`, always explicit) > evaluator quorum (`EvaluatorQuorum`,
only when `eval.mode=required`). The quorum verdict latches: once spoken it
is never re-derived.

## 4. Evaluator market protocol

### 4.1 Commit–reveal quorum

- Commitment (address-bound, anti-copy):
  `commitHash = "0x" + hex(sha256("<score>|<salt>|<lowercase-evaluator-address>"))`
  Scores are integers 0–100. First write wins per address.
- The reveal window opens only after **k counting commits** exist
  (`revealDeadline = now + revealWindow`).
- Reveal verification recomputes the hash; mismatch → `BadReveal`.
  Non-reveal past deadline → graded as worst-case outlier (`NonReveal`).
- Quorum verdict: `median(counting reveals) >= 50` → Verified
  (`evalPassThreshold = 50`). A reveal further than **20** points from the
  median is out-of-band (`evalOutlierBand`) — divergence for ladder
  bookkeeping.

### 4.2 Panel selection

Deterministic, seeded weighted lottery over enrolled evaluators for the
task type:

- Seed source (`OBOL_BOUNTY_SEED` env):
  - `local` (default): `sha256(bountyUID)`.
  - `drand`: `sha256(bountyUID ‖ beacon.randomness)` where the beacon round
    is the first quicknet round strictly after `creationTimestamp + 30s`.
    The BLS signature (bls-unchained-g1-rfc9380, G1) is verified in-process
    against the quicknet group key; provenance
    `{source, round, randomness, signature}` is persisted to
    `status.panelSeed` for third-party re-verification. Relay failure
    requeues — **no silent local fallback** (fallback would be a
    seed-grinding lever).
- Weight: `w = max(0.1, 1 + 0.1 × (effectiveCompleted − divergences))`
  - decay: `effectiveCompleted = completed × 2^(−idle / decayHalfLife)`
    (pure read-time math; `lastEvalAt` nil → no decay; no status writes)
  - grounded bonus: `w ×= 1 + min(1, groundedEvals / max(1, completed))`
  - pair diversity: `w ×= 0.25` for repeat evaluator↔fulfiller pairs
    (`recentFulfillers`, capped 5).
- Tier gating at read time: a `full` record is treated as probation when
  `effectiveCompleted < probationEvals` and idle exceeds the half-life.
- Panel shape: **k counting seats** (Full tier) + **1 probation seat**
  (counts fully in the median, half pay, only on bounties under
  `probationValueCap`, requires k ≥ 3) + **≤ 2 shadow seats** (free,
  randomly assigned, never counted or paid, graded against the median for
  ladder credit).
- Cold start: pool smaller than k → open-door fallback (latched by the
  `PanelSelected` condition); open-door participants still earn ladder
  records, bootstrapping the pool.
- Selection is idempotent: the `PanelSelected` latch guarantees a panel is
  never re-rolled.

### 4.3 Escalation (bribery / dispute defense)

Trigger, checked after grading and before the quorum verdict
(`eval.mode=required`, single-round latch):
- dispersion: out-of-band counting reveals `≥ ⌈k/2⌉`, or
- knife-edge: `|median − 50| ≤ escalationEpsilon` (0 = unset → default 5;
  negative disables).

On trigger: fresh panel of **2k+1** (round-0 panel and fulfiller excluded;
seed = `sha256(round0seed ‖ "escalation-r1")`), all seats full-pay, funded
by the poster within `escalationWindow` via the `eval-voucher-r1`
annotation (escrow id `<uid>-eval-r1`). Funded → full commit-reveal cycle
with `-r1` annotation prefixes; the round-1 median over 2k+1 is **final**.
Unfunded past the deadline → `EscalationUnfunded`, round-0 median stands.

### 4.4 ERC-8004 grounding

Evaluators may submit `validationResponse` on-chain with their own wallets
(the CLI builds calldata; the controller never signs):

- Canonical request hash:
  `requestHash = keccak256("obol/bounty-eval/v1|<bountyUID>|<lowercase-evaluator-address>")`
- ERC-8004 v2.0.0 registries (verified on-chain, `getVersion()=="2.0.0"`):
  `validationResponse(bytes32,uint8,string,bytes32,string)` selector
  `0x3d659a96`; `giveFeedback(...)` selector `0x3c036a7e`.
  Base Sepolia ValidationRegistry: `0x8004Cb1BF31DAf7788923b405b754f57acEB4272`.
- On reveal with `validationTx`, the controller reads the registry through
  in-cluster eRPC and sets `grounded=true` iff on-chain responder ==
  evaluator and on-chain response == revealed score. Grounding **never
  blocks or changes the verdict** (chain down → ungrounded + condition
  note). Grounded evals feed the selection weight bonus.

### 4.5 Anti-griefing

Fulfiller self-bond: reserved at claim (`<uid>-bond`), voided ("Returned")
on Verified or honest timeout, captured to the poster ("Forfeited") on
Rejected — offsets the poster's burned eval budget.

## 5. Money rail — Permit2 vouchers + x402-escrow facilitator

### 5.1 Voucher (agent-signed authorization)

JSON object ferried via annotations:

```json
{
  "owner":   "0x…",          // signer (poster or fulfiller agent wallet)
  "token":   "0x…",          // asset contract
  "network": "base-sepolia", // chain alias
  "spender": "0x…",          // facilitator address (signature-bound)
  "nonce":   "…",            // uint256 decimal, deterministic (below)
  "deadline": 1760000000,    // unix; hard on-chain expiry
  "recipients": [{"address":"0x…","amount":"…"}],  // atomic units/seat
  "signature": "0x…"         // 65-byte EIP-712 signature
}
```

- EIP-712: Uniswap **Permit2 SignatureTransfer `PermitBatchTransferFrom`**.
  Domain `{name:"Permit2", chainId, verifyingContract:
  0x000000000022D473030F116dDEE9F6B43aC78BA3}` (no version field).
  `permitted[i] = {token, amount}` — one entry per recipient seat.
- Deterministic nonce: `uint256(keccak256(uid + "|" + leg))` with legs
  `reward|bond|eval|eval-r1` — re-funding is idempotent and a consumed
  nonce is unrepeatable (Permit2 unordered nonces), so a voucher cannot be
  double-captured.
- Signing: agent remote-signer `SignTypedData` (REST) or a dev `--key`.
- Who signs when: reward → poster at claim (fulfiller known); bond →
  fulfiller at claim; eval → poster at panel selection (seat addresses
  known, probation seat at half price); eval-r1 → poster at escalation.

### 5.2 Facilitator service (`x402-escrow`)

In-cluster, ClusterIP-only (never tunnel-exposed), port 8403, distroless.

| Route | Semantics |
|---|---|
| `POST /escrow/reserve/{id}` | no voucher → `{state:"AwaitingVoucher", spender}`; with voucher → verify (recover signer == owner, spender binding, future deadline, positive amounts) → `Reserved`. Re-reserve attaches/replaces a voucher pre-capture. |
| `POST /escrow/capture/{id}` | requires Reserved+voucher. Optional `recipients[]` body (batch): must be a **subset of the voucher's declared seats with exact amounts** — omitted seats are simply unpaid. Builds `permitBatchTransferFrom` (transferDetails pair index-wise with permitted; omitted seats get `requestedAmount=0`), submits with the facilitator wallet, waits for the receipt → `{state:"Captured", txHash}`. Idempotent. |
| `POST /escrow/void/{id}` | store-only; the voucher deadline is the hard guarantee. |
| `GET /escrow/info` | `{address, networks}` — agents fetch the spender before signing. |

Auth: bearer token (constant-time compare). Settlement key from env
(`OBOL_ESCROW_KEY`) or a remote signer; RPC via in-cluster eRPC per
network. State: file-backed, atomic writes.

**Custody model**: funds move owner → recipients **directly through
Permit2** in one transaction; the facilitator pays gas and is never
custodial. Loss is bounded by signed amounts + deadline.

**Documented v1 trust residue**: Permit2 SignatureTransfer lets the
spender choose `to` on-chain, so recipient binding is enforced by
facilitator policy (stored-voucher subset rule) + namespaced RBAC on the
voucher annotations, not by the signature. Cryptographic binding requires
`permitWitnessTransferFrom` + a disperse contract (planned upgrade). A
forged/foreign voucher can never move third-party funds (signature
recovery binds the owner).

**Controller coupling**: the controller reads escrow URL/token **only from
env** (`OBOL_BOUNTY_ESCROW_URL/TOKEN`) — never from CR spec or
annotations (exfiltration guard, test-pinned). Escrow ids: `<uid>`,
`<uid>-bond`, `<uid>-eval`, `<uid>-eval-r1`. A capture refused for a
missing voucher parks as condition `EscrowAwaitingVoucher` + requeue;
`obol bounty status` prints the exact fund command to run next.

## 6. Convergence with the inference-exchange direction

The facilitator's `/escrow/*` + batch-capture routes are deliberately the
same primitive a regional inference gateway needs to batch-settle earnings
to `obol sell inference` operators (one tx, k sellers). The bounty eval
leg and gateway payouts share this workstream; sellers need zero changes.

## 7. CLI surface (additions)

```
obol bounty post <type> [--dangerously-skip-verification] [--yes] …
obol bounty fund <name> (--key|--signer-url) [--spender 0x…]   # reward voucher
obol bounty claim … [--bond-key|--bond-signer-url]              # bond voucher
obol bounty eval enroll|pool                                    # ladder state
obol bounty eval fund <name>                                    # eval / eval-r1 voucher
obol bounty eval commit  --address --score --salt
obol bounty eval reveal  --address --score --salt [--validation-tx]
obol bounty eval calldata [--bounty --address | --request-hash] # ERC-8004, own wallet
obol bounty feedback <name>                                     # giveFeedback calldata
obol bounty status                                              # seed/panel/escalation/grounding/escrow
```

## 8. Reports (A2UI v1.0)

Task packages ship `deliverable.report.variants[]`:
`{kind: declarative|mcp-app, surface, catalogId}` — declarative variants
target the A2UI v1.0 basic catalog (schema-validated against the spec
repo); `mcp-app` variants are self-contained HTML served as a `custom`
node (`url_encoded:` content) — double-iframe isolation is entirely the
client's job. A free `bounty_report` MCP tool renders reports with
client-preference catalog negotiation (first supported variant wins) and
path-traversal guards.

## 9. Security invariants (test-pinned)

1. Controller never signs; holds no key material.
2. Escrow endpoint config from controller env only — never spec/annotations.
3. Bounty reconcile creates no HTTPRoute/Middleware/ReferenceGrant/Secret/
   Namespace (structural source scan across all bounty reconcile files).
4. Controller read-only on EvaluatorEnrollment spec; no create/delete.
5. Agent bounty/enrollment RBAC is namespaced.
6. CRD ↔ Go bidirectional parity test (walks every json tag against the
   hand-written CRD schema; has caught real silent-pruning bugs).
7. Voucher capture ≤ signed amounts, to declared recipients only.
8. drand mode has no silent local fallback (no seed-grinding path).
9. `x402-escrow` is ClusterIP-internal; frontend/eRPC hostname
   restrictions untouched.

## 10. Validation status

- Unit/controller: full `go build/vet/test` green, including commit-reveal,
  panel, escalation, grounding, voucher-ferry, decay, drand-fixture (BLS
  verify passes on a recorded beacon, fails on a flipped bit), Permit2
  golden calldata, and the parity/RBAC/structural pins.
- Live cluster smokes: eval-market quorum pass/reject; full panel mode
  (3 full + 1 shadow, outsider gated, median excludes shadow, eval budget
  batch-captured, shadow unpaid, ladder records written, validation-tx
  provenance).
- Money-rail compatibility: flow-12 (OBOL Permit2 sell→buy→settle through
  the x402-buyer sidecar) passes against the upstream-sync x402-rs
  facilitator build (v1.5.6 overlay): 3 settlements `status=0x1`, exact
  buyer/seller balance deltas, ~113k avg gas per settlement.

## 11. Known gaps / next steps

- Disperse contract + `permitWitnessTransferFrom` for signature-bound
  recipients.
- Live Base Sepolia escrow smoke with deployed OBOL.
- VRF only if drand provenance proves insufficient cross-party.
- On-chain `giveFeedback` submission flow (calldata exists today).
- Frontend A2UI rendering (separate repo).
- x402-rs fork release (v1.5.6 overlay) push + image repin.
