---
name: buy-inference
description: "Buy remote inference from x402-gated endpoints via a risk-isolated payment sidecar. Pre-signs bounded payment authorizations, declares them through `PurchaseRequest`, and exposes purchased models through the static LiteLLM namespace `paid/<remote-model>`. Zero signer access at runtime — spending is capped by design."
metadata: { "openclaw": { "emoji": "\ud83d\uded2", "requires": { "bins": ["python3"] } } }
---

# Buy Inference

Purchase access to remote x402-gated inference endpoints using a risk-isolated sidecar architecture. The agent pre-signs a bounded batch of USDC payment authorizations, embeds them in a `PurchaseRequest` CR in its own namespace, and lets the controller publish buyer config/auth files into `llm`. A lean Go proxy (`x402-buyer`) handles payments at runtime with zero signer access — max loss = N x price.

## When to Use

- Probing an endpoint to check pricing before buying
- Purchasing access to a remote model (pre-signs auths, creates a `PurchaseRequest`, exposes `paid/<remote-model>`)
- Manually topping up an existing purchase by re-running `buy <same-name>`
- Listing purchased providers and remaining auth counts
- Checking USDC balance before buying
- Inspecting the live sidecar status for remaining/spent auths

## When NOT to Use

- Selling your own services — use `monetize`
- Discovering agents without buying — use `discovery`
- Signing transactions directly — use `ethereum-local-wallet`
- Cluster diagnostics — use `obol-stack`

## Quick Start

```bash
# Probe an endpoint to see its pricing
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py probe https://seller.example.com/services/my-model/v1/chat/completions

# Probe with the concrete remote model when the seller validates model IDs
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py probe https://seller.example.com/services/my-model/v1/chat/completions --model qwen3.5:35b

# Buy access (probes, pre-signs auths, creates/updates a PurchaseRequest)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py buy remote-qwen \
  --endpoint https://seller.example.com/services/my-model \
  --model qwen3.5:35b

# Buy with agent-managed auto-refill intent
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py buy remote-qwen \
  --endpoint https://seller.example.com/services/my-model \
  --model qwen3.5:35b \
  --count 100 \
  --auto-refill \
  --refill-threshold 20 \
  --refill-count 50

# Manual top-up on the same purchase name
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py buy remote-qwen \
  --endpoint https://seller.example.com/services/my-model \
  --model qwen3.5:35b \
  --count 25

# List purchased providers + remaining auths
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py list

# Check sidecar health + remaining auths
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py status remote-qwen

# Reconcile auto-refill policies (heartbeat / cron entrypoint)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py process --all

# Check your USDC balance
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py balance

# Compatibility alias for the same reconcile loop
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py maintain
```

## Commands

| Command | Description |
|---------|-------------|
| `probe <endpoint-url> [--model <id>]` | Send request without payment, parse 402 response for pricing |
| `buy <name> --endpoint <url> --model <id> [--budget N] [--count N]` | Pre-sign auths, create/update `PurchaseRequest`, expose `paid/<model>` |
| `process <name> | --all` | Reconcile `autoRefill` policies against live `x402-buyer` status |
| `list` | List purchased providers + remaining auth counts |
| `status <name>` | Check sidecar pod status + remaining auths |
| `balance [--chain <network>]` | Check agent's USDC balance via eRPC |

## Surfaces

### Human / foreground surface

Use these when a human operator or foreground agent is actively deciding what
to buy:

- `probe` — inspect seller pricing
- `buy <new-name>` — acquire a new purchase
- `buy <same-name>` — manual top-up for that purchase
- `status`, `list`, `balance` — inspect live runtime state

### Agent / automation surface

Use this when Hermes or OpenClaw is maintaining existing purchases in the
background:

- `process --all` — maintenance reconcile loop for `autoRefill`

Current controller-mode limitation:
- `process --all` is the intended heartbeat / cron entrypoint for Hermes or OpenClaw.
- Re-running `buy <same-name>` is the manual top-up path.
- Only one active purchase may own a given `paid/<remote-model>` alias at a time.
- Manual `refill` is still not a first-class command.
- `remove` is still not a first-class command; deleting the `PurchaseRequest`
  directly now enters a drain-first lifecycle instead of tearing the route down
  immediately.

## Automation Recipes

### OpenClaw heartbeat recipe

Use the absolute script path inside the pod. Do not rely on `cd ... && ...`
shell wrapping.

```bash
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py process --all
```

Tell the agent to schedule that as its maintenance loop only when at least one
`PurchaseRequest.spec.autoRefill.enabled=true` purchase exists.

### Hermes cron recipe

Hermes already has a cron scheduler. The maintenance job should load the
buy-inference skill and run the same reconcile primitive on a schedule.

CLI example:

```bash
hermes cron create "every 5m" \
  "Reconcile existing x402 PurchaseRequests. Use the buy-inference skill and run python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py process --all. Report only errors or state changes." \
  --name "x402 buy reconcile" \
  --skill buy-inference
```

Python API example:

```python
from cron.jobs import create_job

create_job(
    prompt="Reconcile existing x402 PurchaseRequests. Use the buy-inference skill and run python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py process --all. Report only errors or state changes.",
    schedule="every 5m",
    name="x402 buy reconcile",
    skills=["buy-inference"],
)
```

### Surface map

```mermaid
flowchart LR
  subgraph Human["Human / Foreground"]
    H1["probe"]
    H2["buy <new-name>"]
    H3["buy <same-name>"]
    H4["status / list / balance"]
  end

  subgraph Agent["Agent / Background"]
    A1["Hermes cron or OpenClaw heartbeat"]
    A2["process --all"]
  end

  subgraph Control["Control Plane"]
    PR["PurchaseRequest"]
    RS["remote-signer"]
  end

  subgraph Runtime["Runtime Plane"]
    C["serviceoffer-controller"]
    X["x402-buyer /status"]
    L["LiteLLM paid/<model>"]
  end

  S["Seller"]

  H1 --> S
  H2 --> RS
  H2 --> PR
  H3 --> RS
  H3 --> PR
  H4 --> X
  A1 --> A2
  A2 --> X
  A2 --> RS
  A2 --> PR
  PR --> C
  C --> X
  C --> L
  L --> X
  X --> S
```

## How It Works

1. **Probe**: Sends a request without payment. The x402 gate returns `402 Payment Required` with pricing info (`payTo`, `network`, `amount`; legacy sellers may still use `maxAmountRequired`).

2. **Pre-sign**: The agent signs N ERC-3009 `TransferWithAuthorization` vouchers via the remote-signer. Each voucher has a random nonce and is single-use (consumed on-chain when the facilitator settles).

3. **Delete / drain behavior**: deleting a `PurchaseRequest` does not
   immediately remove the paid route if there are still auths remaining. The
   controller marks the purchase as draining, keeps `paid/<model>` live, and
   only tears the route down after the remaining auth pool reaches zero. While
   draining:
   - `buy.py list` and `buy.py status <name>` still show the purchase
   - the purchase still owns `paid/<model>`
   - a second `buy <other-name> --model <same-model>` is still rejected

4. **Final cleanup**: once `remaining == 0`, the controller removes the buyer
   config/auth material, removes `paid/<model>` if there is no other owner,
   reloads the buyer sidecar, and clears the finalizer so the CR can disappear.

3. **Declare**: `buy.py` creates or updates a `PurchaseRequest` in the agent namespace with the pre-signed authorizations embedded in `spec.preSignedAuths`. When requested, it also stores `spec.autoRefill` intent on the CR.

4. **Reconcile**: The controller validates pricing, writes per-upstream buyer config/auth files into the `x402-buyer-config` and `x402-buyer-auths` ConfigMaps in `llm`, and keeps the paid model route available in LiteLLM.

5. **Runtime mount**: A lean Go sidecar (`x402-buyer`) already runs inside the existing `litellm` pod in the `llm` namespace. It mounts both ConfigMaps and serves as an OpenAI-compatible reverse proxy on `127.0.0.1:8402`.

6. **Wire**: LiteLLM keeps one static wildcard route: `paid/* -> openai/* -> 127.0.0.1:8402/v1`. The controller also adds explicit paid-model entries when required so models with colons resolve reliably. The public model name is always `paid/<remote-model>`.

7. **Runtime**: On each request through the sidecar:
   - Sidecar forwards to upstream seller
   - If 402 → pops one pre-signed auth from pool → builds X-PAYMENT header → retries
   - Seller verifies payment via facilitator → returns 200 + inference result
   - Sidecar confirms local nonce consumption after a successful paid upstream response; if the paid retry still fails, the held auth is released back to the local pool
   - Sidecar has zero signer access — it only uses pre-signed vouchers

8. **Heartbeat**: `buy.py process --all` reads live `x402-buyer` `/status`,
   checks each `PurchaseRequest.spec.autoRefill` policy, signs a fresh batch
   when remaining auths are at or below the configured threshold, trims spent
   history from `spec.preSignedAuths`, and patches the CR. The controller then
   republishes the refreshed pool into `llm`.

## Architecture

```mermaid
flowchart LR
  subgraph Agent["Agent Namespace"]
    B["buy.py"]
    RS["remote-signer"]
    PR["PurchaseRequest"]
  end

  subgraph Runtime["llm Namespace"]
    C["serviceoffer-controller"]
    L["LiteLLM"]
    X["x402-buyer"]
  end

  S["Seller Endpoint"]

  B -->|"probe"| S
  B -->|"sign auths"| RS
  B -->|"create/update"| PR
  B -->|"process --all"| PR
  PR --> C
  C -->|"write config/auth pool"| X
  C -->|"publish paid/<model>"| L
  L -->|"paid/<model>"| X
  X -->|"402 retry with X-PAYMENT"| S
```

## Security Properties

- **Zero signer access**: The sidecar reads pre-signed auths from ConfigMaps — no remote-signer access
- **Bounded spending**: Max loss = N x price (where N = number of pre-signed auths)
- **Risk isolation**: If sidecar crashes, LiteLLM routes to other providers (Ollama, etc.) — inference unaffected
- **Single-use vouchers**: Each auth is consumed on-chain when settled — no replay

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REMOTE_SIGNER_URL` | `http://remote-signer:9000` | Remote-signer REST API |
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `base` | Default chain for balance queries |

## Constraints

- **Python stdlib only** — no pip install, no external packages
- **Requires remote-signer** — must have agent wallet provisioned via `obol openclaw onboard`
- **Requires x402-buyer image** — `ghcr.io/obolnetwork/x402-buyer:latest` must be available in cluster
- **Static public interface** — purchased models are always addressed as `paid/<remote-model>`
- **Max 1000 auths per batch** — signing takes ~50s at 1000; this cap applies to both `buy` and each refill batch driven by `process --all`
- **Live state comes from the sidecar** — use `x402-buyer` `/status` via `status`, `list`, or `process --all`, not only `PurchaseRequest.status`
- **Auth pool is finite unless auto-refill is configured** — enable `autoRefill` on the CR and run `process --all` from a scheduler to keep it topped up

## Full Buy Flow (Discovery → Probe → Buy → Use)

This is the complete journey from discovering a seller to using purchased inference:

### Step 1: Discover sellers on-chain (use `discovery` skill)

```bash
# Search the ERC-8004 registry for recently registered agents
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/discovery/scripts/discovery.py search --chain base-sepolia

# Fetch a candidate's registration JSON to check x402Support and services
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/discovery/scripts/discovery.py uri <agent-id> --chain base-sepolia
```

Look for agents with `"x402Support": true` and a `"web"` service endpoint.

### Step 2: Probe the seller endpoint

```bash
# Send an unauthenticated request to get 402 pricing
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py probe <service-endpoint> --model <model-name>
```

This returns the seller's pricing: `payTo`, `network`, `price`, and `asset` (USDC contract).

### Step 3: Check balance and buy

```bash
# Check USDC balance
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py balance --chain base-sepolia

# Buy access (pre-sign auths, create PurchaseRequest, wait for controller reconciliation)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py buy <friendly-name> \
  --endpoint <service-endpoint> \
  --model <model-name> \
  --count 20
```

### Step 4: Use the purchased model

After buying, the model is available through LiteLLM as `paid/<model-name>`:

```bash
curl -X POST http://litellm.llm.svc.cluster.local:4000/v1/chat/completions \
  -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "paid/<model-name>", "messages": [{"role": "user", "content": "hello"}]}'
```

The `paid/` prefix routes through the x402-buyer sidecar, which transparently attaches payment headers.

### Step 5: Monitor purchases

```bash
# Check remaining auths
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py list

# Check one purchased upstream in detail
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py status <friendly-name>

# Reconcile auto-refill intent (what the heartbeat should run)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-inference/scripts/buy.py process --all
```

Manual `refill` and `remove` commands are still not available in the current
controller-based path. `maintain` is now only a compatibility alias for
`process --all`.

## References

- `references/purchase-request-spec.md` — Full `PurchaseRequest` CRD field reference
- `references/x402-buyer-api.md` — Wire formats for 402 responses, X-PAYMENT headers, and sidecar config
- See also: `discovery` skill for finding sellers on the ERC-8004 registry
