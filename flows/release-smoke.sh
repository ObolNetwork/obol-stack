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

run_flow() {
    local flow="$1"
    local name log rc fail_count skip_count result
    name=$(basename "$flow" .sh)
    log="$ARTIFACT_DIR/$name.log"

    echo
    echo "===== START $name ====="
    set +e
    if [ "$name" = "flow-11-dual-stack" ]; then
        FLOW11_ARTIFACT_DIR="$ARTIFACT_DIR/flow-11-receipts" bash "$flow" 2>&1 | tee "$log"
    elif [ "$name" = "flow-13-dual-stack-obol" ]; then
        FLOW13_ARTIFACT_DIR="$ARTIFACT_DIR/flow-13-receipts" bash "$flow" 2>&1 | tee "$log"
    elif [ "$name" = "flow-14-live-obol-base-sepolia" ]; then
        FLOW14_ARTIFACT_DIR="$ARTIFACT_DIR/flow-14-receipts" bash "$flow" 2>&1 | tee "$log"
    else
        bash "$flow" 2>&1 | tee "$log"
    fi
    rc=${PIPESTATUS[0]}
    set -e

    fail_count=$(grep -c '^FAIL:' "$log" 2>/dev/null || true)
    skip_count=$(grep -c '^SKIP:' "$log" 2>/dev/null || true)
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

    if [ "${RELEASE_SMOKE_INCLUDE_OBOL:-false}" = "true" ]; then
        if ! run_flow "$SCRIPT_DIR/flow-14-live-obol-base-sepolia.sh"; then
            failed=$((failed + 1))
        fi
    fi

    if [ "${RELEASE_SMOKE_INCLUDE_OBOL_FORK:-false}" = "true" ]; then
        if ! run_flow "$SCRIPT_DIR/flow-13-dual-stack-obol.sh"; then
            failed=$((failed + 1))
        fi
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
