# ADR-0004: Pre-Signed ERC-3009 Voucher Pool for Buy-Side Payments

**Status:** Accepted
**Date:** 2026-03-27

## Context

The OpenClaw agent needs to purchase inference from remote x402-gated sellers. The buy-side payment mechanism must satisfy:

- **No hot wallet in the sidecar**: The x402-buyer sidecar must never have access to a private key. A compromised sidecar should not drain the wallet.
- **Bounded spending**: The maximum possible loss must be known and capped at deployment time.
- **Low latency**: Payment attachment must not add significant overhead to each inference request.
- **Restart resilience**: Consumed vouchers must not be reused after a sidecar restart.

Alternatives considered:

| Option | Pros | Cons |
|--------|------|------|
| **Pre-signed ERC-3009 vouchers** | Zero signer in sidecar, bounded loss (N * price), O(1) per request | Finite pool requires replenishment, storage in ConfigMap |
| **Hot wallet in sidecar** | Sign on demand, no pool management | Compromised sidecar = drained wallet, unbounded loss |
| **Allowance (ERC-20 approve)** | Standard pattern, no pre-signing | Unbounded spending once approved, requires revocation |
| **Permit (ERC-2612)** | Gasless approval | Still requires a signer for each permit, not supported by all tokens |
| **Payment channel** | Amortized gas, high throughput | Complex setup, requires both parties online, custom protocol |

## Decision

Pre-sign a **bounded batch of ERC-3009 `TransferWithAuthorization`** vouchers using the agent wallet (via `buy.py`), store them in Kubernetes ConfigMaps, and have the `x402-buyer` sidecar pop one voucher per paid request.

## Rationale

1. **Zero signer access**: The sidecar only reads from ConfigMaps. It has no private key, no signing capability, no wallet access. The `PreSignedSigner` implements the `x402.Signer` interface by popping from a finite pool.
2. **Bounded loss**: If the sidecar is compromised or misbehaves, the maximum loss is exactly `N * price` where N is the number of pre-signed vouchers. This is decided at `buy.py buy --count N` time.
3. **O(1) per request**: Popping a voucher is a mutex-guarded array pop. No cryptographic operations at request time. No network calls for signing.
4. **Restart resilience**: The `StateStore` persists consumed nonces. On restart, the sidecar reloads the state and skips already-consumed vouchers.
5. **ConfigMap-native**: Vouchers and upstream config are standard Kubernetes ConfigMaps, managed by `buy.py` (the agent's buy skill). No custom storage backend.
6. **Separation of concerns**: The agent (`buy.py`) handles discovery, negotiation, and pre-signing. The sidecar handles only payment attachment and forwarding. LiteLLM routes via the static `paid/*` entry.

## Consequences

### Positive

- Security posture is strong: compromised sidecar has no signing capability and bounded financial exposure.
- The sidecar is stateless except for the consumed-nonce tracker. Scaling or replacing it is trivial.
- `buy.py` can pre-sign vouchers for multiple sellers, each with different prices and chains.
- The LiteLLM configuration is static (`paid/* -> :8402`); no dynamic reconfiguration needed per seller.

### Negative

- **Pool exhaustion**: When all vouchers are consumed, the sidecar returns `pre-signed auth pool exhausted`. The agent must run `buy.py` again to replenish. There is no automatic replenishment.
- **ConfigMap size limits**: Kubernetes ConfigMaps have a ~1MB limit. Each voucher is ~500 bytes of JSON, so the practical limit is ~2000 vouchers per ConfigMap. Large pools may need sharding.
- **No partial spending**: Each voucher is for a fixed amount. If the seller's price changes, existing vouchers may become invalid (underpayment) or wasteful (overpayment).
- **Nonce tracking persistence**: The `StateStore` must survive restarts. If the state file is lost, there is a risk of attempting to reuse consumed nonces (which will fail on-chain, but wastes a request).
- **Double-spend prevention is on-chain**: The ERC-3009 contract itself prevents double-spend. If two sidecars share the same pool, only the first submission of each nonce succeeds.

## SPEC References

- Section 3.5 -- Monetize Buy Side
- Section 3.5.3 -- Architecture (zero signer access, bounded spending)
- Section 3.5.4 -- Configuration (ConfigMap structure)
- Section 3.5.7 -- Error States (pool exhaustion)
- Section 7.2 -- Payment Security (bounded spending, replay protection)
