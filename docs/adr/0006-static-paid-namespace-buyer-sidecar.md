# ADR-0006: Static Paid Namespace with a Bounded-Risk Buyer Sidecar

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 3.2.3, 3.6, 7.2, 7.4

## Context

Remote paid inference needs a stable buyer-facing model contract, but giving the request path direct access to live signing authority would create a large security and spend risk.

## Decision

Paid remote models are exposed through a static `paid/*` namespace at LiteLLM and fulfilled by a buyer sidecar that holds only a bounded pool of pre-signed authorizations. The sidecar handles payment retries and forwarding without receiving live signer authority.

## Consequences

- **Positive**: The buyer path is easier to integrate and materially safer than a live-signing proxy.
- **Negative**: Capacity is limited by the pre-signed auth pool and requires replenishment workflows.
- **Neutral**: Observability for auth exhaustion and payment retries becomes a first-class operational concern.
