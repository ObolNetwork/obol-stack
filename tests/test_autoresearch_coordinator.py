#!/usr/bin/env python3
import importlib.util
import sys
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "autoresearch-coordinator" / "scripts" / "coordinate.py"


def load_coordinate_module():
    spec = importlib.util.spec_from_file_location("autoresearch_coordinate", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class CoordinatorHelpersTest(unittest.TestCase):
    def test_build_scan_api_url_uses_agents_endpoint(self):
        mod = load_coordinate_module()
        url = mod.build_scan_api_url(protocol="OASF", search="machine_learning/model_optimization", limit=5)
        self.assertIn("/agents?", url)
        self.assertIn("protocol=OASF", url)
        self.assertIn("limit=5", url)

    def test_normalize_public_api_url_adds_public_suffix(self):
        mod = load_coordinate_module()
        self.assertEqual(
            mod.normalize_public_api_url("http://indexer.internal:8088"),
            "http://indexer.internal:8088/api/v1/public",
        )
        self.assertEqual(
            mod.normalize_public_api_url("http://indexer.internal:8088/api/v1/public"),
            "http://indexer.internal:8088/api/v1/public",
        )

    def test_build_indexer_health_url_strips_public_suffix(self):
        mod = load_coordinate_module()
        self.assertEqual(
            mod.build_indexer_health_url("http://indexer.internal:8088/api/v1/public"),
            "http://indexer.internal:8088/health",
        )

    def test_extract_registration_from_agent_prefers_offchain_content(self):
        mod = load_coordinate_module()
        registration = {
            "services": [{"name": "web", "endpoint": "https://worker.example.com/services/autoresearch-worker"}],
            "metadata": {"best_val_bpb": "1.234"},
        }
        agent = {
            "raw_metadata": {
                "offchain_content": registration,
            }
        }
        self.assertEqual(mod.extract_registration_from_agent(agent), registration)

    def test_extract_oasf_skills_from_services_entry(self):
        mod = load_coordinate_module()
        registration = {
            "services": [
                {"name": "OASF", "skills": ["machine_learning/model_optimization"], "domains": ["technology/artificial_intelligence/research"]}
            ]
        }
        skills = mod.extract_oasf_skills(registration)
        self.assertIn("machine_learning/model_optimization", skills)
        self.assertIn("technology/artificial_intelligence/research", skills)

    def test_sign_erc3009_auth_uses_current_signer_api(self):
        mod = load_coordinate_module()
        calls = []

        def fake_get(path):
            calls.append(("GET", path))
            return {"keys": ["0x1111111111111111111111111111111111111111"]}

        def fake_post(path, payload):
            calls.append(("POST", path, payload))
            return {"signature": "0xsigned"}

        mod.HAS_SIGNER = True
        mod._signer_get = fake_get
        mod._signer_post = fake_post

        signed = mod.sign_erc3009_auth(
            "0x2222222222222222222222222222222222222222",
            1234,
            "base-sepolia",
        )

        self.assertEqual(calls[0], ("GET", "/api/v1/keys"))
        self.assertEqual(calls[1][0], "POST")
        self.assertEqual(calls[1][1], "/api/v1/sign/0x1111111111111111111111111111111111111111/typed-data")
        self.assertEqual(signed["signature"], "0xsigned")
        self.assertEqual(signed["authorization"]["from"], "0x1111111111111111111111111111111111111111")


if __name__ == "__main__":
    unittest.main()
