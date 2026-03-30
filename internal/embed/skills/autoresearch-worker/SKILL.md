---
name: autoresearch-worker
description: "Run a GPU worker that accepts paid autoresearch experiments over HTTP and monetize it through obol sell http."
metadata: { "openclaw": { "emoji": "🖥️", "requires": { "bins": ["python3", "uv"] } } }
---

# Autoresearch Worker

Run a single-GPU worker that accepts `train.py` experiments over HTTP, executes them one at a time, stores results on disk, and exposes a simple API that can be gated with x402 using `obol sell http`.

## When to Use

- Selling GPU-backed autoresearch experiment execution
- Exposing a remote `POST /experiment` endpoint for the autoresearch coordinator
- Running a worker on a Linux GPU host with `k3s`
- Testing the sell-side of the GPU marketplace locally before exposing it publicly

## When NOT to Use

- Coordinating experiments across many workers — use `autoresearch-coordinator`
- Publishing optimized checkpoints as inference — use `autoresearch`
- Selling a generic HTTP app — use `sell`

## What the Worker Exposes

- `GET /health` / `GET /healthz` — worker health
- `GET /status` — busy/idle plus current, last, and best result
- `GET /best` — best known result
- `GET /experiments/<id>` — fetch a stored result
- `POST /experiment` — submit a `train.py` experiment

The worker is intentionally simple:
- one experiment at a time
- one GPU worker process
- local disk for logs/results
- no distributed queue or scheduler inside the worker

## Quick Start

### 1. Start the worker API

```bash
python3 scripts/worker_api.py serve \
  --repo /path/to/autoresearch \
  --data-dir /data \
  --host 0.0.0.0 \
  --port 8080 \
  --timeout 300
```

The repo path should point at a prepared autoresearch workdir/repo with the dependencies already available via `uv`.

### 2. Verify the worker locally

```bash
curl -s http://127.0.0.1:8080/health | jq .
curl -s http://127.0.0.1:8080/status | jq .
```

### 3. Deploy the worker behind a Kubernetes Service

For production GPU sellers, prefer `k3s` on the GPU host. The monetization path is cluster-based, so the worker should be reachable as a Kubernetes Service.

### 4. Monetize it with x402

```bash
obol sell http autoresearch-worker \
  --namespace autoresearch \
  --upstream autoresearch-worker \
  --port 8080 \
  --health-path /health \
  --wallet 0xYourWalletAddress \
  --chain base-sepolia \
  --per-hour 0.50 \
  --path /services/autoresearch-worker \
  --register \
  --register-name "GPU Worker Alpha" \
  --register-description "A GPU worker for paid autoresearch experiments" \
  --register-skills devops_mlops/model_versioning \
  --register-domains research_and_development/scientific_research
```

This creates a `ServiceOffer` that:
- health-checks the worker
- creates a payment-gated public route
- optionally registers the worker on ERC-8004 for discovery

## Request Format

Submit an experiment with:

```json
{
  "train_py": "print('hello world')",
  "config": {
    "batch_size": 64,
    "learning_rate": 0.001
  }
}
```

If the request makes it through the x402 gate, the worker runs the experiment synchronously and returns a result JSON document.

## Important Constraints

- **Single-flight** — one experiment at a time; concurrent submissions return `409`
- **Arbitrary code execution** — submitted `train.py` is executed, so run this only on infrastructure dedicated to the worker
- **Local persistence** — results are stored under `$DATA_DIR/autoresearch-worker/results/`
- **Approx pricing** — `--per-hour` is converted into a request price using the current 5-minute experiment budget assumption
- **Cluster-based monetization** — `obol sell http` expects a Kubernetes Service, so `k3s` is the preferred deployment target for GPU sellers

## Files

- `scripts/worker_api.py` — HTTP worker service
- `references/worker-api.md` — endpoint and deployment details
- `Dockerfile.worker` (repo root) — container image for the worker

## References

- `references/worker-api.md` — worker endpoints, result schema, and deployment notes
- `references/k3s-gpu-worker.md` — minimal k3s Deployment + Service example
- See also: `sell` for ServiceOffer monetization
- See also: `autoresearch-coordinator` for the buyer/coordinator side of remote experiments
