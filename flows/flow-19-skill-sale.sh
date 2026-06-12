#!/bin/bash
# Flow 19: Skill Sale — type=skill ServiceOffer (skill marketplace v0).
#
# Sells an embedded skill as a paid, hash-pinned bundle download and checks
# the integrity surfaces end to end:
#
#   1. Build obol, confirm the `obol sell skill` / `obol skills` CLI surfaces
#      exist (skip cleanly when this branch predates them).
#   2. `obol sell skill <offer> --from-embedded <skill>` → bundle ConfigMap +
#      type=skill ServiceOffer; controller renders the so-<offer>-bundle
#      busybox httpd and gates /services/<offer>/* behind x402.
#   3. Assert the bundle ConfigMap bytes hash to spec.skill.sha256 and stay
#      under the 900000-byte compressed cap.
#   4. Unpaid probe → 402 with accepts[0].extra.skill {name,version,sha256};
#      sha256 must equal the ConfigMap bytes hash (pre-purchase integrity).
#   5. sha256-verify the served bundle artifact and check it is a well-formed
#      gzipped tar with a top-level SKILL.md.
#   6. (Gated: FLOW19_PAID_FETCH=true + funded agent wallet) one-shot paid
#      fetch via the buy-x402 skill's buy.py pay.
#   7. Derive ERC-8004 set-hash + feedback (tag1=asr:skill) calldata via
#      `obol skills calldata` and assert non-empty, deterministic hex. The
#      calldata is OPERATOR-submitted — this flow never signs or sends a tx.
#
# Requires for the cluster section: flows 01-02 (running stack). The calldata
# section runs without a cluster. Cleanup is opt-in via FLOW_CLEANUP=1.
source "$(dirname "$0")/lib.sh"

SKILL_NAME="${FLOW19_SKILL:-gas}"
SKILL_VERSION="${FLOW19_SKILL_VERSION:-0.1.0}"
OFFER_NAME="${FLOW19_OFFER_NAME:-flow19-${SKILL_NAME}}"
OFFER_NS="${FLOW19_NS:-default}"
PRICE="${FLOW19_PRICE:-0.001}"
FLOW19_CHAIN="${FLOW19_CHAIN:-base-sepolia}"
# Deterministic Hardhat/Anvil test address (account #1) — public test
# constant, same address lib.sh derives from the well-known mnemonic.
PAY_TO="${FLOW19_PAY_TO:-${SELLER_WALLET:-0x70997970C51812dc3A010C7d01b50e0d17dc79C8}}"
FEEDBACK_AGENT_ID="${FLOW19_AGENT_ID:-1}"
FEEDBACK_VALUE="${FLOW19_FEEDBACK_VALUE:-95}"
BUNDLE_FILE=""
CM_SHA=""

sha256_file() {
    python3 - "$1" <<'PY'
import hashlib, sys
with open(sys.argv[1], "rb") as f:
    print(hashlib.sha256(f.read()).hexdigest())
PY
}

# §0: Prerequisites + build
step "required local tools are available"
require_tool python3
pass "required tools found"

step "build obol CLI"
if [ "${FLOW19_SKIP_BUILD:-false}" = "true" ] && [ -x "$OBOL" ]; then
    skip "FLOW19_SKIP_BUILD=true — using existing $OBOL"
elif command -v go >/dev/null 2>&1; then
    if build_out=$(cd "$OBOL_ROOT" && go build -o "$OBOL_BIN_DIR/obol" ./cmd/obol 2>&1); then
        pass "built $OBOL_BIN_DIR/obol"
    else
        fail "go build failed — ${build_out:0:300}"
        emit_metrics
        exit 1
    fi
elif [ -x "$OBOL" ]; then
    skip "go not on PATH — using existing $OBOL"
else
    fail "no go toolchain and no prebuilt obol at $OBOL"
    emit_metrics
    exit 1
fi

# §1: CLI surface gates — skip cleanly on branches that predate the
# skill-marketplace CLI instead of failing the whole flow.
HAVE_SELL_SKILL=0
step "obol sell skill subcommand present"
sell_help=$("$OBOL" sell --help 2>&1 || true)
if echo "$sell_help" | grep -qE '^[[:space:]]+skill([[:space:],]|$)'; then
    HAVE_SELL_SKILL=1
    pass "obol sell skill is available"
else
    skip "obol sell skill not in this build — skipping sell/cluster section"
fi

HAVE_SKILLS_CMD=0
step "obol skills command group present"
root_help=$("$OBOL" --help 2>&1 || true)
if echo "$root_help" | grep -qE '^[[:space:]]+skills([[:space:],]|$)'; then
    HAVE_SKILLS_CMD=1
    pass "obol skills is available"
else
    skip "obol skills not in this build — skipping calldata section"
fi

# §2: Cluster gate
CLUSTER_OK=0
step "local stack reachable"
if [ -f "$OBOL_CONFIG_DIR/.stack-id" ] && [ -f "$OBOL_CONFIG_DIR/kubeconfig.yaml" ] \
    && "$OBOL" kubectl cluster-info >/dev/null 2>&1; then
    CLUSTER_OK=1
    pass "cluster reachable via $OBOL_CONFIG_DIR/kubeconfig.yaml"
else
    skip "no running local stack — skipping sell/cluster section"
fi

if [ "$HAVE_SELL_SKILL" = "1" ] && [ "$CLUSTER_OK" = "1" ]; then
    # §3: Sell the embedded skill
    step "obol sell skill $OFFER_NAME --from-embedded $SKILL_NAME"
    sell_out=$("$OBOL" sell skill "$OFFER_NAME" \
        --from-embedded "$SKILL_NAME" \
        --skill-version "$SKILL_VERSION" \
        --per-request "$PRICE" \
        --chain "$FLOW19_CHAIN" \
        --pay-to "$PAY_TO" \
        --no-register \
        -n "$OFFER_NS" 2>&1) && sell_rc=0 || sell_rc=$?
    if [ "${sell_rc:-1}" -eq 0 ]; then
        pass "sell skill accepted"
    else
        fail "obol sell skill exited $sell_rc — ${sell_out:0:300}"
    fi

    # §3.1: ServiceOffer landed with type=skill + spec.skill block
    step "ServiceOffer $OFFER_NAME has spec.type=skill and spec.skill"
    so_type=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.spec.type}' 2>/dev/null || true)
    SPEC_SHA=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.spec.skill.sha256}' 2>/dev/null || true)
    SPEC_VERSION=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.spec.skill.version}' 2>/dev/null || true)
    BUNDLE_CM=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
        -o jsonpath='{.spec.skill.bundleConfigMap}' 2>/dev/null || true)
    if [ "$so_type" = "skill" ] && echo "$SPEC_SHA" | grep -qE '^[a-f0-9]{64}$' \
        && [ "$SPEC_VERSION" = "$SKILL_VERSION" ] && [ -n "$BUNDLE_CM" ]; then
        pass "spec.skill: version=$SPEC_VERSION cm=$BUNDLE_CM sha256=${SPEC_SHA:0:12}…"
    else
        fail "spec mismatch: type=$so_type version=$SPEC_VERSION cm=$BUNDLE_CM sha=$SPEC_SHA"
    fi

    # §3.2: Bundle ConfigMap bytes — size cap + hash pin. These are the exact
    # bytes the controller hash-verifies and the bundle server serves; a
    # mismatch here means the controller must refuse to publish
    # (BundleHashMismatch).
    step "bundle ConfigMap bytes hash to spec.skill.sha256 (<=900000 bytes)"
    BUNDLE_FILE="$(mktemp -t flow19-bundle-XXXXXX).tar.gz"
    "$OBOL" kubectl get configmap "$BUNDLE_CM" -n "$OFFER_NS" \
        -o jsonpath='{.binaryData.bundle\.tar\.gz}' 2>/dev/null \
        | python3 -c 'import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.read()))' \
        > "$BUNDLE_FILE" || true
    bundle_size=$(wc -c < "$BUNDLE_FILE" | tr -d ' ')
    CM_SHA=$(sha256_file "$BUNDLE_FILE" 2>/dev/null || true)
    if [ "${bundle_size:-0}" -gt 0 ] && [ "${bundle_size:-0}" -le 900000 ] \
        && [ "$CM_SHA" = "$SPEC_SHA" ]; then
        pass "bundle $bundle_size bytes, sha256 matches spec (${CM_SHA:0:12}…)"
    else
        fail "bundle bytes invalid: size=$bundle_size cmSha=$CM_SHA specSha=$SPEC_SHA"
    fi

    # §3.3: Controller convergence. Registration is disabled (--no-register),
    # so the full ladder ends at Ready=True. Anchored grep — a bare
    # "Ready=True" would substring-match "PaymentGateReady=True".
    step "ServiceOffer $OFFER_NAME reaches Ready=True (polling, max 60x5s)"
    so_ready=""
    conds=""
    for _ in $(seq 1 60); do
        conds=$("$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
            -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}' 2>/dev/null || true)
        if echo "$conds" | grep -qE '(^| )Ready=True'; then
            so_ready="yes"
            break
        fi
        sleep 5
    done
    if [ -n "$so_ready" ]; then
        pass "ServiceOffer Ready (conditions: $conds)"
    else
        fail "ServiceOffer not Ready within 300s — conditions: ${conds:-unreadable}"
        "$OBOL" kubectl get serviceoffer "$OFFER_NAME" -n "$OFFER_NS" \
            -o jsonpath='{range .status.conditions[*]}{.type}: {.reason} — {.message}{"\n"}{end}' 2>/dev/null || true
    fi

    # §3.4: Controller-rendered bundle server children exist
    step "bundle server so-$OFFER_NAME-bundle rendered"
    bundle_deploy=$("$OBOL" kubectl get deploy "so-$OFFER_NAME-bundle" -n "$OFFER_NS" \
        -o jsonpath='{.metadata.name}' 2>/dev/null || true)
    if [ "$bundle_deploy" = "so-$OFFER_NAME-bundle" ]; then
        pass "Deployment so-$OFFER_NAME-bundle exists"
    else
        fail "bundle server Deployment missing in $OFFER_NS"
    fi

    # §4: Unpaid probe → 402 carries extra.skill with the pinned hash.
    # Retry loop per pitfall 14 (first-request race on a fresh verifier route).
    refresh_obol_ingress_env
    BASE_URL="${OBOL_INGRESS_URL%/}"
    if [[ "$BASE_URL" == *"obol.stack"* ]]; then
        CURL_BASE="$CURL_OBOL"
    else
        CURL_BASE="curl"
    fi

    step "402 response carries extra.skill {name,version,sha256} (polling, max 12x5s)"
    EXTRA_SHA=""
    body_402=""
    for _ in $(seq 1 12); do
        body_402=$($CURL_BASE -s --max-time 10 \
            "$BASE_URL/services/$OFFER_NAME/bundle.tar.gz" 2>&1) || true
        EXTRA_SHA=$(echo "$body_402" | python3 -c "
import json, re, sys
d = json.load(sys.stdin)
skill = (d['accepts'][0].get('extra') or {}).get('skill') or {}
assert skill.get('name'), 'skill.name missing'
assert skill.get('version') == '$SKILL_VERSION', 'skill.version mismatch'
assert re.fullmatch(r'[a-f0-9]{64}', skill.get('sha256', '')), 'skill.sha256 malformed'
print(skill['sha256'])
" 2>/dev/null) || EXTRA_SHA=""
        [ -n "$EXTRA_SHA" ] && break
        sleep 5
    done
    if [ -n "$EXTRA_SHA" ]; then
        pass "402 extra.skill present (sha256 ${EXTRA_SHA:0:12}…)"
    else
        fail "402 missing/invalid extra.skill — ${body_402:0:300}"
    fi

    step "402-advertised sha256 equals served bundle bytes hash"
    if [ -n "$EXTRA_SHA" ] && [ "$EXTRA_SHA" = "$CM_SHA" ]; then
        pass "pre-purchase integrity holds: extra.skill.sha256 == sha256(bundle bytes)"
    else
        fail "hash mismatch: 402=$EXTRA_SHA bundle=$CM_SHA"
    fi

    # §4.1: Artifact well-formedness — gzipped tar with top-level SKILL.md.
    # A bundle without SKILL.md is not a skill.
    step "bundle is a gzipped tar containing top-level SKILL.md"
    tar_list=$(tar -tzf "$BUNDLE_FILE" 2>&1 || true)
    if echo "$tar_list" | grep -qx "SKILL.md"; then
        pass "SKILL.md present ($(echo "$tar_list" | grep -c . ) entries)"
    else
        fail "top-level SKILL.md missing from bundle — entries: $(echo "$tar_list" | head -5 | tr '\n' ' ')"
    fi

    # §5: One-shot paid fetch via buy-x402 buy.py pay (gated — needs a funded
    # agent wallet on $FLOW19_CHAIN; spends one auth = $PRICE).
    #
    # Target /skill.json (text JSON): buy.py pay prints the response body via
    # decode(errors="replace") and is not binary-safe, so the gzip artifact is
    # byte-verified from the ConfigMap above; the paid request proves the
    # 402 → sign → X-PAYMENT → 200 loop on the same gated route.
    step "one-shot paid fetch via buy.py pay (FLOW19_PAID_FETCH gate)"
    if [ "${FLOW19_PAID_FETCH:-false}" != "true" ]; then
        skip "FLOW19_PAID_FETCH != true — paid fetch not attempted"
    elif ! "$OBOL" kubectl get deploy hermes -n hermes-obol-agent >/dev/null 2>&1; then
        skip "hermes agent deployment not found — paid fetch needs the default agent"
    else
        pay_out=$("$OBOL" kubectl exec -n hermes-obol-agent deploy/hermes -c hermes -- \
            python3 /data/.hermes/obol-skills/buy-x402/scripts/buy.py pay \
            "http://traefik.traefik.svc.cluster.local/services/$OFFER_NAME/skill.json" \
            --timeout 60 2>&1) || true
        if echo "$pay_out" | grep -q "HTTP 200" \
            && echo "$pay_out" | grep -q "$CM_SHA"; then
            pass "paid fetch returned 200 with the pinned sha256 in skill.json"
        else
            fail "paid fetch failed — ${pay_out:0:400}"
        fi
    fi
fi

# §6: ERC-8004 calldata derivation (no cluster, no chain, no signing).
# set-hash pins sha256(bundle) under metadata key skill.sha256:<name>@<version>;
# feedback scores the skill with tag1=asr:skill. Both print calldata for the
# OPERATOR to submit with their own wallet — assert non-empty deterministic hex.
if [ "$HAVE_SKILLS_CMD" = "1" ]; then
    SKILL_REF="$SKILL_NAME@$SKILL_VERSION"

    # Without a cluster run there is no ConfigMap bundle — pack the embedded
    # skill dir from the repo so set-hash has bytes to hash. Determinism of
    # the calldata is asserted by running each command twice on the same
    # input, not by reproducing the canonical Go packer here.
    if [ -z "$BUNDLE_FILE" ] || [ ! -s "$BUNDLE_FILE" ]; then
        step "pack local fallback bundle for calldata derivation"
        BUNDLE_FILE="$(mktemp -t flow19-bundle-XXXXXX).tar.gz"
        if python3 - "$OBOL_ROOT/internal/embed/skills/$SKILL_NAME" "$BUNDLE_FILE" <<'PY'
import gzip, io, os, sys, tarfile

src, out = sys.argv[1], sys.argv[2]
if not os.path.isfile(os.path.join(src, "SKILL.md")):
    raise SystemExit(f"not a skill dir (no SKILL.md): {src}")
paths = []
for root, dirs, files in os.walk(src):
    dirs[:] = sorted(d for d in dirs if d != "__pycache__")
    for name in sorted(dirs + files):
        p = os.path.join(root, name)
        if os.path.islink(p):
            raise SystemExit(f"symlink not allowed: {p}")
        if name.endswith(".pyc"):
            continue
        paths.append(p)
paths.sort(key=lambda p: os.path.relpath(p, src).replace(os.sep, "/"))
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode="w", format=tarfile.USTAR_FORMAT) as tf:
    for p in paths:
        rel = os.path.relpath(p, src).replace(os.sep, "/")
        if os.path.isdir(p):
            info = tarfile.TarInfo(rel + "/")
            info.type, info.mode = tarfile.DIRTYPE, 0o755
            info.mtime = info.uid = info.gid = 0
            info.uname = info.gname = ""
            tf.addfile(info)
        else:
            with open(p, "rb") as f:
                data = f.read()
            info = tarfile.TarInfo(rel)
            info.size = len(data)
            info.mode = 0o755 if os.stat(p).st_mode & 0o111 else 0o644
            info.mtime = info.uid = info.gid = 0
            info.uname = info.gname = ""
            tf.addfile(info, io.BytesIO(data))
# filename="" — a named fileobj would leak the output path into the gzip
# FNAME header and break run-to-run determinism.
with open(out, "wb") as f:
    with gzip.GzipFile(filename="", fileobj=f, mode="wb", compresslevel=9, mtime=0) as gz:
        gz.write(buf.getvalue())
PY
        then
            pass "fallback bundle packed ($(wc -c < "$BUNDLE_FILE" | tr -d ' ') bytes)"
        else
            fail "could not pack fallback bundle from internal/embed/skills/$SKILL_NAME"
        fi
    fi

    step "obol skills calldata set-hash prints deterministic calldata"
    sethash_1=$("$OBOL" skills calldata set-hash \
        --agent-id "$FEEDBACK_AGENT_ID" \
        --chain "$FLOW19_CHAIN" \
        --skill "$SKILL_REF" \
        --bundle "$BUNDLE_FILE" 2>&1) || true
    sethash_2=$("$OBOL" skills calldata set-hash \
        --agent-id "$FEEDBACK_AGENT_ID" \
        --chain "$FLOW19_CHAIN" \
        --skill "$SKILL_REF" \
        --bundle "$BUNDLE_FILE" 2>&1) || true
    sethash_hex=$(echo "$sethash_1" | grep -oE 'Calldata: 0x[0-9a-fA-F]+' | head -1 | awk '{print $2}')
    if [ -n "$sethash_hex" ] && [ ${#sethash_hex} -gt 10 ] \
        && [ "$sethash_1" = "$sethash_2" ] \
        && echo "$sethash_1" | grep -qE 'IdentityRegistry.*0x[0-9a-fA-F]{40}'; then
        pass "set-hash calldata deterministic (${#sethash_hex} hex chars)"
    else
        fail "set-hash calldata missing or non-deterministic — ${sethash_1:0:300}"
    fi

    step "obol skills calldata feedback (tag1=asr:skill) prints deterministic calldata"
    fb_1=$("$OBOL" skills calldata feedback \
        --agent-id "$FEEDBACK_AGENT_ID" \
        --skill "$SKILL_REF" \
        --value "$FEEDBACK_VALUE" \
        --chain "$FLOW19_CHAIN" 2>&1) || true
    fb_2=$("$OBOL" skills calldata feedback \
        --agent-id "$FEEDBACK_AGENT_ID" \
        --skill "$SKILL_REF" \
        --value "$FEEDBACK_VALUE" \
        --chain "$FLOW19_CHAIN" 2>&1) || true
    fb_hex=$(echo "$fb_1" | grep -oE 'Calldata: 0x[0-9a-fA-F]+' | head -1 | awk '{print $2}')
    if [ -n "$fb_hex" ] && [ ${#fb_hex} -gt 10 ] \
        && [ "$fb_1" = "$fb_2" ] \
        && echo "$fb_1" | grep -qE 'ReputationRegistry.*0x[0-9a-fA-F]{40}'; then
        pass "feedback calldata deterministic (${#fb_hex} hex chars)"
    else
        fail "feedback calldata missing or non-deterministic — ${fb_1:0:300}"
    fi

    step "set-hash and feedback calldata differ (distinct selectors/payloads)"
    if [ -n "$sethash_hex" ] && [ -n "$fb_hex" ] && [ "$sethash_hex" != "$fb_hex" ]; then
        pass "calldata payloads are distinct"
    else
        fail "calldata sanity failed: set-hash=$sethash_hex feedback=$fb_hex"
    fi
fi

# §7: Cleanup — opt-in so a successful run leaves the offer for inspection.
if [ "${FLOW_CLEANUP:-0}" = "1" ] && [ "$CLUSTER_OK" = "1" ] && [ "$HAVE_SELL_SKILL" = "1" ]; then
    "$OBOL" sell delete "$OFFER_NAME" -n "$OFFER_NS" >/dev/null 2>&1 || true
    [ -n "${BUNDLE_CM:-}" ] && "$OBOL" kubectl delete configmap "$BUNDLE_CM" -n "$OFFER_NS" \
        --ignore-not-found >/dev/null 2>&1 || true
fi
[ -n "$BUNDLE_FILE" ] && rm -f "$BUNDLE_FILE" 2>/dev/null || true

emit_metrics
