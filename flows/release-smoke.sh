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

    local config_dir stack_id cluster
    for config_dir in "$OBOL_CONFIG_DIR" "$OBOL_ROOT/.workspace-alice/config" "$OBOL_ROOT/.workspace-bob/config"; do
        [ -f "$config_dir/.stack-id" ] || continue
        stack_id=$(cat "$config_dir/.stack-id" 2>/dev/null || true)
        [ -n "$stack_id" ] || continue
        cluster="obol-stack-$stack_id"
        k3d cluster delete "$cluster" >/dev/null 2>&1 || true
    done
}
trap cleanup_stacks EXIT

write_report_header() {
    cat > "$REPORT" <<EOF
# Obol Stack Release Smoke Report

Date: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
Commit: $(git -C "$OBOL_ROOT" rev-parse HEAD)
Artifacts: $ARTIFACT_DIR

| Flow | Result | FAIL lines | Exit code |
| --- | --- | ---: | ---: |
EOF
}

append_report_row() {
    local flow="$1"
    local result="$2"
    local fail_count="$3"
    local rc="$4"
    printf '| `%s` | %s | %s | %s |\n' "$flow" "$result" "$fail_count" "$rc" >> "$REPORT"
}

append_report_footer() {
    cat >> "$REPORT" <<EOF

## Notes

- The runner uses the real \`obol\` CLI and the flow scripts as black-box release checks.
- Any \`FAIL:\` line is release-gating, even when a child script exits zero.
- \`flow-11-dual-stack.sh\` writes on-chain receipt artifacts under \`$ARTIFACT_DIR/flow-11-receipts\`.
EOF
}

prepare_workspace() {
    echo "==> Building obol"
    (cd "$OBOL_ROOT" && go build -o "$OBOL" ./cmd/obol)

    local tool src
    for tool in kubectl helm helmfile k3d k9s openclaw; do
        src=$(command -v "$tool" 2>/dev/null || true)
        [ -n "$src" ] && ln -sf "$src" "$OBOL_BIN_DIR/$tool"
    done

    echo "==> Ensuring Python payment dependencies"
    ensure_payment_python_deps
}

run_flow() {
    local flow="$1"
    local name log rc fail_count result
    name=$(basename "$flow" .sh)
    log="$ARTIFACT_DIR/$name.log"

    echo
    echo "===== START $name ====="
    set +e
    if [ "$name" = "flow-11-dual-stack" ]; then
        FLOW11_ARTIFACT_DIR="$ARTIFACT_DIR/flow-11-receipts" bash "$flow" 2>&1 | tee "$log"
    else
        bash "$flow" 2>&1 | tee "$log"
    fi
    rc=${PIPESTATUS[0]}
    set -e

    fail_count=$(grep -c '^FAIL:' "$log" 2>/dev/null || true)
    if [ "$rc" -eq 0 ] && [ "$fail_count" -eq 0 ]; then
        result="PASS"
    else
        result="FAIL"
    fi
    append_report_row "$name" "$result" "$fail_count" "$rc"
    echo "===== END $name result=$result rc=$rc fails=$fail_count ====="

    [ "$result" = "PASS" ]
}

cleanup_default_stack_before_dual() {
    echo
    echo "==> Cleaning default stack before dual-stack flow"
    "$OBOL" stack down >/dev/null 2>&1 || true
    if [ -f "$OBOL_CONFIG_DIR/.stack-id" ]; then
        k3d cluster delete "obol-stack-$(cat "$OBOL_CONFIG_DIR/.stack-id")" >/dev/null 2>&1 || true
    fi
}

main() {
    write_report_header
    prepare_workspace

    local failed=0
    local flow
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
        if ! run_flow "$flow"; then
            failed=$((failed + 1))
        fi
    done

    cleanup_default_stack_before_dual

    if ! run_flow "$SCRIPT_DIR/flow-11-dual-stack.sh"; then
        failed=$((failed + 1))
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
