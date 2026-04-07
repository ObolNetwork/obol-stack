# Seller Onboarding Prompt

Use this prompt with Claude Code to set up monetization for any service on obol-stack. Copy and adapt it for your specific use case.

---

## Prompt Template

```
You are helping me monetize a service on my obol-stack cluster.

## My Setup

- Cluster: running (obol stack up completed)
- Service type: [inference / http]
- Service details: [describe what you're selling]

## What I Need

1. Check my cluster is ready (nodes, agent RBAC, wallet)
2. Detect available [models / services] and recommend what to sell
3. Research comparable pricing on the ERC-8004 registry
4. Propose a price and ask me to confirm
5. Create the ServiceOffer and wait for it to reach Ready
6. Verify the endpoint returns a proper 402 to unauthenticated requests
7. Show me the final public URL and how buyers interact with it

## Constraints

- Use the `monetize-guide` skill for the end-to-end flow
- Always ask me to confirm pricing before proceeding
- Register the service on ERC-8004 for on-chain discovery
- Use OASF skills/domains that match my service type
```

---

## Service Type Guidance

### Inference (Ollama models)

For monetizing a local LLM:

- **Upstream**: Ollama at `localhost:11434` (auto-detected)
- **Pricing model**: `--per-request` or `--per-mtok`
- **Registration skills**: `natural_language_processing/natural_language_generation/text_completion`
- **Registration domains**: `technology/data_science`
- **Buyer interaction**: OpenAI-compatible API at `/services/<name>/v1/chat/completions`
- **Health check**: Ollama `/api/tags` (auto-configured)

### HTTP API (generic service)

For monetizing any HTTP service running in the cluster:

- **Upstream**: Kubernetes service name + port
- **Pricing model**: `--per-request`
- **Registration skills**: Choose from OASF taxonomy based on what the service does
- **Registration domains**: Choose from OASF taxonomy based on the domain
- **Buyer interaction**: REST API at `/services/<name>/*`
- **Health check**: Must have a health endpoint (default: `/health`)

### Compute (GPU hours)

For selling GPU compute time (fine-tuning, training):

- **Upstream**: Worker API (e.g., autoresearch worker at port 8080)
- **Pricing model**: `--per-hour`
- **Registration skills**: `devops_mlops/model_versioning`
- **Registration domains**: `research_and_development/scientific_research`
- **Buyer interaction**: Experiment submission API at `/services/<name>/experiment`
- **Health check**: `/health` or `/healthz`
- **Note**: `--per-hour` is approximated to per-request at 5 min/request for x402 gating

---

## Post-Sell Monitoring

After the service is live, periodically check:

```bash
# Service status and conditions
obol sell status <name> -n <namespace>

# Verify endpoint is payment-gated
obol sell test <name> -n <namespace>

# Check x402 verifier logs for payment activity
obol kubectl logs -l app=x402-verifier -n x402 --tail=20

# Check tunnel is active
obol tunnel status
```

### Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| ServiceOffer stuck at UpstreamHealthy | Upstream not reachable | Check pod is running, service exists, port is correct |
| ServiceOffer stuck at PaymentGateReady | x402 namespace not ready | Check `obol kubectl get pods -n x402` |
| 404 instead of 402 at endpoint | HTTPRoute not created | Check RoutePublished condition, verify Traefik gateway |
| 200 instead of 402 (no payment gate) | Pricing route missing | Check x402-pricing ConfigMap in x402 namespace |
| Registration failed | Wallet not funded | Non-blocking; service still works without on-chain registration |
