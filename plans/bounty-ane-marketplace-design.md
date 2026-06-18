# Bounties: a demand-side marketplace for AI work on a distributed ANE fleet

**Status:** Design / buildable brainstorm · **Owner:** Lead Architect · **Target:** obol-stack `obol.org/v1alpha1`

> **Naming (locked):** the CRD Kind is **`ServiceBounty`** (plural `servicebounties`, short `sb`) so it sorts beside `ServiceOffer` in `kubectl get crds` and reads as its matched pair. The CLI verb stays **`obol bounty`** — Kind ≠ verb, exactly as `obol sell` creates a `ServiceOffer`. "Bounty" remains the human/CLI/domain concept (e.g. `BountyRunner`, `BountyEscrow.sol`); only the Kubernetes resource carries the `Service…` prefix.

> ⚠️ **READ FIRST — must-fix corrections (from adversarial review, Appendix B).** The body below is the design exploration; these five corrections OVERRIDE it where they conflict:
> 1. **Payout does NOT reuse the buyer-sidecar.** `internal/x402/buyer/proxy.go` is a request-time `http.RoundTripper` that burns one voucher only when a *live* x402 upstream returns `<400` (`ConfirmSpend`, `signer.go:295`); money flows buyer→seller. A bounty needs escrow→fulfiller-on-verdict — the inverse. Honest v0 payout = a **coordinator agent** that, on `Verified`, submits one poster-pre-signed ERC-3009 voucher (`payTo`=fulfiller) to the facilitator `/settle` directly. That coordinator **is a trusted release authority** — say so; don't claim "trustless on shipped code."
> 2. **Agent RBAC is cluster-wide, not namespace-scoped.** `openclaw-monetize-write` is a ClusterRole+ClusterRoleBinding to both agent SAs. Put `servicebounties` in a **namespaced Role/RoleBinding**, or state the cluster-wide posture plainly.
> 3. **Remove `escrowRef.namespace` entirely** — force same-namespace by construction (a runtime string-compare guard is a future-refactor footgun given cluster-wide PurchaseRequest write).
> 4. **Cut the ANE/Ray worker substrate (§6) from v0.** Fulfillment is opaque: any process that emits a signed deliverable. ANE inference is real but niche (≤8B, 2–5× slower than the same Mac's GPU); ANE *training* is research PoC. See Appendix A.
> 5. **Hard invariant + test: a ServiceBounty NEVER creates an HTTPRoute/Middleware/Secret/Namespace.** The servicebounty-controller has zero route/secret creation capability; discovery rides only the existing `/skill.md` + `agent-registration.json`. Extend `internal/embed/embed_crd_test.go`.
> 6. **§5.3's stake-weighted verifier selection/slashing and the §9 v1/v2 stake/juror-committee roadmap are SUPERSEDED by §11 (evaluator market, 2026-06-10).** Post-scaffold design steer: no validator staking, no slashing — verification is an OBOL-paid evaluator market with a median-of-k quorum and a Shadow→Probation→Full reputation ladder, **on by default** with `--dangerously-skip-verification` as the explicit opt-out. Wherever the body says "stake-weighted", "slashing", "challenge bond", or "juror committee" for verification, read §11.

---

## 1. Vision

Someone on the timeline posted a public bounty — *"benchmark DeepSeek-V4-Flash on real hardware, $500"* — and a stranger ran it on their MacBook and got paid $500 USDT on Polygon. That transaction happened on Twitter and a block explorer, not on a marketplace, because no marketplace for it exists. obol-stack already ships the **seller half** of exactly this economy: a `ServiceOffer` declares "I will serve work for pay," the controller publishes an x402-gated route (`internal/serviceoffercontroller/controller.go:528-532`), and the buyer sidecar settles ERC-3009 vouchers after success (`internal/x402/buyer/`). What's missing is the **buyer-initiated half**: a way to *post demand* — "here is money, here is the work, here is how I'll know it's done." This document specifies the **ServiceBounty**: the structural inverse of a ServiceOffer that turns obol-stack into a two-sided marketplace, with a distributed Apple-silicon fleet (GPU-first, ANE where it honestly helps) as the execution substrate.

---

## 2. Core insight: the ServiceBounty is the inverse of a ServiceOffer

A ServiceOffer and a ServiceBounty are the two halves of one market, mirror-imaged on every axis:

| Axis | `ServiceOffer` (supply) | `ServiceBounty` (demand) |
|---|---|---|
| Who initiates payment | Buyer, at request time | **Poster, up front, escrowed** |
| Money direction | buyer → `payment.payTo` (the seller) | escrow → fulfiller (`payTo` filled at claim) |
| Terminal state | a route that **stays up** serving traffic (`Ready`, controller.go:528-532) | a resource that **settles and closes** (`Paid`/`Refunded`) |
| Work latency | milliseconds | minutes → hours → days |
| Acceptance signal | HTTP `<400` from upstream | a **verifier judgement on a deliverable** |
| Counterparties | 1 buyer ↔ 1 seller | 1 poster ↔ **N fulfillers** (race / split / redundant) |
| Side-effect rail | Middleware + HTTPRoute (controller.go:660/:695) | escrow `PurchaseRequest` payout (types.go:565) |
| Time-box | `DrainAt` graceful teardown (types.go:142) | `deadline` → expiry → refund |
| Sibling CR | `RegistrationRequest` (on-chain side effect, controller.go:802) | `PurchaseRequest` (escrow side effect, types.go:536) |
| Trust vocab | `registration.supportedTrust[]` (types.go:320) | `acceptance.verifier` (same enum, repurposed) |

**Crisp statement:** *A ServiceOffer is standing supply that converges to one live route and stays up. A ServiceBounty is time-boxed demand that converges to one paid deliverable and closes. They are the same state-machine skeleton run in opposite directions, sharing the same money rail (x402/ERC-3009), the same identity rail (ERC-8004), and the same controller plumbing — and together they are a complete marketplace.*

This symmetry is the spine of the whole design. Everywhere a perspective proposed new machinery, I checked whether the *inverse* of existing ServiceOffer machinery already does the job. Usually it does.

---

## 3. Data model — the `ServiceBounty` CRD

**The call: a new top-level `ServiceBounty` CRD in `obol.org/v1alpha1`, plus a co-located `servicebounty-controller` in the existing `serviceoffer-controller` binary, reusing `PurchaseRequest` as the escrow/payout primitive.**

### 3.1 Why a new CRD and not `type=bounty` or a generic `WorkRequest`

I considered three designs and reject two:

- **Rejected: `ServiceOffer.type=bounty`.** ServiceOffer's entire reconcile loop converges toward *keeping an HTTPRoute + Traefik Middleware live* (`reconcilePaymentGate` controller.go:660, `reconcileRoute` controller.go:695). A bounty has **no upstream Service to route to** and **inverts the meaning of `payment.payTo`** (seller-receives → escrow-pays-out). Overloading the enum forces every consumer — the verifier's `serviceoffer_source`, `/skill.md`, `obol sell list/status`, the `IsInference()/IsAgent()` helpers — to learn a type with no route, no upstream, and reversed money flow. Large blast radius for a leaky abstraction.
- **Rejected: a generic `WorkRequest`.** Too abstract to validate. The whole value of a CRD is that `kubectl`/the API server enforce a schema; a `map[string]any` task blob defers all validation to the controller and loses the per-type acceptance gates that make verification possible.
- **Chosen: a dedicated `ServiceBounty` CRD.** It mirrors the ServiceOffer shape exactly (group `obol.org`, version `v1alpha1`, status conditions, finalizers), so it inherits the codebase's conventions and RBAC posture, while keeping the demand-side lifecycle (`Open → Claimed → … → Paid`, with `Disputed/Expired/Refunded`) cleanly separate from the route-publication loop. This is the *same architectural precedent the codebase already chose* for `RegistrationRequest`: a sibling CR + a sibling reconcile pass in the same binary (controller.go:524, :802), isolating a side-effecting concern from the main loop.

The unimplemented `fine-tuning` ServiceOffer enum value (types.go:105) is the **supply-side dual** ("I sell fine-tuning capacity") of a demand-side `fine-tune` bounty. I leave that enum untouched and make `ServiceBounty.spec.task.type` include `fine-tune`, so the two meet in the middle without entangling.

### 3.2 Field table

`ServiceBounty` is **namespaced** (mirrors ServiceOffer, inherits per-namespace RBAC). Register `ServiceBountyKind = "ServiceBounty"`, `ServiceBountyResource = "servicebounties"`, `ServiceBountyGVR` next to the existing GVRs (types.go:48-67) — **plural `servicebounties`, singular `servicebounty`, shortName `sb`**, so it sorts beside `serviceoffers`.

| Field | Type | Reuses (file:line) | Notes |
|---|---|---|---|
| `spec.task.type` | enum `benchmark\|fine-tune\|serve\|http\|generic` | `ServiceOfferSpec.Type` (types.go:105) | `fine-tune` deliberately mirrors the unimplemented `fine-tuning` supply hook. |
| `spec.task.runner` | string | new | `BountyRunner` plugin id (§6), e.g. `mlx-lora`, `anemll-serve`. Opaque to the controller. |
| `spec.task.requires[]` | `[]string` | new | Capability tags the fulfiller node must advertise (e.g. `serve.ane`, `finetune.mlx`). Matched at claim. |
| `spec.task.targetModel` | `{name, runtime}` | `ServiceOfferModel` (types.go:166-174) | Reused verbatim. Runtime enum `ollama\|vllm\|tgi`. |
| `spec.task.datasetRef` | `{uri, hash, format}` | new | Content-addressed dataset pointer; hash makes verification deterministic. |
| `spec.task.harnessRef` | `{name, uri, version}` | new | Pinned eval harness / trainer image, content-addressed. |
| `spec.task.params` | `map[string]string` | `ServiceOfferSpec.Provenance` shape (types.go:129) | Free-form knobs (`epochs`, `lr`, `seqlen`, `tasks`). Keeps schema stable across task types. |
| `spec.acceptance.criteria[]` | `[]{metric, op, threshold, weight}` | new | Machine-checkable gates. The bounty's *raison d'être*. |
| `spec.acceptance.verifier` | enum `self-attested\|harness-rerun\|tee-attestation\|consensus\|poster-manual` | `supportedTrust[]` vocab (types.go:320) | How a submission is checked (§5). |
| `spec.acceptance.deliverableSchema` | `{artifacts[]{name,kind,required}, resultHashRequired}` | new | Declares a valid submission's contents. |
| `spec.reward` | `ServiceOfferPayment` | `ServiceOfferPayment` (types.go:211-247) | **Reused whole.** `method=crypto\|card` (#608), `network`, `asset`, `card{...}`, `price`. `payTo` here = escrow-return address. |
| `spec.reward.price.perRequest` | string | `PriceTable.PerRequest` (types.go:299) | The flat lump-sum reward (the "$500"). |
| `spec.reward.price.perEpoch` | string | `PriceTable.PerEpoch` (types.go:305) | Milestone/staged payout for fine-tunes. |
| `spec.escrowRef` | `{name, namespace}` → `PurchaseRequest` | `PurchaseRequest` (types.go:536) + `AgentRef` shape (types.go:159-164) | Poster's pre-signed reward auths (`PreSignedAuths[]`, types.go:565). **Confused-deputy guard: namespace MUST equal bounty namespace** (copy agent_resolver.go:46). |
| `spec.deadline` | `*metav1.Time` | `DrainAt` pattern (types.go:142) | Past deadline + no `Verified` → `Expired` → `Refunded`. Reuse requeue-at-expiry logic. |
| `spec.claimGracePeriod` | `*metav1.Duration` | `DrainGracePeriod` (types.go:148) | How long a `Claimed` fulfiller has before the claim lapses and the bounty re-opens. |
| `spec.maxFulfillers` | `int64` (default 1) | new | `1` = single-winner; `>1` = first-N-valid paid (split/redundant). |
| `spec.firstValidWins` | `bool` (default true) | new | First submission passing `acceptance` is auto-paid; controller stops accepting claims. |
| `spec.bond` | `{required, amount, token}` | new | Fulfiller anti-griefing stake (§4, §5). |
| `spec.registration` | `ServiceOfferRegistration` | `ServiceOfferRegistration` (types.go:308-333) | Optional ERC-8004 publication of the bounty as **discoverable demand**. |
| `spec.provenance` | `map[string]string` | `ServiceOfferSpec.Provenance` (types.go:129) | Why this bounty exists. |

**Status** reuses the shared `Condition` type and the `isConditionTrue` AND-rollup idiom (controller.go:528-532):

```go
type ServiceBountyStatus struct {
    ObservedGeneration int64         `json:"observedGeneration,omitempty"`
    Phase              string        `json:"phase,omitempty"`          // human rollup, like AgentStatus.Phase (types.go:718)
    Conditions         []Condition   `json:"conditions,omitempty"`     // shared type
    Claims             []ServiceBountyClaim `json:"claims,omitempty"`         // observed fulfiller bindings
    EscrowFunded       bool          `json:"escrowFunded,omitempty"`
    EscrowRemaining    string        `json:"escrowRemaining,omitempty"`// mirrors PurchaseRequest.Status.Remaining (types.go:638)
    WinningClaim       string        `json:"winningClaim,omitempty"`
    PayoutTxHash       string        `json:"payoutTxHash,omitempty"`   // like RegistrationTxHash (types.go:355)
    RefundTxHash       string        `json:"refundTxHash,omitempty"`
}
```

Claims are *observed* facts → they live in `status.claims[]`, not spec (a separate `Claim` CRD over-engineers the common single-winner case). Each `ServiceBountyClaim` binds `{fulfillerAddress, fulfillerAgentRef, claimedAt, submission{artifacts,resultHash,metrics,submittedAt}, phase, payoutRef}`.

**Lifecycle** (machine truth = condition set; `phase` is the human rollup):

```
                          ┌─────────────► Expired ──► Refunded
                          │ (deadline, no Verified)
Open ──► Claimed ──► InProgress ──► Submitted ──► Verified ──► Paid
  ▲         │                            │            │
  └─────────┘ (claimGracePeriod lapses)  └─► Rejected └─► Disputed ──► (Verified | Refunded)
```

Condition set, each mirroring an inverse ServiceOffer condition: `EscrowFunded` (inverse of `PaymentGateReady`), `Open`, `Claimed`, `Submitted`, `Verified` (the core gate), `Paid` (inverse of `Registered`). The `done` rollup:

```go
done := isConditionTrue(status,"Verified") && isConditionTrue(status,"Paid")  // mirrors controller.go:528-532
```

### 3.3 Three example YAMLs

**(a) Benchmark — the motivating $500 case**

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceBounty
metadata: { name: bench-deepseek-v4-flash, namespace: hermes-obol-agent }
spec:
  task:
    type: benchmark
    runner: bench
    requires: ["benchmark"]
    targetModel: { name: "deepseek-v4-flash", runtime: vllm }      # ServiceOfferModel, types.go:166
    harnessRef:  { name: lm-eval-harness, uri: "ghcr.io/eleutherai/lm-eval-harness", version: v0.4.3 }
    params: { tasks: "mmlu,gsm8k,humaneval", hardwareClass: "M4-Max-40c-128g", seed: "1234", dtype: fp16 }
  acceptance:
    criteria:
      - { metric: mmlu,  op: ">=", threshold: "0.0", weight: 1 }    # report-only; eval SCORE is the verifiable gold case
    verifier: consensus                                             # N-of-M re-run on committed dataset (§5)
    deliverableSchema:
      resultHashRequired: true
      artifacts:
        - { name: results.json, kind: eval-report, required: true }
        - { name: run.manifest, kind: provenance,  required: true } # signed run-manifest (§5.0)
  reward:                                                           # ServiceOfferPayment, types.go:211
    method: crypto
    network: base
    payTo: "0xPOSTER...aaaa"                                        # escrow-return addr
    asset: { symbol: USDT, decimals: 6, transferMethod: eip3009 }
    price: { perRequest: "500.00" }                                # the $500 lump sum
  escrowRef: { name: bench-deepseek-escrow, namespace: hermes-obol-agent }  # PurchaseRequest, types.go:536
  deadline: "2026-07-01T00:00:00Z"                                 # DrainAt pattern, types.go:142
  claimGracePeriod: "72h"
  maxFulfillers: 1
  firstValidWins: true
  bond: { required: true, amount: "750.00", token: USDT }          # 1.5x → lying is -EV
  registration:
    enabled: true
    name: "Benchmark DeepSeek-V4-Flash"
    skills: ["evaluation/benchmarking"]
    supportedTrust: ["reputation"]
```

**(b) Fine-tune — staged, pay-per-epoch**

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceBounty
metadata: { name: ft-qwen-coder, namespace: hermes-obol-agent }
spec:
  task:
    type: fine-tune                                                # mirrors the unimplemented supply hook, types.go:105
    runner: mlx-lora                                               # MLX GPU trainer (NOT ane-train; see §6)
    requires: ["finetune.mlx"]
    targetModel: { name: "qwen3.5:9b", runtime: vllm }
    datasetRef: { uri: "ipfs://bafy.../sql-pairs-v2.jsonl", hash: "sha256:9f2c...", format: jsonl }
    harnessRef: { name: mlx-lm.lora, uri: "ghcr.io/obol/mlx-tune", version: 0.6.0 }
    params: { epochs: "3", lr: "1e-4", loraRank: "32", seqlen: "4096" }
  acceptance:
    criteria:
      - { metric: sql_exec_acc, op: ">=", threshold: "0.78", weight: 3 }   # held-out execution accuracy
      - { metric: eval_loss,    op: "<=", threshold: "0.85", weight: 2 }
    verifier: harness-rerun                                        # held-out re-eval on committed checkpoint hash (§5)
    deliverableSchema:
      resultHashRequired: true
      artifacts:
        - { name: adapter.safetensors, kind: weights,     required: true }
        - { name: eval.json,           kind: eval-report, required: true }
  reward:
    method: crypto
    network: base-sepolia
    payTo: "0xPOSTER...bbbb"
    asset: { symbol: USDC, decimals: 6, transferMethod: eip3009 }
    price: { perEpoch: "40.00", perRequest: "120.00" }            # PerEpoch staged (types.go:305) + 120 on final pass
  escrowRef: { name: ft-qwen-escrow, namespace: hermes-obol-agent }
  deadline: "2026-06-20T00:00:00Z"
  claimGracePeriod: "168h"
  maxFulfillers: 1
  firstValidWins: false                                           # poster reviews before final release
  bond: { required: true, amount: "200.00", token: USDC }
```

**(c) Serve — keep ComfyUI up, pay with a credit card (MPP #608)**

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceBounty
metadata: { name: host-comfyui-sdxl, namespace: hermes-obol-agent }
spec:
  task:
    type: serve
    runner: comfyui
    requires: ["render"]
    targetModel: { name: "sdxl-comfyui", runtime: tgi }
    harnessRef: { name: comfyui, uri: "ghcr.io/comfyanonymous/comfyui", version: v0.3 }
    params: { workflow: "txt2img-sdxl", endpoint_kind: openai-compat, uptime_window: 30d }
  acceptance:
    criteria:
      - { metric: uptime_pct,     op: ">=", threshold: "99.5", weight: 3 }   # Prometheus SLA, automatic
      - { metric: p95_latency_ms, op: "<=", threshold: "4000", weight: 2 }
    verifier: tee-attestation                                     # enclave-bound device identity (§5)
    deliverableSchema:
      resultHashRequired: false
      artifacts:
        - { name: served-endpoint,  kind: http-endpoint, required: true }    # a live URL, monitored
        - { name: attestation.json, kind: tee-quote,     required: true }
  reward:
    method: card                                                  # MPP credit-card, #608, types.go:216
    card: { provider: stripe, account: "acct_1ObolHostExample", currency: usd }
    price: { perHour: "0.50", perRequest: "300.00" }             # PerHour for serving window (types.go:303)
  escrowRef: { name: host-comfyui-escrow, namespace: hermes-obol-agent }     # Stripe manual-capture hold (§4)
  deadline: "2026-08-01T00:00:00Z"
  claimGracePeriod: "24h"
  maxFulfillers: 3                                                # up to 3 redundant hosts paid
  firstValidWins: true
```

---

## 4. Escrow & payment

**The invariant that survives every phase: the controller never holds keys.** This is already enforced — the controller's only secret access is `secretRef` plumbing, and the agent-resolver confused-deputy guard (agent_resolver.go:46) exists precisely to stop it brokering credentials it shouldn't. All signing lives in agent wallets / remote-signer `:9000` (`internal/openclaw/wallet.go`) / Secure Enclave (`internal/enclave/enclave_darwin.go`). The controller is declarative: it watches `ServiceBounty`/`PurchaseRequest`, drives the state machine, and **observes** tx hashes it never produces. We keep this absolute.

The hard problem is the **temporal gap**. The shipped x402 path is a request-time micropayment: work completes in milliseconds, so the voucher *is* the conditional release and no custody is needed. A bounty inverts this — funds must commit *up front* and release *hours or days later* on a deliverable. That gap is what forces an escrow design.

### 4.1 The call: MVP = conditional-voucher escrow; end-state = on-chain `BountyEscrow` contract — and settlement is pluggable

I evaluated three options. Resolving the disagreement between Perspectives A and B (A reuses `PurchaseRequest` as-is; B notes a bare ERC-3009 voucher has *no native condition*):

- **Option 1 — On-chain `BountyEscrow.sol`.** Poster `lock()`s USDC/OBOL into a contract; release on a verifier EIP-712 signature; refund on timeout; native milestones + bond/slash. **Real custody, trust-minimized — but needs an audited contract per chain.** Cannot directly hold card funds.
- **Option 2 — Pre-signed conditional ERC-3009 voucher held by a coordinator agent.** Reuses `PurchaseRequest.PreSignedAuths[]` (types.go:565) verbatim. **Ships this week on existing code, zero new contracts.** *Honest limit B surfaced and A glossed:* an ERC-3009 voucher is a bearer instrument valid for its whole `validBefore` window — it has no on-chain condition. So the coordinator agent is a *de facto* custodian of the release *decision* (never of the funds-bearing key), and refund = the poster calling `cancelAuthorization(nonce)`. The poster's balance is not actually reserved. **This is escrow theater for trust-minimization — acceptable for low-value / reputation-gated pairs, fenced by a bond + value cap.**
- **Option 3 — x402-as-settlement (deliverable-as-a-sale).** The fulfiller "sells" the verified deliverable as a ServiceOffer; the poster "buys" it through the buyer sidecar. **Zero new payment code — but it provides no lock leg at all** (the fulfiller works on a promise). It is a *settlement rail*, not an escrow.

**Decision:**
- **MVP = Option 2** for the lock, **gated to low-value / reputation-vetted fulfillers**, so we ship on shipped code.
- **End-state = Option 1** for custody + native milestones + slashing, with **Option 3 as the release rail** (the payout txn can be modeled as the poster buying the deliverable), and **ERC-8004 reputation progressively replacing escrow** as trust accrues.

Critically, **the CRD surface is identical across phases** — only the *settlement adapter* swaps (`voucherAdapter` → `escrowContractAdapter` → `cardAuthAdapter`). This mirrors exactly how MPP #608 made payment *methods* pluggable (`Method: crypto|card`, types.go:216). ServiceBounty settlement becomes a fourth pluggable rail: `voucher | escrow | sale | cardAuth`. **One switch, four rails, one invariant: the controller never signs.**

### 4.2 Who signs what (no-signer invariant, made explicit)

| Action | Signer | Where |
|---|---|---|
| `lock` / voucher pre-sign | **Poster's agent wallet** | remote-signer `:9000`, poster ns (wallet.go) |
| `release` verifier signature | **Verifier agent / oracle** | its own wallet, or **Secure Enclave** for attestable trust (enclave_darwin.go) |
| voucher submission / `release()` call | **Coordinator agent** (MVP) or any submitter holding the verifier sig (contract verifies EIP-712) | agent ns; submitter is untrusted in the contract case |
| Fulfiller bond | **Fulfiller's agent wallet** | fulfiller ns remote-signer |

### 4.3 Milestone / per-epoch release

`PriceTable.PerEpoch` already exists (types.go:305, marked "Fine-tuning only") — bounties finally exercise it. **One milestone = one epoch = one release tranche.**

- **MVP:** poster pre-signs **one voucher per epoch** into the escrow `PurchaseRequest` (this is the *exact* N-auth fan-out the buyer sidecar already does, `PreSignedAuths[]`). Verifier signs off on epoch *k*'s checkpoint → coordinator submits voucher *k*. Refund = cancel the unspent epochs' nonces.
- **End-state:** `release(id, fraction)` callable per milestone; contract tracks `releasedFraction`.

For a 5-epoch fine-tune at `perEpoch: 40`, a fulfiller who completes 3/5 and then fails keeps 60% — incentive-aligned, and **poster loss is bounded to one unverified epoch**. This is the same bounded-loss discipline the buyer sidecar already enforces (`max loss = N × price`, `internal/x402/buyer/`).

### 4.4 Fee, bond, payout

- **Platform fee** — `feeBps` + `feeRecipient`. Contract: deducted atomically in `release()`. Voucher-MVP: a *second* pre-signed voucher poster→`feeRecipient` submitted alongside. Card: Stripe `application_fee_amount`.
- **Fulfiller bond** (anti-griefing) — `spec.bond`, staked before `Claimed`, returned on accepted proof or honest timeout, **slashed** to `feeRecipient`/poster on bad-faith submission. Bond ≥ verifier's marginal verification cost so spamming is never profitable. Sized so `bond × P(detected) > reward` → lying is always −EV.
- **Payout token** — reuses `ServiceOfferPayment.Method`/`Asset`: `eip3009` (USDC), `permit2` (OBOL), or `Method: card`. **Card rewards cannot fund an on-chain escrow**, so for `reward.method: card` the lock is a **Stripe manual-capture `PaymentIntent`** (`capture_method: manual`): authorize up front (lock), capture on accepted proof (release), cancel on timeout (refund) — the off-chain mirror of `cancelAuthorization`, slotting into the same rail switch as `cardAuth`.

### 4.5 Reconcile loop extension

Add a sibling `reconcileServiceBounty` pass to `cmd/serviceoffer-controller/main.go`, cloned structurally from `reconcileOffer` (controller.go:386-559) — *not* an extension of it, following the `RegistrationRequest` precedent (one binary, now three controllers):

1. **Finalizer + decode** — identical to controller.go:400-428; on delete, refund escrow (tombstone cleanup).
2. **`reconcileEscrow`** (replaces `reconcileUpstream`) — resolve `escrowRef`, apply the confused-deputy guard `escrowRef.Namespace == bounty.Namespace` copied from agent_resolver.go:46. Set `EscrowFunded` when `status.remaining > 0`.
3. **`reconcileClaims`** (replaces gate/route) — **no Middleware, no HTTPRoute**; admit claims up to `maxFulfillers`, lapse stale claims past `claimGracePeriod` using the `DrainEndsAt`/`DrainExpired` time math (types.go:498-519) + requeue-at-expiry.
4. **`reconcileVerification`** (new, the core) — run `acceptance.verifier` per submitted claim (§5).
5. **`reconcilePayout`** (replaces `reconcileRegistrationStatus`) — on `Verified`, trigger the existing buyer-sidecar settlement path against the escrow `PurchaseRequest`; for `card`, route through `internal/x402/card.go`. Set `Paid` + `PayoutTxHash`.
6. **Rollup** — `done := isConditionTrue("Verified") && isConditionTrue("Paid")`; on deadline-past-no-Verified → `Expired` → `reconcileRefund` → `Refunded`.

---

## 5. Verification — from trusted coordinator to trust-minimized consensus

**The honest premise up front: most ML deliverables are not cryptographically verifiable.** You cannot prove a tok/s number with a SNARK or that a model "is good" with a hash. What you *can* do is make cheating **expensive, attributable, and slashable** — reducing the trusted surface from "trust a person" to "trust a hash + a quorum + a bond."

### 5.0 Shared primitives (all bounty types)

- **Commit–reveal.** Fulfiller posts `H = hash(deliverable ‖ manifest ‖ salt)` *before* escrow logic runs, then reveals. Defeats "report a good number, ship a different model." Costs one 32-byte commitment.
- **Signed run-manifest.** Every deliverable ships `{datasetCommit, modelHash, harnessCommit, seed, params, hardwareClass, result, resultHash, fulfillerSig, enclaveSig?}`, signed by the fulfiller's ERC-8004 agent wallet. A bare "47 tok/s" is unfalsifiable; the manifest makes it **re-runnable**, which is the whole game.
- **Optimistic-by-default with a bonded challenge window**; pessimistic N-of-M consensus only for a low-reputation agent's first job or above a value threshold. **Reputation (ERC-8004) is the throttle** — it sets the verification tax: high-rep fulfillers clear with a short window and no upfront re-run; new agents are fully re-verified. This is a *policy dial*, not a protocol fork.

### 5.1 Per-type verification

| Type | What's verifiable | Mechanism | Honest limit |
|---|---|---|---|
| **Benchmark — eval *score*** | ✅ Strongly | Deterministic re-run on a *committed* held-out dataset (root committed at creation, rows revealed post-commit so they can't be trained on) + pinned harness + greedy/seed decode → **agreement within ε on the rounded score** (not bit-exact logits), N-of-M consensus on the rounded `resultHash`. **The flagship MVP case.** | Floating-point nondeterminism across GPUs → consensus on rounded scalar, never raw logits. |
| **Benchmark — tok/s** | ⚠️ Hardware-relative | Bind every claim to a `hardwareClass`; **reference-task calibration** (`normalized = claimed / referenceTokPerSec`) neutralizes silicon-lottery; verifier re-runs on *same-class* hardware and checks `verified ≥ claimed × (1−tol)`. Verifiable as **lower-bound + comparative ranking**, never a portable absolute. | Needs same-class verifiers in the pool. Frame as "verified ≥ claimed on declared class." |
| **Benchmark — tok/s/W** | ❌ Trust-only | — | No remote wattmeter attestation. Reputation + spot-audit only. Directly bites ANE "47–62 tok/s @ 2W" claims: throughput checkable, watts not. |
| **Fine-tune — checkpoint** | ✅ Strongly | Commit `modelHash` (no bait-and-switch), then **held-out eval re-run** on the committed checkpoint against `criteria` thresholds. Verification is **inference-only and orders of magnitude cheaper than the fine-tune itself** → optimistic verification is viable (an honest challenger can always afford to call a bluff). | **Never re-train** (non-deterministic, prohibitive). Data-contamination ("trained on test") is reputation/audit, not crypto — mitigate with a rotating never-revealed gold subset + reputation decay. |
| **Serving — SLA** | ✅ Automatic | **Reuse the deployed PodMonitor → Prometheus** (`internal/embed/infrastructure/base/templates/x402.yaml`, `llm.yaml`): liveness probes, quality canaries (known-good prompts vs committed reference), p50/p95/error-rate vs SLA. **Real paid x402 traffic doubles as liveness+quality proof** — a successful paid request *is* a datapoint. The most trust-minimizable type: machines decide payout, not people. Epoch payout via `price.perEpoch`/`perHour`; `drainAt`/`drainGracePeriod` give graceful teardown. | Graded/open-ended output quality isn't machine-verifiable → buyer dispute + reputation. Latency claims are probe-vantage-dependent → pin probe locations. |

### 5.2 TEE / Secure Enclave — what it does and does NOT buy you

The stack has **real** Secure Enclave signing (`enclave_darwin.go:330`, `Key.Sign(digest)` over P-256, hardware-bound, non-exportable, SIP-checked). Resolving a temptation across perspectives: **this is device/identity attestation, not computation attestation.**

- ✅ It proves: "this result was signed by a key that physically lives in *a* Secure Enclave and never left the chip." Strong **sybil-resistance + device-binding** (one enclave key = one device).
- ❌ It does NOT prove the *computation* (the inference, the tok/s) ran in a TEE. The ANE/GPU compute runs *outside* any TEE; there is **no macOS TEE that attests an LLM forward pass.**

**Correct use:** the enclave signature is a **reputation multiplier and challenge-window reducer** (more expensive to fake at scale because you need real distinct devices), *not* an oracle. **Don't claim TEE-verified inference.**

### 5.3 Collusion & the oracle problem

> **Superseded by §11** where this subsection leans on stake-weighting or slashing. The layered-defense framing survives; the *levers* changed (reputation ladder + random assignment + commit-reveal + escalation, not stake).

Even with re-run + consensus: who watches the watchers? Layered defenses, none sufficient alone:

1. **VRF-sampled, stake- and reputation-weighted verifier selection** *after* the result is committed — the fulfiller can't pre-select friendly verifiers.
2. **ERC-8004 reputation + stake** (`OnChainReg.AgentID`, ERC-721) — verifiers overturned by challenge/audit are slashed and lose reputation; sybils with no history carry near-zero weight.
3. **Enclave-bound verifier identity** — sybil farms now cost real hardware per identity.
4. **Disagreement → escalation, not blind majority** — escalate to a larger fresh pessimistic panel; collusion must win *every* escalation while the cost of being caught (full bond) dominates.
5. **Poster-as-oracle (MVP) → stake-weighted juror committee (v2)** for non-deterministic deliverables.

**Honest floor (said out loud to users):** for deterministic deliverables (eval scores, checkpoint held-out re-run, SLA metrics) the oracle *is the re-run* — trust-minimizable. For non-deterministic deliverables (subjective quality, "is this a good fine-tune") **there is no cryptographic oracle** — you are buying a stake-weighted, slashable human/committee judgment. Power/watt, absolute cross-hardware tok/s, and "didn't train on the test set" rest permanently on reputation + stake + audit.

---

## 6. ANE execution substrate

**The honest framing, baked into the design (not an afterthought):** the verified ANE landscape says ANE = Core ML/ANEMLL **inference** for ≤8B models at ≤4K context, ~2–5× *slower* than the same Mac's GPU but ~10× more power-efficient. **No mainstream runtime (MLX, llama.cpp, vLLM, Ollama) dispatches LLM matmul to the ANE — they all run on the Metal GPU.** ANE *training* is reverse-engineering research only (maderix/ANE, Orion — PoC at 5–9% of peak, "does NOT replace GPU training"). Nobody clusters ANEs; real Mac fleets (exo) shard across the GPU/MLX. Ray multi-node on macOS is officially unsupported (Linux-only).

So the fabric is, honestly named: **"distributed Mac *GPU* inference with optional per-node ANE for low-power small-model inference."** It advertises three capability classes and dispatches each to the substrate the research says actually works:

| ServiceBounty class | Real substrate (today) | ANE role | Pluggable future |
|---|---|---|---|
| **serve** | MLX-GPU (`vllm-metal`/`llama.cpp`) for throughput; **ANEMLL→ANE** for ≤8B/≤4K low-power | ANE *is real here* for battery-bound nodes | — |
| **fine-tune** | **MLX GPU** (`mlx-lm.lora`/`mlx-tune`) | ANE only to *eval* checkpoints | `ane-train` (Orion) behind `OBOL_EXPERIMENTAL_ANE_TRAIN=1`, default OFF |
| **benchmark** | whatever engine the bounty names | ANE as a *measured target* (report ANE tok/s honestly: ~19 TFLOPS FP16, never "38 TOPS INT8" or "16×") | — |

The fabric **never claims ANE training.** A fine-tune bounty demanding `task.requires: ["finetune.ane"]` is **rejected at claim time** unless the node opted into the experimental gate.

### 6.1 Where Ray runs — host-side, NOT in-cluster (the load-bearing decision)

**The ANE and Metal GPU are only reachable from host processes.** k3d nodes are Linux containers with no ANE, no Metal, no Core ML. Putting Ray workers in-cluster strands them on Linux with neither accelerator. obol-stack *already* solves this exact seam: the standalone inference gateway (`internal/inference/gateway.go`) and the Secure Enclave signer (`enclave_darwin.go`) **run on the Mac host**, and the cluster reaches them via `host.k3d.internal`. We reuse it.

Because **Ray multi-node on macOS is unsupported**, the **Ray head runs on Linux** (a small k3d pod) while **Mac nodes run host-side Ray worker processes** that join it — the facts' recommended "Ray-head-on-Linux + Mac workers" pattern. Single-node degenerate case needs no cluster at all: `ray.init()` local mode.

```
┌──────────────── Mac host ─────────────────┐     ┌──── k3d (Linux) ─────┐
│ obol runner  (Agent runtime=worker)        │     │  Ray HEAD pod        │
│  ├─ Ray WORKER  ───────────────────────────┼────▶│   (GCS, scheduler)   │
│  │   ├─ Ray Serve  → MLX-GPU / ANEMLL-ANE   │     │  serviceoffer- +     │
│  │   ├─ Ray Train  → MLX trainer            │     │  servicebounty-controller,  │
│  │   └─ benchmark task → harness            │     │  x402, LiteLLM,      │
│  ├─ Secure Enclave signer                   │◀────┼─ Traefik (reach host │
│  └─ obol sell inference (host gateway)      │     │  via host.k3d.internal)│
└────────────────────────────────────────────┘     └──────────────────────┘
```

Control plane (bounty board, x402 verify/settle, ERC-8004) stays **in-cluster**. Ray + accelerators stay **on the host**. The runner is the bridge.

### 6.2 Node identity — reuse the `Agent` CR (one schema change)

A Mac joins by creating **one `Agent` CR** (its identity + payout wallet). The Agent CR already gives a namespaced identity, an optional remote-signer wallet (`AgentWallet.Create` → `GenerateWallet()` in wallet.go), and a status block with `WalletAddress`/`Endpoint`/`Phase` (types.go:715-727). The **only schema change to an existing CRD** is extending the runtime enum:

```go
// AgentSpec.Runtime — types.go:686-690
// +kubebuilder:validation:Enum=hermes;worker      // ← add "worker"
Runtime string `json:"runtime,omitempty"`
```

`EffectiveRuntime()` (types.go:731) already defaults to hermes, so this is additive. A `runtime: worker` Agent is **not a Hermes pod** — it's a host-side runner process whose `Status.Endpoint` points at its Ray Serve / control port and whose wallet is the **payout address**.

**Capability is measured, not declared** (every Mac *claims* an ANE). A one-time onboarding probe writes a `WorkerProfile` into the existing ERC-8004 `Metadata`/`Provenance` maps (types.go:249-274; published via the `RegistrationRequest` path, controller.go:802) — measured per-engine tok/s, chip, RAM, cached model inventory, context ceiling, and a `capabilities[]` list (`serve.ane`, `serve.gpu`, `finetune.mlx`, `benchmark`, `render`). **`finetune.ane` is deliberately absent** unless the experimental gate is on. A node that lies (claims `serve.ane`, has no ANE) fails the deterministic benchmark gate (§5) and loses reputation. No new CRD field — capability rides the free-form metadata maps.

### 6.3 The `BountyRunner` plugin interface

New task types must drop in **without touching the controller or the `ServiceBounty` CRD**. The controller only ever sees an opaque `spec.task.runner` + `spec.task` blob, a verifiable `Proof`, and a settlement trigger. All task semantics live host-side behind a `BountyRunner` interface keyed by `spec.task.runner`:

```go
// internal/worker/runner.go  (host-side; controller never imports this)
type BountyRunner interface {
    ID() string                                  // matches ServiceBounty.spec.task.runner
    Capabilities() []Capability                  // must intersect spec.task.requires
    Validate(spec ServiceBountySpec, node WorkerProfile) error  // ANE limits enforced HERE:
        // serve.ane rejects params>8B or ctx>4K; finetune rejects ane-train unless gated
    Resolve(ctx, spec) (ResolvedInputs, error)            // pull content-addressed model/dataset
    Run(ctx, in, progress chan<- ProgressEvent) (outputs map[string]string, error)  // streams 1→n
    Prove(in, outputs, sign Signer) (Proof, error)        // controller verifies generically
}

register(MLXServeRunner{})    // serve.gpu
register(ANEMLLServeRunner{}) // serve.ane  — ≤8B / ≤4K only
register(MLXLoRARunner{})     // finetune.mlx — GPU
register(BenchmarkRunner{})   // benchmark  — doubles as the anti-lying gate (§5)
register(ComfyRenderRunner{}) // render     — wraps ComfyUI, exposed via `obol sell http`
if experimentalANETrain { register(OrionANETrainRunner{}) } // finetune.ane — gated, OFF by default
```

`Run` streams per-step `{step, loss, tok_s, etaSec}` over **the SSE flush seam that already exists** (`x402-verifier.HandleProxy` flushes per-write; `statusRecorder.Flush` must forward to the underlying `http.Flusher`, `internal/x402/verifier.go`, regression `TestVerifier_HandleProxy_StreamsSSEChunks`). This is not cosmetic: a 500-step job streams keepalive progress so it survives the Cloudflare quick-tunnel ~100s idle ceiling — the exact reason CLAUDE.md prefers `stream: true`.

Adding RL, eval, or embeddings = write one `BountyRunner`, register it, advertise its `Capabilities()`. CRD, controller, x402 settlement, ERC-8004 — untouched. This is the same polymorphism the controller already uses for `ServiceOffer.Type` (the `agent` resolver synthesizes upstream without the rest of the pipeline branching, agent_resolver.go:33). **The ANE-training gate is the whole modularity payoff:** if Orion ever graduates, flip the env flag, `finetune.ane` appears, `ane-train` bounties start matching — with no controller or CRD change. Until then it's vapor and the fabric correctly refuses to schedule it.

### 6.4 One Mac, end-to-end (no Ray cluster needed)

1. `obol stack up` (k3d + controllers + x402 + LiteLLM, as today).
2. `obol agent new worker-x --runtime worker --create-wallet` → one Agent CR + wallet.
3. Runner probes the box, publishes `WorkerProfile` via a `RegistrationRequest`.
4. `ray.init()` local mode — no head pod.
5. A `ServiceBounty` is claimed, executed against **MLX-GPU (finetune/serve) or ANEMLL-ANE (small-model serve)**, proven with the Enclave key, and either pinned (IPFS) or handed off as a `ServiceOffer` (§7). **A real ANE-served bounty is demoable on one MacBook today.**

### 6.5 What changes at N nodes

| Concern | 1 node | N nodes |
|---|---|---|
| Ray topology | `ray.init()` local | **Head on Linux**; Mac runners are host-side workers (forced: macOS multi-node unsupported) |
| Scheduling | trivial | Ray places by **custom resources = `capabilities[]`**: small-model low-power → ANE nodes, GPU jobs → Max chips |
| Claim contention | none | Controller lease (`status.claimedBy` + finalizer) — single-writer, same discipline as serviceoffer-controller |
| Fine-tune scale-out | single worker | Ray Train `num_workers>1` **on MLX-GPU** — *distributed GPU* (the real path), **not** distributed ANE |
| Serve scale-out | 1 replica | Ray Serve `num_replicas=N`, fronted by **one ServiceOffer** → Traefik load-balances `/services/<name>/*` over multiple Endpoints (ClusterIP, per the ExternalName-avoidance rule) |

**Invariant across the growth curve: Ray scales the *GPU* fabric; the ANE is always a per-node, small-model, low-power inference/eval accelerator — never a cluster-wide training pool.** That is the only design the landscape supports.

---

## 7. Three worked examples (post → claim → run → verify → pay)

**Proposed CLI surface.** Demand side: `obol bounty post|list|claim|submit|status|cancel`. Fulfiller side: `obol fulfill <bounty>` (the runner loop), `obol worker onboard` (probe + register, an alias over `agent new --runtime worker`). Reuse `obol buy inference` (#607) for consuming served bounties and `obol sell mcp` (#609) for verifier-as-a-tool.

### 7.1 Benchmark (the $500 case)

```bash
# POST — poster escrows $500 as pre-signed vouchers into a PurchaseRequest, creates the ServiceBounty
obol bounty post bench-deepseek-v4-flash --type benchmark --runner bench \
  --model deepseek-v4-flash --hardware-class M4-Max-40c-128g \
  --reward 500 --asset USDT --chain base --bond 750 \
  --verifier consensus --harness lm-eval-harness@v0.4.3 \
  --criteria "mmlu>=0,gsm8k>=0,humaneval>=0"
#   → escrow PurchaseRequest (PreSignedAuths[]) + ServiceBounty CR, phase=Open

# CLAIM — a fulfiller's runner sees the board, stakes the bond, leases the bounty
obol bounty list --requires benchmark
obol fulfill bench-deepseek-v4-flash      # sets status.claimedBy, stakes bond, phase=Claimed

# RUN — runner commits H, runs the pinned harness, signs the run-manifest
#   (BenchmarkRunner; engine reported honestly: GPU or ANE)
# SUBMIT
obol bounty submit bench-deepseek-v4-flash \
  --artifact results.json --artifact run.manifest   # phase=Submitted (committed first)

# VERIFY — N-of-M VRF-sampled same-class verifiers re-run on the committed dataset,
#   agree within ε on the rounded eval-score hash → controller sets Verified
# PAY — reconcilePayout releases one escrow voucher to the fulfiller's wallet → phase=Paid
obol bounty status bench-deepseek-v4-flash    # Verified=True, Paid=True, PayoutTxHash=0x...
```

### 7.2 Fine-tune (staged, pay-per-epoch)

```bash
obol bounty post ft-qwen-coder --type fine-tune --runner mlx-lora \
  --model qwen3.5:9b --dataset ipfs://bafy.../sql.jsonl --epochs 3 \
  --reward-per-epoch 40 --reward 120 --asset USDC --chain base-sepolia --bond 200 \
  --verifier harness-rerun --criteria "sql_exec_acc>=0.78,eval_loss<=0.85" \
  --no-first-valid-wins      # poster reviews before final release

obol fulfill ft-qwen-coder
#   runner trains on MLX GPU (NOT ANE), streams {step,loss,tok_s} via SSE through HandleProxy
#   after each epoch's checkpoint: verifier does held-out re-eval (inference-only, cheap)
#   → controller releases that epoch's $40 voucher; 3 epochs = $120 + $120 final = $240
obol bounty status ft-qwen-coder      # shows EscrowRemaining shrinking per accepted epoch
```

### 7.3 Serve (ComfyUI, card-paid, becomes a sellable endpoint)

```bash
obol bounty post host-comfyui-sdxl --type serve --runner comfyui \
  --model sdxl-comfyui --reward-per-hour 0.50 --pay-with card \
  --verifier tee-attestation --criteria "uptime_pct>=99.5,p95_latency_ms<=4000" \
  --max-fulfillers 3        # 3 redundant hosts

obol fulfill host-comfyui-sdxl
#   runner stands up Ray Serve → ComfyUI, then runs the HANDOFF that closes the loop:
obol sell inference bounty-svc-host-comfyui --model sdxl-comfyui \
  --pay-to 0xWORKER... --per-mtok 0.05 --chain base
#   → ServiceOffer → controller: ModelReady→...→Ready (controller.go:528-532)
#   → Traefik routes /services/bounty-svc-host-comfyui/* via x402 to the host listener

# VERIFY — continuous, automatic: PodMonitor→Prometheus checks uptime/p95; canary probes;
#   real paid traffic doubles as liveness. SLA met → epoch payout via Stripe capture (#608).
# CONSUME — the bounty produced a DURABLE revenue endpoint, not a one-shot:
obol buy inference http://obol.stack:8080/services/bounty-svc-host-comfyui   # #607 UX
```

The serve example is the marketplace's keystone: **a fulfilled serve bounty becomes a `ServiceOffer`**, so the bounty doesn't just pay once — it spins up standing supply that anyone can then buy. Demand creates supply. That is the two-sided market closing on itself.

---

## 8. Modularity & growth

- **New task types** drop in as a single `BountyRunner` (§6.3) advertising new `capabilities[]`. The CRD, controller, x402 rail, and ERC-8004 path never change — they operate on the opaque `spec.task.runner` + a generic `Proof`. RL, eval, embeddings, render: one file each.
- **New payment methods** drop in as a settlement adapter (`voucher|escrow|sale|cardAuth`), exactly as MPP #608 made `Method: crypto|card` pluggable. The controller never signs in any of them.
- **Composition with `obol sell mcp` (#609):** a verifier exposes "verify-this-bounty" as a **paid MCP tool over x402** (`internal/x402mcp/server.go`) — verification becomes a permissionless, per-job-compensated market, and submission/verification ride the same in-band `_meta` x402 rail.
- **Composition with card payments (#608):** rewards payable in USDC/OBOL/card via the same pluggable `cardSettleFunc` (`internal/x402/card.go`); card escrow = Stripe manual-capture.
- **Composition with buy-inference (#607):** fulfillers *discover* demand via the same `/skill.md` + `/api/services.json` feeds and `internal/buy/discover.go`; posters *consume* served-bounty endpoints with the new positional-URL `obol buy inference` UX (`internal/buy/{balance,discover,purchases}.go`).

The marketplace is therefore **closed under composition**: a ServiceBounty can be fulfilled by an Agent (`obol sell agent`), served as a ServiceOffer, consumed via buy-inference, verified via a paid MCP tool, and paid in fiat — all on machinery that already ships.

---

## 9. Phased roadmap

**Smallest shippable slice — v0 (target: the deterministic-eval happy path on one Mac):**

1. **`ServiceBounty` CRD + GVR registration** — `internal/monetizeapi/types.go` (add `ServiceBountyKind`/`ServiceBountyResource`/`ServiceBountyGVR` near :48-67; `ServiceBountySpec`/`ServiceBountyStatus`/`ServiceBountyClaim`; clone `DrainEndsAt`/`DrainExpired` as `EffectiveDeadline`/`ClaimExpired` from :498-519). Ship the CRD manifest in `internal/embed/infrastructure/base/templates/` beside `serviceoffer-crd.yaml`; extend `internal/embed/embed_crd_test.go`.
2. **`runtime: worker` enum** — one-line additive change at `types.go:686-690`.
3. **`reconcileServiceBounty` sibling pass** — `internal/serviceoffercontroller/` (new `bounty_controller.go` + `bounty_render.go`), wired into `cmd/serviceoffer-controller/main.go` as a third queue, cloned from `reconcileOffer` (controller.go:386-559). Includes the confused-deputy escrow guard (copy agent_resolver.go:46).
4. **Escrow via Option 2 (voucher)** — reuse `PurchaseRequest.PreSignedAuths[]` (types.go:565); release through the existing buyer-sidecar settlement path. **Trusted coordinator + poster-as-judge** for acceptance; single re-run for deterministic types. No consensus yet.
5. **CLI** — `obol bounty post|list|claim|submit|status` in `cmd/obol/` (new `bounty.go`); `obol worker onboard` as an alias over `agent new --runtime worker`.
6. **Single-Mac runner** — `internal/worker/` (new): `runner.go` (the `BountyRunner` interface + loop) with `BenchmarkRunner`, `MLXServeRunner`, `ANEMLLServeRunner`, `MLXLoRARunner`. `ray.init()` local mode. Reuse `enclave_darwin.go` for proof signing, `inference/gateway.go` for serve handoff.
7. **RBAC** — add `servicebounties` + `servicebounties/status` to the agent role in `internal/embed/infrastructure/base/templates/obol-agent-monetize-rbac.yaml` — as a **namespaced Role/RoleBinding** (NOT the existing cluster-wide `openclaw-monetize-write` ClusterRole; see corrections #2), beside `serviceoffers`/`purchaserequests`.

**Flagship v0 bounty types:** deterministic **eval-score benchmark** (near-trust-minimized for free) and **serving SLA** (automatic via existing PodMonitor/Prometheus). v0 is *honest about trust*: you trust the coordinator and the poster.

**v1 / v2 verification roadmap — superseded by §11.** The paragraphs below predate the no-staking steer; the canonical eval roadmap is §11.7. Kept for the non-verification items only (hardware-class binding, `BountyEscrow.sol`, MCP composition).

**v1 — verifier consensus + optimistic challenge market** *(superseded where stake-weighted)*: ~~VRF-sampled, stake-weighted N-of-M consensus~~ → median-of-k OBOL-paid evaluator quorum (§11); hardware-class binding + reference normalization for tok/s; probabilistic full-audit for fine-tunes. Coordinator becomes a dumb router.

**v2 — trust-minimized** *(superseded where stake-weighted)*: on-chain `BountyEscrow.sol` removes fund custody; enclave-bound evaluator identities for real sybil cost; ~~a stake-weighted juror committee~~ → disagreement-triggered escalation panels (§11.7); ERC-8004 reputation sets the verification tax (high-rep → short optimistic window; low-rep → mandatory pessimistic re-run); verifier-as-a-paid-MCP-tool (#609) makes the evaluator market permissionless. Flip `OBOL_EXPERIMENTAL_ANE_TRAIN=1` *only if* Orion ever leaves PoC.

**Build it Monday:** items 1–6 are the v0 cut. The only genuinely new code is the `ServiceBounty` CRD, the `reconcileServiceBounty` pass, the `cmd/obol/bounty.go` CLI, and the `internal/worker/` runner. Everything else — escrow vouchers, x402 settlement, ERC-8004 identity, the Enclave signer, the serve handoff, PodMonitor SLA — already ships.

---

## 10. Honest risks & open questions

1. **Voucher-MVP is escrow theater.** Option 2 gives the coordinator control over a *release decision* without funds custody, and the poster's balance isn't actually reserved (the voucher bounces if the poster spends elsewhere). This is deliberate, fenced by a value cap + bond + reputation gating, and retired the moment `BountyEscrow.sol` ships. **Don't market it as custody.**
2. **No cryptographic oracle for non-deterministic deliverables.** Open-ended quality, tok/s/W, absolute cross-hardware throughput, and "didn't train on the test set" rest *permanently* on reputation + stake + audit. The product must label each bounty's class so posters know what they're buying.
3. **TEE attests the signer, not the computation.** There is no macOS TEE for an LLM forward pass. Overselling "TEE-verified inference" would be a lie; the enclave is a sybil-resistance multiplier only.
4. **Same-class verifier liquidity.** Verifying an M4-Max tok/s claim needs M4-Max verifiers in the pool. Bootstrapping that pool per hardware class is a real operational cost; until it exists, tok/s bounties fall back to reputation.
5. **ANE training is vapor today.** The `finetune.ane` path is gated off for a reason (Orion: GPT-2-124M, 5–9% peak). If we ever ship it on, we must re-validate the landscape — building product on it now would be dishonest.
6. **Ray-head-on-Linux is an extra moving part.** macOS multi-node being unsupported forces a Linux head; this complicates the N-node story and the demo. Single-node `ray.init()` is the safe default; clustering is a v1+ concern.
7. **Cross-namespace escrow is a confused-deputy footgun.** The `escrowRef.Namespace == bounty.Namespace` guard (mirroring agent_resolver.go:46) is **load-bearing** — without it a poster in ns A could drain a `PurchaseRequest`'s pre-signed auths in ns B. This must ship with the CRD, not after.
8. **Open question: does the coordinator hold the verifier release key in MVP?** If yes, it's a single point of compromise for all open bounties (mitigate: per-bounty keys / threshold / move to on-chain EIP-712 release ASAP). If no, who submits the voucher after verification? Resolve before v0 ships.
9. **Open question: dispute resolution latency.** The challenge window trades payout speed against safety. What window length per type, and who funds the watcher incentive at low volume before challenger rewards self-sustain? *(Partially superseded: §11.7's escalation panel is the new dispute path; window economics still open.)*

---

## 11. Evaluator market — verification by default (canonical, 2026-06-10)

> **This section is the canonical verification design.** It supersedes §5.3's stake-weighted machinery and the §9 v1/v2 stake/slashing roadmap. Design steer after the v1 scaffold shipped: **no validator staking, no slashing — we are not rebuilding EigenLayer.** Verification is a separate OBOL-paid evaluator market anchored on ERC-8004 reputation. Full research citations: `plans/evaluator-market-research-notes.md`.

### 11.1 Trust model and money legs

The poster funds **two legs** at post time; the controller tallies but never signs:

| Leg | Token | Signed by | When | On pass | On fail |
|---|---|---|---|---|---|
| Reward | USDC | Poster at post (`upto`, recipient bound at claim via `witness.to`) | Escrowed at post | Captured → fulfiller | Voided → refund poster |
| Eval budget | OBOL | Poster's **agent** at selection time (Permit2, `witness.to` = each evaluator) | Reserved at post, signed when evaluator set is known | Batch-settled to k evaluators (one tx) | Batch-settled to k evaluators |
| Self-bond | OBOL | Fulfiller at claim (`ServiceBountySelfBond`) | Held with claim | Returned | Forfeited → offsets poster's eval spend (anti-griefing) |

The eval leg **cannot be pre-signed at post** — `witness.to` needs evaluator addresses that don't exist until selection. The poster's agent signs at selection (buy.py-process-loop style); bounded to exactly k × the per-eval price approved at post. Evaluators submit ERC-8004 `validationResponse` (0–100) with **their own agent wallets**; the controller reads and tallies. Per-eval price, k, and tolerance bands come from the task package (`task.yaml`), not per-bounty negotiation.

### 11.2 Defaults and the dangerous flag

Verification is **on by default**. `obol bounty post` shows a cost preview (reward + k × evalPrice) and confirms in a TTY. Opt-out is explicit and never silent:

- `--dangerously-skip-verification` (house precedent: `dangerouslyDisableDeviceAuth`) → `spec.eval.mode: dangerouslySkipped`, printer column `VERIFIED: no`, `Verified` condition keeps `reason=PosterOverride` — the shipped v1 scaffold's poster-as-judge path **is** the skipped path, correctly labeled, nothing retrofits.
- Skipped bounties write no ERC-8004 validation entries and their reputation feedback is suppressed/discounted — an unverified bounty cannot be farmed for reputation.
- Non-TTY: no prompt, but skipping still requires the flag.
- `--evaluators N` raises k above the package default; `--no-newcomer-seat` buys an all-veteran quorum at full price (§11.4).

### 11.3 Lifecycle with the EVALUATING phase

```
 post ─► Open ─claim─► Claimed ─submit─► Submitted
                                            │
                              ┌─────────────┴──────────────┐
                              │         EVALUATING         │
   enrolled evaluator pool ──►│ 1. SELECT  k evaluators,   │
   (ERC-8004 id + enclave     │    reputation-weighted     │
    attestation, per task     │ 2. COMMIT  hash(score ‖    │◄─ each re-runs the
    type)                     │       salt ‖ evaluatorAddr)│   private dataset
                              │ 3. REVEAL  scores + salt   │   fraction locally
                              │ 4. QUORUM  median within   │
                              │    tolerance band?         │
                              └──────┬──────────────┬──────┘
                                pass │              │ fail
                                     ▼              ▼
                              Verified=True      Rejected
                              reason=            (reward voids → refund,
                              EvaluatorQuorum     self-bond forfeits)
                                     │
                                     ▼
                              Paid: reward → fulfiller (capture)
                                    eval budget → k evaluators (batch-settlement)
```

Evaluators claim slots and post verdicts through the same annotation write-channel as fulfillers (`obol.org/eval-claim|eval-commit|eval-verdict`), validated and promoted by the controller. The eval an evaluator runs is **the same embedded task package** — they re-run and compare, they don't grade freestyle.

### 11.4 The ladder: Shadow → Probation → Full (cold-start without ossification)

Quorum = **median of k** is what makes this safe: a median is robust to one outlier by construction, so one newcomer seat cannot flip a verdict even if malicious.

```
                        ┌─────────────────────────────────────────────┐
                        │     SEAT COMPOSITION OF A k=3 QUORUM        │
   TIER 2 · FULL ──────►│  Seat 1  high-rep   full price   counts    │
   rep-weighted lottery │  Seat 2  high-rep   full price   counts    │
   TIER 1 · PROBATION ─►│  Seat 3  newcomer   ~50% price   counts    │
   reserved seat,       │          (median absorbs one outlier;      │
   value-capped bounties│           discount passed to poster)       │
   TIER 0 · SHADOW ────►│  +1..2   shadow     free         scored    │
   random assignment,   │          commit-reveal alongside, verdict  │
   can't pick bounties  │          graded against quorum median      │
                        └─────────────────────────────────────────────┘
   PROMOTION   Shadow ──(N agreements within tolerance)──► Probation
               Probation ──(M paid evals, no divergence)──► Full
   DEMOTION    divergence → rep hit → weight drops; inactivity → decay
```

- **Tier 0 Shadow (free)**: enroll = ERC-8004 identity + Secure Enclave device attestation, per task type. Randomly *assigned* to live bounties (can't park sybils where you want them); commits and reveals in the same window; verdict counts for nothing, pays nothing; graded against the quorum median → ERC-8004 feedback anchored to the settled bounty. Farming cost = real GPU time per attested device.
- **Tier 1 Probation**: one reserved seat of k, counts fully (median protects the verdict), ~50% pay with the **discount passed to the poster** — posters gain from hosting newcomer seats. Only on bounties below a value cap. Requires k≥3 whenever seated.
- **Tier 2 Full**: reputation-weighted lottery, full price, all values. v1 selection is controller-side weighted sampling (honest about local-first centralization); the selection function is the swap seam for VRF when cross-party.
- Promotion thresholds live in the task package: `eval.ladder: {shadowAgreements, probationEvals, probationValueCap}`.
- Anti-collusion: random shadow assignment, commit-reveal, **pair-diversity** (down-weight repeat evaluator↔fulfiller pairs), device-binding, rep decay. Reputation is **per task type**.

### 11.5 What adjacent protocols taught us (deep-research 2026-06-10, all claims 3-vote verified)

**The no-stake bet is vindicated.** Bittensor's stake-weighted Yuma Consensus is governed by capital, not quality: top 1% of wallets held a median ~90% of stake across 64 subnets; >half of subnets 51%-attackable by <1% of wallets; rewards correlate with stake at r≈0.80–0.95 vs r≈0.50 with consensus quality. The cold-start corollary transfers: low-participant markets are trivially capturable — benchmark the ladder against small-coalition takeover in the early phase.

**Three confirmed weaknesses:**
1. **Median-proximity free-riding** (Bittensor weight-copying, production-exploited: copiers out-earned honest validators). Commit-reveal only stops *same-round* copying — Bittensor's own docs concede that for static ground truth "nothing can prevent weight copying." For repeated bounty types, copying last round's revealed median works. Fix = **make the answer move** (rotate the private fraction), not longer concealment.
2. **p+epsilon bribery** (executed on Kleros mainnet, 2018 Doges on Trial: the bribe won rounds 1–2 of disputeID 75 and was reversed only by an appeal to a fresh 14-juror panel). Attacker pledges P+ε conditional on the dishonest outcome *losing* → everyone complies → bribe never paid → zero realized cost. The two defenses that work — slashable deposits and escalating appeals (O(N²) attacker lockup) — are both absent from our v1. Our bribery floor = per-task reward + discounted reputation-stream value; commit-reveal is *load-bearing* in a no-appeal design.
3. **Attestation-only sybil resistance has no production precedent.** Kleros is explicit that stake IS the sybil defense for random sortition. Device attestation + rep decay carry that burden alone; the free Shadow tier is the attack surface — cost-per-attested-device must exceed the value of walking a sybil to a Full seat.

**Plus**: base-rate guessing beats coherence reputation (Kleros: ~70% Reject skew → zero-effort base-rate voting looks ~88% coherent). If most bounties pass, rubber-stamp "pass" votes climb the ladder.

### 11.6 Mechanisms stolen verbatim

| Steal | From | Fixes |
|---|---|---|
| `hash(score ‖ salt ‖ evaluatorAddress)` commitments | Kleros §4.3 | Commitment copy/replay between evaluators |
| Non-reveal penalty ≥ outlier penalty | Kleros incentive system | Silent abstention as the cheap exit |
| Automated reveals (Drand time-lock) or non-reveal = worst case | Bittensor CR4 | Selective revelation |
| EV-balance tuning (no-effort evaluation must be EV-negative) | Kleros parameterization | Lazy rubber-stamping; our lever is rep decay, not voteStake |
| Difficulty-weighted rep (reward correct-minority, not easy unanimity) | derived from Kleros base-rate data | Base-rate climbing |
| Known-fail canaries in the private fraction | derived | Makes rubber-stampers detectably wrong |
| Disagreement-triggered escalation to a larger fresh panel | Kleros appeals | The only defense that beat p+epsilon in production |

### 11.7 Amendments (folded into the build plan)

**v1 (ship in the ladder slice):**
1. Commitment format = `hash(score ‖ salt ‖ evaluatorAddress)`.
2. Fixed reveal window; non-reveal = worst-case outlier (rep penalty ≥ divergence penalty). `task.yaml` ladder block gains `revealWindow` + `nonRevealPenalty`.
3. Seed `datasetCommit.privateFraction` with known-fail canaries; **rotate the private fraction per round** for repeatable bounty types.
4. Reputation gains weighted by disagreement/difficulty — unanimous easy agreement earns ~0; correct minority positions earn most.

**v2 (design before cross-party):**
5. **Disagreement-triggered escalation**: revealed scores straddling the tolerance band → re-run with a larger fresh panel (2k+1); poster pre-approves an escalation budget cap at post. Weaker than Kleros's (no loser-deposit redistribution funds it) — cost falls on the eval budget.
6. **Quantify the bribery floor in OBOL**: the discounted value of a Full seat's future income stream is our analog of Kleros's O(N²) lockup. If corrupting ⌈k/2⌉+1 medians costs less than plausible bounty values, raise k or tighten value caps.
7. Drand-style time-lock reveals when cross-party.

**Open questions carried forward:** OBOL value of a Full-tier reputation stream (unquantified); empirical adequacy of device attestation as a sybil bound (no production precedent anywhere); which task types have static-enough ground truth that commit-reveal is structurally insufficient → rotation cadence; how Truebit/Gensyn/Numerai/Chainlink handle non-deterministic verification (didn't survive this research round — re-research before a verifiable-compute task type ships).

---

*Relevant code anchors reused throughout: `internal/monetizeapi/types.go` (Type enum :105, Model :166-174, Payment/PriceTable :211-247/:299-305, card :216, registration/supportedTrust :308-333, drain time-math :498-519, PurchaseRequest/PreSignedAuths :536/:565/:638, Agent runtime/status :686-690/:715-727/:731); `internal/serviceoffercontroller/controller.go` (reconcile loop :386-559, Ready rollup :528-532, gate/route :660/:695, registration sibling :802); `internal/serviceoffercontroller/agent_resolver.go:33,:46` (polymorphic upstream + confused-deputy guard); `internal/x402/verifier.go` (SSE flush seam); `internal/x402/buyer/` (bounded settle-after-success); `internal/inference/gateway.go` (host gateway/NoPaymentGate); `internal/enclave/enclave_darwin.go:330` (real Secure Enclave Sign); `internal/openclaw/wallet.go` (payout wallet); `internal/erc8004/types.go:15-47` (OnChainReg.AgentID, SupportedTrust[]); `internal/embed/infrastructure/base/templates/{x402.yaml,llm.yaml}` (PodMonitor→Prometheus), `obol-agent-monetize-rbac.yaml` (agent RBAC); `internal/x402/card.go` (#608), `internal/x402mcp/server.go` (#609), `internal/buy/` (#607).*


---

## Appendix A — Verified ANE landscape (live research, 2026-06-09)

**Feasibility verdict.** INFERENCE on ANE: REAL but niche. Running small LLMs (<=8B) on the ANE works today via Apple Core ML (ANEMLL is the leading open pipeline, Beta 0.3.5). It is power-efficient but 2-5x SLOWER than the same Mac's GPU. No mainstream runtime (MLX, llama.cpp, vLLM, Ollama) dispatches LLM matmul to the ANE; they all run on the Metal GPU and leave the ANE idle. TRAINING on ANE: REAL only as research PoC. Two reverse-engineered projects (maderix/ANE and its successor mechramc/Orion) genuinely run forward+backward passes on the ANE via private _ANEClient/_ANECompiler APIs, but the authors themselves say it does NOT replace GPU training and runs at ~5-9% of peak. DISTRIBUTED ANE / Mac fleets: No one clusters ANEs. Real Mac clusters (exo) shard models across the GPU/MLX, not the ANE. Ray multi-node on macOS is officially UNSUPPORTED (Linux-only; macOS multi-node is 'untested', needs an at-your-own-risk env flag). A 'distributed ANE access platform' for training is NOT buildable today; a distributed GPU-based Mac inference cluster IS.


**Detailed findings.** Skeptical verdict after cross-verifying every pasted claim against primary sources (GitHub repos/issues, an arXiv preprint, Apple research, and independent benchmarks).\n\nINFERENCE on the ANE is real but a niche, low-power play: ANEMLL (Beta 0.3.5) genuinely runs <=8B LLMs (Llama/Qwen/Gemma/DeepSeek-distill) through Core ML on the ANE at ~512-4K context, but it is 2-5x SLOWER than the same Mac's GPU (e.g. ~47-62 tok/s @2W on Llama-3.2-1B vs ~204 tok/s @20W on GPU). Crucially, NO mainstream runtime uses the ANE for LLMs: MLX (issue #18 open), llama.cpp (issue #10453 is an OPEN proposal, nothing merged; discussion #336 is exploratory), vLLM (vllm-metal/vllm-mlx are real but GPU-via-MLX), Ollama and LM Studio all run on the Metal GPU and leave the ANE idle. Unsloth has no ANE support ('in the works'); 'Unsloth-MLX' was renamed mlx-tune and trains on the GPU.\n\nThe Anemll 'Flash-MoE' / anemll-flash-llama.cpp fork is real and IS a llama.cpp fork, but it streams MoE experts from SSD to the Metal GPU — not the ANE.\n\nTRAINING on the ANE is real ONLY as reverse-engineering research. maderix/ANE genuinely does forward+backward, Adam, dynamic weight patching, and zero-copy GPU<->ANE via private _ANEClient/_ANECompiler + MIL — but the author labels it a PoC at ~5-9% of peak that 'does NOT replace GPU training.' Its successor mechramc/Orion (backed by arXiv 2603.06728, Mar 2026) extends this with LoRA hot-swap and a compiler, but still on tiny GPT-2-124M/Stories-110M models. These prove the inference-only restriction is a software policy, not silicon — but ANE training is nowhere near production.\n\nDISTRIBUTED: no one clusters ANEs. Real Mac fleets (exo, ~38k stars, RDMA-over-Thunderbolt 5) shard across the GPU/MLX, not the ANE. Ray multi-node is officially Linux-only; macOS multi-node is 'untested' behind RAY_ENABLE_WINDOWS_OR_OSX_CLUSTER=1.\n\nFor a 'distributed ANE access platform': building it on ANE *training* is not feasible today. The realistic build is a distributed Mac *GPU* inference platform (exo or Ray-head-on-Linux + Mac workers, MLX/vllm-metal/llama.cpp per node), with optional per-node ANEMLL for low-power small-model inference. Numbers to distrust: the '16x speedup' has no source (fabricated), and Apple's '38 TOPS INT8' is a 2x-convention over a measured ~19 TFLOPS FP16 peak with no real INT8 compute speedup for LLM matmul.


---

## Appendix B — Adversarial red-team

### Biggest risk

The whole design rests on a load-bearing falsehood: that bounty payout "reuses the existing buyer-sidecar settlement path against the escrow PurchaseRequest" (sec 4.5 step 5, sec 9 item 4, sec 7.1 PAY). It does not, and cannot, without net-new payment code, which collapses the doc's central "build it Monday, only 4 new files" thesis. Verified against internal/x402/buyer/proxy.go and signer.go: the buyer sidecar is an http.RoundTripper (proxy.go:580-649) that consumes exactly one pre-signed ERC-3009 voucher when, and only when, a LIVE x402-gated HTTP upstream returns <400 to a per-request micropayment (proxy.go:241, 605; ConfirmSpend at signer.go:295-297 "persists a nonce as consumed after a successful paid upstream response"). There is no primitive for "hold N vouchers, then release one to a fulfiller address on a verifier verdict." The buyer-side money flow is buyer->seller-at-request-time; a bounty needs escrow->fulfiller-on-acceptance, the inverse direction the sidecar has no code path for. Worse, EffectiveBuyerNamespace() hard-returns "llm" (types.go:649-651), so every PurchaseRequest's auths are written into the single shared llm-namespace buyer pool; there is no per-poster, per-fulfiller payout isolation primitive at all. The doc's own sec 10.1 admits the voucher is "escrow theater" (no on-chain condition, poster balance not reserved, refund = poster racing to cancelAuthorization), but then still routes the actual release through machinery that physically performs the opposite operation. Net: the "v0 ships this week on shipped code" claim is the single biggest reason this fails; the only honest v0 is "trusted coordinator manually triggers an off-band transfer," exactly the centralized-custodian design the doc claims to avoid.


### Sharpest 5 fixes

1. DELETE the 'reuse buyer-sidecar settlement' claim everywhere (sec 4.5 step5, sec 7, sec 9). The buyer sidecar is a request-time micropayment RoundTripper (proxy.go:580-649, ConfirmSpend signer.go:295) that consumes a voucher on a live upstream 2xx; it has no release-on-verdict path and EffectiveBuyerNamespace() pins everything to 'llm' (types.go:649). Honest v0 = a coordinator agent that, on Verified, submits a single poster-pre-signed ERC-3009 voucher (payTo=fulfiller) by calling the facilitator /settle directly. State that this coordinator IS a trusted release authority and that sec 10.8's open question is a v0 blocker, not a v2 nicety.
2. FIX the RBAC claim, which is factually wrong and security-relevant. The doc says agent bounty RBAC is 'namespace-scoped' (sec 9 item7, sec 2). Verified: serviceoffers and purchaserequests are granted via ClusterRole 'openclaw-monetize-write' + ClusterRoleBinding to BOTH Hermes and OpenClaw SAs (obol-agent-monetize-rbac.yaml); cluster-wide create/update/delete. Adding 'bounties' there gives every agent cluster-wide write on all bounties/escrow refs in every namespace. Either move bounties to a namespaced Role/RoleBinding or state plainly the posture is cluster-wide and design the confused-deputy guard accordingly.
3. KILL the cross-namespace escrowRef before it ships. sec 10.7 calls the escrowRef.Namespace==bounty.Namespace guard 'load-bearing' but the field is {name,namespace} with namespace settable (sec 3.2). Given cluster-wide PurchaseRequest write, an attacker posts a ServiceBounty in ns A whose escrowRef points at a victim PurchaseRequest in ns B and drains its pre-signed auths. Mitigation: REMOVE the namespace field from escrowRef entirely (force same-namespace by construction) rather than relying on a runtime string compare a future refactor can drop.
4. CUT the entire ANE/Ray/worker substrate (sec 6) from v0. It is the largest, least-buildable surface (host-side Ray worker join with macOS multi-node officially unsupported, WorkerProfile probes, BountyRunner plugin registry, ane-train gating). v0 needs none of it: a bounty is fulfilled by any process that can produce a signed deliverable. Ship ServiceBounty CRD + reconcile + CLI + a single deterministic verifier (eval-score re-run OR PodMonitor SLA) and let fulfillment be opaque. Re-introduce the substrate only after money/verification rails are proven.
5. ADD a hard admission invariant that a ServiceBounty NEVER produces an HTTPRoute, Middleware, or any tunnel-exposed route, and that the servicebounty-controller has zero route/Secret/Namespace creation capability. Make it a test (extend embed_crd_test.go) so a ServiceBounty can never become unintended public ingress, and ensure registration.enabled discovery rides only the existing /skill.md + agent-registration.json surfaces, never a new public path.


### Economic / trust attacks

- **[HIGH] Escrow griefing: poster never accepts (poster-as-oracle for non-deterministic deliverables). Fulfiller burns real compute on a fine-tune, submits a valid checkpoint, poster stalls (no deadline pressure on the poster) or rejects in bad faith. With voucher-MVP the poster's funds were never reserved (sec 10.1), so the poster's downside is zero while the fulfiller ate the compute. firstValidWins=false (sec 3.3b) makes this the DEFAULT for fine-tunes.**
  - _Mitigation:_ Symmetric bonds: poster must also bond. On a deterministic-verifier pass the controller auto-releases with NO poster discretion. Reserve poster-manual strictly for explicitly-labeled subjective bounties, and on poster non-response past a review deadline auto-release to the fulfiller. Require real on-chain lock (BountyEscrow.sol) above a low value cap so the poster has skin in the game.
- **[HIGH] Reward front-running / claim-then-copy. firstValidWins=true + maxFulfillers + readable submissions: a watcher sees fulfiller A's revealed deliverable (or the payout tx in the mempool) and submits a copy to win the race. Commit-reveal (sec 5.0) is described but submissions are still readable and payout is an observable tx the coordinator submits.**
  - _Mitigation:_ Enforce commit-reveal as a HARD protocol gate: H=hash(deliverable||salt) committed before any reveal, reward binds to the address that committed first so a copied reveal pays the original committer. Encrypt the deliverable to the poster or use threshold reveal so a watcher cannot lift it.
- **[HIGH] Sybil fulfillers + verifier collusion on tok/s and 'didn't train on test' claims. New agents carry near-zero ERC-8004 reputation but a sybil farm spins up many Agent CRs cheaply (agent new --create-wallet is free), self-claims, self-verifies in a consensus pool, and splits rewards. VRF-sampled stake-weighted selection assumes a deep honest same-hardware-class verifier pool that does not exist at launch (sec 10.4).**
  - _Mitigation:_ At low pool depth fall back to a single trusted coordinator re-run (deterministic types only) and REFUSE non-deterministic bounties until verifier liquidity exists. Gate consensus weight on enclave-bound identity (one Secure Enclave key = one device) so each sybil costs real hardware. Make bond >= reward x (1/P(detected)) a validated admission constraint, not prose. Never pay tok/s/W or contamination claims on consensus; reputation+audit only.
- **[MED] Free-riding via report-vs-ship mismatch / fabricated benchmarks. sec 3.3a example criteria are all 'mmlu>=0, gsm8k>=0, humaneval>=0' (report-only) with verifier:consensus, so the acceptance gate accepts ANY score; only the optional, liquidity-dependent consensus re-run catches it.**
  - _Mitigation:_ Reject threshold==0 / report-only acceptance criteria at admission for any reward-bearing bounty (a gate that always passes is not a gate). Require resultHashRequired + a mandatory deterministic re-run (not optional consensus) for eval-score bounties. Commit the eval dataset at creation with rows revealed post-commit and make contamination-resistant gold subsets non-optional.
- **[MED] Voucher replay / double-spend across bounties. A pre-signed ERC-3009 voucher is a bearer instrument valid for its whole validBefore window (types.go PreSignedAuth.ValidBefore). The same poster voucher, or one leaked from the shared 'llm' buyer pool (EffectiveBuyerNamespace=llm, types.go:649), could be submitted against multiple bounties or replayed before the poster cancels the nonce.**
  - _Mitigation:_ One nonce per (bounty, epoch); controller tracks consumed nonces in status and refuses re-submission. Do not co-mingle bounty reward vouchers in the shared llm buyer ConfigMap pool; give bounty escrow isolated holding. Move to on-chain escrow where the contract enforces single-release per id.
- **[MED] Privilege escalation via serve-bounty handoff. The serve flow has a fulfiller run 'obol sell inference/http' to publish a /services/* route (sec 7.3). Combined with cluster-wide serviceoffers write (ClusterRole openclaw-monetize-write) and agent-factory namespace+secret create rights (hermes-agent-factory-write), a malicious bounty could induce an agent to publish routes or create namespaces/secrets it shouldn't, straining the 'only x402-gated + discovery endpoints public' tunnel invariant.**
  - _Mitigation:_ servicebounty-controller must have ZERO route/Middleware/Secret/Namespace creation capability (assert via test). Route publication stays an explicit, separately-authorized 'obol sell' action by the node operator, never auto-driven by bounty reconciliation. Keep the existing hostnames restriction and confirm bounty discovery rides only /skill.md + agent-registration.json.
- **[MED] Card-escrow auth expiry + chargeback griefing (MPP #608 path). sec 4.4 uses a Stripe manual-capture PaymentIntent as the card 'lock'. Stripe auth holds expire (~7 days); a long-deadline bounty (sec 3.3c deadline weeks out) outlives the auth, so the lock silently evaporates, and the poster can chargeback after capture, clawing back paid work with no on-chain finality.**
  - _Mitigation:_ Reject card-method bounties whose deadline exceeds the provider's max auth-hold window at admission. Treat card rewards as non-final (chargeback-exposed): require fulfiller reputation gating and/or a poster bond for card bounties, and never advertise card payout as settlement-final the way on-chain USDC is.


### Architecture conflicts with existing obol-stack invariants

1. RBAC is NOT namespace-scoped, contrary to the doc's repeated claim. serviceoffers + purchaserequests are granted via ClusterRole 'openclaw-monetize-write' bound by ClusterRoleBinding to BOTH Hermes and OpenClaw SAs (obol-agent-monetize-rbac.yaml); cluster-wide create/update/patch/delete. sec 9 item7 and sec 2 assert namespace-scoped; false today. Adding 'bounties' there is a cluster-wide grant on all bounties and their escrow refs.
2. Payout cannot reuse the existing buyer-sidecar settlement path (sec 4.5 step5, sec 7, sec 9 item4). proxy.go is an http.RoundTripper that consumes one voucher only when a LIVE x402 upstream returns <400 (proxy.go:241,605; ConfirmSpend signer.go:295). It has no release-on-verifier-verdict path. The money direction (buyer->seller at request time) is the inverse of bounty payout (escrow->fulfiller on acceptance). Structural mismatch, not a tweak.
3. Shared-namespace escrow co-mingling. EffectiveBuyerNamespace() hard-returns 'llm' (types.go:649-651). PurchaseRequest auths all land in the single llm-namespace buyer ConfigMap pool built for buyer micropayments. Routing multi-poster bounty REWARD vouchers through PurchaseRequest (sec 3.2 escrowRef, sec 4.1 Option2) puts N posters' payout instruments into one shared pool with no per-bounty isolation; a custody and replay hazard the doc does not acknowledge.
4. Cross-namespace confused-deputy reintroduced. The doc adds escrowRef:{name,namespace} with a settable namespace (sec 3.2) and relies on a runtime guard copied from agent_resolver.go:46. Unlike the agent case nothing forces it: combined with cluster-wide PurchaseRequest write, a settable namespace is a drain-victim's-auths footgun. Omit the namespace field (force same-ns by construction).
5. Controller-holds-no-keys preserved in spirit but the doc smuggles a de-facto custodian. sec 4.2 has a coordinator agent submit the voucher / hold the verifier release key (sec 10.8 leaves open whether it holds that key), making a single coordinator a release authority over all open bounties. Strains the purely-declarative posture (agent.go reads only litellm-secrets in 'llm'; agent_resolver guards credential brokering). The bounty coordinator is a new trusted signer the architecture has no slot for.
6. verifyOnly permanence vs serve-bounty. x402.yaml:35 verifyOnly:true is permanent and forwardauth.go:24-36 documents the invariant. The serve handoff to obol sell inference is fine (own in-process settle), but the doc must not let a bounty reconcile flip verifyOnly or settle at the Traefik gate, and should say so explicitly; 'reconcilePayout route through internal/x402/card.go' (sec 4.5) brushes against gate settlement.
7. The 'agent' Type / agent-resolver precedent is mis-cited as a model for opaque polymorphism (sec 6.3). The agent resolver synthesizes a CONCRETE upstream (hermes:8642) so the existing route pipeline runs; it is NOT an opaque task-blob dispatcher. A ServiceBounty has no upstream and no route; the precedent supports 'sibling reconcile pass' but not 'controller operates on an opaque task.runner it never interprets.'


### Overstated / unbuildable ANE claims to retract from the body

1. sec 5.1 / sec 3.3a present benchmark eval-score under 'verifier: consensus' as near-trust-minimized, but the consensus re-run depends on same-hardware-class verifier liquidity the doc itself admits does not exist (sec 10.4). For tok/s the doc is mostly honest (lower-bound + ranking only), but the sec 3.3a YAML still labels a tok/s-relevant benchmark 'consensus', overstating verifiability at launch.
2. sec 6.4 claims a 'real ANE-served bounty is demoable on one MacBook today' and lists ANEMLLServeRunner as a v0 deliverable (sec 9 item6). Per verified ANE facts this is real ONLY for <=8B models at <=4K context via ANEMLL Beta 0.3.5, at 2-5x SLOWER than the same Mac's GPU. Shipping an ANE runner in v0 sells a niche, slower-than-GPU path as a headline. The honest v0 substrate is MLX-GPU; ANE should be explicitly deferred, not a v0 runner.
3. The serve example (sec 3.3c, sec 5.1) uses 'verifier: tee-attestation' with a tee-quote artifact, implying TEE-verified serving. sec 5.2 correctly retracts this (attests the signer, not the computation; no macOS TEE for an LLM forward pass), but the example YAML and acceptance enum still advertise tee-attestation as a verifier for the computation/SLA. Rename the enum value (e.g. 'enclave-identity') so the CRD surface can't imply TEE-verified inference.
4. sec 6 keeps ane-train (Orion) as a gated-but-named capability and frames flipping OBOL_EXPERIMENTAL_ANE_TRAIN=1 as a near-term modularity payoff. Per facts Orion is research PoC on GPT-2-124M/Stories-110M at 5-9% of peak that does NOT replace GPU training. Naming finetune.ane in the design (even gated) overstates buildability; drop it from CRD/runner vocabulary until it leaves PoC, not parked behind an env flag.
5. Implicit in sec 6: that Ray gives a distributed Apple-silicon fabric. Verified: Ray multi-node on macOS is officially unsupported (Linux-only, RAY_ENABLE_WINDOWS_OR_OSX_CLUSTER=1 at-your-own-risk). The doc acknowledges this (sec 6.1, 6.5) but still lists Ray-head-on-Linux + Mac workers as the N-node story with more confidence than the 'untested' upstream status warrants. The honest N-node claim is GPU sharding via exo/MLX, not Ray.


### What the MVP should DROP

1. The entire ANE/Ray/worker execution substrate (sec 6): host-side Ray worker join (macOS multi-node unsupported), WorkerProfile capability probes, the BountyRunner plugin registry, ANEMLLServeRunner, MLXLoRARunner, ane-train gating. v0 fulfillment can be opaque: any process producing a signed deliverable. The single largest, least-buildable cut.
2. Verifier consensus / VRF-sampled stake-weighted selection (sec 5.3); also drop 'consensus' from the v0 acceptance enum so v0 YAMLs cannot claim it. v0 exposes ONLY deterministic single-re-run (eval-score) and automatic PodMonitor SLA.
3. Fine-tune bounties entirely from v0 (sec 3.3b). They combine the weakest verification (held-out re-eval depends on contamination assumptions + reputation), the worst griefing surface (firstValidWins=false poster-discretion default), milestone/per-epoch voucher fan-out, and the MLX trainer runner. Ship benchmark-eval + serve-SLA first.
4. Card payments / MPP #608 escrow (sec 4.4, sec 3.3c). Stripe manual-capture as 'escrow' adds auth-expiry and chargeback failure modes orthogonal to the core crypto rail. Prove the on-chain/voucher path first; add card later as a pure adapter.
5. maxFulfillers>1, redundant/split payouts, and firstValidWins racing (sec 3.2-3.3). v0 should be single-winner, single-claim only; N-fulfiller contention multiplies front-running and double-spend surface before the basic single-winner flow is proven.
6. ERC-8004 reputation-as-verification-tax dial and the paid-MCP-verifier composition (sec 5, sec 8). v0 has no reputation history to throttle on and no verifier market to meter. Defer.


---

_Generated via a 7-agent design workflow (live ANE research → 4 parallel design perspectives → synthesis → adversarial red-team) on 2026-06-09._
