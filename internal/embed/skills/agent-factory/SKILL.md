---
name: agent-factory
description: "Spawn durable child Hermes agents from inside Obol Stack. Creates child namespaces, optional profile/env Secrets, Agent CRDs, and optional ServiceOffers for x402-paid child services."
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
