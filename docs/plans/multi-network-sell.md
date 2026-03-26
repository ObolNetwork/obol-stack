# Multi-Network Sell Command + UX Improvements

## Context

The `obol sell` command currently only supports ERC-8004 registration on Base Sepolia, requires manual private key management via `--private-key-file`, and forces users to specify all flags explicitly. We want to:

1. Support 3 registration networks: **base-sepolia**, **base**, **ethereum mainnet**
2. Support **multi-chain** registration: `--chain mainnet,base` registers on both, best-effort
3. Use the **remote-signer** for all signing (not private key extraction) — EIP-712 typed data + transaction signing via its REST API
4. Use **sponsored registration** (zero gas) on ethereum mainnet via howto8004.com
5. Use the **local eRPC** (`localhost/rpc`) for chain access instead of public RPCs
6. Add **interactive prompts** using `charmbracelet/huh` with good defaults
7. **Auto-discover** the remote-signer wallet address
8. Add **ethereum mainnet** as a valid x402 payment chain

Frontend deferred to follow-up PR. EIP-7702 handled server-side by sponsor — no CLI implementation needed.

### Network Matrix

| Network | x402 Payment | x402 Facilitator | ERC-8004 Registration | Sponsored Reg |
|---------|-------------|-------------------|----------------------|---------------|
| base-sepolia | Yes | `facilitator.x402.rs` | Yes (direct tx via remote-signer) | No |
| base | Yes | `x402.gcp.obol.tech` | Yes (direct tx via remote-signer) | No |
| ethereum | Yes (no facilitator yet) | TBD | Yes | Yes (`sponsored.howto8004.com/api/register`) |

---

## Phase 1: Multi-Network ERC-8004 Registry Config

### `internal/erc8004/networks.go` (new)

```go
type NetworkConfig struct {
    Name            string // "base-sepolia", "base", "ethereum"
    ChainID         int64
    RegistryAddress string // per-chain registry address
    SponsorURL      string // empty if no sponsor
    DelegateAddress string // EIP-7702 delegate (for sponsored flow)
    ERPCNetwork     string // eRPC path segment: "base-sepolia", "base", "mainnet"
}

func ResolveNetwork(name string) (NetworkConfig, error)
func ResolveNetworks(csv string) ([]NetworkConfig, error) // "mainnet,base" → []NetworkConfig
func SupportedNetworks() []NetworkConfig
```

Three entries:
- `base-sepolia`: chainID 84532, registry `0x8004A818BFB912233c491871b3d84c89A494BD9e`, eRPC `base-sepolia`
- `base`: chainID 8453, registry TBD (confirm CREATE2 address), eRPC `base`
- `ethereum` / `mainnet`: chainID 1, registry `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432`, sponsor `https://sponsored.howto8004.com/api/register`, delegate `0x77fb3D2ff6dB9dcbF1b7E0693b3c746B30499eE8`, eRPC `mainnet`

RPC URL is **not** in NetworkConfig — always use local eRPC at `http://localhost/rpc/{ERPCNetwork}` (from host via k3d port mapping).

### `internal/erc8004/client.go`

- Add `NewClientForNetwork(ctx, rpcBaseURL string, net NetworkConfig) (*Client, error)` — constructs RPC URL as `rpcBaseURL + "/" + net.ERPCNetwork`, uses `net.RegistryAddress`
- Keep `NewClient(ctx, rpcURL)` as backward-compat wrapper

### Files
- `internal/erc8004/networks.go` (new)
- `internal/erc8004/networks_test.go` (new)
- `internal/erc8004/client.go` (add `NewClientForNetwork`)

---

## Phase 2: Remote-Signer Integration for Registration

### Architecture

The remote-signer REST API at port 9000 already supports:
- `POST /api/v1/sign/{address}/transaction` — sign raw transactions
- `POST /api/v1/sign/{address}/typed-data` — sign EIP-712 typed data
- `GET /api/v1/keys` — list loaded wallet addresses

From the host CLI, access via **temporary port-forward** to `remote-signer:9000` (same pattern as `openclaw cli`).

### `internal/erc8004/signer.go` (new)

```go
// RemoteSigner wraps the remote-signer REST API for ERC-8004 operations.
type RemoteSigner struct {
    baseURL string // e.g. "http://localhost:19000" (port-forwarded)
}

func NewRemoteSigner(baseURL string) *RemoteSigner

// GetAddress returns the first loaded signing address.
func (s *RemoteSigner) GetAddress(ctx context.Context) (common.Address, error)

// SignTransaction signs an EIP-1559 transaction for direct on-chain registration.
func (s *RemoteSigner) SignTransaction(ctx context.Context, addr common.Address, tx SignTxRequest) ([]byte, error)

// SignTypedData signs EIP-712 typed data (for sponsored registration).
func (s *RemoteSigner) SignTypedData(ctx context.Context, addr common.Address, data EIP712TypedData) ([]byte, error)
```

### `internal/erc8004/register.go` (new)

Two registration paths:

**Direct on-chain** (base-sepolia, base):
1. Port-forward to remote-signer
2. `signer.GetAddress()` → wallet address
3. Build `register(agentURI)` calldata
4. Get nonce + gas estimates from eRPC
5. `signer.SignTransaction()` → signed tx
6. `eth_sendRawTransaction` via eRPC
7. Wait for receipt, parse `Registered` event

**Sponsored** (ethereum mainnet):
1. Port-forward to remote-signer
2. `signer.GetAddress()` → wallet address
3. `signer.SignTypedData()` → EIP-712 authorization + registration intent signatures
4. POST to `net.SponsorURL` with signatures
5. Parse response `{success, agentId, txHash}`

### Port-Forward Helper

Reuse or adapt the pattern from `openclaw cli` (`cmd/obol/openclaw.go`). New helper:

```go
// portForwardRemoteSigner starts a port-forward to the remote-signer in the
// given namespace and returns the local URL + cleanup function.
func portForwardRemoteSigner(cfg *config.Config, namespace string) (baseURL string, cleanup func(), err error)
```

### Files
- `internal/erc8004/signer.go` (new — remote-signer REST client)
- `internal/erc8004/signer_test.go` (new — HTTP mock tests)
- `internal/erc8004/register.go` (new — direct + sponsored registration flows)
- `internal/erc8004/sponsor.go` (new — sponsored API client, EIP-712 types)
- `internal/erc8004/sponsor_test.go` (new)

---

## Phase 3: Wallet Auto-Discovery

### `internal/openclaw/wallet_resolve.go` (new)

```go
// ResolveWalletAddress returns the wallet address from the single OpenClaw instance.
// 0 instances → error, 1 → auto-select, 2+ → error suggesting --wallet.
func ResolveWalletAddress(cfg *config.Config) (string, error)

// ResolveInstanceNamespace returns the namespace of the single OpenClaw instance
// (needed for port-forwarding to the remote-signer in that namespace).
func ResolveInstanceNamespace(cfg *config.Config) (string, error)
```

Flow:
1. `ListInstanceIDs(cfg)` → instance IDs
2. 0 → error, 1 → read wallet.json, 2+ → error with list of addresses
3. `ReadWalletMetadata(DeploymentPath(cfg, id))` → `WalletInfo.Address`

**No private key extraction.** The address is all we need for auto-discovery. Signing goes through the remote-signer API.

### Files
- `internal/openclaw/wallet_resolve.go` (new)
- `internal/openclaw/wallet_resolve_test.go` (new)

---

## Phase 4: Rewrite `sell register`

### `cmd/obol/sell.go` — `sellRegisterCommand`

**New flags:**

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--chain` | string | `base-sepolia` | Comma-separated: `base-sepolia,base,mainnet`. Register on each, best-effort |
| `--sponsored` | bool | auto | `true` when network has sponsor URL |
| `--endpoint` | string | auto | Auto-detected from tunnel |
| `--name` | string | `Obol Agent` | Agent name for registration |
| `--description` | string | smart default | Auto-generated from stack info |
| `--image` | string | smart default | Default Obol logo URL |
| `--private-key-file` | string | | Fallback — used only if no remote-signer detected |

**Removed:** `--private-key` (deprecated), `--rpc-url` (use local eRPC)

**Action logic:**
1. Parse `--chain` → `erc8004.ResolveNetworks(chainCSV)` → `[]NetworkConfig`
2. Resolve wallet: try `openclaw.ResolveWalletAddress(cfg)`. If found, use remote-signer path. If not, require `--private-key-file`.
3. Resolve endpoint: `--endpoint` if set, else tunnel auto-detect
4. For each network (best-effort):
   a. If sponsored + network has sponsor → sponsored path (sign EIP-712 via remote-signer, POST to sponsor)
   b. Else → direct path (sign tx via remote-signer, broadcast via eRPC)
   c. On success: print CAIP-10 registry line
   d. On failure: print warning, continue to next chain
5. Update `agent-registration.json` with all successful registrations in the `registrations[]` array

### Files
- `cmd/obol/sell.go` (rewrite `sellRegisterCommand`)
- `cmd/obol/sell_test.go` (update `TestSellRegister_Flags`)

---

## Phase 5: Interactive Prompts with `charmbracelet/huh`

### New dependency

`go get github.com/charmbracelet/huh`

### Signature change

`sellCommand(cfg *config.Config)` → `sellCommand(cfg *config.Config, u *ui.UI)` (match `openclawCommand` pattern). Wire from `main.go`.

### TTY guard

```go
import "golang.org/x/term"
isInteractive := term.IsTerminal(int(os.Stdin.Fd()))
```

### `sell inference` interactive flow:

| Field | Default | Prompt type | When prompted |
|-------|---------|-------------|---------------|
| Name | (required) | Text input | No positional arg |
| Model | (required) | Select from Ollama models | `--model` not set |
| Wallet | auto-discovered | Text (pre-filled) | Auto-discover fails |
| Chain | `base-sepolia` | Select | Using default |
| Price | `0.001` | Text (pre-filled) | Confirm or override |

### `sell http` interactive flow:

| Field | Default | Prompt type | When prompted |
|-------|---------|-------------|---------------|
| Name | (required) | Text input | No positional arg |
| Upstream | (required) | Text input | `--upstream` not set |
| Port | `8080` | Text (pre-filled) | Confirm |
| Wallet | auto-discovered | Text (pre-filled) | Auto-discover fails |
| Chain | `base-sepolia` | Select | `--chain` not set (remove `Required: true`) |
| Price model | `perRequest` | Select | No price flag set |
| Price value | `0.001` | Text | After model selected |
| Register? | `false` | Confirm | Not explicitly set |

### `sell register` interactive flow:

| Field | Default | Prompt type | When prompted |
|-------|---------|-------------|---------------|
| Chain(s) | `base-sepolia` | Multi-select | Using default |
| Name | `Obol Agent` | Text (pre-filled) | Confirm or override |
| Description | auto-generated | Text (pre-filled) | Confirm or override |
| Image | default logo URL | Text (pre-filled) | Confirm or override |
| Sponsored? | yes (when available) | Confirm | Network supports it |
| Endpoint | auto-detected | Text (pre-filled) | Tunnel fails |

### Non-interactive path

All prompts gated on `isInteractive`. When not TTY: flag validation applies, defaults used, no prompts.

### Files
- `go.mod` / `go.sum` (add `charmbracelet/huh`)
- `cmd/obol/sell.go` (add prompts to inference, http, register)
- `cmd/obol/main.go` (wire `*ui.UI` to `sellCommand`)

---

## Phase 6: x402 Payment Chain Updates

### `cmd/obol/sell.go` — `resolveX402Chain`

Add:
```go
case "ethereum", "ethereum-mainnet", "mainnet":
    return x402.EthereumMainnet, nil
```

If `x402.EthereumMainnet` doesn't exist in the upstream `mark3labs/x402-go` library, define a local constant.

### `cmd/obol/sell.go` — `sellPricingCommand`

- Auto-discover wallet via `openclaw.ResolveWalletAddress(cfg)` when `--wallet` not set
- Remove `Required: true` from `--wallet`
- Update chain usage help: `"Payment chain (base-sepolia, base, ethereum)"`

### Files
- `cmd/obol/sell.go` (`resolveX402Chain`, `sellPricingCommand`)
- `internal/x402/config.go` (`ResolveChain` — add ethereum)
- `internal/x402/config_test.go` (add ethereum test cases)
- `cmd/obol/sell_test.go` (update `TestResolveX402Chain`)

---

## Phase 7: Tests & Docs

### Tests
- `internal/erc8004/networks_test.go`: `ResolveNetwork` all chains, `ResolveNetworks` CSV parsing
- `internal/erc8004/signer_test.go`: HTTP mock for remote-signer API
- `internal/erc8004/sponsor_test.go`: EIP-712 construction, HTTP mock
- `internal/openclaw/wallet_resolve_test.go`: 0/1/multi instance
- `cmd/obol/sell_test.go`: Updated register flags, multi-chain parsing, new x402 chains

### Docs
- `CLAUDE.md`: Update CLI command table, add `--chain` multi-value, remove `--rpc-url`
- `internal/embed/skills/sell/SKILL.md`: New registration flow, multi-network, remote-signer
- `internal/embed/skills/discovery/SKILL.md`: Multi-network registry info
- `cmd/obol/main.go`: Update root help text for sell register

---

## Dependency Graph

```
Phase 1 (multi-network config)
  ├──→ Phase 2 (remote-signer integration + registration flows)
  └──→ Phase 3 (wallet auto-discovery)
            │
            v
       Phase 4 (rewrite sell register) ← depends on 1+2+3
            │
            v
       Phase 5 (interactive prompts) ← depends on 3 (wallet discovery)
            │
            v
       Phase 6 (x402 payment chains + sell pricing)
            │
            v
       Phase 7 (tests & docs — throughout)
```

---

## Key Design Decisions

1. **Remote-signer for all signing** — Never extract private keys. Use `POST /api/v1/sign/{address}/transaction` for direct registration, `POST /api/v1/sign/{address}/typed-data` for sponsored EIP-712. Access via temporary port-forward.

2. **Local eRPC for all chain access** — `http://localhost/rpc/{network}` via k3d port mapping. No public RPCs. eRPC already has upstreams for mainnet, base, base-sepolia.

3. **Multi-chain `--chain mainnet,base`** — Same agentURI and wallet registered on each chain. Best-effort: if one fails, continue to next. Update `registrations[]` array in `agent-registration.json` with all successes.

4. **Prefer remote-signer, fallback to `--private-key-file`** — Auto-discover wallet → use remote-signer. If no instance found, accept `--private-key-file` for standalone usage.

5. **Good defaults for registration metadata** — Pre-fill name (`Obol Agent`), description, image URL. Interactive mode lets users confirm or override each.

6. **`charmbracelet/huh` for prompts** — Modern TUI with select, input, confirm. TTY-gated.

---

## Key Files Summary

| File | Change |
|------|--------|
| `internal/erc8004/networks.go` | New — multi-network config registry |
| `internal/erc8004/signer.go` | New — remote-signer REST API client |
| `internal/erc8004/register.go` | New — direct + sponsored registration flows |
| `internal/erc8004/sponsor.go` | New — sponsored API client |
| `internal/erc8004/client.go` | Add `NewClientForNetwork` |
| `internal/openclaw/wallet_resolve.go` | New — wallet address + namespace discovery |
| `cmd/obol/sell.go` | Rewrite register, add prompts to inference/http/register/pricing |
| `cmd/obol/main.go` | Wire `*ui.UI`, update help text |
| `cmd/obol/sell_test.go` | Update all affected tests |
| `internal/x402/config.go` | Add ethereum mainnet chain |

---

## Verification

```bash
# Phase 1
go test ./internal/erc8004/ -run TestResolveNetwork

# Phase 2 (unit — mock remote-signer)
go test ./internal/erc8004/ -run TestRemoteSigner
go test ./internal/erc8004/ -run TestSponsored

# Phase 3
go test ./internal/openclaw/ -run TestResolveWallet

# Phase 4+5 (manual — needs running cluster + tunnel)
obol sell register --chain base-sepolia              # direct tx via remote-signer
obol sell register --chain mainnet --sponsored       # zero-gas via howto8004
obol sell register --chain mainnet,base              # multi-chain best-effort
obol sell inference                                   # interactive prompts
obol sell http                                        # interactive prompts
obol sell register                                    # interactive with defaults to confirm

# Phase 6
obol sell pricing --chain base                        # auto-discovers wallet

# All unit tests
go test ./cmd/obol/ -run TestSell
go test ./internal/erc8004/
go test ./internal/openclaw/ -run TestResolve
go test ./internal/x402/ -run TestResolveChain
```
