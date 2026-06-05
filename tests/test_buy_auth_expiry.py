#!/usr/bin/env python3
"""Unit tests for buy.py auth-pool expiry (_auth_expiry).

Locks the unified expiry contract shared by the Permit2 `deadline` and the
ERC-3009 `validBefore`: default 1 month, configurable seconds (floored at the
settle window), and a `never` sentinel that the x402 facilitator accepts
(validBefore/deadline only checked as >= now + small buffer, with no
maxTimeoutSeconds upper bound).
"""
import importlib.util
import os
import sys
import time
import unittest
from pathlib import Path

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "internal" / "embed" / "skills" / "buy-x402" / "scripts" / "buy.py"
)


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_x402", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class AuthExpiryTest(unittest.TestCase):
    def setUp(self):
        self.buy = load_buy_module()
        os.environ.pop("OBOL_X402_AUTH_TTL", None)

    def tearDown(self):
        os.environ.pop("OBOL_X402_AUTH_TTL", None)

    def test_default_is_one_month(self):
        now = int(time.time())
        self.assertAlmostEqual(
            self.buy._auth_expiry() - now, 30 * 24 * 3600, delta=5
        )

    def test_never_aliases_map_to_sentinel(self):
        for value in ("never", "0", "none", "-1", "NEVER", " never "):
            os.environ["OBOL_X402_AUTH_TTL"] = value
            self.assertEqual(
                self.buy._auth_expiry(), self.buy.MAX_SAFE_DEADLINE, msg=value
            )

    def test_custom_seconds(self):
        os.environ["OBOL_X402_AUTH_TTL"] = "3600"
        now = int(time.time())
        self.assertAlmostEqual(self.buy._auth_expiry() - now, 3600, delta=5)

    def test_floored_at_settle_window(self):
        # Values below the 300s settle-window floor are clamped up.
        os.environ["OBOL_X402_AUTH_TTL"] = "10"
        now = int(time.time())
        self.assertAlmostEqual(self.buy._auth_expiry() - now, 300, delta=5)

    def test_garbage_falls_back_to_default(self):
        os.environ["OBOL_X402_AUTH_TTL"] = "not-a-number"
        now = int(time.time())
        self.assertAlmostEqual(
            self.buy._auth_expiry() - now, 30 * 24 * 3600, delta=5
        )

    def test_sentinel_is_facilitator_safe(self):
        # 0xFFFFFFFF (~year 2106): a real uint the facilitator accepts
        # (validBefore/deadline must be >= now + buffer; no upper bound).
        self.assertEqual(self.buy.MAX_SAFE_DEADLINE, 0xFFFFFFFF)
        self.assertGreater(self.buy.MAX_SAFE_DEADLINE, int(time.time()) + 6)


if __name__ == "__main__":
    unittest.main()
