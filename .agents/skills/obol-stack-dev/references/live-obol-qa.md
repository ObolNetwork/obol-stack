# Live OBOL QA

Use this reference for `flow-14-live-obol-base-sepolia.sh`, release smoke OBOL checks, and demo-gate validation.

## Flow Selection

| Flow | Use for | Network | Token | Facilitator |
|------|---------|---------|-------|-------------|
| `flows/flow-11-dual-stack.sh` | USDC baseline | Base Sepolia | USDC | configured x402 facilitator |
| `flows/flow-14-live-obol-base-sepolia.sh` | default live OBOL smoke/demo gate | Base Sepolia | deployed OBOL | `https://x402.gcp.obol.tech` |
| `flows/flow-13-dual-stack-obol.sh` | OBOL Permit2 regression without live funds | Anvil fork | fork-local OBOL | local x402-rs |

Default to live Base Sepolia for OBOL. Start Anvil only when explicitly testing the fork regression.

## Live Token

```bash
OBOL_TOKEN_BASE_SEPOLIA=0x0a09371a8b011d5110656ceBCc70603e53FD2c78
# Source of truth: ObolNetwork/obol-stack#447
```

`flow-14` defaults to this token. Override only when validating a different deployment.

## Required Funding

`REMOTE_SIGNER_PRIVATE_KEY` is Alice's seller/register key and must hold Base Sepolia ETH.

Bob is the deterministic second-derived key from `REMOTE_SIGNER_PRIVATE_KEY`; it must already hold Base Sepolia OBOL. `flow-14` pre-seeds Bob's remote-signer with this key before `stack up`. Do not fund a generated signer inside the flow.

Current canonical live QA pair:

- Use the single Alice/Bob pair derived from the `REMOTE_SIGNER_PRIVATE_KEY` selected for the run.
- Alice/register address is `cast wallet address --private-key "$REMOTE_SIGNER_PRIVATE_KEY"`.
- Bob/buyer address is the deterministic second-derived key from that same Alice key.

Keep live OBOL funding on that deterministic Bob address. Do not infer the canonical pair from balances on older duplicate/testnet token deployments, and do not maintain a second live QA key set unless a test explicitly needs an isolated wallet pair.

Derive/check Bob:

```bash
SIGNER_KEY=$(grep -E '^[[:space:]]*REMOTE_SIGNER_PRIVATE_KEY=' .env | head -1 | cut -d= -f2-)
BOB_PRIVATE_KEY=$(env -u CHAIN cast keccak "$(env -u CHAIN cast abi-encode 'f(bytes32,uint256)' "$SIGNER_KEY" 2)")
BOB_WALLET=$(env -u CHAIN cast wallet address --private-key "$BOB_PRIVATE_KEY")
cast call "$OBOL_TOKEN_BASE_SEPOLIA" "balanceOf(address)(uint256)" "$BOB_WALLET" --rpc-url "${BASE_SEPOLIA_RPC:-https://sepolia.base.org}"
```

## Success Criteria

- OBOL metadata reads as `Obol Network` / `OBOL` / 18 decimals and exposes `DOMAIN_SEPARATOR()`.
- Alice ServiceOffer reaches `Ready=True`.
- Alice is registered in ERC-8004.
- Bob remote-signer equals the deterministic `BOB_WALLET`.
- Bob in-cluster eRPC sees Bob's OBOL balance.
- Bob buys auths; `PurchaseRequest` reaches `Ready=True`.
- Alice and Bob route through `OBOL_LLM_ENDPOINT`, and Hermes default model equals `OBOL_LLM_MODEL`.
- LiteLLM exposes `paid/<OBOL_LLM_MODEL>`; default QA model is `qwen36-fast`.
- Paid inference returns HTTP 200 and final-answer content, not reasoning metadata or tool-catalogue text.
- A Base Sepolia OBOL `Transfer(Bob signer -> Alice, 1000000000000000)` receipt is archived.
- Alice balance increases and Bob signer balance decreases by exactly `1000000000000000` wei.

## Release Smoke

- `RELEASE_SMOKE_INCLUDE_OBOL=true` runs live `flow-14`.
- `RELEASE_SMOKE_INCLUDE_OBOL_FORK=true` runs fork `flow-13`.
- Keep these paths explicit. Do not hide live/fork behavior behind one selector name.

## Local Checks

```bash
bash -n flows/*.sh
git diff --check
helm lint internal/embed/infrastructure/cloudflared
helm template cloudflared internal/embed/infrastructure/cloudflared | rg 'cloudflare/cloudflared:'
go test ./cmd/obol ./internal/tunnel ./internal/stack -count=1
```
