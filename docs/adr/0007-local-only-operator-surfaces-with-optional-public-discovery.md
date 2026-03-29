# ADR-0007: Local-Only Operator Surfaces with Optional Public Discovery

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 3.7, 4.3, 7.3

## Context

The stack needs public reachability for paid services and optional discovery, but it also exposes sensitive operator surfaces such as the frontend, eRPC gateway, and monitoring.

## Decision

Operator surfaces remain local-only by default. Tunnel exposure is scoped to the routes that explicitly need it, and public discovery metadata follows the current tunnel address rather than widening local control-plane surfaces.

## Consequences

- **Positive**: Public monetization and discovery can coexist with conservative operator safety boundaries.
- **Negative**: Public operator dashboards and remote admin UX remain out of scope for the current contract.
- **Neutral**: If public operator surfaces are ever introduced, they require an explicit architectural change rather than an incremental tunnel tweak.
