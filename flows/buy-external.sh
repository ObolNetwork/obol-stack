#!/bin/bash
# flows/buy-external.sh
#
# Single-stack Bob buyer against an arbitrary external x402 seller. Skips the
# Alice provisioning that flows 11/13/14 do — Bob is the only stack we bring
# up. Useful for QA against third-party x402 sellers (e.g.
# https://inference.v1337.org/services/aeon).
#
# What it does:
#   1.  Source flows/lib.sh (helpers + .env auto-load) and lib-dual-stack.sh
#       (only for run_with_timeout / preseed_bob_wallet helpers via bob()).
#   2.  Derive Bob's deterministic buyer wallet from REMOTE_SIGNER_PRIVATE_KEY
#       (Hardhat-style: keccak(abi.encode(signer_key, 2))) — same algorithm
#       flow-15-live-obol-faucet-alice-bob.sh uses.
#   3.  Probe EXTERNAL_ENDPOINT without X-PAYMENT, parse 402 response, assert
#       accepts[0] matches EXTERNAL_TOKEN/CHAIN/PRICE/PAYTO. Fail loudly with
#       expected vs got.
#   4.  Confirm Bob's on-chain ERC-20 balance >= EXTERNAL_PRICE. Abort with the
#       cast send command operator should run if not. We never auto-fund here
#       because Alice is not provisioned.
#   5.  Bring up a Bob-only k3d stack under .workspace-bob-external/ with stack
#       ID "post490-buy-external-bob" (pinned by writing .stack-id before
#       `obol stack init`), preseed Bob's deterministic key into the
#       remote-signer, route LiteLLM at OBOL_LLM_ENDPOINT/MODEL.
#   6.  Invoke buy.py inside the agent pod via `obol kubectl exec` (no agent
#       chat round-trip — the seller has no agentId in
#       /.well-known/agent-registration.json so the LLM-driven discovery flow
#       cannot find it).
#   7.  Wait for PurchaseRequest Ready=True (5 min), port-forward LiteLLM,
#       send ONE paid request via paid/$EXTERNAL_MODEL, capture the body and
#       any X-PAYMENT-RESPONSE header.
#   8.  Scan the most recent N blocks of the EXTERNAL_TOKEN contract for
#       Transfer(Bob -> EXTERNAL_PAYTO, EXTERNAL_PRICE), capture tx hash.
#   9.  Capture x402-buyer /status before+after, balance before+after,
#       PurchaseRequest YAML, and write everything under
#       $EXTERNAL_BUY_ARTIFACT_DIR.
#
# Required env (or CLI flags overriding env):
#   EXTERNAL_ENDPOINT     full URL e.g. https://inference.v1337.org/services/aeon
#   EXTERNAL_MODEL        seller's actual model id (probe will tell you if you
#                         pick wrong)
#   EXTERNAL_TOKEN        ERC-20 token address e.g.
#                         0x0a09371a8b011d5110656ceBCc70603e53FD2c78 (OBOL Base
#                         Sepolia)
#   EXTERNAL_CHAIN        chain alias (default base-sepolia)
#   EXTERNAL_PAYTO        seller wallet
#   EXTERNAL_PRICE        wei amount per request
#   EXTERNAL_FACILITATOR  facilitator URL (default https://x402.gcp.obol.tech)
#   REMOTE_SIGNER_PRIVATE_KEY  populated in .env (Alice key — Bob is derived
#                              from it deterministically)
#   BASE_SEPOLIA_RPC      paid Base Sepolia RPC for cast balance/log scans.
#                         Always piped through scrub_secrets so the log never
#                         leaks the API key.
#
# Optional env:
#   EXTERNAL_BUY_ARTIFACT_DIR   default: $OBOL_ROOT/.tmp/buy-external-<ts>
#   EXTERNAL_LITELLM_PORT       default: pick_free_port
#   EXTERNAL_PR_NAME            default: v1337-aeon
#   EXTERNAL_PURCHASE_COUNT     default: 1
#   EXTERNAL_PR_TIMEOUT_S       default: 300 (5 min)
#   EXTERNAL_LOG_BLOCKS_BACK    default: 30 (~6 min on Base Sepolia at 2s/blk)
#   OBOL_LLM_ENDPOINT           default: http://127.0.0.1:8000/v1
#   OBOL_LLM_MODEL              default: qwen36-fast
#   OBOL_LLM_NAME               default: external-llm
#
# Exit code: 0 on PASS (every step pass), 1 on any FAIL.

set -euo pipefail

# ─────────────────────────────────────────────────────────────────
# CLI FLAG OVERRIDES
# Mostly: env vars work; flags are a convenience for ad-hoc invocations.
# ─────────────────────────────────────────────────────────────────
while [ "$#" -gt 0 ]; do
    case "$1" in
        --endpoint)     EXTERNAL_ENDPOINT="$2"; shift 2 ;;
        --model)        EXTERNAL_MODEL="$2"; shift 2 ;;
        --token)        EXTERNAL_TOKEN="$2"; shift 2 ;;
        --chain)        EXTERNAL_CHAIN="$2"; shift 2 ;;
        --pay-to)       EXTERNAL_PAYTO="$2"; shift 2 ;;
        --price)        EXTERNAL_PRICE="$2"; shift 2 ;;
        --facilitator)  EXTERNAL_FACILITATOR="$2"; shift 2 ;;
        --name)         EXTERNAL_PR_NAME="$2"; shift 2 ;;
        --count)        EXTERNAL_PURCHASE_COUNT="$2"; shift 2 ;;
        --artifact-dir) EXTERNAL_BUY_ARTIFACT_DIR="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,70p' "$0"
            exit 0
            ;;
        *)
            echo "unknown arg: $1" >&2
            exit 2
            ;;
    esac
done

# ─────────────────────────────────────────────────────────────────
# DEFAULTS
# Don't overwrite anything that's already exported.
# ─────────────────────────────────────────────────────────────────
EXTERNAL_CHAIN="${EXTERNAL_CHAIN:-base-sepolia}"
EXTERNAL_FACILITATOR="${EXTERNAL_FACILITATOR:-https://x402.gcp.obol.tech}"
EXTERNAL_PR_NAME="${EXTERNAL_PR_NAME:-v1337-aeon}"
EXTERNAL_PURCHASE_COUNT="${EXTERNAL_PURCHASE_COUNT:-1}"
EXTERNAL_PR_TIMEOUT_S="${EXTERNAL_PR_TIMEOUT_S:-300}"
EXTERNAL_LOG_BLOCKS_BACK="${EXTERNAL_LOG_BLOCKS_BACK:-30}"

OBOL_LLM_ENDPOINT="${OBOL_LLM_ENDPOINT:-http://127.0.0.1:8000/v1}"
OBOL_LLM_MODEL="${OBOL_LLM_MODEL:-qwen36-fast}"
OBOL_LLM_NAME="${OBOL_LLM_NAME:-external-llm}"

# Resolve OBOL_ROOT before sourcing helpers — lib.sh re-derives it but
# operating on the canonical path simplifies later relative paths.
OBOL_ROOT="${OBOL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
export OBOL_ROOT

# shellcheck source=flows/lib.sh
source "$OBOL_ROOT/flows/lib.sh"
# shellcheck source=flows/lib-dual-stack.sh
DUAL_STACK_FLOW_PREFIX="EXTERNAL_BUY"
source "$OBOL_ROOT/flows/lib-dual-stack.sh"

# Per-flow workspace lives next to .workspace-bob so tools that already know
# about that pattern continue to work, but separated so we don't collide with
# flow-14/15 reruns.
BOB_DIR="$OBOL_ROOT/.workspace-bob-external"
# k3d cluster names are capped at 32 chars total, INCLUDING the "obol-stack-"
# prefix (11 chars). So the user-supplied stack-id portion must be <= 21 chars,
# or `obol stack up` fails the k3d config validation:
#   "Cluster name must be <= 32 characters". Keep the default short.
PINNED_STACK_ID="${EXTERNAL_STACK_ID:-buy-ext-bob}"

EXTERNAL_BUY_ARTIFACT_DIR="${EXTERNAL_BUY_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/buy-external-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$EXTERNAL_BUY_ARTIFACT_DIR"
echo "Artifact dir: $EXTERNAL_BUY_ARTIFACT_DIR"

# Receipt helpers in lib.sh expect FLOW11_ARTIFACT_DIR + USDC_ADDRESS_BASE_SEPOLIA
# even when the asset is OBOL — find_usdc_transfer is generic ERC-20.
export FLOW11_ARTIFACT_DIR="$EXTERNAL_BUY_ARTIFACT_DIR"

# ─────────────────────────────────────────────────────────────────
# PRE-FLIGHT
# ─────────────────────────────────────────────────────────────────
require_tool curl
require_tool python3
require_tool docker
require_tool kubectl
require_tool jq
require_tool cast

for v in EXTERNAL_ENDPOINT EXTERNAL_MODEL EXTERNAL_TOKEN EXTERNAL_PAYTO EXTERNAL_PRICE; do
    if [ -z "${!v:-}" ]; then
        fail "Required env var $v is empty (pass via env or --flag). See header for full list."
        emit_metrics
        exit 1
    fi
done

if [ -z "${BASE_SEPOLIA_RPC:-}" ]; then
    fail "BASE_SEPOLIA_RPC not set — required for balance + log scans."
    emit_metrics
    exit 1
fi
BASE_SEPOLIA_RPC_LOG="$(redact_url_for_log "$BASE_SEPOLIA_RPC")"

# Same env-vs-.env precedence as flow-14/15: prefer process env, fall back to
# whatever lib.sh sourced from .env.
SIGNER_KEY="${REMOTE_SIGNER_PRIVATE_KEY:-}"
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY missing in environment / .env"
    emit_metrics
    exit 1
fi

# ─────────────────────────────────────────────────────────────────
# CLEANUP TRAP
# Same shape as flow-14: only tear down on non-zero exit so a passing run
# leaves the cluster + artifacts in place for inspection.
# ─────────────────────────────────────────────────────────────────
PF_LITELLM=""
PF_LITELLM_LOG=""

# Best-effort diagnostic snapshot, taken on the failure path BEFORE the cluster
# is torn down. Each command is wrapped in `|| true` so a single failure does
# not abort the rest of the bundle. The sidecar status snapshot uses the same
# `kubectl exec ... python3 -c` shape as the in-flow before/after captures
# (the buyer container is distroless — no curl/wget).
external_snapshot_on_fail() {
    type bob >/dev/null 2>&1 || return 0
    [ -d "$EXTERNAL_BUY_ARTIFACT_DIR" ] || return 0

    local f

    f="$EXTERNAL_BUY_ARTIFACT_DIR/controller.log"
    if bob kubectl logs -n x402 deploy/serviceoffer-controller --tail=2000 --previous \
        > "$f" 2>/dev/null; then
        echo "  snapshot: $f"
    else
        if bob kubectl logs -n x402 deploy/serviceoffer-controller --tail=2000 \
            > "$f" 2>/dev/null; then
            echo "  snapshot: $f (no --previous available)"
        else
            rm -f "$f" 2>/dev/null || true
        fi
    fi

    f="$EXTERNAL_BUY_ARTIFACT_DIR/controller-current.log"
    if bob kubectl logs -n x402 deploy/serviceoffer-controller --tail=2000 \
        > "$f" 2>/dev/null; then
        echo "  snapshot: $f"
    else
        rm -f "$f" 2>/dev/null || true
    fi

    f="$EXTERNAL_BUY_ARTIFACT_DIR/purchaserequest.yaml"
    if bob kubectl get purchaserequest -A -o yaml > "$f" 2>/dev/null; then
        echo "  snapshot: $f"
    else
        rm -f "$f" 2>/dev/null || true
    fi

    f="$EXTERNAL_BUY_ARTIFACT_DIR/buyer-status-after.json"
    if bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, json
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    print(json.dumps(json.loads(resp.read()), indent=2))
except Exception as e:
    print(json.dumps({'error': repr(e)}))
" > "$f" 2>/dev/null; then
        echo "  snapshot: $f"
    else
        rm -f "$f" 2>/dev/null || true
    fi

    # Re-use the harness-captured buy.py log if it was written; do not re-fetch.
    if [ -f "$EXTERNAL_BUY_ARTIFACT_DIR/buy-py.log" ]; then
        f="$EXTERNAL_BUY_ARTIFACT_DIR/agent-pod-buypy.log"
        if cp "$EXTERNAL_BUY_ARTIFACT_DIR/buy-py.log" "$f" 2>/dev/null; then
            echo "  snapshot: $f"
        fi
    fi

    f="$EXTERNAL_BUY_ARTIFACT_DIR/cluster-pods.txt"
    if bob kubectl get pods -A -o wide > "$f" 2>/dev/null; then
        echo "  snapshot: $f"
    else
        rm -f "$f" 2>/dev/null || true
    fi

    f="$EXTERNAL_BUY_ARTIFACT_DIR/cluster-events.txt"
    if bob kubectl get events -A --sort-by='.lastTimestamp' 2>/dev/null \
        | tail -100 > "$f" 2>/dev/null && [ -s "$f" ]; then
        echo "  snapshot: $f"
    else
        rm -f "$f" 2>/dev/null || true
    fi
}

external_cleanup() {
    local ec=$?
    set +e
    [ -n "$PF_LITELLM" ] && cleanup_pid "$PF_LITELLM" 2>/dev/null
    [ -n "$PF_LITELLM_LOG" ] && rm -f "$PF_LITELLM_LOG" 2>/dev/null

    # Cleanup gate: tear down only when every step passed. On FAIL, snapshot
    # diagnostics and preserve the cluster — the only places that record why
    # a PurchaseRequest never advanced are the controller logs, PR
    # status.conditions[], and sidecar /status, all of which die with the
    # cluster. Operator pays one manual `bob stack down` when done diagnosing.
    if type bob >/dev/null 2>&1; then
        if [ "$ec" -eq 0 ]; then
            bob stack down >/dev/null 2>&1 || true
        else
            echo "Capturing failure snapshot to $EXTERNAL_BUY_ARTIFACT_DIR"
            external_snapshot_on_fail
            echo ""
            echo "FAIL → cluster preserved for diagnosis."
            echo "  Stack id:  $PINNED_STACK_ID"
            echo "  Artifacts: $EXTERNAL_BUY_ARTIFACT_DIR"
            echo "  Manual cleanup when done:"
            echo "    bob stack down"
        fi
    fi
    cleanup_k3d_obol_networks
    set -e
    return $ec
}
trap external_cleanup EXIT

# Reclaim leaked Docker networks at start so the new cluster can allocate.
cleanup_k3d_obol_networks

# ─────────────────────────────────────────────────────────────────
# STEP 1: Derive Bob's deterministic buyer wallet
# ─────────────────────────────────────────────────────────────────
step "Derive Bob deterministic buyer wallet from REMOTE_SIGNER_PRIVATE_KEY"
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null) || {
    fail "cast wallet address failed for REMOTE_SIGNER_PRIVATE_KEY"
    emit_metrics; exit 1
}
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak \
    "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)") || {
    fail "Could not derive Bob private key"
    emit_metrics; exit 1
}
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null)
export BOB_WALLET BOB_PRIVATE_KEY ALICE_WALLET
pass "Alice (parent) $ALICE_WALLET → Bob (derived buyer) $BOB_WALLET"

# ─────────────────────────────────────────────────────────────────
# STEP 2: Probe EXTERNAL_ENDPOINT, expect 402, cross-check accepts[0]
# ─────────────────────────────────────────────────────────────────
step "Probe $EXTERNAL_ENDPOINT — expect HTTP 402 + matching accepts[0]"
PROBE_FILE="$EXTERNAL_BUY_ARTIFACT_DIR/probe-402.json"
PROBE_HEADERS_FILE="$EXTERNAL_BUY_ARTIFACT_DIR/probe-402-headers.txt"

# Inference-shaped probe: POST with empty messages so the seller's gate fires
# before any model validation. Some sellers 405 a GET, so we POST.
http_code=$(curl -sS -o "$PROBE_FILE" -D "$PROBE_HEADERS_FILE" \
    -w '%{http_code}' --max-time 30 \
    -X POST "$EXTERNAL_ENDPOINT/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$EXTERNAL_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":1}" \
    2>&1 | scrub_secrets || true)

if [ "$http_code" != "402" ]; then
    fail "Expected HTTP 402, got '$http_code'. Body:"
    head -c 800 "$PROBE_FILE" 2>/dev/null | scrub_secrets
    echo ""
    emit_metrics; exit 1
fi

PROBE_DIFF=$(EXT_TOKEN="$EXTERNAL_TOKEN" EXT_CHAIN="$EXTERNAL_CHAIN" \
    EXT_PRICE="$EXTERNAL_PRICE" EXT_PAYTO="$EXTERNAL_PAYTO" \
    PROBE_FILE="$PROBE_FILE" \
    python3 <<'PY'
import json, os, sys

# Sellers may report network in CAIP-2 form ("eip155:84532") while operators
# naturally use the legacy alias ("base-sepolia"). Normalize both sides to the
# CAIP-2 form before comparing so the test isn't sensitive to wire format.
CAIP2 = {
    "mainnet":      "eip155:1",
    "base":         "eip155:8453",
    "base-sepolia": "eip155:84532",
    "sepolia":      "eip155:11155111",
    "hoodi":        "eip155:560048",
    "polygon":      "eip155:137",
    "optimism":     "eip155:10",
    "arbitrum":     "eip155:42161",
    "avalanche":    "eip155:43114",
}
def caip2(n: str) -> str:
    n = (n or "").strip()
    return CAIP2.get(n.lower(), n)

with open(os.environ["PROBE_FILE"]) as f:
    body = json.load(f)
accepts = body.get("accepts") or []
if not accepts:
    print("accepts[] missing or empty in 402 body", file=sys.stderr)
    sys.exit(1)
acc = accepts[0]
got = {
    "asset":   (acc.get("asset")   or "").lower(),
    "network": caip2(acc.get("network") or ""),
    "price":   str(acc.get("amount", acc.get("maxAmountRequired", ""))),
    "payTo":   (acc.get("payTo")   or "").lower(),
}
want = {
    "asset":   os.environ["EXT_TOKEN"].lower(),
    "network": caip2(os.environ["EXT_CHAIN"]),
    "price":   os.environ["EXT_PRICE"],
    "payTo":   os.environ["EXT_PAYTO"].lower(),
}
diffs = [(k, want[k], got[k]) for k in want if want[k] != got[k]]
if diffs:
    for k, w, g in diffs:
        print(f"MISMATCH {k}: expected={w} got={g}")
    sys.exit(1)
print("OK")
PY
)
PROBE_RC=$?
if [ "$PROBE_RC" -ne 0 ]; then
    fail "402 accepts[0] does not match expected:"
    echo "$PROBE_DIFF" | scrub_secrets
    emit_metrics; exit 1
fi
pass "402 accepts[0] matches asset/chain/price/payTo for $EXTERNAL_TOKEN @ $EXTERNAL_PRICE wei"

# ─────────────────────────────────────────────────────────────────
# STEP 3: Confirm Bob's on-chain balance >= EXTERNAL_PRICE
# Abort (no auto-fund) — Alice is not provisioned in this flow.
# ─────────────────────────────────────────────────────────────────
step "Bob ERC-20 balance >= $EXTERNAL_PRICE on $EXTERNAL_TOKEN ($EXTERNAL_CHAIN)"
BOB_BAL_BEFORE=$(env -u CHAIN cast call "$EXTERNAL_TOKEN" \
    "balanceOf(address)(uint256)" "$BOB_WALLET" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null \
    | grep -oE '^[0-9]+' | head -1 || true)
BOB_BAL_BEFORE="${BOB_BAL_BEFORE:-0}"
echo "$BOB_BAL_BEFORE" > "$EXTERNAL_BUY_ARTIFACT_DIR/bob-balance-before.txt"

if ! python3 -c "import sys; sys.exit(0 if int('$BOB_BAL_BEFORE') >= int('$EXTERNAL_PRICE') else 1)"; then
    deficit=$(python3 -c "print(int('$EXTERNAL_PRICE') - int('$BOB_BAL_BEFORE'))")
    fail "Bob balance $BOB_BAL_BEFORE wei < required $EXTERNAL_PRICE wei. Deficit: $deficit wei."
    echo "  Operator funding command (run from a wallet that holds $EXTERNAL_TOKEN):"
    echo "    cast send $EXTERNAL_TOKEN \"transfer(address,uint256)\" $BOB_WALLET $deficit \\"
    echo "      --rpc-url \"\$BASE_SEPOLIA_RPC\" --private-key \"\$FUNDER_PRIVATE_KEY\""
    emit_metrics; exit 1
fi
pass "Bob has $BOB_BAL_BEFORE wei (>= required $EXTERNAL_PRICE)"

# ─────────────────────────────────────────────────────────────────
# STEP 4: Bring up single Bob k3d stack with pinned ID, preseed buyer wallet
# ─────────────────────────────────────────────────────────────────
step "Bootstrap Bob workspace at $BOB_DIR"
if [ ! -x "$OBOL_ROOT/.build/obol" ]; then
    if ! (cd "$OBOL_ROOT" && go build -o .build/obol ./cmd/obol >/dev/null 2>&1); then
        fail "Could not build $OBOL_ROOT/.build/obol — run 'just build' or 'go build -o .build/obol ./cmd/obol' first"
        emit_metrics; exit 1
    fi
fi
bootstrap_flow_workspace "$BOB_DIR" "$OBOL_ROOT/.build/obol"
pass "Bob workspace ready"

# Pin the stack ID before stack init — stack.Init reads .stack-id when present
# and uses petname.Generate otherwise. This gives us a deterministic
# k3d-obol-stack-<id> container name for repeat runs.
mkdir -p "$BOB_DIR/config"
printf '%s\n' "$PINNED_STACK_ID" > "$BOB_DIR/config/.stack-id"

# Bob's host port assignments — pick free so we don't collide with any
# already-running stack (release-smoke, flow-14 etc.).
BOB_HTTP_PORT="$(pick_free_port)"
BOB_HTTP_ALT_PORT="$(pick_free_port)"
BOB_HTTPS_PORT="$(pick_free_port)"
BOB_HTTPS_ALT_PORT="$(pick_free_port)"
export BOB_HTTP_PORT BOB_HTTP_ALT_PORT BOB_HTTPS_PORT BOB_HTTPS_ALT_PORT

stack_init_and_up_with_retry "Bob" bob "$BOB_DIR" preseed_bob_wallet

# Make sure the runtime detection runs against the live cluster (sets
# BOB_AGENT_NS / DEPLOY / CONTAINER / OBOL_SKILLS_DIR for buy.py exec).
detect_buyer_runtime bob

# ─────────────────────────────────────────────────────────────────
# STEP 5: Repoint LiteLLM at OBOL_LLM_ENDPOINT and add the live RPC route
# ─────────────────────────────────────────────────────────────────
step "Bob: route LiteLLM via $OBOL_LLM_NAME ($OBOL_LLM_MODEL)"
if route_llm_via_obol_cli bob; then
    pass "LiteLLM routed via $OBOL_LLM_ENDPOINT"
else
    fail "route_llm_via_obol_cli failed for $OBOL_LLM_ENDPOINT"
    emit_metrics; exit 1
fi

step "Bob: add $EXTERNAL_CHAIN route in eRPC (live RPC, writes allowed)"
bob network add "$EXTERNAL_CHAIN" --endpoint "$BASE_SEPOLIA_RPC" --allow-writes \
    >/dev/null 2>&1 || true
bob kubectl rollout restart deployment/erpc -n erpc >/dev/null 2>&1 || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s >/dev/null 2>&1 || true
pass "Bob eRPC: $EXTERNAL_CHAIN routed to $BASE_SEPOLIA_RPC_LOG"

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers
poll_step_grep "Bob: ${BOB_AGENT_RUNTIME} agent ready" "true" 36 5 \
    bob kubectl get pods -n "$BOB_AGENT_NS" -l "$BOB_AGENT_LABEL" \
        -o "jsonpath={range .items[*].status.containerStatuses[?(@.name=='${BOB_AGENT_CONTAINER}')]}{.ready}{'\n'}{end}"

# ─────────────────────────────────────────────────────────────────
# STEP 6: Capture buyer sidecar status BEFORE the buy
# ─────────────────────────────────────────────────────────────────
step "Capture buyer sidecar /status before buy"
bob kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "
import urllib.request, json, sys
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    print(json.dumps(json.loads(resp.read()), indent=2))
except Exception as e:
    print(json.dumps({'error': repr(e)}))
" > "$EXTERNAL_BUY_ARTIFACT_DIR/buyer-status-before.json" 2>&1 || true
pass "Sidecar status snapshot saved"

# ─────────────────────────────────────────────────────────────────
# STEP 7: Run buy.py inside the agent pod (no agent-chat detour)
# ─────────────────────────────────────────────────────────────────
step "Bob's agent: buy $EXTERNAL_PURCHASE_COUNT auth(s) from external seller"
buy_log="$EXTERNAL_BUY_ARTIFACT_DIR/buy-py.log"
set +e
bob kubectl exec -n "$BOB_AGENT_NS" "deploy/$BOB_AGENT_DEPLOY" -c "$BOB_AGENT_CONTAINER" -- \
    env "ERPC_URL=http://erpc.erpc.svc.cluster.local/rpc" \
        "ERPC_NETWORK=$EXTERNAL_CHAIN" \
    python3 "$BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py" buy "$EXTERNAL_PR_NAME" \
        --endpoint "$EXTERNAL_ENDPOINT" \
        --model "$EXTERNAL_MODEL" \
        --count "$EXTERNAL_PURCHASE_COUNT" \
    > "$buy_log" 2>&1
buy_rc=$?
set -e
scrub_secrets < "$buy_log" | tail -n 30
if [ "$buy_rc" -ne 0 ]; then
    fail "buy.py exited $buy_rc — see $buy_log"
    emit_metrics; exit 1
fi
pass "buy.py completed (PurchaseRequest declared)"

# Snapshot the PurchaseRequest CR for the artifact bundle.
bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" "$EXTERNAL_PR_NAME" -o yaml \
    > "$EXTERNAL_BUY_ARTIFACT_DIR/purchaserequest.yaml" 2>/dev/null || true

# ─────────────────────────────────────────────────────────────────
# STEP 8: Wait for PurchaseRequest Ready=True (5 min)
# ─────────────────────────────────────────────────────────────────
step "Wait for PurchaseRequest $EXTERNAL_PR_NAME to reach Ready=True (timeout ${EXTERNAL_PR_TIMEOUT_S}s)"
pr_ready=0
elapsed=0
interval=5
while [ "$elapsed" -lt "$EXTERNAL_PR_TIMEOUT_S" ]; do
    cond=$(bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" "$EXTERNAL_PR_NAME" \
        -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{"|"}{.message}{end}' \
        2>/dev/null || true)
    status="${cond%%|*}"
    if [ "$status" = "True" ]; then
        pr_ready=1
        break
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
done
# Refresh the snapshot now that the controller has reconciled.
bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" "$EXTERNAL_PR_NAME" -o yaml \
    > "$EXTERNAL_BUY_ARTIFACT_DIR/purchaserequest.yaml" 2>/dev/null || true
if [ "$pr_ready" = "1" ]; then
    pass "PurchaseRequest Ready=True after ${elapsed}s"
else
    fail "PurchaseRequest never reached Ready=True after ${EXTERNAL_PR_TIMEOUT_S}s — see purchaserequest.yaml"
    emit_metrics; exit 1
fi

# Wait for LiteLLM rollout that the controller may have triggered.
bob kubectl rollout status deployment/litellm -n llm --timeout=180s >/dev/null 2>&1 || true

# Resolve the actual paid model name from the buyer sidecar — flows-14
# precedent: prefer the live sidecar's published model over our guess so we
# don't accidentally call paid/<wrong-id>.
sidecar_status_raw=$(bob kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "
import urllib.request
print(urllib.request.urlopen('http://localhost:8402/status', timeout=5).read().decode())
" 2>/dev/null || true)
PAID_MODEL=$(printf '%s' "$sidecar_status_raw" | python3 -c "
import json, sys
try:
    d = json.loads(sys.stdin.read())
    for v in d.values():
        m = v.get('public_model')
        if m:
            print(m); break
except Exception:
    pass
")
[ -z "$PAID_MODEL" ] && PAID_MODEL="paid/$EXTERNAL_MODEL"
echo "  Paid model alias: $PAID_MODEL"

# ─────────────────────────────────────────────────────────────────
# STEP 9: Port-forward LiteLLM, send ONE paid request
# ─────────────────────────────────────────────────────────────────
step "Port-forward LiteLLM on host"
LITELLM_PORT="${EXTERNAL_LITELLM_PORT:-$(pick_free_port)}"
PF_LITELLM_LOG=$(mktemp)
bob kubectl port-forward svc/litellm "${LITELLM_PORT}:4000" -n llm \
    >"$PF_LITELLM_LOG" 2>&1 &
PF_LITELLM=$!
pf_ready=0
for _ in $(seq 1 20); do
    if python3 - "$LITELLM_PORT" <<'PY'
import socket, sys
s = socket.socket(); s.settimeout(1)
try: s.connect(("127.0.0.1", int(sys.argv[1])))
except OSError: sys.exit(1)
finally: s.close()
PY
    then pf_ready=1; break; fi
    if ! kill -0 "$PF_LITELLM" 2>/dev/null; then break; fi
    sleep 1
done
if [ "$pf_ready" != "1" ]; then
    fail "LiteLLM port-forward did not become ready: $(tail -n 10 "$PF_LITELLM_LOG" 2>/dev/null | tr '\n' ' ' | scrub_secrets)"
    emit_metrics; exit 1
fi
pass "LiteLLM reachable at http://127.0.0.1:$LITELLM_PORT"

step "Capture LITELLM_MASTER_KEY"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$BOB_MASTER_KEY" ]; then
    fail "Could not read LITELLM_MASTER_KEY"
    emit_metrics; exit 1
fi
pass "LITELLM master key captured"

step "Issue ONE paid request via $PAID_MODEL"
BUY_START_BLOCK=$(base_sepolia_block_number "$BASE_SEPOLIA_RPC" || true)
PAID_RESP_BODY="$EXTERNAL_BUY_ARTIFACT_DIR/paid-response.json"
PAID_RESP_HEADERS="$EXTERNAL_BUY_ARTIFACT_DIR/paid-response-headers.txt"
paid_code=$(curl -sS -o "$PAID_RESP_BODY" -D "$PAID_RESP_HEADERS" \
    -w '%{http_code}' --max-time 180 \
    -X POST "http://127.0.0.1:${LITELLM_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_MASTER_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$PAID_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: external x402 buy smoke passed.\"}],\"max_tokens\":60,\"temperature\":0,\"stream\":false}" \
    2>&1 | scrub_secrets || true)
if [ "$paid_code" != "200" ]; then
    fail "Paid request returned HTTP $paid_code (body: $(head -c 400 "$PAID_RESP_BODY" 2>/dev/null | scrub_secrets))"
    emit_metrics; exit 1
fi
pass "Paid request returned HTTP 200"

# Surface the X-PAYMENT-RESPONSE header (settlement metadata) from the
# captured response headers — the buyer sidecar passes it through when the
# seller's settlement-aware path provides it.
xpr=$(grep -i '^x-payment-response' "$PAID_RESP_HEADERS" 2>/dev/null | head -1 \
    | tr -d '\r' | scrub_secrets || true)
if [ -n "$xpr" ]; then
    echo "  $xpr" | head -c 400
    echo ""
fi

# ─────────────────────────────────────────────────────────────────
# STEP 10: Settlement scan — find Transfer(Bob -> EXTERNAL_PAYTO, EXTERNAL_PRICE)
# ─────────────────────────────────────────────────────────────────
step "On-chain: Transfer($BOB_WALLET -> $EXTERNAL_PAYTO, $EXTERNAL_PRICE) scan"
SETTLE_FROM_BLOCK="$BUY_START_BLOCK"
if [ -n "$SETTLE_FROM_BLOCK" ]; then
    SETTLE_FROM_BLOCK=$((SETTLE_FROM_BLOCK - EXTERNAL_LOG_BLOCKS_BACK))
    [ "$SETTLE_FROM_BLOCK" -lt 0 ] && SETTLE_FROM_BLOCK=0
else
    # base_sepolia_block_number couldn't read — fall back to last 30 blocks
    # by asking cast directly.
    head_block=$(env -u CHAIN cast block-number --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null | tr -d ' ' || true)
    SETTLE_FROM_BLOCK=$((head_block - EXTERNAL_LOG_BLOCKS_BACK))
fi

# Reuse find_usdc_transfer (generic ERC-20 Transfer scanner) by pointing
# USDC_ADDRESS_BASE_SEPOLIA at the external token for this single call.
export USDC_ADDRESS_BASE_SEPOLIA="$EXTERNAL_TOKEN"
SETTLE_TX=""
SETTLE_AMOUNT=""
for _ in $(seq 1 60); do
    settle_match=$(find_usdc_transfer "$BOB_WALLET" "$EXTERNAL_PAYTO" "$EXTERNAL_PRICE" "$SETTLE_FROM_BLOCK" 2>/dev/null || true)
    SETTLE_TX=$(echo "$settle_match" | awk '{print $1; exit}')
    SETTLE_AMOUNT=$(echo "$settle_match" | awk '{print $2; exit}')
    if [ -n "$SETTLE_TX" ]; then
        break
    fi
    sleep 4
done
if [ -n "$SETTLE_TX" ] && [ "$SETTLE_AMOUNT" = "$EXTERNAL_PRICE" ]; then
    echo "  tx=$SETTLE_TX amount=$SETTLE_AMOUNT (from block $SETTLE_FROM_BLOCK)"
    archive_receipt "settlement" "$SETTLE_TX" 12 2 || true
    cp "$EXTERNAL_BUY_ARTIFACT_DIR/settlement-receipt.json" \
       "$EXTERNAL_BUY_ARTIFACT_DIR/settlement-tx.json" 2>/dev/null || true
    if [ ! -f "$EXTERNAL_BUY_ARTIFACT_DIR/settlement-tx.json" ]; then
        printf '{"tx":"%s","amount":"%s","fromBlock":%s}\n' \
            "$SETTLE_TX" "$SETTLE_AMOUNT" "$SETTLE_FROM_BLOCK" \
            > "$EXTERNAL_BUY_ARTIFACT_DIR/settlement-tx.json"
    fi
    pass "Settlement Transfer found and archived"
else
    fail "No Bob -> $EXTERNAL_PAYTO Transfer for $EXTERNAL_PRICE wei after block $SETTLE_FROM_BLOCK"
fi

# ─────────────────────────────────────────────────────────────────
# STEP 11: Final state — sidecar /status after, Bob balance after
# ─────────────────────────────────────────────────────────────────
step "Capture buyer sidecar /status after paid call"
bob kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "
import urllib.request, json
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    print(json.dumps(json.loads(resp.read()), indent=2))
except Exception as e:
    print(json.dumps({'error': repr(e)}))
" > "$EXTERNAL_BUY_ARTIFACT_DIR/buyer-status-after.json" 2>&1 || true
pass "Sidecar status (after) snapshot saved"

step "Capture Bob balance after"
BOB_BAL_AFTER=$(env -u CHAIN cast call "$EXTERNAL_TOKEN" \
    "balanceOf(address)(uint256)" "$BOB_WALLET" \
    --rpc-url "$BASE_SEPOLIA_RPC" 2>/dev/null \
    | grep -oE '^[0-9]+' | head -1 || true)
BOB_BAL_AFTER="${BOB_BAL_AFTER:-unknown}"
echo "$BOB_BAL_AFTER" > "$EXTERNAL_BUY_ARTIFACT_DIR/bob-balance-after.txt"
echo "  Bob: $BOB_BAL_BEFORE → $BOB_BAL_AFTER"
pass "Bob balance recorded"

# ─────────────────────────────────────────────────────────────────
# SUMMARY
# ─────────────────────────────────────────────────────────────────
emit_metrics
echo "Artifacts: $EXTERNAL_BUY_ARTIFACT_DIR"
ls -1 "$EXTERNAL_BUY_ARTIFACT_DIR" 2>/dev/null | sed 's/^/  /'

if [ "${FAIL_COUNT:-0}" -eq 0 ]; then
    echo ""
    echo "RESULT: PASS — Bob $BOB_WALLET bought $EXTERNAL_PURCHASE_COUNT auth(s) from $EXTERNAL_ENDPOINT, paid request HTTP 200, settlement tx ${SETTLE_TX:-pending}"
    exit 0
else
    echo ""
    echo "RESULT: FAIL — $FAIL_COUNT step(s) failed of $STEP_COUNT total. See $EXTERNAL_BUY_ARTIFACT_DIR for evidence."
    exit 1
fi
