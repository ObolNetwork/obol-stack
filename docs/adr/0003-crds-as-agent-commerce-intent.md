# ADR-0003: CRDs as Agent Commerce Intent

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 4, 5.3, 5.4, 5.6, 5.7

## Context

CLI commands, agents, controllers, and tests need a shared source of truth for services, purchases, identities, and child agents.

## Decision

Represent durable intent as Kubernetes CRDs: `ServiceOffer`, `PurchaseRequest`, `Agent`, `AgentIdentity`, and `RegistrationRequest`. CLIs apply spec; controllers own status and child resources.

## Consequences

- Positive: agents and humans can inspect and mutate intent with standard K8s tools.
- Positive: controllers can converge status after restarts.
- Negative: CRD schema changes must be carefully migrated.
- Neutral: host-side helpers may seed files, but runtime readiness is controller-owned.
