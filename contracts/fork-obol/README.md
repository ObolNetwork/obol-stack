# Base Sepolia OBOL test token + faucet

This Foundry project contains the fork-local OBOL test token used by the x402
integration tests plus a bounded Base Sepolia faucet variant for public testnet
smokes.

## Contracts

- `ForkObolToken.sol` — existing unrestricted fork-local token used by Anvil integration tests.
- `BaseSepoliaObolToken.sol` — minimal ERC-20 + EIP-2612 token with the same OBOL metadata (`name: Obol Network`, `symbol: OBOL`, `decimals: 18`, `version: 1`) and owner-restricted minting.
- `BaseSepoliaObolFaucet.sol` — rate-limited faucet that transfers from its own OBOL balance via `claim()` / `claim(address to)`.

The faucet intentionally does not mint. Deployers should mint test OBOL to a funded holder, transfer a bounded allocation into the faucet, and top it up as needed.

## Example deploy flow

```bash
# Requires a funded Base Sepolia deployer key in your local shell environment.
# Do not commit or paste private keys.
export BASE_SEPOLIA_RPC_URL="https://..."
export DEPLOYER_PRIVATE_KEY="[REDACTED]"

forge create src/BaseSepoliaObolToken.sol:BaseSepoliaObolToken \
  --rpc-url "$BASE_SEPOLIA_RPC_URL" \
  --private-key "$DEPLOYER_PRIVATE_KEY" \
  --constructor-args <initial-holder> 100000000000000000000000000

forge create src/BaseSepoliaObolFaucet.sol:BaseSepoliaObolFaucet \
  --rpc-url "$BASE_SEPOLIA_RPC_URL" \
  --private-key "$DEPLOYER_PRIVATE_KEY" \
  --constructor-args <token-address> <owner> 100000000000000000000 86400
```

After deploying and seeding the faucet, configure the frontend with:

```bash
NEXT_PUBLIC_BASE_SEPOLIA_OBOL_TOKEN_ADDRESS=<token-address>
NEXT_PUBLIC_BASE_SEPOLIA_OBOL_FAUCET_ADDRESS=<faucet-address>
NEXT_PUBLIC_BASE_SEPOLIA_OBOL_FAUCET_AMOUNT="100 OBOL"
```
