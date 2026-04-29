#!/bin/bash
# Flow 12: OBOL payment asset over the existing USDC commerce baseline.
#
# Runs the fork-local OBOL Permit2 integration path. The test deploys an
# OBOL-compatible ERC20Permit token on the Anvil Base Sepolia fork, sells a
# LiteLLM-backed service with OBOL payment metadata, buys it through the
# x402-buyer sidecar, and verifies OBOL buyer->seller settlement receipts.
#
# Requires:
#   - A running obol stack with the agent initialized.
#   - X402_FACILITATOR_BIN or X402_RS_DIR pointing to a current x402-rs build
#     with eip2612GasSponsoring support.
source "$(dirname "$0")/lib.sh"

resolve_facilitator_bin() {
    if [ -n "${X402_FACILITATOR_BIN:-}" ] && [ -x "$X402_FACILITATOR_BIN" ]; then
        if [ -z "${X402_RS_DIR:-}" ]; then
            case "$X402_FACILITATOR_BIN" in
                */target/release/*)
                    X402_RS_DIR=$(cd "$(dirname "$X402_FACILITATOR_BIN")/../.." && pwd)
                    ;;
            esac
        fi
        printf '%s\n' "$X402_FACILITATOR_BIN"
        return 0
    fi

    local rs_dir="${X402_RS_DIR:-}"
    if [ -z "$rs_dir" ] && [ -d "$HOME/Development/R&D/x402-rs" ]; then
        rs_dir="$HOME/Development/R&D/x402-rs"
    fi
    if [ -n "$rs_dir" ]; then
        for candidate in \
            "$rs_dir/target/release/x402-facilitator" \
            "$rs_dir/target/release/facilitator"; do
            if [ -x "$candidate" ]; then
                X402_RS_DIR="$rs_dir"
                printf '%s\n' "$candidate"
                return 0
            fi
        done
    fi

    return 1
}

validate_x402_rs_source() {
    local rs_dir="${X402_RS_DIR:-}"
    local expect_remote="${FLOW12_EXPECT_X402_RS_REMOTE:-x402-rs/x402-rs}"
    local expect_version="${FLOW12_EXPECT_X402_RS_VERSION:-1.4.7}"
    local remote version

    [ -n "$rs_dir" ] || return 0
    [ -d "$rs_dir/.git" ] || return 0

    remote=$(git -C "$rs_dir" remote get-url origin 2>/dev/null || true)
    if [ -n "$expect_remote" ] && [ "$expect_remote" != "any" ]; then
        case "$remote" in
            *"$expect_remote"*)
                ;;
            *)
                fail "x402-rs origin mismatch: expected remote containing '$expect_remote', got '${remote:-unknown}'"
                emit_metrics
                exit 1
                ;;
        esac
    fi

    if [ -n "$expect_version" ] && [ "$expect_version" != "any" ]; then
        version=$(python3 - "$rs_dir" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
workspace_version = ""
root_manifest = root / "Cargo.toml"
if root_manifest.exists():
    in_workspace_package = False
    for line in root_manifest.read_text().splitlines():
        stripped = line.strip()
        if stripped == "[workspace.package]":
            in_workspace_package = True
            continue
        if stripped.startswith("[") and stripped != "[workspace.package]":
            in_workspace_package = False
        if in_workspace_package:
            match = re.match(r'version\s*=\s*"([^"]+)"', stripped)
            if match:
                workspace_version = match.group(1)
                break

for path in (root / "facilitator" / "Cargo.toml", root / "crates" / "x402-facilitator-local" / "Cargo.toml"):
    if path.exists():
        for line in path.read_text().splitlines():
            stripped = line.strip()
            if re.match(r'version\s*\.workspace\s*=\s*true', stripped) and workspace_version:
                print(workspace_version)
                raise SystemExit(0)
            match = re.match(r'version\s*=\s*"([^"]+)"', stripped)
            if match:
                print(match.group(1))
                raise SystemExit(0)

for path in (root / "crates" / "x402-facilitator" / "Cargo.toml",):
    if path.exists():
        for line in path.read_text().splitlines():
            stripped = line.strip()
            match = re.match(r'version\s*=\s*"([^"]+)"', stripped)
            if match:
                print(match.group(1))
                raise SystemExit(0)
if workspace_version:
    print(workspace_version)
    raise SystemExit(0)
raise SystemExit(1)
PY
        ) || version=""
        if [ "$version" != "$expect_version" ]; then
            fail "x402-rs facilitator version mismatch: expected $expect_version, got ${version:-unknown}"
            emit_metrics
            exit 1
        fi
    fi

    echo "  x402-rs origin: ${remote:-unknown}"
    echo "  x402-rs head: $(git -C "$rs_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
    echo "  x402-rs facilitator version: ${version:-not-checked}"
}

step "local stack context is isolated"
assert_local_stack_context
pass "KUBECONFIG=$KUBECONFIG"

step "required local tools are available"
for tool in go git python3; do
    require_tool "$tool"
done
pass "required tools found"

step "stack core deployments are ready"
for rollout in \
    "deployment/litellm -n llm" \
    "deployment/x402-verifier -n x402" \
    "deployment/serviceoffer-controller -n x402" \
    "deployment/openclaw -n openclaw-obol-agent"; do
    # shellcheck disable=SC2086
    if ! "$OBOL" kubectl rollout status $rollout --timeout=120s >/dev/null 2>&1; then
        fail "rollout not ready: $rollout"
        "$OBOL" kubectl get pods -A
        emit_metrics
        exit 1
    fi
done
pass "core deployments ready"

step "LiteLLM HTTP readiness"
if "$OBOL" kubectl exec -n llm deployment/litellm -c litellm -- \
    python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:4000/health/readiness', timeout=5)" >/dev/null 2>&1; then
    pass "LiteLLM readiness endpoint is healthy"
else
    fail "LiteLLM readiness endpoint is not healthy"
    "$OBOL" kubectl logs -n llm deployment/litellm --tail=80 || true
    emit_metrics
    exit 1
fi

step "x402-rs facilitator binary available for OBOL Permit2"
FACILITATOR_BIN=$(resolve_facilitator_bin || true)
if [ -n "$FACILITATOR_BIN" ]; then
    export X402_FACILITATOR_BIN="$FACILITATOR_BIN"
    pass "X402_FACILITATOR_BIN=$X402_FACILITATOR_BIN"
else
    fail "x402-rs facilitator binary not found — set X402_FACILITATOR_BIN or X402_RS_DIR"
    emit_metrics
    exit 1
fi

step "x402-rs facilitator source matches expected release line"
validate_x402_rs_source
pass "x402-rs facilitator source validated"

step "OBOL Permit2 sell->buy->settle integration test"
ARTIFACT_DIR="${FLOW12_ARTIFACT_DIR:-$OBOL_ROOT/.tmp/flow-12-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$ARTIFACT_DIR"
LOG="$ARTIFACT_DIR/test-output.log"
set +e
go test -tags integration -v \
    -run '^TestIntegration_SellBuySidecar_OBOLPermit2$' \
    -timeout "${FLOW12_TIMEOUT:-30m}" \
    ./internal/openclaw/ 2>&1 | tee "$LOG"
rc=${PIPESTATUS[0]}
set -e

extract() { grep -oE "$1=\\S+" "$LOG" | head -1 | cut -d= -f2; }
AGENT_ID=$(extract FLOW12_AGENT_ID)
REGISTRATION_TX=$(extract FLOW12_REGISTRATION_TX)
FUNDING_TX=$(extract FLOW12_FUNDING_TX)
SETTLEMENT_TX=$(extract FLOW12_SETTLEMENT_TX)
COMMIT=$(git -C "$OBOL_ROOT" rev-parse HEAD 2>/dev/null || echo "")
ARTIFACT_DIR="$ARTIFACT_DIR" \
COMMIT="$COMMIT" \
AGENT_ID="$AGENT_ID" \
REGISTRATION_TX="$REGISTRATION_TX" \
FUNDING_TX="$FUNDING_TX" \
SETTLEMENT_TX="$SETTLEMENT_TX" \
python3 - <<'PY'
import json, os
out = os.path.join(os.environ["ARTIFACT_DIR"], "receipt-summary.json")
data = {
    "commit": os.environ.get("COMMIT", ""),
    "agentId": os.environ.get("AGENT_ID", ""),
    "transactions": {
        "registration": os.environ.get("REGISTRATION_TX", ""),
        "funding": os.environ.get("FUNDING_TX", ""),
        "settlement": os.environ.get("SETTLEMENT_TX", ""),
    },
}
with open(out, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
PY

if [ "$rc" -eq 0 ]; then
    pass "OBOL Permit2 integration passed"
    pass "Receipt summary: $ARTIFACT_DIR/receipt-summary.json"
else
    fail "OBOL Permit2 integration failed with exit $rc"
fi

emit_metrics
