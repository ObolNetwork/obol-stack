# ADR-0002: Central LiteLLM Gateway

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 2.3, 3.2, 4.3, 7.1

## Context

The stack needs one consistent model routing surface for local Ollama models, cloud APIs, and paid remote models. Per-instance provider wiring leads to duplicated credentials, stale model lists, and inconsistent behavior across agents.

## Decision

LiteLLM is the central cluster-wide model gateway. OpenClaw instances and operator flows route through LiteLLM for normal model access, while provider credentials and static paid-route configuration remain centralized in the `llm` namespace.

## Consequences

- **Positive**: Model routing becomes uniform across operator, agent, and buyer paths.
- **Negative**: LiteLLM readiness becomes a critical dependency for most inference surfaces.
- **Neutral**: Direct-to-provider experiments remain possible, but they are exceptions to the main platform contract rather than the default architecture.
