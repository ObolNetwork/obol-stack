#!/usr/bin/env python3
import importlib.util
import sys
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "sell" / "scripts" / "monetize.py"


def load_monetize_module():
    spec = importlib.util.spec_from_file_location("sell_monetize", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class RegistrationMetadataTest(unittest.TestCase):
    def test_build_registration_doc_includes_custom_metadata(self):
        mod = load_monetize_module()
        spec = {
            "type": "http",
            "path": "/services/autoresearch-worker",
            "payment": {"price": {"perHour": "0.50"}},
            "registration": {
                "name": "GPU Worker Alpha",
                "skills": ["machine_learning/model_optimization"],
                "domains": ["technology/artificial_intelligence/research"],
                "metadata": {
                    "gpu": "A100-80GB",
                    "framework": "pytorch",
                    "best_val_bpb": "1.234",
                    "total_experiments": "42",
                },
            },
        }

        doc = mod.build_registration_doc(spec, "autoresearch-worker", "1789", "http://obol.stack:8080")
        self.assertEqual(doc["name"], "GPU Worker Alpha")
        self.assertIn("metadata", doc)
        self.assertEqual(doc["metadata"]["gpu"], "A100-80GB")
        self.assertEqual(doc["metadata"]["best_val_bpb"], "1.234")
        self.assertTrue(any(s.get("name") == "OASF" for s in doc["services"]))

    def test_build_indexed_metadata_includes_registration_metadata(self):
        mod = load_monetize_module()
        spec = {
            "type": "http",
            "registration": {
                "metadata": {
                    "gpu": "A100-80GB",
                    "best_val_bpb": "1.234",
                }
            },
        }
        entries = mod.build_indexed_metadata(spec)
        self.assertEqual(entries["service.type"], b"http")
        self.assertEqual(entries["metadata.gpu"], b"A100-80GB")
        self.assertEqual(entries["metadata.best_val_bpb"], b"1.234")


if __name__ == "__main__":
    unittest.main()
