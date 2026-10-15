#!/usr/bin/env python3
"""Unit tests for buy.py's per-call price ceiling (--max-price).

One-shot `pay`, `pay-agent` and `go` sign the seller-quoted price as-is, so
they refuse — before any signing — a quote above --max-price (token units;
default $OBOL_BUY_MAX_PRICE, else 1). The comparison is exact (Decimal /
integer atomic units), never float.
"""
import importlib.util
import io
import os
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from decimal import Decimal
from pathlib import Path
from unittest import mock

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "internal"
    / "embed"
    / "skills"
    / "buy-x402"
    / "scripts"
    / "buy.py"
)

USDC_BASE_SEPOLIA = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
OBOL_MAINNET = "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7"
UNKNOWN_TOKEN = "0x1111111111111111111111111111111111111111"


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_x402_max_price", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class ResolveMaxPriceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_default_is_one_token(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("OBOL_BUY_MAX_PRICE", None)
            self.assertEqual(self.buy._resolve_max_price(None), Decimal("1"))

    def test_env_overrides_default(self):
        with mock.patch.dict(os.environ, {"OBOL_BUY_MAX_PRICE": "0.25"}):
            self.assertEqual(self.buy._resolve_max_price(None), Decimal("0.25"))

    def test_flag_overrides_env(self):
        with mock.patch.dict(os.environ, {"OBOL_BUY_MAX_PRICE": "0.25"}):
            self.assertEqual(self.buy._resolve_max_price("5"), Decimal("5"))

    def test_rejects_invalid_values(self):
        for bad in ("abc", "-1", "NaN", "Infinity", "1 USDC"):
            with self.subTest(value=bad):
                with self.assertRaises(ValueError):
                    self.buy._resolve_max_price(bad)
        with self.assertRaises(ValueError):
            self.buy._resolve_max_price(True)  # bare --max-price with no value

    def test_rejects_invalid_env(self):
        with mock.patch.dict(os.environ, {"OBOL_BUY_MAX_PRICE": "lots"}):
            with self.assertRaises(ValueError) as ctx:
                self.buy._resolve_max_price(None)
            self.assertIn("OBOL_BUY_MAX_PRICE", str(ctx.exception))


class CeilingComparisonTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_usdc_boundaries(self):
        exceeds = self.buy._price_exceeds_ceiling
        self.assertFalse(exceeds(1_000_000, 6, Decimal("1")))    # exactly 1 USDC
        self.assertTrue(exceeds(1_000_001, 6, Decimal("1")))     # 1.000001 USDC
        self.assertFalse(exceeds(1_000, 6, Decimal("0.001")))
        self.assertTrue(exceeds(1, 6, Decimal("0")))

    def test_18_decimals_is_exact(self):
        exceeds = self.buy._price_exceeds_ceiling
        one_obol = 10**18
        self.assertFalse(exceeds(one_obol, 18, Decimal("1")))
        # One wei over the ceiling must be detected (float would round it away).
        self.assertTrue(exceeds(one_obol + 1, 18, Decimal("1")))
        big = 123_456_789 * 10**18 + 1
        self.assertTrue(exceeds(big, 18, Decimal("123456789")))
        self.assertFalse(exceeds(big, 18, Decimal("123456789.000000000000000001")))

    def test_refusal_message_names_price_and_how_to_raise(self):
        msg = self.buy._max_price_refusal("2500000", USDC_BASE_SEPOLIA, {}, Decimal("1"))
        self.assertIsNotNone(msg)
        self.assertIn("2.5 USDC", msg)
        self.assertIn("--max-price 2.5", msg)
        self.assertIn("OBOL_BUY_MAX_PRICE", msg)

    def test_within_ceiling_returns_none(self):
        self.assertIsNone(self.buy._max_price_refusal("1000", USDC_BASE_SEPOLIA, {}, Decimal("1")))
        self.assertIsNone(self.buy._max_price_refusal(str(10**18), OBOL_MAINNET, {}, Decimal("1")))

    def test_obol_over_default_ceiling(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("OBOL_BUY_MAX_PRICE", None)
            msg = self.buy._max_price_refusal(str(5 * 10**18), OBOL_MAINNET, {}, None)
        self.assertIsNotNone(msg)
        self.assertIn("5 OBOL", msg)

    def test_unknown_decimals_is_refused(self):
        msg = self.buy._max_price_refusal("1", UNKNOWN_TOKEN, {}, Decimal("1000"))
        self.assertIsNotNone(msg)
        self.assertIn("unknown", msg)


class OneShotCommandsRefuseBeforeSigningTests(unittest.TestCase):
    """pay / pay-agent exit before the signer is touched when over the ceiling."""

    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def _pricing(self, amount):
        return {
            "accepts": [{
                "scheme": "exact",
                "network": "base-sepolia",
                "amount": str(amount),
                "asset": USDC_BASE_SEPOLIA,
                "payTo": "0x2222222222222222222222222222222222222222",
                "extra": {"name": "USDC", "version": "2"},
            }],
        }

    def _run(self, fn, amount, **kwargs):
        buy = self.buy
        signer_calls = []

        def forbidden(*args, **kw):
            signer_calls.append(args)
            raise AssertionError("signer must not be used when the price is over the ceiling")

        with mock.patch.object(buy, "_probe_endpoint", return_value=self._pricing(amount)), \
                mock.patch.object(buy, "_get_signer_address", return_value="0x" + "3" * 40), \
                mock.patch.object(buy, "_validate_contract_exists", return_value=True), \
                mock.patch.object(buy, "_get_usdc_balance", return_value=10**12), \
                mock.patch.object(buy, "_ensure_permit2_allowance", side_effect=forbidden), \
                mock.patch.object(buy, "_presign_auths", side_effect=forbidden):
            err = io.StringIO()
            with redirect_stdout(io.StringIO()), redirect_stderr(err):
                with self.assertRaises(SystemExit) as ctx:
                    fn("https://seller.example/services/x", **kwargs)
        return ctx.exception.code, err.getvalue(), signer_calls

    def test_pay_refuses_over_ceiling(self):
        code, err, calls = self._run(self.buy.cmd_pay, 2_000_000, max_price=Decimal("1"))
        self.assertEqual(code, 1)
        self.assertIn("exceeds the per-call ceiling", err)
        self.assertIn("--max-price 2", err)
        self.assertEqual(calls, [])

    def test_pay_agent_refuses_over_ceiling(self):
        code, err, calls = self._run(
            self.buy.cmd_pay_agent, 1_500_000, messages="hi", max_price=Decimal("1"),
        )
        self.assertEqual(code, 1)
        self.assertIn("1.5 USDC", err)
        self.assertEqual(calls, [])

    def test_pay_proceeds_to_signing_within_ceiling(self):
        # Within the ceiling the flow reaches the (mocked, forbidden) signer —
        # proving the ceiling, not some earlier check, stopped the cases above.
        buy = self.buy
        with mock.patch.object(buy, "_probe_endpoint", return_value=self._pricing(1_000)), \
                mock.patch.object(buy, "_get_signer_address", return_value="0x" + "3" * 40), \
                mock.patch.object(buy, "_validate_contract_exists", return_value=True), \
                mock.patch.object(buy, "_get_usdc_balance", return_value=10**12), \
                mock.patch.object(buy, "_ensure_permit2_allowance", return_value=None), \
                mock.patch.object(buy, "_presign_auths", return_value=[]) as presign:
            with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit):
                    buy.cmd_pay("https://seller.example/services/x", max_price=Decimal("1"))
        presign.assert_called_once()


if __name__ == "__main__":
    unittest.main()
