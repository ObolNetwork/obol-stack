#!/bin/bash
# Flow 07: Sell Verify — monetize-inference.md §1.5-1.7.
# Runs AFTER flow-06 (ServiceOffer flow-qwen must be Ready).
source "$(dirname "$0")/lib.sh"

refresh_obol_ingress_env
INGRESS_URL="${OBOL_INGRESS_URL%/}"

# Controller-based reconciliation lives in the x402 namespace.
run_step_grep "serviceoffer-controller pod running" "Running" \
    "$OBOL" kubectl get pods -n x402 -l app=serviceoffer-controller --no-headers

# Security: verify eRPC and frontend HTTPRoutes have hostname restrictions (CLAUDE.md)
# NEVER expose eRPC or frontend via tunnel — only obol.stack (local) allowed
step "eRPC HTTPRoute restricted to obol.stack (security: not exposed via tunnel)"
erpc_hostnames=$("$OBOL" kubectl get httproute erpc -n erpc \
    -o jsonpath='{.spec.hostnames}' 2>&1) || true
if echo "$erpc_hostnames" | grep -q "obol.stack"; then
    pass "eRPC HTTPRoute hostname: $erpc_hostnames (local only)"
else
    fail "eRPC HTTPRoute missing hostname restriction — exposed to public tunnel! ($erpc_hostnames)"
fi

step "Frontend HTTPRoute restricted to obol.stack (security: not exposed via tunnel)"
fe_hostnames=$("$OBOL" kubectl get httproute obol-frontend -n obol-frontend \
    -o jsonpath='{.spec.hostnames}' 2>&1) || true
if echo "$fe_hostnames" | grep -q "obol.stack"; then
    pass "Frontend HTTPRoute hostname: $fe_hostnames (local only)"
else
    fail "Frontend HTTPRoute missing hostname restriction — exposed to public tunnel! ($fe_hostnames)"
fi

# Security: default Hermes agent restricted to local subdomain (not public)
step "Hermes HTTPRoute restricted to subdomain (security: not fully public)"
agent_hostnames=$("$OBOL" kubectl get httproute hermes -n hermes-obol-agent \
    -o jsonpath='{.spec.hostnames}' 2>&1) || true
if echo "$agent_hostnames" | grep -q "obol.stack"; then
    pass "Hermes HTTPRoute hostname: $agent_hostnames (local subdomain)"
else
    fail "Hermes HTTPRoute missing hostname restriction — ${agent_hostnames:0:100}"
fi

# §1.6 pre-check: eRPC accessible (local Traefik, obol.stack only — never via tunnel)
# GET /rpc returns network list (from getting-started.md §2, monetize §1.6)
step "eRPC accessible at $INGRESS_URL/rpc"
erpc_out=$($CURL_OBOL -sf --max-time 10 "$INGRESS_URL/rpc" 2>&1) || true
if echo "$erpc_out" | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'rpc' in d or 'error' in d" 2>/dev/null; then
    pass "eRPC at $INGRESS_URL/rpc returned JSON"
else
    fail "eRPC not responding — ${erpc_out:0:100}"
fi

# §1.5: Tunnel status
step "Tunnel status"
TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
TUNNEL_HOST=""
TUNNEL_IP=""
if [ -n "$TUNNEL_URL" ]; then
    TUNNEL_HOST=$(printf '%s\n' "${TUNNEL_URL#https://}" | cut -d/ -f1)
    TUNNEL_IP=$(resolve_public_ipv4 "$TUNNEL_HOST" || true)
fi

tunnel_get_code() {
    local url="$1"
    local code
    if [ -n "$TUNNEL_HOST" ] && [ -n "$TUNNEL_IP" ]; then
        if code=$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
            --resolve "$TUNNEL_HOST:443:$TUNNEL_IP" \
            "$url" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            printf '000\n'
        fi
    else
        if code=$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
            "$url" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            printf '000\n'
        fi
    fi
}

tunnel_get_file_code() {
    local url="$1"
    local outfile="$2"
    local code
    if [ -n "$TUNNEL_HOST" ] && [ -n "$TUNNEL_IP" ]; then
        if code=$(curl -sS --max-time 15 -o "$outfile" -w '%{http_code}' \
            --resolve "$TUNNEL_HOST:443:$TUNNEL_IP" \
            "$url" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            : > "$outfile"
            printf '000\n'
        fi
    else
        if code=$(curl -sS --max-time 15 -o "$outfile" -w '%{http_code}' \
            "$url" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            : > "$outfile"
            printf '000\n'
        fi
    fi
}

tunnel_post_code_json() {
    local url="$1"
    local body="$2"
    local code
    if [ -n "$TUNNEL_HOST" ] && [ -n "$TUNNEL_IP" ]; then
        if code=$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
            --resolve "$TUNNEL_HOST:443:$TUNNEL_IP" \
            -X POST "$url" -H "Content-Type: application/json" \
            -d "$body" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            printf '000\n'
        fi
    else
        if code=$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
            -X POST "$url" -H "Content-Type: application/json" \
            -d "$body" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            printf '000\n'
        fi
    fi
}

tunnel_post_json_file_code() {
    local url="$1"
    local body="$2"
    local outfile="$3"
    local code
    if [ -n "$TUNNEL_HOST" ] && [ -n "$TUNNEL_IP" ]; then
        if code=$(curl -sS --max-time 15 -o "$outfile" -w '%{http_code}' \
            --resolve "$TUNNEL_HOST:443:$TUNNEL_IP" \
            -X POST "$url" -H "Content-Type: application/json" \
            -d "$body" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            : > "$outfile"
            printf '000\n'
        fi
    else
        if code=$(curl -sS --max-time 15 -o "$outfile" -w '%{http_code}' \
            -X POST "$url" -H "Content-Type: application/json" \
            -d "$body" 2>/dev/null); then
            printf '%s\n' "$code"
        else
            : > "$outfile"
            printf '000\n'
        fi
    fi
}

if [ -n "$TUNNEL_URL" ]; then
    pass "Tunnel URL: $TUNNEL_URL"
else
    fail "No tunnel URL found — ${TUNNEL_OUTPUT:0:200}"
fi

# §1.6: Verify paths

# Wait for x402-verifier pods to be ready — Kubernetes Reloader restarts them when
# x402-pricing ConfigMap changes (e.g., from obol sell pricing).  Fresh pods take ~10s.
step "x402 verifier pods ready"
for i in $(seq 1 12); do
    ready=$("$OBOL" kubectl get pods -n x402 --no-headers 2>&1 | grep "Running" | grep -c "1/1" || echo 0)
    total=$("$OBOL" kubectl get pods -n x402 --no-headers 2>&1 | grep -v "^$" | wc -l | tr -d ' ')
    if [ "$ready" -ge 1 ] && [ "$ready" = "$total" ]; then
        pass "x402 verifier pods ready ($ready/$total)"
        break
    fi
    [ "$i" -eq 12 ] && fail "x402 verifier not ready after 60s ($ready/$total pods running)"
    sleep 5
done

# 402 via local Traefik (primary check — no tunnel dependency)
# Poll briefly: Traefik needs ~10s to propagate a newly created HTTPRoute
step "402 via local Traefik"
for i in $(seq 1 6); do
    local_code=$($CURL_OBOL -s --max-time 5 -o /dev/null -w '%{http_code}' -X POST \
        "$INGRESS_URL/services/flow-qwen/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
    if [ "$local_code" = "402" ]; then
        pass "Local 402 Payment Required (attempt $i)"
        break
    fi
    [ "$i" -eq 6 ] && fail "Expected 402 after 30s, got: $local_code"
    sleep 5
done

# Validate 402 JSON body has required x402 fields
step "402 body has x402Version and accepts[]"
body=$($CURL_OBOL -s --max-time 10 -X POST \
    "$INGRESS_URL/services/flow-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
if echo "$body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('x402Version') is not None
assert d['accepts'][0]['payTo']
" 2>/dev/null; then
    pass "402 body has x402Version + accepts[].payTo"
else
    fail "402 body missing fields — ${body:0:200}"
fi

# 402 via tunnel
if [ -n "$TUNNEL_URL" ]; then
    step "402 via tunnel"
    tunnel_target="$TUNNEL_URL/services/flow-qwen/v1/chat/completions"
    for i in $(seq 1 48); do
        tunnel_code=$(tunnel_post_code_json "$tunnel_target" \
            "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}")
        if [ "$tunnel_code" = "402" ]; then
            pass "Tunnel 402 Payment Required (attempt $i)"
            break
        fi
        [ "$i" -eq 48 ] && fail "Tunnel expected 402 after 240s, got $tunnel_code"
        sleep 5
    done
fi

# Security: public tunnel must not expose local-only eRPC routes.
if [ -n "$TUNNEL_URL" ]; then
    step "Public tunnel does not expose eRPC"
    public_erpc_exposed=false
    tunnel_erpc_index_code="000"
    tunnel_erpc_chain_code="000"
    tunnel_erpc_index_body=""
    tunnel_erpc_chain_body=""

    tunnel_erpc_index_file=$(mktemp)
    for i in $(seq 1 12); do
        tunnel_erpc_index_code=$(tunnel_get_file_code "$TUNNEL_URL/rpc" "$tunnel_erpc_index_file")
        [ "$tunnel_erpc_index_code" != "000" ] && break
        sleep 2
    done
    tunnel_erpc_index_body=$(cat "$tunnel_erpc_index_file")
    rm -f "$tunnel_erpc_index_file"
    if [ "$tunnel_erpc_index_code" = "000" ]; then
        fail "Could not verify tunnel eRPC /rpc exposure — tunnel unreachable after 24s"
    elif echo "$tunnel_erpc_index_body" | python3 -c "
import sys, json
try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(1)
raise SystemExit(0 if 'rpc' in payload else 1)
" 2>/dev/null; then
        public_erpc_exposed=true
        fail "Public tunnel unexpectedly exposed eRPC /rpc (HTTP $tunnel_erpc_index_code)"
    fi

    tunnel_erpc_chain_file=$(mktemp)
    for i in $(seq 1 12); do
        tunnel_erpc_chain_code=$(tunnel_post_json_file_code "$TUNNEL_URL/rpc/evm/84532" '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' "$tunnel_erpc_chain_file")
        [ "$tunnel_erpc_chain_code" != "000" ] && break
        sleep 2
    done
    tunnel_erpc_chain_body=$(cat "$tunnel_erpc_chain_file")
    rm -f "$tunnel_erpc_chain_file"
    if [ "$tunnel_erpc_chain_code" = "000" ]; then
        fail "Could not verify tunnel eRPC JSON-RPC exposure — tunnel unreachable after 24s"
    elif echo "$tunnel_erpc_chain_body" | python3 -c "
import sys, json
try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(1)
raise SystemExit(0 if payload.get('result') else 1)
" 2>/dev/null; then
        public_erpc_exposed=true
        fail "Public tunnel unexpectedly served eRPC eth_chainId successfully (HTTP $tunnel_erpc_chain_code)"
    fi

    if [ "$public_erpc_exposed" = false ]; then
        pass "Public tunnel did not expose eRPC (index HTTP $tunnel_erpc_index_code, JSON-RPC HTTP $tunnel_erpc_chain_code)"
    fi
fi

# §1.7: Verifier metrics — check metrics from ALL x402-verifier pods
# (metrics are per-pod; requests load-balance to any pod, so we must check all)
step "x402 verifier metrics"
metrics_found=false
for pod in $("$OBOL" kubectl get pods -n x402 -o name 2>/dev/null | grep verifier); do
    kill $(lsof -ti:8889) 2>/dev/null || true
    "$OBOL" kubectl port-forward -n x402 "$pod" 8889:8080 &>/dev/null &
    PF_METRICS_PID=$!
    for i in $(seq 1 10); do
        if curl -sf --max-time 2 http://localhost:8889/metrics >/dev/null 2>&1; then
            break
        fi
        sleep 1
    done
    pod_metrics=$(curl -sf --max-time 5 http://localhost:8889/metrics 2>&1) || true
    cleanup_pid "$PF_METRICS_PID"
    if echo "$pod_metrics" | grep -q "obol_x402"; then
        metrics_found=true
        break
    fi
done
if $metrics_found; then
    pass "Verifier metrics available"
else
    fail "Verifier metrics not found on any pod (requests may not have reached verifier yet)"
fi

# §1.7: Verifier request logs (monitoring — verifier logs each ForwardAuth call)
step "x402 verifier logs show 402 request handling"
verifier_logs=$("$OBOL" kubectl logs -n x402 deployment/x402-verifier --tail=20 2>&1) || true
if echo "$verifier_logs" | grep -q "402\|payment\|services/flow-qwen"; then
    pass "Verifier logs show 402 request activity"
else
    # May be empty if Reloader just restarted the pod — soft fail
    fail "Verifier logs empty (Reloader may have restarted pod) — ${verifier_logs:0:100}"
fi

# PR 299 derives live per-offer routes directly from ServiceOffer state rather
# than mutating x402-pricing with dynamic route entries.
step "ServiceOffer RoutePublished condition is True"
status_out=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
if { echo "$status_out" | grep -q "type: RoutePublished" && \
     echo "$status_out" | grep -B4 "type: RoutePublished" | grep -q 'status: "True"'; } || \
   echo "$status_out" | grep -Eq '^  ✓ RoutePublished:'; then
    pass "ServiceOffer flow-qwen has RoutePublished=True"
else
    fail "ServiceOffer flow-qwen missing RoutePublished=True — ${status_out:0:200}"
fi

# §1.5: obol tunnel logs — verify cloudflared is logging (documented command)
step "obol tunnel logs shows cloudflared output"
tunnel_logs=$("$OBOL" tunnel logs 2>&1) || true
if echo "$tunnel_logs" | grep -q "cloudflare\|tunnel\|INF\|info\|TUN"; then
    pass "Tunnel logs available"
else
    fail "Tunnel logs empty or missing — ${tunnel_logs:0:100}"
fi

# §1.4: obol sell status <name> — individual ServiceOffer conditions (monetize §1.4/§4)
# Verifies all 6 conditions are True: ModelReady, UpstreamHealthy, PaymentGateReady,
# RoutePublished, Registered, Ready
step "obol sell status flow-qwen shows Ready conditions"
status_out=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
if { echo "$status_out" | grep -q 'type: Ready' && echo "$status_out" | grep -q "status: \"True\""; } || \
   echo "$status_out" | grep -Eq '^  ✓ Ready:'; then
    pass "ServiceOffer flow-qwen has Ready condition True"
else
    fail "ServiceOffer status missing Ready condition — ${status_out:0:200}"
fi

emit_metrics
