# Obol CLI — Agent Context

Machine-readable context for AI agents consuming the `obol` CLI.

## Structured Output

- Use `--output json` (or `-o json`, or `OBOL_OUTPUT=json`) for machine-readable output
- JSON goes to stdout; diagnostics go to stderr
- Use `--quiet` to suppress all non-error diagnostics
- Combine: `obol sell list -o json -q` for clean JSON only

## Non-Interactive Mode

- All prompts auto-resolve to defaults when stdin is not a TTY
- Use `--force` for destructive operations without confirmation
- Provide all required flags explicitly — don't rely on interactive prompts
- In JSON mode, prompts are never shown

## Input Requirements

- Service/resource names: lowercase alphanumeric + hyphens, 1-63 chars
- Wallet addresses: 0x-prefixed, 42 hex chars (EIP-55 checksummed)
- Chain names: `base-sepolia`, `base`, `ethereum` (not CAIP-2 format)
- Prices: positive decimal strings (e.g. `"0.001"`)
- Registration chains accept comma-separated values: `--chain mainnet,base`

## Commands with JSON Support

| Command | JSON Output | Notes |
|---------|------------|-------|
| `obol sell list -o json` | ServiceOffer CRs | |
| `obol sell status -o json` | Payment config + routes + registrations | |
| `obol sell status <name> -o json` | Single ServiceOffer CR | |
| `obol sell info <name> -o json` | Inference gateway details | |
| `obol network list -o json` | RPC networks + local nodes | |
| `obol model status -o json` | LLM provider status | |
| `obol model list -o json` | Available models | |
| `obol openclaw list -o json` | OpenClaw instances | |
| `obol tunnel status -o json` | Tunnel mode, status, URL | |
| `obol version -o json` | Version, commit, build time | |
| `obol update -o json` | Available chart updates | |

## Sell Commands

- `obol sell inference <name>` — start x402 payment-gated inference gateway
- `obol sell http <name>` — publish x402 payment-gated HTTP service
- `obol sell register --chain mainnet,base` — register on ERC-8004 Agent Registry (multi-chain)
- `obol sell pricing --chain base-sepolia` — configure x402 payment wallet/chain
- `obol sell list` / `status` / `stop` / `delete` — manage service offerings

Wallet is auto-discovered from the remote-signer when available. Override with `--wallet`.

## Prerequisites

- Cluster commands require a running stack (`obol stack up`)
- Wallet auto-discovery requires `obol agent init` (creates remote-signer)
- ERC-8004 registration uses the remote-signer for signing (port-forward on demand) and pays gas from the agent's wallet
- `obol sell demo --no-register` skips the on-chain register step (offer still reaches Ready via the controller's `registration.enabled=false` short-circuit)
