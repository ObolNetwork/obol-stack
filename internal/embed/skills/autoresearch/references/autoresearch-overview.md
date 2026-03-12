# Autoresearch Overview

## What Is Autoresearch?

Autoresearch is an autonomous LLM optimization methodology where an AI agent iteratively modifies a training script (`train.py`), runs short experiments on GPU, and measures improvement using a single metric: **validation bits per byte (val_bpb)**.

The agent operates in a tight loop:

1. Analyze previous experiment results
2. Hypothesize an improvement to `train.py`
3. Commit the change (git)
4. Run training for up to 5 minutes on GPU
5. Measure val_bpb on the held-out validation set
6. Record the result in `results.tsv` with a keep/discard verdict
7. Repeat

## val_bpb Metric

**Validation bits per byte** measures how many bits the model needs, on average, to encode each byte of the validation set. It is derived from cross-entropy loss:

```
val_bpb = cross_entropy_loss / ln(2) * (tokens / bytes)
```

**Interpretation:**

- **Lower is better** — fewer bits per byte means the model compresses / predicts the validation data more efficiently
- Typical range for small LLMs: 0.8 - 1.2 bpb
- A 0.01 improvement in val_bpb is meaningful at small scale
- The metric is data-dependent — only comparable across runs on the same validation set

## 5-Minute Time Budget

Each experiment is capped at **5 minutes of GPU wall-clock time**. This constraint:

- Forces the agent to make small, testable changes
- Prevents runaway training jobs
- Enables rapid iteration (dozens of experiments per hour)
- Makes the search tractable on consumer GPUs

The time budget is enforced by the training harness. If training does not converge within 5 minutes, the checkpoint at timeout is evaluated.

## The train.py Modification Loop

The agent treats `train.py` as the single artifact to optimize. Modifications include:

- **Architecture changes**: layer count, hidden dimensions, attention heads, normalization
- **Training hyperparameters**: learning rate, batch size, warmup schedule, weight decay
- **Optimization tricks**: gradient accumulation, mixed precision, curriculum
- **Regularization**: dropout, data augmentation, label smoothing
- **Novel ideas**: the agent can try unconventional approaches

Each modification is a git commit so the exact code for every experiment is reproducible.

## results.tsv Format

Tab-separated file appended after each experiment:

| Column | Type | Description |
|--------|------|-------------|
| `commit_hash` | string | Git commit SHA of the experiment |
| `val_bpb` | float | Validation bits per byte (lower is better) |
| `status` | string | `keep` or `discard` |
| `description` | string | Brief description of what changed |

Example:

```
commit_hash	val_bpb	status	description
a1b2c3d	1.042	keep	baseline transformer
e4f5g6h	1.038	keep	added RMSNorm pre-norm
i7j8k9l	1.051	discard	unstable cosine lr schedule
m0n1o2p	1.031	keep	increased hidden dim to 512
```

## OASF Registration

When publishing an autoresearch-optimized model on-chain via ERC-8004, use these OASF classifications:

- **Skills**: `machine_learning/model_optimization`
- **Domains**: `technology/artificial_intelligence/research`

These tags help buyers discover optimized models through the agent discovery protocol and understand the provenance of the offering.

## Provenance

The `publish.py` script generates a provenance JSON file that records:

- **framework**: `autoresearch`
- **metricName**: `val_bpb`
- **metricValue**: the winning `val_bpb` value as a string
- **trainHash**: `sha256:` hash of `train.py` at the winning commit (reproducibility proof)
- **paramCount**: model parameter count as a string (when available from checkpoint metadata)
- **experimentId**: git commit hash of the winning experiment

This provenance file can be passed to `obol sell inference --provenance-file` so that buyers can verify the optimization lineage of the model they are purchasing.
