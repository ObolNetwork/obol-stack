# Coordination Protocol Reference

How the autoresearch coordinator maps from the original Ensue-based shared-memory model to obol-stack's decentralised primitives.

## Ensue to obol-stack Mapping

| Ensue Concept | obol-stack Equivalent | Notes |
|---|---|---|
| Shared memory (Redis/filesystem) | ERC-8004 on-chain registry + public index API | Workers register capabilities on-chain; coordinator prefers the internal Reth indexer and falls back to 8004scan |
| Task queue (Ensue scheduler) | Direct HTTP POST to worker `/experiment` endpoint | No central queue; coordinator submits directly to chosen worker |
| Worker discovery (static config) | 8004scan OASF query (`machine_learning/model_optimization`) | Dynamic discovery; workers join/leave without coordinator restart |
| Payment (none / trust-based) | x402 micropayments (USDC via ERC-3009 pre-signed auths) | Per-experiment payment; no credit accounts or invoicing |
| Result aggregation (shared DB) | Local `results.jsonl` + worker `.well-known` metadata | Coordinator stores locally; workers publish best scores in registration |
| Leaderboard (centralized) | 8004scan metadata aggregation | Workers self-report best `val_bpb` in their registration JSON |
| Worker health checks (heartbeat) | x402 probe (402 = alive, timeout = dead) | Payment gate doubles as health check |
| Experiment versioning (git) | SHA-256 hash of `train.py` (`train_hash` field) | Immutable reference to exact code submitted |

## Discovery Flow

```
Coordinator                    Internal Indexer / 8004scan           Worker
    |                                    |                              |
    |-- GET /health -------------------->|                              |
    |<-- 200 ready / 503 unhealthy ------|                              |
    |                                    |                              |
    |-- GET /api/v1/public/agents ------>|                              |
    |   ?protocol=OASF                   |                              |
    |   &search=machine_learning/        |                              |
    |     model_optimization             |                              |
    |   &limit=20                        |                              |
    |                                    |                              |
    |<-- {data: [agent summaries]} ------|                              |
    |                                    |                              |
    |   (prefer raw_metadata.offchain_content when present)             |
    |                                    |                              |
    |-- GET <offchain_uri> (fallback only) ---------------------------->|
    |                                                                   |
    |<-- {services: [...], x402Support: true, metadata: {...}} ---------|
    |                                                                   |
    |   (extract endpoint, verify x402 + OASF service entry)            |
```

### Public Index API Parameters

| Parameter | Type | Description |
|---|---|---|
| `protocol` | string | Filter by protocol: `OASF`, `MCP`, `A2A`, `Web`, `Email` |
| `search` | string | Keyword search across name, description, skills |
| `chainId` | int | Filter by chain (e.g., 84532 for Base Sepolia) |
| `ownerAddress` | address | Filter by registration owner |
| `sortBy` | string | Sort field (e.g., `registeredAt`) |
| `limit` | int | Max results to return |

The coordinator uses the same query contract against both the internal Reth-backed indexer and the public 8004scan API. The preferred base is `OBOL_INDEXER_API_URL`; `SCAN_API_URL` remains the fallback when `/health` is unavailable, non-200, or reports `ready: false`.

### Worker Registration JSON

Workers advertise capabilities in their `.well-known/agent-registration.json`:

```json
{
  "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
  "name": "GPU Worker Alpha",
  "description": "A100 GPU worker for autoresearch experiments",
  "services": [
    {
      "name": "web",
      "endpoint": "https://worker.example.com/services/autoresearch-worker",
      "version": "1.0.0"
    },
    {
      "name": "OASF",
      "version": "0.8",
      "skills": ["machine_learning/model_optimization"],
      "domains": ["technology/artificial_intelligence/research"]
    }
  ],
  "x402Support": true,
  "metadata": {
    "gpu": "A100-80GB",
    "framework": "pytorch",
    "best_val_bpb": "1.234",
    "total_experiments": "42",
    "updated": "2026-03-12T10:30:00Z"
  },
  "active": true
}
```

## Payment Flow

```
Coordinator                   Worker (x402 gate)           Facilitator          Chain
    |                              |                           |                  |
    |-- POST /experiment --------->|                           |                  |
    |   (no X-PAYMENT header)      |                           |                  |
    |                              |                           |                  |
    |<-- 402 Payment Required -----|                           |                  |
    |   {payTo, network,           |                           |                  |
    |    maxAmountRequired}        |                           |                  |
    |                              |                           |                  |
    |-- sign ERC-3009 auth --------|---------------------------|----------------->|
    |   (GET /api/v1/keys +        |                           |                  |
    |    POST /api/v1/sign/<addr>/typed-data)                  |                  |
    |                              |                           |                  |
    |-- POST /experiment --------->|                           |                  |
    |   X-PAYMENT: {signature,     |                           |                  |
    |    authorization, chain,     |-- verify payment -------->|                  |
    |    token}                    |                           |-- settle USDC -->|
    |   body: {train_py, config}   |<-- 200 OK (valid) -------|                  |
    |                              |                           |                  |
    |                              |-- run experiment          |                  |
    |                              |   (GPU training)          |                  |
    |                              |                           |                  |
    |<-- 200 {val_bpb, metrics} ---|                           |                  |
```

### ERC-3009 Authorization Structure

Each payment authorization contains:

| Field | Type | Description |
|---|---|---|
| `from` | address | Coordinator's wallet (agent wallet from remote-signer) |
| `to` | address | Worker's `payTo` address (from 402 response) |
| `value` | uint256 | Payment amount in USDC micro-units |
| `validAfter` | uint256 | Unix timestamp (0 = immediately valid) |
| `validBefore` | uint256 | Unix timestamp (current time + 1 hour) |
| `nonce` | bytes32 | Random 32-byte nonce (single-use, prevents replay) |

### X-PAYMENT Header Format

```json
{
  "signature": "0x...",
  "authorization": {
    "from": "0xCoordinatorWallet",
    "to": "0xWorkerPayTo",
    "value": "1000",
    "validAfter": "0",
    "validBefore": "1741784400",
    "nonce": "0xrandom32bytes..."
  },
  "chain": "base-sepolia",
  "token": "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
}
```

## Leaderboard Metadata Format

Workers publish their best results in the `.well-known/agent-registration.json` metadata section. The coordinator aggregates these via 8004scan queries.

### Required Fields

| Field | Type | Description |
|---|---|---|
| `metadata.best_val_bpb` | float | Best validation bits-per-byte achieved |
| `metadata.total_experiments` | int | Total experiments processed by this worker |
| `metadata.updated` | string | ISO 8601 timestamp of last result update |

### Optional Fields

| Field | Type | Description |
|---|---|---|
| `metadata.gpu` | string | GPU model (e.g., `A100-80GB`, `H100`) |
| `metadata.framework` | string | Training framework (e.g., `pytorch`, `jax`) |
| `metadata.best_experiment_hash` | string | SHA-256 hash of the train.py that produced the best result |
| `metadata.avg_experiment_time` | float | Average seconds per experiment |

### Leaderboard Ranking

The coordinator ranks workers by `metadata.best_val_bpb` in ascending order (lower is better). When querying the leaderboard:

1. Fetch all workers with `machine_learning/model_optimization` skill from 8004scan
2. For each worker, fetch their registration JSON
3. Extract `metadata.best_val_bpb` (skip workers without this field)
4. Sort ascending by `val_bpb`
5. Display rank, score, agent name, and last update time

### Local Results Format

The coordinator also maintains a local `results.jsonl` file for provenance tracking. Each line is a JSON object:

```json
{
  "experiment_id": "exp-20260312-a1b2c3",
  "train_hash": "sha256:abcdef1234567890...",
  "val_bpb": 1.234,
  "worker_endpoint": "https://worker.example.com/services/autoresearch",
  "worker_agent_id": 42,
  "timestamp": "2026-03-12T10:30:00Z",
  "chain": "base-sepolia",
  "raw_result": { "...worker response..." }
}
```

The `experiment_id` format is `exp-YYYYMMDD-XXXXXX` where `XXXXXX` is 6 random hex chars. The `train_hash` is the SHA-256 of the exact `train.py` source submitted, providing an immutable reference for reproducibility.
