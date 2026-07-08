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
    local commit
    commit="${RELEASE_SMOKE_COMMIT:-}"
    if [ -z "$commit" ]; then
        commit="$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
    fi

    cat > "$REPORT" <<EOF
# Obol Stack Release Smoke Report

Date: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
Commit: $commit
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
    # All flow output (stdout + stderr) is piped through scrub_secrets before
    # both the terminal and the artifact log. This catches secret leaks that
    # other CLI tools (e.g. `obol network add` echoing the full RPC URL with
    # an Alchemy API key) would otherwise persist on disk.
    if [ -n "$env_name" ]; then
        env "$env_name=$env_value" bash "$flow" 2>&1 | scrub_secrets | tee "$log"
    else
        bash "$flow" 2>&1 | scrub_secrets | tee "$log"
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
    export OBOL_LLM_MODEL=qwen36-deep      # 27B-class default; or whatever the endpoint serves

  See .claude/skills/obol-stack-dev/references/qa-model-envs.md.
EOF
        exit 2
    fi

    if ! preflight_openai_llm_endpoint; then
        cat >&2 <<EOF
release-smoke: OBOL_LLM_ENDPOINT is set but did not pass the OpenAI-compatible
               chat preflight for model ${OBOL_LLM_MODEL:-qwen36-deep}.

  The OBOL flows depend on the endpoint returning final assistant content, not
  only reasoning metadata or an empty response. Fix OBOL_LLM_ENDPOINT /
  OBOL_LLM_MODEL before spending time on the cluster flows.
EOF
        exit 2
    fi
}

# Warn (not block) when the OBOL/_FORK gates are on but no paid Base
# Sepolia RPC is configured. The free-tier fallbacks (drpc.org, sepolia.base.org)
# routinely hit 408 "Request timeout on the free tier" during smoke runs
# that fire multiple anvil forks + receipt scans + balance reads, which
# surfaces as flow-13 facilitator-not-reachable or flow-11 cast-call empty.
warn_unpaid_base_sepolia_rpc() {
    local need=false
    [ "${RELEASE_SMOKE_INCLUDE_OBOL:-false}" = "true" ]      && need=true
    [ "${RELEASE_SMOKE_INCLUDE_OBOL_FORK:-false}" = "true" ] && need=true
    [ "$need" = "true" ] || return 0
    [ -n "${ALCHEMY_BASE_SEPOLIA_API_KEY:-}" ] && return 0
    [ -n "${BASE_SEPOLIA_RPC:-}" ] && return 0

    cat >&2 <<EOF
release-smoke: WARNING — no paid Base Sepolia RPC configured.
               Set ALCHEMY_BASE_SEPOLIA_API_KEY in .env for a stable run.

  flow-13's anvil fork and flow-11/14 balance reads default to the free-tier
  candidates (drpc.org, sepolia.base.org, ...) which routinely return
  HTTP 408 "Request timeout on the free tier" under release-smoke load.
  Expect intermittent failures at:
    flow-11 step 8     (Could not read Alice starting USDC balance)
    flow-11 step 1170  (Bob signer USDC pre-buy snapshot)
    flow-13 step 9     (Facilitator did not become reachable — fork RPC timeout)
    flow-14 balance reads
EOF
}

main() {
    require_obol_llm_endpoint
    warn_unpaid_base_sepolia_rpc
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
        # flow-16 declares a CRD sub-agent and gates it via `obol sell agent`,
        # asserting (among other things) that the `.no-bundled-skills` marker
        # is honored inside the Hermes pod — the contract that v2026.5.28
        # silently broke (~100k tokens of bundled-skill bloat re-seeded on
        # every launch, undetected because this flow wasn't in CI). Adding it
        # here is follow-up #2 from the rc13 report PR.
        "$SCRIPT_DIR/flow-16-sell-agent.sh"
        # flow-17 is the only end-to-end coverage of `obol sell mcp` (paid
        # MCP tool over x402 in-band _meta). It reuses flow-10's anvil fork
        # + facilitator, runs the foreground MCP server, and drives the full
        # free/requirements/unpaid/paid loop with the x402 SDK's MCP client,
        # asserting on-chain settlement + seller-credential injection. Needs
        # no cluster of its own, so it sits at the end of the baseline group
        # while anvil + the facilitator are still up.
        "$SCRIPT_DIR/flow-17-sell-mcp.sh"
        # flow-19 is the only live coverage of SIWx identity-gated routes
        # (gate=auth): it asserts the 401 challenge advertises the
        # sign-in-with-x extension and that a wallet-signed credential
        # (minted by the buy-x402 skill via the agent's remote-signer) is
        # accepted. No payment/anvil dependency — only the baseline stack +
        # the flow-04 agent's remote-signer.
        "$SCRIPT_DIR/flow-19-sell-auth.sh"
        # flow-20 is the only live coverage of async job offers (the
        # verifier→job-broker hand-off): submit → 402 → pay → 202 with a job
        # handle → poll to completion → jobToken-gated result retrieval. It
        # reuses flow-10's anvil fork + facilitator (still up here) for the
        # paid submit, exactly like flow-17.
        "$SCRIPT_DIR/flow-20-async-job.sh"
        # flow-21 is the only live coverage of multi-route gate classes
        # (spec.routes[] free/paid/undeclared-fail-closed) and per-offer
        # hostname binding (P1b), on a self-contained throwaway offer. gate
        # enforcement needs no facilitator, so it also sits in this group.
        "$SCRIPT_DIR/flow-21-route-surface.sh"
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
