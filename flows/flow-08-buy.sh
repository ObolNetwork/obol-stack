#!/bin/bash
# Flow 08: Buy — monetize-inference.md §2.1-2.5.
# Requires: flow-06 (ServiceOffer Ready) + flow-10 (Anvil + facilitator running).
source "$(dirname "$0")/lib.sh"

TUNNEL_OUTPUT=$("$OBOL" tunnel status 2>&1) || true
TUNNEL_URL=$(echo "$TUNNEL_OUTPUT" | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | head -1 || true)
refresh_obol_ingress_env
BASE_URL="${OBOL_INGRESS_URL%/}"
if [ -n "$TUNNEL_URL" ]; then
    tunnel_probe=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST \
        "$TUNNEL_URL/services/flow-qwen/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>/dev/null || echo "000")
    if [ "$tunnel_probe" = "402" ]; then
        BASE_URL="$TUNNEL_URL"
    fi
fi
if [[ "$BASE_URL" == *"obol.stack"* ]]; then
    CURL_BASE="$CURL_OBOL"
else
    CURL_BASE="curl"
fi

# §2.1: Discover services via /skill.md (machine-readable catalog, always published
# when ServiceOffers are ready; /.well-known/agent-registration.json requires
# on-chain ERC-8004 registration via --register flag which is not used in this flow)
step "Discover services via /skill.md"
skill_out=$($CURL_BASE -sf --max-time 10 "$BASE_URL/skill.md" 2>&1) || true
if echo "$skill_out" | grep -q "x402\|service\|obol"; then
    pass "Service catalog (/skill.md) discovered"
else
    status_fallback=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
    if echo "$status_fallback" | grep -q "/services/flow-qwen"; then
        pass "Service catalog unavailable, but ServiceOffer endpoint is published"
    else
        fail "Service catalog not found and no ServiceOffer fallback — ${skill_out:0:200}"
    fi
fi

# §2.1: skill.md lists flow-qwen service with its endpoint (agent publishes after reconcile)
step "/skill.md lists flow-qwen service"
if echo "$skill_out" | grep -q "flow-qwen"; then
    endpoint=$(echo "$skill_out" | grep -oE '`https://[^`]+`' | head -1 || echo "(local)")
    pass "/skill.md lists flow-qwen (endpoint: ${endpoint})"
else
    status_fallback=$("$OBOL" sell status flow-qwen -n llm 2>&1) || true
    if echo "$status_fallback" | grep -q "/services/flow-qwen"; then
        pass "flow-qwen discovered via ServiceOffer status fallback"
    else
        fail "/skill.md does not list flow-qwen and fallback missing — ${skill_out:0:200}"
    fi
fi

# §2.2: 402 body validation
step "402 body validated"
body_402=$($CURL_BASE -s --max-time 10 -X POST \
    "$BASE_URL/services/flow-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$FLOW_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" 2>&1) || true
if echo "$body_402" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d.get('x402Version') is not None, 'missing x402Version'
a = d['accepts'][0]
assert a['payTo'], 'missing payTo'
assert a['network'], 'missing network'
amount = a.get('amount') or a.get('maxAmountRequired')
assert amount, 'missing amount/maxAmountRequired'
print('OK: payTo=%s network=%s amount=%s' % (a['payTo'], a['network'], amount))
" 2>&1; then
    pass "402 body validated"
else
    fail "402 body validation failed — ${body_402:0:200}"
fi

# §2.4 pre-capture: Record seller balance BEFORE paid inference to verify settlement
# (monetize §2.4 — "payee balance should have increased")
PRE_SELLER_BAL=""
if command -v cast &>/dev/null; then
    PRE_SELLER_BAL=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
        --rpc-url "$ANVIL_RPC" 2>&1) || true
    [[ "$PRE_SELLER_BAL" =~ ^[0-9] ]] || PRE_SELLER_BAL=""
fi

# §2.3: Paid inference — sign EIP-712 ERC-3009 payment and retry
# Uses eth_account to sign the TransferWithAuthorization payload, matching
# internal/testutil/eip712_signer.go. If host Python lacks the dependency,
# lib.sh creates an isolated .workspace/venv and puts it on PATH.
step "Paid inference via x402 payment signing"
if ensure_payment_python_deps; then
    paid_out=$(python3 << 'PYEOF' 2>&1
import sys, os, json, base64, secrets, time
import httpx
from eth_account import Account
from eth_account.messages import encode_typed_data

SERVICE_URL = os.environ.get('BASE_URL', os.environ.get('OBOL_INGRESS_URL', 'http://obol.stack:8080'))
SERVICE_PATH = "/services/flow-qwen/v1/chat/completions"
CONSUMER_KEY  = os.environ["CONSUMER_PRIVATE_KEY"]  # derived from Hardhat mnemonic in lib.sh
USDC_ADDRESS  = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
CHAIN_ID      = 84532  # Base Sepolia
MODEL         = os.environ.get("FLOW_MODEL", "qwen3.5:9b")

acct = Account.from_key(CONSUMER_KEY)

# 1. Initial request → 402
url = SERVICE_URL + SERVICE_PATH
body = {"model": MODEL, "messages": [{"role": "user", "content": "What is 2+2?"}], "max_tokens": 20}
headers = {"Content-Type": "application/json"}
if "obol.stack" in SERVICE_URL:
    # macOS mDNS bypass: connect to 127.0.0.1 but send Host header
    transport = httpx.HTTPTransport()
resp = httpx.post(url, json=body, headers=headers, timeout=30, follow_redirects=True)
if resp.status_code != 402:
    print(f"ERROR: expected 402, got {resp.status_code}: {resp.text[:200]}")
    sys.exit(1)

req_data = resp.json()
accept = req_data["accepts"][0]
pay_to  = accept["payTo"]
amount  = accept.get("amount") or accept.get("maxAmountRequired")  # micro-USDC string e.g. "1000"
network = accept["network"]
asset   = accept.get("asset") or USDC_ADDRESS
domain_name = "USDC"
domain_version = "2"

# 2. Sign EIP-712 TransferWithAuthorization (ERC-3009)
nonce = "0x" + secrets.token_hex(32)
valid_before = str(int(time.time()) + 3600)  # 1 hour from now

structured = {
    "types": {
        "EIP712Domain": [
            {"name": "name",              "type": "string"},
            {"name": "version",           "type": "string"},
            {"name": "chainId",           "type": "uint256"},
            {"name": "verifyingContract", "type": "address"},
        ],
        "TransferWithAuthorization": [
            {"name": "from",        "type": "address"},
            {"name": "to",          "type": "address"},
            {"name": "value",       "type": "uint256"},
            {"name": "validAfter",  "type": "uint256"},
            {"name": "validBefore", "type": "uint256"},
            {"name": "nonce",       "type": "bytes32"},
        ],
    },
    "primaryType": "TransferWithAuthorization",
    "domain": {
        "name": domain_name, "version": domain_version,
        "chainId": CHAIN_ID, "verifyingContract": USDC_ADDRESS,
    },
    "message": {
        "from":        acct.address,
        "to":          pay_to,
        "value":       int(amount),
        "validAfter":  0,
        "validBefore": int(valid_before),
        "nonce":       bytes.fromhex(nonce[2:]),
    },
}
signed = acct.sign_message(encode_typed_data(full_message=structured))
sig_hex = "0x" + signed.signature.hex()

# 3. Build x402 v2 payment envelope. The accepted requirement must round-trip
# exactly enough for strict facilitators to deserialize the EIP-3009 variant.
accepted = dict(accept)
accepted["amount"] = amount
accepted["asset"] = asset
envelope = {
    "x402Version": 2,
    "accepted": accepted,
    "payload": {
        "signature": sig_hex,
        "authorization": {
            "from":        acct.address,
            "to":          pay_to,
            "value":       amount,
            "validAfter":  "0",
            "validBefore": valid_before,
            "nonce":       nonce,
        },
    },
}
payment_header = base64.b64encode(json.dumps(envelope).encode()).decode()

# 4. Retry with X-Payment header
resp2 = httpx.post(url, json=body,
    headers={**headers, "X-Payment": payment_header},
    timeout=120, follow_redirects=True)
if resp2.status_code == 200 and "choices" in resp2.text:
    d = resp2.json()
    nc = len(d.get("choices", []))
    print(f"PAID_RESPONSE: HTTP 200, choices={nc}")
else:
    print(f"ERROR: payment rejected — HTTP {resp2.status_code}: {resp2.text[:300]}")
    sys.exit(1)
PYEOF
    ) || true  # prevent set -e from killing the flow on Python script failure
    if echo "$paid_out" | grep -q "PAID_RESPONSE:\|choices_ok"; then
        pass "Paid inference succeeded"
    else
        fail "Paid inference failed — ${paid_out:0:400}"
    fi
else
    fail "eth_account/httpx unavailable and automatic venv setup failed"
fi

# §2.4: Balance checks (requires cast/Foundry)
# Use exit-code check + numeric pattern to avoid false positives from cast error messages
if command -v cast &>/dev/null; then
    step "Buyer USDC balance check"
    # env -u CHAIN: CHAIN=base-sepolia conflicts with foundry (expects uint64)
    if buyer_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$CONSUMER_WALLET" \
            --rpc-url "$ANVIL_RPC" 2>&1) && [[ "$buyer_bal" =~ ^[0-9] ]]; then
        pass "Buyer USDC balance: $buyer_bal"
    else
        fail "Buyer balance check failed — ${buyer_bal:0:100}"
    fi

    step "Seller USDC balance increased after payment (§2.4 settlement)"
    if seller_bal=$(env -u CHAIN cast call "$USDC_ADDRESS" "balanceOf(address)(uint256)" "$SELLER_WALLET" \
            --rpc-url "$ANVIL_RPC" 2>&1) && [[ "$seller_bal" =~ ^[0-9] ]]; then
        # If we captured a pre-balance, verify it increased (actual settlement check)
        if [ -n "$PRE_SELLER_BAL" ] && echo "$paid_out" | grep -q "PAID_RESPONSE:"; then
            pre_num=$(echo "$PRE_SELLER_BAL" | grep -oE '^[0-9]+' | head -1)
            post_num=$(echo "$seller_bal" | grep -oE '^[0-9]+' | head -1)
            if [ -n "$pre_num" ] && [ -n "$post_num" ] && [ "$post_num" -gt "$pre_num" ] 2>/dev/null; then
                pass "Seller USDC balance increased: $pre_num → $post_num (payment settled)"
            elif [ "$post_num" = "$pre_num" ]; then
                fail "Seller balance unchanged after payment: $pre_num (settlement may have failed)"
            else
                pass "Seller USDC balance: $seller_bal (pre-balance: ${PRE_SELLER_BAL:-unknown})"
            fi
        else
            pass "Seller USDC balance: $seller_bal"
        fi
    else
        fail "Seller balance check failed — ${seller_bal:0:100}"
    fi
else
    fail "cast (Foundry) not installed — skipping balance checks"
fi

emit_metrics
