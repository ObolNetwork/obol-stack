#!/usr/bin/env python3
"""Pluggable fine-tune backend runner.

One contract over several backends: read a dataset's sft.jsonl, run a LoRA/SFT
fine-tune, and emit adapter + eval.json + run.manifest. run.manifest binds the
result to the dataset's content-address (manifestHash) — the provenance link
from a model back to the data it was trained on.

The only thing shared across real backends is "regex-extract the eval metric
from stdout"; each backend otherwise has its own command + layout.

Usage:
    runner.py --backend <name> --dataset <path.jsonl> --base-model <id>
              [--manifest-hash H] [--lora-rank N] [--epochs E] [--lr LR]
              [--out DIR] [--dry-run]
"""
import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import time


def file_sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def count_records(path):
    n = 0
    with open(path) as f:
        for line in f:
            if line.strip():
                json.loads(line)  # validate JSONL
                n += 1
    return n


# --- backend command builders: (argv, eval_loss_regex) ---

def build_mlx(ds, base, hp, out):
    argv = ["mlx_lm.lora", "--model", base, "--train", "--data", os.path.dirname(ds) or ".",
            "--iters", str(hp["epochs"] * 100), "--adapter-path", out]
    return argv, re.compile(r"[Vv]al loss[:\s]+([0-9.]+)")


def build_unsloth(ds, base, hp, out):
    argv = [sys.executable, "-m", "unsloth.cli", "--model", base, "--dataset", ds,
            "--lora-rank", str(hp["lora_rank"]), "--epochs", str(hp["epochs"]),
            "--lr", str(hp["lr"]), "--output", out]
    return argv, re.compile(r"eval_loss['\"]?\s*[:=]\s*([0-9.]+)")


def build_axolotl(ds, base, hp, out):
    argv = ["accelerate", "launch", "-m", "axolotl.cli.train", os.path.join(out, "axolotl.yaml")]
    return argv, re.compile(r"eval_loss['\"]?\s*[:=]\s*([0-9.]+)")


def build_torchtune(ds, base, hp, out):
    argv = ["tune", "run", "lora_finetune_single_device", "--config", os.path.join(out, "tune.yaml")]
    return argv, re.compile(r"eval_loss['\"]?\s*[:=]\s*([0-9.]+)")


BACKENDS = {
    "mlx-lora": build_mlx,
    "unsloth": build_unsloth,
    "axolotl": build_axolotl,
    "torchtune": build_torchtune,
}


def run_real(backend, ds, base, hp, out):
    build = BACKENDS[backend]
    argv, metric_re = build(ds, base, hp, out)
    print(f"  $ {' '.join(argv)}", file=sys.stderr)
    proc = subprocess.run(argv, capture_output=True, text=True)
    sys.stderr.write(proc.stderr[-2000:])
    combined = proc.stdout + "\n" + proc.stderr
    m = None
    for mm in metric_re.finditer(combined):
        m = mm  # last match wins (final eval)
    if proc.returncode != 0:
        raise SystemExit(f"backend {backend} exited {proc.returncode}")
    if not m:
        raise SystemExit(f"backend {backend} produced no eval metric on stdout")
    return float(m.group(1))


def run_mock(ds, base, hp, out, records):
    # Deterministic, framework-free: synthesize a plausible eval_loss from the
    # data + hyperparams so the contract + provenance can be validated anywhere.
    seed = int(file_sha256(ds)[:8], 16)
    eval_loss = round(1.5 + (seed % 1000) / 2000.0 - 0.02 * hp["epochs"], 6)
    with open(os.path.join(out, "adapter.safetensors"), "wb") as f:
        f.write(b"OBOL-MOCK-ADAPTER\x00" + ds.encode() + b"\x00" + base.encode())
    print(f"  mock backend: {records} records, base {base} -> eval_loss {eval_loss}", file=sys.stderr)
    return eval_loss


def main():
    ap = argparse.ArgumentParser(description="Pluggable fine-tune backend runner")
    ap.add_argument("--backend", default="mock", choices=["mock"] + list(BACKENDS))
    ap.add_argument("--dataset", required=True)
    ap.add_argument("--base-model", required=True)
    ap.add_argument("--manifest-hash", default="", help="dataset content-address (manifestHash) to bind into run.manifest")
    ap.add_argument("--lora-rank", type=int, default=16)
    ap.add_argument("--epochs", type=int, default=1)
    ap.add_argument("--lr", type=float, default=2e-4)
    ap.add_argument("--out", default="./run")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)
    records = count_records(args.dataset)
    if records == 0:
        raise SystemExit("dataset is empty")
    dataset_file_hash = file_sha256(args.dataset)
    hp = {"lora_rank": args.lora_rank, "epochs": args.epochs, "lr": args.lr}

    eval_loss = None
    if args.dry_run:
        print(f"  dry-run: {records} valid records, would run {args.backend}", file=sys.stderr)
    elif args.backend == "mock":
        eval_loss = run_mock(args.dataset, args.base_model, hp, args.out, records)
    else:
        eval_loss = run_real(args.backend, args.dataset, args.base_model, hp, args.out)

    eval_json = {"eval_loss": eval_loss, "backend": args.backend, "base_model": args.base_model, "records": records}
    with open(os.path.join(args.out, "eval.json"), "w") as f:
        json.dump(eval_json, f, indent=2)

    manifest = {
        # The provenance link: this fine-tune is bound to exactly the dataset
        # version it trained on.
        "dataset_hash": args.manifest_hash or dataset_file_hash,
        "dataset_file_hash": dataset_file_hash,
        "base_model": args.base_model,
        "backend": args.backend,
        "hyperparams": hp,
        "eval_loss": eval_loss,
        "adapter": "adapter.safetensors",
        "records": records,
        "created_at": int(time.time()),
    }
    with open(os.path.join(args.out, "run.manifest"), "w") as f:
        json.dump(manifest, f, indent=2)

    print(json.dumps({"out": args.out, "backend": args.backend, "eval_loss": eval_loss,
                      "dataset_hash": manifest["dataset_hash"]}))


if __name__ == "__main__":
    main()
