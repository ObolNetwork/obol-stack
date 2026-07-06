---
name: agent-factory
description: "Spawn durable child Hermes agents from inside Obol Stack. Creates child namespaces, optional profile/env Secrets, Agent CRDs, and optional ServiceOffers for x402-paid child services. Use when the user says 'spawn an agent', 'create a sub-agent', 'child agent', or wants a separate long-lived service agent. For deciding WHAT agent to build, whether it's good enough to sell, and how to price it, load sub-agent-business first."
metadata: { "openclaw": { "requires": { "bins": ["python3"] } } }
---

# Agent Factory

Create durable child Hermes agents from a permissioned mother agent.

Use this when the user wants a separate long-lived service agent with isolated Kubernetes namespace, PVC-backed Hermes state, optional child wallet, optional injected environment secrets, and optional x402 `ServiceOffer`.

## Quick Start

```bash
python3 scripts/factory.py create medical-advisor \
  --model antangelmed \
  --skills medical-safety,privacy-filter,citations \
  --objective "Answer medical education questions with emergency escalation and no diagnosis." \
  --create-wallet \
  --price 0.05 \
  --pay-to 0xYourProviderWallet \
  --network base-sepolia \
  --register-name "Medical Advisor"
```

### Multiple currencies / networks (one agent, one offer)

Use repeatable `--accept` instead of `--price`/`--network` to advertise several
payment options on a single offer (the buyer picks one). `token=<symbol>` pulls
the asset metadata from the built-in registry; for an unlisted token supply it
inline with `asset=0x..,decimals=..,transfer=eip3009|permit2,eip712-name=..,eip712-version=..,symbol=..`:

```bash
python3 scripts/factory.py create bankr-analyst \
  --model openrouter/auto --skills bankr-token-analysis --create-wallet \
  --accept token=OBOL,network=ethereum,price=10 \
  --accept token=USDC,network=base,price=1,pay-to=0xColdVault \
  --description "Paid token-analysis agent"
```

Each `--accept` may carry its own `pay-to`; otherwise it inherits `--pay-to`,
which **defaults to the master Hermes agent's own wallet** — so a sub-agent
needn't provision its own wallet just to take payment (pass `--create-wallet`
only when it genuinely needs to hold/sign funds). For an unlisted token the
`asset=0x..` escape hatch needs only the address: `decimals`, `symbol`, and the
EIP-712 domain (`eip712-name`/`eip712-version`) are read from the chain
(EIP-5267) when omitted, and `transfer` defaults to `permit2`; the factory
errors and asks you to specify them if they can't be read. ERC-8004
registration uses the first option's network.

## Commands

| Command | Description |
|---------|-------------|
| `create <name>` | Create/update namespace, profile seed, optional env Secret, Agent CR, and optional ServiceOffer |
| `status <name>` | Show Agent and ServiceOffer readiness |
| `list` | List child Agent CRs across namespaces |
| `delete <name>` | Delete the child ServiceOffer only. Agent/runtime deletion remains an operator action for now |

## Notes

- Child runtime isolation is Kubernetes namespace + pod + PVC isolation.
- Hermes profile material is imported into the child pod's `/data/.hermes`.
- The profile seed Secret is named `hermes-profile-seed` and contains `profile.tar.gz`.
- Runtime environment overrides go in the optional `hermes-env` Secret.
- The factory intentionally writes deterministic resource names only.
