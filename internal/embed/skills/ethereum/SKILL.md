---
name: ethereum
description: Ethereum JSON-RPC access via the Obol Stack eRPC gateway
---

# Ethereum RPC via eRPC

Query Ethereum networks through the Obol Stack's eRPC gateway.

## eRPC Gateway

The eRPC service provides a unified JSON-RPC proxy for all connected Ethereum networks. The base URL is always `http://erpc.erpc.svc.cluster.local:4000` inside the cluster.

- **Config/discovery**: `GET http://erpc.erpc.svc.cluster.local:4000/` — returns the eRPC configuration schema including all connected networks and their endpoints
- **RPC endpoint pattern**: `http://erpc.erpc.svc.cluster.local:4000/rpc/<network>`

## Discovering Connected Networks

Fetch the eRPC root config to discover which networks are available:

```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/
```

Parse the response to find project IDs — each project ID is a `<network>` you can query.

## JSON-RPC Queries

All queries use standard Ethereum JSON-RPC. Send POST requests to `http://erpc.erpc.svc.cluster.local:4000/rpc/<network>`.

### Common read methods
- `eth_blockNumber` — latest block number
- `eth_syncing` — sync status (false if synced)
- `eth_getBalance` — account balance (params: address, block)
- `eth_getBlockByNumber` — block details (params: block number, full txs bool)
- `eth_getTransactionReceipt` — transaction receipt (params: tx hash)
- `eth_call` — read-only contract call (params: call object, block)
- `net_peerCount` — connected peer count
- `eth_gasPrice` — current gas price
- `eth_chainId` — chain identifier

### Example: get latest block number on mainnet
```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/rpc/mainnet \
  -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Limitations
- Read-only queries only — no write transactions (eth_sendTransaction, eth_sendRawTransaction)
- Network availability depends on what the user has installed via `obol network install`
