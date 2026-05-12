#!/bin/bash
# Release smoke runner.
#
# Runs the documented black-box flow scripts in release order, preserves logs,
# treats any logged FAIL as a failed flow, and cleans test stacks on exit.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=flows/lib.sh
source "$SCRIPT_DIR/lib.sh"

RUN_ID="${RELEASE_SMOKE_RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
ARTIFACT_DIR="${RELEASE_SMOKE_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/release-smoke-$RUN_ID}"
REPORT="$ARTIFACT_DIR/RELEASE_REPORT.md"
mkdir -p "$ARTIFACT_DIR" "$OBOL_BIN_DIR" "$OBOL_CONFIG_DIR" "$OBOL_DATA_DIR"

cleanup_stacks() {
    if [ "${RELEASE_SMOKE_KEEP_STACKS:-false}" = "true" ]; then
        return 0
    fi

    local workspace
    for workspace in "$OBOL_ROOT/.workspace" "$OBOL_ROOT/.workspace-alice" "$OBOL_ROOT/.workspace-bob"; do
        reset_flow_workspace "$workspace"
    done
}
trap cleanup_stacks EXIT

write_report_header() {
    cat > "$REPORT" <<EOF
# Obol Stack Release Smoke Report

Date: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
Commit: $(git -C "$OBOL_ROOT" rev-parse HEAD)
Artifacts: $ARTIFACT_DIR

| Flow | Result | FAIL lines | SKIP lines | Exit code |
| --- | --- | ---: | ---: | ---: |
EOF
}

append_report_row() {
    local flow="$1"
    local result="$2"
    local fail_count="$3"
    local skip_count="$4"
    local rc="$5"
    printf '| `%s` | %s | %s | %s | %s |\n' "$flow" "$result" "$fail_count" "$skip_count" "$rc" >> "$REPORT"
}

append_report_footer() {
    cat >> "$REPORT" <<EOF

## Notes

- The runner uses the real \`obol\` CLI and the flow scripts as black-box release checks.
- Any \`FAIL:\` line is release-gating, even when a child script exits zero.
- A \`SKIP:\` line records an intentionally optional prerequisite path and does not count as release-gating.
- \`flow-11-dual-stack.sh\` writes on-chain receipt artifacts under \`$ARTIFACT_DIR/flow-11-receipts\`.
- Set \`RELEASE_SMOKE_INCLUDE_OBOL=true\` to run \`flow-14-live-obol-base-sepolia.sh\`.
- Set \`RELEASE_SMOKE_INCLUDE_OBOL_FORK=true\` to run \`flow-13-dual-stack-obol.sh\`.
EOF
}

prepare_workspace() {
    echo "==> Building obol"
    local tmp_obol="$OBOL.tmp"
    rm -f "$tmp_obol"
    (cd "$OBOL_ROOT" && go build -o "$tmp_obol" ./cmd/obol)
    chmod +x "$tmp_obol"
    mv "$tmp_obol" "$OBOL"

    local tool src
    for tool in kubectl helm helmfile k3d k9s openclaw; do
        src=$(command -v "$tool" 2>/dev/null || true)
        [ -n "$src" ] && ln -sf "$src" "$OBOL_BIN_DIR/$tool"
    done
    for tool in "$OBOL_BIN_DIR/kubectl" "$OBOL_BIN_DIR/helm" "$OBOL_BIN_DIR/helmfile" "$OBOL_BIN_DIR/k3d" "$OBOL_BIN_DIR/openclaw"; do
        if [ ! -x "$tool" ]; then
            echo "Missing required tool: $tool" >&2
            return 1
        fi
    done

    echo "==> Ensuring Python payment dependencies"
    ensure_payment_python_deps
}

# run_flow <flow_path> [env_name env_value]
# Optional env_name/env_value is exported only for the child flow process
# (used to pin per-flow receipt dirs without polluting the runner's env).
run_flow() {
    local flow="$1"
    local env_name="${2:-}"
    local env_value="${3:-}"
    local name log rc fail_count skip_count result
    name=$(basename "$flow" .sh)
    log="$ARTIFACT_DIR/$name.log"

    echo
    echo "===== START $name ====="
    set +e
    if [ -n "$env_name" ]; then
        env "$env_name=$env_value" bash "$flow" 2>&1 | tee "$log"
    else
        bash "$flow" 2>&1 | tee "$log"
    fi
    rc=${PIPESTATUS[0]}
    set -e

    fail_count=$(grep -c '^FAIL:' "$log" 2>/dev/null || true)
    skip_count=$(grep -c '^SKIP:' "$log" 2>/dev/null || true)
    fail_count=${fail_count:-0}
    skip_count=${skip_count:-0}
    if [ "$rc" -eq 0 ] && [ "$fail_count" -eq 0 ]; then
        if [ "$skip_count" -gt 0 ]; then
            result="SKIP"
        else
            result="PASS"
        fi
    else
        result="FAIL"
    fi
    append_report_row "$name" "$result" "$fail_count" "$skip_count" "$rc"
    echo "===== END $name result=$result rc=$rc fails=$fail_count skips=$skip_count ====="

    [ "$result" != "FAIL" ]
}

# Guard: flow-11, flow-14, and flow-13 each enforce a preflight that rejects
# the run unless OBOL_LLM_ENDPOINT points at an OpenAI-compatible vLLM /
# llama.cpp endpoint. Without this guard the runner spends ~30 min on the
# baseline flows before those three FAIL on their preflights, which makes
# release-smoke results look worse than they are. Fail fast instead.
require_obol_llm_endpoint() {
    local need=false
    [ "${RELEASE_SMOKE_INCLUDE_OBOL:-false}" = "true" ]      && need=true
    [ "${RELEASE_SMOKE_INCLUDE_OBOL_FORK:-false}" = "true" ] && need=true
    [ "$need" = "true" ] || return 0

    if [ -z "${OBOL_LLM_ENDPOINT:-}" ]; then
        cat >&2 <<EOF
release-smoke: OBOL_LLM_ENDPOINT must be set when RELEASE_SMOKE_INCLUDE_OBOL=true
               or RELEASE_SMOKE_INCLUDE_OBOL_FORK=true.

  flow-11 / flow-14 / flow-13 enforce this preflight themselves — running
  them without OBOL_LLM_ENDPOINT only produces three guaranteed FAILs.

  Set, for example:
    export OBOL_LLM_ENDPOINT=http://127.0.0.1:8000/v1
    export OBOL_LLM_MODEL=qwen36-fast      # or whatever the endpoint serves

  See .claude/skills/obol-stack-dev/references/qa-model-envs.md.
EOF
        exit 2
    fi
}

main() {
    require_obol_llm_endpoint
    write_report_header
    reset_flow_workspace "$OBOL_ROOT/.workspace"
    prepare_workspace

    local failed=0
    local flow
    # flow-10 runs between flow-07 and flow-08 because it starts the local
    # Anvil + facilitator that flow-08 (and downstream buy/lifecycle) require.
    local flows=(
        "$SCRIPT_DIR/flow-01-prerequisites.sh"
        "$SCRIPT_DIR/flow-02-stack-init-up.sh"
        "$SCRIPT_DIR/flow-03-inference.sh"
        "$SCRIPT_DIR/flow-04-agent.sh"
        "$SCRIPT_DIR/flow-05-network.sh"
        "$SCRIPT_DIR/flow-06-sell-setup.sh"
        "$SCRIPT_DIR/flow-07-sell-verify.sh"
        "$SCRIPT_DIR/flow-10-anvil-facilitator.sh"
        "$SCRIPT_DIR/flow-08-buy.sh"
        "$SCRIPT_DIR/flow-09-lifecycle.sh"
    )

    for flow in "${flows[@]}"; do
        run_flow "$flow" || failed=$((failed + 1))
    done

    echo
    echo "==> Cleaning default stack before dual-stack flow"
    reset_flow_workspace "$OBOL_ROOT/.workspace"

    run_flow "$SCRIPT_DIR/flow-11-dual-stack.sh" \
        FLOW11_ARTIFACT_DIR "$ARTIFACT_DIR/flow-11-receipts" \
        || failed=$((failed + 1))

    if [ "${RELEASE_SMOKE_INCLUDE_OBOL:-false}" = "true" ]; then
        run_flow "$SCRIPT_DIR/flow-14-live-obol-base-sepolia.sh" \
            FLOW14_ARTIFACT_DIR "$ARTIFACT_DIR/flow-14-receipts" \
            || failed=$((failed + 1))
    fi

    if [ "${RELEASE_SMOKE_INCLUDE_OBOL_FORK:-false}" = "true" ]; then
        run_flow "$SCRIPT_DIR/flow-13-dual-stack-obol.sh" \
            FLOW13_ARTIFACT_DIR "$ARTIFACT_DIR/flow-13-receipts" \
            || failed=$((failed + 1))
    fi

    append_report_footer

    echo
    echo "Report: $REPORT"
    if [ "$failed" -gt 0 ]; then
        echo "Release smoke failed: $failed flow(s)"
        return 1
    fi
    echo "Release smoke passed"
}

main "$@"
