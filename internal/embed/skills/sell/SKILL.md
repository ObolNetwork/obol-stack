---
name: sell
description: "Sell access to services via x402 payment gating. Create ServiceOffer CRDs that automatically health-check upstreams, create payment-gated routes, and optionally pull models and register on ERC-8004. Supports inference, HTTP, and fine-tuning service types. Use for ServiceOffer plumbing: listing offers, checking/waiting on reconciliation status, creating or deleting offers. For the guided end-to-end walkthrough use monetize-guide; for selling a child agent's turns use sub-agent-business + agent-factory."
metadata: { "openclaw": { "emoji": "\ud83d\udcb0", "requires": { "bins": ["python3"] } } }
---

# Sell

Sell access to services via ServiceOffer custom resources. Each ServiceOffer describes a service to expose publicly with x402 micropayments (USDC via EIP-3009 or OBOL via Permit2, selected with `--token`). The cluster's `serviceoffer-controller` performs reconciliation; `monetize.py process` now waits for controller convergence and refreshes `/skill.md`.

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
  --model qwen3.5:9b \
  --runtime ollama \
  --upstream ollama \
  --namespace llm \
  --port 11434 \
  --per-request 0.001 \
  --network base-sepolia \
  --pay-to 0xYourWalletAddress

# Check status of an offer
python3 scripts/monetize.py status my-inference --namespace llm

# Process all pending offers (waits for controller convergence)
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
| `process <name> --namespace <ns>` | Wait for a single offer to converge |
| `process --all` | Wait for all non-Ready offers to converge |
| `delete <name> --namespace <ns>` | Delete an offer and its owned resources |

## Reconciliation Flow

The `serviceoffer-controller` drives these stages:

1. **ModelReady** — Pull the model via Ollama API (if runtime is ollama)
2. **UpstreamHealthy** — Health-check the upstream service
3. **PaymentGateReady** — Create a Traefik ForwardAuth Middleware pointing at x402-verifier
4. **RoutePublished** — Create a Gateway API HTTPRoute with the middleware
5. **Registered** — (Optional) create a `RegistrationRequest`; the controller publishes `/.well-known/agent-registration.json` and performs ERC-8004 side effects when configured
6. **Ready** — All conditions met, service is live

The x402-verifier watches published ServiceOffers directly, so deleting or pausing the offer removes enforcement without a separate rendered route object.

## Payment (x402-aligned)

- `payment.payTo`: USDC recipient wallet address (x402: payTo)
- `payment.network`: Chain for payments (e.g., base-sepolia, base)
- `payment.price.perRequest`: Flat per-request price in USDC
- `payment.price.perMTok`: Per-million-tokens price in USDC (inference)
- `payment.price.perHour`: Per-compute-hour price in USDC (fine-tuning)
- `payment.scheme`: Payment scheme (default: exact)

Phase 1 pricing behavior:

- `perRequest` or `price` wins if explicitly provided
- otherwise `perMTok` is accepted and converted to a temporary enforced request price using `perMTok / 1000`
- the fixed approximation input is `approxTokensPerRequest = 1000`
- the original `perMTok` value is still persisted in pricing metadata and shown in status output

## Architecture

```
ServiceOffer CR (obol.org/v1alpha1)
    |
    +-- serviceoffer-controller
    |     +-- Health-check upstream
    |     +-- Create Middleware (ForwardAuth -> x402-verifier)
    |     +-- Create HTTPRoute (path -> upstream, with middleware)
    |     +-- Register on-chain (ERC-8004, optional)
    |
    +-- x402-verifier
    |     +-- Watch published ServiceOffers
    |     +-- Derive in-memory pricing rules + upstream auth
    |
    +-- monetize.py process
          +-- Wait for controller convergence (no-op — controller owns all resources)
```

## References

- `references/serviceoffer-spec.md` — Full CRD field reference
- `references/registrationrequest-spec.md` — Child CRD used for publication and ERC-8004 side effects
- `references/x402-pricing.md` — x402 pricing model details
