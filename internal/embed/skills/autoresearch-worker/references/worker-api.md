# Autoresearch Worker API

The worker API is a small synchronous HTTP service for selling GPU-backed autoresearch experiments.

## Endpoints

### `GET /health` and `GET /healthz`

Returns basic health/config status.

Example response:

```json
{
  "status": "ok",
  "busy": false,
  "repo": "/data/autoresearch",
  "timeoutSeconds": 300
}
```

### `GET /status`

Returns the live worker status including current, last, and best results.

### `GET /best`

Returns the best result seen so far, or `404` if no completed result exists yet.

### `GET /experiments/<id>`

Returns the stored JSON result for one experiment.

### `POST /experiment`

Runs an experiment. Expected request body:

```json
{
  "train_py": "print('training logic here')",
  "config": {
    "batch_size": 64,
    "learning_rate": 0.001
  },
  "experiment_id": "optional-custom-id"
}
```

Special-case probe body:

```json
{
  "probe": true
}
```

This returns `200` with a small readiness payload when the request reaches the worker directly. In the x402-gated flow, unauthenticated probe requests should usually be intercepted before the worker and turned into `402 Payment Required`.

## Result Shape

Example response:

```json
{
  "experiment_id": "exp-20260312-deadbeef",
  "status": "completed",
  "return_code": 0,
  "val_bpb": 1.0234,
  "train_hash": "sha256:...",
  "artifact_path": "/data/autoresearch-worker/results/exp-20260312-deadbeef/work/model.gguf",
  "log_path": "/data/autoresearch-worker/results/exp-20260312-deadbeef/run.log",
  "startedAt": "2026-03-12T12:00:00+00:00",
  "finishedAt": "2026-03-12T12:05:00+00:00",
  "durationSeconds": 300.0,
  "config": {}
}
```

`status` may be:
- `completed`
- `failed`
- `timeout`

## Data Layout

The worker stores state under:

```text
$DATA_DIR/autoresearch-worker/
  best.json
  results.jsonl
  results/
    <experiment-id>/
      config.json
      train.py
      run.log
      result.json
      work/
```

## Deployment Notes

## Recommended: k3s on the GPU host

This is the cleanest production path because:
- the worker is reachable through a Kubernetes Service
- `obol sell http` can point at that service directly
- GPU access can be provided via the host's Kubernetes GPU setup

### Minimal deployment pattern

1. Build the image:

```bash
docker build -f Dockerfile.worker -t autoresearch-worker:dev .
```

2. Run it on a GPU host with the autoresearch repo mounted at `/data/autoresearch`.

3. Expose it as a Kubernetes Service named `autoresearch-worker` in namespace `autoresearch`.

4. Monetize it with:

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

## Security Note

This API executes submitted `train.py` code. Treat the worker as dedicated, untrusted-code infrastructure.
Do not run it on a machine that also hosts unrelated workloads or sensitive data.
