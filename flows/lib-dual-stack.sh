#!/bin/bash
# Shared helpers for the Alice/Bob dual-stack smoke flows.
#
# The scenario scripts still own their business logic and assertions. This file
# only carries repeated orchestration mechanics that are otherwise duplicated
# across the large seller/buyer flows.

dual_stack_flow_prefix() {
    printf '%s' "${DUAL_STACK_FLOW_PREFIX:?DUAL_STACK_FLOW_PREFIX must be set before sourcing lib-dual-stack.sh}"
}

dual_stack_env_or_free_port() {
    local suffix="$1"
    local var
    var="$(dual_stack_flow_prefix)_$suffix"
    if [ -n "${!var:-}" ]; then
        printf '%s\n' "${!var}"
    else
        pick_free_port
    fi
}

alice() {
    OBOL_DEVELOPMENT=true \
    OBOL_NONINTERACTIVE=true \
    OBOL_CONFIG_DIR="$ALICE_DIR/config" \
    OBOL_BIN_DIR="$ALICE_DIR/bin" \
    OBOL_DATA_DIR="$ALICE_DIR/data" \
    "$ALICE_DIR/bin/obol" "$@"
}

bob() {
    OBOL_DEVELOPMENT=true \
    OBOL_NONINTERACTIVE=true \
    OBOL_CONFIG_DIR="$BOB_DIR/config" \
    OBOL_BIN_DIR="$BOB_DIR/bin" \
    OBOL_DATA_DIR="$BOB_DIR/data" \
    "$BOB_DIR/bin/obol" "$@"
}

lower_addr() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

rewrite_k3d_ports() {
    local config_path="$1"
    local http_port="$2"
    local http_alt_port="$3"
    local https_port="$4"
    local https_alt_port="$5"

    if [ ! -f "$config_path" ]; then
        echo "missing k3d config: $config_path" >&2
        return 1
    fi

    sed -i.bak \
        -e "s/port: 80:80/port: ${http_port}:80/" \
        -e "s/port: 8080:80/port: ${http_alt_port}:80/" \
        -e "s/port: 443:443/port: ${https_port}:443/" \
        -e "s/port: 8443:443/port: ${https_alt_port}:443/" \
        "$config_path"
}

refresh_alice_ports() {
    ALICE_HTTP_PORT="$(dual_stack_env_or_free_port ALICE_HTTP_PORT)"
    ALICE_HTTP_ALT_PORT="$(dual_stack_env_or_free_port ALICE_HTTP_ALT_PORT)"
    ALICE_HTTPS_PORT="$(dual_stack_env_or_free_port ALICE_HTTPS_PORT)"
    ALICE_HTTPS_ALT_PORT="$(dual_stack_env_or_free_port ALICE_HTTPS_ALT_PORT)"
}

refresh_bob_ports() {
    BOB_HTTP_PORT="$(dual_stack_env_or_free_port BOB_HTTP_PORT)"
    BOB_HTTP_ALT_PORT="$(dual_stack_env_or_free_port BOB_HTTP_ALT_PORT)"
    BOB_HTTPS_PORT="$(dual_stack_env_or_free_port BOB_HTTPS_PORT)"
    BOB_HTTPS_ALT_PORT="$(dual_stack_env_or_free_port BOB_HTTPS_ALT_PORT)"
}

stack_init_and_up_with_retry() {
    local label="$1"
    local runner="$2"
    local dir="$3"
    local pre_up_hook="${4:-}"
    local attempt out rc

    for attempt in 1 2 3; do
        step "$label: stack init"
        set +e
        out=$("$runner" stack init --force 2>&1)
        rc=$?
        set -e
        printf '%s\n' "$out" | tail -1
        if [ "$rc" -ne 0 ]; then
            printf '%s\n' "$out" | tail -120
            fail "$label: stack init failed (exit $rc)"
            emit_metrics
            exit "$rc"
        fi

        if [ "$label" = "Alice" ]; then
            rewrite_k3d_ports "$dir/config/k3d.yaml" \
                "$ALICE_HTTP_PORT" "$ALICE_HTTP_ALT_PORT" "$ALICE_HTTPS_PORT" "$ALICE_HTTPS_ALT_PORT"
            pass "Alice ports set to $ALICE_HTTP_PORT/$ALICE_HTTP_ALT_PORT/$ALICE_HTTPS_PORT/$ALICE_HTTPS_ALT_PORT"
        else
            rewrite_k3d_ports "$dir/config/k3d.yaml" \
                "$BOB_HTTP_PORT" "$BOB_HTTP_ALT_PORT" "$BOB_HTTPS_PORT" "$BOB_HTTPS_ALT_PORT"
            pass "Bob ports set to $BOB_HTTP_PORT/$BOB_HTTP_ALT_PORT/$BOB_HTTPS_PORT/$BOB_HTTPS_ALT_PORT"
        fi

        if [ -n "$pre_up_hook" ]; then
            "$pre_up_hook"
        fi

        step "$label: stack up"
        set +e
        out=$("$runner" stack up 2>&1)
        rc=$?
        set -e
        if [ "$rc" -eq 0 ]; then
            printf '%s\n' "$out" | tail -3
            pass "$label stack up completed"
            return 0
        fi

        printf '%s\n' "$out" | tail -120
        if [ "$attempt" -lt 3 ] && echo "$out" | grep -qiE "address already in use|failed to bind host port"; then
            echo "  $label stack up hit a host port bind race; retrying with fresh ports (attempt $((attempt + 1))/3)"
            "$runner" stack down >/dev/null 2>&1 || true
            if [ "$label" = "Alice" ]; then
                refresh_alice_ports
            else
                refresh_bob_ports
            fi
            continue
        fi
        if [ "$attempt" -lt 3 ] && echo "$out" | grep -qiE "context deadline exceeded|Client.Timeout|cannot be reached|failed to import images"; then
            echo "  $label stack up hit a transient image/Helm repository error; retrying (attempt $((attempt + 1))/3)"
            "$runner" stack down >/dev/null 2>&1 || true
            sleep 10
            continue
        fi

        fail "$label: stack up failed (exit $rc)"
        emit_metrics
        exit "$rc"
    done
}

preseed_bob_wallet() {
    local deploy_dir existing import_out onboard_out rc

    deploy_dir="$BOB_DIR/config/applications/hermes/obol-agent"
    if [ ! -f "$deploy_dir/helmfile.yaml" ]; then
        step "Bob: scaffold default agent before stack up"
        set +e
        onboard_out=$(bob agent new --runtime hermes --id obol-agent --no-sync 2>&1)
        rc=$?
        set -e
        echo "$onboard_out" | tail -8
        if [ "$rc" -ne 0 ]; then
            fail "Could not scaffold Bob agent before stack up: ${onboard_out:0:300}"
            emit_metrics
            exit "$rc"
        fi
        pass "Bob default agent scaffolded"
    fi

    existing=$(bob agent wallet address --runtime hermes obol-agent 2>/dev/null || true)
    if [ "$(lower_addr "$existing")" = "$(lower_addr "$BOB_WALLET")" ]; then
        pass "Bob wallet preseeded: $existing"
        return 0
    fi

    step "Bob: import derived buyer wallet before stack up"
    set +e
    import_out=$(bob wallet import \
        --instance obol-agent \
        --private-key-file <(printf '%s\n' "$BOB_PRIVATE_KEY") \
        --force 2>&1)
    rc=$?
    set -e
    echo "$import_out" | tail -8
    if [ "$rc" -ne 0 ]; then
        fail "Could not preseed Bob buyer wallet: ${import_out:0:300}"
        emit_metrics
        exit "$rc"
    fi

    existing=$(bob agent wallet address --runtime hermes obol-agent 2>/dev/null || true)
    if [ "$(lower_addr "$existing")" != "$(lower_addr "$BOB_WALLET")" ]; then
        fail "Bob preseeded wallet mismatch — metadata=$existing expected=$BOB_WALLET"
        emit_metrics
        exit 1
    fi
    pass "Bob wallet preseeded: $existing"
}

tunnel_hostname() {
    python3 - "$1" <<'PY'
from urllib.parse import urlparse
import sys

print(urlparse(sys.argv[1]).hostname or "")
PY
}

resolve_public_ipv4() {
    local host="$1"
    local ip=""
    local resolver

    ip=$(dig +short A "$host" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1 || true)
    if [ -n "$ip" ]; then
        printf '%s\n' "$ip"
        return 0
    fi

    for resolver in 1.1.1.1 8.8.8.8; do
        ip=$(dig @"$resolver" +short A "$host" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -1 || true)
        if [ -n "$ip" ]; then
            printf '%s\n' "$ip"
            return 0
        fi
    done

    return 1
}

system_resolves_host() {
    python3 - "$1" <<'PY'
import socket
import sys

try:
    socket.getaddrinfo(sys.argv[1], 443)
except OSError:
    sys.exit(1)
PY
}

curl_tunnel_402_code() {
    local url="$1"
    local host="$2"
    local ip="$3"

    if [ -n "$host" ] && [ -n "$ip" ] && ! system_resolves_host "$host"; then
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            --resolve "$host:443:$ip" -X POST "$url" \
            -H "Content-Type: application/json" \
            -d "{\"model\":\"$OBOL_LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":5}" 2>/dev/null || true
    else
        curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
            -X POST "$url" -H "Content-Type: application/json" \
            -d "{\"model\":\"$OBOL_LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":5}" 2>/dev/null || true
    fi
}

dual_stack_tunnel_dns_problem() {
    local message="$1"
    if [ "${DUAL_STACK_TUNNEL_DNS_WARN_ONLY:-false}" = "true" ]; then
        echo "  ! $message"
    else
        fail "$message"
    fi
}

ensure_bob_tunnel_dns() {
    local host="$1"
    local ip="$2"
    local nodehosts patch_file

    [ -n "$host" ] || return 0
    if [ -z "$ip" ]; then
        ip=$(resolve_public_ipv4 "$host" || true)
    fi
    if [ -z "$ip" ]; then
        echo "  ! Could not resolve public IPv4 for tunnel host $host; continuing without CoreDNS override"
        return 0
    fi

    step "Bob: tunnel DNS override"
    nodehosts=$(bob kubectl get configmap coredns -n kube-system -o jsonpath='{.data.NodeHosts}' 2>/dev/null || true)
    if [ -z "$nodehosts" ]; then
        dual_stack_tunnel_dns_problem "Could not read Bob CoreDNS NodeHosts"
        return 0
    fi
    if echo "$nodehosts" | grep -Fq "$host"; then
        pass "Bob CoreDNS NodeHosts already maps $host"
        return 0
    fi

    patch_file=$(mktemp)
    DUAL_STACK_NODEHOSTS="$nodehosts" DUAL_STACK_TUNNEL_HOST="$host" DUAL_STACK_TUNNEL_IP="$ip" \
        python3 - <<'PY' > "$patch_file"
import json
import os

nodehosts = os.environ["DUAL_STACK_NODEHOSTS"].rstrip()
host = os.environ["DUAL_STACK_TUNNEL_HOST"]
ip = os.environ["DUAL_STACK_TUNNEL_IP"]
nodehosts = f"{nodehosts}\n{ip} {host}\n"
print(json.dumps({"data": {"NodeHosts": nodehosts}}))
PY
    if bob kubectl patch configmap coredns -n kube-system --type merge --patch-file "$patch_file" >/dev/null 2>&1; then
        bob kubectl rollout restart deployment/coredns -n kube-system >/dev/null 2>&1 || true
        bob kubectl rollout status deployment/coredns -n kube-system --timeout=60s >/dev/null 2>&1 || true
        pass "Bob CoreDNS NodeHosts maps $host -> $ip"
    else
        dual_stack_tunnel_dns_problem "Could not patch Bob CoreDNS for $host"
    fi
    rm -f "$patch_file"
}

bob_tunnel_402_code() {
    local service_name="${DUAL_STACK_SERVICE_NAME:-alice-obol-inference}"

    bob kubectl exec -n "$BOB_AGENT_NS" "deploy/$BOB_AGENT_DEPLOY" -c "$BOB_AGENT_CONTAINER" -- \
        python3 -c "
import json, urllib.error, urllib.request
req = urllib.request.Request('$TUNNEL_URL/services/$service_name/v1/chat/completions',
    data=json.dumps({'model':'$OBOL_LLM_MODEL','messages':[{'role':'user','content':'hi'}],'max_tokens':5}).encode(),
    headers={'Content-Type':'application/json'})
try:
    resp = urllib.request.urlopen(req, timeout=20); print(resp.status)
except urllib.error.HTTPError as e:
    print(e.code)
except Exception as e:
    print('ERR: %s' % e)
" 2>/dev/null || true
}

purchase_request_status() {
    bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" --no-headers 2>&1 || true
}

buyer_sidecar_status() {
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, json
try:
    resp = urllib.request.urlopen('http://localhost:8402/status', timeout=5)
    d = json.loads(resp.read())
    for name, info in d.items():
        print('%s: remaining=%d spent=%d model=%s' % (name, info['remaining'], info['spent'], info['public_model']))
except Exception as e:
    print('error: %s' % e)
" 2>&1 || true
}

# Send the long single-shot buy prompt to Bob's agent. The prompt expands
# against the caller's environment (BOB_AGENT_PORT, BOB_TOKEN,
# BOB_AGENT_RUNTIME, BOB_OBOL_SKILLS_DIR, TUNNEL_URL, OBOL_LLM_MODEL).
_agent_buy_send_prompt() {
    local llm_payload_suffix
    llm_payload_suffix="$(llm_disable_thinking_payload_suffix)"

    curl -sf --max-time 300 \
        -X POST "http://localhost:${BOB_AGENT_PORT}/v1/chat/completions" \
        -H "Authorization: Bearer $BOB_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{
            \"model\": \"$BOB_AGENT_RUNTIME-agent\",
            \"messages\": [{
                \"role\": \"user\",
                \"content\": \"Use the buy-x402 skill and your terminal tool. Run exactly once: ERPC_URL=http://erpc.erpc.svc.cluster.local/rpc ERPC_NETWORK=base-sepolia python3 $BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py buy alice-obol --endpoint $TUNNEL_URL/services/alice-obol-inference/v1/chat/completions --model $OBOL_LLM_MODEL --count 5\"
            }],
            \"max_tokens\": 4000,
            \"stream\": false${llm_payload_suffix}
        }" 2>&1 || true
}

_agent_buy_pr_exists() {
    bob kubectl get purchaserequests.obol.org -n "$BOB_AGENT_NS" alice-obol \
        -o name 2>/dev/null | grep -q .
}

# 1-retry wrapper for the agent buy prompt at flow-13/14 step 46. The QA LLM
# (qwen36-deep, 27B-class — see OBOL_LLM_MODEL default) occasionally narrates a
# fabricated failure on the long single-shot buy prompt instead of actually
# invoking the bash tool. When that happens, no PurchaseRequest is created and
# step 47 fails with "PurchaseRequest CR not ready" — even though buy.py was
# never invoked. The smaller qwen36-fast (~4B) flakes much more often; deep is
# the new default for that reason. See plans/inference-v1337-followup-20260514.md.
#
# Strategy: poll for the PR for up to 60s after the first prompt; if absent,
# print a LOUD warning flagging this as agent unreliability and re-send the
# prompt once. If still absent after the retry, step 47 fails as before.
agent_buy_with_retry() {
    local response content retried=0 i

    response=$(_agent_buy_send_prompt)
    content=$(extract_assistant_content "$response" 2>/dev/null || true)
    echo "${content:0:500}"
    if [ -z "$(printf '%s' "$content" | tr -d '[:space:]')" ]; then
        echo "  ! Agent returned no final assistant text; confirming purchase via PurchaseRequest CR"
    fi
    if printf '%s' "$content" | agent_response_refused; then
        fail "Agent refused to run buy.py: ${content:0:500}"
        emit_metrics; exit 1
    fi

    # Wait up to 60s for the controller to reconcile the PR. Healthy runs see
    # it within ~5s; the long ceiling absorbs cluster-cold-start jitter.
    for i in $(seq 1 12); do
        _agent_buy_pr_exists && break
        sleep 5
    done

    if ! _agent_buy_pr_exists; then
        echo ""
        echo "  ╔════════════════════════════════════════════════════════════════════════╗"
        echo "  ║  WARN: agent did NOT create a PurchaseRequest after 60s.               ║"
        echo "  ║  Documented LLM flake on the long single-shot buy prompt — agent       ║"
        echo "  ║  narrated a fabricated failure instead of invoking buy.py.             ║"
        echo "  ║  Re-prompting ONCE.                                                    ║"
        echo "  ║  If this fires regularly: confirm OBOL_LLM_MODEL=qwen36-deep (default) ║"
        echo "  ║  not qwen36-fast (4B), or escalate to qwen36-35b-heretic, or add a     ║"
        echo "  ║  non-agent fallback path.                                              ║"
        echo "  ║  Ref: plans/inference-v1337-followup-20260514.md                       ║"
        echo "  ╚════════════════════════════════════════════════════════════════════════╝"
        echo ""
        retried=1
        response=$(_agent_buy_send_prompt)
        content=$(extract_assistant_content "$response" 2>/dev/null || true)
        echo "  RETRY response: ${content:0:500}"
        if printf '%s' "$content" | agent_response_refused; then
            fail "Agent refused to run buy.py on retry: ${content:0:500}"
            emit_metrics; exit 1
        fi
    fi

    pass "Agent buy prompt issued (retry=$retried; success will be confirmed by PurchaseRequest CR)"
}

extract_assistant_content() {
    DUAL_STACK_RESPONSE="$1" python3 - <<'PY'
import json
import os
import sys

try:
    data = json.loads(os.environ["DUAL_STACK_RESPONSE"])
    content = data["choices"][0]["message"].get("content", "")
    if isinstance(content, list):
        content = json.dumps(content)
    sys.stdout.write(content)
except Exception:
    sys.exit(1)
PY
}

bob_buy_skill_balance() {
    bob kubectl exec \
        -n "$BOB_AGENT_NS" "deploy/$BOB_AGENT_DEPLOY" -c "$BOB_AGENT_CONTAINER" -- \
        python3 "$BOB_OBOL_SKILLS_DIR/buy-x402/scripts/buy.py" balance 2>&1 || true
}

litellm_paid_inference() {
    bob kubectl exec -n llm deployment/litellm -c litellm -- \
        python3 -c "
import urllib.request, urllib.error, json, time
t0 = time.time()
req = urllib.request.Request('http://localhost:4000/v1/chat/completions',
    data=json.dumps({
        'model': '$PAID_MODEL',
        'messages': [
            {'role':'system','content':'Return only the final answer. Do not include reasoning, analysis, markdown, lists, or preambles.'},
            {'role':'user','content':'Reply with exactly this sentence: OBOL payment smoke test passed.'}
        ],
        'max_tokens': 60, 'temperature': 0, 'stream': False,
        'chat_template_kwargs': {'enable_thinking': False}
    }).encode(),
    headers={'Content-Type':'application/json','Authorization':'Bearer $BOB_MASTER_KEY'})
try:
    resp = urllib.request.urlopen(req, timeout=180)
    elapsed = time.time() - t0
    body = json.loads(resp.read())
    c = body['choices'][0]['message']
    content = ' '.join((c.get('content') or '').split())
    reasoning = c.get('reasoning_content') or c.get('reasoning') or ''
    print('STATUS=%d TIME=%.1fs' % (resp.status, elapsed))
    print('MODEL=%s' % body.get('model','?'))
    if reasoning:
        print('REASONING_PRESENT=1')
    print('CONTENT=%s' % content[:300])
except urllib.error.HTTPError as e:
    print('ERROR=%d %s' % (e.code, e.read().decode()[:300]))
except Exception as e:
    print('ERROR=%s' % repr(e))
" 2>&1 || true
}

wait_for_paid_inference() {
    local attempts="${1:-24}"
    local delay="${2:-5}"
    local transient_retries="${PAID_INFERENCE_TRANSIENT_RETRIES:-1}"
    local transient_seen=0
    local out=""
    local i

    for i in $(seq 1 "$attempts"); do
        out=$(litellm_paid_inference)
        if echo "$out" | grep -q "STATUS=200"; then
            printf '%s\n' "$out"
            return 0
        fi
        if echo "$out" | paid_inference_pending_error; then
            sleep "$delay"
            continue
        fi
        if echo "$out" | paid_inference_transient_error && [ "$transient_seen" -lt "$transient_retries" ]; then
            transient_seen=$((transient_seen + 1))
            echo "RETRY_TRANSIENT=${transient_seen}/${transient_retries}: paid inference hit transient timeout/error" >&2
            printf '%s\n' "$out" >&2
            sleep "$delay"
            continue
        fi
        printf '%s\n' "$out"
        return 1
    done

    printf '%s\n' "$out"
    return 1
}

# Pin a chain to a single eRPC upstream by mutating the eRPC ConfigMap. The
# structured YAML transform lives in Go so smoke flows do not gain ad hoc host
# dependencies such as Ruby.
pin_erpc_chain_single_upstream() {
    local runner="$1"   # alice | bob
    local chain_id="$2"
    local upstream_id="$3"

    local current
    current=$("$runner" kubectl get cm erpc-config -n erpc -o jsonpath='{.data.erpc\.yaml}' 2>/dev/null || true)
    if [ -z "$current" ]; then
        return 1
    fi

    local patched
    if ! patched=$(printf '%s' "$current" | \
        (cd "$OBOL_ROOT" && go run ./flows/tools/pin-erpc-upstream \
            --chain-id "$chain_id" --upstream-id "$upstream_id")); then
        return 1
    fi
    [ -n "$patched" ] || return 1

    local tmp rc
    tmp=$(mktemp)
    printf '%s' "$patched" > "$tmp"
    "$runner" kubectl create cm erpc-config -n erpc \
        --from-file=erpc.yaml="$tmp" --dry-run=client -o yaml | \
        "$runner" kubectl replace -f - >/dev/null 2>&1
    rc=$?
    rm -f "$tmp"
    "$runner" kubectl rollout restart deployment/erpc -n erpc >/dev/null 2>&1 || true
    "$runner" kubectl rollout status deployment/erpc -n erpc --timeout=60s >/dev/null 2>&1 || true
    return "$rc"
}
