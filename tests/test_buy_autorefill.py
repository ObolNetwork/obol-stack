#!/usr/bin/env python3
import importlib.util
import io
import sys
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).resolve().parents[1] / "internal" / "embed" / "skills" / "buy-inference" / "scripts" / "buy.py"


def load_buy_module():
    spec = importlib.util.spec_from_file_location("buy_inference_buy", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def http_error(code):
    return urllib.error.HTTPError("http://cluster.local", code, "boom", None, io.BytesIO(b""))


def make_pricing(amount="1000", network="base-sepolia", asset="0xasset", pay_to="0xpayto"):
    return {
        "accepts": [{
            "amount": amount,
            "network": network,
            "asset": asset,
            "payTo": pay_to,
        }],
    }


def make_auths(prefix, count, signer="0xsigner"):
    return [{
        "signature": f"0x{prefix}{i}",
        "from": signer,
        "to": "0xpayto",
        "value": "1000",
        "validAfter": "0",
        "validBefore": "4294967295",
        "nonce": f"{prefix}-{i}",
    } for i in range(count)]


def make_purchase(name="solo", model="qwen3.5:9b", auths=None, auto_refill=None, generation=1):
    auths = auths if auths is not None else make_auths("old", 3)
    auto_refill = auto_refill if auto_refill is not None else {"enabled": True, "threshold": 1, "count": 2}
    return {
        "metadata": {"name": name, "namespace": "agent-ns", "generation": generation},
        "spec": {
            "model": model,
            "endpoint": "http://seller/v1/chat/completions",
            "count": len(auths),
            "payment": {
                "network": "base-sepolia",
                "payTo": "0xpayto",
                "price": "1000",
                "asset": "0xasset",
            },
            "preSignedAuths": auths,
            "autoRefill": auto_refill,
        },
    }


class BuyAutorefillHelpersTest(unittest.TestCase):
    def test_resolve_auto_refill_enables_when_policy_flags_present(self):
        mod = load_buy_module()
        policy = mod._resolve_auto_refill({"refill_count": "25"}, desired_count=100, existing_policy=None)
        self.assertTrue(policy["enabled"])
        self.assertEqual(policy["count"], 25)
        self.assertEqual(policy["threshold"], 20)

    def test_resolve_auto_refill_preserves_existing_without_overrides(self):
        mod = load_buy_module()
        existing = {"enabled": True, "threshold": 7, "count": 33}
        self.assertEqual(mod._resolve_auto_refill({}, desired_count=100, existing_policy=existing), existing)

    def test_plan_autorefill_refills_at_threshold(self):
        mod = load_buy_module()
        count, reason = mod._plan_autorefill(
            10,
            {"enabled": True, "threshold": 10, "count": 25},
        )
        self.assertEqual(count, 25)
        self.assertIn("at or below threshold 10", reason)

    def test_plan_autorefill_skips_when_above_threshold(self):
        mod = load_buy_module()
        count, reason = mod._plan_autorefill(
            11,
            {"enabled": True, "threshold": 10, "count": 25},
        )
        self.assertEqual(count, 0)
        self.assertIn("above threshold 10", reason)

    def test_compact_active_auths_drops_spent_prefix(self):
        mod = load_buy_module()
        auths = [{"nonce": "a"}, {"nonce": "b"}, {"nonce": "c"}]
        self.assertEqual(mod._compact_active_auths(auths, 2), [{"nonce": "c"}])

    def test_build_active_auth_pool_appends_new_auths(self):
        mod = load_buy_module()
        existing = [{"nonce": "a"}, {"nonce": "b"}, {"nonce": "c"}]
        live = {"remaining": 1, "spent": 2}
        new = [{"nonce": "d"}, {"nonce": "e"}]
        self.assertEqual(
            mod._build_active_auth_pool(existing, live, new),
            [{"nonce": "c"}, {"nonce": "d"}, {"nonce": "e"}],
        )

    def test_find_purchase_by_model_ignores_same_name_and_detects_conflict(self):
        mod = load_buy_module()
        purchases = [
            {"metadata": {"name": "alpha"}, "spec": {"model": "qwen3.5:9b"}},
            {"metadata": {"name": "beta"}, "spec": {"model": "qwen3.5:9b"}},
            {"metadata": {"name": "gamma"}, "spec": {"model": "qwen3.6:9b"}},
            {"metadata": {"name": "draining", "deletionTimestamp": "now"}, "spec": {"model": "qwen3.7:9b"}},
        ]
        self.assertEqual(mod._find_purchase_by_model(purchases, "qwen3.5:9b", exclude_name="alpha"), "beta")
        self.assertIsNone(mod._find_purchase_by_model(purchases, "qwen3.6:9b", exclude_name="gamma"))
        self.assertEqual(mod._find_purchase_by_model(purchases, "qwen3.7:9b"), "draining")

    def test_get_litellm_pod_skips_terminating_pods(self):
        mod = load_buy_module()
        pods = {
            "items": [
                {
                    "metadata": {"name": "litellm-old", "deletionTimestamp": "2026-04-23T00:00:00Z"},
                    "status": {"phase": "Running", "podIP": "10.0.0.1"},
                },
                {
                    "metadata": {"name": "litellm-new"},
                    "status": {"phase": "Running", "podIP": "10.0.0.2"},
                },
            ],
        }
        with mock.patch.object(mod, "api_get", return_value=pods):
            pod = mod._get_litellm_pod("token", object())
        self.assertEqual(pod["metadata"]["name"], "litellm-new")

    def test_purchase_ready_requires_current_generation_and_pool_sync(self):
        mod = load_buy_module()
        pr = {
            "metadata": {"generation": 2},
            "spec": {"preSignedAuths": [{"nonce": "a"}, {"nonce": "b"}]},
            "status": {
                "observedGeneration": 1,
                "remaining": 2,
                "publicModel": "paid/qwen3.5:9b",
                "conditions": [{"type": "Ready", "status": "True", "message": "stale"}],
            },
        }
        ready, _, _, reason = mod._purchase_ready(pr)
        self.assertFalse(ready)
        self.assertIn("observedGeneration", reason)

        pr["status"]["observedGeneration"] = 2
        pr["status"]["remaining"] = 1
        ready, _, _, reason = mod._purchase_ready(pr)
        self.assertFalse(ready)
        self.assertIn("auth pool", reason)

        pr["status"]["remaining"] = 2
        ready, remaining, public_model, _ = mod._purchase_ready(pr)
        self.assertTrue(ready)
        self.assertEqual(remaining, 2)
        self.assertEqual(public_model, "paid/qwen3.5:9b")


class BuyLifecycleCommandTest(unittest.TestCase):
    def test_cmd_buy_new_purchase_happy_path(self):
        mod = load_buy_module()
        new_auths = make_auths("new", 3)

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[]), \
             mock.patch.object(mod, "_get_purchase_request", side_effect=http_error(404)), \
             mock.patch.object(mod, "_presign_auths", return_value=new_auths) as presign, \
             mock.patch.object(mod, "_create_purchase_request") as create_purchase, \
             mock.patch.object(mod, "_wait_for_purchase_ready", return_value=True), \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]):
            mod.cmd_buy("solo", "http://seller/v1/chat/completions", "qwen3.5:9b", count="3", opts={})

        presign.assert_called_once_with("0xsigner", "0xpayto", "1000", "base-sepolia", "0xasset", 3)
        args = create_purchase.call_args.args
        self.assertEqual(args[0], "solo")
        self.assertEqual(args[1], "http://seller")
        self.assertEqual(args[2], "qwen3.5:9b")
        self.assertEqual(args[3], 3)
        self.assertEqual(args[8], new_auths)

    def test_cmd_buy_budget_based_count(self):
        mod = load_buy_module()
        new_auths = make_auths("new", 4)

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing(amount="1000")), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[]), \
             mock.patch.object(mod, "_get_purchase_request", side_effect=http_error(404)), \
             mock.patch.object(mod, "_presign_auths", return_value=new_auths) as presign, \
             mock.patch.object(mod, "_create_purchase_request"), \
             mock.patch.object(mod, "_wait_for_purchase_ready", return_value=True), \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]):
            mod.cmd_buy("solo", "http://seller", "qwen3.5:9b", budget="4000", opts={})

        presign.assert_called_once_with("0xsigner", "0xpayto", "1000", "base-sepolia", "0xasset", 4)

    def test_cmd_buy_rejects_duplicate_model_before_signing(self):
        mod = load_buy_module()
        purchases = [make_purchase(name="alpha")]

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=purchases), \
             mock.patch.object(mod, "_get_purchase_request", side_effect=http_error(404)), \
             mock.patch.object(mod, "_presign_auths") as presign, \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "beta"]), \
             self.assertRaises(SystemExit):
            mod.cmd_buy("beta", "http://seller", "qwen3.5:9b", count="2", opts={})

        presign.assert_not_called()

    def test_cmd_buy_same_name_requires_live_status_before_top_up(self):
        mod = load_buy_module()
        existing = make_purchase(name="solo")

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[existing]), \
             mock.patch.object(mod, "_get_purchase_request", return_value=existing), \
             mock.patch.object(mod, "_buyer_status", return_value={}), \
             mock.patch.object(mod, "_presign_auths") as presign, \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]), \
             self.assertRaises(SystemExit):
            mod.cmd_buy("solo", "http://seller", "qwen3.5:9b", count="2", opts={})

        presign.assert_not_called()

    def test_cmd_buy_same_name_rejects_draining_purchase(self):
        mod = load_buy_module()
        existing = make_purchase(name="solo")
        existing["metadata"]["deletionTimestamp"] = "2026-04-23T00:00:00Z"

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[existing]), \
             mock.patch.object(mod, "_get_purchase_request", return_value=existing), \
             mock.patch.object(mod, "_presign_auths") as presign, \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]), \
             self.assertRaises(SystemExit):
            mod.cmd_buy("solo", "http://seller", "qwen3.5:9b", count="2", opts={})

        presign.assert_not_called()

    def test_cmd_buy_same_name_rejects_pending_runtime_sync(self):
        mod = load_buy_module()
        existing = make_purchase(name="solo")
        existing["status"] = {
            "observedGeneration": 1,
            "conditions": [{
                "type": "Ready",
                "status": "False",
                "reason": "RuntimeSyncing",
                "message": "waiting for runtime auth pool to reach 3 active auths",
            }],
        }

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[existing]), \
             mock.patch.object(mod, "_get_purchase_request", return_value=existing), \
             mock.patch.object(mod, "_buyer_status", return_value={"solo": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_presign_auths") as presign, \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]), \
             self.assertRaises(SystemExit):
            mod.cmd_buy("solo", "http://seller", "qwen3.5:9b", count="2", opts={})

        presign.assert_not_called()

    def test_cmd_buy_same_name_appends_active_auths_and_preserves_auto_refill(self):
        mod = load_buy_module()
        existing = make_purchase(name="solo")
        new_auths = make_auths("new", 2)
        create_result = {}

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[existing]), \
             mock.patch.object(mod, "_get_purchase_request", return_value=existing), \
             mock.patch.object(mod, "_buyer_status", return_value={"solo": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_presign_auths", return_value=new_auths), \
             mock.patch.object(mod, "_create_purchase_request", return_value=create_result) as create_purchase, \
             mock.patch.object(mod, "_wait_for_purchase_ready", return_value=True), \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]):
            mod.cmd_buy("solo", "http://seller/v1/chat/completions", "qwen3.5:9b", count="2", opts={})

        args = create_purchase.call_args.args
        kwargs = create_purchase.call_args.kwargs
        self.assertEqual(args[0], "solo")
        self.assertEqual(args[1], "http://seller")
        self.assertEqual(args[2], "qwen3.5:9b")
        self.assertEqual(args[3], 3)
        self.assertEqual(
            args[8],
            [existing["spec"]["preSignedAuths"][2], new_auths[0], new_auths[1]],
        )
        self.assertEqual(kwargs["auto_refill"], existing["spec"]["autoRefill"])

    def test_cmd_buy_same_name_overrides_auto_refill_policy(self):
        mod = load_buy_module()
        existing = make_purchase(name="solo")
        new_auths = make_auths("new", 2)

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing()), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[existing]), \
             mock.patch.object(mod, "_get_purchase_request", return_value=existing), \
             mock.patch.object(mod, "_buyer_status", return_value={"solo": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_presign_auths", return_value=new_auths), \
             mock.patch.object(mod, "_create_purchase_request") as create_purchase, \
             mock.patch.object(mod, "_wait_for_purchase_ready", return_value=True), \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]):
            mod.cmd_buy(
                "solo",
                "http://seller/v1/chat/completions",
                "qwen3.5:9b",
                count="2",
                opts={"auto_refill": "true", "refill_threshold": "3", "refill_count": "7"},
            )

        self.assertEqual(
            create_purchase.call_args.kwargs["auto_refill"],
            {"enabled": True, "threshold": 3, "count": 7},
        )

    def test_cmd_buy_requires_force_when_balance_is_insufficient(self):
        mod = load_buy_module()

        with mock.patch.object(mod, "_probe_endpoint", return_value=make_pricing(amount="1000")), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_get_usdc_balance", return_value="500"), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[]), \
             mock.patch.object(mod, "_get_purchase_request", side_effect=http_error(404)), \
             mock.patch.object(mod, "_presign_auths") as presign, \
             mock.patch.object(sys, "argv", ["buy.py", "buy", "solo"]), \
             self.assertRaises(SystemExit):
            mod.cmd_buy("solo", "http://seller", "qwen3.5:9b", count="2", opts={})

        presign.assert_not_called()

    def test_cmd_process_all_only_reconciles_live_purchases(self):
        mod = load_buy_module()
        purchases = [make_purchase(name="alpha"), make_purchase(name="beta")]

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_buyer_status", return_value={"alpha": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=purchases), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_reconcile_purchase_autorefill", return_value=True) as reconcile:
            mod.cmd_process(process_all=True)

        reconcile.assert_called_once()
        self.assertEqual(reconcile.call_args.args[0]["metadata"]["name"], "alpha")

    def test_cmd_process_all_mixes_success_skip_and_error(self):
        mod = load_buy_module()
        alpha = make_purchase(name="alpha")
        beta = make_purchase(name="beta")
        gamma = make_purchase(name="gamma")

        def reconcile(pr, *_args):
            name = pr["metadata"]["name"]
            if name == "alpha":
                return True
            if name == "beta":
                return False
            raise RuntimeError("boom")

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_buyer_status", return_value={
                 "alpha": {"remaining": 1, "spent": 2},
                 "gamma": {"remaining": 1, "spent": 2},
             }), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[alpha, beta, gamma]), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_reconcile_purchase_autorefill", side_effect=reconcile), \
             self.assertRaises(SystemExit):
            mod.cmd_process(process_all=True)

    def test_cmd_process_all_exits_when_sidecar_status_is_unavailable(self):
        mod = load_buy_module()

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_buyer_status", return_value=None), \
             self.assertRaises(SystemExit):
            mod.cmd_process(process_all=True)

    def test_cmd_process_named_purchase_exits_when_reconcile_errors(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_buyer_status", return_value={"solo": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_get_purchase_request", return_value=purchase), \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_reconcile_purchase_autorefill", side_effect=RuntimeError("boom")), \
             self.assertRaises(SystemExit):
            mod.cmd_process(name="solo")

    def test_cmd_process_named_purchase_fetches_only_target(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_buyer_status", return_value={"solo": {"remaining": 1, "spent": 2}}), \
             mock.patch.object(mod, "_get_purchase_request", return_value=purchase) as get_purchase, \
             mock.patch.object(mod, "_get_signer_address", return_value="0xsigner"), \
             mock.patch.object(mod, "_reconcile_purchase_autorefill", return_value=False):
            mod.cmd_process(name="solo")

        get_purchase.assert_called_once()

    def test_reconcile_purchase_autorefill_refills_at_threshold(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")
        live = {"remaining": 1, "spent": 2}
        new_auths = make_auths("new", 2)
        updated = make_purchase(name="solo")
        updated["spec"] = dict(purchase["spec"])

        with mock.patch.object(mod, "_get_usdc_balance", return_value=str(10_000)), \
             mock.patch.object(mod, "_presign_auths", return_value=new_auths), \
             mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_get_purchase_request", return_value=updated), \
             mock.patch.object(mod, "_kube_json") as kube_json:
            changed = mod._reconcile_purchase_autorefill(purchase, live, "0xsigner")

        self.assertTrue(changed)
        body = kube_json.call_args.args[4]
        self.assertEqual(body["spec"]["count"], 3)
        self.assertEqual(
            body["spec"]["preSignedAuths"],
            [purchase["spec"]["preSignedAuths"][2], new_auths[0], new_auths[1]],
        )

    def test_reconcile_purchase_autorefill_noop_above_threshold(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")

        with mock.patch.object(mod, "_presign_auths") as presign:
            changed = mod._reconcile_purchase_autorefill(
                purchase,
                {"remaining": 5, "spent": 0},
                "0xsigner",
            )

        self.assertFalse(changed)
        presign.assert_not_called()

    def test_reconcile_purchase_autorefill_skips_on_signer_mismatch(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")

        with mock.patch.object(mod, "_presign_auths") as presign:
            changed = mod._reconcile_purchase_autorefill(
                purchase,
                {"remaining": 1, "spent": 2},
                "0xother",
            )

        self.assertFalse(changed)
        presign.assert_not_called()

    def test_reconcile_purchase_autorefill_skips_on_insufficient_balance(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")

        with mock.patch.object(mod, "_get_usdc_balance", return_value="0"), \
             mock.patch.object(mod, "_presign_auths") as presign:
            changed = mod._reconcile_purchase_autorefill(
                purchase,
                {"remaining": 1, "spent": 2},
                "0xsigner",
            )

        self.assertFalse(changed)
        presign.assert_not_called()

    def test_cmd_list_filters_sidecar_ghosts_against_purchase_requests(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")
        out = io.StringIO()

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[purchase]), \
             mock.patch.object(mod, "_buyer_status", return_value={
                 "solo": {"remaining": 2, "network": "base-sepolia", "public_model": "paid/qwen3.5:9b"},
                 "ghost": {"remaining": 99, "network": "base-sepolia", "public_model": "paid/ghost"},
             }), \
             mock.patch.object(sys, "stdout", out):
            mod.cmd_list()

        rendered = out.getvalue()
        self.assertIn("solo", rendered)
        self.assertNotIn("ghost", rendered)

    def test_cmd_list_uses_live_alias_and_remaining(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")
        out = io.StringIO()

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_list_purchase_requests", return_value=[purchase]), \
             mock.patch.object(mod, "_buyer_status", return_value={
                 "solo": {"remaining": 7, "network": "base-sepolia", "public_model": "paid/custom-model"},
             }), \
             mock.patch.object(sys, "stdout", out):
            mod.cmd_list()

        rendered = out.getvalue()
        self.assertIn("paid/custom-model", rendered)
        self.assertIn("7", rendered)

    def test_cmd_status_requires_purchase_request_even_if_sidecar_has_ghost(self):
        mod = load_buy_module()

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_get_purchase_request", side_effect=http_error(404)), \
             mock.patch.object(mod, "_buyer_status", return_value={"ghost": {"remaining": 1, "spent": 0}}), \
             self.assertRaises(SystemExit):
            mod.cmd_status("ghost")

    def test_cmd_status_reflects_live_remaining_and_spent(self):
        mod = load_buy_module()
        purchase = make_purchase(name="solo")
        out = io.StringIO()

        with mock.patch.object(mod, "load_sa", return_value=("token", None)), \
             mock.patch.object(mod, "make_ssl_context", return_value=object()), \
             mock.patch.object(mod, "_get_purchase_request", return_value=purchase), \
             mock.patch.object(mod, "_buyer_status", return_value={
                 "solo": {
                     "remaining": 4,
                     "spent": 9,
                     "public_model": "paid/qwen3.5:9b",
                     "url": "http://seller",
                     "remote_model": "qwen3.5:9b",
                     "network": "base-sepolia",
                 },
             }), \
             mock.patch.object(mod, "_get_litellm_pod", return_value={"status": {"phase": "Running"}}), \
             mock.patch.object(sys, "stdout", out):
            mod.cmd_status("solo")

        rendered = out.getvalue()
        self.assertIn("Auths remaining: 4", rendered)
        self.assertIn("Auths spent:     9", rendered)


if __name__ == "__main__":
    unittest.main()
