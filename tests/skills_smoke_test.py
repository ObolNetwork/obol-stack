#!/usr/bin/env python3
"""Smoke tests for OpenClaw skills (ethereum-networks, obol-stack, distributed-validators).

Run inside the OpenClaw pod:
    obol kubectl exec -i -n openclaw-default deploy/openclaw -c openclaw -- python3 - < tests/skills_smoke_test.py

Or from outside:
    obol kubectl exec -i -n openclaw-<id> deploy/openclaw -c openclaw -- python3 - < tests/skills_smoke_test.py
"""

import json
import os
import re
import subprocess
import sys
import urllib.request

SKILLS_DIR = "/data/.openclaw/skills"
RPC = os.path.join(SKILLS_DIR, "ethereum-networks", "scripts", "rpc.py")
KUBE = os.path.join(SKILLS_DIR, "obol-stack", "scripts", "kube.py")

passed = 0
failed = 0
errors = []


def test(name, fn):
    global passed, failed
    try:
        fn()
        passed += 1
        print(f"  \033[32mPASS\033[0m  {name}")
    except AssertionError as e:
        failed += 1
        errors.append((name, str(e)))
        print(f"  \033[31mFAIL\033[0m  {name}: {e}")
    except Exception as e:
        failed += 1
        errors.append((name, f"unexpected: {e}"))
        print(f"  \033[31mFAIL\033[0m  {name}: unexpected error: {e}")


def run(cmd, timeout=30):
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    return r.returncode, r.stdout.strip(), r.stderr.strip()


def http_get(url, timeout=15):
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return resp.status, json.loads(resp.read())


# ──────────────────────────────────────────────
# ethereum-networks tests
# ──────────────────────────────────────────────
print("\n\033[1m--- ethereum-networks ---\033[0m")


def test_blockchain_files():
    for f in [
        "SKILL.md",
        "scripts/rpc.py",
        "references/erc20-methods.md",
        "references/common-contracts.md",
    ]:
        path = os.path.join(SKILLS_DIR, "ethereum-networks", f)
        assert os.path.isfile(path), f"missing: {f}"


test("ethereum-networks/files_exist", test_blockchain_files)


def test_block_number():
    rc, out, err = run(["python3", RPC, "eth_blockNumber"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "Block:" in out, f"unexpected output: {out}"
    m = re.search(r"Block:\s+([\d,]+)", out)
    assert m, f"no block number found in: {out}"
    block = int(m.group(1).replace(",", ""))
    assert block > 20_000_000, f"block number too low: {block}"


test("ethereum-networks/block_number", test_block_number)


def test_chain_id():
    rc, out, err = run(["python3", RPC, "eth_chainId"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "Chain ID: 1" in out, f"unexpected chain id: {out}"
    assert "mainnet" in out, f"missing 'mainnet' in: {out}"


test("ethereum-networks/chain_id", test_chain_id)


def test_gas_price():
    rc, out, err = run(["python3", RPC, "eth_gasPrice"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "Gwei" in out, f"missing 'Gwei' in: {out}"
    m = re.search(r"([\d.]+)\s*Gwei", out)
    assert m, f"no gwei value in: {out}"
    gwei = float(m.group(1))
    assert gwei > 0, f"gas price is 0"


test("ethereum-networks/gas_price", test_gas_price)


def test_eth_balance():
    vitalik = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
    rc, out, err = run(["python3", RPC, "eth_getBalance", vitalik])
    assert rc == 0, f"exit {rc}: {err}"
    assert "ETH" in out, f"missing 'ETH' in: {out}"
    m = re.search(r"([\d.]+)\s*ETH", out)
    assert m, f"no ETH value in: {out}"
    eth = float(m.group(1))
    assert eth > 0, f"balance is 0"


test("ethereum-networks/eth_balance", test_eth_balance)


def test_erc20_total_supply():
    usdc = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
    rc, out, err = run(["python3", RPC, "eth_call", usdc, "0x18160ddd"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "Result:" in out or "0x" in out, f"unexpected output: {out}"


test("ethereum-networks/erc20_total_supply", test_erc20_total_supply)


def test_hoodi_chain_id():
    rc, out, err = run(["python3", RPC, "--network", "evm/560048", "eth_chainId"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "560048" in out, f"missing '560048' in: {out}"
    assert "hoodi" in out, f"missing 'hoodi' in: {out}"


test("ethereum-networks/hoodi_chain_id", test_hoodi_chain_id)


# ──────────────────────────────────────────────
# obol-stack tests
# ──────────────────────────────────────────────
print("\n\033[1m--- obol-stack ---\033[0m")


def test_k8s_files():
    for f in ["SKILL.md", "scripts/kube.py"]:
        path = os.path.join(SKILLS_DIR, "obol-stack", f)
        assert os.path.isfile(path), f"missing: {f}"


test("obol-stack/files_exist", test_k8s_files)

# We'll capture pod name from the pods test for use in logs test
_discovered_pod = [None]


def test_pods():
    rc, out, err = run(["python3", KUBE, "pods"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "openclaw" in out.lower(), f"no openclaw pod in: {out}"
    # extract first pod name containing "openclaw"
    for line in out.splitlines():
        if "openclaw" in line.lower() and not line.startswith(("-", "NAME", "=")):
            _discovered_pod[0] = line.split()[0]
            break


test("obol-stack/pods", test_pods)


def test_services():
    rc, out, err = run(["python3", KUBE, "services"])
    assert rc == 0, f"exit {rc}: {err}"
    assert len(out) > 0, "empty output"


test("obol-stack/services", test_services)


def test_deployments():
    rc, out, err = run(["python3", KUBE, "deployments"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "openclaw" in out.lower(), f"no openclaw deployment in: {out}"


test("obol-stack/deployments", test_deployments)


def test_events():
    rc, out, err = run(["python3", KUBE, "events"])
    assert rc == 0, f"exit {rc}: {err}"
    # events may legitimately be empty


test("obol-stack/events", test_events)


def test_configmaps():
    rc, out, err = run(["python3", KUBE, "configmaps"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "openclaw" in out.lower(), f"no openclaw configmap in: {out}"


test("obol-stack/configmaps", test_configmaps)


def test_logs():
    pod = _discovered_pod[0]
    assert pod, "no pod discovered from pods test"
    rc, out, err = run(["python3", KUBE, "logs", pod, "--tail", "10"])
    assert rc == 0, f"exit {rc}: {err}"
    assert len(out) > 0, "empty logs"


test("obol-stack/logs", test_logs)


def test_describe_deployment():
    rc, out, err = run(["python3", KUBE, "describe", "deployment", "openclaw"])
    assert rc == 0, f"exit {rc}: {err}"
    assert "replica" in out.lower() or "Replica" in out, f"no replicas info in: {out[:200]}"


test("obol-stack/describe_deployment", test_describe_deployment)


# ──────────────────────────────────────────────
# distributed-validators tests
# ──────────────────────────────────────────────
print("\n\033[1m--- distributed-validators ---\033[0m")


def test_dvt_files():
    for f in ["SKILL.md", "references/api-examples.md"]:
        path = os.path.join(SKILLS_DIR, "distributed-validators", f)
        assert os.path.isfile(path), f"missing: {f}"


test("distributed-validators/files_exist", test_dvt_files)


def curl_json(url):
    """Fetch JSON via curl (matches how the DVT skill documents API access)."""
    rc, out, err = run(["curl", "-sf", url])
    assert rc == 0, f"curl failed (exit {rc}): {err}"
    return json.loads(out)


def test_obol_api_health():
    # _health returns 503 when any sub-check is down, so use curl without -f
    rc, out, err = run(["curl", "-s", "https://api.obol.tech/v1/_health"])
    assert rc == 0, f"curl failed: {err}"
    data = json.loads(out)
    assert "status" in data, f"no status field in: {data}"
    # mainnet beacon should be up even if other networks aren't
    details = data.get("details", data.get("info", {}))
    mainnet = details.get("mainnet beacon node health", {})
    assert mainnet.get("status") == "up", f"mainnet beacon not up: {details}"


test("distributed-validators/api_health", test_obol_api_health)


def test_network_summary():
    data = curl_json("https://api.obol.tech/v1/lock/network/summary/mainnet")
    assert isinstance(data, dict), f"expected dict, got {type(data)}"
    clusters = data.get("total_clusters", data.get("totalClusters", 0))
    assert clusters > 0, f"total_clusters is 0 or missing: {data}"


test("distributed-validators/network_summary", test_network_summary)


# ──────────────────────────────────────────────
# Summary
# ──────────────────────────────────────────────
print(f"\n{'='*50}")
if errors:
    print("\nFailures:")
    for name, msg in errors:
        print(f"  - {name}: {msg}")
    print()

total = passed + failed
print(f"Results: \033[32m{passed} passed\033[0m, \033[31m{failed} failed\033[0m, {total} total")
sys.exit(1 if failed else 0)
