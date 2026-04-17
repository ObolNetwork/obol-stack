# x402 Seller Gateway Redesign Report

## Branch

- `feature/x402-seller-gateway-redesign`

## Summary

This branch redesigns `sell http` from:

- `Traefik -> ForwardAuth verifier -> upstream`

to:

- `Traefik -> shared seller-owned x402 gateway -> upstream`

The gateway now owns:

1. `402` payment requirement generation
2. payment verification with the facilitator
3. upstream proxying
4. settlement after upstream success
5. `X-PAYMENT-RESPONSE` emission back to the buyer

This restores correct x402 seller-side semantics for cluster-routed `sell http`.

## Test Results

### Unit Tests

- `go test ./internal/x402` : passed
- `go test ./internal/serviceoffercontroller` : passed

### Integration Compile / Smoke

- `go test -tags integration ./internal/openclaw -run TestDoesNotExist` : passed compile
- `go test -tags integration -v -run TestBDDIntegration -timeout 20m ./internal/x402` : blocked by host environment

Blocked environment detail:

- the x402 BDD bootstrap path still assumes host port `8080`
- this machine already has `8080` allocated by another process
- I did not kill that unrelated listener

### Flow Tests

- `flows/flow-11-dual-stack.sh` : passed `41/41`

Run used high-port overrides for both stacks:

- Alice: `18080/18081/18443/18444`
- Bob: `19080/19081/19443/19444`

## Successful Flow-11 Artifacts

### Seller

- Seller wallet: `0xC0De030F6C37f490594F93fB99e2756703c4297E`
- Published tunnel URL:
  - `https://ten-municipal-mortgages-offerings.trycloudflare.com`
- Registered agent ID:
  - `5003`

### Buyer

- Buyer discovery wallet:
  - `0x57b0eF875DeB5A37301F1640E469a2129Da9490E`
- Bob remote-signer wallet:
  - `0x5D01290Fd77EbD7a82bD316dC2d762C30B07D107`
- Purchased alias:
  - `paid/qwen3.5:9b`

## Seller Registration Receipts

### 1. Identity Registration

- Tx hash:
  - `0x32eef92ede6779a3d05e780bf0920f4744b22ec6e85eb81d4d83371644d751b9`
- Block:
  - `40331698`
- Chain:
  - Base Sepolia (`84532`)
- Function:
  - `register(string)`
- Registry:
  - `0x8004A818BFB912233c491871b3d84c89A494BD9e`
- Sender / owner:
  - `0xC0De030F6C37f490594F93fB99e2756703c4297E`
- Agent ID:
  - `5003`
- Registered URI:
  - `https://ten-municipal-mortgages-offerings.trycloudflare.com/.well-known/agent-registration.json`

Observed receipt facts:

- status: `1`
- gas used: `183900`
- emitted `Registered(agentId=5003, agentURI=..., owner=seller)`
- emitted `MetadataSet(agentWallet=0xC0De...)`

### 2. x402 Metadata Write

- Tx hash:
  - `0xab815b78ab688697ef1e39f479bf83d57b53c7afb26c00c85775105d860b1df8`
- Block:
  - `40331700`
- Function:
  - `setMetadata(uint256,string,bytes)`
- Metadata key:
  - `x402`
- Metadata value:
  - `{"x402":true}`

Observed receipt facts:

- status: `1`
- gas used: `57552`
- sender: `0xC0De030F6C37f490594F93fB99e2756703c4297E`
- target: `0x8004A818BFB912233c491871b3d84c89A494BD9e`

## Buyer Settlement Receipt

### 3. x402 Settlement Transfer

- Tx hash:
  - `0x847de0118110b4f056c025b9804cd82f87274a2b95926419c34ccfc0eb1216a2`
- Block:
  - `40331811`
- Function:
  - `transferWithAuthorization(address,address,uint256,uint256,uint256,bytes32,bytes)`
- Chain:
  - Base Sepolia (`84532`)
- Token:
  - USDC `0x036CbD53842c5426634e7929541eC2318f3dCF7e`
- Facilitator settlement sender:
  - `0xd744494E28b01073514EBC89987B305001ed257A`
- Buyer signer:
  - `0x5D01290Fd77EbD7a82bD316dC2d762C30B07D107`
- Seller payTo:
  - `0xC0De030F6C37f490594F93fB99e2756703c4297E`
- Settled amount:
  - `1000` micro-USDC (`0.001 USDC`)

Observed receipt facts:

- status: `1`
- gas used: `86144`
- `AuthorizationUsed` emitted by USDC
- `Transfer` emitted from buyer signer to seller for `1000`

## Buyer-Side Receipt Summary

From the successful `flow-11` run:

- `PurchaseRequest` reached `Ready=True`
- buyer sidecar live status changed:
  - before: `remaining=5 spent=0`
  - after one paid request: `remaining=4 spent=1` in effect, with one settled request consumed
- paid inference returned `200`
- settlement transaction was observed on-chain

## Supported x402 Chain Recipes In This Repo

These are the chain recipes currently encoded in `internal/x402/chains.go`.

| Chain | CAIP-2 | USDC |
|---|---|---|
| `base` | `eip155:8453` | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` |
| `base-sepolia` | `eip155:84532` | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` |
| `ethereum` | `eip155:1` | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |
| `polygon` | `eip155:137` | `0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359` |
| `polygon-amoy` | `eip155:80002` | `0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582` |
| `avalanche` | `eip155:43114` | `0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E` |
| `avalanche-fuji` | `eip155:43113` | `0x5425890298aed601595a70AB815c96711a31Bc65` |
| `arbitrum-one` | `eip155:42161` | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` |
| `arbitrum-sepolia` | `eip155:421614` | `0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d` |

## Architecture Outcome

This branch makes the seller-side responsibility explicit:

- buyers still use `x402-buyer` for bounded auth pools and automatic paid retries
- sellers now settle from the shared seller-owned gateway after upstream success
- the legacy `/verify` endpoint remains available, but `verifyOnly=true` is treated as a legacy ForwardAuth-only safety setting, not the hot path for `sell http`
