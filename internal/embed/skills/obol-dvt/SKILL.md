---
name: obol-dvt
description: "Distributed Validator (DVT) cluster monitoring, operator management, and exit coordination via Obol Network API. Use when: querying DVT clusters, checking validator performance, investigating operator status, coordinating exits, or discussing Obol/Charon/DKG concepts. Uses mcporter MCP tools if configured, falls back to direct Obol API calls via curl. NOT for: creating clusters, running DKG, or submitting exits (write operations)."
metadata: { "openclaw": { "emoji": "🔱", "requires": { "bins": ["curl"] } } }
---

# Obol Distributed Validator (DV) Skill

Query and monitor Distributed Validators on the Obol Network. Covers cluster health, operator management, exit coordination, and DVT concepts.

---

## What is a Distributed Validator?

A **Distributed Validator (DV)** is an Ethereum validator whose private key is never held
by a single party. Instead, the signing key is split across a group of **operators** using
threshold BLS cryptography. Any `threshold-of-N` operators must cooperate to produce a valid
signature — so the validator keeps attesting even if some operators go offline, and no single
operator can act maliciously on their own.

Obol's open-source middleware is called **Charon**. Each operator runs a Charon client
alongside their validator client (e.g., Lighthouse, Teku). Charon handles the consensus
protocol between operators so the validator appears as a single validator to the beacon chain.

| Term | Meaning |
|------|---------|
| **Cluster** | A group of N operators running DVs together |
| **Threshold** | Minimum operators needed to sign (e.g., 3-of-4) |
| **DKG** | Distributed Key Generation — operators collaboratively create the shared key without anyone seeing the full private key |
| **Cluster Definition** | Pre-DKG proposal: who the operators are, how many validators, which network |
| **Cluster Lock** | Post-DKG artifact: locked configuration + generated validator public keys; identified by `lock_hash` |
| **config_hash** | Hash of the cluster definition (pre-DKG); also embedded in the lock |
| **lock_hash** | Hash of the cluster lock (post-DKG); the primary identifier for a running cluster |
| **Operator** | An Ethereum address that participates in one or more DV clusters |
| **Techne** | Obol's operator reputation system: base > bronze > silver |
| **OWR** | Optimistic Withdrawal Recipient — a smart contract that splits validator rewards |

---

## Cluster Lifecycle

```
[Cluster Definition]  -> operators agree on config, sign T&Cs
        |
    [DKG Ceremony]    -> Charon nodes exchange key shares; no full key ever assembled
        |
  [Cluster Lock]      -> validator pubkeys generated; cluster is live on beacon chain
        |
  [Active Validators] -> attesting, proposing blocks, earning rewards
        |
  [Exit Coordination] -> operators sign exit messages; broadcast when threshold reached
```

When a user provides a `config_hash`, they are referring to something at or before DKG.
When they provide a `lock_hash`, the cluster has completed DKG and may have active validators.

---

## API Access

**Base URL:** `https://api.obol.tech` (public, no authentication needed)

### Preferred: mcporter MCP tools

If mcporter is configured with the obol-mcp server:

```bash
mcporter call obol.obol_cluster_lock_by_hash lock_hash=0x4d6e7f8a...
mcporter call obol.obol_cluster_effectiveness lock_hash=0x4d6e7f8a...
```

Check availability: `mcporter list obol 2>/dev/null`

### Fallback: Direct curl

```bash
# Helper function for Obol API calls
obol_api() {
  curl -s "https://api.obol.tech$1" | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin),indent=2))"
}

# Example
obol_api "/v1/lock/0x4d6e7f8a..."
```

---

## Tool Selection Guide

### "I have a lock_hash"

| Goal | API Endpoint | curl |
|------|-------------|------|
| Cluster config | `GET /v1/lock/{lockHash}` | `curl -s "https://api.obol.tech/v1/lock/0x..."` |
| Validator performance | `GET /v1/effectiveness/{lockHash}` | `curl -s "https://api.obol.tech/v1/effectiveness/0x..."` |
| Validator beacon states | `GET /v1/state/{lockHash}` | `curl -s "https://api.obol.tech/v1/state/0x..."` |
| Exit status summary | `GET /v1/exp/exit/status/summary/{lockHash}` | `curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x..."` |
| Detailed exit status | `GET /v1/exp/exit/status/{lockHash}` | `curl -s "https://api.obol.tech/v1/exp/exit/status/0x..."` |

### "I have a config_hash"

| Goal | API Endpoint | curl |
|------|-------------|------|
| Pre-DKG definition | `GET /v1/definition/{configHash}` | `curl -s "https://api.obol.tech/v1/definition/0x..."` |
| Cluster lock (if DKG done) | `GET /v1/lock/configHash/{configHash}` | `curl -s "https://api.obol.tech/v1/lock/configHash/0x..."` |

### "I have an operator address (0x...)"

| Goal | API Endpoint |
|------|-------------|
| All clusters | `GET /v1/lock/operator/{address}` |
| Cluster definitions | `GET /v1/definition/operator/{address}` |
| Badges (Lido, EtherFi) | `GET /v1/address/badges/{address}` |
| Techne credential level | `GET /v1/address/techne/{address}` |
| Token incentives | `GET /v1/address/incentives/{network}/{address}` |
| T&Cs signed? | `GET /v1/termsAndConditions/{address}` |

### "I want to explore a network (mainnet / holesky / sepolia)"

| Goal | API Endpoint |
|------|-------------|
| All clusters | `GET /v1/lock/network/{network}` |
| Network statistics | `GET /v1/lock/network/summary/{network}` |
| Search clusters | `GET /v1/lock/search/{network}?q=...` |
| All operators | `GET /v1/address/network/{network}` |
| Search operators | `GET /v1/address/search/{network}?q=...` |

### Other

| Goal | API Endpoint |
|------|-------------|
| Migrateable validators | `GET /v1/address/migrateable-validators/{network}/{withdrawalAddress}` |
| OWR tranches | `GET /v1/owr/{network}/{address}` |
| API health | `GET /v1/_health` |

---

## Common Workflows

### Investigate a cluster's health

```bash
# 1. Get cluster config
curl -s "https://api.obol.tech/v1/lock/0x4d6e7f8a..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
print(f'Cluster: {d.get(\"name\",\"?\")} on {d.get(\"network\",\"?\")}')
print(f'Threshold: {d.get(\"threshold\",\"?\")}-of-{len(d.get(\"operators\",[]))}')
print(f'Validators: {d.get(\"num_validators\",len(d.get(\"validators\",[])))}')
"

# 2. Check effectiveness
curl -s "https://api.obol.tech/v1/effectiveness/0x4d6e7f8a..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
for v in d.get('effectiveness',[]):
    eff = v.get('effectiveness',0)
    pk = v.get('public_key','?')[:16]
    status = 'healthy' if eff > 0.95 else 'degraded' if eff > 0.8 else 'CRITICAL'
    print(f'{pk}...  {eff:.3f}  [{status}]')
"

# 3. Check validator states
curl -s "https://api.obol.tech/v1/state/0x4d6e7f8a..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
for v in d.get('validators',[]):
    bal = int(v.get('balance','0')) / 1e9
    print(f'{v[\"public_key\"][:16]}...  {v.get(\"status\",\"?\")}  {bal:.4f} ETH')
"
```

If effectiveness is low: one or more operators offline, misconfigured Charon, network latency, or a validator stuck in `exiting` state.

### Coordinate a voluntary exit

```bash
# 1. Exit status summary
curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
print(f'Ready to exit: {d.get(\"validators_ready_to_exit\",0)}/{d.get(\"total_validators\",0)}')
for op in d.get('operators',[]):
    print(f'  {op[\"address\"][:12]}...  signed: {op.get(\"signed_exits\",0)}')
"
```

Exit is broadcast automatically once `threshold` operators have submitted their exit signature shares.

### Audit an operator

```bash
# Techne level
curl -s "https://api.obol.tech/v1/address/techne/0xAbCd..." | python3 -c "
import sys,json; d=json.load(sys.stdin); print(f'Level: {d.get(\"credential_level\",\"?\")}')"

# Badges
curl -s "https://api.obol.tech/v1/address/badges/0xAbCd..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
badges = [b['type'] for b in d.get('badges',[])]
print(f'Badges: {badges if badges else \"none\"}')"

# Cluster count
curl -s "https://api.obol.tech/v1/lock/operator/0xAbCd..." | python3 -c "
import sys,json; d=json.load(sys.stdin)
clusters = d if isinstance(d,list) else d.get('items',[])
print(f'Active in {len(clusters)} cluster(s)')"

# T&Cs signed?
curl -s "https://api.obol.tech/v1/termsAndConditions/0xAbCd..." | python3 -c "
import sys,json; d=json.load(sys.stdin); print(f'T&Cs signed: {d}')"
```

---

## How to Talk About DVs

**Do say:**
- "Your cluster has 4 operators with a 3-of-4 threshold, so it tolerates one operator going offline."
- "The cluster lock (identified by its `lock_hash`) is the source of truth after DKG."
- "Effectiveness of 0.95 means the validator is attesting in ~95% of expected slots."
- "Exit coordination requires threshold operators to submit their key shares of the exit message."

**Avoid:**
- Saying the private key is "split into pieces" — it's threshold cryptography; no full key is ever assembled.
- Saying a cluster "fails" if one operator goes offline — it degrades gracefully until the threshold is not met.
- Confusing `config_hash` (pre-DKG) with `lock_hash` (post-DKG).

## Identifier Formats

- `lock_hash` and `config_hash`: hex strings starting with `0x`, typically 66 characters
- Operator `address`: standard Ethereum address, `0x` + 40 hex chars
- Validator `pubkey`: BLS public key, `0x` + 96 hex chars

All identifiers are **case-sensitive** in Obol API calls. If a user provides an address without `0x`, remind them to include it.

**Networks:** `mainnet` (real ETH), `hoodi` (staking/infra testnet, successor to holesky), `holesky` (legacy testnet), `sepolia` (secondary testnet)

**Validator status values:** `active_ongoing`, `active_exiting`, `active_slashed`, `exited_unslashed`, `exited_slashed`, `withdrawal_possible`, `withdrawal_done`, `pending_*`

## Examples

For parameter shapes, response field reference, and example conversation patterns, see:
`references/api-examples.md`

## Limitations

- All API calls are **read-only** — creating clusters, running DKG, and submitting exits require authenticated POST endpoints
- Exit status endpoints are under `/v1/exp/` (experimental) — pagination is 1-indexed
- API rate limits apply; if timeouts occur, check `GET /v1/_health` first
- mcporter MCP integration requires the obol-mcp server to be installed (pip not available in pod currently)
