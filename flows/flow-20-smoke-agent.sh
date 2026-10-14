#!/bin/bash
# Flow 20: Smoke-test agent — sellable read-only prober for Obol Stack
# public surfaces (skill: internal/embed/skills/smoke-test).
#
# Coverage:
#   §1  Host-side syntax gate — python3 -m py_compile of the embedded
#       smoke-test skill scripts (smoke.py + gh_post.py).
#   §2  Self-smoke (cluster-gated) — run smoke.py probe against THIS
#       stack's public catalog surface through the Traefik ingress;
#       assert report.md + results.json are well-formed, score255/score100
#       are in range and internally consistent, and reportSha256 matches
#       the exact bytes of report.md on disk.
#   §3  GitHub posting (env-gated) — ONLY when GITHUB_TOKEN and
#       GITHUB_REPORT_REPO are both set; posts the §2 report and asserts a
#       commit-pinned permalink. Explicit SKIP otherwise so CI never needs
#       GitHub credentials.
#   §4  Verdict calldata — build obol, run `obol smoke calldata` with
#       fixed inputs, assert non-empty calldata carrying the
#       validationResponse(bytes32,uint8,string,bytes32,string) selector
#       0x3d659a96, and that target normalization (trailing slash) does
#       not change the derived request hash.
#
# The probe path is strictly read-only: GET-only requests against the
# published public routes (/skill.md, /api/services.json, /services/*,
# /.well-known/agent-registration.json). No X-PAYMENT header is ever sent
# and nothing in the cluster is mutated.
#
# scrub_secrets-safe: GITHUB_TOKEN is read from the environment only —
# never echoed, never placed in argv — and every captured output that
# could embed it is redacted before printing.
#
# Env overrides:
#   FLOW20_TARGET        probe target base URL (default: this stack's
#                        ingress with obol.stack rewritten to 127.0.0.1 so
#                        python3/urllib needs no special DNS resolution)
#   GITHUB_TOKEN         fine-grained PAT for the seller-owned report repo
#   GITHUB_REPORT_REPO   <owner>/<repo> public report repository
source "$(dirname "$0")/lib.sh"

require_tool python3

SKILL_SCRIPTS_DIR="$OBOL_ROOT/internal/embed/skills/smoke-test/scripts"
SMOKE_PY="$SKILL_SCRIPTS_DIR/smoke.py"
GH_POST_PY="$SKILL_SCRIPTS_DIR/gh_post.py"

FLOW_STATE_DIR="$OBOL_ROOT/.workspace/state/flows"
RUN_ROOT="$FLOW_STATE_DIR/flow20-$(date +%Y%m%d-%H%M%S)-$$"
mkdir -p "$RUN_ROOT"

# Redact the GitHub token from any captured output before it is printed.
# scrub_secrets (lib.sh) does not know about GitHub PATs, so this flow
# guarantees the token never reaches stdout/stderr on its own.
redact_gh_token() {
    local text="$1"
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        text="${text//${GITHUB_TOKEN}/[REDACTED-GH-TOKEN]}"
    fi
    printf '%s' "$text"
}

# py_compile with an explicit cfile in the flow workspace — the default
# cfile would drop a __pycache__/ dir inside internal/embed/skills/,
# polluting the repo checkout the flow runs from.
py_compile_check() {
    python3 -c 'import py_compile, sys; py_compile.compile(sys.argv[1], cfile=sys.argv[2], doraise=True)' \
        "$1" "$RUN_ROOT/$(basename "$1").pyc"
}

# §1: Host-side compile gate — the skill scripts must at least be valid
# python3 before anything ships them into an agent PVC.
step "smoke.py compiles (python3 -m py_compile)"
if [ ! -f "$SMOKE_PY" ]; then
    fail "smoke-test skill script missing: $SMOKE_PY"
else
    compile_out=""
    if compile_out=$(py_compile_check "$SMOKE_PY" 2>&1); then
        pass "smoke.py compiles"
    else
        fail "smoke.py failed py_compile — ${compile_out:0:200}"
    fi
fi

step "gh_post.py compiles (python3 -m py_compile)"
if [ ! -f "$GH_POST_PY" ]; then
    # Tolerated layout difference: posting may live in `smoke.py post`
    # instead of a dedicated gh_post.py. §3 falls back accordingly.
    skip "gh_post.py not present at $GH_POST_PY — assuming posting lives in 'smoke.py post'"
else
    compile_out=""
    if compile_out=$(py_compile_check "$GH_POST_PY" 2>&1); then
        pass "gh_post.py compiles"
    else
        fail "gh_post.py failed py_compile — ${compile_out:0:200}"
    fi
fi

# §2: Self-smoke against this stack's public catalog surface.
# Cluster-gated: every step here SKIPs cleanly when no local stack is up.
CLUSTER_UP=""
if [ -x "$OBOL" ] \
    && [ -f "$OBOL_CONFIG_DIR/.stack-id" ] \
    && [ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ] \
    && "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    CLUSTER_UP="1"
fi

TARGET=""
SELF_SMOKE_READY=""
step "Public catalog surface reachable (GET /api/services.json)"
if [ -z "$CLUSTER_UP" ]; then
    skip "no local stack (config/kubeconfig/cluster unreachable) — self-smoke steps skipped"
else
    refresh_obol_ingress_env
    # python3/urllib cannot use curl's --resolve, so default the probe
    # target to the loopback form of the ingress. The catalog routes have
    # no hostname restriction (public by design), so Host: 127.0.0.1 is
    # routed identically to obol.stack.
    TARGET="${FLOW20_TARGET:-${OBOL_INGRESS_URL/obol.stack/127.0.0.1}}"
    TARGET="${TARGET%/}"
    catalog_code=""
    # Small retry: the controller-served catalog can lag right after the
    # route is wired (same first-request race as flows 07/08).
    for _ in 1 2 3 4 5 6; do
        catalog_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 \
            "$TARGET/api/services.json" 2>/dev/null) || true
        [ "$catalog_code" = "200" ] && break
        sleep 5
    done
    if [ "$catalog_code" = "200" ]; then
        pass "catalog surface up at $TARGET/api/services.json"
        SELF_SMOKE_READY="1"
    else
        fail "catalog surface not reachable at $TARGET/api/services.json (HTTP ${catalog_code:-none})"
    fi
fi

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-flow20"
RUN_DIR=""
step "smoke.py probe ${TARGET:-<no target>} (run id $RUN_ID)"
if [ -z "$SELF_SMOKE_READY" ]; then
    skip "self-smoke target unavailable — probe skipped"
elif [ ! -f "$SMOKE_PY" ]; then
    skip "smoke.py missing — probe skipped (§1 already failed)"
else
    probe_rc=0
    # Worst case per the skill budget: 8 probes x 8s + report writes; 180s
    # is comfortably above that without masking a hang. -B because smoke.py
    # imports gh_post — without it python drops a __pycache__/ dir into
    # internal/embed/skills/smoke-test/scripts/ in this checkout.
    probe_out=$(cd "$RUN_ROOT" && run_with_timeout 180 \
        python3 -B "$SMOKE_PY" probe "$TARGET" --run-id "$RUN_ID" 2>&1) || probe_rc=$?
    if [ "$probe_rc" -eq 0 ]; then
        RUN_DIR=$(find "$RUN_ROOT/smoke" -type d -name "$RUN_ID" 2>/dev/null | head -1 || true)
        if [ -n "$RUN_DIR" ] && [ -f "$RUN_DIR/results.json" ] && [ -f "$RUN_DIR/report.md" ]; then
            pass "probe wrote $RUN_DIR/{report.md,results.json}"
        else
            RUN_DIR=""
            fail "probe exited 0 but run dir/artifacts not found under $RUN_ROOT/smoke — ${probe_out:0:200}"
        fi
    else
        fail "smoke.py probe exited $probe_rc — ${probe_out:0:300}"
    fi
fi

step "results.json + report.md well-formed (scores in range, reportSha256 matches file)"
if [ -z "$RUN_DIR" ]; then
    skip "no probe artifacts — validation skipped"
else
    validate_rc=0
    validate_out=$(python3 - "$RUN_DIR" "$RUN_ID" <<'PY' 2>&1
import hashlib
import json
import re
import sys

run_dir, run_id = sys.argv[1], sys.argv[2]

with open(run_dir + "/results.json", encoding="utf-8") as fh:
    results = json.load(fh)
with open(run_dir + "/report.md", "rb") as fh:
    report = fh.read()

assert report.startswith(b"# Obol Stack Smoke Report"), "report.md missing canonical header"

assert results.get("version") == "obol/smoke-test/v1", f"version={results.get('version')!r}"
assert results.get("runId") == run_id, f"runId={results.get('runId')!r} want {run_id!r}"
assert isinstance(results.get("target"), str) and results["target"], "target missing"
assert not results["target"].endswith("/"), "target not normalized (trailing slash)"

checks = results.get("checks")
assert isinstance(checks, list) and checks, "checks missing/empty"
for c in checks:
    assert isinstance(c, dict) and c.get("name"), "check entry malformed"
    assert isinstance(c.get("ok"), bool), f"check {c.get('name')}: ok not bool"
    assert isinstance(c.get("ms"), (int, float)) and c["ms"] >= 0, f"check {c.get('name')}: ms invalid"

names = {c["name"] for c in checks}
assert "skill-md" in names, "skill-md check missing"
assert "services-json" in names, "services-json check missing"

counted = [c for c in checks if not c.get("informational")]
passed, total = results.get("passed"), results.get("total")
assert total == len(counted), f"total={total} != counted checks {len(counted)}"
assert total >= 2, f"total={total} < 2 (skill-md + services-json are always counted)"
assert passed == sum(1 for c in counted if c["ok"]), "passed != recount of ok counted checks"
assert 0 <= passed <= total, f"passed={passed} out of range"

score255, score100 = results.get("score255"), results.get("score100")
assert score255 == (255 * passed) // total, f"score255={score255} != floor(255*{passed}/{total})"
assert 0 <= score255 <= 255, f"score255={score255} out of range"
assert score100 == (100 * passed) // total, f"score100={score100} != floor(100*{passed}/{total})"
assert 0 <= score100 <= 100, f"score100={score100} out of range"

sha = results.get("reportSha256", "")
assert re.fullmatch(r"[0-9a-f]{64}", sha or ""), f"reportSha256 not 64 lowercase hex: {sha!r}"
assert sha == hashlib.sha256(report).hexdigest(), "reportSha256 does not match report.md bytes"

# Probe-only run: permalink stays empty until a post succeeds.
assert results.get("permalink", "") == "", "permalink non-empty before post"

print(f"OK passed={passed} total={total} score255={score255} score100={score100} sha256={sha[:12]}…")
PY
    ) || validate_rc=$?
    if [ "$validate_rc" -eq 0 ]; then
        pass "artifacts valid — $validate_out"
    else
        fail "artifact validation failed — ${validate_out:0:300}"
    fi
fi

# §3: GitHub posting — strictly env-gated. CI runs probe-only and must see
# an explicit SKIP here, never an attempted network write.
step "Post report to GitHub (gh_post)"
if [ -z "${GITHUB_TOKEN:-}" ] || [ -z "${GITHUB_REPORT_REPO:-}" ]; then
    skip "GITHUB_TOKEN / GITHUB_REPORT_REPO not set — GitHub posting step skipped (probe-only mode)"
elif [ -z "$RUN_DIR" ]; then
    skip "no successful probe run dir — nothing to post"
else
    post_rc=0
    if [ -f "$GH_POST_PY" ]; then
        post_out=$(cd "$RUN_ROOT" && run_with_timeout 60 \
            python3 -B "$GH_POST_PY" "$RUN_DIR" 2>&1) || post_rc=$?
    else
        post_out=$(cd "$RUN_ROOT" && run_with_timeout 60 \
            python3 -B "$SMOKE_PY" post "$RUN_DIR" 2>&1) || post_rc=$?
    fi
    post_out=$(redact_gh_token "$post_out")
    if [ "$post_rc" -ne 0 ]; then
        fail "GitHub post exited $post_rc — ${post_out:0:300}"
    else
        permalink_rc=0
        permalink_out=$(python3 - "$RUN_DIR" <<'PY' 2>&1
import json
import re
import sys

with open(sys.argv[1] + "/results.json", encoding="utf-8") as fh:
    results = json.load(fh)

permalink = results.get("permalink", "")
# Commit-pinned blob URL — never a branch-floating html_url.
assert re.match(r"^https://github\.com/[^/]+/[^/]+/blob/[0-9a-f]{7,40}/", permalink or ""), \
    f"permalink not a commit-pinned GitHub blob URL: {permalink!r}"
print(f"OK permalink={permalink}")
PY
        ) || permalink_rc=$?
        if [ "$permalink_rc" -eq 0 ]; then
            pass "report posted — $permalink_out"
        else
            fail "post succeeded but permalink invalid — ${permalink_out:0:300}"
        fi
    fi
fi

# §4: Verdict calldata derivation — `obol smoke calldata` must emit
# validationResponse(bytes32,uint8,string,bytes32,string) calldata
# (selector 0x3d659a96) for the operator to submit with their own wallet.
CALLDATA_OBOL=""
step "Build obol for calldata derivation"
if ! command -v go >/dev/null 2>&1; then
    skip "go toolchain not on PATH — calldata steps skipped"
else
    build_rc=0
    build_out=$(cd "$OBOL_ROOT" && run_with_timeout 600 \
        go build -o "$RUN_ROOT/obol" ./cmd/obol 2>&1) || build_rc=$?
    if [ "$build_rc" -eq 0 ] && [ -x "$RUN_ROOT/obol" ]; then
        CALLDATA_OBOL="$RUN_ROOT/obol"
        pass "obol built at $RUN_ROOT/obol"
    else
        fail "go build ./cmd/obol failed — ${build_out:0:300}"
    fi
fi

# Fixed inputs — deterministic across runs so the request-hash stability
# assertion below is meaningful. The response hash is the sha256 of the
# empty string (a recognizable, obviously-synthetic 32-byte value).
FIXED_TARGET="http://obol.stack:8080"
FIXED_RUN_ID="20260101T000000Z-cafe01"
FIXED_RESPONSE_HASH="0xe3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
FIXED_RESPONSE_URI="https://github.com/example-org/example-reports/blob/0000000000000000000000000000000000000000/reports/obol.stack-8080/${FIXED_RUN_ID}.md"

step "obol smoke calldata emits selector 0x3d659a96"
if [ -z "$CALLDATA_OBOL" ]; then
    skip "obol binary unavailable — calldata derivation skipped"
else
    calldata_rc=0
    # Trailing slash on --target on purpose: the request hash must be
    # derived from the NORMALIZED target (trailing slash stripped).
    calldata_out=$(run_with_timeout 30 "$CALLDATA_OBOL" smoke calldata \
        --target "$FIXED_TARGET/" \
        --run-id "$FIXED_RUN_ID" \
        --response 100 \
        --response-uri "$FIXED_RESPONSE_URI" \
        --response-hash "$FIXED_RESPONSE_HASH" \
        --network base-sepolia 2>&1) || calldata_rc=$?
    request_hash=$(echo "$calldata_out" | grep -oE 'Request hash: 0x[0-9a-fA-F]{64}' | head -1 | awk '{print $3}' || true)
    if [ "$calldata_rc" -ne 0 ]; then
        fail "obol smoke calldata exited $calldata_rc — ${calldata_out:0:300}"
    elif [ -z "$request_hash" ] || [ "$request_hash" = "0x0000000000000000000000000000000000000000000000000000000000000000" ]; then
        fail "request hash missing or zero — ${calldata_out:0:300}"
    elif ! echo "$calldata_out" | grep -qE 'ValidationRegistry \(base-sepolia\): 0x[0-9a-fA-F]{40}'; then
        fail "ValidationRegistry address line missing — ${calldata_out:0:300}"
    elif echo "$calldata_out" | grep -qE 'Calldata: 0x3d659a96[0-9a-fA-F]+'; then
        pass "calldata carries validationResponse selector 0x3d659a96 (request hash $request_hash)"
    else
        fail "calldata missing or wrong selector (want 0x3d659a96) — ${calldata_out:0:300}"
    fi
fi

step "Request hash stable under target normalization (trailing slash)"
if [ -z "$CALLDATA_OBOL" ] || [ -z "${request_hash:-}" ]; then
    skip "no baseline request hash — normalization check skipped"
else
    norm_rc=0
    norm_out=$(run_with_timeout 30 "$CALLDATA_OBOL" smoke calldata \
        --target "$FIXED_TARGET" \
        --run-id "$FIXED_RUN_ID" \
        --response 100 \
        --response-uri "$FIXED_RESPONSE_URI" \
        --response-hash "$FIXED_RESPONSE_HASH" \
        --network base-sepolia 2>&1) || norm_rc=$?
    norm_hash=$(echo "$norm_out" | grep -oE 'Request hash: 0x[0-9a-fA-F]{64}' | head -1 | awk '{print $3}' || true)
    if [ "$norm_rc" -eq 0 ] && [ -n "$norm_hash" ] && [ "$norm_hash" = "$request_hash" ]; then
        pass "trailing-slash and bare target derive the same request hash"
    else
        fail "request hash drifted under normalization: with-slash=$request_hash bare=${norm_hash:-none} (exit $norm_rc)"
    fi
fi

step "obol smoke calldata rejects --response > 100"
if [ -z "$CALLDATA_OBOL" ]; then
    skip "obol binary unavailable — bounds check skipped"
else
    bounds_rc=0
    bounds_out=$(run_with_timeout 30 "$CALLDATA_OBOL" smoke calldata \
        --target "$FIXED_TARGET" \
        --run-id "$FIXED_RUN_ID" \
        --response 101 \
        --network base-sepolia 2>&1) || bounds_rc=$?
    if [ "$bounds_rc" -ne 0 ]; then
        pass "--response 101 rejected (the deployed registry reverts above 100)"
    else
        fail "--response 101 was accepted — ${bounds_out:0:200}"
    fi
fi

emit_metrics
