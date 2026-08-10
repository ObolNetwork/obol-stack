# ADR-0002: LiteLLM as Model Gateway

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 5.2, 5.6

## Context

Agents need a stable OpenAI-compatible API across host Ollama, cloud providers, and paid remote inference.

## Decision

Use LiteLLM in the `llm` namespace as the model gateway. Keep a static `paid/*` route to the local `x402-buyer` sidecar and add concrete provider or purchased models through config/controller updates.

## Consequences

- Positive: Hermes can use one OpenAI-compatible provider path.
- Positive: purchased remote inference is called as `paid/<model>`.
- Negative: the Obol LiteLLM fork is part of the deployment contract.
- Neutral: x402-buyer consumed state currently requires LiteLLM replicas=1.
