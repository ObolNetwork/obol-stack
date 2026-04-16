# Monetize Sell-Side Testing Log

Full lifecycle walkthrough of the hardened monetize subsystem on a fresh dev cluster, using the real x402-rs facilitator against an Anvil fork of base-sepolia.

**Branch**: `fix/review-hardening` (off `feat/secure-enclave-inference`)
**Date**: 2026-02-27
**Cluster**: `obol-stack-sweeping-man` (k3d, 1 server node)

---

## Prerequisites

```bash
# Working directory: the obol-stack repo (or worktree)
cd /path/to/obol-stack

# Environment — set these in every terminal session
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data

# Alias for brevity (optional)
alias obol="$OBOL_BIN_DIR/obol"
```

**External dependencies** (must be installed separately):

| Dependency | Install | Purpose |
|-----------|---------|---------|
| Docker | [docker.com](https://docker.com) | k3d runs inside Docker |
| Foundry (`anvil`, `cast`) | `curl -L https://foundry.paradigm.xyz \| bash && foundryup` | Local base-sepolia fork |
| Rust toolchain | [rustup.rs](https://rustup.rs) | Building x402-rs facilitator |
| Python 3 + venv | System package manager | Signing the EIP-712 payment header |
| x402-rs | `git clone https://github.com/x402-rs/x402-rs ~/Development/R&D/x402-rs` | Real x402 facilitator |
| Ollama | [ollama.com](https://ollama.com) | Local LLM inference (must be running on host) |
| `/etc/hosts` entry | `echo "127.0.0.1 obol.stack" \| sudo tee -a /etc/hosts` | `obolup.sh` does this, or add manually |

---

## Phase 1: Build & Cluster

```bash
# 1. Build the obol binary from the hardened branch
go build -o .workspace/bin/obol ./cmd/obol

# 2. Wipe any previous cluster
obol stack down 2>/dev/null; obol stack purge -f 2>/dev/null
rm -rf "$OBOL_CONFIG_DIR" "$OBOL_DATA_DIR"

# 3. Initialize fresh cluster config
obol stack init

# 4. Bring up the cluster
#    (builds x402-verifier Docker image locally, deploys all infrastructure)
obol stack up

# 5. Verify — all pods should be Running
obol kubectl get pods -A
```

Expected: ~18 pods across namespaces (`erpc`, `kube-system`, `llm`, `monitoring`, `obol-frontend`, `openclaw-default`, `reloader`, `traefik`, `x402`). x402-verifier should have **2 replicas**.

---

## Phase 2: Verify Hardening

```bash
# Split RBAC ClusterRoles exist
obol kubectl get clusterrole openclaw-monetize-read
obol kubectl get clusterrole openclaw-monetize-workload

# x402 namespace Role exists
obol kubectl get role openclaw-x402-pricing -n x402

# x402 HA: 2 replicas
obol kubectl get deploy x402-verifier -n x402 -o jsonpath='{.spec.replicas}'
# → 2

# PDB active
obol kubectl get pdb -n x402
# → x402-verifier   minAvailable=1   allowedDisruptions=1
```

---

## Phase 3: Deploy Agent

```bash
# 6. Deploy the obol-agent singleton
#    - creates namespace openclaw-obol-agent
#    - deploys openclaw + remote-signer pods
#    - injects 24 skills (including monetize)
#    - patches all 3 RBAC bindings to the agent's ServiceAccount
obol agent init

# 7. Verify RBAC bindings point to the agent's ServiceAccount
obol kubectl get clusterrolebinding openclaw-monetize-read-binding \
    -o jsonpath='{.subjects}'
obol kubectl get clusterrolebinding openclaw-monetize-workload-binding \
    -o jsonpath='{.subjects}'
obol kubectl get rolebinding openclaw-x402-pricing-binding -n x402 \
    -o jsonpath='{.subjects}'
# All three should show:
#   [{"kind":"ServiceAccount","name":"openclaw","namespace":"openclaw-obol-agent"}]
```

---

## Phase 4: Configure Payment & Create Offer

```bash
# 8. Configure x402 pricing (seller wallet + chain)
obol sell pricing \
    --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --chain base-sepolia

# 9. Verify Ollama has the model available on the host
curl -s http://localhost:11434/api/tags | python3 -c \
    "import sys,json; [print(m['name']) for m in json.load(sys.stdin)['models']]"
# Should include qwen3:0.6b — if not:
#   ollama pull qwen3:0.6b

# 10. Create ServiceOffer CR
obol sell http my-qwen \
    --type inference \
    --model qwen3:0.6b \
    --runtime ollama \
    --per-request 0.001 \
    --network base-sepolia \
    --pay-to 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --namespace llm \
    --upstream ollama \
    --port 11434 \
    --path /services/my-qwen
# → serviceoffer.obol.org/my-qwen created
```

---

## Phase 5: Agent Reconciliation

```bash
# 11. Trigger reconciliation from inside the agent pod
#     (The heartbeat cron runs every 30 min by default —
#      this is the same script it would execute)
obol kubectl exec -n openclaw-obol-agent deploy/openclaw -c openclaw -- \
    python3 /data/.openclaw/skills/monetize/scripts/monetize.py process --all

# Expected output:
#   Processing 1 pending offer(s)...
#   Reconciling llm/my-qwen...
#     Checking if model qwen3:0.6b is available...
#     Model qwen3:0.6b already available
#     Health-checking http://ollama.llm.svc.cluster.local:11434/health...
#     Upstream reachable (HTTP 404 — acceptable for health check)
#     Creating Middleware x402-my-qwen...
#     Added pricing route: /services/my-qwen/* → 0.001 USDC
#     Creating HTTPRoute so-my-qwen...
#     ServiceOffer llm/my-qwen is Ready

# 12. Verify all 6 conditions are True
obol sell status my-qwen --namespace llm
# → ModelReady=True
#   UpstreamHealthy=True
#   PaymentGateReady=True
#   RoutePublished=True
#   Registered=True (Skipped)
#   Ready=True
```

---

## Phase 6: Test 402 Gate (No Payment)

```bash
# 13. Request without payment → expect HTTP 402
curl -s -w "\nHTTP %{http_code}" -X POST \
    "http://obol.stack:8080/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}],"stream":false}'

# Expected: HTTP 402 + JSON body:
# {
#   "x402Version": 1,
#   "error": "Payment required for this resource",
#   "accepts": [{
#     "scheme": "exact",
#     "network": "base-sepolia",
#     "maxAmountRequired": "1000",
#     "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
#     "payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
#     ...
#   }]
# }
```

---

## Phase 7: Start x402-rs Facilitator + Anvil

```bash
# 14. Start Anvil forking base-sepolia (background, port 8545)
anvil --fork-url https://sepolia.base.org --port 8545 --host 0.0.0.0 --silent &

# Verify Anvil is running:
curl -s -X POST http://localhost:8545 \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
# → {"jsonrpc":"2.0","id":1,"result":"0x14a34"}  (84532 = base-sepolia)

# 15. Build x402-rs facilitator (first time only, ~2 min)
cd ~/Development/R\&D/x402-rs/facilitator && cargo build --release && cd -

# 16. Start facilitator with Anvil config (background, port 4040)
#     config-anvil.json points RPC at host.docker.internal:8545
~/Development/R\&D/x402-rs/facilitator/target/release/facilitator \
    --config ~/Development/R\&D/x402-rs/config-anvil.json &

# Verify facilitator is running:
curl -s http://localhost:4040/supported
# → {"kinds":[{"x402Version":1,"scheme":"exact","network":"base-sepolia"}, ...],
#    "signers":{"eip155:84532":["0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"]}}

# 17. Verify buyer (Anvil account 0) has USDC on the fork
cast call 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
    "balanceOf(address)(uint256)" \
    0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 \
    --rpc-url http://localhost:8545
# → non-zero balance (e.g. 287787514 = ~287 USDC)
```

---

## Phase 8: Patch Verifier → Local Facilitator

```bash
# 18. Point x402-verifier at the local x402-rs facilitator
#     macOS: host.docker.internal
#     Linux: host.k3d.internal
obol kubectl patch configmap x402-pricing -n x402 --type merge -p '{
  "data": {
    "pricing.yaml": "wallet: 0x70997970C51812dc3A010C7d01b50e0d17dc79C8\nchain: base-sepolia\nfacilitatorURL: http://host.docker.internal:4040\nverifyOnly: true\nroutes:\n- pattern: \"/services/my-qwen/*\"\n  price: \"0.001\"\n  description: \"ServiceOffer my-qwen\"\n  payTo: \"0x70997970C51812dc3A010C7d01b50e0d17dc79C8\"\n  network: \"base-sepolia\"\n"
  }
}'

# 19. Restart verifier to pick up immediately
#     (otherwise the file watcher takes 60-120s)
obol kubectl rollout restart deploy/x402-verifier -n x402
obol kubectl rollout status  deploy/x402-verifier -n x402 --timeout=60s
```

---

## Phase 9: Sign Payment & Test Paid Request

```bash
# 20. Create venv and install eth-account
python3 -m venv /tmp/x402-venv
/tmp/x402-venv/bin/pip install eth-account --quiet

# 21. Write the payment signing script
cat > /tmp/x402-pay.py << 'PYEOF'
#!/usr/bin/env python3
"""Sign an x402 V1 exact payment header using Anvil account 0."""
import json, base64, os
from eth_account import Account
from eth_account.messages import encode_typed_data

PRIVATE_KEY = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
PAYER       = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
PAY_TO      = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
USDC        = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
CHAIN_ID    = 84532
AMOUNT      = "1000"          # 0.001 USDC in 6-decimal micro-units
NONCE       = "0x" + os.urandom(32).hex()

signable = encode_typed_data(full_message={
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
        "name": "USDC", "version": "2",
        "chainId": CHAIN_ID, "verifyingContract": USDC,
    },
    "message": {
        "from": PAYER, "to": PAY_TO,
        "value": int(AMOUNT),
        "validAfter": 0, "validBefore": 4294967295,
        "nonce": bytes.fromhex(NONCE[2:]),
    },
})

signed = Account.sign_message(signable, PRIVATE_KEY)

# IMPORTANT: x402-rs wire format requires validAfter/validBefore as STRINGS
payload = {
    "x402Version": 1,
    "scheme": "exact",
    "network": "base-sepolia",
    "payload": {
        "signature": "0x" + signed.signature.hex(),
        "authorization": {
            "from": PAYER, "to": PAY_TO,
            "value":       AMOUNT,           # string (decimal_u256)
            "validAfter":  "0",              # string (UnixTimestamp)
            "validBefore": "4294967295",     # string (UnixTimestamp)
            "nonce":       NONCE,            # string (B256 hex)
        },
    },
    "resource": {
        "payTo": PAY_TO, "maxAmountRequired": AMOUNT,
        "asset": USDC, "network": "base-sepolia",
    },
}
print(base64.b64encode(json.dumps(payload).encode()).decode())
PYEOF

# 22. Generate payment header and send paid request
PAYMENT=$(/tmp/x402-venv/bin/python3 /tmp/x402-pay.py)

curl -s -w "\nHTTP %{http_code}" -X POST \
    "http://obol.stack:8080/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-PAYMENT: $PAYMENT" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Say hello in exactly 3 words"}],"stream":false}'

# Expected: HTTP 200 + full Ollama inference response JSON
```

---

## Phase 10: Lifecycle Cleanup

```bash
# 23. Stop offer (removes pricing route from ConfigMap, keeps CR)
obol sell stop my-qwen --namespace llm

# 24. Restart verifier so removed route takes effect immediately
obol kubectl rollout restart deploy/x402-verifier -n x402

# 25. Verify endpoint is now free (no payment required)
curl -s -w "\nHTTP %{http_code}" -X POST \
    "http://obol.stack:8080/services/my-qwen/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"qwen3:0.6b","messages":[{"role":"user","content":"Hello"}],"stream":false}'
# → HTTP 200 (free endpoint, no 402)

# 26. Full delete — removes CR + Middleware + HTTPRoute (ownerRef cascade)
obol sell delete my-qwen --namespace llm --force

# 27. Verify everything is cleaned up
obol kubectl get serviceoffers,middleware,httproutes -n llm
# → No resources found in llm namespace.

# 28. Stop background processes and clean up temp files
pkill -f "anvil.*fork-url"
pkill -f "facilitator.*config-anvil"
rm -rf /tmp/x402-venv /tmp/x402-pay.py
```

---

## Reference: Key Addresses

| Role | Address | Note |
|------|---------|------|
| Seller (payTo) | `0x70997970C51812dc3A010C7d01b50e0d17dc79C8` | Anvil account 1 |
| Buyer (payer) | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` | Anvil account 0 |
| Buyer private key | `0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80` | Anvil default — never use in production |
| USDC (base-sepolia) | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` | Circle USDC on base-sepolia |
| Chain ID | `84532` | base-sepolia |

## Reference: Key Gotchas

| Gotcha | Detail |
|--------|--------|
| **macOS vs Linux host bridging** | macOS: `host.docker.internal`. Linux: `host.k3d.internal` (step 18) |
| **x402-rs timestamp format** | `validAfter`/`validBefore` must be **strings** (`"0"`, `"4294967295"`), not integers. x402-rs `UnixTimestamp` deserializes from stringified u64 |
| **ConfigMap propagation delay** | x402-verifier file watcher takes 60-120s. Use `kubectl rollout restart` for immediate effect |
| **Heartbeat interval** | 30 minutes by default. For interactive testing, exec into the pod and run `monetize.py process --all` manually (step 11) |
| **`/etc/hosts`** | Must have `127.0.0.1 obol.stack`. `obolup.sh` sets this during install, or add manually |
| **`OBOL_DEVELOPMENT=true`** | Required for `obol stack up` to build the x402-verifier Docker image locally instead of pulling from registry |
| **Anvil fork freshness** | Each `anvil` restart creates a fresh fork. USDC balances come from the forked base-sepolia state at the time of fork |
| **x402-rs `config-anvil.json`** | Ships with the x402-rs repo. Points `eip155:84532` RPC at `host.docker.internal:8545` (Anvil). Adjust if your Anvil is on a different port |
