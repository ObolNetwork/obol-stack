# ADR-0001: Local-First Stack Runtime

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 1.3, 3.1, 5.1, 6, 7.3

## Context

Obol Stack serves operators running a full agent platform from their own machine. The system needs reproducible local cluster lifecycle control, predictable filesystem ownership, and a recovery path that does not depend on remote control planes.

## Decision

The stack remains local-first. The operator machine owns config, binaries, and persistent data, while `k3d` and `k3s` are the supported backend runtime options exposed through one `obol stack` lifecycle. Public exposure is optional and layered on top of a usable local baseline rather than required for startup.

## Consequences

- **Positive**: Startup, recovery, and inspection flows stay operator-centric and easier to reason about.
- **Negative**: Some cloud-native assumptions, such as always-on public endpoints or remote state stores, are intentionally deprioritized.
- **Neutral**: Future hosted or multi-node modes must be expressed as new phases rather than silently widening the local-first contract.
