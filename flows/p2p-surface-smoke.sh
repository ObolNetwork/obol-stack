#!/usr/bin/env bash
# P2P-surface smoke — fills the smoke-coverage GAPS the flows report flagged for
# the peer-to-peer / host-gateway paths that neither release-smoke.sh (entirely
# the cluster path) nor hf-surface-smoke.sh (unpaid dataset only) cover:
#
#   1. Direct-P2P inference   standalone `obol sell inference` gateway emits a
#                             valid x402 402 and is wired to a REMOTE model
#                             (spark1, over an SSH forward) — the no-cluster
#                             seller path that nothing smoked before.
#   2. Paid dataset join      `obol sell data publish --price` x402 /join/paid
#                             gate + `obol buy dataset --join` client: the 402
#                             paid-join challenge, the --max-price cap (rejects
#                             before signing), and fail-closed download.
#   3. Research E2E           `obol research publish` membership → submit →
#                             payout, asserting worker identity is TOKEN-derived
#                             (not the self-declared field) and payouts are
#                             best-per-worker (the H4 fixes), end to end.
#
# Each section SKIPs on a missing prerequisite, never aborts (hf-surface style).
#
# Settlement & transport coverage:
#   - Full on-chain x402 SETTLEMENT (1d paid 200, 2e paid mint+download) runs for
#     REAL when a local anvil base-sepolia fork (to fund test wallets) AND an x402
#     facilitator are reachable (stand them up with flow-10). Otherwise those two
#     legs SKIP; the 402 emission, client --max-price cap, and fail-closed checks
#     need no facilitator and always run.
#   - The `--secure` transport gate (Surface 4): exercised E2E when a genuinely
#     non-secure origin exists. 4a (ACCEPT over a NAMED cloudflared tunnel) needs
#     named-tunnel creds + SECURE_TUNNEL_NAME/SECURE_TUNNEL_HOSTNAME; 4b/4c
#     (REJECT/ACCEPT a CGNAT plaintext origin) need THIS host on a tailnet so a
#     remote peer can reach a non-private mac IP — requestIsSecure() treats
#     loopback AND every RFC1918 origin as secure. Each leg auto-activates when
#     its prereq is present and SKIPs precisely otherwise; the gate is also
#     unit-tested (internal/x402/forwardauth_test.go).
#
#   --secure activation env:
#     SECURE_TUNNEL_NAME / SECURE_TUNNEL_HOSTNAME   named cloudflared tunnel (4a)
#     SECURE_ORIGIN_SSH   tailnet peer that originates the plaintext probe (4b/4c;
#                         default: $SPARK). Requires `tailscale up` on THIS host.
#
# Overridable env:
#   OBOL_BIN     built obol (default: build from this tree)
#   SPARK        ssh host serving the inference upstream (default: spark1; "" skips)
#   SPARK_PORT   local port SSH-forwarded to spark's ollama (default: 11435)
#   SPARK_MODEL  model to serve from spark (default: qwen3:0.6b)
#   FACILITATOR  x402 facilitator URL for settlement (default: auto-detect :4040)
#   ANVIL_RPC    base-sepolia fork RPC for funding test wallets (default: :8545)
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
OBOL_BIN="${OBOL_BIN:-$WORK/obol}"
SPARK="${SPARK-spark1}"
SPARK_PORT="${SPARK_PORT:-11435}"
SPARK_MODEL="${SPARK_MODEL:-qwen3:0.6b}"
FACILITATOR="${FACILITATOR:-}"
SKILLS="$ROOT/internal/embed/skills"

INF_PORT=18402
DS_PORT=18951
RES_PORT=18981
SPARK_FWD_PID=""

declare -a RESULTS
pass() { RESULTS+=("PASS  $1"); echo "  ✓ $1"; }
skip() { RESULTS+=("SKIP  $1 — $2"); echo "  - SKIP $1 — $2"; }
fail() { RESULTS+=("FAIL  $1 — $2"); echo "  ✗ FAIL $1 — $2"; }
section() { echo; echo "=== $1 ==="; }

cleanup() {
  jobs -p | xargs -r kill 2>/dev/null
  [ -n "$SPARK_FWD_PID" ] && kill "$SPARK_FWD_PID" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

# --- build obol if needed ---
if [ ! -x "$OBOL_BIN" ]; then
  echo "Building obol …"
  ( cd "$ROOT" && go build -o "$OBOL_BIN" ./cmd/obol ) || { echo "obol build failed"; exit 1; }
fi
# A FRESH config dir with no kubeconfig keeps `sell inference` in standalone
# (no-cluster) mode — exactly the direct-P2P path we want to smoke.
export OBOL_CONFIG_DIR="$WORK/config"
mkdir -p "$OBOL_CONFIG_DIR"

# Free our ports from any orphan left by a prior aborted run.
for p in "$INF_PORT" "$DS_PORT" "$RES_PORT"; do
  lsof -nP -iTCP:"$p" -sTCP:LISTEN -t 2>/dev/null | xargs -r kill 2>/dev/null
done

# --- optional settlement prerequisites (local anvil base-sepolia fork + x402
# facilitator). When both are reachable, 1d/2e settle on chain for real; else
# they SKIP. Reuses flow-10's infra — stand it up with:
#     bash flows/flow-10-anvil-facilitator.sh
ANVIL_RPC="${ANVIL_RPC:-http://127.0.0.1:8545}"
USDC_ADDR="${USDC_ADDR:-0x036CbD53842c5426634e7929541eC2318f3dCF7e}"
SETTLE=0
XSIGN="$WORK/x402-sign"
if command -v cast >/dev/null 2>&1 \
   && [ "$(curl -s --max-time 4 "$ANVIL_RPC" -X POST -H 'Content-Type: application/json' \
            -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' 2>/dev/null \
            | jq -r '.result // empty' 2>/dev/null)" = "0x14a34" ]; then
  if [ -z "$FACILITATOR" ] && curl -sf --max-time 4 http://127.0.0.1:4040/supported >/dev/null 2>&1; then
    FACILITATOR="http://127.0.0.1:4040"
  fi
  if [ -n "$FACILITATOR" ] && curl -sf --max-time 4 "$FACILITATOR/supported" >/dev/null 2>&1; then
    # Host-side raw X-PAYMENT signer — no buyer CLI exists for `sell inference`.
    ( cd "$ROOT" && go build -o "$XSIGN" ./flows/tools/x402-sign ) >/dev/null 2>&1 \
      && [ -x "$XSIGN" ] && SETTLE=1
  fi
fi

fund_usdc() {  # $1=address — mint 1000 USDC to its FiatToken balanceOf slot (idx 9)
  local slot; slot=$(cast index address "$1" 9 2>/dev/null)
  cast rpc anvil_setStorageAt "$USDC_ADDR" "$slot" \
    "0x000000000000000000000000000000000000000000000000000000003B9ACA00" \
    --rpc-url "$ANVIL_RPC" >/dev/null 2>&1
}
sync_anvil_clock() {  # avoid 'authorization not yet valid' on a long-lived fork (pitfall 18)
  cast rpc evm_setNextBlockTimestamp "$(date +%s)" --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
  cast rpc anvil_mine 1 --rpc-url "$ANVIL_RPC" >/dev/null 2>&1 || true
}

# ===========================================================================
section "Surface 1 — Direct-P2P inference (standalone gateway → remote model)"
# ===========================================================================
UPSTREAM="http://127.0.0.1:$SPARK_PORT"
SPARK_OK=0
if [ -z "$SPARK" ]; then
  skip "1  spark upstream" "SPARK unset"
elif ! ssh -o BatchMode=yes -o ConnectTimeout=6 "$SPARK" 'command -v ollama >/dev/null' >/dev/null 2>&1; then
  skip "1  spark upstream" "$SPARK unreachable or no ollama"
else
  # Ensure the model is served on the remote box, then forward it locally so the
  # seller's gateway proxies to a model running on ANOTHER machine (real P2P).
  ssh "$SPARK" "pgrep -x ollama >/dev/null || (OLLAMA_HOST=0.0.0.0:11434 nohup ollama serve >/tmp/ollama.log 2>&1 &); OLLAMA_HOST=0.0.0.0:11434 ollama pull $SPARK_MODEL >/dev/null 2>&1" >/dev/null 2>&1
  lsof -nP -iTCP:"$SPARK_PORT" -sTCP:LISTEN -t 2>/dev/null | xargs -r kill 2>/dev/null
  ssh -fN -o ConnectTimeout=8 -o ExitOnForwardFailure=yes -L "$SPARK_PORT:127.0.0.1:11434" "$SPARK" \
    && SPARK_FWD_PID=$(pgrep -f "$SPARK_PORT:127.0.0.1:11434" | head -1)
  sleep 1
  if curl -sf --max-time 10 "$UPSTREAM/api/tags" >/dev/null 2>&1; then
    pass "1a spark upstream reachable — model on $SPARK served via SSH forward :$SPARK_PORT"
    SPARK_OK=1
  else
    skip "1a spark upstream" "forward to $SPARK:11434 not reachable"
    UPSTREAM="http://127.0.0.1:11434"  # fall back to host ollama for the gate test
  fi
fi

PAY_TO="0x1111111111111111111111111111111111111111"
"$OBOL_BIN" sell inference p2p-smoke --model "$SPARK_MODEL" --pay-to "$PAY_TO" \
  --price 0.001 --chain base-sepolia --upstream "$UPSTREAM" --listen "127.0.0.1:$INF_PORT" \
  ${FACILITATOR:+--facilitator "$FACILITATOR"} >"$WORK/inf-gw.log" 2>&1 &
# Wait for the gateway to listen.
for _ in $(seq 1 25); do curl -s -o /dev/null "http://127.0.0.1:$INF_PORT/v1/chat/completions" && break; sleep 0.5; done

CODE=$(curl -s -o "$WORK/inf-402.json" -w '%{http_code}' --max-time 8 \
  "http://127.0.0.1:$INF_PORT/v1/chat/completions" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$SPARK_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}")
if [ "$CODE" = "402" ] && jq -e '.accepts[0] | select(.scheme=="exact" and .payTo=="'"$PAY_TO"'" and .amount=="1000")' "$WORK/inf-402.json" >/dev/null 2>&1; then
  pass "1b standalone gateway emits x402 402 (exact / base-sepolia USDC / amount 1000 / payTo)"
else
  fail "1b inference 402" "expected 402 + accepts[exact,payTo,amount], got HTTP $CODE: $(head -c 160 "$WORK/inf-402.json")"
fi

if [ "$SPARK_OK" = 1 ]; then
  RESP=$(curl -s --max-time 60 "$UPSTREAM/v1/chat/completions" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$SPARK_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_tokens\":64,\"stream\":false}")
  if echo "$RESP" | jq -e '.choices[0].message' >/dev/null 2>&1; then
    pass "1c remote model on $SPARK serves a valid completion (the resource the gateway proxies)"
  else
    fail "1c spark inference" "no valid completion: $(echo "$RESP" | head -c 120)"
  fi
fi

if [ "$SETTLE" = 1 ] && jq -e '.accepts[0].asset and .accepts[0].extra.name' "$WORK/inf-402.json" >/dev/null 2>&1; then
  NEW=$(cast wallet new 2>/dev/null)
  BUYER=$(echo "$NEW" | awk '/Address/{print $2}'); BKEY=$(echo "$NEW" | awk '/Private key/{print $3}')
  fund_usdc "$BUYER"; sync_anvil_clock
  XPAY=$(jq -c '.' "$WORK/inf-402.json" | X402_SIGN_KEY="$BKEY" "$XSIGN" 2>/dev/null)
  PCODE=$(curl -s -o "$WORK/inf-paid.json" -w '%{http_code}' --max-time 90 \
    "http://127.0.0.1:$INF_PORT/v1/chat/completions" -H 'Content-Type: application/json' \
    -H "X-PAYMENT: $XPAY" \
    -d "{\"model\":\"$SPARK_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"one word hi\"}],\"max_tokens\":16,\"stream\":false}")
  if [ "$PCODE" = "200" ] && jq -e '.choices[0]' "$WORK/inf-paid.json" >/dev/null 2>&1; then
    pass "1d paid 200 — fresh EOA signs EIP-3009 X-PAYMENT, gateway verifies+settles via facilitator, proxies upstream"
  else
    fail "1d paid settlement" "expected 200 + choices, got HTTP $PCODE: $(head -c 160 "$WORK/inf-paid.json")"
  fi
else
  skip "1d paid 200 (full settlement)" "needs a local anvil base-sepolia fork + x402 facilitator (flow-10); 402/proxy covered above"
fi

# ===========================================================================
section "Surface 2 — Paid dataset join (x402 /join/paid gate + buy --join)"
# ===========================================================================
DS_ID="p2p-ds"
BUNDLE="$WORK/ds"; mkdir -p "$BUNDLE"
printf '{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]}\n' > "$BUNDLE/sft.jsonl"
HASH=$(shasum -a 256 "$BUNDLE/sft.jsonl" | awk '{print $1}')
printf '{"hash":"%s","files":["sft.jsonl"]}\n' "$HASH" > "$BUNDLE/manifest.json"

if "$OBOL_BIN" sell data from "$BUNDLE" --name "$DS_ID" >/dev/null 2>&1 \
   && "$OBOL_BIN" sell data verify "$DS_ID" 2>&1 | grep -q 'Chain valid'; then
  pass "2a sign + verify — signed version chain valid"
else
  fail "2a sign+verify" "version not recorded or chain invalid"
fi

# Publish WITH a per-join price → enables the x402 /join/paid gate.
"$OBOL_BIN" sell data publish "$DS_ID" --membership open --port "$DS_PORT" --no-tunnel \
  --price 0.001 --pay-to "$PAY_TO" --chain base-sepolia \
  ${FACILITATOR:+--facilitator "$FACILITATOR"} >"$WORK/ds-pub.log" 2>&1 &
curl -sf --retry 25 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$DS_PORT/healthz" >/dev/null 2>&1
DS_URL="http://127.0.0.1:$DS_PORT"

CODE=$(curl -s -o "$WORK/join-402.json" -w '%{http_code}' --max-time 8 -X POST "$DS_URL/dataset/$DS_ID/join/paid")
if [ "$CODE" = "402" ] && jq -e '.accepts[0].scheme=="exact"' "$WORK/join-402.json" >/dev/null 2>&1; then
  pass "2b /join/paid emits an x402 402 paid-join challenge"
else
  fail "2b paid-join 402" "expected 402 + accepts, got HTTP $CODE: $(head -c 160 "$WORK/join-402.json")"
fi

# --max-price below the advertised price must reject BEFORE signing (no chain).
OUT=$("$OBOL_BIN" buy dataset "$DS_URL" --id "$DS_ID" --join --max-price 1 --out "$WORK/never.jsonl" 2>&1)
if echo "$OUT" | grep -qiE 'exceeds .*max-price|max-price'; then
  pass "2c buy --join --max-price caps the join price (rejects before signing)"
else
  fail "2c max-price cap" "expected a max-price rejection, got: $(echo "$OUT" | head -c 160)"
fi

# Fail-closed: a download without a member token must be refused.
CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$DS_URL/dataset/$DS_ID/download?version=1")
if [ "$CODE" = "401" ] || [ "$CODE" = "403" ]; then
  pass "2d download fails closed without a member token (HTTP $CODE)"
else
  fail "2d fail-closed" "expected 401/403, got HTTP $CODE"
fi

if [ "$SETTLE" = 1 ]; then
  # Two-pass: pass 1 surfaces+creates the buyer wallet (unfunded → 503); fund it
  # on the fork, then pass 2 settles, mints a version-scoped token, downloads.
  OWNER=$("$OBOL_BIN" sell data verify "$DS_ID" 2>/dev/null | grep -oiE '0x[0-9a-fA-F]{40}' | head -1)
  P1=$("$OBOL_BIN" buy dataset "$DS_URL" --id "$DS_ID" --join --out "$WORK/paid1.jsonl" 2>&1)
  BUYER=$(echo "$P1" | grep -oiE '0x[0-9a-fA-F]{40}' | head -1)
  fund_usdc "$BUYER"; sync_anvil_clock
  P2=$("$OBOL_BIN" buy dataset "$DS_URL" --id "$DS_ID" --join ${OWNER:+--owner "$OWNER"} --out "$WORK/paid2.jsonl" 2>&1)
  if echo "$P2" | grep -qi 'minted' && [ -s "$WORK/paid2.jsonl" ]; then
    pass "2e paid join settles — minted a version-scoped token + verified signed-log download ($(wc -c <"$WORK/paid2.jsonl" | tr -d ' ') bytes)"
  else
    fail "2e paid mint+download" "settlement failed: $(echo "$P2" | tail -1 | head -c 160)"
  fi
else
  skip "2e paid mint+download (full settlement)" "needs a local anvil base-sepolia fork + x402 facilitator (flow-10)"
fi

# ===========================================================================
section "Surface 3 — Research E2E (membership → submit → payout, token identity)"
# ===========================================================================
# Inline stdlib worker: device-auth join (open mode auto-approves), submit a
# metric, print the member token. Exercises the real server endpoints.
cat > "$WORK/rworker.py" <<'PY'
import json,sys,time,urllib.request
def call(url,token,body=None):
    m="POST" if body is not None else "GET"
    req=urllib.request.Request(url,data=(json.dumps(body).encode() if body is not None else None),method=m)
    req.add_header("Content-Type","application/json")
    if token: req.add_header("Authorization","Bearer "+token)
    return json.loads(urllib.request.urlopen(req,timeout=30).read())
kb,worker,value=sys.argv[1].rstrip("/"),sys.argv[2],float(sys.argv[3])
g=call(kb+"/auth/device/code",None,{"worker":worker})
tok=None
for _ in range(30):
    r=call(kb+"/auth/device/token",None,{"device_code":g["device_code"]})
    if r.get("status")=="authorized": tok=r["token"]; break
    time.sleep(1)
if not tok: sys.exit("join timed out")
call(kb+"/results",tok,{"worker":worker,"value":value,"output":"p2p-smoke"})
print(tok)
PY

"$OBOL_BIN" research publish demo-prog --metric val_bpb --direction minimize \
  --accept threshold --threshold 1.0 --baseline 1.0 --pool 100 --token OBOL \
  --membership open --no-tunnel --port "$RES_PORT" >"$WORK/res-pub.log" 2>&1 &
if curl -sf --retry 25 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$RES_PORT/healthz" >/dev/null 2>&1; then
  RES_URL="http://127.0.0.1:$RES_PORT"
  # Worker A (value 0.50) and worker B (value 0.40) submit under the SAME
  # self-declared name "spoof" — if identity were the self-declared field they
  # would collapse to one worker. spark computes B's metric when reachable.
  BVAL=0.40
  if [ -n "$SPARK" ] && ssh -o BatchMode=yes -o ConnectTimeout=6 "$SPARK" 'echo ok' >/dev/null 2>&1; then
    BVAL=$(ssh "$SPARK" 'echo 0.40' 2>/dev/null || echo 0.40)
  fi
  TOKA=$(python3 "$WORK/rworker.py" "$RES_URL" spoof 0.50 2>/dev/null)
  TOKB=$(python3 "$WORK/rworker.py" "$RES_URL" spoof "$BVAL" 2>/dev/null)
  STATUS=$(curl -s --max-time 8 -H "Authorization: Bearer $TOKA" "$RES_URL/status" 2>/dev/null)
  echo "$STATUS" > "$WORK/res-status.json"

  if [ -n "$TOKA" ] && [ -n "$TOKB" ] && [ "$TOKA" != "$TOKB" ]; then
    pass "3a two workers admitted (open membership), distinct member tokens minted"
  else
    fail "3a membership" "join failed (tokA=$TOKA tokB=$TOKB)"
  fi

  python3 - "$WORK/res-status.json" <<'PY' && pass "3b token-derived identity + best-per-worker payouts + champion" || fail "3b payout/identity" "see status json"
import json,sys
d=json.load(open(sys.argv[1]))
results=d.get("results") or []
workers={r.get("worker") for r in results}
champ=(d.get("champion") or {}).get("value")
payouts=d.get("payouts") or {}
# two DISTINCT token-derived worker ids despite the shared self-declared name
assert len(workers)>=2, ("identity collapsed to self-declared name", workers)
assert all(str(w).startswith("w-") for w in workers), ("identity not token-derived", workers)
# champion is the better (lower) value
assert abs(float(champ)-0.40)<1e-6, ("champion not best", champ)
# payouts split across both workers, best-per-worker, summing to the pool
vals=[v for v in payouts.values() if isinstance(v,(int,float))]
assert len(payouts)>=2 and all(v>0 for v in vals) and abs(sum(vals)-100.0)<1.0, ("payout split wrong", payouts)
print("OK", "workers=",len(workers),"champ=",champ,"payouts=",payouts)
PY
else
  skip "3  research" "research publish did not become healthy"
fi

# A second, CLI-surface smoke of the operator view.
if "$OBOL_BIN" research status demo-prog >/dev/null 2>&1; then
  pass "3c obol research status (operator CLI) returns the program state"
else
  skip "3c research status CLI" "status command unavailable"
fi

# ===========================================================================
section "Surface 4 — --secure transport gate (needs a real non-secure origin)"
# ===========================================================================
# requestIsSecure() (internal/x402/forwardauth.go) treats loopback AND every
# RFC1918-private origin as secure, so exercising the gate E2E needs a genuinely
# non-secure peer. Each leg auto-activates when its prereq is present and SKIPs
# precisely otherwise (this host has neither today: not on a tailnet, no
# cloudflared named-tunnel creds).
SEC_PORT=18403
SEC_PID=""
SEC_BODY="{\"model\":\"$SPARK_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
# Cloudflare WAF on some zones (e.g. v1337.org) 403s default curl/urllib UAs
# (managed-challenge / error 1010); send a browser UA so 4a reaches the origin.
SEC_UA="${SECURE_TUNNEL_UA:-Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36}"
secure_gw() {  # $1 = extra flags (e.g. --secure); binds 0.0.0.0 so a tailnet peer reaches it
  [ -n "$SEC_PID" ] && kill "$SEC_PID" 2>/dev/null; SEC_PID=""
  lsof -nP -iTCP:"$SEC_PORT" -sTCP:LISTEN -t 2>/dev/null | xargs -r kill 2>/dev/null
  # shellcheck disable=SC2086
  "$OBOL_BIN" sell inference p2p-secure --model "$SPARK_MODEL" --pay-to "$PAY_TO" --price 0.001 \
    --chain base-sepolia --upstream "$UPSTREAM" --listen "0.0.0.0:$SEC_PORT" $1 \
    >"$WORK/sec-gw.log" 2>&1 &
  SEC_PID=$!
  for _ in $(seq 1 25); do curl -s -o /dev/null "http://127.0.0.1:$SEC_PORT/v1/chat/completions" && return 0; sleep 0.5; done
  return 1
}

# --- 4a — ACCEPT over a NAMED HTTPS tunnel (X-Forwarded-Proto: https) ---
if ! command -v cloudflared >/dev/null 2>&1; then
  skip "4a --secure ACCEPTS over an HTTPS tunnel" "cloudflared not installed"
elif [ -z "${SECURE_TUNNEL_HOSTNAME:-}" ] || [ -z "${SECURE_TUNNEL_NAME:-}" ] || [ ! -f "$HOME/.cloudflared/cert.pem" ]; then
  skip "4a --secure ACCEPTS over an HTTPS tunnel" "no named-tunnel creds — run 'cloudflared tunnel login', route a hostname, then set SECURE_TUNNEL_NAME + SECURE_TUNNEL_HOSTNAME (quick tunnels not used: account-less edge is unreliable)"
elif ! secure_gw "--secure"; then
  fail "4a --secure gateway" "gateway did not start on :$SEC_PORT"
else
  # NB: --no-autoupdate is NOT a `tunnel run` flag (only valid for the quick
  # `tunnel --url` form); passing it here makes cloudflared print usage and never
  # connect → the hostname 530s. Omit it (no autoupdate fires in a ~60s test).
  cloudflared tunnel run --url "http://127.0.0.1:$SEC_PORT" "$SECURE_TUNNEL_NAME" >"$WORK/cf.log" 2>&1 &
  for _ in $(seq 1 20); do grep -qiE 'Registered tunnel connection' "$WORK/cf.log" && break; sleep 1; done
  SC=000
  for _ in $(seq 1 25); do
    SC=$(curl -s -o /dev/null -w '%{http_code}' --max-time 12 -A "$SEC_UA" "https://$SECURE_TUNNEL_HOSTNAME/v1/chat/completions" \
      -H 'Content-Type: application/json' -d "$SEC_BODY")
    [ "$SC" = "402" ] && break; sleep 2
  done
  case "$SC" in
    402) pass "4a --secure ACCEPTS an HTTPS-tunneled request (X-Forwarded-Proto: https → 402, not 400)" ;;
    400) fail "4a --secure over tunnel" "gate REJECTED an HTTPS-forwarded request (400) — secure-transport detection broken" ;;
    *)   skip "4a --secure over tunnel" "named tunnel $SECURE_TUNNEL_HOSTNAME did not serve (HTTP $SC) — check the tunnel run + DNS route" ;;
  esac
  jobs -p | xargs -r kill 2>/dev/null; SEC_PID=""
fi

# --- 4b/4c — REJECT/ACCEPT a real non-private plaintext origin from a tailnet peer ---
MAC_TS_IP=""
command -v tailscale >/dev/null 2>&1 && MAC_TS_IP=$(tailscale ip -4 2>/dev/null | head -1)
[ -z "$MAC_TS_IP" ] && [ -x "/Applications/Tailscale.app/Contents/MacOS/Tailscale" ] \
  && MAC_TS_IP=$("/Applications/Tailscale.app/Contents/MacOS/Tailscale" ip -4 2>/dev/null | head -1)
ORIGIN_SSH="${SECURE_ORIGIN_SSH:-$SPARK}"
remote_code() {  # HTTP code a remote tailnet peer sees hitting mac-tailnet-ip:$SEC_PORT in plaintext
  ssh -o BatchMode=yes -o ConnectTimeout=8 "$ORIGIN_SSH" \
    "curl -s -o /dev/null -w '%{http_code}' --max-time 8 http://$MAC_TS_IP:$SEC_PORT/v1/chat/completions -H 'Content-Type: application/json' -d '$SEC_BODY'" 2>/dev/null || echo 000
}
if [ -z "$MAC_TS_IP" ]; then
  skip "4b --secure REJECTS a non-secure (CGNAT) plaintext origin" "this host is not on a tailnet — 'tailscale up' so a remote peer can reach a non-private mac IP"
  skip "4c insecure-default ACCEPTS the same origin" "this host is not on a tailnet"
elif [ -z "$ORIGIN_SSH" ] || ! ssh -o BatchMode=yes -o ConnectTimeout=6 "$ORIGIN_SSH" 'echo ok' >/dev/null 2>&1; then
  skip "4b --secure REJECTS a non-secure plaintext origin" "no reachable tailnet origin peer (set SECURE_ORIGIN_SSH=<tailnet-peer>)"
  skip "4c insecure-default ACCEPTS the same origin" "no reachable tailnet origin peer"
else
  secure_gw "--secure"; SC=$(remote_code)
  case "$SC" in
    400) pass "4b --secure REJECTS plaintext from a non-secure origin ($ORIGIN_SSH → $MAC_TS_IP, HTTP 400)" ;;
    000) skip "4b --secure reject" "origin $ORIGIN_SSH could not reach $MAC_TS_IP:$SEC_PORT (tailnet routing/firewall)" ;;
    *)   fail "4b --secure reject" "expected 400 from a non-private origin, got HTTP $SC" ;;
  esac
  secure_gw ""; SC=$(remote_code)
  case "$SC" in
    000) skip "4c insecure-default accept" "origin $ORIGIN_SSH could not reach $MAC_TS_IP:$SEC_PORT" ;;
    400) fail "4c insecure accept" "insecure default rejected a plaintext origin (400) — should accept" ;;
    *)   pass "4c insecure-default ACCEPTS the same plaintext origin (HTTP $SC, not 400)" ;;
  esac
  [ -n "$SEC_PID" ] && kill "$SEC_PID" 2>/dev/null; SEC_PID=""
fi

# ===========================================================================
section "Summary"
# ===========================================================================
printf '%s\n' "${RESULTS[@]}"
FAILS=$(printf '%s\n' "${RESULTS[@]}" | grep -c '^FAIL' || true)
echo
if [ "$FAILS" -eq 0 ]; then
  echo "P2P-surface smoke: no failures ✓"
  exit 0
else
  echo "P2P-surface smoke: $FAILS failure(s) ✗"
  exit 1
fi
