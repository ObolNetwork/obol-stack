# Ethereum Wallet: Web3Signer Integration Plan

> **Status**: Draft v2 — awaiting review before implementation
> **Date**: 2026-02-20
> **Scope**: Deploy Web3Signer per OpenClaw instance, generate signing key at init time, flesh out the `ethereum-wallet` skill, expose public keys to frontend read-only

---

## 1. Overview

Add transaction signing capabilities to OpenClaw agents by deploying a [Web3Signer](https://docs.web3signer.consensys.io/) instance alongside each OpenClaw deployment. A SECP256K1 signing key is generated at `obol agent init` time by the `obol` CLI (not by OpenClaw). The `ethereum-wallet` skill gives the agent HTTP-only access to sign and submit transactions — it never touches private key material.

### Design Principles

1. **Key generation is infrastructure, not agent behavior** — the `obol` CLI generates keys at init time and provisions them into web3signer's volume. OpenClaw never creates, reads, or manages private keys.
2. **Separate volumes** — web3signer owns its key PVC exclusively. OpenClaw has no mount to the keys directory.
3. **ClusterIP isolation** — web3signer has no HTTPRoute, no external exposure. It's a ClusterIP service reachable only within the namespace.
4. **Sign via web3signer, submit via eRPC** — `eth_signTransaction` returns RLP-encoded signed tx data. The skill submits it via `eth_sendRawTransaction` on eRPC with the appropriate `--network` path.
5. **Public key metadata via ConfigMap** — the frontend reads a ConfigMap for display purposes without any access to signing operations.

### What Changes

| Area | Change |
|------|--------|
| `internal/openclaw/openclaw.go` | `generateHelmfile()` adds `web3signer` release in the same namespace |
| `internal/openclaw/web3signer.go` (new) | Key generation, values generation, ConfigMap creation |
| `internal/openclaw/openclaw.go` | `generateOverlayValues()` adds `WEB3SIGNER_URL` env var |
| `internal/embed/skills/ethereum-wallet/` | Full skill: `SKILL.md`, `scripts/signer.py`, `references/web3signer-api.md` |

### What Doesn't Change

- `obol agent init` still calls `openclaw.Onboard()` — no new top-level CLI commands
- Stack infrastructure (`obol stack init/up`) — web3signer is per-instance, not cluster-wide
- Existing skills — `ethereum-networks` remains read-only, `obol-stack` and `distributed-validators` unchanged
- OpenClaw chart — no new volume mounts needed (skill only uses HTTP)

---

## 2. Architecture

### Deployment Topology

```
Namespace: openclaw-<id>
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ┌──────────────────┐              ┌──────────────────┐          │
│  │   OpenClaw Pod    │    HTTP      │  Web3Signer Pod   │          │
│  │                  │────────────▶│                  │          │
│  │  skills/         │  :9000      │  /data/keys/     │          │
│  │    ethereum-     │  (JSON-RPC) │    <keyid>.hex   │          │
│  │    wallet/       │             │    <keyid>.toml  │          │
│  │    scripts/      │             │                  │          │
│  │    signer.py     │             │  No external     │          │
│  │                  │             │  route exposed   │          │
│  └──────────────────┘              └────────┬─────────┘          │
│                                             │                    │
│                                    ┌────────┴─────────┐          │
│                                    │  web3signer PVC   │          │
│                                    │  (keys only —     │          │
│                                    │   NOT shared)     │          │
│                                    └──────────────────┘          │
│                                                                  │
│  ┌──────────────────────────────────────────────────────┐        │
│  │  ConfigMap: web3signer-metadata                      │        │
│  │  ┌─────────────────────────────────────────────────┐ │        │
│  │  │ addresses.json:                                 │ │        │
│  │  │   { "addresses": [{"address":"0x...",           │ │        │
│  │  │     "publicKey":"0x...", "createdAt":"..."}],   │ │        │
│  │  │     "count": 1 }                                │ │        │
│  │  └─────────────────────────────────────────────────┘ │        │
│  └──────────────────────────────────────────────────────┘        │
│     ▲ readable by frontend (existing ClusterRole)                │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
         │
         │ eth_sendRawTransaction
         ▼
  ┌──────────────┐
  │  eRPC         │  (namespace: erpc)
  │  :4000/rpc    │
  └──────────────┘
```

### Data Flow: Sign & Send Transaction

```
1. Agent decides to send ETH
2. Skill script: GET /api/v1/eth1/publicKeys → web3signer → signer public key
3. Skill script: eth_accounts (JSON-RPC) → web3signer → signer address
4. Skill script: eth_getTransactionCount → eRPC → nonce
5. Skill script: eth_gasPrice + eth_estimateGas → eRPC → gas params
6. Skill script: eth_signTransaction (JSON-RPC) → web3signer → RLP-encoded signed tx
7. Skill script: eth_sendRawTransaction → eRPC → tx hash
8. Skill script: eth_getTransactionReceipt → eRPC → confirmation
```

Key point: `eth_signTransaction` on web3signer returns the RLP-encoded signed transaction directly. No RLP encoding needed in Python. The skill submits it to eRPC as-is via `eth_sendRawTransaction`.

### Data Flow: Key Generation (at init time)

```
1. obol agent init
2. Go code: crypto/ecdsa.GenerateKey(secp256k1) → private key
3. Go code: derive public key → derive Ethereum address (keccak256)
4. Go code: write private key hex → $DATA_DIR/openclaw-<id>/web3signer-keys/<keyid>.hex
5. Go code: write TOML config → $DATA_DIR/openclaw-<id>/web3signer-keys/<keyid>.toml
6. Go code: create ConfigMap web3signer-metadata with address + public key
7. helmfile sync → deploys web3signer with PVC mounted at key directory
8. web3signer starts → reads key files → ready to sign
```

---

## 3. Web3Signer Deployment

### 3.1 Helmfile Integration

Modify `generateHelmfile()` in `internal/openclaw/openclaw.go` to produce a multi-release helmfile:

```yaml
# OpenClaw instance: <id>
# Managed by obol openclaw

repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/
  - name: ethereum
    url: https://ethpandaops.github.io/ethereum-helm-charts

releases:
  - name: openclaw
    namespace: openclaw-<id>
    createNamespace: true
    chart: obol/openclaw
    version: 0.1.4
    values:
      - values-obol.yaml

  - name: web3signer
    namespace: openclaw-<id>
    chart: ethereum/web3signer
    version: 1.0.6
    values:
      - values-web3signer.yaml
```

### 3.2 Web3Signer Values (`values-web3signer.yaml`)

Generated by `generateWeb3SignerValues()` in a new `internal/openclaw/web3signer.go`:

```yaml
# Web3Signer configuration for OpenClaw instance <id>
replicas: 1

image:
  repository: consensys/web3signer
  tag: "24.12.1"    # Pin a specific stable version

# ETH1 signing mode — keys loaded from /data/keys/
extraArgs:
  - "--eth1-enabled"
  - "--key-store-path=/data/keys"
  - "--http-host-allowlist=*"

# Use the chart's built-in persistence for key storage
# Keys are pre-provisioned by `obol agent init` via host-path PVC
persistence:
  enabled: true
  size: 100Mi
  accessModes:
    - ReadWriteOnce

# Disable PostgreSQL — not needed for ETH1 file-based keys
postgresql:
  enabled: false

# Service — ClusterIP only, no external exposure
service:
  type: ClusterIP

# No ingress — web3signer is namespace-internal only
ingress:
  enabled: false

resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### 3.3 Key Pre-Provisioning via Host-Path

Keys are written to the host filesystem before web3signer starts, using the same host-path PVC pattern as skills injection:

```
Host path: $DATA_DIR/openclaw-<id>/web3signer-data/keys/
         ↓ (k3d volume mount + local-path-provisioner)
Pod path:  /data/keys/
```

The Go code in `web3signer.go` writes key files to the host-side path at init time. When the web3signer pod starts, the PVC is already populated.

**File layout on host**:
```
$DATA_DIR/openclaw-<id>/web3signer-data/keys/
├── <keyid>.hex          # Raw private key (64 hex chars)
└── <keyid>.toml         # Web3Signer key config
```

**TOML key config format**:
```toml
[metadata]
description = "obol-agent-<id>"

[signing]
type = "file-raw"
filename = "/data/keys/<keyid>.hex"
```

### 3.4 Key Generation in Go

New function in `internal/openclaw/web3signer.go`:

```go
import (
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "encoding/hex"

    "golang.org/x/crypto/sha3"
)

func generateSigningKey() (privateKeyHex, publicKeyHex, address string, err error) {
    // Generate SECP256K1 key
    key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    // NOTE: Go stdlib doesn't have secp256k1. Options:
    //   a) Use github.com/ethereum/go-ethereum/crypto (already used?)
    //   b) Use github.com/decred/dcrd/dcrec/secp256k1
    //   c) Use P256 for dev, swap to secp256k1 before production

    // Derive Ethereum address: keccak256(uncompressed_pubkey[1:])[12:]
    pubBytes := elliptic.Marshal(key.Curve, key.PublicKey.X, key.PublicKey.Y)
    hash := sha3.NewLegacyKeccak256()
    hash.Write(pubBytes[1:])  // skip 0x04 prefix
    addr := hash.Sum(nil)[12:]

    return hex.EncodeToString(key.D.Bytes()),
           hex.EncodeToString(pubBytes),
           "0x" + hex.EncodeToString(addr),
           nil
}
```

**Note on secp256k1**: Go's `crypto/elliptic` has P256 but NOT secp256k1. We need one of:
- `github.com/ethereum/go-ethereum/crypto` — the standard Go-Ethereum library (may already be a transitive dep)
- `github.com/decred/dcrd/dcrec/secp256k1/v4` — lightweight standalone
- `github.com/btcsuite/btcd/btcec/v2` — Bitcoin library with secp256k1

Recommendation: use `go-ethereum/crypto` if it's already in the dep tree, otherwise `decred/secp256k1` is the lightest option. Check `go.mod` during implementation.

### 3.5 ConfigMap for Frontend (Public Key Metadata)

Created by Go code at init time, in the instance namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: web3signer-metadata
  namespace: openclaw-<id>
  labels:
    app.kubernetes.io/component: web3signer
    app.kubernetes.io/managed-by: obol
data:
  addresses.json: |
    {
      "instanceId": "<id>",
      "addresses": [
        {
          "address": "0xAbCd...1234",
          "publicKey": "0x04...",
          "createdAt": "2026-02-20T14:30:00Z",
          "label": "obol-agent-<id>"
        }
      ],
      "count": 1
    }
```

**Frontend reads this via**: the existing `obol-frontend-rbac` ClusterRole which grants read access to ConfigMaps across namespaces. The frontend can list ConfigMaps with label `app.kubernetes.io/component: web3signer` across all `openclaw-*` namespaces to discover all agents' addresses.

**Multiple OpenClaw instances**: Each instance gets its own `web3signer-metadata` ConfigMap in its own namespace. The frontend aggregates them:

```
openclaw-alpha/  → web3signer-metadata (address: 0xABC...)
openclaw-beta/   → web3signer-metadata (address: 0xDEF...)
```

**Update mechanism**: If keys are ever added later (e.g., via a future `obol web3signer add-key` CLI command), the ConfigMap is patched via `kubectl apply`. The skill script never updates this ConfigMap — that's the CLI's responsibility.

---

## 4. Security Model

### 4.1 ClusterIP Isolation (Primary)

Web3Signer is deployed as a ClusterIP service with **no HTTPRoute**. It's unreachable from outside the cluster and from external Traefik routing.

```
Accessible:    http://web3signer.openclaw-<id>.svc.cluster.local:9000
Not exposed:   No HTTPRoute, no Ingress, no NodePort, no LoadBalancer
```

### 4.2 No Bearer Token (Simplified)

Web3Signer ETH1 mode does not have built-in bearer token auth (that's a keymanager/ETH2 feature). Rather than adding an auth proxy sidecar, we accept ClusterIP isolation as sufficient for a local development stack.

**Rationale**:
- The k3d cluster runs on localhost — there's no network path from the internet to web3signer
- No HTTPRoute means Traefik doesn't route any external traffic to it
- Flannel's default behavior still requires being in-cluster to reach ClusterIP services
- The keys are for development/testing, not production custody

### 4.3 Separation of Concerns

| Actor | Can do | Cannot do |
|-------|--------|-----------|
| `obol` CLI | Generate keys, write to PVC, create ConfigMap, deploy web3signer | N/A (full control) |
| OpenClaw skill | Call JSON-RPC signing endpoints via HTTP | Read key files, create keys, access PVC |
| Frontend | Read `web3signer-metadata` ConfigMap (public keys, counts) | Call web3signer API, read secrets, modify ConfigMap |
| Other namespaces | Nothing | Reach web3signer service (ClusterIP is namespace-scoped for practical purposes) |

### 4.4 Future Hardening (Out of Scope)

If stronger isolation is needed later:
- Switch k3s CNI to Calico for NetworkPolicy enforcement
- Add an nginx auth proxy sidecar with shared-secret bearer token
- Move to encrypted keystores (V3) with password-protected keys
- Add RBAC restricting which ServiceAccounts can exec into the web3signer pod

---

## 5. Ethereum Wallet Skill

### 5.1 Skill Structure

```
internal/embed/skills/ethereum-wallet/
├── SKILL.md                          # Full skill documentation
├── scripts/
│   └── signer.py                     # Python 3 stdlib-only HTTP client
└── references/
    └── web3signer-api.md             # ETH1 API quick-reference
```

### 5.2 SKILL.md Content

```markdown
---
name: ethereum-wallet
description: "Sign and send Ethereum transactions via the local Web3Signer.
  Use when asked to send ETH, sign messages, or interact with contracts
  that modify state."
metadata:
  openclaw:
    emoji: "🔐"
    requires:
      bins: ["python3"]
---

# Ethereum Wallet

Sign and send Ethereum transactions through the local Web3Signer instance.
Keys are pre-generated during setup — this skill signs and submits only.

## When to Use

- Listing available signing addresses (wallets)
- Sending ETH to an address
- Signing messages or typed data (EIP-712)
- Signing transactions for later broadcast
- Calling contract functions that modify state (write operations)

## When NOT to Use

- Reading blockchain data (balances, blocks, transactions) — use `ethereum-networks`
- Creating new keys — keys are managed by the `obol` CLI, not this skill
- Monitoring validators — use `distributed-validators`
- Kubernetes diagnostics — use `obol-stack`

## Quick Start

# List signing addresses
python3 scripts/signer.py accounts

# Check web3signer health
python3 scripts/signer.py health

# Sign a message
python3 scripts/signer.py sign <address> <hex-data>

# Sign a transaction (returns signed raw tx hex)
python3 scripts/signer.py sign-tx \
  --from <address> --to <address> --value 1000000000000000000

# Sign AND submit a transaction
python3 scripts/signer.py send-tx \
  --from <address> --to <address> --value 1000000000000000000

# Sign EIP-712 typed data
python3 scripts/signer.py sign-typed <address> '{"types":...}'

## Available Commands

| Command | Params | Description |
|---------|--------|-------------|
| `accounts` | none | List signing addresses from web3signer |
| `health` | none | Check web3signer /upcheck |
| `sign` | `address data` | Sign arbitrary hex data (eth_sign) |
| `sign-tx` | `--from --to [--value] [--data] [--gas] [--nonce] [--network]` | Sign a tx, return raw signed hex |
| `sign-typed` | `address typed-data-json` | Sign EIP-712 typed data |
| `send-tx` | `--from --to [--value] [--data] [--network]` | Sign AND broadcast via eRPC |

## Transaction Submission Flow

`send-tx` does the following:
1. Fetches nonce, gas price, chain ID from eRPC (unless provided)
2. Calls `eth_signTransaction` on web3signer → gets RLP-encoded signed tx
3. Calls `eth_sendRawTransaction` on eRPC → gets tx hash
4. Reports the tx hash (use `ethereum-networks` to check receipt)

## Multi-Network Support

By default, transactions target `mainnet`. Use `--network` to change:

python3 scripts/signer.py send-tx --network hoodi \
  --from 0x... --to 0x... --value 1000000000000000000

The signing key is chain-agnostic — the same address works on any EVM network.
Network routing goes through eRPC at /rpc/{network}.

## Constraints

- **Shell is `sh`, not `bash`** — POSIX-compatible syntax only
- **Python stdlib only** — no web3, eth_abi, or third-party packages
- **No key creation** — keys are managed by `obol` CLI. If no keys exist, tell the user to run `obol agent init`
- **Local only** — always use the in-cluster web3signer at $WEB3SIGNER_URL
- **Values in wei** — `--value` is in wei (1 ETH = 1000000000000000000). The script does NOT auto-convert from ETH
- **Always check for null** — RPC responses may be null; always validate before accessing fields
- **Confirm before sending** — always show the user what will be signed before executing `send-tx`
```

### 5.3 `scripts/signer.py` Design

Python 3 stdlib-only script. HTTP client for Web3Signer JSON-RPC + eRPC.

```python
"""
Ethereum wallet operations via local Web3Signer.

Environment variables:
  WEB3SIGNER_URL  — default: http://web3signer:9000
  ERPC_URL        — default: http://erpc.erpc.svc.cluster.local:4000/rpc
  ERPC_NETWORK    — default: mainnet
"""

import json, os, sys
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

WEB3SIGNER_URL = os.environ.get("WEB3SIGNER_URL", "http://web3signer:9000")
ERPC_BASE = os.environ.get("ERPC_URL", "http://erpc.erpc.svc.cluster.local:4000/rpc")
ERPC_NETWORK = os.environ.get("ERPC_NETWORK", "mainnet")
```

**Commands — no filesystem access, HTTP only**:

| Command | Web3Signer Call | eRPC Call | Notes |
|---------|----------------|-----------|-------|
| `accounts` | `GET /api/v1/eth1/publicKeys` | — | Derives addresses from public keys. Or uses `eth_accounts` JSON-RPC |
| `health` | `GET /upcheck` | — | Returns "OK" or error |
| `sign` | `eth_sign` JSON-RPC | — | `[address, data]` → signature |
| `sign-tx` | `eth_signTransaction` JSON-RPC | `eth_getTransactionCount`, `eth_gasPrice`, `eth_estimateGas`, `eth_chainId` | Auto-fills missing nonce/gas/chainId from eRPC. Returns RLP-encoded signed tx |
| `sign-typed` | `eth_signTypedData` JSON-RPC | — | `[address, typedData]` → signature |
| `send-tx` | `eth_signTransaction` JSON-RPC | Same as sign-tx + `eth_sendRawTransaction` | Signs then submits. Reports tx hash |

**Helper functions**:

```python
def web3signer_rpc(method, params):
    """JSON-RPC call to Web3Signer."""
    payload = {"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
    req = Request(WEB3SIGNER_URL, json.dumps(payload).encode(),
                  {"Content-Type": "application/json"})
    resp = json.load(urlopen(req))
    if "error" in resp:
        print(f"Error: {resp['error'].get('message', resp['error'])}", file=sys.stderr)
        sys.exit(1)
    return resp.get("result")

def erpc_rpc(method, params, network=None):
    """JSON-RPC call to eRPC."""
    url = f"{ERPC_BASE}/{network or ERPC_NETWORK}"
    payload = {"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
    req = Request(url, json.dumps(payload).encode(),
                  {"Content-Type": "application/json"})
    resp = json.load(urlopen(req))
    if "error" in resp:
        print(f"Error: {resp['error'].get('message', resp['error'])}", file=sys.stderr)
        sys.exit(1)
    return resp.get("result")

def web3signer_rest(method, path):
    """REST API call to Web3Signer."""
    req = Request(f"{WEB3SIGNER_URL}{path}")
    req.method = method
    return json.load(urlopen(req))
```

**`send-tx` implementation sketch**:

```python
def send_tx(from_addr, to_addr, value="0x0", data="0x", gas=None, nonce=None, network=None):
    net = network or ERPC_NETWORK

    # Auto-fill from eRPC
    if nonce is None:
        nonce = erpc_rpc("eth_getTransactionCount", [from_addr, "pending"], net)
    if gas is None:
        gas = erpc_rpc("eth_estimateGas", [{"from": from_addr, "to": to_addr,
                                             "value": value, "data": data}], net)
    gas_price = erpc_rpc("eth_gasPrice", [], net)
    chain_id = erpc_rpc("eth_chainId", [], net)

    tx = {
        "from": from_addr, "to": to_addr, "value": value,
        "data": data, "gas": gas, "gasPrice": gas_price,
        "nonce": nonce, "chainId": chain_id
    }

    # Sign via web3signer
    signed = web3signer_rpc("eth_signTransaction", [tx])

    # Submit via eRPC
    tx_hash = erpc_rpc("eth_sendRawTransaction", [signed], net)
    print(f"Transaction submitted: {tx_hash}")
    return tx_hash
```

### 5.4 `references/web3signer-api.md`

Quick-reference for the ETH1 API surface:

```markdown
# Web3Signer ETH1 API Reference

Base URL: $WEB3SIGNER_URL (default: http://web3signer:9000)

## JSON-RPC Methods

All methods use POST to the base URL with Content-Type: application/json.

| Method | Params | Returns | Description |
|--------|--------|---------|-------------|
| `eth_accounts` | `[]` | `["0x..."]` | List signer addresses |
| `eth_sign` | `[address, data]` | `"0x..."` (signature) | Sign with Ethereum prefix |
| `eth_signTransaction` | `[{from,to,gas,gasPrice,value,data,nonce}]` | `"0x..."` (signed RLP) | Sign tx for later broadcast |
| `eth_signTypedData` | `[address, typedData]` | `"0x..."` (signature) | EIP-712 typed data signing |

## REST API Endpoints

| Method | Path | Description | Response |
|--------|------|-------------|----------|
| GET | `/upcheck` | Health check | `"OK"` (200) |
| GET | `/api/v1/eth1/publicKeys` | List public keys | `["0x..."]` |
| POST | `/api/v1/eth1/sign/{pubkey}` | Sign raw data | signature hex |
| POST | `/reload` | Reload key configs | 202 Accepted |

## Transaction Object Fields

| Field | Required | Description |
|-------|----------|-------------|
| `from` | yes | Signer address |
| `to` | yes* | Recipient (* omit for contract deploy) |
| `value` | no | Wei to send (hex) |
| `data` | no | Calldata (hex) |
| `gas` | no | Gas limit (hex) |
| `gasPrice` | no | Gas price (hex) |
| `maxFeePerGas` | no | EIP-1559 max fee (hex) |
| `maxPriorityFeePerGas` | no | EIP-1559 priority fee (hex) |
| `nonce` | no | Sender nonce (hex) |

## Error Responses

| Code | Meaning |
|------|---------|
| 400 | Bad request (malformed params) |
| 404 | Public key not found in keystore |
| 500 | Internal server error |
```

---

## 6. Implementation Phases

### Phase 1: Key Generation & Web3Signer Deployment

**New file**: `internal/openclaw/web3signer.go`

**Functions**:
- `generateSigningKey() (privateKeyHex, publicKeyHex, address string, err error)` — uses `crypto/ecdsa` with secp256k1 curve
- `provisionKeyFiles(dataDir, id, privateKeyHex string) error` — writes `.hex` + `.toml` to host-path PVC location
- `generateWeb3SignerValues(id string) string` — produces `values-web3signer.yaml`
- `createMetadataConfigMap(cfg, id, address, publicKey string) error` — applies `web3signer-metadata` ConfigMap via kubectl
- `web3signerKeysPath(cfg, id string) string` — returns `$DATA_DIR/openclaw-<id>/web3signer-data/keys/`

**Modify**: `internal/openclaw/openclaw.go`
- `generateHelmfile()` — add `ethereum` repo and `web3signer` release
- `Onboard()` — call key generation + provisioning before `doSync()`
- `generateOverlayValues()` — add `WEB3SIGNER_URL` env var to OpenClaw pod config

**Acceptance criteria**:
- `obol agent init` generates a key and deploys web3signer in the same namespace
- `GET /upcheck` returns `OK` from within the openclaw pod
- `eth_accounts` JSON-RPC returns the generated address
- `web3signer-metadata` ConfigMap exists with correct address data
- Web3signer is NOT accessible via any HTTPRoute

### Phase 2: Skill Script — Accounts & Signing

**New files**:
- `internal/embed/skills/ethereum-wallet/scripts/signer.py`
- `internal/embed/skills/ethereum-wallet/references/web3signer-api.md`

**Commands to implement**:
1. `accounts` — `eth_accounts` JSON-RPC → list addresses
2. `health` — `GET /upcheck`
3. `sign` — `eth_sign` JSON-RPC
4. `sign-typed` — `eth_signTypedData` JSON-RPC

**Acceptance criteria**:
- `python3 scripts/signer.py accounts` lists the pre-generated address
- `python3 scripts/signer.py sign <addr> 0xdeadbeef` returns a valid signature
- `python3 scripts/signer.py health` returns OK

### Phase 3: Transaction Signing & Submission

**Modify**: `scripts/signer.py`

**Commands to implement**:
1. `sign-tx` — build tx, auto-fill from eRPC, `eth_signTransaction` → return signed hex
2. `send-tx` — `sign-tx` + `eth_sendRawTransaction` on eRPC
3. `--network` flag support for multi-chain

**Acceptance criteria**:
- `sign-tx` returns RLP-encoded signed transaction
- `send-tx` submits to eRPC and returns tx hash
- Missing nonce/gas/chainId are auto-fetched from eRPC
- `--network hoodi` routes through eRPC at `/rpc/hoodi`
- Errors (insufficient funds, bad nonce) produce clear messages

### Phase 4: Skill Documentation & Polish

**Modify**:
- `internal/embed/skills/ethereum-wallet/SKILL.md` — full rewrite (replace placeholder)
- `internal/embed/skills/ethereum-networks/SKILL.md` — add cross-reference to wallet skill for write operations

**New tests**:
- `internal/openclaw/web3signer_test.go` — unit tests for key generation, values generation, TOML format
- Integration test in `internal/openclaw/integration_test.go` — full deploy + sign flow

**Acceptance criteria**:
- All unit tests pass
- Integration test deploys web3signer, signs a message, verifies signature
- SKILL.md is complete and follows existing skill patterns
- `ethereum-networks` SKILL.md cross-references wallet skill

---

## 7. Open Questions (Resolved)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Transaction submission | `eth_signTransaction` → `eth_sendRawTransaction` via eRPC | Web3Signer returns RLP-encoded signed tx. No downstream config needed. eRPC handles network routing. |
| Q2 | Auth mechanism | ClusterIP isolation only, no bearer token | Web3Signer ETH1 doesn't support bearer auth natively. ClusterIP + no HTTPRoute is sufficient for local dev. |
| Q3 | Shared PVC | Not needed — volumes are separate | Key generation happens at init time via Go code. Skill uses HTTP only. |
| Q4 | K8s security without NetworkPolicy | ClusterIP + no HTTPRoute + no Ingress | Flannel doesn't enforce NetworkPolicy, but ClusterIP services are only reachable in-cluster. No external route = no external access. |
| Q5 | Frontend access to public keys | ConfigMap `web3signer-metadata` per instance | Created by Go code at init time. Frontend reads via existing ClusterRole. One ConfigMap per web3signer, aggregated across namespaces. |
| Q6 | Multi-network signing | One web3signer per instance, chain-agnostic keys | SECP256K1 keys work on any EVM chain. Network routing is the skill script's concern via eRPC path. |
| Q7 | secp256k1 in Go | Check for go-ethereum/crypto or add decred/secp256k1 | Go stdlib only has P256. Need a secp256k1 implementation. |

---

## 8. Testing Strategy

### Unit Tests (`internal/openclaw/web3signer_test.go`)

| Test | Validates |
|------|-----------|
| `TestGenerateSigningKey` | Valid secp256k1 key, correct address derivation, deterministic length |
| `TestProvisionKeyFiles` | `.hex` file has 64 chars, `.toml` has correct structure, file permissions |
| `TestGenerateWeb3SignerValues` | Valid YAML, ETH1 enabled, PostgreSQL disabled, correct persistence config |
| `TestCreateMetadataConfigMap` | Correct JSON structure, address format, label selectors |
| `TestGenerateHelmfileWithWeb3Signer` | Two releases (openclaw + web3signer), same namespace, ethereum repo present |

### Integration Tests (`internal/openclaw/integration_test.go`)

| Test | Tag | Validates |
|------|-----|-----------|
| `TestIntegration_Web3SignerDeploy` | `integration` | Pod starts, /upcheck returns OK, eth_accounts returns address |
| `TestIntegration_SignMessage` | `integration` | eth_sign returns valid signature format |
| `TestIntegration_SignTransaction` | `integration` | eth_signTransaction returns RLP hex |
| `TestIntegration_MetadataConfigMap` | `integration` | ConfigMap exists, contains correct address |

### Skill Smoke Tests (in-pod Python)

| Test | Validates |
|------|-----------|
| `test_health` | Web3Signer reachable at $WEB3SIGNER_URL |
| `test_accounts` | At least one address returned |
| `test_sign` | Signature for known data matches expected format |
| `test_erpc_reachable` | eRPC responds to eth_blockNumber |

---

## 9. Dependencies

| Dependency | Version | Source | Notes |
|------------|---------|--------|-------|
| `ethereum/web3signer` Helm chart | 1.0.6 | ethpandaops helm-charts | Already have `ethereum` repo in infra helmfile |
| `consensys/web3signer` Docker image | 24.12.1 | Docker Hub | Pin specific version |
| secp256k1 Go library | TBD | go-ethereum or decred | For key generation in Go |
| PostgreSQL (chart sub-dep) | — | Disabled | Not needed for ETH1 file-based keys |

---

## 10. Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| No NetworkPolicy enforcement (Flannel) | Low — local dev only | ClusterIP + no HTTPRoute. No internet-facing path to web3signer. |
| Key material stored as raw hex on PVC | Medium — unencrypted at rest | Acceptable for dev. Future: V3 encrypted keystores. |
| secp256k1 not in Go stdlib | Low — implementation detail | Use go-ethereum/crypto or decred/secp256k1. Check go.mod first. |
| `eth_signTransaction` return format varies | Medium — may not return raw RLP | Test with actual Web3Signer. Fallback: minimal RLP encoder in Python (~50 lines). |
| PVC not available before pod start | Low — race condition | Key files written at init time, before `helmfile sync`. PVC is populated before web3signer starts. |

---

## 11. Future Enhancements (Out of Scope)

- `obol web3signer add-key` — CLI command to add more keys post-init
- ETH2/BLS signing — validator attestations, blocks, voluntary exits
- Encrypted keystores (V3) — password-protected keys
- Hardware signer backends — HashiCorp Vault, AWS KMS
- Transaction builder in frontend — UI for composing transactions
- Gas estimation intelligence — smart gas pricing
- Key export/backup — encrypted key export for disaster recovery
- Multi-instance key isolation — per-agent key namespaces
