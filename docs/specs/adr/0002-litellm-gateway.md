# ADR-0002: LiteLLM as Unified LLM Gateway

**Status:** Accepted
**Date:** 2026-03-27

## Context

The OpenClaw agent and cluster services need to access LLM inference from multiple providers:

- **Ollama** (local, no API key) for on-device models like qwen3.5:9b.
- **Anthropic** (cloud, API key) for Claude models.
- **OpenAI** (cloud, API key) for GPT models.
- **Paid remote sellers** (x402-gated) for purchased inference from other agents.

The application layer (OpenClaw, LiteLLM overlays, downstream apps) should not need to know which provider serves a given model. A single OpenAI-compatible endpoint simplifies routing, auth, and configuration.

Alternatives considered:

| Option | Pros | Cons |
|--------|------|------|
| **LiteLLM** | OpenAI-compatible proxy, multi-provider, ConfigMap-driven, wildcard routing | `drop_params` behavior can silently discard unsupported fields, restart required for config changes |
| **Direct provider SDKs** | No proxy overhead, full parameter control | Each consumer must handle auth + routing per provider, no unified API |
| **vLLM / llm-d** | High-performance serving, GPU scheduling | Different abstraction layer (model serving, not routing); evaluated and rejected for this role |
| **Custom proxy** | Full control | Maintenance burden, reimplements LiteLLM's model routing |

## Decision

Use **LiteLLM** (deployed as a Kubernetes Deployment in the `llm` namespace on port 4000) as the unified LLM gateway for all inference routing.

## Rationale

1. **Single API surface**: All consumers (OpenClaw agent, apps, tests) use `http://litellm.llm.svc:4000/v1` with standard OpenAI client libraries.
2. **Multi-provider routing**: LiteLLM's `model_list` supports exact names (Ollama models), wildcards (`anthropic/*`, `openai/*`), and catch-alls (`paid/*`).
3. **ConfigMap-driven**: The `litellm-config` ConfigMap and `litellm-secrets` Secret are patched by Go code (`internal/model/model.go`) without forking LiteLLM.
4. **Auto-configuration**: During `obol stack up`, `autoConfigureLLM()` detects Ollama models and cloud API keys, patches config + secret, and performs a single restart.
5. **Paid inference integration**: The static `paid/*` route forwards to the `x402-buyer` sidecar at `http://127.0.0.1:8402/v1`, keeping the LiteLLM image unmodified.
6. **Per-instance overlay**: `buildLiteLLMRoutedOverlay()` reuses the "ollama" provider slot pointing at `litellm.llm.svc:4000/v1`, enabling app-level model aliasing without additional infrastructure.

## Consequences

### Positive

- Unified endpoint for all LLM access -- no provider-specific client code needed.
- Adding a new provider is a ConfigMap patch + Secret update + restart.
- Paid inference works through vanilla LiteLLM with a static route to the buyer sidecar.
- `dangerouslyDisableDeviceAuth` is enabled for Traefik-proxied access, avoiding auth double-gate.

### Negative

- **`drop_params` risk**: LiteLLM silently drops parameters not supported by the target provider. This can cause subtle behavior differences between providers for the same model name.
- **Restart required**: Config changes require a Deployment restart (10-30 second latency). There is no live-reload mechanism.
- **Single point of failure**: All inference routes through one LiteLLM pod. Pod failure means no inference until restart.
- **ConfigMap complexity**: The `litellm-config` ConfigMap grows with every provider and model. Patching logic in `internal/model/model.go` must handle merges carefully.
- **Version coupling**: Pinned LiteLLM image (v1.82.3 as of writing, pinned for supply chain security) must be updated when new provider features are needed.

## SPEC References

- Section 3.2 -- LLM Routing
- Section 3.2.4 -- Logic (autoConfigureLLM, paid inference routing)
- Section 3.2.5 -- LiteLLM Config Structure
- Section 3.5 -- Monetize Buy Side (paid/* route)
- Section 3.6.4 -- Cloud Provider Detection
