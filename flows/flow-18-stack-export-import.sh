#!/bin/bash
# Flow 18: Stack export / import round-trip — the persistence recovery path.
#
# Covers the orchestration the unit tests cannot reach (export quiesce +
# cluster harvest, host restore, and the stack-up replay ordering): seed
# recoverable state, export it, PURGE the stack, import it back, bring the
# stack up, and assert every seeded resource returned.
#
#   1. Seed record-on-write state: a recorded remote RPC + a preferred model
#      (+ an Agent CR when FLOW_INCLUDE_AGENT=1).
#   2. `obol stack export` → archive with manifest + config/cluster components.
#   3. `obol stack purge --force` → config + cluster gone.
#   4. `obol stack import <archive>` → host state restored.
#   5. `obol stack up` → ReconcileRecordedRPCs + ResumeAll + model reconcile.
#   6. `obol stack import <archive> --cluster-only` → re-apply etcd resources.
#   7. Assert the recorded model is back at the model_list head and the RPC
#      upstream returned (+ the Agent CR when seeded).
#
# DESTRUCTIVE + TERMINAL: this flow purges and recreates the cluster, so it is
# NOT part of the baseline release-smoke array (it would tear the shared
# cluster out from under the other flows, and it is port-exclusive with them).
# Run it standalone against a dedicated workspace:
#
#   export OBOL_DEVELOPMENT=true OBOL_NONINTERACTIVE=true
#   export OBOL_CONFIG_DIR=$PWD/.workspace/config \
#          OBOL_BIN_DIR=$PWD/.workspace/bin \
#          OBOL_DATA_DIR=$PWD/.workspace/data
#   go build -o "$OBOL_BIN_DIR/obol" ./cmd/obol
#   "$OBOL_BIN_DIR/obol" stack init && "$OBOL_BIN_DIR/obol" stack up
#   OBOL=$OBOL_BIN_DIR/obol bash flows/flow-18-stack-export-import.sh
#
# NOTE: not yet exercised end-to-end in CI — validate one live run before
# relying on it as a gate.
source "$(dirname "$0")/lib.sh"

export OBOL_NONINTERACTIVE=true

ARCHIVE="${FLOW_EXPORT_ARCHIVE:-${OBOL_ROOT:-$PWD}/.tmp/flow-18-export-$$.tar.gz}"
mkdir -p "$(dirname "$ARCHIVE")"
RPC_CHAIN="${FLOW_RPC_CHAIN:-8453}"
RPC_ENDPOINT="${FLOW_RPC_ENDPOINT:-https://mainnet.base.org}"

cleanup_archive() { rm -f "$ARCHIVE" 2>/dev/null || true; }
trap cleanup_archive EXIT

assert_stack_up() {
    "$OBOL" kubectl get ns x402 >/dev/null 2>&1
}

# §0: Precondition — a stack must already be up.
step "Stack is up before export"
if assert_stack_up; then
    pass "Cluster reachable"
else
    fail "No running stack — run 'obol stack init && obol stack up' first (see header)"
    emit_metrics
    exit 0
fi

# §1: Seed a recorded remote RPC (record-on-write → recorded-upstreams.yaml).
step "Seed recorded RPC: obol network add $RPC_CHAIN --endpoint"
add_out=$("$OBOL" network add "$RPC_CHAIN" --endpoint "$RPC_ENDPOINT" 2>&1) || true
if [ -f "$OBOL_CONFIG_DIR/rpc/recorded-upstreams.yaml" ]; then
    pass "recorded-upstreams.yaml written"
else
    fail "network add did not record host-side upstream — ${add_out:0:200}"
fi

# §2: Seed a preferred model (record-on-write → recorded-models.yaml).
SEED_MODEL="${FLOW_MODEL:-}"
if [ -z "$SEED_MODEL" ]; then
    SEED_MODEL=$("$OBOL" model list 2>/dev/null | grep -vE 'paid/|^NAME|^$' | head -1 | awk '{print $1}' || true)
fi
step "Seed preferred model: obol model prefer $SEED_MODEL"
if [ -z "$SEED_MODEL" ]; then
    skip "No non-paid model available to prefer — model record path not seeded"
else
    prefer_out=$("$OBOL" model prefer "$SEED_MODEL" 2>&1) || true
    if [ -f "$OBOL_CONFIG_DIR/llm/recorded-models.yaml" ]; then
        pass "recorded-models.yaml written (head=$SEED_MODEL)"
    else
        fail "model prefer did not record host-side model state — ${prefer_out:0:200}"
    fi
fi

# §2a: Optional Agent CR seed (heavy; opt-in).
AGENT_NAME="${FLOW_AGENT_NAME:-flow18-agent}"
if [ "${FLOW_INCLUDE_AGENT:-0}" = "1" ]; then
    step "Seed Agent CR: obol agent new $AGENT_NAME"
    agent_out=$("$OBOL" agent new "$AGENT_NAME" --model "$SEED_MODEL" --objective "flow-18 seed" 2>&1) || true
    if [ -f "$OBOL_CONFIG_DIR/agents/$AGENT_NAME.yaml" ]; then
        pass "agents/$AGENT_NAME.yaml persisted"
    else
        fail "agent new did not persist a manifest — ${agent_out:0:200}"
    fi
fi

# §3: Export.
step "obol stack export --output $ARCHIVE"
export_out=$("$OBOL" stack export --output "$ARCHIVE" --passphrase "" 2>&1) || true
if [ -f "$ARCHIVE" ]; then
    pass "archive created ($(du -h "$ARCHIVE" 2>/dev/null | awk '{print $1}'))"
else
    fail "export produced no archive — ${export_out:0:300}"
    emit_metrics
    exit 0
fi

step "Archive carries manifest + config + cluster components"
listing=$(tar tzf "$ARCHIVE" 2>/dev/null || true)
have_manifest=$(echo "$listing" | grep -cE '^manifest\.json$' || true)
have_config=$(echo "$listing" | grep -cE '^config/' || true)
have_cluster=$(echo "$listing" | grep -cE '^cluster/' || true)
if [ "$have_manifest" -ge 1 ] && [ "$have_config" -ge 1 ] && [ "$have_cluster" -ge 1 ]; then
    pass "manifest+config+cluster present (cluster entries: $have_cluster)"
else
    fail "archive missing components: manifest=$have_manifest config=$have_config cluster=$have_cluster"
fi

step "Archive is 0600 (holds keystore passwords + provider keys)"
mode=$(stat -c '%a' "$ARCHIVE" 2>/dev/null || stat -f '%Lp' "$ARCHIVE" 2>/dev/null || echo "?")
if [ "$mode" = "600" ]; then
    pass "archive mode 0600"
else
    fail "archive mode is $mode, want 0600"
fi

# §4: Purge.
step "obol stack purge --force --yes"
"$OBOL" stack purge --force --yes >/dev/null 2>&1 || true
if [ ! -f "$OBOL_CONFIG_DIR/.stack-id" ] && ! assert_stack_up; then
    pass "config + cluster gone"
else
    fail "purge left config or cluster behind"
fi

# §5: Import host state.
step "obol stack import $ARCHIVE --force (host restore)"
import_out=$("$OBOL" stack import "$ARCHIVE" --force 2>&1) || true
restored=0
[ -f "$OBOL_CONFIG_DIR/rpc/recorded-upstreams.yaml" ] && restored=$((restored + 1))
[ -f "$OBOL_CONFIG_DIR/.stack-id" ] && restored=$((restored + 1))
if [ "$restored" -ge 2 ]; then
    pass "host state restored (.stack-id + recorded-upstreams.yaml back)"
else
    fail "host restore incomplete — ${import_out:0:300}"
fi

# §6: Bring the stack up (replays RPCs + agents + model reconcile).
step "obol stack up (replay recorded state)"
if run_with_timeout 600 "$OBOL" stack up >/dev/null 2>&1; then
    pass "stack up completed"
else
    fail "stack up failed after import"
    emit_metrics
    exit 0
fi

# §7: Re-apply etcd-resident resources.
step "obol stack import $ARCHIVE --cluster-only"
"$OBOL" stack import "$ARCHIVE" --cluster-only >/dev/null 2>&1 || true
pass "cluster-only re-apply invoked"

# §8: Assert recorded state returned.
# poll_step_grep prints its own STEP/PASS/FAIL and always returns 0.
poll_step_grep "Recorded RPC $RPC_CHAIN returned in network list" "$RPC_CHAIN" 12 5 \
    "$OBOL" network list

if [ -n "$SEED_MODEL" ]; then
    step "Preferred model is back at the model_list head"
    head_model=$("$OBOL" model list 2>/dev/null | grep -vE 'paid/|^NAME|^$' | head -1 | awk '{print $1}' || true)
    if [ "$head_model" = "$SEED_MODEL" ]; then
        pass "model_list head = $SEED_MODEL (operator intent reconciled)"
    else
        fail "model_list head = ${head_model:-none}, want $SEED_MODEL"
    fi
fi

if [ "${FLOW_INCLUDE_AGENT:-0}" = "1" ]; then
    poll_step_grep "Agent $AGENT_NAME CR replayed into cluster" "$AGENT_NAME" 18 5 \
        "$OBOL" kubectl get agents.obol.org -A
fi

# §9: Teardown unless asked to keep.
if [ "${FLOW_KEEP:-0}" != "1" ]; then
    "$OBOL" stack purge --force --yes >/dev/null 2>&1 || true
fi

emit_metrics
