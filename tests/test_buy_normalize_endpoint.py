#!/usr/bin/env python3
"""Unit tests for buy.py's _normalize_endpoint helper.

Regression suite for the bug where a seller's ServiceOffer
upstream.healthPath (e.g. /api/tags for Ollama) leaked into the
buyer's PurchaseRequest endpoint URL, producing dead URLs like
``http://traefik.../services/demo-hello/api/tags/v1/chat/completions``.

Root cause: ``_normalize_endpoint`` only stripped trailing
``/v1/chat/completions`` / ``/chat/completions`` suffixes. Anything
between ``/services/<name>`` and the LLM suffix survived, and the
spec builder then re-appended ``/v1/chat/completions`` on top of it.
"""
import importlib.util
import sys
import unittest
from pathlib import Path

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "internal"
    / "embed"
    / "skills"
    / "buy-x402"
    / "scripts"
    / "buy.py"
)


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_x402", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class NormalizeEndpointTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    # ── existing behaviour is preserved ───────────────────────────────────

    def test_strips_trailing_slash(self):
        self.assertEqual(
            self.buy._normalize_endpoint("http://example.com/services/demo/"),
            "http://example.com/services/demo",
        )

    def test_strips_v1_chat_completions(self):
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://example.com/services/demo/v1/chat/completions"
            ),
            "http://example.com/services/demo",
        )

    def test_strips_chat_completions(self):
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://example.com/services/demo/chat/completions"
            ),
            "http://example.com/services/demo",
        )

    def test_canonical_offer_base_unchanged(self):
        self.assertEqual(
            self.buy._normalize_endpoint("http://traefik.svc/services/demo"),
            "http://traefik.svc/services/demo",
        )

    def test_traefik_in_cluster_url(self):
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.traefik.svc.cluster.local/services/demo-hello"
            ),
            "http://traefik.traefik.svc.cluster.local/services/demo-hello",
        )

    # ── healthPath leak regression ────────────────────────────────────────
    #
    # These cases reproduce the original bug. Before the fix
    # ``_normalize_endpoint`` returned the input untouched, and the spec
    # builder then formed ``.../services/demo-hello/api/tags/v1/chat/completions``.

    def test_strips_health_path_api_tags(self):
        # Ollama healthPath.
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.svc/services/demo-hello/api/tags"
            ),
            "http://traefik.svc/services/demo-hello",
        )

    def test_strips_health_path_health(self):
        # Default ``/health`` healthPath.
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.svc/services/demo/health"
            ),
            "http://traefik.svc/services/demo",
        )

    def test_strips_nested_health_path(self):
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.svc/services/demo/api/v1/status"
            ),
            "http://traefik.svc/services/demo",
        )

    def test_strips_health_path_then_chat_suffix(self):
        # Belt-and-braces: even if both were appended, only the offer base
        # should survive.
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.svc/services/demo/api/tags/v1/chat/completions"
            ),
            "http://traefik.svc/services/demo",
        )

    def test_strips_trailing_slash_on_health_path(self):
        self.assertEqual(
            self.buy._normalize_endpoint(
                "http://traefik.svc/services/demo/api/tags/"
            ),
            "http://traefik.svc/services/demo",
        )

    # Final assembled URL: this is the load-bearing assertion. The bug
    # produced ``/services/demo/api/tags/v1/chat/completions`` (404 from
    # upstream); the fix must produce ``/services/demo/v1/chat/completions``.
    def test_assembled_purchase_endpoint_drops_health_path(self):
        normalized = self.buy._normalize_endpoint(
            "http://traefik.svc/services/demo-hello/api/tags"
        )
        assembled = normalized + "/v1/chat/completions"
        self.assertEqual(
            assembled,
            "http://traefik.svc/services/demo-hello/v1/chat/completions",
        )
        self.assertNotIn("/api/tags/", assembled)

    # ── non-/services URLs must not be over-normalized ────────────────────

    def test_non_services_path_preserved(self):
        # External x402 sellers may publish offers under any path; the
        # ``/services/<name>`` truncation must only fire for the obol
        # storefront convention.
        self.assertEqual(
            self.buy._normalize_endpoint(
                "https://seller.example.com/api/inference"
            ),
            "https://seller.example.com/api/inference",
        )

    def test_root_path_preserved(self):
        self.assertEqual(
            self.buy._normalize_endpoint("https://seller.example.com/"),
            "https://seller.example.com",
        )


if __name__ == "__main__":
    unittest.main()
