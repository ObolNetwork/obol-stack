#!/usr/bin/env python3
"""worker_api.py -- GPU worker HTTP API for autoresearch experiments.

Runs submitted train.py experiments one at a time, stores results on disk, and
exposes a minimal HTTP API suitable for x402-gated selling via `obol sell http`.

Endpoints:
  GET  /health, /healthz      Worker health and config summary
  GET  /status                Busy/idle status plus last/best results
  GET  /best                  Best known result
  GET  /experiments/<id>      Fetch a stored experiment result
  POST /experiment            Submit a train.py experiment (or probe with {"probe": true})

Environment:
  DATA_DIR                    Base directory for worker state (default: /data)
  AUTORESEARCH_REPO           Template repo/workdir copied per experiment
  EXPERIMENT_TIMEOUT_SECONDS  Max runtime per experiment (default: 300)
  TRAIN_COMMAND               Command to run inside experiment workdir (default: uv run train.py)
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import urlparse
import uuid

CHECKPOINT_EXTENSIONS = (".gguf", ".safetensors", ".pt", ".pth", ".bin")
COPY_IGNORE = shutil.ignore_patterns(
    ".git",
    ".venv",
    "__pycache__",
    ".pytest_cache",
    ".mypy_cache",
    "worktrees",
    "queue",
    "results",
)


class BusyError(RuntimeError):
    pass


@dataclass
class WorkerConfig:
    repo: Path
    data_dir: Path
    command: list[str]
    timeout_seconds: int


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def sha256_text(text: str) -> str:
    return f"sha256:{hashlib.sha256(text.encode()).hexdigest()}"


def generate_experiment_id() -> str:
    return f"exp-{datetime.now(timezone.utc).strftime('%Y%m%d')}-{uuid.uuid4().hex[:8]}"


def extract_val_bpb(log_text: str, workdir: Path | None = None) -> float | None:
    """Extract val_bpb from log output or results.tsv.

    Prefers structured JSON payloads, then common textual patterns, then the
    smallest val_bpb recorded in a results.tsv file if present.
    """
    stripped = (log_text or "").strip()
    if stripped:
        # JSON payloads emitted as either a top-level object or metrics object.
        try:
            data = json.loads(stripped)
            if isinstance(data, dict):
                if data.get("val_bpb") is not None:
                    return float(data["val_bpb"])
                metrics = data.get("metrics")
                if isinstance(metrics, dict) and metrics.get("val_bpb") is not None:
                    return float(metrics["val_bpb"])
        except (json.JSONDecodeError, ValueError, TypeError):
            pass

        patterns = [
            r"\bval_bpb\b\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)",
            r"\bbest[_ ]val[_ ]bpb\b\s*[:=]\s*([0-9]+(?:\.[0-9]+)?)",
            r'"val_bpb"\s*:\s*([0-9]+(?:\.[0-9]+)?)',
        ]
        for pattern in patterns:
            match = re.search(pattern, stripped, flags=re.IGNORECASE)
            if match:
                try:
                    return float(match.group(1))
                except ValueError:
                    pass

    if workdir is not None:
        results_path = Path(workdir) / "results.tsv"
        if results_path.exists():
            best: float | None = None
            with open(results_path, "r", encoding="utf-8") as f:
                reader = csv.DictReader(f, delimiter="\t")
                for row in reader:
                    value = row.get("val_bpb")
                    if not value:
                        continue
                    try:
                        parsed = float(value)
                    except ValueError:
                        continue
                    if best is None or parsed < best:
                        best = parsed
            return best

    return None


def find_artifact(root: Path) -> Path | None:
    """Return the newest checkpoint-like artifact under root."""
    candidates: list[Path] = []
    for ext in CHECKPOINT_EXTENSIONS:
        candidates.extend(root.rglob(f"*{ext}"))
    candidates = [p for p in candidates if p.is_file() and ".git" not in p.parts]
    if not candidates:
        return None
    return max(candidates, key=lambda p: p.stat().st_mtime)


def choose_best_result(existing: dict[str, Any] | None, candidate: dict[str, Any] | None) -> dict[str, Any] | None:
    """Pick the better result by lower val_bpb, treating missing values as worse."""
    if not candidate:
        return existing
    if candidate.get("val_bpb") is None:
        return existing
    if not existing or existing.get("val_bpb") is None:
        return candidate
    try:
        if float(candidate["val_bpb"]) < float(existing["val_bpb"]):
            return candidate
    except (ValueError, TypeError):
        return existing
    return existing


class WorkerState:
    def __init__(self, config: WorkerConfig):
        self.config = config
        self.lock = threading.Lock()
        self.busy = False
        self.current: dict[str, Any] | None = None
        self.last_result: dict[str, Any] | None = None
        self.base_dir = config.data_dir / "autoresearch-worker"
        self.results_dir = self.base_dir / "results"
        self.best_path = self.base_dir / "best.json"
        self.history_path = self.base_dir / "results.jsonl"
        self.base_dir.mkdir(parents=True, exist_ok=True)
        self.results_dir.mkdir(parents=True, exist_ok=True)
        self.best_result = self._load_json(self.best_path)

    def _load_json(self, path: Path) -> dict[str, Any] | None:
        if not path.exists():
            return None
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return None

    def snapshot(self) -> dict[str, Any]:
        with self.lock:
            return {
                "status": "busy" if self.busy else "idle",
                "current": self.current,
                "last": self.last_result,
                "best": self.best_result,
                "repo": str(self.config.repo),
                "command": self.config.command,
                "timeoutSeconds": self.config.timeout_seconds,
            }

    def record_result(self, result: dict[str, Any]) -> None:
        exp_dir = self.results_dir / result["experiment_id"]
        exp_dir.mkdir(parents=True, exist_ok=True)
        (exp_dir / "result.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
        with open(self.history_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(result) + "\n")
        best = choose_best_result(self.best_result, result)
        if best is not self.best_result and best is not None:
            self.best_result = best
            self.best_path.write_text(json.dumps(best, indent=2), encoding="utf-8")
        self.last_result = result

    def run_experiment(self, train_py_source: str, config_overrides: dict[str, Any] | None = None, experiment_id: str | None = None) -> dict[str, Any]:
        if not train_py_source.strip():
            raise ValueError("train_py is required")

        with self.lock:
            if self.busy:
                raise BusyError("worker is already running an experiment")
            exp_id = experiment_id or generate_experiment_id()
            self.busy = True
            self.current = {
                "experiment_id": exp_id,
                "startedAt": utc_now(),
            }

        try:
            result = self._run_experiment_impl(exp_id, train_py_source, config_overrides or {})
            self.record_result(result)
            return result
        finally:
            with self.lock:
                self.busy = False
                self.current = None

    def _run_experiment_impl(self, experiment_id: str, train_py_source: str, config_overrides: dict[str, Any]) -> dict[str, Any]:
        exp_dir = self.results_dir / experiment_id
        workdir = exp_dir / "work"
        exp_dir.mkdir(parents=True, exist_ok=True)

        started = time.time()
        started_at = utc_now()
        train_hash = sha256_text(train_py_source)
        artifact: Path | None = None
        status = "completed"
        return_code = 0
        stdout = ""
        stderr = ""
        note = None

        if self.config.repo.exists():
            shutil.copytree(self.config.repo, workdir, ignore=COPY_IGNORE)
        else:
            workdir.mkdir(parents=True, exist_ok=True)

        train_path = workdir / "train.py"
        train_path.write_text(train_py_source, encoding="utf-8")
        (exp_dir / "train.py").write_text(train_py_source, encoding="utf-8")

        config_path = exp_dir / "config.json"
        config_path.write_text(json.dumps(config_overrides, indent=2), encoding="utf-8")

        env = os.environ.copy()
        env.setdefault("PYTHONUNBUFFERED", "1")
        env["AUTORESEARCH_EXPERIMENT_ID"] = experiment_id
        env["AUTORESEARCH_EXPERIMENT_DIR"] = str(exp_dir)
        env["AUTORESEARCH_EXPERIMENT_CONFIG"] = str(config_path)

        try:
            completed = subprocess.run(
                self.config.command,
                cwd=workdir,
                env=env,
                capture_output=True,
                text=True,
                timeout=self.config.timeout_seconds,
                check=False,
            )
            stdout = completed.stdout
            stderr = completed.stderr
            return_code = completed.returncode
            if completed.returncode != 0:
                status = "failed"
                note = f"command exited with status {completed.returncode}"
        except subprocess.TimeoutExpired as exc:
            stdout = exc.stdout or ""
            stderr = exc.stderr or ""
            return_code = -1
            status = "timeout"
            note = f"experiment exceeded timeout of {self.config.timeout_seconds}s"
        except OSError as exc:
            stderr = str(exc)
            return_code = -1
            status = "failed"
            note = f"failed to execute command: {exc}"

        log_text = stdout
        if stderr:
            log_text += ("\n--- stderr ---\n" if log_text else "") + stderr
        log_path = exp_dir / "run.log"
        log_path.write_text(log_text, encoding="utf-8")

        val_bpb = extract_val_bpb(log_text, workdir)
        artifact = find_artifact(workdir)

        result = {
            "experiment_id": experiment_id,
            "status": status,
            "return_code": return_code,
            "val_bpb": val_bpb,
            "train_hash": train_hash,
            "artifact_path": str(artifact) if artifact else None,
            "log_path": str(log_path),
            "startedAt": started_at,
            "finishedAt": utc_now(),
            "durationSeconds": round(time.time() - started, 3),
            "config": config_overrides,
        }
        if note:
            result["note"] = note
        return result


class WorkerHandler(BaseHTTPRequestHandler):
    state: WorkerState | None = None

    def log_message(self, format: str, *args: Any) -> None:
        sys.stderr.write("%s - - [%s] %s\n" % (self.address_string(), self.log_date_time_string(), format % args))

    def _json(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length <= 0:
            return {}
        raw = self.rfile.read(length)
        if not raw:
            return {}
        try:
            data = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid JSON body: {exc}") from exc
        if not isinstance(data, dict):
            raise ValueError("JSON body must be an object")
        return data

    def do_GET(self) -> None:
        assert self.state is not None
        path = urlparse(self.path).path
        if path in ("/health", "/healthz"):
            self._json(200, {
                "status": "ok",
                "busy": self.state.snapshot()["status"] == "busy",
                "repo": str(self.state.config.repo),
                "timeoutSeconds": self.state.config.timeout_seconds,
            })
            return
        if path == "/status":
            self._json(200, self.state.snapshot())
            return
        if path == "/best":
            if self.state.best_result is None:
                self._json(404, {"error": "no completed experiments yet"})
                return
            self._json(200, self.state.best_result)
            return
        if path.startswith("/experiments/"):
            exp_id = path.rsplit("/", 1)[-1]
            result_path = self.state.results_dir / exp_id / "result.json"
            if not result_path.exists():
                self._json(404, {"error": f"experiment {exp_id} not found"})
                return
            self._json(200, json.loads(result_path.read_text(encoding="utf-8")))
            return
        self._json(404, {"error": f"unknown endpoint: {path}"})

    def do_POST(self) -> None:
        assert self.state is not None
        path = urlparse(self.path).path
        if path != "/experiment":
            self._json(404, {"error": f"unknown endpoint: {path}"})
            return

        try:
            payload = self._read_json()
        except ValueError as exc:
            self._json(400, {"error": str(exc)})
            return

        if payload.get("probe") is True:
            self._json(200, {
                "status": "ok",
                "probe": True,
                "busy": self.state.snapshot()["status"] == "busy",
                "timeoutSeconds": self.state.config.timeout_seconds,
            })
            return

        train_py = payload.get("train_py", payload.get("trainPy"))
        if not isinstance(train_py, str) or not train_py.strip():
            self._json(400, {"error": "train_py string is required"})
            return

        config_overrides = payload.get("config")
        if config_overrides is None:
            config_overrides = {}
        if not isinstance(config_overrides, dict):
            self._json(400, {"error": "config must be a JSON object when provided"})
            return

        experiment_id = payload.get("experiment_id", payload.get("experimentId"))
        if experiment_id is not None and not isinstance(experiment_id, str):
            self._json(400, {"error": "experiment_id must be a string when provided"})
            return

        try:
            result = self.state.run_experiment(train_py, config_overrides, experiment_id)
        except BusyError as exc:
            self._json(409, {"error": str(exc), "status": "busy"})
            return
        except ValueError as exc:
            self._json(400, {"error": str(exc)})
            return
        except Exception as exc:
            self._json(500, {"error": f"internal worker error: {exc}"})
            return

        self._json(200, result)


def make_server(state: WorkerState, host: str, port: int) -> ThreadingHTTPServer:
    WorkerHandler.state = state
    return ThreadingHTTPServer((host, port), WorkerHandler)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run the autoresearch GPU worker API.")
    parser.add_argument("command", nargs="?", default="serve", choices=["serve"], help="Command to run (default: serve)")
    parser.add_argument("--repo", default=os.environ.get("AUTORESEARCH_REPO", "/data/autoresearch"), help="Path to the base autoresearch repo/workdir")
    parser.add_argument("--data-dir", default=os.environ.get("DATA_DIR", "/data"), help="Directory for worker state and results")
    parser.add_argument("--host", default="0.0.0.0", help="Listen host")
    parser.add_argument("--port", type=int, default=8080, help="Listen port")
    parser.add_argument("--timeout", type=int, default=int(os.environ.get("EXPERIMENT_TIMEOUT_SECONDS", "300")), help="Max experiment runtime in seconds")
    parser.add_argument("--train-command", default=os.environ.get("TRAIN_COMMAND", "uv run train.py"), help="Command used to run train.py")
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    command = shlex.split(args.train_command)
    config = WorkerConfig(
        repo=Path(args.repo),
        data_dir=Path(args.data_dir),
        command=command,
        timeout_seconds=args.timeout,
    )
    state = WorkerState(config)
    server = make_server(state, args.host, args.port)
    print(f"autoresearch-worker listening on http://{args.host}:{args.port}")
    print(f"repo={config.repo} data_dir={config.data_dir} timeout={config.timeout_seconds}s command={' '.join(config.command)}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nshutting down worker")
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
