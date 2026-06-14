---
name: finetune-backend
description: Fine-tune a local model on a purchased/owned dataset through one pluggable backend contract (mock, mlx-lora, unsloth, axolotl, torchtune). Emits adapter + eval + a run.manifest binding the output to the dataset's content-address.
---

# finetune-backend

Run a LoRA/SFT fine-tune over a dataset's `sft.jsonl` artifact through a single
thin contract, selectable per machine:

```
run(dataset_path, base_model, hyperparams) -> { adapter, eval_metric, run.manifest }
```

Every backend reads the **same** JSONL artifact (the bytes you downloaded with
`obol buy dataset`), so swapping backends never reshapes your data. The runner
binds each result to the exact dataset it trained on by writing the dataset's
content-address (`manifestHash`) into `run.manifest` — the provenance link from
a served/sold model back to the data that produced it.

## Backends

| `--backend` | Tool | Hardware | Notes |
|---|---|---|---|
| `mock` *(default)* | none | any | validates the contract + provenance with no framework; emits a deterministic stub adapter + eval. Use in CI/smoke. |
| `mlx-lora` | MLX-LM | Apple silicon | near-native chat-JSONL; native LoRA |
| `unsloth` | Unsloth | NVIDIA | fast QLoRA; on GB10 (sm_121) run eager (FA3 has no kernel) |
| `axolotl` | axolotl | multi-GPU | YAML-config; exposes grad-accum |
| `torchtune` | torchtune | NVIDIA | modular recipes; guard `torch.compile` |

The only thing shared across real backends is "regex-extract the eval metric
from the backend CLI's stdout" — each backend's command + file layout is
otherwise its own. Add a backend by registering one `(build_cmd, metric_regex)`
pair in `BACKENDS`.

## Usage

```bash
# Contract/provenance smoke (no GPU, no framework):
python3 scripts/runner.py --backend mock \
    --dataset my-dataset-v1.jsonl --base-model qwen2.5-0.5b \
    --manifest-hash <the dataset's manifestHash> --out ./run

# Real run on a GPU box:
python3 scripts/runner.py --backend unsloth \
    --dataset my-dataset-v1.jsonl --base-model unsloth/Qwen2.5-0.5B \
    --manifest-hash <manifestHash> --lora-rank 16 --epochs 1 --out ./run

cat ./run/run.manifest    # dataset_hash == the version you bought
cat ./run/eval.json       # {eval_loss, backend, base_model}
```

`run.manifest` is the exact deliverable shape the bounty `finetune@v1` task
declares (`adapter.safetensors` + `eval.json` + `run.manifest` with
`dataset_hash`), so a standalone run and a verified/bounty run stay consistent.
A `--dry-run` validates the dataset and emits the manifest without invoking the
backend.
