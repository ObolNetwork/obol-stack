"""Validation runner — downloads models, runs benchmarks, and evaluates tool-calling ability."""

import json
import logging
import os
import re
import signal
import subprocess
import time
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Dict, List, Optional

from .registry import ModelStatus, Registry
from .hardware import HardwareProfile, compute_optimal_ngl
from .discover import DiscoveryCandidate

logger = logging.getLogger(__name__)

LLAMA_BENCH_BIN = Path.home() / "Development" / "llama-cpp-turboquant-cuda" / "build" / "bin" / "llama-bench"
LLAMA_SERVER_BIN = Path.home() / "Development" / "llama-cpp-turboquant-cuda" / "build" / "bin" / "llama-server"
MODEL_CACHE_DIR = Path.home() / ".cache" / "obol" / "models"
TOOLCALL15_DIR = Path.home() / "Development" / "toolcall-15"


@dataclass
class ValidationResult:
    """Results from model validation pipeline."""
    model_id: str
    tok_s_gen: float = 0.0
    tok_s_prompt: float = 0.0
    toolcall15_score: float = 0.0
    toolcall15_details: Dict[str, bool] = field(default_factory=dict)
    passed: bool = False
    timestamp: str = field(default_factory=lambda: datetime.utcnow().isoformat())
    error: Optional[str] = None


def download_model(hf_url: str, dest_dir: Optional[Path] = None) -> Path:
    """Download a model from HuggingFace using wget.

    Args:
        hf_url: HuggingFace model URL (will resolve GGUF files).
        dest_dir: Download destination directory.

    Returns:
        Path to downloaded model file.

    Raises:
        RuntimeError: If download fails.
    """
    dest_dir = dest_dir or MODEL_CACHE_DIR
    dest_dir.mkdir(parents=True, exist_ok=True)

    # Construct download URL for GGUF files from HF repo
    if not hf_url.endswith(".gguf"):
        # Try to find GGUF files in the repo
        resolve_url = f"{hf_url}/resolve/main/"
        logger.info("Looking for GGUF files at %s", hf_url)
        # Attempt to list files via HF API
        api_url = hf_url.replace("huggingface.co", "huggingface.co/api/models")
        try:
            result = subprocess.run(
                ["curl", "-sL", api_url],
                capture_output=True, text=True, timeout=15
            )
            if result.returncode == 0:
                data = json.loads(result.stdout)
                siblings = data.get("siblings", [])
                gguf_files = [s["rfilename"] for s in siblings if s["rfilename"].endswith(".gguf")]
                if gguf_files:
                    # Pick the first Q4_K_M or smallest file
                    preferred = [f for f in gguf_files if "Q4_K_M" in f]
                    chosen = preferred[0] if preferred else gguf_files[0]
                    hf_url = f"{hf_url}/resolve/main/{chosen}"
                    logger.info("Selected GGUF file: %s", chosen)
                else:
                    raise RuntimeError(f"No GGUF files found in {hf_url}")
        except (json.JSONDecodeError, subprocess.TimeoutExpired) as e:
            raise RuntimeError(f"Failed to resolve GGUF files: {e}")

    filename = hf_url.split("/")[-1]
    dest_path = dest_dir / filename

    if dest_path.exists():
        logger.info("Model already cached: %s", dest_path)
        return dest_path

    logger.info("Downloading %s to %s", hf_url, dest_path)
    result = subprocess.run(
        ["wget", "-q", "--show-progress", "-O", str(dest_path), hf_url],
        timeout=3600  # 1 hour max
    )
    if result.returncode != 0:
        dest_path.unlink(missing_ok=True)
        raise RuntimeError(f"Download failed with code {result.returncode}")

    logger.info("Download complete: %s (%.1f GB)", dest_path,
                dest_path.stat().st_size / (1024**3))
    return dest_path


def run_llama_bench(model_path: Path, ngl: int, threads: int = 8) -> Dict[str, float]:
    """Run llama-bench and parse throughput results.

    Args:
        model_path: Path to GGUF model file.
        ngl: Number of GPU layers.
        threads: CPU threads to use.

    Returns:
        Dict with 'tok_s_gen' and 'tok_s_prompt' values.

    Raises:
        RuntimeError: If benchmark fails to run or parse.
    """
    if not LLAMA_BENCH_BIN.exists():
        raise RuntimeError(f"llama-bench not found at {LLAMA_BENCH_BIN}")

    logger.info("Running llama-bench: model=%s ngl=%d threads=%d", model_path, ngl, threads)
    result = subprocess.run(
        [str(LLAMA_BENCH_BIN),
         "-m", str(model_path),
         "-ngl", str(ngl),
         "-t", str(threads),
         "-p", "512", "-n", "128",
         "-o", "json"],
        capture_output=True, text=True, timeout=600
    )
    if result.returncode != 0:
        raise RuntimeError(f"llama-bench failed: {result.stderr[:500]}")

    try:
        data = json.loads(result.stdout)
        results = {"tok_s_gen": 0.0, "tok_s_prompt": 0.0}
        for entry in data if isinstance(data, list) else [data]:
            if entry.get("type") == "tg" or "tg" in str(entry.get("test", "")):
                results["tok_s_gen"] = float(entry.get("avg_ts", 0))
            elif entry.get("type") == "pp" or "pp" in str(entry.get("test", "")):
                results["tok_s_prompt"] = float(entry.get("avg_ts", 0))
        logger.info("Bench results: gen=%.1f tok/s, prompt=%.1f tok/s",
                     results["tok_s_gen"], results["tok_s_prompt"])
        return results
    except (json.JSONDecodeError, KeyError) as e:
        # Fallback: parse text output
        gen_match = re.search(r"tg\s.*?([\d.]+)\s*±", result.stdout)
        pp_match = re.search(r"pp\s.*?([\d.]+)\s*±", result.stdout)
        results = {
            "tok_s_gen": float(gen_match.group(1)) if gen_match else 0.0,
            "tok_s_prompt": float(pp_match.group(1)) if pp_match else 0.0,
        }
        if results["tok_s_gen"] == 0 and results["tok_s_prompt"] == 0:
            raise RuntimeError(f"Failed to parse llama-bench output: {e}")
        return results


def run_toolcall15(model_path: Path, ngl: int, port: int = 9090) -> Dict:
    """Run ToolCall-15 evaluation suite against a model.

    Starts a temporary llama-server, runs the ToolCall-15 eval suite,
    and parses results from the SSE endpoint.

    Args:
        model_path: Path to GGUF model file.
        ngl: Number of GPU layers.
        port: Port for temporary llama-server.

    Returns:
        Dict with 'score' (int out of 15) and 'details' (per-scenario results).
    """
    if not LLAMA_SERVER_BIN.exists():
        raise RuntimeError(f"llama-server not found at {LLAMA_SERVER_BIN}")

    server_proc = None
    try:
        # Start temporary llama-server
        logger.info("Starting temp llama-server on port %d", port)
        server_proc = subprocess.Popen(
            [str(LLAMA_SERVER_BIN),
             "-m", str(model_path),
             "-ngl", str(ngl),
             "--port", str(port),
             "-c", "8192",
             "--host", "0.0.0.0"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )

        # Wait for health endpoint
        healthy = False
        for attempt in range(60):
            try:
                health = subprocess.run(
                    ["curl", "-sf", f"http://localhost:{port}/health"],
                    capture_output=True, text=True, timeout=5
                )
                if health.returncode == 0:
                    healthy = True
                    logger.info("llama-server healthy after %d seconds", attempt)
                    break
            except subprocess.TimeoutExpired:
                pass
            time.sleep(1)

        if not healthy:
            raise RuntimeError("llama-server failed to become healthy within 60s")

        # Run ToolCall-15 eval
        logger.info("Starting ToolCall-15 evaluation")
        eval_result = subprocess.run(
            ["curl", "-sN", f"http://localhost:3001/api/eval/stream?port={port}"],
            capture_output=True, text=True, timeout=600
        )

        # Parse SSE events
        score = 0
        details = {}
        for line in eval_result.stdout.split("\n"):
            if line.startswith("data:"):
                try:
                    event_data = json.loads(line[5:].strip())
                    if event_data.get("type") == "scenario_result":
                        scenario = event_data.get("scenario", "unknown")
                        passed = event_data.get("passed", False)
                        details[scenario] = passed
                        if passed:
                            score += 1
                except json.JSONDecodeError:
                    continue

        logger.info("ToolCall-15 score: %d/15", score)
        return {"score": score, "details": details}

    finally:
        if server_proc:
            logger.info("Stopping temp llama-server (pid=%d)", server_proc.pid)
            server_proc.terminate()
            try:
                server_proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server_proc.kill()
                server_proc.wait()


def parse_sse_results(sse_text: str) -> Dict:
    """Parse ToolCall-15 SSE output into score and details.

    Args:
        sse_text: Raw SSE text from the eval stream.

    Returns:
        Dict with 'score' and 'details'.
    """
    score = 0
    details = {}
    for line in sse_text.split("\n"):
        line = line.strip()
        if line.startswith("data:"):
            try:
                event_data = json.loads(line[5:].strip())
                if event_data.get("type") == "scenario_result":
                    scenario = event_data.get("scenario", "unknown")
                    passed = event_data.get("passed", False)
                    details[scenario] = passed
                    if passed:
                        score += 1
            except json.JSONDecodeError:
                continue
    return {"score": score, "details": details}


def validate_model(candidate: DiscoveryCandidate,
                   hardware: HardwareProfile,
                   registry: Registry) -> ValidationResult:
    """Full validation pipeline: download, benchmark, evaluate, update registry.

    Args:
        candidate: Discovery candidate to validate.
        hardware: Current hardware profile.
        registry: Model registry instance.

    Returns:
        ValidationResult with all metrics.
    """
    # Register the model in discovered state
    record = registry.add_model(
        name=candidate.name,
        source_url=candidate.hf_url,
        quant=candidate.quant,
        size_gb=candidate.size_gb_est,
        signal_score=candidate.signal_score,
    )
    model_id = record.id
    result = ValidationResult(model_id=model_id)

    try:
        # Download
        registry.update_status(model_id, ModelStatus.downloading)
        if not candidate.hf_url:
            raise RuntimeError("No HuggingFace URL for candidate")
        model_path = download_model(candidate.hf_url)

        # Update path in registry
        conn = registry._get_conn()
        conn.execute("UPDATE models SET gguf_path = ? WHERE id = ?",
                     (str(model_path), model_id))
        conn.commit()

        # Validate
        registry.update_status(model_id, ModelStatus.validating)
        ngl = compute_optimal_ngl(
            candidate.size_gb_est or 4.0, hardware.vram_gb
        )

        # Benchmark
        bench = run_llama_bench(model_path, ngl, threads=hardware.cpu_cores)
        result.tok_s_gen = bench["tok_s_gen"]
        result.tok_s_prompt = bench["tok_s_prompt"]

        # ToolCall-15
        tc15 = run_toolcall15(model_path, ngl)
        result.toolcall15_score = tc15["score"]
        result.toolcall15_details = tc15["details"]

        # Record benchmark
        registry.update_benchmark(
            model_id, "toolcall-15", tc15["score"],
            bench["tok_s_gen"], bench["tok_s_prompt"]
        )

        # Pass/fail threshold: >=10/15 score and >=10 tok/s gen
        result.passed = (tc15["score"] >= 10 and bench["tok_s_gen"] >= 10.0)
        new_status = ModelStatus.passed if result.passed else ModelStatus.failed
        registry.update_status(model_id, new_status)
        logger.info("Validation %s for %s: score=%d gen=%.1f",
                     "PASSED" if result.passed else "FAILED",
                     candidate.name, tc15["score"], bench["tok_s_gen"])

    except Exception as e:
        logger.error("Validation failed for %s: %s", model_id, e)
        result.error = str(e)
        try:
            registry.update_status(model_id, ModelStatus.failed)
        except ValueError:
            pass  # Already in failed state

    return result
