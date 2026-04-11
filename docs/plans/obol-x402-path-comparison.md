# OBOL x402 Path Comparison

Date: 2026-04-11

## Scope

Compare two local paths for OBOL payments on a forked Base Sepolia Anvil:

1. `exact` + Permit2 + EIP-2612 gas sponsoring
2. session-style authorize-once / close-once prototype

The asset is a fork-local `ForkObolToken` deployed only inside the Anvil fork.

## Benchmarks

### Exact path

Source:
- `internal/openclaw/monetize_integration_test.go`
- `TestIntegration_SellBuySidecar_OBOLPermit2`

Workload:
- buy `3` auths
- consume `3` paid requests
- settle each request on-chain

Measured receipts:

1. `gasUsed=136993`, `gasWei=598512279518`
2. `gasUsed=102793`, `gasWei=405836940507`
3. `gasUsed=102769`, `gasWei=367890102513`

Aggregate:

- requests: `3`
- total gas used: `342555`
- total gas wei: `1372239322538`
- average gas used / request: `114185`
- average gas wei / request: `457413107512`

Observations:

- The first request is more expensive because it pays the EIP-2612 approval path.
- Later requests are cheaper because the Permit2 allowance already exists and settlement falls back to the normal exact Permit2 path.

### Session prototype

Source:
- measured from a local prototype on the same forked Base Sepolia / fork-local OBOL setup
- kept here as comparison data only
- not included in the current exact-path implementation diff

Workload:
- authorize once for `3 * 0.001 OBOL`
- close once after off-chain usage accounting

Measured receipts:

1. authorize: `gasUsed=193319`, `gasWei=763123466077`
2. close: `gasUsed=84517`, `gasWei=302520487868`

Aggregate:

- requests represented: `3`
- total gas used: `277836`
- total gas wei: `1065643953945`
- average gas used / request: `92612`
- average gas wei / request: `355214651315`

## Comparison

For `3` requests:

- session total gas used is lower by `64719` (`18.9%`)
- session total gas wei is lower by `306595368593` (`22.3%`)

### Crossover intuition

Using the measured receipts:

- exact first request ~= `136993` gas
- exact follow-up request ~= `102781` gas
- session fixed cost ~= `277836` gas total

So session becomes cheaper than exact at roughly `3` requests and above.

## Storage

Measured on the current exact path:

- `x402-buyer-auths` ConfigMap payload: `5851` bytes for `3` auths
  - ~= `1950` bytes/auth stored
- `PurchaseRequest.spec.preSignedAuths`: `4278` bytes for `3` auths
  - ~= `1426` bytes/auth

Practical limit in the current ConfigMap-based exact path:

- buyer auth pool ceiling ~= `537` auths per ConfigMap payload

The session prototype does not need one auth object per request. It stores a single session authorization and settles once, which is structurally better for large prepaid packs.

## Current recommendation

1. Keep `exact` for low-count request flows and broad compatibility.
2. Prefer a session / commerce-style path for:
   - packs
   - subscriptions
   - workloads with `3+` paid requests per purchase
3. Do not treat the current exact path as a subscription primitive. It is still one on-chain settlement per request.
