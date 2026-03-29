# ADR-0009: Phase 2 Exact Metering After the Pre-Request Payment Gate

**Date**: 2026-03-29
**Status**: Proposed

**Impacts**: SPEC Sections 3.5.5, 4.5, 10

## Context

The PR288 baseline supports `perMTok` and `perHour` pricing, but current enforcement relies on approximation before execution. The platform needs a clearer future direction for exact post-response accounting without discarding the existing pre-request payment gate.

## Decision

Phase 2 exact metering, where implemented, should augment the current pre-request payment gate rather than replace it. Authorization remains the entry check, while measured usage becomes a post-response accounting and observability concern for supported protocols.

## Consequences

- **Positive**: The current gatekeeping model remains intact while exact accounting improves fidelity where it is technically feasible.
- **Negative**: The platform must operate two related billing surfaces during transition: pre-request authorization and post-response accounting.
- **Neutral**: Streaming and non-OpenAI-compatible formats may continue to use approximation until a stronger metering contract exists.
