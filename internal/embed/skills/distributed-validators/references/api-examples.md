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
  "lock_hash": "0x4d6e7f8a9b...",
  "config_hash": "0x1a2b3c4d5e...",
  "name": "my-dv-cluster",
  "network": "mainnet",
  "threshold": 3,
  "num_validators": 4,
  "operators": [
    { "address": "0xAbCd...1234", "enr": "enr:-...", "approved": true }
  ],
  "validators": [
    { "public_key": "0xb3a2c1...", "fee_recipient_address": "0xDead...Beef" }
  ],
  "created_at": "2024-03-15T10:22:00Z"
}
```

**What to tell the user:** "Your cluster has 4 operators with a 3-of-4 threshold running 4
validators on mainnet. All operators have approved the configuration."

---

## Effectiveness — `GET /v1/effectiveness/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/effectiveness/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "effectiveness": [
    {
      "public_key": "0xb3a2c1...",
      "effectiveness": 0.987,
      "attestation_effectiveness": 0.991,
      "proposal_effectiveness": 1.0
    },
    {
      "public_key": "0xc4d3e2...",
      "effectiveness": 0.612,
      "attestation_effectiveness": 0.608,
      "proposal_effectiveness": null
    }
  ]
}
```

**Notes:**
- Scores are 0–1; anything above ~0.95 is healthy.
- `proposal_effectiveness: null` means no proposals have occurred yet — not a problem.
- Low `attestation_effectiveness` (e.g. 0.6) usually means one or more operators are offline
  or have connectivity issues with the rest of the cluster.

---

## Validator States — `GET /v1/state/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/state/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "validators": [
    {
      "public_key": "0xb3a2c1...",
      "index": 412503,
      "status": "active_ongoing",
      "balance": "32045231042"
    },
    {
      "public_key": "0xc4d3e2...",
      "index": 412504,
      "status": "active_exiting",
      "balance": "32001000000"
    }
  ]
}
```

**Notes:**
- `balance` is in Gwei — divide by 1,000,000,000 for ETH (e.g. 32045231042 = 32.045 ETH).
- `active_exiting` means an exit has been initiated; pair with exit status summary to see signing progress.

---

## Exit Status Summary — `GET /v1/exp/exit/status/summary/{lockHash}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/exp/exit/status/summary/0x4d6e7f8a9b..."
```

**Response shape:**
```json
{
  "total_validators": 4,
  "validators_ready_to_exit": 1,
  "operators": [
    { "address": "0xAbCd...1234", "signed_exits": 3 },
    { "address": "0xEfGh...5678", "signed_exits": 3 },
    { "address": "0xIjKl...9012", "signed_exits": 2 },
    { "address": "0xMnOp...3456", "signed_exits": 1 }
  ]
}
```

**What to tell the user:** "1 of 4 validators has reached the 3-of-4 threshold and is ready
to exit. Operators `0xIjKl...` and `0xMnOp...` still need to sign exits for the remaining
validators."

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
  "network": "mainnet",
  "threshold": 3,
  "num_validators": 4,
  "operators": [
    { "address": "0xAbCd...1234", "approved": false },
    { "address": "0xEfGh...5678", "approved": true }
  ],
  "created_at": "2024-03-14T08:00:00Z"
}
```

**What to tell the user:** "DKG hasn't happened yet — operator `0xAbCd...` still needs to
approve the definition before the ceremony can begin."

---

## Operator Techne — `GET /v1/address/techne/{address}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/address/techne/0xAbCd...1234"
```

**Response shape:**
```json
{
  "address": "0xAbCd...1234",
  "credential_level": "silver",
  "issued_at": "2024-01-20T09:00:00Z"
}
```

Levels: `base` < `bronze` < `silver`. Silver = sustained high-quality mainnet operation.

---

## Operator Badges — `GET /v1/address/badges/{address}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/address/badges/0xAbCd...1234"
```

**Response shape:**
```json
{
  "address": "0xAbCd...1234",
  "badges": [
    { "type": "lido" },
    { "type": "etherfi" }
  ]
}
```

Badges indicate protocol participation (Lido CSM, EtherFi, etc.).

---

## Migrateable Validators — `GET /v1/address/migrateable-validators/{network}/{withdrawalAddress}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/address/migrateable-validators/mainnet/0xDead...Beef?limit=10&offset=0"
```

**Response shape:**
```json
{
  "validators": [
    {
      "public_key": "0xaabbcc...",
      "index": 300001,
      "status": "active_ongoing",
      "balance": "32100000000"
    }
  ],
  "total": 3
}
```

**What to tell the user:** "You have 3 active validators eligible for DVT migration. The
process: create a cluster definition with a matching withdrawal address, complete DKG, activate
the DVT cluster, then exit the solo validator."

---

## Network Summary — `GET /v1/lock/network/summary/{network}`

**curl:**
```bash
curl -s "https://api.obol.tech/v1/lock/network/summary/mainnet"
```

**Response shape:**
```json
{
  "network": "mainnet",
  "total_clusters": 312,
  "total_validators": 1248,
  "avg_effectiveness": 0.971,
  "total_operators": 89
}
```

---

## Example Conversations

**"My cluster 0x4d6e... has terrible performance, what's wrong?"**
1. Call `GET /v1/effectiveness/0x4d6e...` — check which validators underperform
2. Call `GET /v1/state/0x4d6e...` — check for non-`active_ongoing` statuses
3. Call `GET /v1/lock/0x4d6e...` — cross-reference operator list to identify likely offline operators

**"How do I exit validator 0xb3a2c1...?"**
Exits are initiated in the Charon/validator client (write operation, not available here).
Use `GET /v1/exp/exit/status/0x...?validatorPubkey=0xb3a2c1...` to show current signing progress.

**"Is 0xAbCd... a trustworthy operator?"**
1. `GET /v1/address/techne/0xAbCd...` — credential level
2. `GET /v1/address/badges/0xAbCd...` — protocol affiliations
3. `GET /v1/lock/operator/0xAbCd...` — how many clusters they run
4. `GET /v1/termsAndConditions/0xAbCd...` — T&C compliance
