#!/bin/bash
# Flow 16: Sell Agent — agent-backed ServiceOffer metadata smoke.
#
# Steps:
#   1. Declare an Agent CRD via `obol agent new <name>` (host seeds SOUL.md
#      + skills, applies the CR; controller provisions Hermes + optional
#      remote-signer in the agent's namespace)
#   2. Gate it with `obol sell agent <name>` (creates a ServiceOffer of
#      type=agent referencing the Agent)
#   3. Probe → expect 402 with extra.agentModel + extra.agentSkills
#   4. Leave paid-call/settlement coverage to flow-08 until this scenario's
#      commerce path is promoted into CI as a first-class smoke.
#
# Requires: flows 01-04 (stack + master agent + LLM), 10 (anvil + facilitator).
source "$(dirname "$0")/lib.sh"

AGENT_NAME="${FLOW_AGENT_NAME:-quant-test}"
AGENT_NS="agent-${AGENT_NAME}"
OFFER_NAME="$AGENT_NAME"
AGENT_SKILLS="${FLOW_AGENT_SKILLS:-ethereum-networks,ethereum-local-wallet,addresses,gas}"
AGENT_MODEL="${FLOW_AGENT_MODEL:-${FLOW_MODEL:-qwen3.5:9b}}"
OBJECTIVE="${FLOW_AGENT_OBJECTIVE:-You answer questions about EVM chain state using the tools you have. Be concise.}"

# §1: Declare the Agent
step "obol agent new $AGENT_NAME"
new_out=$("$OBOL" agent new "$AGENT_NAME" \
    --model "$AGENT_MODEL" \
    --skills "$AGENT_SKILLS" \
    --create-wallet \
    --objective "$OBJECTIVE" 2>&1) || true
if echo "$new_out" | grep -qE "Agent .*$AGENT_NAME (created|updated)"; then
    pass "Agent $AGENT_NAME declared"
else
    fail "obol agent new failed — ${new_out:0:300}"
fi

# §1.1: Host-side seed landed
step "Host data dir contains SOUL.md and skills"
host_root="${OBOL_DATA_DIR}/${AGENT_NS}/hermes-data/.hermes"
if [ -f "$host_root/SOUL.md" ]; then
    pass "SOUL.md seeded at $host_root/SOUL.md"
else
    fail "SOUL.md missing at $host_root/SOUL.md"
fi
expected=$(echo "$AGENT_SKILLS" | tr ',' '\n' | sort | tr '\n' ',' | sed 's/,$//')
got=$(ls "$host_root/obol-skills" 2>/dev/null | sort | tr '\n' ',' | sed 's/,$//' || true)
if [ "$got" = "$expected" ]; then
    pass "Skills dir = $got"
else
    fail "Skills mismatch: got=$got want=$expected"
fi

# §1.2: Agent CR observable
step "kubectl get agent $AGENT_NAME -n $AGENT_NS"
phase=$("$OBOL" kubectl get agent "$AGENT_NAME" -n "$AGENT_NS" \
    -o jsonpath='{.status.phase}' 2>/dev/null || true)
case "$phase" in
    Ready)
        pass "Agent Phase=Ready (controller has provisioned)"
        ;;
    Provisioning|Pending|"")
        pass "Agent observed (phase=${phase:-pending}, controller has not yet provisioned — GATED_ON_2D)"
        ;;
    Failed)
        fail "Agent Phase=Failed — see status.conditions[Validated]"
        ;;
    *)
        fail "Unexpected agent phase: $phase"
        ;;
esac

# §1.3: Hermes pod check
step "Hermes pod running in $AGENT_NS"
pod_phase=""
for i in $(seq 1 30); do
    pod_phase=$("$OBOL" kubectl get pods -n "$AGENT_NS" -l app.kubernetes.io/name=hermes \
        -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)
    [ "$pod_phase" = "Running" ] && break
    sleep 4
done
if [ "$pod_phase" = "Running" ]; then
    pass "Hermes pod Running"
else
    fail "Hermes pod did not reach Running within 120s (phase=$pod_phase)"
fi

# §1.4: Remote-signer pod (only when wallet was requested)
step "Remote-signer pod running in $AGENT_NS"
signer_phase=""
for i in $(seq 1 30); do
    signer_phase=$("$OBOL" kubectl get pods -n "$AGENT_NS" -l app.kubernetes.io/name=remote-signer \
        -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)
    [ "$signer_phase" = "Running" ] && break
    sleep 4
done
if [ "$signer_phase" = "Running" ]; then
    pass "Remote-signer pod Running"
else
    fail "Remote-signer pod did not reach Running within 120s (phase=$signer_phase)"
fi

# §1.5: status.walletAddress populated
step "Agent status.walletAddress set"
wallet=$("$OBOL" kubectl get agent "$AGENT_NAME" -n "$AGENT_NS" \
    -o jsonpath='{.status.walletAddress}' 2>/dev/null || true)
if [[ "$wallet" =~ ^0x[0-9a-fA-F]{40}$ ]]; then
    pass "walletAddress = $wallet"
else
    fail "walletAddress missing or malformed: $wallet"
fi

# §2: Create the ServiceOffer
step "obol sell agent $AGENT_NAME"
sell_out=$("$OBOL" sell agent "$AGENT_NAME" \
    --price "0.001" \
    --token "USDC" \
    --chain "base-sepolia" 2>&1) || true
if echo "$sell_out" | grep -qE "ServiceOffer .*$OFFER_NAME (created|updated)"; then
    pass "ServiceOffer published"
else
    fail "obol sell agent failed — ${sell_out:0:300}"
fi

# §2.1: ServiceOffer.spec.agent.ref present
step "ServiceOffer carries spec.agent.ref"
ref_name=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$AGENT_NS" \
    -o jsonpath='{.spec.agent.ref.name}' 2>/dev/null || true)
ref_ns=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$AGENT_NS" \
    -o jsonpath='{.spec.agent.ref.namespace}' 2>/dev/null || true)
if [ "$ref_name" = "$AGENT_NAME" ] && [ "$ref_ns" = "$AGENT_NS" ]; then
    pass "spec.agent.ref → $ref_ns/$ref_name"
else
    fail "spec.agent.ref unexpected: name=$ref_name ns=$ref_ns"
fi

# §2.2: ServiceOffer Ready
step "ServiceOffer $OFFER_NAME reaches Ready"
ready=""
for i in $(seq 1 60); do
    out=$("$OBOL" sell status "$OFFER_NAME" -n "$AGENT_NS" 2>&1 || true)
    if echo "$out" | grep -q "Ready: True\|Ready=True"; then
        ready="yes"
        break
    fi
    sleep 2
done
if [ -n "$ready" ]; then
    pass "ServiceOffer Ready"
else
    fail "ServiceOffer did not reach Ready within 120s"
fi

# §3: Probe surfaces agent metadata in 402 extra (GATED_ON_2D — needs route)
refresh_obol_ingress_env
BASE_URL="${OBOL_INGRESS_URL%/}"
if [[ "$BASE_URL" == *"obol.stack"* ]]; then
    CURL_BASE="$CURL_OBOL"
else
    CURL_BASE="curl"
fi

step "402 response carries agentModel + agentSkills"
body_402=$($CURL_BASE -s --max-time 10 -X POST \
    "$BASE_URL/services/$OFFER_NAME/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"agent/$OFFER_NAME\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}]}" 2>&1) || true
if echo "$body_402" | python3 -c "
import sys, json
d = json.load(sys.stdin)
extra = d['accepts'][0].get('extra') or {}
assert extra.get('agentModel'), 'agentModel missing'
assert extra.get('agentSkills'), 'agentSkills missing'
print(f\"OK: model={extra['agentModel']} skills={','.join(extra['agentSkills'])}\")
" 2>&1 | grep -q "^OK:"; then
    pass "402 extra surfaces agent metadata"
else
    fail "402 missing agent metadata — ${body_402:0:300}"
fi

# §4: Paid path intentionally deferred.
# flow-08 already exercises the paid request + settlement loop; this smoke
# stops at agent declaration, route publication, and 402 metadata exposure.
step "Paid chat-completions path deferred to flow-08"
pass "Paid path coverage remains in flow-08-buy.sh until agent-commerce smoke is promoted into CI"

# §5: Cleanup — opt-in so a successful run leaves the agent for inspection.
if [ "${FLOW_CLEANUP:-0}" = "1" ]; then
    "$OBOL" sell delete "$OFFER_NAME" -n "$AGENT_NS" >/dev/null 2>&1 || true
    "$OBOL" agent delete --force "$AGENT_NAME" >/dev/null 2>&1 || true
fi

summary
