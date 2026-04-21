#!/usr/bin/env python3
import importlib.util
import sys
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "buy-inference" / "scripts" / "buy.py"


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_inference_buy", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class BuyAutorefillHelpersTest(unittest.TestCase):
    def test_resolve_auto_refill_enables_when_policy_flags_present(self):
        mod = load_buy_module()
        policy = mod._resolve_auto_refill({"refill_count": "25"}, desired_count=100, existing_policy=None)
        self.assertTrue(policy["enabled"])
        self.assertEqual(policy["count"], 25)
        self.assertEqual(policy["threshold"], 20)

    def test_resolve_auto_refill_preserves_existing_without_overrides(self):
        mod = load_buy_module()
        existing = {"enabled": True, "threshold": 7, "count": 33, "maxTotal": 80}
        self.assertEqual(mod._resolve_auto_refill({}, desired_count=100, existing_policy=existing), existing)

    def test_plan_autorefill_refills_at_threshold_and_caps_to_active_pool_limit(self):
        mod = load_buy_module()
        count, reason = mod._plan_autorefill(
            10,
            {"enabled": True, "threshold": 10, "count": 25, "maxTotal": 20},
        )
        self.assertEqual(count, 10)
        self.assertIn("at or below threshold 10", reason)

    def test_plan_autorefill_skips_when_above_threshold_or_daily_cap_requested(self):
        mod = load_buy_module()
        count, reason = mod._plan_autorefill(
            11,
            {"enabled": True, "threshold": 10, "count": 25},
        )
        self.assertEqual(count, 0)
        self.assertIn("above threshold 10", reason)

        count, reason = mod._plan_autorefill(
            0,
            {"enabled": True, "threshold": 0, "count": 25, "maxSpendPerDay": "1000000"},
        )
        self.assertEqual(count, 0)
        self.assertIn("not implemented", reason)

    def test_compact_active_auths_drops_spent_prefix(self):
        mod = load_buy_module()
        auths = [{"nonce": "a"}, {"nonce": "b"}, {"nonce": "c"}]
        self.assertEqual(mod._compact_active_auths(auths, 2), [{"nonce": "c"}])


if __name__ == "__main__":
    unittest.main()
