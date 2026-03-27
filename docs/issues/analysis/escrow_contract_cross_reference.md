# Escrow Feature vs. AuthCaptureEscrow Contract: Cross-Reference Analysis

**Date:** 2025-03-27
**Contract:** AuthCaptureEscrow at 0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff (Base + Base Sepolia)
**Source:** https://github.com/base/commerce-payments (src/AuthCaptureEscrow.sol)
**Feature:** escrow_round_lifecycle.feature

---

## 1. Multiple capture() Calls Per Authorization

**Feature assumes:** capture() called once per worker (lines 39-40: capture for 0xAAA then 0xBBB)

**Contract reality: YES — multiple captures ARE supported.**

From AuthCaptureEscrow.sol line 258-260:
```
/// @dev Can be called multiple times up to cumulative authorized amount
```

The logic at lines 283-291:
```solidity
if (state.capturableAmount < amount) {
    revert InsufficientAuthorization(...)
}
state.capturableAmount -= uint120(amount);
state.refundableAmount += uint120(amount);
```

Each capture reduces capturableAmount and increases refundableAmount. The test
`test_succeeds_withMultipleCaptures` in capture.t.sol confirms two consecutive
captures work correctly.

**Verdict: Feature assumption is correct for multiple calls. BUT SEE ISSUE #4 BELOW.**

---

## 2. void() After Partial Captures (Partial Void)

**Feature assumes:** void() returns "remaining 30 USDC" after captures (line 42)

**Contract reality: YES — partial void works correctly.**

From void() at lines 304-316:
```solidity
uint256 authorizedAmount = paymentState[paymentInfoHash].capturableAmount;
if (authorizedAmount == 0) revert ZeroAuthorization(paymentInfoHash);
paymentState[paymentInfoHash].capturableAmount = 0;
_sendTokens(paymentInfo.operator, paymentInfo.token, paymentInfo.payer, authorizedAmount);
```

After partial captures, capturableAmount holds only the REMAINING uncaptured amount.
void() returns exactly that remainder to the payer. It does NOT touch refundableAmount
(previously captured funds).

**Verdict: Feature assumption is correct.** Capture 70 USDC, void returns 30 USDC.

---

## 3. reclaim() After authorizationExpiry

**Feature assumes:** "platform wallet calls reclaim() directly" with "no operator signature required" (lines 67-69)

**Contract reality: PARTIALLY CORRECT — important nuances.**

From reclaim() at lines 323-340:
```solidity
function reclaim(PaymentInfo calldata paymentInfo)
    external nonReentrant onlySender(paymentInfo.payer) {

    if (block.timestamp < paymentInfo.authorizationExpiry) {
        revert BeforeAuthorizationExpiry(...)
    }
    uint256 authorizedAmount = paymentState[paymentInfoHash].capturableAmount;
    if (authorizedAmount == 0) revert ZeroAuthorization(paymentInfoHash);

    paymentState[paymentInfoHash].capturableAmount = 0;
    _sendTokens(paymentInfo.operator, paymentInfo.token, paymentInfo.payer, authorizedAmount);
}
```

Key findings:
- reclaim() is restricted to `onlySender(paymentInfo.payer)` — the PAYER must call it
- No operator involvement needed — feature is correct about "no operator signature"
- Requires `block.timestamp >= authorizationExpiry` — feature's >= condition is correct
- Returns only capturableAmount (remaining after any captures)
- The payer needs the full PaymentInfo struct to call reclaim (for hash verification)

**ISSUE:** Feature says "platform wallet calls reclaim()". This works ONLY IF the platform
wallet address equals paymentInfo.payer. In our model, the platform locks its own USDC,
so it IS the payer. This is fine but must be explicitly ensured.

**ISSUE:** The feature says "full 100 USDC returns." This is only true if NO captures
were made before the crash. If any captures happened, reclaim returns only the remainder.
After authorizationExpiry, capture() also stops working (block.timestamp >= check), so
there's no race — once expired, only reclaim works.

**Verdict: Feature is correct IF platform wallet == payer in PaymentInfo.**

---

## 4. CRITICAL: Receiver Address Per Capture Call

**Feature assumes:** Different receiver per capture:
```
capture() is called for "0xAAA" with 42 USDC   (line 39)
capture() is called for "0xBBB" with 28 USDC   (line 40)
```

**Contract reality: NO — receiver is FIXED in PaymentInfo. THIS IS A BLOCKING ISSUE.**

From capture() at line 295:
```solidity
_distributeTokens(paymentInfo.token, paymentInfo.receiver, amount, feeBps, feeReceiver);
```

The PaymentInfo struct (lines 27-52) has a SINGLE `receiver` field:
```solidity
struct PaymentInfo {
    address operator;
    address payer;
    address receiver;    // <-- FIXED per authorization
    ...
}
```

The PaymentInfo hash is computed from ALL fields including receiver. You CANNOT change
the receiver between capture calls because:
1. capture() takes the full PaymentInfo as calldata
2. The hash must match the authorization's hash
3. Changing receiver = different hash = InsufficientAuthorization revert

**ALL captures from one authorization go to the SAME receiver address.**

### Workaround Options:

**Option A: Separate Authorization Per Worker**
Create individual PaymentInfo (with unique salt+receiver) per worker. Each gets its own
authorize() call. Pros: Direct payment to workers. Cons: N authorize() transactions per
round (gas-expensive), requires knowing workers before round starts, complex payer
signature management.

**Option B: Distributor Contract as Receiver (RECOMMENDED)**
Set receiver to a Splitter/Distributor contract that the platform controls. Flow:
1. authorize() with receiver = DistributorContract
2. capture(fullAmount) sends all to DistributorContract
3. DistributorContract.distribute() pays individual workers
Pros: Single authorize+capture. Cons: Extra contract, extra step, workers don't see
direct on-chain escrow commitment to their individual share.

**Option C: Platform Wallet as Receiver + Direct Transfers**
Set receiver = platform wallet. After capture, platform sends to workers via standard
ERC20 transfers. Pros: Simplest. Cons: Workers must trust platform for last-mile
distribution — undermines the escrow trust model.

**Option D: Multiple Authorizations with Multicall**
Use the deployed Multicall3 (at 0xcA11bde05977b3631167028862bE2a173976CA11) to batch
multiple authorize() calls in one transaction, each with a different worker as receiver.
Requires separate payer signatures per authorization. Could work with EIP-7702 batch
or Smart Wallet batching.

**Verdict: Feature file is INCORRECT. Must be redesigned. Option B or D recommended.**

---

## 5. Refund Flow — Per-Capture or Global?

**Feature assumes:** "refund() for 42 USDC" (line 74) — appears per-worker amount

**Contract reality: GLOBAL refund pool, not per-capture.**

From refund() at lines 351-374:
```solidity
uint120 captured = paymentState[paymentInfoHash].refundableAmount;
if (captured < amount) revert RefundExceedsCapture(amount, captured);
paymentState[paymentInfoHash].refundableAmount = captured - uint120(amount);
```

Key findings:
- refundableAmount is the CUMULATIVE total of all captures for that paymentInfoHash
- refund() can return any amount up to that cumulative total
- Refund goes to the PAYER (paymentInfo.payer), not to any specific receiver/worker
- The OPERATOR must provide the refund tokens via a tokenCollector
- Refund has its own expiry: paymentInfo.refundExpiry

**IMPORTANT:** The operator must have liquidity to fund the refund. The contract pulls
tokens FROM the operator (via OperatorRefundCollector), not from the receiver. This
means:
- After capture sends tokens to the receiver, the operator can't automatically refund
- The operator needs to acquire tokens independently to execute a refund
- In our model, this means the platform (as operator) must hold enough USDC to cover
  potential refunds

**Feature says "42 USDC returns to the platform wallet."** This is correct IF
platform wallet == payer. The refund goes to paymentInfo.payer.

**Verdict: Feature is approximately correct but oversimplifies. Refund is global,
requires operator to supply tokens, and goes to payer not receiver.**

---

## 6. Reentrancy and Ordering Issues Not Covered

### Covered by Contract
- **ReentrancyGuardTransient** on all state-changing functions (authorize, capture, void,
  reclaim, refund, charge). Uses Solady's transient storage variant.
- **Single authorization** enforced: `hasCollectedPayment` flag prevents double-authorize
- **Void idempotency**: void sets capturableAmount=0, second void reverts ZeroAuthorization
- Reentrancy tests in reentrancy.t.sol confirm protection works

### Issues NOT Covered by Feature Scenarios

**A. Authorization Expiry Race Condition**
The feature doesn't cover the case where authorizationExpiry is reached DURING the
capture sequence. If captures for workers are sequential (not batched), the last capture
could fail because block.timestamp >= authorizationExpiry. Must set authorizationExpiry
with generous buffer (feature says "round end + 1 hour" which helps).

**B. Refund After Partial Void**
After void(), refundableAmount still holds the captured amount. refund() can still be
called. The feature has no scenario for: capture some, void remainder, THEN refund.

**C. Payer Reclaim vs Operator Void Race**
Both void() (operator) and reclaim() (payer) clear capturableAmount. If both are
attempted near authorizationExpiry:
- Before expiry: only void() works (reclaim reverts)
- After expiry: both revert on void() (no timestamp check) but void still works!
  Actually, void() has NO timestamp check — it can be called any time. So the operator
  could void() even after authorizationExpiry, before the payer calls reclaim().
  First one to execute wins (both set capturableAmount to 0).

**D. Fee Rounding on Multiple Small Captures**
Multiple small captures may accumulate rounding errors in fees vs. one large capture.
The contract calculates: `feeAmount = amount * feeBps / 10_000` per capture. With many
small captures, total fees could differ by a few wei from a single large capture.

**E. Front-Running by Operator**
The operator could theoretically front-run a payer's reclaim() with a capture() just
before authorizationExpiry. The feature doesn't cover adversarial operator behavior.
Since in our model the operator IS the platform, this is self-defeating, but worth noting.

**F. PaymentInfo Replay Protection**
Each PaymentInfo (identified by hash) can only be authorized once (hasCollectedPayment).
A unique `salt` field prevents hash collisions across rounds. The feature doesn't
explicitly test salt uniqueness per round — if the same salt is reused, authorize()
will revert with PaymentAlreadyCollected.

---

## Summary of Feature File Accuracy

| Check | Status | Notes |
|-------|--------|-------|
| Multiple capture() calls | CORRECT | Supported, decrements capturableAmount |
| Partial void after captures | CORRECT | Returns only remaining capturableAmount |
| reclaim() after expiry | CORRECT* | *Only if platform wallet == payer |
| Different receiver per capture | **INCORRECT** | **Receiver is fixed in PaymentInfo** |
| Refund flow | MOSTLY CORRECT | Global pool, operator must supply tokens |
| Reentrancy protection | ADEQUATE | transient reentrancy guard on all functions |

## REQUIRED CHANGES TO FEATURE FILE

1. **CRITICAL:** Lines 39-40 and 50 must be redesigned. Cannot capture to different
   worker addresses from one authorization. Must either:
   - Use a distributor/splitter contract as the single receiver
   - Create separate authorizations per worker
   - Use platform as receiver + off-chain distribution

2. **Line 42:** void() call is correct but should specify it returns to the PAYER
   (paymentInfo.payer), not generically to "platform wallet"

3. **Line 67-68:** reclaim() must be called by the PAYER address. Feature should
   clarify platform wallet == paymentInfo.payer

4. **Lines 73-75:** refund() pulls tokens FROM the operator, not from the receiver.
   The platform (operator) must have separate USDC to fund refunds.

5. **Missing scenario:** Salt uniqueness per round to avoid PaymentAlreadyCollected

6. **Missing scenario:** Authorization expiry race during sequential captures

7. **Missing scenario:** What happens if authorizationExpiry is set too tight and
   the last capture fails
