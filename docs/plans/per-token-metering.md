# Per-Token Metering Plan

## Scope

This document defines phase 2 of issue 258: exact seller-side token metering
for paid inference offers, with Prometheus-native monitoring and a lightweight
status surface on `ServiceOffer`.

Phase 1 is already deployed separately:

- `perMTok` is accepted by the sell flows
- the enforced x402 charge is approximated as `perMTok / 1000`
- the source pricing metadata is persisted on each pricing route
- buyer and verifier expose operational Prometheus metrics

This document covers how to replace that approximation for non-streaming
OpenAI-compatible chat completions.

## Goals

- Meter actual prompt, completion, and total token usage for paid inference
  routes.
- Convert measured usage into estimated USDC using the seller's `perMTok`.
- Expose seller-side metrics through Prometheus.
- Surface roll-up usage on `ServiceOffer.status.usage`.
- Keep the verifier as the pre-request payment gate.

## Non-Goals

- Post-pay settlement or escrow.
- Exact metering for streaming responses.
- Exact metering for non-OpenAI response formats.
- Buyer-side billing authority. Buyer token telemetry remains observational.

## Request Flow

```text
client
  -> Traefik HTTPRoute
  -> x402-verifier (pre-request payment gate)
  -> x402-meter
  -> upstream inference service
  -> x402-meter parses usage.total_tokens
  -> response returned to client
  -> x402-meter exports Prometheus metrics and updates ServiceOffer.status.usage
```

Key point:

- `x402-verifier` still decides whether a request may proceed.
- `x402-meter` becomes the source of truth for exact usage accounting after the
  upstream response is known.

## Config Schema

`x402-meter` is configured per monetized route.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: x402-meter-config
  namespace: x402
data:
  config.yaml: |
    routes:
      - pattern: /services/my-qwen/v1/chat/completions
        offerNamespace: llm
        offerName: my-qwen
        upstreamURL: http://ollama.llm.svc.cluster.local:11434
        upstreamAuth: ""
        perMTok: "1.25"
        priceModel: perMTok
        responseFormat: openai-chat-completions
```

Required fields:

- `pattern`
- `offerNamespace`
- `offerName`
- `upstreamURL`
- `perMTok`

Optional fields:

- `upstreamAuth`
- `responseFormat`

## Status Schema

`ServiceOffer.status.usage` is extended with a seller-side rollup:

```yaml
status:
  usage:
    requests: 124
    promptTokens: 102400
    completionTokens: 18432
    totalTokens: 120832
    estimatedUSDC: "0.15104"
    lastUpdated: "2026-03-06T12:34:56Z"
```

Rules:

- `estimatedUSDC` is derived from `totalTokens / 1_000_000 * perMTok`
- values are monotonic rollups, not per-request histories
- writes should be batched to avoid excessive CR status churn

## Prometheus Metrics

`x402-meter` exposes `/metrics` and is scraped through a `ServiceMonitor`.

Metric set:

- `obol_x402_meter_requests_total{offer_namespace,offer_name,route}`
- `obol_x402_meter_prompt_tokens_total{offer_namespace,offer_name,route}`
- `obol_x402_meter_completion_tokens_total{offer_namespace,offer_name,route}`
- `obol_x402_meter_total_tokens_total{offer_namespace,offer_name,route}`
- `obol_x402_meter_estimated_usdc_total{offer_namespace,offer_name,route}`
- `obol_x402_meter_parse_failures_total{offer_namespace,offer_name,route}`

Label guidance:

- keep labels limited to offer identity and route pattern
- do not label by user, wallet, or request id

## Buyer-Side Observational Metrics

Buyer-side metrics remain separate from billing:

- `x402-buyer` continues exposing request, payment, auth pool, and active model
  mapping metrics.
- A later extension may parse `usage.total_tokens` from remote seller responses
  and emit observational counters keyed by `upstream` and `remote_model`.
- Disagreement between buyer-observed tokens and seller-billed tokens should be
  treated as an alerting or debugging signal, not a settlement input.

## Rollout Plan

1. Deploy `x402-meter` behind the verifier for one non-streaming paid route.
2. Validate token parsing and Prometheus scrape health.
3. Enable `ServiceOffer.status.usage` updates with rate limiting.
4. Switch sell-side status output from approximation-first to exact-usage-first
   whenever meter data is present.
5. Keep the phase-1 `perMTok / 1000` approximation as a fallback for routes not
   yet migrated to `x402-meter`.

## Failure Handling

- If the response body cannot be parsed, increment
  `obol_x402_meter_parse_failures_total` and return the upstream response
  unchanged.
- If the upstream omits `usage.total_tokens`, do not synthesize exact billing.
- If status updates fail, metrics must still be emitted.
- If Prometheus is unavailable, request serving must continue.

## Open Questions

- Whether streamed responses should be handled with token trailers, chunk
  aggregation, or remain explicitly unsupported.
- Whether meter state should be derived solely from Prometheus counters or also
  persisted locally for faster CR status reconciliation.
