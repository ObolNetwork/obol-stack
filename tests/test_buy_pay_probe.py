#!/usr/bin/env python3
"""Unit tests for buy.py's one-shot `pay` / `go` request shaping.

- `--data` implies POST unless `--method` is given explicitly.
- The 402 probe carries the caller's body (and Content-Type: application/json):
  a seller's body policy validates the body before answering 402, so a
  bodyless probe of a POST route is rejected with 400.
- Identical payment options in a 402 accepts[] are shown once.
"""
import importlib.util
import io
import json
import sys
import unittest
import urllib.error
from contextlib import redirect_stderr, redirect_stdout
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
PAY_TO = "0x2222222222222222222222222222222222222222"


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_x402_pay_probe", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def _option(**overrides):
    acc = {
        "scheme": "exact",
        "network": "base-sepolia",
        "asset": USDC_BASE_SEPOLIA,
        "amount": "1000",
        "payTo": PAY_TO,
        "extra": {"name": "USDC", "version": "2"},
    }
    acc.update(overrides)
    return acc


def _http_402(url, accepts):
    body = json.dumps({"x402Version": 2, "accepts": accepts}).encode()
    return urllib.error.HTTPError(url, 402, "Payment Required", {}, io.BytesIO(body))


class _FakeResponse:
    status = 200
    headers = {}

    def __init__(self, body=b"ok"):
        self._body = body

    def read(self):
        return self._body

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class ResolveMethodTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_data_implies_post(self):
        self.assertEqual(self.buy._resolve_pay_method(None, '{"a":1}'), "POST")

    def test_no_data_is_get(self):
        self.assertEqual(self.buy._resolve_pay_method(None, None), "GET")

    def test_explicit_method_wins(self):
        self.assertEqual(self.buy._resolve_pay_method("put", '{"a":1}'), "PUT")
        self.assertEqual(self.buy._resolve_pay_method("GET", '{"a":1}'), "GET")

    def test_inference_is_post(self):
        self.assertEqual(self.buy._resolve_pay_method(None, None, kind="inference"), "POST")

    def test_bare_flag_is_ignored(self):
        # parse_flags turns a value-less `--method` into True.
        self.assertEqual(self.buy._resolve_pay_method(True, None), "GET")


class ProbeBodyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_http_probe_sends_body_and_content_type(self):
        seen = []

        def fake_urlopen(req, timeout=None):
            seen.append(req)
            raise _http_402(req.full_url, [_option()])

        with mock.patch.object(self.buy.urllib.request, "urlopen", side_effect=fake_urlopen):
            pricing = self.buy._probe_endpoint(
                "https://seller.example.com/services/x/", kind="http", method="POST",
                data='{"messages":[]}',
            )
        self.assertIsNotNone(pricing)
        self.assertEqual(len(seen), 1)
        req = seen[0]
        self.assertEqual(req.get_method(), "POST")
        self.assertEqual(req.data, b'{"messages":[]}')
        self.assertEqual(req.get_header("Content-type"), "application/json")

    def test_http_probe_without_data_has_no_body(self):
        seen = []

        def fake_urlopen(req, timeout=None):
            seen.append(req)
            raise _http_402(req.full_url, [_option()])

        with mock.patch.object(self.buy.urllib.request, "urlopen", side_effect=fake_urlopen):
            self.buy._probe_endpoint("https://seller.example.com/services/x", kind="http")
        self.assertEqual(seen[0].get_method(), "GET")
        self.assertIsNone(seen[0].data)
        self.assertIsNone(seen[0].get_header("Content-type"))

    def test_probe_dedupes_identical_options(self):
        accepts = [_option(), _option(), _option(amount="2000")]

        def fake_urlopen(req, timeout=None):
            raise _http_402(req.full_url, accepts)

        with mock.patch.object(self.buy.urllib.request, "urlopen", side_effect=fake_urlopen):
            pricing = self.buy._probe_endpoint("https://seller.example.com/services/x", kind="http")
        self.assertEqual([a["amount"] for a in pricing["accepts"]], ["1000", "2000"])


class DedupeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_case_insensitive_addresses(self):
        a = _option()
        b = _option(asset=USDC_BASE_SEPOLIA.lower(), payTo=PAY_TO.upper().replace("0X", "0x"))
        self.assertEqual(len(self.buy._dedupe_accepts([a, b])), 1)

    def test_caip2_and_legacy_alias_collapse_to_first(self):
        caip2 = _option(network="eip155:84532")
        alias = _option(network="base-sepolia")
        out = self.buy._dedupe_accepts([caip2, alias])
        self.assertEqual(out, [caip2])

    def test_distinct_options_kept(self):
        opts = [
            _option(),
            _option(network="base"),
            _option(payTo="0x3333333333333333333333333333333333333333"),
            _option(scheme="upto"),
        ]
        self.assertEqual(len(self.buy._dedupe_accepts(opts)), 4)

    def test_legacy_max_amount_required(self):
        a = {k: v for k, v in _option().items() if k != "amount"}
        a["maxAmountRequired"] = "1000"
        self.assertEqual(len(self.buy._dedupe_accepts([_option(), a])), 1)


class CmdPayTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def _run_pay(self, **kwargs):
        buy = self.buy
        probe_reqs = []
        paid_reqs = []

        def fake_urlopen(req, timeout=None):
            probe_reqs.append(req)
            raise _http_402(req.full_url, [_option(), _option()])

        def fake_paid_open(req, timeout=None):
            paid_reqs.append(req)
            return _FakeResponse()

        with mock.patch.object(buy.urllib.request, "urlopen", side_effect=fake_urlopen), \
                mock.patch.object(buy._PAID_OPENER, "open", side_effect=fake_paid_open), \
                mock.patch.object(buy, "_get_signer_address", return_value="0x" + "1" * 40), \
                mock.patch.object(buy, "_validate_contract_exists", return_value=True), \
                mock.patch.object(buy, "_get_usdc_balance", return_value="1000000"), \
                mock.patch.object(buy, "_ensure_permit2_allowance", return_value=None), \
                mock.patch.object(buy, "_presign_auths", return_value=[{"payment": {"x": 1}}]):
            out = io.StringIO()
            with redirect_stdout(out), redirect_stderr(io.StringIO()):
                rc = buy.cmd_pay("https://seller.example.com/services/llm", **kwargs)
        return rc, probe_reqs, paid_reqs, out.getvalue()

    def test_data_implies_post_for_probe_and_paid_request(self):
        body = '{"messages":[{"role":"user","content":"hi"}]}'
        rc, probes, paid, out = self._run_pay(data=body)
        self.assertEqual(rc, 0)
        self.assertEqual(probes[0].get_method(), "POST")
        self.assertEqual(probes[0].data, body.encode())
        self.assertEqual(probes[0].get_header("Content-type"), "application/json")
        self.assertEqual(paid[0].get_method(), "POST")
        self.assertEqual(paid[0].data, body.encode())
        # Duplicate options collapse to one, so no option list is printed.
        self.assertNotIn("[2]", out)

    def test_explicit_post_probe_carries_body(self):
        body = '{"input":"x"}'
        _, probes, paid, _ = self._run_pay(method="POST", data=body)
        self.assertEqual(probes[0].get_method(), "POST")
        self.assertEqual(probes[0].data, body.encode())
        self.assertEqual(paid[0].get_method(), "POST")

    def test_no_data_stays_get(self):
        _, probes, paid, _ = self._run_pay()
        self.assertEqual(probes[0].get_method(), "GET")
        self.assertIsNone(probes[0].data)
        self.assertEqual(paid[0].get_method(), "GET")


class CmdGoTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.buy = load_buy_module()

    def test_go_http_offer_with_data_probes_post_with_body(self):
        buy = self.buy
        probes = []

        def fake_urlopen(req, timeout=None):
            probes.append(req)
            raise _http_402(req.full_url, [_option()])

        body = '{"q":"x"}'
        with mock.patch.object(buy.urllib.request, "urlopen", side_effect=fake_urlopen), \
                mock.patch.object(buy, "cmd_pay") as pay:
            with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
                buy.cmd_go("https://seller.example.com/services/api", {"data": body, "max_price": "1"})
        self.assertEqual(probes[0].get_method(), "POST")
        self.assertEqual(probes[0].data, body.encode())
        self.assertEqual(pay.call_args.kwargs["method"], "POST")
        self.assertEqual(pay.call_args.kwargs["data"], body)


if __name__ == "__main__":
    unittest.main()
