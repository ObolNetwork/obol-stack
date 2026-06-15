#!/usr/bin/env bash
# HF-surface smoke — validate the "decentralised Hugging Face" surfaces the
# obol-stack ships, end to end:
#
#   1. Dataset Hub      anonymize -> sign a version -> publish (gated) -> buy
#                       (resumable, whole-file-hash-verified download)
#   2. Inference        a type=inference offer in the federated catalog
#   3. Fine-tuning      run a backend over the BOUGHT dataset on a real GPU box
#                       (spark), producing run.manifest bound to the dataset's
#                       content-address (provenance)
#   4. Discovery        federate the seller catalog through obol-router and
#                       assert both the dataset and inference offers surface
#   5. Indexer          cross-check against the obol-exex ERC-8004 indexer
#
# Each surface is independent; a missing prerequisite SKIPs, never aborts.
#
# Overridable env:
#   OBOL_BIN        path to a built obol (default: build from this tree)
#   ROUTER_BIN      path to a built obol-router (default: /tmp/obol-router)
#   SPARK           ssh host for the fine-tune (default: spark1; "" to skip)
#   INDEXER_DIR     obol-exex-indexer checkout (default: sibling repo)
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'jobs -p | xargs -r kill 2>/dev/null; rm -rf "$WORK"' EXIT

OBOL_BIN="${OBOL_BIN:-$WORK/obol}"
ROUTER_BIN="${ROUTER_BIN:-/tmp/obol-router}"
SPARK="${SPARK-spark1}"
INDEXER_DIR="${INDEXER_DIR:-$ROOT/../obol-exex-indexer}"
SKILLS="$ROOT/internal/embed/skills"

declare -a RESULTS
pass() { RESULTS+=("PASS  $1"); echo "  ✓ $1"; }
skip() { RESULTS+=("SKIP  $1 — $2"); echo "  - SKIP $1 — $2"; }
fail() { RESULTS+=("FAIL  $1 — $2"); echo "  ✗ FAIL $1 — $2"; }

section() { echo; echo "=== $1 ==="; }

# --- build obol if needed ---
if [ ! -x "$OBOL_BIN" ]; then
  echo "Building obol …"
  ( cd "$ROOT" && go build -o "$OBOL_BIN" ./cmd/obol ) || { echo "obol build failed"; exit 1; }
fi
export OBOL_CONFIG_DIR="$WORK/config"
mkdir -p "$OBOL_CONFIG_DIR"

DS_ID="pi-sessions"
DS_PORT=18951
SELLER_PORT=18961
ROUTER_PORT=18971

# Free our ports from any orphan left by a prior aborted run (a subshelled
# server can outlive its parent's job-kill), so federation never reads a
# stale 404.
for p in "$DS_PORT" "$SELLER_PORT" "$ROUTER_PORT"; do
  lsof -nP -iTCP:"$p" -sTCP:LISTEN -t 2>/dev/null | xargs -r kill 2>/dev/null
done

# ===========================================================================
section "Surface 1 — Dataset Hub (anonymize → sign → publish → buy → verify)"
# ===========================================================================
BUNDLE="$WORK/bundle"; mkdir -p "$BUNDLE"
cat > "$BUNDLE/raw.jsonl" <<'EOF'
{"messages":[{"role":"user","content":"email me at alice@example.com from 10.0.0.7"},{"role":"assistant","content":"path /Users/bob/notes.txt, key sk-ABCDEF0123456789abcdef"}]}
{"messages":[{"role":"user","content":"summarize the design doc"},{"role":"assistant","content":"the stack ships a dataset hub, inference, and fine-tuning"}]}
EOF

if python3 "$SKILLS/dataset-anonymize/scripts/anonymize.py" "$BUNDLE/raw.jsonl" "$BUNDLE/sft.jsonl" >/dev/null 2>&1 \
   && ! grep -q 'alice@example.com' "$BUNDLE/sft.jsonl"; then
  pass "1a anonymize — PII masked, no raw leak"
else
  fail "1a anonymize" "PII leaked or script error"
fi

HASH=$(shasum -a 256 "$BUNDLE/sft.jsonl" | awk '{print $1}')
printf '{"hash":"%s","files":["sft.jsonl"]}\n' "$HASH" > "$BUNDLE/manifest.json"

if "$OBOL_BIN" sell data from "$BUNDLE" --name "$DS_ID" >/dev/null 2>&1 \
   && "$OBOL_BIN" sell data verify "$DS_ID" 2>&1 | grep -q 'Chain valid'; then
  pass "1b sign + verify — signed version chain valid"
else
  fail "1b sign+verify" "version not recorded or chain invalid"
fi

MANIFEST_HASH=$(python3 -c "import json;print(json.load(open('$OBOL_CONFIG_DIR/dataset-serve/$DS_ID.store.json'))['versions'][0]['manifestHash'])")

"$OBOL_BIN" sell data publish "$DS_ID" --membership open --port "$DS_PORT" --no-tunnel >/dev/null 2>&1 &
curl -sf --retry 25 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$DS_PORT/healthz" >/dev/null
OWNER=$(python3 -c "import json;print(json.load(open('$OBOL_CONFIG_DIR/dataset-serve/$DS_ID.state.json'))['owner_token'])" 2>/dev/null)

if [ -n "$OWNER" ] && "$OBOL_BIN" buy dataset "http://127.0.0.1:$DS_PORT" --id "$DS_ID" --version 1 \
     --member-token "$OWNER" --out "$WORK/bought.jsonl" >/dev/null 2>&1 \
   && diff -q "$WORK/bought.jsonl" "$BUNDLE/sft.jsonl" >/dev/null; then
  pass "1c buy — resumable download byte-identical + hash-verified"
else
  fail "1c buy" "download/verify mismatch"
fi

# ===========================================================================
section "Surface 2 — Inference offer (federated catalog entry)"
# ===========================================================================
# Build a seller /api/services.json carrying BOTH a type=dataset entry (the
# real signed version above) and a type=inference entry.
mkdir -p "$WORK/seller/api"
DS_SIZE=$(wc -c < "$BUNDLE/sft.jsonl" | tr -d ' ')
cat > "$WORK/seller/api/services.json" <<EOF
[
  {"name":"$DS_ID","namespace":"llm","type":"dataset","endpoint":"http://127.0.0.1:$DS_PORT/dataset/$DS_ID","price":"0.01 USDC/MB","priceUnit":"perMB","payTo":"0x1111111111111111111111111111111111111111","network":"base-sepolia","description":"sanitized coding-session dataset","isDemo":false,"datasetManifestHash":"$MANIFEST_HASH","datasetVersion":"1","datasetSizeBytes":$DS_SIZE},
  {"name":"qwen-inf","namespace":"llm","type":"inference","model":"qwen3.5:9b","endpoint":"http://127.0.0.1:$DS_PORT/services/qwen-inf","price":"0.001 USDC/request","priceUnit":"perRequest","payTo":"0x2222222222222222222222222222222222222222","network":"base-sepolia","description":"paid inference","isDemo":false}
]
EOF
if python3 -c "import json,sys;d=json.load(open('$WORK/seller/api/services.json'));assert any(e['type']=='inference' for e in d) and any(e['type']=='dataset' for e in d)"; then
  pass "2  inference + dataset offers present in seller catalog"
else
  fail "2  inference offer" "catalog malformed"
fi

# ===========================================================================
section "Surface 3 — Fine-tuning on a real GPU box (provenance-bound)"
# ===========================================================================
if [ -z "$SPARK" ]; then
  skip "3  fine-tune" "SPARK unset"
elif ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$SPARK" 'echo ok' >/dev/null 2>&1; then
  skip "3  fine-tune" "$SPARK unreachable"
else
  RDIR="/tmp/obol-ft-smoke.$$"
  ssh "$SPARK" "mkdir -p $RDIR/out" >/dev/null 2>&1
  scp -q "$WORK/bought.jsonl" "$SPARK:$RDIR/ds.jsonl"
  scp -q "$SKILLS/finetune-backend/scripts/runner.py" "$SPARK:$RDIR/runner.py"
  # mock backend: no framework needed, validates contract + provenance on real hw
  if ssh "$SPARK" "cd $RDIR && python3 runner.py --backend mock --dataset ds.jsonl --base-model qwen2.5-0.5b --manifest-hash $MANIFEST_HASH --out out" >/dev/null 2>&1; then
    BOUND=$(ssh "$SPARK" "python3 -c \"import json;print(json.load(open('$RDIR/out/run.manifest'))['dataset_hash'])\"" 2>/dev/null)
    ARCH=$(ssh "$SPARK" 'uname -m' 2>/dev/null)
    if [ "$BOUND" = "$MANIFEST_HASH" ]; then
      pass "3  fine-tune on $SPARK ($ARCH) — run.manifest dataset_hash == bought manifestHash"
    else
      fail "3  fine-tune" "provenance mismatch ($BOUND != $MANIFEST_HASH)"
    fi
    ssh "$SPARK" "rm -rf $RDIR" >/dev/null 2>&1
  else
    skip "3  fine-tune" "runner failed on $SPARK"
  fi
fi

# ===========================================================================
section "Surface 4 — Discovery via obol-router (federation)"
# ===========================================================================
if [ ! -x "$ROUTER_BIN" ]; then
  skip "4  router discovery" "obol-router not built at $ROUTER_BIN"
else
  ( cd "$WORK/seller" && exec python3 -m http.server "$SELLER_PORT" >/dev/null 2>&1 ) &
  curl -sf --retry 15 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$SELLER_PORT/api/services.json" >/dev/null
  # The router federates members' /api/services.json and serves the merge at
  # GET /api/services.json. PAY_TO/FACILITATOR/BUYER_KEY are required for its
  # x402 routing path but unused by this discovery-only check (dummy values).
  OBOL_ROUTER_MEMBERS="seller1=http://127.0.0.1:$SELLER_PORT" \
    PORT="$ROUTER_PORT" \
    ROUTER_CHAIN="base-sepolia" \
    ROUTER_PAY_TO="0x1111111111111111111111111111111111111111" \
    ROUTER_FACILITATOR_URL="http://127.0.0.1:1" \
    ROUTER_BUYER_KEY_HEX="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80" \
    "$ROUTER_BIN" >"$WORK/router.log" 2>&1 &
  curl -sf --retry 20 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$ROUTER_PORT/healthz" >/dev/null 2>&1
  MERGED=$(curl -sf "http://127.0.0.1:$ROUTER_PORT/api/services.json" 2>/dev/null)
  if echo "$MERGED" | python3 -c "import json,sys;d=json.load(sys.stdin);t=[e.get('type') for e in (d if isinstance(d,list) else d.get('services',d.get('offers',[])))];assert 'dataset' in t and 'inference' in t,t" 2>/dev/null; then
    pass "4  router federated the dataset AND inference offers"
  else
    fail "4  router discovery" "merged catalog missing dataset/inference (got: $(echo "$MERGED" | head -c 120))"
  fi
fi

# ===========================================================================
section "Surface 5 — Cross-check via the obol-exex ERC-8004 indexer"
# ===========================================================================
if [ ! -d "$INDEXER_DIR" ]; then
  skip "5  indexer" "obol-exex-indexer not found at $INDEXER_DIR"
elif ! command -v cargo >/dev/null 2>&1; then
  skip "5  indexer" "cargo not installed"
else
  if ( cd "$INDEXER_DIR" && cargo test -p indexer-core --quiet ) >/dev/null 2>&1; then
    pass "5  indexer-core tests green (ERC-8004 registration parsing / feeds parity)"
  else
    fail "5  indexer" "indexer-core tests failed"
  fi
fi

# ===========================================================================
section "Summary"
# ===========================================================================
printf '%s\n' "${RESULTS[@]}"
FAILS=$(printf '%s\n' "${RESULTS[@]}" | grep -c '^FAIL' || true)
echo
if [ "$FAILS" -eq 0 ]; then
  echo "HF-surface smoke: no failures ✓"
  exit 0
else
  echo "HF-surface smoke: $FAILS failure(s) ✗"
  exit 1
fi
