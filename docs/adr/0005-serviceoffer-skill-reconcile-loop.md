# ADR-0005: ServiceOffer-Driven Sell-Side Reconcile Loop

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 3.5, 4.2, 5.3, 8

## Context

Sell-side publication needs declarative state, observable status, and reconciliation across Kubernetes routing, pricing, and optional registration. Prior proposals considered separate controllers or looser imperative flows.

## Decision

Sell-side publication is driven by the `ServiceOffer` custom resource and reconciled by a dedicated Go controller (`serviceoffer-controller`) deployed in the `x402` namespace. The reconcile loop advances through explicit stages that cover model readiness, upstream health, payment gate setup, route publication, optional registration, and final readiness.

## Consequences

- **Positive**: Operators get one declarative resource and one status model for sell-side lifecycle.
- **Positive**: Dedicated controller provides sub-second reconciliation latency via Kubernetes informers, independent of the agent heartbeat cadence.
- **Neutral**: Future generalized agent-authored services should extend this pattern only if they preserve explicit ownership, isolation, and stage visibility.
