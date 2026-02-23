---
name: distributed-validators
description: "Monitor distributed validator clusters via the Obol API. Use when asked about DVT cluster health, validator performance, operator status, exit coordination, or anything related to Obol, Charon, or distributed validators. Read-only — cannot create clusters or submit exits."
metadata: { "openclaw": { "emoji": "🔱", "requires": { "bins": ["curl"] } } }
---

# Distributed Validators

Monitor distributed validator (DV) clusters using the Obol API. Check cluster health, validator performance, operator status, and exit progress.

A distributed validator splits signing across multiple operators so no single party holds the full key. Obol's middleware (Charon) coordinates this. A cluster has N operators with a threshold (e.g. 3-of-4) needed to sign.

## When to Use

- Checking cluster health or validator performance
- Looking up operator reputation or badges
- Checking exit coordination progress
- Exploring network-wide DV statistics

## When NOT to Use

- Creating clusters or running DKG (write operations)
- Ethereum RPC queries — use `ethereum-networks`
- Kubernetes diagnostics — use `obol-stack`

## API Base

All endpoints are public, no authentication needed:

```
https://api.obol.tech
```

## Key Identifiers

- **lock_hash**: Identifies a cluster after key generation (post-DKG). `0x` + 64 hex chars.
- **config_hash**: Identifies a cluster definition before key generation (pre-DKG).
- **operator address**: Standard Ethereum address, `0x` + 40 hex chars.

## Lookup by lock_hash

```bash
# Cluster config
curl -s "https://api.obol.tech/v1/lock/0x..."

# Validator performance (0-1 scale, >0.95 is healthy)
curl -s "https://api.obol.tech/v1/effectiveness/0x..."

# Validator beacon chain states
curl -s "https://api.obol.tech/v1/state/0x..."

# Exit status summary
curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x..."

# Detailed exit status
curl -s "https://api.obol.tech/v1/exp/exit/status/0x..."
```

## Lookup by config_hash

```bash
# Pre-DKG cluster definition
curl -s "https://api.obol.tech/v1/definition/0x..."

# Get lock (if DKG completed)
curl -s "https://api.obol.tech/v1/lock/configHash/0x..."
```

## Lookup by operator address

```bash
# All clusters for this operator
curl -s "https://api.obol.tech/v1/lock/operator/0x..."

# Reputation level (base < bronze < silver)
curl -s "https://api.obol.tech/v1/address/techne/0x..."

# Protocol badges (Lido, EtherFi, etc.)
curl -s "https://api.obol.tech/v1/address/badges/0x..."

# Terms & conditions signed?
curl -s "https://api.obol.tech/v1/termsAndConditions/0x..."
```

## Network-wide queries

```bash
# Network summary stats
curl -s "https://api.obol.tech/v1/lock/network/summary/mainnet"

# Search clusters
curl -s "https://api.obol.tech/v1/lock/search/mainnet?q=..."

# All operators on a network
curl -s "https://api.obol.tech/v1/address/network/mainnet"
```

Networks: `mainnet`, `hoodi`, `holesky`, `sepolia`

## Common Workflows

### Check cluster health

```bash
# 1. Get cluster info
curl -s "https://api.obol.tech/v1/lock/0x..." | python3 -c "
import sys, json
d = json.load(sys.stdin)
if 'error' in d or 'name' not in d:
    print('Error or cluster not found:', json.dumps(d, indent=2))
else:
    ops = d.get('operators', [])
    print(f'Cluster: {d.get(\"name\",\"?\")} | {d.get(\"threshold\",\"?\")}-of-{len(ops)} | {d.get(\"network\",\"?\")}')
"

# 2. Check validator effectiveness
curl -s "https://api.obol.tech/v1/effectiveness/0x..." | python3 -c "
import sys, json
d = json.load(sys.stdin)
for v in d.get('effectiveness', []):
    eff = v.get('effectiveness', 0)
    status = 'healthy' if eff > 0.95 else 'degraded' if eff > 0.8 else 'CRITICAL'
    print(f'{v.get(\"public_key\", \"?\")[:16]}...  {eff:.3f}  [{status}]')
if not d.get('effectiveness'):
    print('No effectiveness data found')
"
```

### Check exit progress

```bash
curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x..." | python3 -c "
import sys, json
d = json.load(sys.stdin)
if not isinstance(d, dict) or 'total_validators' not in d:
    print('No exit data or cluster not found:', json.dumps(d, indent=2))
else:
    print(f'Ready to exit: {d.get(\"validators_ready_to_exit\", 0)}/{d.get(\"total_validators\", 0)}')
    for op in d.get('operators', []):
        print(f'  {op.get(\"address\", \"?\")[:12]}...  signed: {op.get(\"signed_exits\", 0)}')
"
```

Exit broadcasts automatically once enough operators have signed (threshold reached).

## Constraints

- **Read-only** — creating clusters, running DKG, and submitting exits require authenticated endpoints
- Exit status endpoints (`/v1/exp/`) are experimental — pagination is 1-indexed
- If timeouts occur, check `GET /v1/_health` first
- **Shell is `sh`, not `bash`** — do not use bashisms
- **Always check for null/missing data** — API responses may return errors or empty results. Always check before accessing nested fields

See `references/api-examples.md` for response shapes and field reference.

## See Also

- `ethereum-networks` — blockchain RPC queries (validator balances, beacon chain data)
- `obol-stack` — Kubernetes cluster monitoring (pod status, logs)
- `local-ethereum-wallet` — transaction signing for exit operations
