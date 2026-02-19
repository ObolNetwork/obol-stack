# Obol API — Example Calls & Responses

Illustrative examples of API parameters and the key fields returned.
Use these to interpret real API responses and explain them to users.

---

## Cluster Lock — `GET /v1/lock/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/lock/0x4d6e7f8a9b..."
```

**Response shape (key fields):**
```json
{
  "cluster_definition": {
    "name": "my-dv-cluster",
    "uuid": "abc123...",
    "version": "v1.8.0",
    "num_validators": 4,
    "threshold": 3,
    "config_hash": "0x1a2b3c4d5e...",
    "fork_version": "0x00000000",
    "timestamp": "2024-03-15T10:22:00Z",
    "operators": [
      { "address": "0xAbCd...1234", "enr": "enr:-...", "enr_signature": "0x...", "config_signature": "0x..." }
    ],
    "validators": [
      { "fee_recipient_address": "0xDead...Beef", "withdrawal_address": "0x..." }
    ]
  },
  "distributed_validators": [
    { "distributed_public_key": "0xb3a2c1...", "public_shares": ["0x...", "0x..."] }
  ],
  "lock_hash": "0x4d6e7f8a9b..."
}
```

**What to tell the user:** "Your cluster has 4 operators with a 3-of-4 threshold. It was created
on 2024-03-15. Validator public keys are in `distributed_validators[].distributed_public_key`."

**Key access patterns:**
- Cluster name: `d["cluster_definition"]["name"]`
- Threshold: `d["cluster_definition"]["threshold"]`
- Operators: `d["cluster_definition"]["operators"]`
- Validator pubkeys: `[v["distributed_public_key"] for v in d["distributed_validators"]]`
- Lock hash: `d["lock_hash"]`

---

## Effectiveness — `GET /v1/effectiveness/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/effectiveness/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "0xb3a2c1...": {
    "oneDay": 0.998,
    "sevenDay": 0.995,
    "thirtyDay": 0.991,
    "all": 0.987
  },
  "0xc4d3e2...": {
    "oneDay": 0.0,
    "sevenDay": 0.612,
    "thirtyDay": 0.608,
    "all": 0.550
  }
}
```

**Notes:**
- Response is a dict keyed by validator public key (not an array).
- Each entry has time-period scores: `oneDay`, `sevenDay`, `thirtyDay`, `all`.
- Scores are 0–1; anything above ~0.95 is healthy.
- A `oneDay` of 0.0 with non-zero `sevenDay` usually means a recent outage.
- Low scores usually mean one or more operators are offline or have connectivity issues.

**Parsing:**
```python
for pubkey, scores in d.items():
    eff = scores.get('sevenDay', 0)
    status = 'healthy' if eff > 0.95 else 'degraded' if eff > 0.8 else 'CRITICAL'
    print(f'{pubkey[:16]}...  7d={eff:.3f}  [{status}]')
```

---

## Validator States — `GET /v1/state/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/state/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "0xb3a2c1...": {
    "index": "412503",
    "status": "active_ongoing",
    "balance": "32.045231042",
    "effective_balance": "32.0",
    "withdrawal_credentials": "0x01..."
  },
  "0xc4d3e2...": {
    "index": "412504",
    "status": "active_exiting",
    "balance": "32.001000000",
    "effective_balance": "32.0",
    "withdrawal_credentials": "0x01..."
  }
}
```

**Notes:**
- Response is a dict keyed by validator public key.
- `balance` is in ETH (decimal string), not Gwei.
- `active_exiting` means an exit has been initiated; pair with exit status summary to see signing progress.
- May return `{}` if the cluster has no active validators on the beacon chain.

---

## Exit Status Summary — `GET /v1/exp/exit/status/summary/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "operator_exits": {
    "0xAbCd...1234": 3,
    "0xEfGh...5678": 3,
    "0xIjKl...9012": 2,
    "0xMnOp...3456": 1
  },
  "ready_exits": 1
}
```

**What to tell the user:** "1 validator has reached the signing threshold and is ready to exit.
Operators `0xIjKl...` and `0xMnOp...` have signed fewer exits than the others — they need to
sign more to unlock the remaining validators."

**Key access patterns:**
- Ready count: `d["ready_exits"]`
- Per-operator signed count: `d["operator_exits"]` (dict of address → count)

---

## Detailed Exit Status — `GET /v1/exp/exit/status/{lockHash}`

**curl (filtered by validator):**
```bash
curl -s "https://api.obol.tech/v1/exp/exit/status/0x4d6e7f8a9b...?validatorPubkey=0xc4d3e2...&page=1&limit=10"
```

**Note:** This endpoint uses 1-indexed pagination (start at `page=1`, not `page=0`).

---

## Cluster Definition (pre-DKG) — `GET /v1/definition/{configHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/definition/0x1a2b3c4d5e..."
```

**Response shape:**
```json
{
  "config_hash": "0x1a2b3c4d5e...",
  "name": "my-dv-cluster",
  "version": "v1.8.0",
  "num_validators": 4,
  "threshold": 3,
  "fork_version": "0x00000000",
  "timestamp": "2024-03-14T08:00:00Z",
  "operators": [
    { "address": "0xAbCd...1234", "config_signature": "", "enr_signature": "" },
    { "address": "0xEfGh...5678", "config_signature": "0x...", "enr_signature": "0x..." }
  ]
}
```

**What to tell the user:** "DKG hasn't happened yet. An operator has signed when their
`config_signature` is non-empty. Operators with empty signatures still need to approve."

---

## Operator Techne — `GET /v1/address/techne/{address}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/address/techne/0xAbCd...1234"
```

**Response shape:**
```json
{
  "base": [],
  "bronze": [],
  "silver": [
    { "image_url": "https://nft-cdn.alchemy.com/...", "earned_at": "2025-04-21T19:28:21.949Z" }
  ],
  "gold": []
}
```

**Notes:**
- Response is an object with arrays per tier: `base`, `bronze`, `silver`, `gold`.
- A non-empty array means the operator has earned that credential level.
- Tiers: `base` < `bronze` < `silver` < `gold`. Higher = sustained high-quality operation.
- To determine the highest earned tier, check arrays from gold down to base.

**Parsing:**
```python
for tier in ['gold', 'silver', 'bronze', 'base']:
    if d.get(tier):
        print(f'Level: {tier} (earned {d[tier][0].get("earned_at", "?")})')
        break
else:
    print('Level: none')
```

---

## Operator Badges — `GET /v1/address/badges/{address}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/address/badges/0xAbCd...1234"
```

**Response shape:**
```json
{
  "badges": [
    {
      "name": "Lido Mainnet",
      "description": "Participation in the Lido x Obol Simple DVT module on mainnet.",
      "image_url": "https://api.obol.tech/public/lido_mainnet.png",
      "qualified": true,
      "earned_at": "2024-06-15T12:00:00Z"
    },
    {
      "name": "Genesis Dappnode",
      "description": "Holder of one of the original 60 Obol Genesis Dappnodes.",
      "image_url": "https://api.obol.tech/public/genesis_dappnode.png",
      "qualified": false,
      "earned_at": null
    }
  ]
}
```

**Notes:**
- `qualified: true` with a non-null `earned_at` = badge earned.
- `qualified: false` = not earned (informational listing).
- Badge names include: "Lido Testnet", "Lido Mainnet", "Genesis Dappnode", etc.

---

## Network Summary — `GET /v1/lock/network/summary/{network}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/lock/network/summary/mainnet"
```

**Response shape:**
```json
{
  "eth_staked": 596790,
  "total_clusters": 99,
  "total_operators": 294
}
```

**Notes:**
- `eth_staked` is total ETH staked across all DVT clusters on the network.
- Currently only `mainnet` returns reliable data; other networks may return errors.

---

## Terms & Conditions — `GET /v1/termsAndConditions/{address}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/termsAndConditions/0xAbCd...1234"
```

**Response shape:**
```json
{
  "isTermsAndConditionsSigned": true
}
```

---

## Example Conversations

**"My cluster 0x4d6e... has terrible performance, what's wrong?"**
1. Call `GET /v1/effectiveness/0x4d6e...` — check which validators underperform (look at `sevenDay` scores)
2. Call `GET /v1/state/0x4d6e...` — check for non-`active_ongoing` statuses
3. Call `GET /v1/lock/0x4d6e...` — cross-reference `cluster_definition.operators` to identify likely offline operators

**"How do I exit validator 0xb3a2c1...?"**
Exits are initiated in the Charon/validator client (write operation, not available here).
Use `GET /v1/exp/exit/status/0x...?validatorPubkey=0xb3a2c1...` to show current signing progress.

**"Is 0xAbCd... a trustworthy operator?"**
1. `GET /v1/address/techne/0xAbCd...` — check tiers for highest earned credential
2. `GET /v1/address/badges/0xAbCd...` — protocol affiliations (look for `qualified: true`)
3. `GET /v1/lock/operator/0xAbCd...` — how many clusters they run (check `total_count`)
4. `GET /v1/termsAndConditions/0xAbCd...` — T&C compliance (`isTermsAndConditionsSigned`)
