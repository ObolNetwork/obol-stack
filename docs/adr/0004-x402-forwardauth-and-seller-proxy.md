# ADR-0004: x402 ForwardAuth and Seller Proxy

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 3.3, 5.4, 6

## Context

Public service routes need an unpaid discovery response, payment verification, and successful-response settlement semantics.

## Decision

Use `x402-verifier` as both Traefik ForwardAuth gate and seller-owned proxy. ForwardAuth remains verify-only for legacy/gateway integration; the seller proxy path settles after upstream success.

## Consequences

- Positive: unpaid probes receive standard x402 402 responses.
- Positive: seller proxy can settle only after upstream success.
- Negative: direct raw `X-PAYMENT` through ForwardAuth is not a complete production path.
- Neutral: route rules are built dynamically from `ServiceOffer` status.
