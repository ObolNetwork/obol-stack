#!/bin/bash
# Flow 13: Dual-Stack OBOL — Alice sells, Bob discovers and buys with OBOL.
#
# Mirrors flow-11's "Alice sells, Bob discovers via ERC-8004 and buys" structure
# end-to-end, but the payment asset is a fork-local OBOL ERC20Permit token instead
# of USDC, and the chain + facilitator are local rather than public:
#
#   - One Anvil fork of Base Sepolia (chain 84532) shared by Alice's and Bob's
#     obol stacks via the Docker-managed alias `host.k3d.internal:$ANVIL_PORT`.
#   - One x402-rs facilitator container pointing at that Anvil.
#   - A fork-local OBOL ERC20Permit contract (contracts/fork-obol/src/ForkObolToken.sol)
#     deployed via `forge create` against the same Anvil. The same address is
#     visible from both clusters because they share the fork.
#   - Alice's ServiceOffer carries OBOL asset metadata (transferMethod=permit2,
#     eip712Name="Obol Network", eip712Version="1"); buy.py on Bob's agent is
#     OBOL-Permit2-aware and signs Permit2 payloads against the local facilitator.
#
# Requires:
#   - .env with REMOTE_SIGNER_PRIVATE_KEY (used as Alice's seller key + Bob seed)
#   - cast + anvil (Foundry) on PATH
#   - forge on PATH (used to compile ForkObolToken.sol)
#   - Docker running with the configured Alice/Bob ingress ports + Anvil port free
#   - OpenAI-compatible QA LLM endpoint via OBOL_LLM_ENDPOINT
#   - Docker access to ghcr.io/x402-rs/x402-facilitator:1.4.7
#
# Use this flow when you want to validate the OBOL Permit2 path end-to-end
# without depending on the public Obol facilitator or any USDC contract.
#
# Usage:
#   ./flows/flow-13-dual-stack-obol.sh
#
# Override defaults via shell env or repo-root .env:
#   FLOW13_ANVIL_PORT             host port for Anvil (default: auto-pick)
#   FLOW13_FACILITATOR_PORT       host port for x402-rs (default: auto-pick)
#   FLOW13_ALICE_HTTP_PORT, _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW13_BOB_HTTP_PORT,   _ALT, _HTTPS_PORT, _HTTPS_ALT_PORT
#   FLOW13_ARTIFACT_DIR           where receipts + logs land
#   OBOL_LLM_ENDPOINT             required vLLM/llama.cpp/OpenAI-compatible endpoint
#   OBOL_LLM_MODEL                endpoint model name (default: qwen36-fast)
#
source "$(dirname "$0")/lib.sh"
DUAL_STACK_FLOW_PREFIX="FLOW13"
DUAL_STACK_SERVICE_NAME="alice-obol-inference"
DUAL_STACK_TUNNEL_DNS_WARN_ONLY=true
source "$(dirname "$0")/lib-dual-stack.sh"

# ═════════════════════════════════════════════════════════════════
# CONSTANTS / WORKSPACES
# ═════════════════════════════════════════════════════════════════

ALICE_DIR="$OBOL_ROOT/.workspace-alice"
BOB_DIR="$OBOL_ROOT/.workspace-bob"

ALICE_HTTP_PORT="$(dual_stack_env_or_free_port ALICE_HTTP_PORT)"
ALICE_HTTP_ALT_PORT="$(dual_stack_env_or_free_port ALICE_HTTP_ALT_PORT)"
ALICE_HTTPS_PORT="$(dual_stack_env_or_free_port ALICE_HTTPS_PORT)"
ALICE_HTTPS_ALT_PORT="$(dual_stack_env_or_free_port ALICE_HTTPS_ALT_PORT)"

BOB_HTTP_PORT="$(dual_stack_env_or_free_port BOB_HTTP_PORT)"
BOB_HTTP_ALT_PORT="$(dual_stack_env_or_free_port BOB_HTTP_ALT_PORT)"
BOB_HTTPS_PORT="$(dual_stack_env_or_free_port BOB_HTTPS_PORT)"
BOB_HTTPS_ALT_PORT="$(dual_stack_env_or_free_port BOB_HTTPS_ALT_PORT)"

OBOL_LLM_MODEL="${OBOL_LLM_MODEL:-qwen36-fast}"
export OBOL_LLM_MODEL

ANVIL_PORT="${FLOW13_ANVIL_PORT:-$(pick_free_port)}"
FACILITATOR_PORT="${FLOW13_FACILITATOR_PORT:-$(pick_free_port)}"

# Both clusters speak to host processes through a Docker-managed host alias.
# Plain Docker containers use the host alias that matches the current OS.
# From the host shell we use 127.0.0.1.
CLUSTER_HOST="${FLOW13_CLUSTER_HOST:-host.k3d.internal}"
ANVIL_RPC_HOST="http://127.0.0.1:$ANVIL_PORT"
ANVIL_RPC_CLUSTER="http://$CLUSTER_HOST:$ANVIL_PORT"
ANVIL_RPC_FACILITATOR="$(host_service_url_for_plain_container "$ANVIL_PORT")"
FACILITATOR_URL_HOST="http://127.0.0.1:$FACILITATOR_PORT"
FACILITATOR_URL_CLUSTER="http://$CLUSTER_HOST:$FACILITATOR_PORT"
BASE_SEPOLIA_FORK_RPC="${FLOW13_BASE_SEPOLIA_RPC:-${BASE_SEPOLIA_RPC:-}}"

ERC8004_IDENTITY_REGISTRY_BASE_SEPOLIA="0x8004A818BFB912233c491871b3d84c89A494BD9e"

# OBOL Permit2 wire amount: 0.001 OBOL with 18 decimals = 1e15 wei.
OBOL_PRICE_WEI="1000000000000000"

FLOW13_ARTIFACT_DIR="${FLOW13_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-13-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$FLOW13_ARTIFACT_DIR"
ANVIL_LOG="$FLOW13_ARTIFACT_DIR/anvil.log"
FACILITATOR_LOG="$FLOW13_ARTIFACT_DIR/facilitator.log"

if [ -z "${OBOL_LLM_ENDPOINT:-}" ]; then
    fail "Flow 13 requires OBOL_LLM_ENDPOINT for a QA vLLM/llama.cpp/OpenAI-compatible endpoint; local qwen3.5:9b via Ollama is not accepted for full QA"
    emit_metrics
    exit 1
fi

# Receipt helpers in lib.sh expect FLOW11_ARTIFACT_DIR + USDC_ADDRESS_BASE_SEPOLIA +
# BASE_SEPOLIA_RPC. We point them at the OBOL token + Anvil; their ERC-20 transfer
# scan logic is generic, despite the legacy "USDC" naming.
export FLOW11_ARTIFACT_DIR="$FLOW13_ARTIFACT_DIR"
export BASE_SEPOLIA_RPC="$ANVIL_RPC_HOST"

# Initial Hermes defaults; detect_buyer_runtime overwrites these once Bob's
# cluster is up and we know whether OpenClaw or Hermes was deployed.
BOB_AGENT_NS="hermes-obol-agent"
BOB_AGENT_DEPLOY="hermes"
BOB_AGENT_CONTAINER="hermes"
BOB_AGENT_SERVICE="hermes"
BOB_AGENT_REMOTE_PORT="8642"
BOB_OBOL_SKILLS_DIR="/data/.hermes/obol-skills"
BOB_AGENT_LABEL="app.kubernetes.io/name=hermes"
BOB_AGENT_RUNTIME="hermes"

ANVIL_PID=""
FACILITATOR_CONTAINER=""
PF_AGENT=""
PF_AGENT_LOG=""

# ═════════════════════════════════════════════════════════════════
# CLEANUP TRAP
# ═════════════════════════════════════════════════════════════════

flow13_cleanup() {
    local ec=$?
    set +e
    [ -n "$PF_AGENT" ] && cleanup_pid "$PF_AGENT" 2>/dev/null
    [ -n "$PF_AGENT_LOG" ] && rm -f "$PF_AGENT_LOG" 2>/dev/null
    # Drop the base-sepolia eRPC pin we added on each cluster so the next flow
    # (especially flow-11 / flow-14 against live RPC) doesn't inherit a route to
    # a dead Anvil fork. Safe if the cluster is already gone; the obol CLI just
    # fails and we ignore the exit code.
    if [ -d "$ALICE_DIR/config" ]; then
        alice network remove base-sepolia >/dev/null 2>&1 || true
    fi
    if [ -d "$BOB_DIR/config" ]; then
        bob network remove base-sepolia >/dev/null 2>&1 || true
    fi
    if [ "$ec" -ne 0 ]; then
        if type alice >/dev/null 2>&1; then
            alice stack down >/dev/null 2>&1 || true
        fi
        if type bob >/dev/null 2>&1; then
            bob stack down >/dev/null 2>&1 || true
        fi
    fi
    if [ -n "$FACILITATOR_CONTAINER" ]; then
        write_x402_facilitator_logs "$FACILITATOR_CONTAINER" "$FACILITATOR_LOG"
        docker rm -f "$FACILITATOR_CONTAINER" >/dev/null 2>&1 || true
    fi
    if [ -n "$ANVIL_PID" ] && kill -0 "$ANVIL_PID" 2>/dev/null; then
        kill "$ANVIL_PID" 2>/dev/null || true
        wait "$ANVIL_PID" 2>/dev/null || true
    fi
    # Reclaim Docker networks left behind by k3d clusters that crashed mid-
    # create or were force-removed without `obol stack down`. Targeted to
    # `k3d-obol-stack-*` and naturally skips networks with active endpoints.
    cleanup_k3d_obol_networks
    set -e
    return $ec
}
trap flow13_cleanup EXIT
# Proactive: reclaim leaked Docker networks at start so the new cluster can
# allocate even if a prior aborted run left orphans behind.
cleanup_k3d_obol_networks

# ═════════════════════════════════════════════════════════════════
# RUNNERS / HELPERS
# ═════════════════════════════════════════════════════════════════

cluster_json_rpc_probe() {
    local runner="$1"
    local url="$2"

    "$runner" kubectl exec -i -n llm deployment/litellm -c litellm -- \
        python3 - "$url" <<'PY'
import sys
import urllib.request

url = sys.argv[1]
payload = b'{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
with urllib.request.urlopen(req, timeout=8) as resp:
    sys.stdout.write(resp.read().decode())
PY
}

detect_cluster_host_for_anvil() {
    local runner="$1"
    local host out
    LAST_CLUSTER_PROBE_OUT=""

    for host in ${FLOW13_CLUSTER_HOST:-} host.k3d.internal host.docker.internal; do
        [ -n "$host" ] || continue
        out=$(cluster_json_rpc_probe "$runner" "http://$host:$ANVIL_PORT" 2>&1 || true)
        if echo "$out" | grep -q '"result":"0x14a34"'; then
            printf '%s\n' "$host"
            return 0
        fi
        LAST_CLUSTER_PROBE_OUT="host=$host output=${out:0:300}"
    done

    return 1
}

wait_cluster_anvil() {
    local runner="$1"
    local out
    LAST_CLUSTER_PROBE_OUT=""

    for _ in $(seq 1 12); do
        out=$(cluster_json_rpc_probe "$runner" "$ANVIL_RPC_CLUSTER" 2>&1 || true)
        if echo "$out" | grep -q '"result":"0x14a34"'; then
            return 0
        fi
        LAST_CLUSTER_PROBE_OUT="${out:0:300}"
        sleep 5
    done

    return 1
}

# ═════════════════════════════════════════════════════════════════
# 1-5. PREFLIGHT
# ═════════════════════════════════════════════════════════════════

step "Preflight: Foundry tools (cast + anvil + forge) installed"
missing=""
for t in cast anvil forge; do
    command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
if [ -n "$missing" ]; then
    fail "Missing Foundry tools:$missing — run: curl -L https://foundry.paradigm.xyz | bash && foundryup"
    emit_metrics; exit 1
fi
pass "Foundry tools available"

step "Preflight: x402-rs facilitator image available"
FACILITATOR_IMAGE=$(x402_facilitator_image || true)
if [ -z "$FACILITATOR_IMAGE" ]; then
    skip "flow-13 requires Docker access to ghcr.io/x402-rs/x402-facilitator:1.4.7"
    emit_metrics
    exit 0
fi
pass "Facilitator image available: $FACILITATOR_IMAGE"

step "Preflight: .env signer key (Alice/Bob seed)"
SIGNER_KEY=$({ grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' "$OBOL_ROOT/.env" 2>/dev/null || true; } | head -1 | cut -d= -f2-)
if [ -z "$SIGNER_KEY" ]; then
    SIGNER_KEY="${REMOTE_SIGNER_PRIVATE_KEY:-}"
fi
if [ -z "$SIGNER_KEY" ]; then
    fail "REMOTE_SIGNER_PRIVATE_KEY not found in .env or environment"
    emit_metrics; exit 1
fi
ALICE_WALLET=$(env -u CHAIN cast wallet address --private-key "$SIGNER_KEY" 2>/dev/null)
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY" 2>/dev/null)
pass "Alice (seller payTo + funded EOA): $ALICE_WALLET, Bob (derived buyer): $BOB_WALLET"

step "Preflight: host ports free (Alice/Bob ingress + Anvil + facilitator)"
busy=$(require_ports_free \
    "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT" \
    "$BOB_HTTP_PORT"   "$BOB_HTTP_ALT_PORT"   "$BOB_HTTPS_PORT"   "$BOB_HTTPS_ALT_PORT" \
    "$ANVIL_PORT" "$FACILITATOR_PORT") || true
if [ -n "$busy" ]; then
    fail "Ports in use (LISTEN): $busy — unset matching FLOW13_*_PORT to auto-pick"
    emit_metrics; exit 1
fi
pass "Ports: alice=$ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT bob=$BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT anvil=$ANVIL_PORT facilitator=$FACILITATOR_PORT"

step "Preflight: clean stale ethereum namespaces in default workspace"
if [ -f "$OBOL_CONFIG_DIR/.stack-id" ] && [ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ] && "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    assert_obol_kubeconfig
    for ns in $("$OBOL" kubectl get ns --no-headers 2>/dev/null | awk '{print $1}' | grep "^ethereum-" || true); do
        echo "  Deleting stale network namespace: $ns"
        "$OBOL" kubectl delete ns "$ns" --timeout=60s 2>/dev/null || true
    done
    pass "No stale ethereum namespaces remaining"
else
    pass "No default local stack cleanup needed"
fi

# ═════════════════════════════════════════════════════════════════
# 6-8. ANVIL FORK
# ═════════════════════════════════════════════════════════════════

step "Anvil: start fork of Base Sepolia on port $ANVIL_PORT"
if ! BASE_SEPOLIA_FORK_RPC="$(resolve_base_sepolia_rpc "$BASE_SEPOLIA_FORK_RPC")"; then
    fail "Could not find a reachable Base Sepolia RPC for Anvil fork"
    emit_metrics; exit 1
fi
# Bind 0.0.0.0 so the k3d clusters can reach this from inside their containers
# via the docker-managed `host.k3d.internal` alias. Default 127.0.0.1 binding
# would only be reachable from the same loopback the host shell uses.
nohup anvil --fork-url "$BASE_SEPOLIA_FORK_RPC" --port "$ANVIL_PORT" \
    --host 0.0.0.0 \
    > "$ANVIL_LOG" 2>&1 &
ANVIL_PID=$!
# Public Base Sepolia RPC sometimes takes more than 20s to serve the first fork
# response on remote QA hosts, even when Anvil is healthy. Poll long enough to
# cover that cold-start path, and fail early if the process exits.
ready=0
for _ in $(seq 1 60); do
    if curl -sf "$ANVIL_RPC_HOST" -X POST -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' >/dev/null 2>&1; then
        ready=1; break
    fi
    if ! kill -0 "$ANVIL_PID" 2>/dev/null; then
        break
    fi
    sleep 1
done
if [ "$ready" -ne 1 ]; then
    fail "Anvil failed to start on $ANVIL_RPC_HOST (see $ANVIL_LOG)"
    emit_metrics; exit 1
fi
pass "Anvil up at $ANVIL_RPC_HOST (pid $ANVIL_PID)"

step "Anvil: chain ID == 0x14a34 (84532, Base Sepolia)"
chain_id_resp=$(curl -sf "$ANVIL_RPC_HOST" -X POST -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>&1) || true
if echo "$chain_id_resp" | grep -qi '"result":"0x14a34"'; then
    pass "Anvil is a Base Sepolia fork (chain 84532)"
else
    fail "Anvil chain ID unexpected — ${chain_id_resp:0:200}"
    emit_metrics; exit 1
fi

step "Anvil: USDC contract present on the fork (sanity)"
usdc_name=$(env -u CHAIN cast call "$USDC_ADDRESS" "name()(string)" \
    --rpc-url "$ANVIL_RPC_HOST" 2>&1) || true
if echo "$usdc_name" | grep -q 'USDC'; then
    pass "USDC contract reachable on Anvil fork: $usdc_name"
else
    fail "USDC contract missing on Anvil fork — ${usdc_name:0:200}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 9-10. x402-rs FACILITATOR
# ═════════════════════════════════════════════════════════════════

step "Facilitator: start x402-rs container pointing at Anvil"
FACILITATOR_CONFIG="$FLOW13_ARTIFACT_DIR/facilitator-config.json"
FAC_SIGNER_KEY=$(hh_key 0)
FAC_SIGNER_KEY="${FAC_SIGNER_KEY#0x}"
cat > "$FACILITATOR_CONFIG" << FEOF
{
  "port": $FACILITATOR_PORT, "host": "0.0.0.0",
  "chains": {"eip155:84532": {"eip1559": true, "flashblocks": false,
    "signers": ["$FAC_SIGNER_KEY"],
    "rpc": [{"http": "$ANVIL_RPC_FACILITATOR", "rate_limit": 50}]}},
  "schemes": [
    {"id": "v1-eip155-exact", "chains": "eip155:*"},
    {"id": "v2-eip155-exact", "chains": "eip155:*",
     "config": {"eip2612_gas_sponsoring": true}}
  ]
}
FEOF
FACILITATOR_CONTAINER="obol-flow13-x402-facilitator-$$"
if ! start_x402_facilitator_container "$FACILITATOR_CONTAINER" "$FACILITATOR_CONFIG" "$FACILITATOR_LOG" "$FACILITATOR_PORT"; then
    fail "Facilitator container failed to start — see $FACILITATOR_LOG"
    emit_metrics; exit 1
fi
fac_ready=0
for _ in $(seq 1 30); do
    if curl -sf "$FACILITATOR_URL_HOST/supported" >/dev/null 2>&1; then
        fac_ready=1; break
    fi
    sleep 1
done
if [ "$fac_ready" -eq 1 ]; then
    pass "Facilitator container up at $FACILITATOR_URL_HOST ($FACILITATOR_CONTAINER)"
else
    write_x402_facilitator_logs "$FACILITATOR_CONTAINER" "$FACILITATOR_LOG"
    fail "Facilitator did not become reachable — see $FACILITATOR_LOG"
    emit_metrics; exit 1
fi

step "Facilitator: /supported advertises base-sepolia exact (v1+v2)"
# The OBOL Permit2 / EIP-2612 gas sponsoring path is enabled via
# config.eip2612_gas_sponsoring=true on the v2-eip155-exact scheme — there is
# no separate "permit2" scheme. The buyer-side is what produces a Permit2
# payment payload; the facilitator's only job is to advertise v2-exact and
# accept the sponsored authorization at /verify and /settle time.
sup_json=$(curl -sf --max-time 5 "$FACILITATOR_URL_HOST/supported" 2>/dev/null || true)
if SUP="$sup_json" python3 - <<'PY'
import json, os, sys
try:
    d = json.loads(os.environ["SUP"])
except Exception:
    sys.exit(1)
v1_ok = False
v2_ok = False
for k in d.get("kinds", []):
    net = k.get("network", "")
    scheme = k.get("scheme", "")
    ver = k.get("x402Version")
    if net in ("base-sepolia", "eip155:84532") and scheme == "exact":
        if ver == 1:
            v1_ok = True
        if ver == 2:
            v2_ok = True
sys.exit(0 if v1_ok and v2_ok else 1)
PY
then
    pass "Facilitator advertises base-sepolia v1+v2 exact (Permit2 path ready)"
else
    fail "Facilitator missing v1+v2 exact for base-sepolia — kinds: ${sup_json:0:300}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 11. DEPLOY OBOL TOKEN ON THE FORK (forge create against Anvil)
# ═════════════════════════════════════════════════════════════════

step "OBOL token: deploy ForkObolToken via forge create"
FORK_OBOL_DIR="$OBOL_ROOT/contracts/fork-obol"
if [ ! -d "$FORK_OBOL_DIR" ]; then
    fail "fork-obol contract project missing at $FORK_OBOL_DIR"
    emit_metrics; exit 1
fi
(cd "$FORK_OBOL_DIR" && forge build >/dev/null 2>&1) || {
    fail "forge build failed in $FORK_OBOL_DIR"
    emit_metrics; exit 1
}
DEPLOYER_KEY=$(hh_key 0)        # Anvil[0] funds itself + acts as deployer
DEPLOYER_ADDR=$(hh_addr 0)
forge_out=$(cd "$FORK_OBOL_DIR" && forge create \
    --root "$FORK_OBOL_DIR" \
    src/ForkObolToken.sol:ForkObolToken \
    --rpc-url "$ANVIL_RPC_HOST" \
    --private-key "$DEPLOYER_KEY" \
    --broadcast \
    --json \
    --constructor-args "$DEPLOYER_ADDR" "0" 2>&1) || true
OBOL_TOKEN=$(echo "$forge_out" | python3 -c "
import json, sys
try:
    d = json.loads(sys.stdin.read())
    print(d.get('deployedTo','') or '')
except Exception:
    pass" 2>/dev/null)
if [ -z "$OBOL_TOKEN" ]; then
    fail "forge create did not return deployedTo — ${forge_out:0:300}"
    emit_metrics; exit 1
fi
# Re-export so lib.sh's generic ERC-20 helpers can scan our OBOL Transfer logs.
export USDC_ADDRESS_BASE_SEPOLIA="$OBOL_TOKEN"
pass "OBOL token deployed at $OBOL_TOKEN"

# EIP-712 early-fail probe: the ServiceOffer below pins `eip712Name: "Obol Network"`
# / `eip712Version: "1"`. If the deployed contract's name()/version don't match,
# the buyer will sign Permit2 payloads against a different EIP-712 domain than
# the contract's permit() expects, and settlement will fail at /verify with an
# unhelpful error. Catch the mismatch here, before any signing happens.
EXPECTED_EIP712_NAME="Obol Network"
TOKEN_NAME=$(env -u CHAIN cast call "$OBOL_TOKEN" "name()(string)" \
    --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d '"')
if [ "$TOKEN_NAME" != "$EXPECTED_EIP712_NAME" ]; then
    fail "EIP-712 name mismatch: token reports '$TOKEN_NAME', ServiceOffer pins '$EXPECTED_EIP712_NAME'"
    emit_metrics; exit 1
fi
pass "EIP-712 domain probe: token name() = '$TOKEN_NAME' matches eip712Name"

# ═════════════════════════════════════════════════════════════════
# 12. MINT 10 OBOL TO ALICE + BOB SIGNER
#     (Bob signer address is unknown until his stack is up — we mint to the
#     Alice EOA + the deployer for now and re-mint to the Bob signer later.
#     Step 30 records the per-wallet balances and treats the mints as funding.)
# ═════════════════════════════════════════════════════════════════

step "OBOL token: mint 10 OBOL to Alice ($ALICE_WALLET)"
ten_obol="10000000000000000000"   # 10 * 1e18
# --json keeps the output machine-readable across foundry versions; older
# `cast send` text format dropped the "transactionHash:" prefix in some 1.x
# releases, which makes regex-based extraction unreliable.
mint_out=$(env -u CHAIN cast send --json "$OBOL_TOKEN" \
    "mint(address,uint256)" "$ALICE_WALLET" "$ten_obol" \
    --rpc-url "$ANVIL_RPC_HOST" --private-key "$DEPLOYER_KEY" 2>&1 || true)
ALICE_MINT_TX=$(echo "$mint_out" | python3 -c 'import json,sys
try:
    d=json.loads(sys.stdin.read())
    print(d.get("transactionHash",""))
except Exception:
    pass' || true)
alice_obol_bal=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$ALICE_WALLET" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
if [ -n "$alice_obol_bal" ] && [ "$alice_obol_bal" = "$ten_obol" ]; then
    pass "Alice OBOL balance: $alice_obol_bal (tx $ALICE_MINT_TX)"
    [ -n "$ALICE_MINT_TX" ] && archive_receipt alice-mint "$ALICE_MINT_TX" 5 1 || true
else
    fail "Alice OBOL mint did not credit balance — got ${alice_obol_bal:-0} expected $ten_obol — ${mint_out:0:200}"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 13-19. ALICE STACK
# ═════════════════════════════════════════════════════════════════

step "Alice: build obol binary"
go build -o "$OBOL_ROOT/.build/obol" ./cmd/obol 2>&1 || { fail "build failed"; emit_metrics; exit 1; }
pass "Binary built"

step "Alice: bootstrap workspace"
bootstrap_flow_workspace "$ALICE_DIR" "$OBOL_ROOT/.build/obol"
pass "Alice workspace ready"

stack_init_and_up_with_retry "Alice" alice "$ALICE_DIR"

route_llm_via_obol_cli alice

poll_step_grep "Alice: x402 pods running" "Running" 30 10 \
    alice kubectl get pods -n x402 --no-headers

step "Alice: anvil reachable from inside cluster"
if detected_host=$(detect_cluster_host_for_anvil alice); then
    CLUSTER_HOST="$detected_host"
    ANVIL_RPC_CLUSTER="http://$CLUSTER_HOST:$ANVIL_PORT"
    FACILITATOR_URL_CLUSTER="http://$CLUSTER_HOST:$FACILITATOR_PORT"
    pass "Alice cluster can reach $ANVIL_RPC_CLUSTER"
else
    fail "Alice cluster cannot reach Anvil via Docker host aliases — probe: ${LAST_CLUSTER_PROBE_OUT:0:300}"
    emit_metrics; exit 1
fi

step "Alice: add base-sepolia route in eRPC pointing at our Anvil (writes allowed)"
alice network add base-sepolia --endpoint "$ANVIL_RPC_CLUSTER" --allow-writes 2>&1 | tail -2
alice kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
alice kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
if pin_erpc_chain_single_upstream alice 84532 "custom-84532-0"; then
    pass "Alice eRPC: 84532 pinned to custom-84532-0 -> $ANVIL_RPC_CLUSTER"
else
    fail "Could not pin Alice eRPC chain 84532 to custom-84532-0 (check upstream id)"
fi

step "Alice: configure x402 pricing pointing at local facilitator"
alice sell pricing \
    --wallet "$ALICE_WALLET" \
    --chain base-sepolia \
    --facilitator-url "$FACILITATOR_URL_CLUSTER" 2>&1 | tail -1
pass "Pricing configured (facilitator=$FACILITATOR_URL_CLUSTER)"

step "Alice: CA bundle populated"
ca_size=$(alice kubectl get cm ca-certificates -n x402 -o jsonpath='{.data}' 2>/dev/null | wc -c | tr -d ' ')
if [ "$ca_size" -gt 1000 ]; then
    pass "CA bundle: $ca_size bytes"
else
    fail "CA bundle empty or too small: $ca_size bytes"
fi

# ═════════════════════════════════════════════════════════════════
# 20. ALICE: CREATE OBOL-PRICED ServiceOffer
# ═════════════════════════════════════════════════════════════════

step "Alice: create OBOL-priced ServiceOffer (transferMethod=permit2)"
REG_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
ALICE_OFFER_YAML=$(mktemp)
cat > "$ALICE_OFFER_YAML" <<YAML
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: alice-obol-inference
  namespace: llm
spec:
  type: http
  upstream:
    service: litellm
    namespace: llm
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "$ALICE_WALLET"
    asset:
      address: "$OBOL_TOKEN"
      symbol: "OBOL"
      decimals: 18
      transferMethod: "permit2"
      eip712Name: "Obol Network"
      eip712Version: "1"
    price:
      perRequest: "0.001"
  path: /services/alice-obol-inference
  # Intentionally NO registration: this flow's focus is the OBOL Permit2
  # payment path, not ERC-8004 discovery. The controller never signs
  # on-chain (registration is a CLI/remote-signer flow); leaving
  # registration off keeps Ready=True reachable without needing to seed
  # a remote-signer. Matches TestIntegration_SellBuySidecar_OBOLPermit2's
  # offer YAML.
YAML
alice kubectl apply -f "$ALICE_OFFER_YAML" 2>&1 | tail -2
rm -f "$ALICE_OFFER_YAML"
pass "ServiceOffer alice-obol-inference applied"

poll_step_grep "Alice: ServiceOffer Ready=True" "True" 60 5 \
    alice kubectl get serviceoffers.obol.org alice-obol-inference -n llm \
        -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# ═════════════════════════════════════════════════════════════════
# 21. TUNNEL + 402 GATE
# ═════════════════════════════════════════════════════════════════

step "Alice: bring up cloudflared tunnel"
# `obol stack up` deploys the cloudflared Deployment at 0 replicas. `obol sell
# http` would scale it to 1 via internal EnsureTunnelForSell, but flow-13
# applies the OBOL ServiceOffer YAML directly (because `obol sell http`
# doesn't expose the OBOL Permit2 asset metadata flags yet). `obol tunnel
# restart` only does `rollout restart` — a no-op when replicas=0. So we
# explicitly scale here, then poll for tunnel-status to capture the URL.
alice kubectl scale deployment/cloudflared -n traefik --replicas=1 2>&1 | tail -2
alice kubectl rollout status deployment/cloudflared -n traefik --timeout=180s 2>&1 | tail -3
pass "Cloudflared scaled to 1"

step "Alice: tunnel URL"
TUNNEL_URL=""
for _ in $(seq 1 30); do
    TUNNEL_URL=$(alice tunnel status 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
    [ -n "$TUNNEL_URL" ] && break
    sleep 5
done
if [ -z "$TUNNEL_URL" ]; then
    fail "No tunnel URL after 150s"; emit_metrics; exit 1
fi
TUNNEL_HOST=$(tunnel_hostname "$TUNNEL_URL")
TUNNEL_IP=$(resolve_public_ipv4 "$TUNNEL_HOST" || true)
pass "Tunnel: $TUNNEL_URL"

step "Alice: 402 gate works on $TUNNEL_URL/services/alice-obol-inference"
gate_code=""
for _ in $(seq 1 48); do
    gate_code=$(curl_tunnel_402_code "$TUNNEL_URL/services/alice-obol-inference/v1/chat/completions" "$TUNNEL_HOST" "$TUNNEL_IP")
    [ "$gate_code" = "402" ] && break
    sleep 5
done
if [ "$gate_code" = "402" ]; then
    pass "402 gate works"
else
    fail "402 gate returned ${gate_code:-no HTTP response} after 240s"
fi

# ═════════════════════════════════════════════════════════════════
# 22. ERC-8004 REGISTRATION (skipped on this flow)
# ═════════════════════════════════════════════════════════════════
# This flow's focus is the OBOL Permit2 payment path. Registration is
# disabled in the offer YAML above; Bob's agent discovers Alice through
# the tunnel storefront / skill.md instead of by scanning the registry.
# Steps 22 and 25 keep their slot numbers so the receipt-summary.json
# still has well-known keys, but registration tx is intentionally empty.

step "ERC-8004 registration intentionally skipped on flow-13"
AGENT_ID=""
REGISTRATION_TX=""
pass "Registration disabled (OBOL Permit2 flow does not exercise ERC-8004)"

# ═════════════════════════════════════════════════════════════════
# 23-28. BOB STACK
# ═════════════════════════════════════════════════════════════════

step "Bob: bootstrap workspace"
bootstrap_flow_workspace "$BOB_DIR" "$OBOL_ROOT/.build/obol"
pass "Bob workspace ready"

stack_init_and_up_with_retry "Bob" bob "$BOB_DIR" preseed_bob_wallet

route_llm_via_obol_cli bob

# detect_buyer_runtime re-exports BOB_AGENT_NS / DEPLOY / CONTAINER / SERVICE /
# REMOTE_PORT / OBOL_SKILLS_DIR / LABEL / RUNTIME based on Bob's actual namespace.
detect_buyer_runtime bob

poll_step_grep "Bob: x402 pods running" "Running" 30 10 \
    bob kubectl get pods -n x402 --no-headers

step "Bob: anvil reachable from inside cluster"
if wait_cluster_anvil bob; then
    pass "Bob cluster can reach $ANVIL_RPC_CLUSTER"
else
    fail "Bob cluster cannot reach Anvil at $ANVIL_RPC_CLUSTER — probe: ${LAST_CLUSTER_PROBE_OUT:0:300}"
    emit_metrics; exit 1
fi

step "Bob: add base-sepolia route to Anvil"
bob network add base-sepolia --endpoint "$ANVIL_RPC_CLUSTER" --allow-writes 2>&1 | tail -2
bob kubectl rollout restart deployment/erpc -n erpc 2>/dev/null || true
bob kubectl rollout status deployment/erpc -n erpc --timeout=60s 2>/dev/null || true
if pin_erpc_chain_single_upstream bob 84532 "custom-84532-0"; then
    pass "Bob eRPC: 84532 pinned to custom-84532-0 -> $ANVIL_RPC_CLUSTER"
else
    fail "Could not pin Bob eRPC chain 84532 to custom-84532-0"
fi

ensure_bob_tunnel_dns "$TUNNEL_HOST" "$TUNNEL_IP"

poll_step_grep "Bob: ${BOB_AGENT_RUNTIME} agent API-server ready" "true" 36 5 \
    bob kubectl get pods -n "$BOB_AGENT_NS" -l "$BOB_AGENT_LABEL" \
        -o "jsonpath={range .items[*].status.containerStatuses[?(@.name=='${BOB_AGENT_CONTAINER}')]}{.ready}{'\n'}{end}"

# ═════════════════════════════════════════════════════════════════
# 29. BOB: TUNNEL REACHABILITY FROM AGENT POD (must see 402)
# ═════════════════════════════════════════════════════════════════

step "Bob: tunnel reachable from agent pod (expect 402)"
bob_tunnel_code=""
for _ in $(seq 1 24); do
    bob_tunnel_code=$(bob_tunnel_402_code)
    [ "$bob_tunnel_code" = "402" ] && break
    sleep 5
done
if [ "$bob_tunnel_code" = "402" ]; then
    pass "Tunnel reachable from agent pod (402)"
else
    fail "Tunnel did not return 402 from agent pod — ${bob_tunnel_code:-no response}"
fi

# ═════════════════════════════════════════════════════════════════
# 30-31. FUND BOB'S SIGNER (mint OBOL on the fork) + verify eRPC sees it
# ═════════════════════════════════════════════════════════════════

step "Bob: locate remote-signer wallet address"
BOB_SIGNER_ADDR=""
for candidate_path in \
    "$BOB_DIR/config/applications/$BOB_AGENT_RUNTIME/obol-agent/wallet.json" \
    "$BOB_DIR/config/applications/openclaw/obol-agent/wallet.json" \
    "$BOB_DIR/config/applications/hermes/obol-agent/wallet.json"; do
    if [ -f "$candidate_path" ]; then
        BOB_SIGNER_ADDR=$(python3 -c "
import json
try:
    d=json.load(open('$candidate_path'))
    print(d.get('address',''))
except Exception:
    pass" 2>/dev/null)
        [ -n "$BOB_SIGNER_ADDR" ] && break
    fi
done
if [ -z "$BOB_SIGNER_ADDR" ]; then
    fail "Could not determine Bob's remote-signer address"
    emit_metrics; exit 1
fi
if [ "$(lower_addr "$BOB_SIGNER_ADDR")" != "$(lower_addr "$BOB_WALLET")" ]; then
    fail "Bob remote-signer wallet mismatch — signer=$BOB_SIGNER_ADDR expected=$BOB_WALLET"
    emit_metrics; exit 1
fi
pass "Bob remote-signer uses derived buyer wallet: $BOB_SIGNER_ADDR"

step "Bob: mint 10 OBOL to remote-signer ($BOB_SIGNER_ADDR)"
FUNDING_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
fund_out=$(env -u CHAIN cast send --json "$OBOL_TOKEN" \
    "mint(address,uint256)" "$BOB_SIGNER_ADDR" "$ten_obol" \
    --rpc-url "$ANVIL_RPC_HOST" --private-key "$DEPLOYER_KEY" 2>&1 || true)
FUNDING_TX=$(echo "$fund_out" | python3 -c 'import json,sys
try:
    d=json.loads(sys.stdin.read())
    print(d.get("transactionHash",""))
except Exception:
    pass' || true)
if [ -n "$FUNDING_TX" ] && archive_receipt funding "$FUNDING_TX" 12 2; then
    pass "Funding receipt archived: $FUNDING_TX"
else
    # Fallback: confirm balance on Anvil even if tx-hash extraction failed.
    bob_obol_bal=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$BOB_SIGNER_ADDR" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    if [ "$bob_obol_bal" = "$ten_obol" ]; then
        pass "Bob OBOL balance: $bob_obol_bal (mint succeeded; tx-hash extraction skipped)"
    else
        fail "Could not archive Bob OBOL mint receipt and balance check failed — ${fund_out:0:300}"
    fi
fi

# Also seed Bob signer with ETH so settlement gas is available even if the
# facilitator is not gas-sponsoring this particular call.
env -u CHAIN cast rpc anvil_setBalance "$BOB_SIGNER_ADDR" "0xDE0B6B3A7640000" \
    --rpc-url "$ANVIL_RPC_HOST" >/dev/null 2>&1 || true

step "Bob: signer holds funded OBOL balance"
# Direct host-side balance read against the Anvil fork. The whole point of
# this step is to assert "the mint actually credited Bob's signer". Going
# through eRPC inside Bob's cluster adds a config-watch delay (~60s) and
# distroless-probe complexity; the canonical proof is the on-chain balance
# itself, which buy.py will read by the same RPC path inside the pod a few
# steps later. If buy.py can't see it, step 44's PurchaseRequest will fail
# and surface the real signal.
got_balance=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
    "$BOB_SIGNER_ADDR" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
if [ -n "$got_balance" ] && [ "$got_balance" = "$ten_obol" ]; then
    pass "Bob signer OBOL balance (anvil): $got_balance"
else
    fail "Bob signer OBOL balance not credited — got ${got_balance:-0} expected $ten_obol"
fi

step "Bob: ensure local OBOL Permit2 allowance"
PERMIT2_ADDRESS="0x000000000022D473030F116dDEE9F6B43aC78BA3"
permit2_allowance=$(env -u CHAIN cast call "$OBOL_TOKEN" "allowance(address,address)(uint256)" \
    "$BOB_SIGNER_ADDR" "$PERMIT2_ADDRESS" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
if [ -n "$permit2_allowance" ] && [ "$permit2_allowance" != "0" ]; then
    pass "Bob Permit2 allowance already set: $permit2_allowance"
else
    approve_out=$(env -u CHAIN cast send --json "$OBOL_TOKEN" \
        "approve(address,uint256)" "$PERMIT2_ADDRESS" \
        115792089237316195423570985008687907853269984665640564039457584007913129639935 \
        --rpc-url "$ANVIL_RPC_HOST" --private-key "$BOB_PRIVATE_KEY" 2>&1 || true)
    approve_tx=$(echo "$approve_out" | python3 -c 'import json,sys
try:
    d=json.loads(sys.stdin.read())
    print(d.get("transactionHash",""))
except Exception:
    pass' || true)
    if [ -z "$approve_tx" ]; then
        fail "Could not submit Bob Permit2 approval: ${approve_out:0:300}"
        emit_metrics; exit 1
    fi
    permit2_ready=0
    for _ in $(seq 1 30); do
        permit2_allowance=$(env -u CHAIN cast call "$OBOL_TOKEN" "allowance(address,address)(uint256)" \
            "$BOB_SIGNER_ADDR" "$PERMIT2_ADDRESS" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
        if [ -n "$permit2_allowance" ] && [ "$permit2_allowance" != "0" ]; then
            permit2_ready=1
            break
        fi
        sleep 2
    done
    if [ "$permit2_ready" = "1" ]; then
        pass "Bob Permit2 approval confirmed: tx=$approve_tx allowance=$permit2_allowance"
    else
        fail "Bob Permit2 approval did not become visible after tx $approve_tx"
        emit_metrics; exit 1
    fi
fi

# ═════════════════════════════════════════════════════════════════
# 32-33. AGENT TOKEN + PORT-FORWARD
# ═════════════════════════════════════════════════════════════════

step "Bob: get $BOB_AGENT_RUNTIME API server token"
if ! BOB_TOKEN_OUT=$(agent_auth_token bob "$BOB_AGENT_RUNTIME" obol-agent 2>&1); then
    fail "Could not get Bob's gateway token: ${BOB_TOKEN_OUT:0:200}"
    emit_metrics; exit 1
fi
BOB_TOKEN="$BOB_TOKEN_OUT"
pass "Token: ${BOB_TOKEN:0:10}..."

step "Bob: $BOB_AGENT_RUNTIME API port-forward"
BOB_AGENT_PORT=$(pick_free_port)
PF_AGENT_LOG=$(mktemp)
bob kubectl port-forward -n "$BOB_AGENT_NS" "svc/$BOB_AGENT_SERVICE" \
    "${BOB_AGENT_PORT}:${BOB_AGENT_REMOTE_PORT}" >"$PF_AGENT_LOG" 2>&1 &
PF_AGENT=$!
pf_ready=0
for _ in $(seq 1 20); do
    if python3 - "$BOB_AGENT_PORT" <<'PY'
import socket, sys
s = socket.socket(); s.settimeout(1)
try: s.connect(("127.0.0.1", int(sys.argv[1])))
except OSError: sys.exit(1)
finally: s.close()
PY
    then pf_ready=1; break; fi
    if ! kill -0 "$PF_AGENT" 2>/dev/null; then break; fi
    sleep 1
done
if [ "$pf_ready" = "1" ]; then
    pass "Agent API on localhost:$BOB_AGENT_PORT"
else
    fail "Agent port-forward failed: $(tail -n 10 "$PF_AGENT_LOG" 2>/dev/null | tr '\n' ' ')"
    emit_metrics; exit 1
fi

# ═════════════════════════════════════════════════════════════════
# 34. AGENT DISCOVERS ALICE (via skill.md or ERC-8004)
# ═════════════════════════════════════════════════════════════════

step "Bob's agent: discover Alice's OBOL service"
discover_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"$BOB_AGENT_RUNTIME-agent\",
        \"messages\": [{
            \"role\": \"user\",
            \"content\": \"Search the local ERC-8004 registry on Base Sepolia (chain 84532) for the agent named 'Dual-Stack OBOL Test Inference'. Use the discovery skill or fetch $TUNNEL_URL/skill.md. Report the agent's ID, name, endpoint, and the asset symbol it requires for x402 payments.\"
        }],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
discover_content=$(extract_assistant_content "$discover_response" 2>/dev/null || true)
echo "${discover_content:0:500}"
# Discovery is informational only on this flow. The structural proof that the
# agent can reach Alice is the next "buy" step + the PurchaseRequest CR going
# Ready=True. Natural-language assertions on agent responses are brittle.
pass "Agent discovery prompt issued (success will be confirmed by buy + PurchaseRequest CR)"

# ═════════════════════════════════════════════════════════════════
# 35. BUY 5 AUTHS VIA buy.py (Permit2-aware on integration branch)
# ═════════════════════════════════════════════════════════════════

step "Bob's agent: buy 5 OBOL Permit2 auths from Alice"
buy_response=$(curl -sf --max-time 300 \
    -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"$BOB_AGENT_RUNTIME-agent\",
        \"messages\": [{
            \"role\": \"user\",
            \"content\": \"Load the buy-x402 skill, then use your terminal tool. Run exactly once: ERPC_URL=http://erpc.erpc.svc.cluster.local/rpc ERPC_NETWORK=base-sepolia python3 $BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py buy alice-obol --endpoint $TUNNEL_URL/services/alice-obol-inference/v1/chat/completions --model $OBOL_LLM_MODEL --count 5\"
        }],
        \"max_tokens\": 4000,
        \"stream\": false
    }" 2>&1 || true)
buy_content=$(extract_assistant_content "$buy_response" 2>/dev/null || true)
echo "${buy_content:0:500}"
# Don't grep buy_content for natural-language confirmation; structural success
# is the PurchaseRequest CR Ready=True poll below.
if [ -z "$(printf '%s' "$buy_content" | tr -d '[:space:]')" ]; then
    echo "  ! Agent returned no final assistant text; confirming purchase via PurchaseRequest CR"
fi
if printf '%s' "$buy_content" | agent_response_refused; then
    fail "Agent refused to run buy.py: ${buy_content:0:500}"
    emit_metrics; exit 1
fi
pass "Agent buy prompt issued (success will be confirmed by PurchaseRequest CR)"

# ═════════════════════════════════════════════════════════════════
# 36-39. PR Ready / LiteLLM rollout / sidecar auths / paid call
# ═════════════════════════════════════════════════════════════════

poll_step_grep "Bob: PurchaseRequest Ready" "True" 24 5 purchase_request_status
pr_status=$(purchase_request_status)
if echo "$pr_status" | grep -q "True"; then
    pass "PurchaseRequest CR ready: $pr_status"
else
    fail "PurchaseRequest CR not ready: $pr_status"
    emit_metrics; exit 1
fi

step "Bob: LiteLLM rollout settled"
bob kubectl rollout status deployment/litellm -n llm --timeout=180s 2>&1 | tail -2
pass "LiteLLM rollout settled"

poll_step_grep "Bob: buyer sidecar has exactly 5 auths" "remaining=5" 24 5 buyer_sidecar_status
buyer_status=$(buyer_sidecar_status)
if echo "$buyer_status" | grep -q "remaining=5"; then
    pass "Sidecar has exactly 5 auths: $buyer_status"
else
    fail "Sidecar auth count mismatch; expected remaining=5, got: $buyer_status"
    emit_metrics; exit 1
fi
PAID_MODEL=$(echo "$buyer_status" | grep -o 'model=[^ ]*' | sed 's/model=//' | head -1 || true)
[ -z "$PAID_MODEL" ] && PAID_MODEL="paid/$OBOL_LLM_MODEL"

step "Bob's agent: paid inference via $PAID_MODEL"
BOB_MASTER_KEY=$(bob kubectl get secret litellm-secrets -n llm \
    -o jsonpath='{.data.LITELLM_MASTER_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [ -z "$BOB_MASTER_KEY" ]; then
    fail "Could not read Bob LiteLLM master key"
    emit_metrics; exit 1
fi
BUY_START_BLOCK=$(env -u CHAIN cast block-number --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | tr -d ' ' || true)
inference_response=$(litellm_paid_inference)
if echo "$inference_response" | grep -q "STATUS=200"; then
    pass "Paid inference succeeded"
    echo "$inference_response"
else
    fail "Paid inference failed: $inference_response"
fi

step "Paid OBOL inference: response content is a coherent answer"
EXPECTED_PAID_CONTENT="OBOL payment smoke test passed."
PAID_CONTENT=$(echo "$inference_response" | sed -n 's/^CONTENT=//p' | head -1)
if [ -z "$PAID_CONTENT" ]; then
    fail "Paid inference response had no CONTENT line: ${inference_response:0:300}"
elif echo "$inference_response" | grep -q '^REASONING_PRESENT=1'; then
    fail "Paid inference returned reasoning metadata instead of only final content: ${inference_response:0:300}"
elif echo "$PAID_CONTENT" | paid_inference_content_invalid; then
    fail "Paid inference reply contained reasoning or tool-catalogue text: ${PAID_CONTENT:0:300}"
elif ! printf '%s' "$PAID_CONTENT" | grep -Fq "$EXPECTED_PAID_CONTENT"; then
    fail "Paid inference reply missed expected smoke sentence; got: ${PAID_CONTENT:0:300}"
elif [ "${#PAID_CONTENT}" -lt 5 ]; then
    fail "Paid inference reply is suspiciously short (${#PAID_CONTENT} chars): $PAID_CONTENT"
else
    pass "Paid OBOL inference reply is coherent (${#PAID_CONTENT} chars)"
fi

# ═════════════════════════════════════════════════════════════════
# 40-41. SETTLEMENT RECEIPT + BALANCE DELTA (OBOL, not USDC)
# ═════════════════════════════════════════════════════════════════

step "On-chain: OBOL settlement Transfer($BOB_SIGNER_ADDR -> $ALICE_WALLET, $OBOL_PRICE_WEI)"
# wait_usdc_transfer_receipt is a generic ERC-20 Transfer scanner; we point it
# at OBOL_TOKEN via USDC_ADDRESS_BASE_SEPOLIA above.
settlement_match=$(wait_usdc_transfer_receipt settlement \
    "$BOB_SIGNER_ADDR" "$ALICE_WALLET" "$OBOL_PRICE_WEI" "$BUY_START_BLOCK" 30 2 || true)
SETTLEMENT_TX=$(echo "$settlement_match" | awk '{print $1; exit}')
SETTLEMENT_AMOUNT=$(echo "$settlement_match" | awk '{print $2; exit}')
if [ -n "$SETTLEMENT_TX" ] && [ "$SETTLEMENT_AMOUNT" = "$OBOL_PRICE_WEI" ]; then
    echo "  tx=$SETTLEMENT_TX amount=$SETTLEMENT_AMOUNT (1e15 wei = 0.001 OBOL)"
    pass "OBOL settlement receipt archived"
else
    fail "No Bob-signer -> Alice OBOL Transfer for $OBOL_PRICE_WEI wei after block $BUY_START_BLOCK"
fi

step "On-chain: balance deltas (Alice +1e15 / Bob signer -1e15)"
ALICE_BAL_BEFORE_PAID="$ten_obol"
BOB_SIGNER_BAL_BEFORE_PAID="$ten_obol"
ALICE_BAL_AFTER=""
BOB_SIGNER_BAL_AFTER=""
expected_alice_after=$(python3 -c "print($ALICE_BAL_BEFORE_PAID + $OBOL_PRICE_WEI)")
expected_bob_after=$(python3 -c "print($BOB_SIGNER_BAL_BEFORE_PAID - $OBOL_PRICE_WEI)")
for _ in $(seq 1 30); do
    ALICE_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$ALICE_WALLET" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    BOB_SIGNER_BAL_AFTER=$(env -u CHAIN cast call "$OBOL_TOKEN" "balanceOf(address)(uint256)" \
        "$BOB_SIGNER_ADDR" --rpc-url "$ANVIL_RPC_HOST" 2>/dev/null | grep -oE '^[0-9]+' | head -1 || true)
    if [ "$ALICE_BAL_AFTER" = "$expected_alice_after" ] && [ "$BOB_SIGNER_BAL_AFTER" = "$expected_bob_after" ]; then
        break
    fi
    sleep 2
done
echo "  Alice (pre-paid):  $ALICE_BAL_BEFORE_PAID"
echo "  Alice (final):     ${ALICE_BAL_AFTER:-unknown}    expected $expected_alice_after"
echo "  Bob signer (pre):  $BOB_SIGNER_BAL_BEFORE_PAID"
echo "  Bob signer (final):${BOB_SIGNER_BAL_AFTER:-unknown} expected $expected_bob_after"
if [ "$ALICE_BAL_AFTER" = "$expected_alice_after" ]; then
    pass "Alice balance increased by exactly $OBOL_PRICE_WEI wei"
else
    fail "Alice balance delta wrong (expected $expected_alice_after, got ${ALICE_BAL_AFTER:-unknown})"
fi
if [ "$BOB_SIGNER_BAL_AFTER" = "$expected_bob_after" ]; then
    pass "Bob signer balance decreased by exactly $OBOL_PRICE_WEI wei"
else
    fail "Bob signer balance delta wrong (expected $expected_bob_after, got ${BOB_SIGNER_BAL_AFTER:-unknown})"
fi

# ═════════════════════════════════════════════════════════════════
# 42-44. CLEANUP
# ═════════════════════════════════════════════════════════════════

cleanup_pid "$PF_AGENT" 2>/dev/null || true
PF_AGENT=""
rm -f "$PF_AGENT_LOG"
PF_AGENT_LOG=""

step "Cleanup: delete Alice's ServiceOffer"
alice sell delete alice-obol-inference -n llm -f 2>&1 | tail -1 || true
pass "ServiceOffer delete issued"

step "Cleanup: Alice stack down"
alice stack down 2>&1 | tail -1 || true
pass "Alice stack down issued"

step "Cleanup: Bob stack down + kill anvil + facilitator"
bob stack down 2>&1 | tail -1 || true
if [ -n "$FACILITATOR_CONTAINER" ]; then
    write_x402_facilitator_logs "$FACILITATOR_CONTAINER" "$FACILITATOR_LOG"
    docker rm -f "$FACILITATOR_CONTAINER" >/dev/null 2>&1 || true
fi
FACILITATOR_CONTAINER=""
if [ -n "$ANVIL_PID" ] && kill -0 "$ANVIL_PID" 2>/dev/null; then
    kill "$ANVIL_PID" 2>/dev/null || true
    wait "$ANVIL_PID" 2>/dev/null || true
fi
ANVIL_PID=""
pass "Local Anvil + facilitator stopped"

# ═════════════════════════════════════════════════════════════════
# 45. RECEIPT SUMMARY (matches flow-11 shape)
# ═════════════════════════════════════════════════════════════════

step "Receipts: write summary"
if FLOW13_ARTIFACT_DIR="$FLOW13_ARTIFACT_DIR" \
   FLOW13_COMMIT="$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || true)" \
   FLOW13_AGENT_ID="${AGENT_ID:-}" \
   FLOW13_ALICE="$ALICE_WALLET" \
   FLOW13_BOB="${BOB_SIGNER_ADDR:-}" \
   FLOW13_BOB_SIGNER="${BOB_SIGNER_ADDR:-}" \
   FLOW13_TUNNEL="${TUNNEL_URL:-}" \
   FLOW13_REGISTRATION_TX="${REGISTRATION_TX:-}" \
   FLOW13_METADATA_TX="" \
   FLOW13_FUNDING_TX="${FUNDING_TX:-}" \
   FLOW13_SETTLEMENT_TX="${SETTLEMENT_TX:-}" \
   FLOW13_OBOL_TOKEN="${OBOL_TOKEN:-}" \
   FLOW13_FACILITATOR_URL="${FACILITATOR_URL_HOST:-}" \
   python3 - <<'PY'
import json, os
from pathlib import Path
artifact_dir = Path(os.environ["FLOW13_ARTIFACT_DIR"])
summary = {
    "commit": os.environ.get("FLOW13_COMMIT", ""),
    "agentId": os.environ.get("FLOW13_AGENT_ID", ""),
    "alice": os.environ.get("FLOW13_ALICE", ""),
    "bob": os.environ.get("FLOW13_BOB", ""),
    "bobSigner": os.environ.get("FLOW13_BOB_SIGNER", ""),
    "tunnel": os.environ.get("FLOW13_TUNNEL", ""),
    "obolToken": os.environ.get("FLOW13_OBOL_TOKEN", ""),
    "facilitator": os.environ.get("FLOW13_FACILITATOR_URL", ""),
    "transactions": {
        "registration": os.environ.get("FLOW13_REGISTRATION_TX", ""),
        "metadata": os.environ.get("FLOW13_METADATA_TX", ""),
        "funding": os.environ.get("FLOW13_FUNDING_TX", ""),
        "settlement": os.environ.get("FLOW13_SETTLEMENT_TX", ""),
    },
}
artifact_dir.mkdir(parents=True, exist_ok=True)
(artifact_dir / "receipt-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
PY
then
    pass "Receipt summary: $FLOW13_ARTIFACT_DIR/receipt-summary.json"
else
    fail "Could not write receipt summary"
fi

emit_metrics
echo ""
echo "════════════════════════════════════════════════════════════"
echo "  Dual-stack OBOL test complete: $PASS_COUNT/$STEP_COUNT passed"
echo "  Alice (seller): $ALICE_WALLET"
echo "  Bob (signer):   ${BOB_SIGNER_ADDR:-unknown}"
echo "  OBOL token:     ${OBOL_TOKEN:-unknown}"
echo "  Tunnel:         ${TUNNEL_URL:-unknown}"
echo "  Anvil:          $ANVIL_RPC_HOST"
echo "  Facilitator:    $FACILITATOR_URL_HOST"
echo "  Artifacts:      $FLOW13_ARTIFACT_DIR"
echo "════════════════════════════════════════════════════════════"
exit_if_failed
