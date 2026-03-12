#!/usr/bin/env python3
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "autoresearch" / "scripts" / "publish.py"


def load_publish_module():
    spec = importlib.util.spec_from_file_location("autoresearch_publish", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class AutoresearchProvenanceTest(unittest.TestCase):
    def test_generate_provenance_uses_canonical_shape(self):
        publish = load_publish_module()
        best = {
            "commit_hash": "abc123def456",
            "val_bpb": "0.9973",
            "description": "better architecture",
        }
        with tempfile.TemporaryDirectory() as td:
            path = publish.generate_provenance(td, best, "deadbeefcafebabe", 50000000)
            data = json.loads(Path(path).read_text())

        self.assertEqual(
            data,
            {
                "framework": "autoresearch",
                "metricName": "val_bpb",
                "metricValue": "0.9973",
                "experimentId": "abc123def456",
                "trainHash": "sha256:deadbeefcafebabe",
                "paramCount": "50000000",
            },
        )


if __name__ == "__main__":
    unittest.main()
