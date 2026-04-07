#!/usr/bin/env python3
import importlib.util
import os
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "autoresearch-worker" / "scripts" / "worker_api.py"


def load_worker_module():
    spec = importlib.util.spec_from_file_location("autoresearch_worker_api", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class WorkerHelpersTest(unittest.TestCase):
    def test_extract_val_bpb_from_plaintext(self):
        worker = load_worker_module()
        text = "training complete\nval_bpb: 1.2345\n"
        self.assertEqual(worker.extract_val_bpb(text), 1.2345)

    def test_extract_val_bpb_from_json_metrics(self):
        worker = load_worker_module()
        text = '{"metrics": {"val_bpb": 0.9987}}'
        self.assertEqual(worker.extract_val_bpb(text), 0.9987)

    def test_extract_val_bpb_from_results_tsv(self):
        worker = load_worker_module()
        with tempfile.TemporaryDirectory() as td:
            results = Path(td) / "results.tsv"
            results.write_text(
                "commit_hash\tval_bpb\tstatus\tdescription\n"
                "aaa\t1.250\tkeep\tbaseline\n"
                "bbb\t1.111\tkeep\tbetter\n"
            )
            self.assertEqual(worker.extract_val_bpb("", Path(td)), 1.111)

    def test_find_artifact_prefers_latest_checkpoint_like_file(self):
        worker = load_worker_module()
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            a = root / "older.pt"
            b = root / "newer.gguf"
            a.write_text("x")
            b.write_text("y")
            os.utime(a, (1, 1))
            os.utime(b, (2, 2))
            self.assertEqual(worker.find_artifact(root), b)

    def test_choose_best_result_prefers_lower_val_bpb(self):
        worker = load_worker_module()
        old = {"val_bpb": 1.2, "experiment_id": "old"}
        new = {"val_bpb": 1.1, "experiment_id": "new"}
        self.assertEqual(worker.choose_best_result(old, new)["experiment_id"], "new")


if __name__ == "__main__":
    unittest.main()
