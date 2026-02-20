---
name: ethereum-wallet
description: "Sign and send Ethereum transactions via a remote Web3Signer. This skill is not yet implemented — it will connect to a Web3Signer instance using a URI and auth token to sign transactions, deploy contracts, and manage validator operations."
metadata: { "openclaw": { "emoji": "🔐", "requires": { "bins": ["curl"] } } }
---

# Ethereum Wallet

> **This skill is coming soon.** It is not yet functional — the instructions below describe what it will do when complete.

## What This Will Do

The ethereum-wallet skill will let you sign and send Ethereum transactions through a remote [Web3Signer](https://docs.web3signer.consensys.io/) instance. Web3Signer is a remote signing service that keeps private keys secure and separate from the application.

### Planned capabilities

- **Send ETH** to any address
- **Call contract functions** that modify state (not just read — that's what `ethereum-networks` does)
- **Deploy contracts** from bytecode
- **Sign messages** for off-chain verification
- **Manage validator operations** like voluntary exits

### Configuration

When ready, this skill will need two things:

1. **Web3Signer URI** — the URL of your Web3Signer instance (e.g. `http://web3signer.svc.cluster.local:9000`)
2. **Auth token** — a bearer token for authenticating with the signer

These will be provided during setup via `obol openclaw setup` or environment variables.

## Current Status

This skill is a placeholder. If you need to:

- **Read** blockchain data (balances, blocks, transactions) — use the `ethereum-networks` skill
- **Monitor** distributed validators — use the `distributed-validators` skill
- **Sign transactions** — this will need to wait until the ethereum-wallet skill is implemented
