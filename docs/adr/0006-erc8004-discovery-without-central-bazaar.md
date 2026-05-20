# ADR-0006: ERC-8004 Discovery without Central Bazaar

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 1.1, 5.7

## Context

Obol Stack competes with centralized service catalogs by publishing native standards metadata that any indexer can consume.

## Decision

Publish ERC-8004 registration JSON at `/.well-known/agent-registration.json`, track chain registrations in `AgentIdentity`, and let operators submit registration transactions through `obol sell register`.

## Consequences

- Positive: discovery is permissionless and indexer-friendly.
- Positive: controller avoids hidden custody or gas side effects.
- Negative: pending on-chain registration can lag behind an operationally ready route.
- Neutral: storefront and `/skill.md` show `registrationPending` rather than hiding usable services.
