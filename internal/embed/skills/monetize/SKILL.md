---
name: monetize
description: "Monetize compute services via x402 payment gating. Create ServiceOffer CRDs that automatically pull models, health-check upstreams, create payment-gated routes, and optionally register on ERC-8004. Manages the full lifecycle: create, process, list, status, delete."
metadata: { "openclaw": { "emoji": "\ud83d\udcb0", "requires": { "bins": ["python3"] } } }
---

# Monetize

Manage payment-gated compute services via ServiceOffer custom resources. Each ServiceOffer describes a service to expose publicly with x402 micropayments — the reconciliation script handles model pulling, health-checking, route creation, and payment middleware.

## When to Use

- Exposing a local Ollama model for paid inference
- Creating payment-gated routes for any upstream service
- Checking the status of monetized services
- Listing or deleting existing service offers
- Processing pending offers that haven't been fully reconciled

## When NOT to Use

- Read-only Ethereum queries — use `ethereum-networks`
- Signing transactions — use `ethereum-local-wallet`
- Cluster diagnostics — use `obol-stack`

## Quick Start

```bash
# List all service offers across namespaces
python3 scripts/monetize.py list

# Create a new offer to monetize a local Ollama model
python3 scripts/monetize.py create my-inference \
  --model qwen3:8b \
  --runtime ollama \
  --upstream ollama \
  --namespace llm \
  --port 11434 \
  --price 0.50 \
  --unit MTok \
  --chain base-sepolia \
  --wallet 0xYourWalletAddress

# Check status of an offer
python3 scripts/monetize.py status my-inference --namespace llm

# Process all pending offers (runs reconciliation)
python3 scripts/monetize.py process --all

# Process a single offer
python3 scripts/monetize.py process my-inference --namespace llm

# Delete an offer (cascades Middleware + HTTPRoute via OwnerRef)
python3 scripts/monetize.py delete my-inference --namespace llm
```

## Commands

| Command | Description |
|---------|-------------|
| `list` | List all ServiceOffer CRs across namespaces |
| `status <name> --namespace <ns>` | Show conditions and endpoint for one offer |
| `create <name> --model ... --namespace ...` | Create a new ServiceOffer CR |
| `process <name> --namespace <ns>` | Reconcile a single offer |
| `process --all` | Reconcile all non-Ready offers |
| `delete <name> --namespace <ns>` | Delete an offer and its owned resources |

## Reconciliation Flow

When `process` runs on an offer, it steps through these stages:

1. **ModelReady** — Pull the model via Ollama API (if runtime is ollama)
2. **UpstreamHealthy** — Health-check the upstream service
3. **PaymentGateReady** — Create a Traefik ForwardAuth Middleware pointing at x402-verifier AND add a pricing route to the x402-pricing ConfigMap so the verifier returns 402 for requests without payment
4. **RoutePublished** — Create a Gateway API HTTPRoute with the middleware
5. **Registered** — (Optional) Register on ERC-8004 via the local wallet
6. **Ready** — All conditions met, service is live

When `delete` runs, it also removes the pricing route from the x402-pricing ConfigMap.

## Pricing

- `amount`: Price per unit (e.g., "0.50")
- `unit`: Billing unit — `MTok` (per million tokens) or `request` (per request)
- `currency`: Payment currency (default: USDC)
- `chain`: Blockchain for payments (e.g., base-sepolia, base)

## Architecture

```
ServiceOffer CR (obol.org/v1alpha1)
    |
    v
monetize.py process
    |
    +-- Pull model (Ollama API)
    +-- Health-check upstream
    +-- Create Middleware (ForwardAuth -> x402-verifier)
    +-- Create HTTPRoute (path -> upstream, with middleware)
    +-- Register on-chain (ERC-8004, optional)
    |
    v
Status conditions updated on CR
```

## References

- `references/serviceoffer-spec.md` — Full CRD field reference
- `references/x402-pricing.md` — x402 pricing model details
