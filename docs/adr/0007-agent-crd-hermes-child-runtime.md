# ADR-0007: Agent CRD with Hermes Child Runtime

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 4.2, 5.3, 5.5, 6

## Context

Providers need a real product primitive for durable child agents that can later be created by a permissioned mother agent.

## Decision

Add a namespaced `Agent` CRD for Hermes child runtimes. The CLI seeds host-side `soul.md` and skills; the controller provisions Hermes resources, optional remote-signer, status, and endpoint. `ServiceOffer type=agent` references this CR.

## Consequences

- Positive: "pay for an agent turn" becomes a first-class service type.
- Positive: model, skills, and runtime can be surfaced in x402 metadata.
- Negative: skills are seeds, not sandboxes.
- Neutral: in-cluster mother-agent creation remains a Phase 1 hardening path.
