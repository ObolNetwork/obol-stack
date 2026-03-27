# Autoresearch infrastructure Helm chart with verified reward distribution

## Summary

Extract the autoresearch components from PR #288 into a standalone Helm chart that adds a round-based reward engine, commit-reveal work verification, and escrow-based reward settlement using the x402 Commerce Payments Protocol. This chart depends on the reth-erc8004-indexer (or its BaseScan/8004scan fallback) for worker discovery and on the base stack for x402 payment settlement.

## Motivation

PR #288 introduced the autoresearch coordinator, worker, and publish skills as embedded agent skills. This was the right starting point for validating the flow, but the economic layer — how workers get paid fairly, how results are verified, and how bad actors are penalized — is missing.

Today's gaps:

1. **Workers self-report val_bpb** — no independent verification of claimed results
2. **Direct 1:1 payment** — buyer pays seller, no reward pool or merit-based distribution
3. **No skin-in-the-game** — workers can submit garbage with no penalty
4. **Naive worker selection** — coordinator picks first available, not best performer
5. **No anti-monopoly** — a single well-resourced worker can capture all experiments
6. **Local provenance only** — results stored on disk, no on-chain attestation

The autoresearch Helm chart addresses all six by adding infrastructure-level components that the skills can rely on.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  obol-stack cluster                                                 │
│                                                                     │
│  ┌─────────────────┐   ┌───────────────────────────────────────┐   │
│  │ base chart      │   │ autoresearch chart                    │   │
│  │                 │   │                                       │   │
│  │ traefik         │   │  ┌─────────────┐  ┌───────────────┐  │   │
│  │ x402-verifier   │   │  │ reward      │  │ challenge     │  │   │
│  │ ollama          │   │  │ engine      │  │ registry      │  │   │
│  │ litellm         │   │  │ (per-round  │  │ (configmap)   │  │   │
│  │                 │   │  │  OPOW calc) │  │               │  │   │
│  └────────┬────────┘   │  └──────┬──────┘  └───────────────┘  │   │
│           │            │         │                              │   │
│           │ x402       │  ┌──────▼──────┐  ┌───────────────┐  │   │
│           │ payments   │  │ verifier    │  │ escrow round  │  │   │
│           │            │  │ (commit-    │  │ manager       │  │   │
│           │            │  │  reveal     │  │ (authorize/   │  │   │
│           │            │  │  proofs)    │  │  capture/void)│  │   │
│           │            │  └─────────────┘  └───────────────┘  │   │
│           │            │                                       │   │
│           │            └───────────────────────────────────────┘   │
│           │                          │                              │
│  ┌────────▼────────────┐    ┌───────▼───────────┐                  │
│  │ discovery           │    │ GPU workers        │                  │
│  │ (reth-indexer /     │    │ (ServiceOffers     │                  │
│  │  BaseScan /         │    │  with x402 gate)   │                  │
│  │  8004scan)          │    │                    │                  │
│  └─────────────────────┘    └────────────────────┘                  │
└─────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

**Reward Engine** — runs per-round reward calculation:
- Reads x402 payment logs from the round period
- Reads worker qualifier data from the verifier
- Computes influence per worker using OPOW-style parity formula
- Instructs the escrow round manager on per-worker capture amounts
- Exposes leaderboard API (GET /leaderboard, GET /round/:id)

**Challenge Registry** — ConfigMap defining active challenges:
- Challenge parameters (quality metric, difficulty, tracks)
- Instance generation seeds (deterministic from block hash + nonce)
- Quality thresholds and verification parameters
- Lifecycle: challenges added/retired via values.yaml updates

**Verifier** — commit-reveal work verification:
- Workers submit Merkle root of results before knowing which will be sampled
- Verifier randomly samples N nonces for re-execution
- Workers submit Merkle proofs for sampled nonces
- Proofs verified against committed root — unverified workers receive no capture

**Escrow Round Manager** — manages per-round USDC settlement via x402 Commerce Payments:
- At round start: calls authorize() to lock the round's reward pool in escrow
- At round end: calls capture() per worker for their earned amount
- Uncaptured funds: void() returns them to the pool for the next round
- Safety net: reclaim() recovers funds if the manager fails
- See "Escrow-Based Reward Settlement" section below for full design

## Escrow-Based Reward Settlement

### Why Not a Custom Escrow Smart Contract

A bespoke `USDCEscrow.sol` for worker bond/slash would require:
- Writing, auditing, and deploying a novel Solidity contract
- Taking on security liability for custom code handling real funds
- Requiring workers to lock upfront capital (barrier to entry)
- Managing adversarial slash mechanics (workers may refuse to participate)
- Paying gas costs for deposit/withdraw/slash per worker per round

This is the highest-risk, highest-cost path. Instead, we use infrastructure that already exists and is already audited.

### The x402 Commerce Payments Protocol

Base (Coinbase) maintains the Commerce Payments Protocol — a set of audited smart contracts for escrow-based payments. These contracts are:

- **Already deployed** on Base Mainnet and Base Sepolia at deterministic addresses
- **Audited 5 times**: 3x by Coinbase Protocol Security, 2x by Spearbit
- **Battle-tested**: used by Coinbase Commerce for merchant payments
- **Zero deployment cost**: we call existing contracts, not deploy new ones

#### Deployed Contract Addresses (same on mainnet + sepolia)

| Contract | Address | Purpose |
|----------|---------|---------|
| AuthCaptureEscrow | `0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff` | Core escrow: authorize/capture/void/reclaim/refund |
| ERC3009PaymentCollector | `0x0E3dF9510de65469C4518D7843919c0b8C7A7757` | Collects USDC via ERC-3009 receiveWithAuthorization |
| Permit2PaymentCollector | `0x992476B9Ee81d52a5BdA0622C333938D0Af0aB26` | Collects USDC via Permit2 signatures |
| PreApprovalPaymentCollector | `0x1b77ABd71FCD21fbe2398AE821Aa27D1E6B94bC6` | Pre-approved payment collection |
| OperatorRefundCollector | `0x934907bffd0901b6A21e398B9C53A4A38F02fa5d` | Handles refund flows |

References:
- Contracts repo: https://github.com/base/commerce-payments
- x402 escrow scheme spec (WIP): https://github.com/coinbase/x402/pull/1425
- x402 escrow scheme issue: https://github.com/coinbase/x402/issues/839
- Reference TypeScript implementation: https://github.com/BackTrackCo/x402r-scheme (npm: @x402r/evm)

#### AuthCaptureEscrow Lifecycle Functions

```solidity
// Core data structure — all fields are client-signed, tamper-proof
struct PaymentInfo {
    address operator;              // who can capture/void/refund
    address payer;                 // reward pool wallet
    address receiver;              // worker address (set per-capture)
    address token;                 // USDC contract address
    uint120 maxAmount;             // total pool for this round
    uint48  preApprovalExpiry;     // deadline to submit authorization
    uint48  authorizationExpiry;   // deadline to capture; after this payer reclaims
    uint48  refundExpiry;          // deadline for post-capture refunds
    uint16  minFeeBps;             // fee floor (basis points)
    uint16  maxFeeBps;             // fee ceiling (basis points)
    address feeReceiver;           // platform fee recipient
    uint256 salt;                  // unique per-round nonce
}

// Round start: lock USDC in escrow
function authorize(
    PaymentInfo calldata paymentInfo,
    uint120 amount,                    // amount to lock (up to maxAmount)
    address tokenCollector,            // ERC3009PaymentCollector address
    bytes calldata collectorData       // ERC-3009 receiveWithAuthorization signature
) external nonReentrant;

// Round end: pay the RewardDistributor the total earned amount
function capture(
    PaymentInfo calldata paymentInfo,
    uint120 amount,                    // total pool to distribute
    uint16 feeBps,                     // platform fee (within signed bounds)
    address feeReceiver                // platform fee recipient
) external nonReentrant;
// IMPORTANT: receiver is FIXED in PaymentInfo. All captures from one
// authorize() go to the SAME receiver. For multi-worker distribution,
// set receiver = RewardDistributor contract, then call distribute().
// Can be called MULTIPLE TIMES (partial captures), sum <= maxAmount.
// Must be called BEFORE authorizationExpiry.

// Round end: return uncaptured funds to pool
function void(
    PaymentInfo calldata paymentInfo
) external nonReentrant;
// Returns ALL remaining escrowed funds (capturableAmount) to payer.
// Can be called AFTER partial captures — only returns what remains.
// Callable by operator at any time (no expiry gate on void itself).

// Safety net: payer self-recovers if operator disappears
function reclaim(
    PaymentInfo calldata paymentInfo
) external nonReentrant;
// Only callable AFTER authorizationExpiry has passed
// No operator needed — payer calls directly

// Post-capture correction: return captured funds
function refund(
    PaymentInfo calldata paymentInfo,
    uint120 amount,
    address refundCollector,
    bytes calldata collectorData
) external nonReentrant;
// Only within refundExpiry window
// amount <= refundableAmount (previously captured)
```

Expiry ordering enforced by contract: `preApprovalExpiry <= authorizationExpiry <= refundExpiry`

### Inverted Trust Model — Why It's Better

The Commerce Payments escrow protects the **payer** (the reward pool), not the service provider (the worker). This inversion is actually the superior design for our use case:

```
TRADITIONAL BOND MODEL (what we rejected):
  Worker posts capital → Platform slashes on fraud → Worker loses money
  Problems:
    - Workers need upfront capital (barrier to entry)
    - Slashing is adversarial (discourages participation)
    - Custom contract (security liability)
    - Gas per deposit/withdraw/slash

INVERTED ESCROW MODEL (what we use):
  Platform locks reward pool → Workers earn by doing verified work → Platform captures proportionally
  Advantages:
    - Workers need ZERO upfront capital
    - "Penalty" for bad work = not getting paid (natural, non-adversarial)
    - Uses 5x-audited contracts (zero security liability)
    - Gas only for authorize (once/round) + capture (once/worker)
    - reclaim() = platform safety net if manager crashes
    - refund() = can recover funds if fraud discovered post-capture
```

The economic effect is equivalent:
- In the bond model: bad worker loses $X deposit
- In the escrow model: bad worker earns $0 while honest workers split their share
- Both create strong incentives for honest work, but the escrow model does it without requiring workers to risk capital

### Per-Round Escrow Flow

```
ROUND START:
  │
  │  1. Reward engine calculates pool for this round
  │     pool = sum(x402_payments_last_round) * pool_percentage
  │
  │  2. Platform wallet signs ERC-3009 receiveWithAuthorization
  │     to: AuthCaptureEscrow (0xBdEA...0cff)
  │     amount: pool_amount (e.g., 100 USDC)
  │     validAfter: now
  │     validBefore: now + round_duration + grace_period
  │
  │  3. Escrow round manager calls authorize()
  │     maxAmount: pool_amount
  │     authorizationExpiry: round_end + 1 hour (grace for computation)
  │     refundExpiry: round_end + 24 hours (fraud discovery window)
  │     operator: escrow_round_manager_address
  │
  │  Funds are now LOCKED in TokenStore. Platform cannot spend them
  │  on anything else. Workers can see the commitment on-chain.
  │
  ▼
DURING ROUND:
  │
  │  Workers submit experiments via existing coordinator loop:
  │    THINK → CLAIM → RUN → VERIFY (commit-reveal proofs)
  │
  │  Verifier records qualifiers per worker per challenge.
  │  No money moves during the round.
  │
  ▼
ROUND END:
  │
  │  4. Reward engine computes per-worker earnings:
  │     influence[i] = OPOW_formula(qualifiers, parity_penalty)
  │     reward[i] = worker_pool * influence[i]
  │
  │  5. Settlement via RewardDistributor:
  │     NOTE: AuthCaptureEscrow receiver is FIXED per PaymentInfo.
  │     All captures go to the SAME address. We set receiver =
  │     RewardDistributor contract, then distribute to workers.
  │
  │     a) capture(paymentInfo, worker_total + innovator_total + operator_total,
  │               feeBps, feeReceiver)
  │        → USDC moves from escrow to RewardDistributor
  │        → Platform fee (e.g., 2%) to feeReceiver
  │     b) RewardDistributor.distribute(
  │           workers[], worker_amounts[],
  │           innovators[], innovator_amounts[],
  │           operator, operator_amount)
  │        → ERC20 transfers from distributor to each participant
  │
  │  6. Return uncaptured funds:
  │     void(paymentInfo)
  │     → Remaining USDC (pool - captured) returns to platform
  │     → void() has no expiry gate — callable any time by operator
  │     → Rolls into next round's pool
  │
  │  7. If manager crashes or bugs out:
  │     After authorizationExpiry, platform calls reclaim()
  │     → ALL remaining escrowed funds return safely
  │     → Reclaim does NOT affect already-captured amounts
  │     → Already-captured funds are in RewardDistributor
  │       (operator can call emergency withdraw if needed)
  │
  ▼
NEXT ROUND starts. Repeat from step 1.
```

### On-Chain Verification of Commitment

Workers can independently verify that the reward pool is committed before doing work:

```
On-chain check (any worker, any block explorer):
  1. Read PaymentState for the current round's paymentInfo hash
  2. hasCollectedPayment == true → funds are locked
  3. capturableAmount == expected pool size → correct amount
  4. authorizationExpiry > current block → still active

This makes the reward commitment credible and transparent without
any trust in the platform beyond the audited contract logic.
```

## x402-rs Implications

### Current State

x402-rs (v1.4.5) ships the `exact` and `upto` schemes but has **zero escrow support** today. No branch, no issue, no WIP code tracking the upstream escrow scheme.

However, x402-rs has an excellent scheme extension system designed for exactly this kind of addition.

### Scheme Extension Architecture

x402-rs uses a trait-based plugin system for payment schemes:

```
Three core traits (from x402-rs/crates/core/):

  X402SchemeId
    → identifies scheme: namespace ("eip155") + scheme name ("escrow")

  X402SchemeFacilitatorBuilder<P>
    → factory that creates scheme handlers from JSON config

  X402SchemeFacilitator
    → verify(payload, requirements) → VerifyResult
    → settle(payload, requirements) → SettleResult
    → supported(requirements) → bool

Registration:
  SchemeBlueprints registry → SchemeRegistry at runtime
  New schemes register via: blueprints.and_register(V2Eip155Escrow)

Config-driven activation:
  {"id": "v2-eip155-escrow", "chains": "eip155:*", "config": {...}}
```

Reference: `x402-rs/docs/how-to-write-a-scheme.md` provides a step-by-step guide.

### What x402-rs Needs for Escrow

Once the upstream x402 escrow scheme spec (PR #1425) stabilizes, x402-rs needs:

```
New directory:
  crates/chains/x402-chain-eip155/src/v2_eip155_escrow/
    ├── mod.rs              # scheme registration
    ├── types.rs            # PaymentInfo, PaymentState, escrow extras
    ├── facilitator.rs      # verify + settle (authorize/capture/void)
    ├── client.rs           # sign ERC-3009 for escrow
    └── server.rs           # generate PaymentRequirements with escrow fields

Key implementation points:

  verify():
    - Validate ERC-3009 signature for the authorized amount
    - Simulate authorize() call against AuthCaptureEscrow
    - Check operator, expiries, fee bounds match requirements
    - Verify payer has sufficient USDC balance + allowance

  settle():
    - Determine settlement method from requirements.extra:
      "authorize" → call operator.authorize()
      "capture"   → call operator.capture()
      "void"      → call operator.void()
    - Submit transaction, wait for receipt (60s timeout)
    - Return tx hash + network + payer address

  supported():
    - Check chain ID matches configured chains
    - Check scheme name == "escrow" (or "commerce" if renamed)

Registration (in facilitator/src/schemes.rs):
    blueprints.and_register(V2Eip155Escrow)
    // ~15 lines of boilerplate, same pattern as exact/upto
```

### Stateful vs Stateless Facilitator

Current x402-rs facilitator is **stateless** — each verify/settle is independent. The escrow scheme introduces a **session concept** (authorize → use → capture/void) that spans multiple HTTP requests.

Two approaches discussed in upstream x402 issue #839:

```
"Dumb facilitator" (recommended, aligns with x402-rs):
  - Facilitator remains stateless
  - Session tracking happens in the escrow round manager (our Helm chart)
  - Facilitator only handles individual authorize/capture/void calls
  - Each call is self-contained (paymentInfo hash identifies the session)
  - No facilitator-side state storage needed

"Smart facilitator" (rejected):
  - Facilitator tracks session lifecycle internally
  - Requires persistent state, adds complexity
  - Goes against x402 principle of minimal facilitator trust
```

The "dumb facilitator" approach means x402-rs needs **no architectural changes** to its core — the escrow scheme handler is just another verify/settle implementation, same as exact. The session lifecycle lives in our escrow round manager, not in the facilitator.

### Contribution Path

This represents a concrete contribution opportunity for the obol-stack team back to x402-rs:

```
Phase 1: Use reference impl directly
  - The BackTrackCo/x402r-scheme npm package implements the escrow
    scheme for TypeScript x402 clients
  - Our escrow round manager can call Commerce Payments contracts
    directly via ethers/viem without going through x402-rs facilitator
  - This works TODAY, no upstream dependency

Phase 2: Port to x402-rs (contribute upstream)
  - Once PR #1425 merges and the spec stabilizes
  - Implement V2Eip155Escrow scheme in x402-rs
  - Estimated effort: 2-3 days for a Rust developer
  - The "upto" scheme implementation (variable settlement amounts)
    is the closest analog and provides the template
  - File path: crates/chains/x402-chain-eip155/src/v2_eip155_escrow/
  - Submit as PR to x402-rs/x402-rs

Phase 3: Native x402 flow
  - Once x402-rs ships escrow scheme support
  - Escrow round manager uses x402 HTTP flow natively:
    402 response with scheme="escrow" → client signs → facilitator settles
  - The entire authorize/capture/void flow goes through standard
    x402 payment headers, same as current per-request payments
  - Workers see escrow commitments as standard x402 PaymentRequirements
```

### Dependency Timeline

```
TODAY:          PR #1425 is open, spec under review
                Commerce Payments contracts are deployed and audited
                Reference impl exists (BackTrackCo/x402r-scheme)
                → We can build Phase 1 NOW

WEEKS:          PR #1425 merges (spec only, no SDK code)
                → Spec is stable, safe to build Phase 2

MONTHS:         x402-rs adds escrow scheme
                → Phase 3, native flow

Our Helm chart should work in ALL three phases:
  values.yaml:
    escrow:
      mode: direct          # Phase 1: call contracts directly
      # mode: x402-rs       # Phase 3: use x402 facilitator
```

## Reward Distribution — Detailed Design

### Round Lifecycle

```
Round N starts
    │
    ├─ Escrow round manager calls authorize()
    │   └─ USDC locked in Commerce Payments escrow
    │
    ├─ Workers submit experiments (x402 paid per-request as today)
    │   └─ Each submission: precommit → benchmark → proof
    │
    ├─ Verifier checks proofs, records qualifiers
    │
    ├─ Round N ends (configurable duration, default: 1 hour)
    │
    ├─ Reward engine runs:
    │   │
    │   ├─ 1. Collect x402 payment totals from round
    │   │      (total_pool = sum of payments * pool_percentage)
    │   │
    │   ├─ 2. For each worker, compute challenge factors:
    │   │      factor[c] = worker_qualifiers[c] / total_qualifiers[c]
    │   │
    │   ├─ 3. Compute parity (anti-monopoly):
    │   │      weighted_avg = mean(factors, weights)
    │   │      variance = weighted_var(factors, weights)
    │   │      imbalance = variance / (weighted_avg * (1 - weighted_avg))
    │   │      penalty = exp(-imbalance_multiplier * imbalance)
    │   │
    │   ├─ 4. Compute influence:
    │   │      weight[i] = weighted_avg[i] * penalty[i]
    │   │      influence[i] = weight[i] / sum(weights)
    │   │
    │   ├─ 5. Split pool:
    │   │      innovator_pool = total_pool * innovator_share  (e.g., 20%)
    │   │      worker_pool    = total_pool * worker_share     (e.g., 70%)
    │   │      operator_pool  = total_pool * operator_share   (e.g., 10%)
    │   │
    │   ├─ 6. Settle via RewardDistributor:
    │   │      capture(paymentInfo, total_distributable, feeBps, feeReceiver)
    │   │      → single capture to RewardDistributor (receiver is fixed per PaymentInfo)
    │   │
    │   └─ 7. Distribute from RewardDistributor:
    │          RewardDistributor.distribute(workers[], amounts[], innovators[], ...)
    │          → ERC20 transfers to each worker (influence-weighted)
    │          → ERC20 transfers to each innovator (adoption-weighted,
    │            only for algorithms above adoption_threshold or merged)
    │          → Unadopted innovator share held in distributor for next round
    │
    ├─ Escrow round manager calls void()
    │   └─ Remaining USDC returns to pool wallet
    │
    └─ Round N+1 starts
```

### Anti-Monopoly Formula

> **Design note:** This formula is inspired by OPOW (Optimizable Proof of Work)
> research but is a **deliberate simplification** for our use case. It omits
> several mechanisms present in the full OPOW specification — specifically:
> challenge-factor weighting, capped self/delegated deposit factors, legacy
> track multipliers, and phase-in blending for new challenges. These are
> omitted because obol-stack uses USDC (not a staking token with weighted
> deposits) and starts with a small challenge set where these refinements
> add complexity without proportionate benefit. As the challenge set grows
> beyond 4+ challenges, these mechanisms should be revisited.

Workers MUST participate across ALL active challenges to earn maximum rewards. Concentrating on a single challenge triggers an exponential penalty:

```
Given:
  N challenges, worker i has qualifier fraction f[c] in each challenge c
  w[c] = weight per challenge = 1/N (equal)

Compute:
  avg_i   = Σ(w[c] * f_i[c])
  var_i   = Σ(w[c] * (f_i[c] - avg_i)²)
  imb_i   = var_i / (avg_i * (1 - avg_i))       max value = 1.0
  pen_i   = e^(-k * imb_i)          where k = configurable multiplier

  influence_i = normalize(avg_i * pen_i)

Effect (with k = 3.0):
  Worker spreading effort across 4 challenges equally:
    f = [0.25, 0.25, 0.25, 0.25] → imbalance = 0 → k*imb = 0 → penalty = 1.0

  Worker concentrating on 1 challenge:
    f = [1.0, 0.0, 0.0, 0.0]    → imbalance = 1.0 → k*imb = 3.0 → penalty ≈ 0.05

  The concentrated worker earns ~5% of what the diversified worker earns
  despite producing the same total output.

Verified penalty values for 2 challenges (k = 3.0):
  f = [0.50, 0.50] → imb = 0.00 → penalty = 1.00
  f = [0.70, 0.30] → imb = 0.16 → penalty = 0.62
  f = [0.90, 0.10] → imb = 0.64 → penalty = 0.15
  f = [1.00, 0.00] → imb = 1.00 → penalty = 0.05

Note: with a single active challenge, imbalance is forced to 0.0
(avg approaches 1.0, denominator approaches 0, clamped by epsilon).
All workers get penalty = 1.0 regardless. The parity mechanism
activates only with ≥2 challenges.
```

### Commit-Reveal Verification

> **Design note:** This verification flow is inspired by OPOW proof-of-work
> verification but uses stratified sampling (above/below median quality
> regions) rather than flat random sampling. Flat sampling is easier to game
> by hiding low-quality work in unsampled bundles. Stratified sampling ensures
> both high-quality and low-quality results are checked.

```
Step 1: PRECOMMIT
  Worker → Verifier: {challenge_id, settings, num_nonces}
  Verifier → Worker: {benchmark_id, rand_hash, track_id}
  Worker pays: base_fee + per_nonce_fee * num_nonces (via x402 exact scheme)

Step 2: BENCHMARK
  Worker runs all nonces, builds Merkle tree over results
  Worker → Verifier: {merkle_root, solution_quality[]}
  Verifier computes median quality across all nonces
  Verifier performs STRATIFIED sampling:
    - samples_above_median nonces from the above-median quality set
    - samples_below_median nonces from the below-median quality set
  This ensures both high- and low-quality results are verified,
  preventing workers from hiding bad results in one quality tier.
  Verifier → Worker: {sampled_nonces: [above1..., below1...]}

Step 3: PROOF
  Worker → Verifier: {merkle_proofs for sampled nonces}
  Each proof: {nonce, solution, runtime_hash, quality}
  Verifier checks:
    - proof.nonce ∈ sampled_nonces
    - hash(proof) produces leaf in Merkle tree
    - Merkle branch validates against committed root
    - solution quality matches claimed quality (re-execute)

  If any proof fails → worker is NOT a qualifier → no capture for them
  If worker times out on proofs → same result, no capture

  Note: no slashing occurs. The penalty is simply not earning.
  This is enforced by the escrow — uncaptured funds void() back to pool.
```

## Helm Chart Structure

```
charts/autoresearch/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── reward-engine-deployment.yaml
│   ├── reward-engine-service.yaml
│   ├── verifier-deployment.yaml
│   ├── verifier-service.yaml
    ├── escrow-round-manager-deployment.yaml
    ├── reward-distributor-configmap.yaml    # RewardDistributor contract address
    ├── challenge-registry-configmap.yaml
    ├── commerce-payments-configmap.yaml     # AuthCaptureEscrow + collector addresses
│   ├── servicemonitor.yaml                  # Prometheus metrics
│   └── tests/
│       ├── test-reward-engine.yaml
│       ├── test-verifier.yaml
│       └── test-escrow-round-manager.yaml
└── scripts/
    ├── rewards.py                           # OPOW influence + pool distribution
    ├── opow.py                              # Parity formula, imbalance calculation
    ├── verifier.py                          # Commit-reveal, Merkle proof checking
    └── escrow_round_manager.py              # authorize/capture/void lifecycle
```

### values.yaml

```yaml
rounds:
  duration: 3600                    # 1 hour per round
  overlap: 0                       # no overlapping rounds

rewards:
  poolPercentage: 0.30              # 30% of x402 payments go to reward pool
  distribution:
    innovators: 0.20                # 20% to algorithm authors
    workers: 0.70                   # 70% to GPU workers (OPOW)
    operators: 0.10                 # 10% to infrastructure operators
  gamma:                            # reward scaling by active challenges
    a: 1.0
    b: 0.5
    c: 0.3
  imbalanceMultiplier: 3.0          # parity penalty strength

verification:
  samplesAboveMedian: 3             # nonces sampled from above-median quality
  samplesBelowMedian: 2             # nonces sampled from below-median quality
  minActiveQuality: 3.5             # minimum val_bpb to qualify
  proofTimeout: 300                 # seconds to submit proofs
  adoptionThreshold: 0.01           # minimum adoption fraction to earn innovator rewards

escrow:
  mode: direct                      # direct | x402-rs (Phase 1 vs Phase 3)
  chain: base-sepolia               # chain for Commerce Payments contracts
  # AuthCaptureEscrow — same address on mainnet + sepolia
  authCaptureEscrow: "0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff"
  # ERC3009PaymentCollector — same address on mainnet + sepolia
  erc3009Collector: "0x0E3dF9510de65469C4518D7843919c0b8C7A7757"
  # OperatorRefundCollector — same address on mainnet + sepolia
  refundCollector: "0x934907bffd0901b6A21e398B9C53A4A38F02fa5d"
  # RewardDistributor — deployed per-cluster, receives all captures
  # then distributes to individual workers/innovators via ERC20 transfers.
  # Required because AuthCaptureEscrow receiver is fixed per PaymentInfo.
  rewardDistributor: ""             # set after deploying the distributor contract
  # USDC contract addresses
  usdcAddress:
    base: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
    base-sepolia: "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
  # Escrow timing
  authorizationGrace: 3600          # 1 hour after round end to capture
  refundWindow: 86400               # 24 hours post-capture for refunds
  # Platform fee
  feeBps: 200                       # 2% platform fee on captures
  # Operator wallet (calls capture/void)
  operatorWallet: ""                # set by operator, signs capture txs

challenges:
  - name: neuralnet_optimizer
    qualityMetric: val_bpb
    qualityDirection: lower_is_better
    minActiveQuality: 3.5
    baseFee: "0.001"                # USDC per benchmark precommit
    perNonceFee: "0.0001"           # USDC per nonce
    oasfSkill: "devops_mlops/model_versioning"
    tracks:
      default:
        noncesPerBundle: 10
        maxQualifiersPerTrack: 100

discovery:
  preferredBackend: auto            # auto | reth | basescan | 8004scan
  rethIndexerUrl: ""                # auto-detected from cluster if empty
  basescanApiKey: ""
  cacheTtlSeconds: 300

leaderboard:
  enabled: true
  port: 8080
  retentionRounds: 100              # keep last 100 rounds of history

image:
  repository: ghcr.io/obolnetwork/autoresearch
  tag: latest
```

## Dependency Chain

```yaml
# Chart.yaml
apiVersion: v2
name: autoresearch
version: 0.1.0
dependencies:
  - name: reth-erc8004-indexer
    version: ">=0.1.0"
    repository: "file://../reth-erc8004-indexer"
    condition: discovery.preferredBackend == "reth"   # optional dependency
```

The autoresearch chart MUST work without the reth-indexer installed (falls back to BaseScan/8004scan). The indexer is a "nice to have" for operators who want real-time, self-hosted discovery.

## Relationship to Existing Skills

The embedded skills in PR #288 remain as the **agent-facing interface**. The Helm chart provides the **infrastructure** those skills rely on:

```
SKILL                              USES FROM CHART
─────────────────────────────────────────────────────
autoresearch-coordinator           reward-engine API (leaderboard)
  coordinate.py                    verifier API (commit-reveal)
                                   discovery client (worker lookup)

autoresearch-worker                escrow round manager (round status)
  worker_api.py                    verifier API (proof submission)

autoresearch (publish)             reward-engine API (earnings query)
  publish.py                       (mostly unchanged)
```

The coordinator's loop becomes:

```
THINK  →  pick hypothesis
CLAIM  →  discover worker via FallbackClient
CHECK  →  verify round escrow is authorized (on-chain)   ← NEW
RUN    →  submit experiment, pay via x402 exact scheme
VERIFY →  commit-reveal proof cycle                       ← NEW
SCORE  →  verifier records qualifiers                     ← NEW
REWARD →  round-end OPOW distribution via capture()       ← NEW
```

## Test Plan

### Unit tests

- [ ] `opow.py`: imbalance calculation with known inputs
- [ ] `opow.py`: parity penalty with balanced vs concentrated workers
- [ ] `opow.py`: influence normalization sums to 1.0
- [ ] `rewards.py`: pool split matches configured percentages
- [ ] `rewards.py`: adoption-weighted innovator distribution
- [ ] `rewards.py`: gamma scaling with 1, 3, 7 active challenges
- [ ] `verifier.py`: Merkle tree construction and proof verification
- [ ] `verifier.py`: random nonce sampling is deterministic from seed
- [ ] `verifier.py`: unverified workers excluded from qualifiers
- [ ] `escrow_round_manager.py`: authorize() call construction
- [ ] `escrow_round_manager.py`: capture() per-worker amount calculation
- [ ] `escrow_round_manager.py`: void() after all captures
- [ ] `escrow_round_manager.py`: reclaim() on manager timeout/crash

### Integration tests

- [ ] Full round lifecycle: authorize → precommit → benchmark → proof → capture → void
- [ ] Worker with valid proofs receives capture proportional to influence
- [ ] Worker with invalid proofs gets no capture, funds void back to pool
- [ ] Anti-monopoly: worker in 1/4 challenges earns <<< worker in 4/4 challenges
- [ ] Innovator whose algorithm is adopted earns proportional to adoption
- [ ] Leaderboard API returns correct rankings after round completion
- [ ] Round transitions correctly (no double-counting, no missed payments)
- [ ] reclaim() works after authorizationExpiry if manager fails mid-round
- [ ] refund() works within refundExpiry window for post-capture corrections
- [ ] On-chain escrow state matches expected capturableAmount at each step

### Integration tests (Commerce Payments on Anvil fork)

- [ ] authorize() locks correct USDC amount in TokenStore
- [ ] Multiple capture() calls to different workers succeed
- [ ] sum(captures) cannot exceed maxAmount
- [ ] capture() fails after authorizationExpiry
- [ ] void() returns remaining funds to payer
- [ ] reclaim() works only after authorizationExpiry
- [ ] refund() works within refundExpiry window
- [ ] ERC-3009 nonce prevents replay of authorization

### BDD scenarios

```gherkin
Scenario: Honest worker earns proportional reward
  Given a round with 100 USDC authorized in escrow
  And worker A has 60% influence and worker B has 40%
  When the round completes and captures are executed
  Then worker A receives 42 USDC (60% of 70% worker pool)
  And worker B receives 28 USDC (40% of 70% worker pool)
  And 30 USDC is captured for innovators (20%) and operators (10%)
  And void() returns 0 USDC (fully distributed)

Scenario: Concentrated worker is penalized
  Given challenges X and Y are both active
  And worker A submits 100 proofs to X and 100 to Y
  And worker B submits 200 proofs to X and 0 to Y
  When influence is calculated
  Then worker A influence > worker B influence
  And worker B penalty factor < 0.10

Scenario: Worker fails verification — no capture
  Given worker C submits a benchmark with invalid Merkle proofs
  When the verifier checks the proofs
  Then worker C is excluded from qualifiers
  And no capture() is called for worker C
  And worker C's share remains in escrow
  And void() returns worker C's unclaimed share to pool

Scenario: Manager crash — funds are safe
  Given a round with 100 USDC authorized in escrow
  And the escrow round manager crashes mid-round
  When authorizationExpiry passes
  Then the platform wallet calls reclaim()
  And all 100 USDC returns to the platform wallet
  And no funds are permanently locked
```

## Migration from PR #288

Files that stay in PR #288 (base sell/buy flow):
- `cmd/obol/sell.go` — sell command
- `internal/inference/store.go` — provenance types
- `internal/schemas/payment.go` — x402 payment parsing
- `flows/flow-06,07,08,10` — sell/buy flow tests
- `ralph-m1.md` — sell flow validation

Files that move to this PR:
- `internal/embed/skills/autoresearch-coordinator/` — coordinator skill
- `internal/embed/skills/autoresearch-worker/` — worker skill
- `internal/embed/skills/autoresearch/` — publish skill
- `ralph-m2.md` — autoresearch validation (becomes test plan reference)
- `Dockerfile.worker` — worker container image
- `tests/test_autoresearch_worker.py` — worker tests

New files:
- `charts/autoresearch/` — full Helm chart as described above
- `internal/discovery/` — shared with indexer PR (or imported)

## Open Questions

1. **Round duration**: 1 hour seems right for autoresearch experiments (5-10 min each, gives workers time for multiple submissions). Need to validate with real GPU workloads.

2. **Pool funding source**: Currently "30% of x402 payments." Alternative: fixed per-round emission funded by the operator (simpler but requires operator treasury). Or a hybrid — operator seeds the pool, x402 payments top it up.

3. **Innovator identity**: How does an algorithm author register? Options: (a) anyone who submits a train.py that gets adopted, (b) explicit registration via ERC-8004 with algorithm metadata, (c) git-based — author identified by commit signature in provenance.

4. **Multi-challenge readiness**: Currently only `neuralnet_optimizer` (val_bpb) exists. The parity formula needs ≥2 challenges to be meaningful. Second challenge candidates: inference latency optimization, model compression ratio, data preprocessing throughput.

5. **x402 escrow scheme timeline**: PR #1425 is under active review but spec-only. The reference implementation exists at BackTrackCo/x402r-scheme. We should build Phase 1 (direct contract calls) now and add Phase 3 (native x402-rs flow) when the ecosystem catches up. This is a clear contribution opportunity for obol-stack back to x402-rs.

6. **Operator wallet security**: The operator wallet can call capture/void/refund. It should be a multisig or hardware wallet, not a hot key in a ConfigMap secret. Consider integration with the existing Secure Enclave key support in obol-stack's sell command.

## References

- Commerce Payments Protocol: https://github.com/base/commerce-payments
- x402 escrow scheme spec PR: https://github.com/coinbase/x402/pull/1425
- x402 escrow scheme discussion: https://github.com/coinbase/x402/issues/839
- Reference TypeScript implementation: https://github.com/BackTrackCo/x402r-scheme
- x402-rs scheme extension guide: https://github.com/x402-rs/x402-rs/blob/main/docs/how-to-write-a-scheme.md
- x402-rs facilitator scheme registration: https://github.com/x402-rs/x402-rs/tree/main/crates/facilitator/src
- AuthCaptureEscrow on BaseScan: https://basescan.org/address/0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff
- ERC-8004 Identity Registry on BaseScan: https://basescan.org/token/0x8004A169FB4a3325136EB29fA0ceB6D2e539a432
- BaseScan ERC-8004 metadata announcement: https://x.com/etherscan/status/2037131140608434517

## Gherkin Feature Specifications

The following `.feature` files define the executable BDD specifications for the autoresearch economic layer. Each feature covers one bounded area of behavior using declarative, domain-level language. Step definitions target the autoresearch Python scripts and the Commerce Payments contracts on an Anvil fork.

### Feature: Escrow Round Lifecycle

```gherkin
@escrow @critical
Feature: Escrow round lifecycle
  The escrow round manager locks USDC in the Commerce Payments
  AuthCaptureEscrow contract at the start of each round and
  distributes earnings to verified workers at round end.

  Background:
    Given the autoresearch chart is deployed on a k3s cluster
    And an Anvil fork of Base Sepolia is running
    And the platform wallet holds 1000 USDC
    And the AuthCaptureEscrow contract is at "0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff"
    And the reward pool percentage is 30%

  Rule: Funds must be locked before any work begins

    Scenario: Round starts with successful escrow authorization
      Given 200 USDC of x402 payments were collected in the previous round
      When a new round begins
      Then the escrow round manager calls authorize() for 60 USDC
      And the AuthCaptureEscrow capturableAmount equals 60 USDC
      And the authorizationExpiry is set to round end plus 1 hour grace
      And workers can verify the commitment on-chain

    Scenario: Round start fails when platform wallet has insufficient USDC
      Given the platform wallet holds 0 USDC
      When a new round begins
      Then the escrow round manager logs an authorization failure
      And no work is accepted for this round
      And the previous round's uncaptured funds are not affected

  Rule: Workers are paid proportionally to verified influence

    Scenario: Two verified workers receive proportional captures
      Given a round with 100 USDC authorized in escrow
      And worker "0xAAA" has 60% influence
      And worker "0xBBB" has 40% influence
      And both workers passed commit-reveal verification
      When the round completes
      Then capture() is called for "0xAAA" with 42 USDC
      And capture() is called for "0xBBB" with 28 USDC
      And the platform fee receiver gets 2% of each capture
      And void() is called for the remaining 30 USDC
      And the remaining USDC returns to the platform wallet

    Scenario: Unverified worker receives no capture
      Given a round with 100 USDC authorized in escrow
      And worker "0xAAA" passed verification with 50% influence
      And worker "0xCCC" failed commit-reveal verification
      When the round completes
      Then capture() is called for "0xAAA" with 35 USDC
      And no capture() is called for "0xCCC"
      And void() returns 65 USDC to the platform wallet

    Scenario: Round with no verified workers voids entirely
      Given a round with 100 USDC authorized in escrow
      And no workers submitted valid proofs
      When the round completes
      Then void() is called
      And the full 100 USDC returns to the platform wallet

  Rule: Funds are always recoverable

    Scenario: Platform reclaims funds after manager crash
      Given a round with 100 USDC authorized in escrow
      And the escrow round manager process has crashed
      When the authorizationExpiry passes
      Then the platform wallet calls reclaim() directly
      And the full 100 USDC returns to the platform wallet
      And no operator signature is required

    Scenario: Operator refunds a worker after post-capture fraud discovery
      Given worker "0xAAA" received a 42 USDC capture in round 5
      And fraud is discovered within the refund window
      When the operator calls refund() for 42 USDC
      Then 42 USDC returns to the platform wallet
      And the refund is recorded in the round history
```

### Feature: OPOW Influence and Anti-Monopoly

```gherkin
@opow @critical
Feature: OPOW influence calculation with anti-monopoly parity
  The reward engine computes per-worker influence using a parity
  formula that penalizes concentration on a single challenge.
  Workers must diversify across all active challenges to maximize
  their earnings.

  Background:
    Given the imbalance multiplier is 3.0

  Rule: Diversified workers earn more than concentrated workers

    Scenario: Equally diversified worker has zero penalty
      Given 4 active challenges
      And worker "0xAAA" has qualifier fractions:
        | challenge | fraction |
        | c001      |     0.25 |
        | c002      |     0.25 |
        | c003      |     0.25 |
        | c004      |     0.25 |
      When influence is calculated
      Then worker "0xAAA" imbalance is 0.0
      And worker "0xAAA" penalty factor is 1.0

    Scenario: Fully concentrated worker is severely penalized
      Given 4 active challenges
      And worker "0xBBB" has qualifier fractions:
        | challenge | fraction |
        | c001      |     1.00 |
        | c002      |     0.00 |
        | c003      |     0.00 |
        | c004      |     0.00 |
      When influence is calculated
      Then worker "0xBBB" imbalance is 3.0
      And worker "0xBBB" penalty factor is less than 0.05

    Scenario: Concentrated worker earns less despite equal total output
      Given 2 active challenges and a worker pool of 100 USDC
      And worker "0xAAA" submitted 50 proofs to c001 and 50 to c002
      And worker "0xBBB" submitted 100 proofs to c001 and 0 to c002
      When influence is calculated and rewards are distributed
      Then worker "0xAAA" earns more than worker "0xBBB"
      And the ratio of earnings exceeds 5:1

    Scenario Outline: Parity penalty scales with concentration
      Given 2 active challenges
      And a worker has qualifier fractions <f1> and <f2>
      When influence is calculated
      Then the penalty factor is approximately <penalty>

      Examples:
        | f1   | f2   | penalty |
        | 0.50 | 0.50 |    1.00 |
        | 0.70 | 0.30 |    0.65 |
        | 0.90 | 0.10 |    0.11 |
        | 1.00 | 0.00 |    0.05 |

  Rule: Influence values are normalized across all workers

    Scenario: Total influence sums to 1.0
      Given 3 workers with varying qualifier fractions
      When influence is calculated for all workers
      Then the sum of all influence values equals 1.0

    Scenario: Single worker in a round gets full influence
      Given 1 worker who participated in all active challenges
      When influence is calculated
      Then that worker's influence is 1.0
      And they receive the entire worker pool
```

### Feature: Commit-Reveal Work Verification

```gherkin
@verification @critical
Feature: Commit-reveal work verification
  Workers commit to results via a Merkle root before learning
  which nonces will be sampled. This prevents retroactive
  fabrication of results.

  Background:
    Given the verifier is running
    And the neuralnet_optimizer challenge is active
    And the sample count is 5 nonces per benchmark

  Rule: Honest workers pass verification

    Scenario: Worker with valid proofs becomes a qualifier
      Given worker "0xAAA" precommits a benchmark with 100 nonces
      And the verifier assigns a random hash and track
      When worker "0xAAA" submits a Merkle root over 100 results
      And the verifier samples 5 nonces for verification
      And worker "0xAAA" submits valid Merkle proofs for all 5
      Then worker "0xAAA" is recorded as a qualifier
      And the benchmark quality scores are accepted

    Scenario: Re-execution confirms claimed quality
      Given worker "0xAAA" claims val_bpb of 3.2 for nonce 42
      When the verifier re-executes nonce 42 with the same settings
      Then the re-executed val_bpb matches the claimed 3.2
      And the proof is accepted

  Rule: Dishonest workers fail verification

    Scenario: Invalid Merkle proof is rejected
      Given worker "0xCCC" submitted a Merkle root
      And the verifier sampled nonces [7, 23, 45, 61, 89]
      When worker "0xCCC" submits a proof for nonce 23 that does not match the root
      Then the verification fails for worker "0xCCC"
      And worker "0xCCC" is excluded from qualifiers for this round
      And no escrow capture is made for worker "0xCCC"

    Scenario: Worker who inflates quality scores is caught
      Given worker "0xCCC" claims val_bpb of 2.8 for nonce 42
      When the verifier re-executes nonce 42 with the same settings
      And the re-executed val_bpb is 3.5
      Then the quality mismatch is detected
      And the verification fails for worker "0xCCC"

    Scenario: Worker who times out on proof submission is excluded
      Given worker "0xCCC" submitted a Merkle root
      And the verifier sampled 5 nonces
      When worker "0xCCC" does not submit proofs within 300 seconds
      Then worker "0xCCC" is excluded from qualifiers
      And the round proceeds without them

  Rule: Sampling is fair and deterministic

    Scenario: Nonce sampling is deterministic from the round seed
      Given the same benchmark settings and random hash
      When nonces are sampled twice
      Then the same 5 nonces are selected both times

    Scenario: Worker cannot predict which nonces will be sampled
      Given the random hash is derived from a future block hash
      When the worker commits their Merkle root
      Then the sampled nonces have not yet been determined
```

### Feature: Reward Pool Distribution

```gherkin
@rewards
Feature: Reward pool distribution across roles
  The reward engine splits the pool among innovators, workers,
  and operators according to configured percentages. Worker
  distribution is influence-weighted. Innovator distribution
  is adoption-weighted.

  Background:
    Given the pool split is 20% innovators, 70% workers, 10% operators
    And a round with 100 USDC in the reward pool

  Rule: Pool splits match configured percentages

    Scenario: Standard round distributes to all three roles
      When the round completes with verified workers
      Then 20 USDC is allocated to innovators
      And 70 USDC is allocated to workers
      And 10 USDC is allocated to operators

  Rule: Workers earn by influence

    Scenario: Workers are paid proportionally to influence
      Given the worker pool is 70 USDC
      And worker "0xAAA" has influence 0.6
      And worker "0xBBB" has influence 0.4
      When worker rewards are distributed
      Then worker "0xAAA" earns 42 USDC
      And worker "0xBBB" earns 28 USDC

  Rule: Innovators earn by adoption

    Scenario: Algorithm author earns when workers adopt their code
      Given the innovator pool is 20 USDC for the neuralnet_optimizer challenge
      And algorithm "fast-muon-v3" by innovator "0xINN1" has 75% adoption
      And algorithm "baseline-adamw" by innovator "0xINN2" has 25% adoption
      When innovator rewards are distributed
      Then innovator "0xINN1" earns 15 USDC
      And innovator "0xINN2" earns 5 USDC

    Scenario: Unadopted algorithm earns nothing
      Given the innovator pool is 20 USDC
      And algorithm "untested-v1" has 0% adoption
      When innovator rewards are distributed
      Then the author of "untested-v1" earns 0 USDC
      And the unadopted share rolls into the next round

  Rule: Gamma scaling adjusts for challenge count

    Scenario Outline: Reward scales with number of active challenges
      Given gamma parameters a=1.0, b=0.5, c=0.3
      And <n> challenges are active
      When the gamma value is calculated
      Then the scaling factor is approximately <gamma>

      Examples:
        | n | gamma |
        | 1 |  0.63 |
        | 3 |  0.80 |
        | 7 |  0.94 |
```

### Feature: Worker Discovery and Fallback

```gherkin
@discovery
Feature: Multi-tier worker discovery with fallback
  The coordinator discovers GPU workers through a prioritized
  chain of discovery backends. If the preferred backend is
  unavailable, it falls back to the next tier automatically.

  Background:
    Given the OASF skill filter is "devops_mlops/model_versioning"

  Rule: Discovery uses the highest-priority available backend

    Scenario: Coordinator uses Reth indexer when available
      Given the reth-erc8004-indexer is deployed in the cluster
      And the indexer has synced past the latest registration
      When the coordinator discovers workers
      Then the query goes to the Reth indexer API
      And workers with the model_versioning skill are returned

    Scenario: Coordinator falls back to BaseScan when indexer is down
      Given the reth-erc8004-indexer is not deployed
      And a BaseScan API key is configured
      When the coordinator discovers workers
      Then the query goes to the BaseScan API
      And ERC-8004 NFT metadata is read for each agent
      And workers with the model_versioning skill are returned

    Scenario: Coordinator falls back to 8004scan as last resort
      Given the reth-erc8004-indexer is not deployed
      And no BaseScan API key is configured
      When the coordinator discovers workers
      Then the query goes to 8004scan.io
      And workers with the model_versioning skill are returned

    Scenario: All backends unavailable produces a clear error
      Given no discovery backends are reachable
      When the coordinator discovers workers
      Then a "no discovery backend available" error is returned
      And the round proceeds with zero workers

  Rule: Discovery results are cached to reduce API calls

    Scenario: Repeated queries within TTL use cached results
      Given the cache TTL is 300 seconds
      And a discovery query succeeded 60 seconds ago
      When the coordinator discovers workers again
      Then no external API call is made
      And the cached results are returned
```

### Feature: End-to-End Round

```gherkin
@e2e @slow
Feature: End-to-end autoresearch round
  A complete round from escrow authorization through worker
  experiments to reward distribution and settlement.

  Background:
    Given the autoresearch chart is deployed with default values
    And an Anvil fork of Base Sepolia is running
    And the platform wallet holds 500 USDC
    And 2 GPU workers are registered on ERC-8004:
      | address | skill                          | gpu       |
      | 0xW001  | devops_mlops/model_versioning  | NVIDIA T4 |
      | 0xW002  | devops_mlops/model_versioning  | NVIDIA A10 |
    And 1 innovator submitted algorithm "muon-opt-v2" for neuralnet_optimizer

  Scenario: Complete round with two honest workers
    # Round setup
    Given 100 USDC of x402 payments were collected in the previous round
    When a new round begins
    Then 30 USDC is authorized in escrow

    # Worker experiments
    When worker "0xW001" precommits a benchmark with 50 nonces
    And worker "0xW002" precommits a benchmark with 50 nonces
    And both workers submit Merkle roots over their results
    And the verifier samples 5 nonces from each worker
    And both workers submit valid Merkle proofs
    Then both workers are recorded as qualifiers

    # Reward calculation
    When the round duration expires
    Then the reward engine computes influence for both workers
    And both workers have balanced challenge participation
    And influence is split proportionally to qualifier count

    # Settlement
    When captures are executed
    Then worker "0xW001" receives their earned USDC via capture()
    And worker "0xW002" receives their earned USDC via capture()
    And innovator "muon-opt-v2" receives adoption-weighted USDC
    And the operator receives 10% of the pool
    And void() returns any remainder to the platform wallet
    And the leaderboard API shows both workers with correct earnings
    And the next round begins with a new authorization

  Scenario: Round where one worker submits fraudulent proofs
    Given 100 USDC of x402 payments were collected
    When a new round begins
    Then 30 USDC is authorized in escrow

    When worker "0xW001" submits valid proofs for all sampled nonces
    And worker "0xW002" submits a proof with a quality mismatch
    Then worker "0xW001" is a qualifier
    And worker "0xW002" is excluded

    When captures are executed
    Then worker "0xW001" receives the entire worker pool share
    And worker "0xW002" receives nothing
    And void() returns worker "0xW002"'s unclaimed share to the platform
```

## Labels

`component:autoresearch` `component:rewards` `component:x402` `priority:high` `size:XL`
