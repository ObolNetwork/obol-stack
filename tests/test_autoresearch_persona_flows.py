#!/usr/bin/env python3
import importlib.util
import json
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
WORKER_MODULE = ROOT / "internal" / "embed" / "skills" / "autoresearch-worker" / "scripts" / "worker_api.py"
COORD_MODULE = ROOT / "internal" / "embed" / "skills" / "autoresearch-coordinator" / "scripts" / "coordinate.py"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def post_json(url: str, payload: dict, headers: dict | None = None):
    req = Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", **(headers or {})},
        method="POST",
    )
    try:
        with urlopen(req, timeout=10) as resp:
            return resp.status, dict(resp.headers), json.loads(resp.read())
    except HTTPError as e:
        body = e.read()
        try:
            parsed = json.loads(body)
        except json.JSONDecodeError:
            parsed = body.decode("utf-8", errors="replace")
        return e.code, dict(e.headers), parsed


def get_json(url: str):
    with urlopen(url, timeout=10) as resp:
        return resp.status, json.loads(resp.read())


class LocalServer:
    def __init__(self, handler):
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_address[1]}"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


class PersonaFlowsTest(unittest.TestCase):
    def test_gpu_contributor_worker_flow(self):
        worker = load_module("persona_worker_api", WORKER_MODULE)
        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            repo = base / "repo"
            repo.mkdir()
            data = base / "data"
            cfg = worker.WorkerConfig(
                repo=repo,
                data_dir=data,
                command=["python3", "train.py"],
                timeout_seconds=30,
            )
            state = worker.WorkerState(cfg)
            server = LocalServer(lambda *args, **kwargs: worker.WorkerHandler(*args, **kwargs))
            worker.WorkerHandler.state = state
            try:
                status, health = get_json(server.base_url + "/health")
                self.assertEqual(status, 200)
                self.assertEqual(health["status"], "ok")

                code = "import json\nprint(json.dumps({'metrics': {'val_bpb': 1.2345}}))\n"
                status, _, result = post_json(server.base_url + "/experiment", {"train_py": code})
                self.assertEqual(status, 200)
                self.assertEqual(result["status"], "completed")
                self.assertAlmostEqual(result["val_bpb"], 1.2345)

                status, best = get_json(server.base_url + "/best")
                self.assertEqual(status, 200)
                self.assertAlmostEqual(best["val_bpb"], 1.2345)
            finally:
                server.close()

    def test_researcher_flow_discover_and_submit_to_paid_worker(self):
        coord = load_module("persona_coordinate", COORD_MODULE)

        class FakeWorkerHandler(BaseHTTPRequestHandler):
            def log_message(self, format, *args):
                return

            def do_POST(self):
                if self.path != "/services/autoresearch-worker/experiment":
                    self.send_response(404)
                    self.end_headers()
                    return
                length = int(self.headers.get("Content-Length", "0") or "0")
                body = json.loads(self.rfile.read(length) or b"{}")
                if "X-PAYMENT" not in self.headers:
                    payload = {
                        "payTo": "0x2222222222222222222222222222222222222222",
                        "network": "base-sepolia",
                        "maxAmountRequired": "1234",
                        "description": "One autoresearch experiment",
                    }
                    raw = json.dumps(payload).encode("utf-8")
                    self.send_response(402)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                    return
                payload = {
                    "status": "completed",
                    "val_bpb": 1.111,
                    "echo": body,
                }
                raw = json.dumps(payload).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        worker_server = LocalServer(FakeWorkerHandler)

        registration = {
            "x402Support": True,
            "services": [
                {"name": "web", "endpoint": worker_server.base_url + "/services/autoresearch-worker"},
                {
                    "name": "OASF",
                    "version": "0.8",
                    "skills": ["machine_learning/model_optimization"],
                    "domains": ["technology/artificial_intelligence/research"],
                },
            ],
            "metadata": {"best_val_bpb": "1.111", "updated": "2026-03-12T10:30:00Z"},
        }

        class FakeScanHandler(BaseHTTPRequestHandler):
            def log_message(self, format, *args):
                return

            def do_GET(self):
                if self.path.startswith("/api/v1/public/agents"):
                    payload = {
                        "success": True,
                        "data": [
                            {
                                "agent_id": "84532:0xabc:1",
                                "token_id": "1",
                                "chain_id": 84532,
                                "name": "GPU Worker Alpha",
                                "x402_supported": True,
                                "raw_metadata": {"offchain_content": registration},
                            }
                        ],
                    }
                    raw = json.dumps(payload).encode("utf-8")
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                    return
                self.send_response(404)
                self.end_headers()

        scan_server = LocalServer(FakeScanHandler)
        try:
            coord.SCAN_API_URL = scan_server.base_url + "/api/v1/public"
            coord.HAS_SIGNER = True
            coord._signer_get = lambda path: {"keys": ["0x1111111111111111111111111111111111111111"]}
            coord._signer_post = lambda path, payload: {"signature": "0xsigned"}

            coordinator = coord.ObolCoordinator(chain="base-sepolia")
            workers = coordinator.discover_workers(limit=1)
            self.assertEqual(len(workers), 1)
            self.assertEqual(workers[0]["endpoint"], worker_server.base_url + "/services/autoresearch-worker")
            self.assertTrue(workers[0]["x402"])

            result = coordinator.submit_experiment(workers[0]["endpoint"], "print('hello')\n")
            self.assertIsNotNone(result)
            self.assertAlmostEqual(result["val_bpb"], 1.111)
        finally:
            scan_server.close()
            worker_server.close()

    def test_service_builder_and_consumer_resume_flow(self):
        coord = load_module("persona_coordinate_for_resume", COORD_MODULE)

        class ResumeGateHandler(BaseHTTPRequestHandler):
            def log_message(self, format, *args):
                return

            def do_POST(self):
                if self.path != "/services/cv-enhancer":
                    self.send_response(404)
                    self.end_headers()
                    return
                length = int(self.headers.get("Content-Length", "0") or "0")
                body = json.loads(self.rfile.read(length) or b"{}")
                if "X-PAYMENT" not in self.headers:
                    payload = {
                        "payTo": "0x3333333333333333333333333333333333333333",
                        "network": "base-sepolia",
                        "maxAmountRequired": "50000",
                        "description": "Resume enhancement request",
                    }
                    raw = json.dumps(payload).encode("utf-8")
                    self.send_response(402)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                    return
                improved = "Summary\nImproved professional resume for: " + body.get("input", "")
                raw = json.dumps({"output": improved}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        server = LocalServer(ResumeGateHandler)
        try:
            status, headers, body = post_json(server.base_url + "/services/cv-enhancer", {"input": "John Doe resume"})
            self.assertEqual(status, 402)
            pricing = coord.parse_402_pricing(headers, body)
            self.assertIsNotNone(pricing)
            self.assertEqual(pricing["network"], "base-sepolia")
            self.assertEqual(pricing["maxAmountRequired"], "50000")

            status, _, body = post_json(
                server.base_url + "/services/cv-enhancer",
                {"input": "John Doe resume"},
                headers={"X-PAYMENT": '{"mock":true}'},
            )
            self.assertEqual(status, 200)
            self.assertIn("Improved professional resume", body["output"])
            self.assertIn("John Doe resume", body["output"])
        finally:
            server.close()


if __name__ == "__main__":
    unittest.main()
