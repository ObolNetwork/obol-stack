# ADR-0001: Local-First Kubernetes Stack

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 2, 5.1, 6

## Context

Obol Stack must run on an operator's machine while still exercising real Kubernetes primitives used by agents, gateways, controllers, and tunnels.

## Decision

Use k3d as the default backend and standalone k3s as the bare-metal backend. Persist stack identity, backend choice, kubeconfig, defaults, and data under local config/data dirs.

## Consequences

- Positive: real Kubernetes APIs and CRDs are available locally.
- Positive: operator can test seller/buyer flows without cloud infra.
- Negative: Kubernetes remains an operational dependency.
- Neutral: host data paths and local-path provisioning are part of the runtime contract.
