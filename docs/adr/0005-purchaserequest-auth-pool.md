# ADR-0005: PurchaseRequest as Bounded Auth Pool

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 1.1, 4.3, 5.6, 6

## Context

Buyer agents need to call paid remote inference without giving runtime sidecars their signing keys.

## Decision

Treat `PurchaseRequest` as a bounded pool of pre-signed x402 authorizations. `buy.py` signs locally, embeds auths in the CR, and the controller writes sidecar config/auth material. It is not escrow.

## Consequences

- Positive: maximum buyer loss is bounded by signed auth count and price.
- Positive: x402-buyer has zero signer access.
- Negative: refill requires agent-managed signing, not controller automation.
- Neutral: status counters are reconciled snapshots; live state comes from sidecar `/status`.
