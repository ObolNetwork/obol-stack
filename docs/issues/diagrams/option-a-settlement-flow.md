# Option A: Platform Wallet Settlement — No Custom Contracts

## The Core Idea

The platform wallet is both the **payer** (locks funds in escrow) and the
**receiver** (gets them back via capture). Then it distributes to workers
and innovators with standard USDC transfers. The "distributor" is a script,
not a smart contract.

```
ZERO custom Solidity.
ZERO new deployments.
Only existing audited Commerce Payments contracts + standard ERC20 transfers.
```

## Full Flow Diagram

```
                         BASE L2 (on-chain)
┌──────────────────────────────────────────────────────────────────────┐
│                                                                      │
│  ┌─────────────────┐              ┌────────────────────────────┐    │
│  │ USDC Contract   │              │ AuthCaptureEscrow          │    │
│  │ (Base Mainnet)  │              │ 0xBdEA...0cff              │    │
│  │                 │              │ (5x audited, deployed)     │    │
│  │                 │              │                            │    │
│  │ balanceOf(plat) │              │ ┌────────────────────────┐ │    │
│  │ balanceOf(w1)   │              │ │ TokenStore             │ │    │
│  │ balanceOf(w2)   │              │ │ (holds escrowed USDC)  │ │    │
│  │ balanceOf(inn)  │              │ └────────────────────────┘ │    │
│  └────────┬────────┘              └─────────────┬──────────────┘    │
│           │                                     │                    │
└───────────│─────────────────────────────────────│────────────────────┘
            │                                     │
            │          OBOL-STACK CLUSTER          │
┌───────────│─────────────────────────────────────│────────────────────┐
│           │                                     │                    │
│  ┌────────▼─────────────────────────────────────▼──────────────┐    │
│  │                    ESCROW ROUND MANAGER                      │    │
│  │                    (Python script in pod)                    │    │
│  │                                                             │    │
│  │  Holds the platform wallet private key (or Secure Enclave)  │    │
│  │  This is the OPERATOR and the PAYER and the RECEIVER        │    │
│  └─────────────────────────┬───────────────────────────────────┘    │
│                            │                                        │
│           ┌────────────────┼────────────────┐                       │
│           │                │                │                       │
│  ┌────────▼──────┐ ┌──────▼──────┐ ┌───────▼─────┐                │
│  │ Reward Engine │ │ Verifier    │ │ Discovery   │                │
│  │ (OPOW calc)   │ │ (proofs)   │ │ (ERC-8004)  │                │
│  └───────────────┘ └─────────────┘ └─────────────┘                │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

## Step-by-Step: One Round

```
STEP 1: AUTHORIZE
════════════════════════════════════════════════════════════════

  Platform wallet signs ERC-3009 receiveWithAuthorization:
    from:     platform_wallet     (payer)
    to:       AuthCaptureEscrow
    value:    100 USDC            (this round's pool)

  PaymentInfo struct:
    operator: platform_wallet     ← same entity
    payer:    platform_wallet     ← same entity
    receiver: platform_wallet     ← SAME ENTITY (this is the trick)
    token:    USDC
    maxAmount: 100 USDC
    authorizationExpiry: round_end + 1 hour
    refundExpiry: round_end + 24 hours

  Call: AuthCaptureEscrow.authorize(paymentInfo, 100, collector, sig)

  Result:
    ┌──────────────────┐         ┌──────────────────┐
    │ Platform Wallet  │ ──$100──▶ TokenStore       │
    │ balance: -100    │         │ (escrowed)       │
    │                  │         │ capturableAmt=100│
    └──────────────────┘         └──────────────────┘

  WHY: The 100 USDC is now LOCKED. Platform can't spend it on
  anything else. Workers can verify on-chain that the commitment
  is real before starting work.


STEP 2: WORKERS DO WORK (during the round)
════════════════════════════════════════════════════════════════

  Workers submit experiments, proofs are verified.
  No money moves. This is the same flow as today.

  ┌──────────┐  precommit   ┌──────────┐
  │ Worker 1 │ ───────────▶ │ Verifier │
  │ (spark1) │  benchmark   │          │
  │          │ ───────────▶ │ records  │
  │          │  proof       │ quals    │
  │          │ ───────────▶ │          │
  └──────────┘              └──────────┘

  ┌──────────┐  precommit   ┌──────────┐
  │ Worker 2 │ ───────────▶ │ Verifier │
  │ (spark2) │  benchmark   │          │
  │          │ ───────────▶ │          │
  │          │  proof       │          │
  │          │ ───────────▶ │          │
  └──────────┘              └──────────┘


STEP 3: REWARD ENGINE COMPUTES SHARES
════════════════════════════════════════════════════════════════

  Pool: 100 USDC
  Split: 70% workers, 20% innovators, 10% operator

  Worker influence (from OPOW parity formula):
    Worker 1: influence = 0.6 → reward = 70 * 0.6 = 42 USDC
    Worker 2: influence = 0.4 → reward = 70 * 0.4 = 28 USDC

  Innovator adoption:
    "muon-v3": adoption 75% → reward = 20 * 0.75 = 15 USDC
    "adamw":   adoption 25% → reward = 20 * 0.25 =  5 USDC

  Operator: 10 USDC

  Total to distribute: 42 + 28 + 15 + 5 + 10 = 100 USDC


STEP 4: CAPTURE (single call)
════════════════════════════════════════════════════════════════

  The escrow round manager calls capture() for the FULL distributable amount.
  Since receiver = platform_wallet, the USDC comes right back to us.

  Call: AuthCaptureEscrow.capture(paymentInfo, 100, feeBps=0, feeReceiver)

  Result:
    ┌──────────────────┐         ┌──────────────────┐
    │ TokenStore       │ ──$100──▶ Platform Wallet  │
    │ capturableAmt=0  │         │ balance: +100    │
    │                  │         │ (back to us)     │
    └──────────────────┘         └──────────────────┘

  WHY capture to ourselves instead of just voiding?
  Because capture creates an ON-CHAIN RECORD of settlement.
  Anyone can verify the round was settled by reading the events.
  void() would look like the round was cancelled.


STEP 5: DISTRIBUTE (standard ERC20 transfers)
════════════════════════════════════════════════════════════════

  Now the platform wallet holds the USDC and distributes directly.
  These are plain USDC.transfer() calls. No custom contract.

  ┌──────────────────┐
  │ Platform Wallet  │
  │ balance: 100     │
  │                  │
  │  transfer(W1, 42)│──── 42 USDC ────▶ Worker 1 wallet
  │  transfer(W2, 28)│──── 28 USDC ────▶ Worker 2 wallet
  │  transfer(I1, 15)│──── 15 USDC ────▶ Innovator 1 wallet
  │  transfer(I2,  5)│────  5 USDC ────▶ Innovator 2 wallet
  │  (keep 10)       │                   Operator keeps 10
  │                  │
  │  balance: 10     │
  └──────────────────┘

  Each transfer is a separate on-chain tx.
  With 10 workers + 5 innovators = 15 transfers ≈ 15 * $0.001 gas ≈ $0.015
  (Base L2 gas is extremely cheap)


STEP 6: VOID (cleanup — usually a no-op)
════════════════════════════════════════════════════════════════

  If we captured the full amount, void() has nothing to return.
  If we captured less (e.g., some workers didn't qualify), void()
  returns the remainder to the platform wallet.

  Call: AuthCaptureEscrow.void(paymentInfo)

  Result: capturableAmount (if any) returns to payer.


STEP 7: NEXT ROUND
════════════════════════════════════════════════════════════════

  Repeat from Step 1 with the new round's pool.
  The operator's 10 USDC stays in the platform wallet.
  New x402 payments from buyers add to the next pool.
```

## Why This Works

```
QUESTION                               ANSWER
──────────────────────────────────────────────────────────────────
"Isn't receiver=payer circular?"        Yes, intentionally. We use
                                        the escrow for COMMITMENT
                                        (locked, verifiable on-chain)
                                        not for routing.

"Why not just transfer directly         Because authorize() creates
 to workers without escrow?"            a verifiable on-chain commitment.
                                        Workers see the locked pool
                                        BEFORE doing work. Without it,
                                        workers have to trust the
                                        platform will pay.

"What if the manager crashes?"          After authorizationExpiry,
                                        platform wallet calls reclaim().
                                        Money comes back. Workers
                                        don't get paid for that round,
                                        but no funds are lost.

"What if a transfer to a worker         The other transfers already
 fails?"                                succeeded (each is independent).
                                        Retry the failed one. USDC
                                        transfer failures are almost
                                        always gas-related, not
                                        permanent.

"Can workers verify they'll             Yes. On-chain:
 get paid?"                             1. Read capturableAmount = pool
                                        2. Read authorizationExpiry > now
                                        3. PaymentInfo is deterministic
                                           from round params
                                        4. If locked, the math determines
                                           their share (public formula)

"What about front-running the           capture() is called by the
 distribution?"                         operator (platform wallet) only.
                                        Nobody else can capture.
                                        Workers receive standard ERC20
                                        transfers after capture.

"Is this as secure as a custom          MORE secure. We use 5x-audited
 distributor contract?"                 Commerce Payments + standard
                                        ERC20 transfers. A custom
                                        contract is a new attack surface.
```

## What Each Party Sees On-Chain

```
WORKER'S PERSPECTIVE:
  Before work:
    → AuthCaptureEscrow.getPaymentState(hash) shows capturableAmount = 100 USDC
    → "Pool is committed, I'll get paid if I do good work"

  After round:
    → USDC.Transfer(platform_wallet → my_wallet, 42 USDC)
    → "I got paid"

  Audit trail:
    → Authorized event (round start, 100 USDC locked)
    → Captured event (round end, 100 USDC settled)
    → Transfer events (42 USDC to me, 28 to other worker, etc)

INNOVATOR'S PERSPECTIVE:
    → Same as worker but smaller amount (adoption-weighted)

PLATFORM OPERATOR'S PERSPECTIVE:
    → authorize(): 100 USDC leaves wallet to escrow
    → capture():   100 USDC returns to wallet from escrow
    → transfer():  90 USDC leaves wallet to participants
    → Net:         kept 10 USDC (operator share)
    → All events auditable on BaseScan
```

## Contract Interaction Summary

```
CALL                            WHO SIGNS          CONTRACT                     WHAT HAPPENS
─────────────────────────────────────────────────────────────────────────────────────────────
authorize(paymentInfo, amt)     platform wallet    AuthCaptureEscrow            USDC locked
capture(paymentInfo, amt)       platform wallet    AuthCaptureEscrow            USDC returned to platform
void(paymentInfo)               platform wallet    AuthCaptureEscrow            remainder returned
reclaim(paymentInfo)            platform wallet    AuthCaptureEscrow            safety recovery
USDC.transfer(worker, amt)      platform wallet    USDC ERC20                   worker gets paid
USDC.transfer(innovator, amt)   platform wallet    USDC ERC20                   innovator gets paid

Total contracts called: 2 (AuthCaptureEscrow + USDC)
Custom contracts deployed: 0
New audits needed: 0
```
