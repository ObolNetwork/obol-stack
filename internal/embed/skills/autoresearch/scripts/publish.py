#!/usr/bin/env python3
"""Publish the best autoresearch experiment to Ollama and optionally sell via x402.

Usage:
    python3 publish.py <dir>
    python3 publish.py <dir> --sell --wallet 0x... --price 0.002 --chain base-sepolia
"""

import argparse
import csv
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path


def die(msg: str) -> None:
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(1)


def run(cmd: list[str], cwd: str | None = None, capture: bool = True) -> str:
    """Run a command and return stdout. Exits on failure."""
    try:
        result = subprocess.run(
            cmd,
            cwd=cwd,
            capture_output=capture,
            text=True,
            check=True,
        )
        return result.stdout.strip() if capture else ""
    except FileNotFoundError:
        die(f"command not found: {cmd[0]}")
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.strip() if exc.stderr else str(exc)
        die(f"command failed: {' '.join(cmd)}\n{detail}")
    return ""  # unreachable, keeps type checkers happy


def read_results(results_path: Path) -> list[dict]:
    """Read results.tsv and return rows as dicts."""
    if not results_path.exists():
        die(f"results.tsv not found at {results_path}")

    rows: list[dict] = []
    with open(results_path, "r") as f:
        reader = csv.DictReader(f, delimiter="\t")
        for row in reader:
            rows.append(row)

    if not rows:
        die("results.tsv is empty")
    return rows


def find_best_experiment(rows: list[dict]) -> dict:
    """Find the experiment with status=keep and lowest val_bpb."""
    kept = [r for r in rows if r.get("status", "").strip().lower() == "keep"]
    if not kept:
        die("no experiments with status=keep found in results.tsv")

    best = None
    best_bpb = float("inf")
    for row in kept:
        try:
            bpb = float(row["val_bpb"])
        except (KeyError, ValueError):
            continue
        if bpb < best_bpb:
            best_bpb = bpb
            best = row

    if best is None:
        die("no valid val_bpb values found in kept experiments")
    return best


def get_train_hash(workdir: str, commit: str) -> str:
    """Get SHA-256 of train.py at the given commit."""
    content = run(["git", "show", f"{commit}:train.py"], cwd=workdir)
    return hashlib.sha256(content.encode()).hexdigest()


def get_param_count(workdir: str, commit: str) -> int | None:
    """Try to extract param count from checkpoint metadata. Returns None if unavailable."""
    # Look for a metadata.json or similar in the commit tree
    try:
        tree = run(["git", "ls-tree", "--name-only", commit], cwd=workdir)
        for fname in tree.splitlines():
            if fname in ("metadata.json", "config.json"):
                content = run(["git", "show", f"{commit}:{fname}"], cwd=workdir)
                meta = json.loads(content)
                for key in ("param_count", "n_params", "num_parameters", "total_params"):
                    if key in meta:
                        return int(meta[key])
    except Exception:
        pass
    return None


def find_checkpoint(workdir: str, commit: str) -> Path | None:
    """Find the model checkpoint file in the working directory for a given commit."""
    # Check common checkpoint patterns
    try:
        tree = run(["git", "ls-tree", "--name-only", "-r", commit], cwd=workdir)
    except SystemExit:
        tree = ""

    checkpoint_patterns = (".pt", ".pth", ".bin", ".safetensors", ".gguf")
    for fname in tree.splitlines():
        if any(fname.endswith(ext) for ext in checkpoint_patterns):
            # Extract the file from the commit
            ckpt_path = Path(workdir) / fname
            if ckpt_path.exists():
                return ckpt_path

    # Fallback: look in working directory for checkpoint files
    for ext in checkpoint_patterns:
        candidates = list(Path(workdir).glob(f"**/*{ext}"))
        if candidates:
            # Return most recently modified
            return max(candidates, key=lambda p: p.stat().st_mtime)

    return None


def generate_provenance(
    workdir: str,
    best: dict,
    train_hash: str,
    param_count: int | None,
) -> Path:
    """Generate canonical provenance JSON and write it to the workdir."""
    provenance = {
        "framework": "autoresearch",
        "metricName": "val_bpb",
        "metricValue": str(best["val_bpb"]),
        "experimentId": best["commit_hash"].strip(),
        "trainHash": f"sha256:{train_hash}",
    }
    if param_count is not None:
        provenance["paramCount"] = str(param_count)

    out_path = Path(workdir) / "provenance.json"
    with open(out_path, "w") as f:
        json.dump(provenance, f, indent=2)
    print(f"Provenance written to {out_path}")
    return out_path


def create_modelfile(checkpoint_path: Path, workdir: str) -> Path:
    """Create an Ollama Modelfile from a checkpoint."""
    modelfile_path = Path(workdir) / "Modelfile"
    content = f'FROM {checkpoint_path}\n'
    with open(modelfile_path, "w") as f:
        f.write(content)
    print(f"Modelfile written to {modelfile_path}")
    return modelfile_path


def ollama_create(model_name: str, modelfile_path: Path) -> None:
    """Register the model with Ollama."""
    print(f"Creating Ollama model: {model_name}")
    run(
        ["ollama", "create", model_name, "-f", str(modelfile_path)],
        capture=False,
    )
    print(f"Model '{model_name}' registered with Ollama")


def sell_inference(
    model_name: str,
    provenance_path: Path,
    wallet: str,
    price: str,
    chain: str,
) -> None:
    """Run obol sell inference to monetize the model."""
    cmd = [
        "obol", "sell", "inference", model_name,
        "--model", model_name,
        "--price", price,
        "--wallet", wallet,
        "--chain", chain,
        "--provenance-file", str(provenance_path),
    ]
    print(f"Selling model: {' '.join(cmd)}")
    run(cmd, capture=False)
    print(f"Model '{model_name}' listed for paid inference")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Publish the best autoresearch experiment to Ollama and optionally sell via x402.",
    )
    parser.add_argument(
        "dir",
        help="Path to autoresearch working directory (must contain results.tsv)",
    )
    parser.add_argument(
        "--name",
        default=None,
        help="Ollama model name (default: autoresearch-<commit_hash[:8]>)",
    )
    parser.add_argument(
        "--sell",
        action="store_true",
        help="Also sell the model via obol sell inference",
    )
    parser.add_argument(
        "--wallet",
        default=None,
        help="USDC recipient wallet address (required with --sell)",
    )
    parser.add_argument(
        "--price",
        default=None,
        help="Price per request in USDC (required with --sell)",
    )
    parser.add_argument(
        "--chain",
        default="base-sepolia",
        help="Payment chain (default: base-sepolia)",
    )
    args = parser.parse_args()

    workdir = os.path.abspath(args.dir)
    if not os.path.isdir(workdir):
        die(f"directory not found: {workdir}")

    results_path = Path(workdir) / "results.tsv"

    # Verify git repo
    if not (Path(workdir) / ".git").exists():
        die(f"{workdir} is not a git repository (autoresearch requires git for commit tracking)")

    # Step 1: Find best experiment
    print("Reading results.tsv...")
    rows = read_results(results_path)
    best = find_best_experiment(rows)
    commit = best["commit_hash"].strip()
    bpb = float(best["val_bpb"])
    desc = best.get("description", "").strip()
    print(f"Best experiment: commit={commit[:8]} val_bpb={bpb:.4f} ({desc})")

    # Step 2: Compute train.py hash
    print("Computing train.py hash...")
    train_hash = get_train_hash(workdir, commit)
    print(f"trainHash (SHA-256): {train_hash[:16]}...")

    # Step 3: Get param count (optional)
    param_count = get_param_count(workdir, commit)
    if param_count is not None:
        print(f"Parameter count: {param_count:,}")

    # Step 4: Generate provenance
    provenance_path = generate_provenance(workdir, best, train_hash, param_count)

    # Step 5: Find checkpoint and create Ollama model
    checkpoint = find_checkpoint(workdir, commit)
    if checkpoint is None:
        die(
            "no model checkpoint found (.pt, .pth, .bin, .safetensors, .gguf). "
            "Ensure the checkpoint is committed or present in the working directory."
        )
    print(f"Using checkpoint: {checkpoint}")

    if not str(checkpoint).endswith(".gguf"):
        die(
            f"checkpoint {checkpoint.name} is not in GGUF format. "
            "Ollama requires GGUF files. Convert with:\n"
            "  python llama.cpp/convert_hf_to_gguf.py <model-dir> --outfile model.gguf"
        )

    model_name = args.name or f"autoresearch-{commit[:8]}"
    modelfile_path = create_modelfile(checkpoint, workdir)
    ollama_create(model_name, modelfile_path)

    # Step 6: Optionally sell
    if args.sell:
        if not args.wallet:
            die("--wallet is required when using --sell")
        if not args.price:
            die("--price is required when using --sell")
        sell_inference(model_name, provenance_path, args.wallet, args.price, args.chain)

    print("\nDone.")
    print(f"  Model:      {model_name}")
    print(f"  val_bpb:    {bpb:.4f}")
    print(f"  Provenance: {provenance_path}")
    if args.sell:
        print(f"  Selling:    yes (chain={args.chain}, price={args.price} USDC)")


if __name__ == "__main__":
    main()
