---
name: autoresearch
description: "Run autonomous LLM optimization experiments (autoresearch) and publish optimized models for paid inference via x402."
metadata: { "openclaw": { "emoji": "🧪", "requires": { "bins": ["python3", "uv"] } } }
---

# Autoresearch

Autonomous LLM optimization: the agent iterates on `train.py`, runs 5-minute GPU experiments, measures validation bits-per-byte (val_bpb), and publishes the best checkpoint as a sellable Ollama model.

## When to Use

- Optimizing a base model for a specific domain or task
- Running automated training experiments to improve val_bpb
- Publishing an optimized model checkpoint to Ollama
- Selling an optimized model via x402 payment-gated inference

## When NOT to Use

- Selling an existing model without optimization — use `sell`
- Buying remote inference — use `buy-inference`
- Cluster diagnostics — use `obol-stack`

## Quick Start

### 1. Prepare Data

Place your training and validation data in the autoresearch working directory:

```
autoresearch/
  train.bin        # training data (tokenized)
  val.bin          # validation data (tokenized)
  train.py         # training script (agent modifies this)
  results.tsv      # experiment log (appended by each run)
```

### 2. Run Experiments

The agent modifies `train.py` and runs experiments in a loop. Each experiment:

- Has a **5-minute time budget** on GPU
- Produces a checkpoint and a val_bpb measurement
- Is tracked as a git commit with status (keep/discard) in `results.tsv`

The `results.tsv` file is tab-separated with columns:

```
commit_hash	val_bpb	status	description
a1b2c3d	1.042	keep	baseline transformer
e4f5g6h	1.038	keep	added RMSNorm
i7j8k9l	1.051	discard	unstable lr schedule
```

### 3. Publish the Best Model

Once experiments are complete, use `publish.py` to find the best checkpoint, register it with Ollama, and optionally sell it:

```bash
# Publish to Ollama only
python3 scripts/publish.py /path/to/autoresearch

# Publish and sell via x402
python3 scripts/publish.py /path/to/autoresearch \
  --sell \
  --wallet 0xYourWalletAddress \
  --price 0.002 \
  --chain base-sepolia
```

## Commands

| Command | Description |
|---------|-------------|
| `publish.py <dir>` | Find best experiment, create Ollama model, generate provenance |
| `publish.py <dir> --sell --wallet <addr> --price <p> --chain <c>` | Publish and sell via `obol sell inference` |

## How It Works

1. **Experiment loop**: The agent edits `train.py`, runs training for up to 5 minutes, measures val_bpb on the validation set, and commits the result with a keep/discard verdict.

2. **Selection**: `publish.py` reads `results.tsv`, filters for `status=keep`, and selects the experiment with the lowest val_bpb (lower is better — fewer bits per byte means better compression / prediction).

3. **Provenance**: A JSON provenance file is generated with:
   - `framework`: training framework used
   - `metric`: `val_bpb` and its value
   - `trainHash`: SHA-256 of the `train.py` at the winning commit
   - `paramCount`: model parameter count (from checkpoint metadata)
   - `experimentId`: git commit hash of the winning experiment

4. **Ollama registration**: A Modelfile is generated from the checkpoint and `ollama create` registers the model locally.

5. **Sell (optional)**: If `--sell` is passed, runs `obol sell inference` with the `--provenance-file` flag pointing at the provenance JSON so buyers can verify optimization lineage.

## Architecture

```
Agent (autoresearch loop)
  |
  +-- edit train.py
  +-- run experiment (5-min budget)
  +-- measure val_bpb
  +-- commit results.tsv
  |
  v
publish.py
  |
  +-- read results.tsv → best experiment
  +-- git show <commit>:train.py → SHA-256 trainHash
  +-- generate provenance.json
  +-- generate Modelfile → ollama create
  +-- (optional) obol sell inference --provenance-file
```

## Constraints

- **Python stdlib + uv** — no pip install; uv for environment management
- **5-minute time budget** — each experiment must complete within 5 minutes
- **GPU required** — training runs on local GPU (Ollama must have GPU access)
- **Git repo required** — autoresearch directory must be a git repository for commit tracking
- **results.tsv format** — tab-separated: `commit_hash`, `val_bpb`, `status`, `description`

## OASF Registration

When registering an autoresearch-optimized model on-chain via ERC-8004:

- **Skills**: `machine_learning/model_optimization`
- **Domains**: `technology/artificial_intelligence/research`

## References

- `references/autoresearch-overview.md` — val_bpb metric, time budget, and the train.py modification loop
