---
name: autoresearch-coordinator
description: "Coordinate distributed autoresearch experiments across GPU workers discovered via ERC-8004 and paid via x402 micropayments."
metadata: { "openclaw": { "emoji": "\ud83d\udd2c", "requires": { "bins": ["python3", "curl"] } } }
---

# Autoresearch Coordinator

Coordinate distributed autoresearch experiments across GPU workers discovered on-chain via ERC-8004 and paid per-experiment via x402 micropayments. This replaces the Ensue-based shared-memory coordinator from autoresearch-at-home with a fully decentralised discovery and payment loop built on obol-stack primitives.

## When to Use

- Discovering GPU workers advertising `devops_mlops/model_versioning` capabilities via the 8004scan public index API
- Probing worker endpoints for x402 pricing before submitting experiments
- Submitting `train.py` experiments to remote GPU workers through x402 payment gates
- Running the continuous THINK/CLAIM/RUN/PUBLISH experiment loop
- Viewing the global leaderboard of autoresearch results from worker metadata
- Coordinating multi-worker experiment campaigns

## When NOT to Use

- Selling your own GPU as a worker -- use `autoresearch-worker` (then monetize it with `obol sell http`)
- Buying generic inference (chat completions) -- use `buy-x402`
- Discovering agents without running experiments -- use `discovery`
- Signing transactions directly -- use `ethereum-local-wallet`
- Cluster diagnostics -- use `obol-stack`

## Quick Start

```bash
# Discover available GPU workers from the preferred public index API
python3 scripts/coordinate.py discover

# Discover with custom limit
python3 scripts/coordinate.py discover --limit 5

# Probe a specific worker for pricing
python3 scripts/coordinate.py probe https://worker.example.com/services/autoresearch-worker

# Submit a single experiment to a worker
python3 scripts/coordinate.py submit https://worker.example.com/services/autoresearch-worker train.py

# Submit with custom config overrides
python3 scripts/coordinate.py submit https://worker.example.com/services/autoresearch-worker train.py \
  --config '{"batch_size": 64, "learning_rate": 0.001}'

# View global leaderboard (best val_bpb across all workers)
python3 scripts/coordinate.py leaderboard

# Run continuous experiment loop (discover -> pick -> submit -> publish)
python3 scripts/coordinate.py loop train.py

# Loop with worker preference and max rounds
python3 scripts/coordinate.py loop train.py --prefer https://worker.example.com/services/autoresearch-worker --rounds 10
```

## Commands

| Command | Description |
|---------|-------------|
| `discover [--limit N]` | Query the preferred public index API for GPU workers with `devops_mlops/model_versioning` skill |
| `probe <endpoint>` | Send unauthenticated request to parse 402 pricing from the worker |
| `submit <endpoint> <train.py> [--config JSON]` | Submit experiment with x402 payment (pre-sign ERC-3009, attach X-PAYMENT) |
| `leaderboard [--limit N]` | Query 8004scan for all autoresearch workers, rank by best `val_bpb` |
| `loop <train.py> [--prefer URL] [--rounds N]` | Continuous loop: discover, pick best worker, submit, collect, publish |

## The Experiment Loop

The coordinator implements a THINK/CLAIM/RUN/PUBLISH loop:

1. **THINK** -- Read current `train.py`, review leaderboard results, decide on next experiment variation
2. **CLAIM** -- Discover workers via 8004scan, probe pricing, select best worker (cheapest or preferred)
3. **RUN** -- Submit `train.py` + config to worker via POST `/experiment` through the x402 payment gate
4. **PUBLISH** -- Record result locally with provenance metadata (val_bpb, experiment_id, train.py hash, worker endpoint)

Each step is atomic and idempotent. If a worker fails mid-experiment, the coordinator retries with the next available worker.

## How Discovery Works

Workers register on-chain via ERC-8004 and advertise capabilities through OASF (Open Agent Skills Framework) metadata. The coordinator discovers workers via the public 8004scan API:

```
GET https://www.8004scan.io/api/v1/public/agents
    ?protocol=OASF
    &search=devops_mlops/model_versioning
    &limit=20
```

The API returns agent summary objects. The coordinator then prefers the embedded registration document in `raw_metadata.offchain_content` and falls back to the off-chain registration URI when needed.

From the registration document it extracts:
- service endpoints (where to POST experiments)
- x402 support flag (payment-gated access)
- OASF capability metadata from the `services[]` entry with `name: OASF`
- leaderboard metadata such as `best_val_bpb`

## How Payment Works

Experiment submission uses the same x402 payment flow as `buy-x402`:

1. **Probe** -- Send unauthenticated POST to worker endpoint, receive `402 Payment Required` with pricing
2. **Sign** -- Pre-sign an ERC-3009 `TransferWithAuthorization` voucher via the current remote-signer API (`GET /api/v1/keys`, `POST /api/v1/sign/<address>/typed-data`)
3. **Submit** -- Re-send the POST with the `X-PAYMENT` header containing the signed voucher
4. **Settle** -- Worker's x402 verifier validates payment via the facilitator, forwards request to GPU

Payment is per-experiment (not per-token). The 402 response includes `amount` in the v2 wire format; legacy sellers may still return `maxAmountRequired`.

## How Results are Published

After collecting a result from a worker, the coordinator stores provenance metadata locally:

```json
{
  "experiment_id": "exp-20260312-a1b2c3",
  "train_hash": "sha256:abcdef...",
  "val_bpb": 1.234,
  "worker_endpoint": "https://worker.example.com/services/autoresearch",
  "worker_agent_id": 42,
  "timestamp": "2026-03-12T10:30:00Z",
  "payment_tx": "0xdeadbeef..."
}
```

Results are appended to `$DATA_DIR/autoresearch/results.jsonl` (one JSON object per line).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAN_API_URL` | `https://www.8004scan.io/api/v1/public` | Public 8004scan API base URL for worker discovery |
| `REMOTE_SIGNER_URL` | `http://remote-signer:9000` | Remote-signer REST API for payment signing |
| `ERPC_URL` | `http://erpc.erpc.svc.cluster.local:4000/rpc` | eRPC gateway base URL |
| `ERPC_NETWORK` | `base` | Default chain for payment |
| `DATA_DIR` | `/data` | Base directory for result storage |

## Architecture

```
coordinate.py
    |
    +-- discover: 8004scan API (OASF filter)
    |       |
    |       v
    |   Worker .well-known/agent-registration.json
    |
    +-- probe: POST /experiment (no payment)
    |       |
    |       v
    |   402 Payment Required (pricing JSON)
    |
    +-- submit: ERC-3009 sign via remote-signer
    |       |
    |       +-- POST /experiment + X-PAYMENT header
    |       |       |
    |       |       v
    |       |   x402 verifier -> facilitator -> GPU worker
    |       |       |
    |       |       v
    |       |   200 + experiment result
    |       |
    |       v
    +-- publish: results.jsonl (local provenance)
```

## Constraints

- **Requires remote-signer** -- must have agent wallet provisioned via `obol openclaw onboard`
- **Requires network access** -- 8004scan API and worker endpoints must be reachable
- **Python stdlib only** -- uses `urllib`; no third-party Python dependencies required
- **Per-experiment payment** -- each submission costs one x402 payment; monitor balance via `buy-x402 balance`
- **Worker availability** -- workers may go offline between discovery and submission; coordinator retries automatically
- **Result storage is local** -- provenance metadata stored on-disk, not on-chain (future: IPFS/on-chain attestations)

## References

- `references/coordination-protocol.md` -- Ensue-to-obol mapping, discovery flow, payment flow, leaderboard format
- See also: `discovery` skill for raw ERC-8004 registry queries
- See also: `buy-x402` skill for the x402 buyer sidecar architecture
- See also: `sell` skill for running a GPU worker (sell-side)
