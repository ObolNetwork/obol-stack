# ADR-0003: Distinct Network Domains

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 3.3, 3.5, 3.6, 4.2

## Context

The platform touches several network concepts that look similar but are not interchangeable: installable local networks, remote RPC aliases, sell-side payment chains, and ERC-8004 registration networks. Previous spec work blurred those domains and created false support claims.

## Decision

The spec and CLI contract must keep these network domains separate. A chain appearing in one subsystem, such as the low-level x402 resolver, does not automatically expand support claims for other subsystems. Multi-chain sell-side support may only be documented once the CLI, payment verifier, and registration surfaces agree on the same contract.

## Consequences

- **Positive**: Support claims stay factual and users can tell which network surface they are configuring.
- **Negative**: Documentation is less compact because one generic “supported networks” list is intentionally avoided.
- **Neutral**: Future multi-chain expansion requires aligned implementation work across several modules before the spec can widen the contract.
