# Trustless Agents: Comprehensive ERC-8004 Lifecycle Support

**Goal:** Make the OpenClaw skills capable of end-to-end ERC-8004 agent identity management — register, update, query reputation, give feedback, request validation, and maintain a resolvable agentURI — all through `cast` and the existing `signer.py`/`tx-helper.sh` pipeline.

---

## Current State

### What exists
- **standards** skill: Registration walkthrough (Solidity-only), JSON schema, cross-chain pattern
- **orchestration** skill: TypeScript example of discover → trust → pay → rate cycle
- **local-ethereum-wallet** skill: `signer.py` (sign/send-tx via Web3Signer) + `tx-helper.sh` (cast-based calldata encoding, gas estimation)
- **ethereum-networks** skill: `rpc.sh` (cast-based read-only queries via eRPC)
- **ship** skill: Mentions ERC-8004 as optional for AI agent services
- **addresses** skill: Contract addresses listed
- **wallets** skill: Key management patterns

### What's missing
1. **No ABIs** — skills reference `registryAbi` / `reputationAbi` without providing them
2. **No cast-based write examples** — Solidity snippets can't be executed by an agent; cast commands can
3. **No agent-identity script** — no equivalent of `rpc.sh` for 8004 operations
4. **No ValidationRegistry coverage** — third registry entirely absent from skills
5. **No agentURI hosting** — "upload to IPFS" is hand-waved, no tooling
6. **No update/metadata lifecycle** — `setAgentURI`, `setMetadata`, `setAgentWallet`, `unsetAgentWallet` undocumented
7. **No reputation write flow** — `giveFeedback`, `revokeFeedback`, `appendResponse` undocumented
8. **No event querying** — no examples of filtering `Registered`, `NewFeedback`, `ValidationRequest` events

---

## Plan

### 1. Add ABI reference files

**New directory:** `standards/references/abis/`

**Files:**
- `IdentityRegistry.json` — full ABI from [erc-8004/erc-8004-contracts](https://github.com/erc-8004/erc-8004-contracts/tree/master/abis)
- `ReputationRegistry.json` — full ABI
- `ValidationRegistry.json` — full ABI

**Additionally:** `standards/references/erc8004-methods.md` — a human-readable quick-reference (like the existing `erc20-methods.md` in ethereum-networks), listing every function signature with parameter names and one-line descriptions. Organized by registry:

```
## IdentityRegistry (0x8004A169FB4a3325136EB29fA0ceB6D2e539a432)

### Write
register()                                                    → uint256 agentId
register(string agentURI)                                     → uint256 agentId
register(string agentURI, MetadataEntry[] metadata)           → uint256 agentId
setAgentURI(uint256 agentId, string newURI)                   owner-only
setAgentWallet(uint256 agentId, address newWallet, ...)       EIP-712 signature required
unsetAgentWallet(uint256 agentId)                             owner-only
setMetadata(uint256 agentId, string key, bytes value)         owner-only

### Read
tokenURI(uint256 tokenId)           → string (agentURI)
ownerOf(uint256 tokenId)            → address
getAgentWallet(uint256 agentId)     → address
getMetadata(uint256 agentId, key)   → bytes
balanceOf(address owner)            → uint256

## ReputationRegistry (0x8004BAa17C55a88189AE136b182e5fdA19dE9b63)

### Write
giveFeedback(uint256 agentId, int128 value, uint8 decimals, string tag1, string tag2, string endpoint, string feedbackURI, bytes32 feedbackHash)
revokeFeedback(uint256 agentId, uint64 feedbackIndex)        caller must be original poster
appendResponse(uint256 agentId, address client, uint64 index, string responseURI, bytes32 responseHash)

### Read
getSummary(uint256 agentId, address[] clients, string tag1, string tag2) → (count, value, decimals)
readFeedback(uint256 agentId, address client, uint64 index)  → (value, decimals, tag1, tag2, isRevoked)
readAllFeedback(uint256 agentId, address[] clients, tag1, tag2, includeRevoked) → arrays
getClients(uint256 agentId)                                   → address[]
getLastIndex(uint256 agentId, address client)                 → uint64

## ValidationRegistry (address TBD — not in addresses skill yet)

### Write
validationRequest(address validator, uint256 agentId, string requestURI, bytes32 requestHash)
validationResponse(bytes32 requestHash, uint8 response, string responseURI, bytes32 responseHash, string tag)

### Read
getValidationStatus(bytes32 requestHash) → (validator, agentId, response, responseHash, tag, lastUpdate)
getAgentValidations(uint256 agentId)     → bytes32[]
getValidatorRequests(address validator)  → bytes32[]
getSummary(uint256 agentId, address[] validators, string tag) → (count, avgResponse)
```

### 2. Create `agent-identity` skill

**New skill:** `agent-identity/`

```
agent-identity/
├── SKILL.md
├── scripts/
│   └── identity.sh        # cast-based CLI for all 8004 operations
└── references/
    ├── abis/
    │   ├── IdentityRegistry.json
    │   ├── ReputationRegistry.json
    │   └── ValidationRegistry.json
    └── erc8004-methods.md
```

**`SKILL.md` structure:**

```yaml
---
name: agent-identity
description: "Register, update, and manage ERC-8004 agent identities onchain. Give and query reputation. Request validation. Full lifecycle management via cast + Web3Signer."
metadata: { "openclaw": { "emoji": "🪪", "requires": { "bins": ["cast", "python3"] } } }
---
```

Sections:
- **When to Use** — registering an agent, updating URI/metadata, giving/reading feedback, requesting validation
- **When NOT to Use** — read-only blockchain queries (use ethereum-networks), key management (use wallets), deploying contracts (use orchestration)
- **Quick Start** — register, update, query reputation, give feedback in 4 commands
- **Contract Addresses** — all three registries, same on 20+ chains
- **Identity Lifecycle** — register → set metadata → update URI → set wallet → transfer ownership
- **Reputation Lifecycle** — give feedback → query summary → read individual → revoke → respond
- **Validation Lifecycle** — request validation → validator responds → query status → get summary
- **IPFS URI Management** — preparing registration JSON, pinning to IPFS, verifying
- **Event Querying** — filtering `Registered`, `URIUpdated`, `NewFeedback`, `ValidationRequest` events via `rpc.sh logs`
- **Cross-Chain Patterns** — register on Base (cheapest), query from any chain
- **Constraints** — signing via signer.py, reads via cast, confirm before writes

**`identity.sh` commands:**

The script follows the same pattern as `rpc.sh` and `tx-helper.sh` — a POSIX shell wrapper around `cast`. For **write operations**, it outputs the encoded calldata and a confirmation prompt, then delegates to `signer.py send-tx --data` for actual submission. For **read operations**, it calls `cast call` directly.

```
# Identity Registry — Write (outputs calldata or sends via signer.py)
identity.sh register [--uri <ipfs://...>] [--metadata key=value,...]
identity.sh set-uri <agentId> <newURI>
identity.sh set-metadata <agentId> <key> <value>
identity.sh set-wallet <agentId> <newWallet> <deadline> <signature>
identity.sh unset-wallet <agentId>

# Identity Registry — Read
identity.sh agent-uri <agentId>
identity.sh owner <agentId>
identity.sh agent-wallet <agentId>
identity.sh metadata <agentId> <key>
identity.sh balance <address>
identity.sh total-supply

# Reputation Registry — Write
identity.sh feedback <agentId> <value> <decimals> <tag1> <tag2> [--endpoint <url>] [--uri <ipfs://...>] [--hash <bytes32>]
identity.sh revoke-feedback <agentId> <feedbackIndex>
identity.sh respond <agentId> <client> <feedbackIndex> <responseURI> <responseHash>

# Reputation Registry — Read
identity.sh reputation <agentId> [--clients <addr1,addr2,...>] [--tag1 <tag>] [--tag2 <tag>]
identity.sh read-feedback <agentId> <client> <feedbackIndex>
identity.sh all-feedback <agentId> [--clients <addrs>] [--tag1 <tag>] [--tag2 <tag>] [--include-revoked]
identity.sh clients <agentId>

# Validation Registry — Write
identity.sh request-validation <validator> <agentId> <requestURI> <requestHash>
identity.sh validation-response <requestHash> <response> <responseURI> <responseHash> <tag>

# Validation Registry — Read
identity.sh validation-status <requestHash>
identity.sh agent-validations <agentId>
identity.sh validator-requests <validator>
identity.sh validation-summary <agentId> [--validators <addr1,...>] [--tag <tag>]

# Events
identity.sh events registered [--from-block N]
identity.sh events feedback <agentId> [--from-block N]
identity.sh events validation <agentId> [--from-block N]

# Utilities
identity.sh prepare-registration --name "MyAgent" --description "..." --services '[...]' [--x402] [--trust reputation,tee]
identity.sh verify-domain <agentId> <domain>
```

**Implementation notes:**

- Read operations use `cast call <address> <sig> <args> --rpc-url "$RPC_URL"` directly
- Write operations use `cast calldata <sig> <args>` to encode, then show a confirmation (what will be sent, estimated gas), then call `python3 ../local-ethereum-wallet/scripts/signer.py send-tx --from <addr> --to <registry> --data <calldata>`
- The `prepare-registration` command generates the JSON file locally and prints it. Actual IPFS pinning is a separate step (see section 5)
- Event queries use `cast logs` with topic filters
- The `--network` flag works the same as in `rpc.sh` — defaults to `mainnet`, configurable
- ABI files are referenced for cast's `--abi` flag where needed (complex structs like MetadataEntry[])

### 3. Update `standards` skill

Replace the Solidity-only integration section with cast-based examples that the agent can actually execute:

**Replace** the current "Integration" code block (Solidity) with:

```bash
# Register agent with URI
sh scripts/identity.sh register --uri "ipfs://QmYourRegistrationHash"

# Update URI after changes
sh scripts/identity.sh set-uri 42 "ipfs://QmNewHash"

# Set arbitrary metadata
sh scripts/identity.sh set-metadata 42 "x402.supported" "0x01"

# Query reputation
sh scripts/identity.sh reputation 42 --tag1 "uptime" --tag2 "30days"

# Give feedback after interacting with agent 42
sh scripts/identity.sh feedback 42 95 0 "quality" "weather" \
  --endpoint "https://weather.agent.example.com" \
  --uri "ipfs://QmFeedbackDetails"
```

**Add** the ValidationRegistry to the "Three Registry System" section — currently missing entirely:

```
**3. Validation Registry**
- Independent third-party verification of agent work
- Trust models: crypto-economic (stake-secured), zkML, TEE attestation
- Validators respond with 0-100 scores
- Contract: address same as IdentityRegistry pattern (CREATE2)
```

**Add** a "Full Lifecycle" section showing the complete cast-based flow:
1. Prepare registration JSON
2. Pin to IPFS
3. Register onchain
4. Set metadata
5. Verify domain
6. Receive feedback
7. Query/respond to feedback
8. Request validation

**Update** the Step-by-Step section to reference `identity.sh` instead of raw Solidity.

**Add** a cross-reference: "See `agent-identity` skill for the full CLI reference and scripts."

### 4. Update `ethereum-networks` skill

Add ERC-8004 read examples to demonstrate that the existing `rpc.sh` can already query these contracts:

```bash
# Read agent URI (tokenURI)
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "tokenURI(uint256)(string)" 42

# Check agent owner
sh scripts/rpc.sh call 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "ownerOf(uint256)(address)" 42

# Get reputation summary
sh scripts/rpc.sh call 0x8004BAa17C55a88189AE136b182e5fdA19dE9b63 \
  "getSummary(uint256,address[],string,string)(uint64,int128,uint8)" 42 "[]" "quality" "30days"

# Query registration events
sh scripts/rpc.sh logs 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  0x$(cast sig-event "Registered(uint256,string,address)") --from-block 0
```

Add a note: "For write operations (registration, feedback, metadata updates), use the `agent-identity` skill."

Add the IdentityRegistry and ReputationRegistry to `references/common-contracts.md`:

```
## ERC-8004 Agent Identity (same address on 20+ chains)
| Contract            | Address                                      |
|---------------------|----------------------------------------------|
| IdentityRegistry    | 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432   |
| ReputationRegistry  | 0x8004BAa17C55a88189AE136b182e5fdA19dE9b63   |
```

### 5. Update `local-ethereum-wallet` skill

Add a section "Contract Interactions (ERC-8004 Example)" showing how `signer.py send-tx --data` works with encoded calldata from `tx-helper.sh`:

```bash
# Encode registration calldata
CALLDATA=$(sh scripts/tx-helper.sh calldata "register(string)" "ipfs://QmYourHash")

# Estimate gas
sh scripts/tx-helper.sh estimate 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  "register(string)" "ipfs://QmYourHash"

# Sign and send
python3 scripts/signer.py send-tx \
  --from 0xYourAddress \
  --to 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432 \
  --data "$CALLDATA"
```

This bridges the gap between "we have a signer" and "we can interact with 8004 contracts."

### 6. Update `addresses` skill

Add the ValidationRegistry address once confirmed. Currently only IdentityRegistry and ReputationRegistry are listed. Verify whether the ValidationRegistry has the same CREATE2 pattern.

### 7. Update `orchestration` skill

Replace the TypeScript agent example with a cast-based equivalent that runs in the OpenClaw pod:

```bash
# 1. Discover: find agents registered for a service
sh scripts/identity.sh events registered --from-block 0
# Filter for agents with "weather" in their URI metadata

# 2. Trust: check reputation
sh scripts/identity.sh reputation <agentId> --tag1 "quality" --tag2 "30days"

# 3-5. Call + Pay: x402Fetch (still TS — agent commerce needs a wallet SDK)
# ... (keep the TS example for the x402 part)

# 6. Rate: post feedback
sh scripts/identity.sh feedback <agentId> 95 0 "quality" "weather" \
  --endpoint "https://weather.agent.example.com"
```

Keep the TypeScript example too — it's valuable for SE2 dApp builders. But add the shell equivalent for agents running in the Obol Stack.

### 8. Update `ship` skill

Expand the "AI Agent Service" archetype (currently 4 lines) with concrete guidance:

- When to use ERC-8004 registration (public-facing agent service with reputation)
- When not to (internal agent, no need for discovery)
- Registration cost (just gas — no fee in the contract)
- Recommended chain (Base — cheapest, largest ERC-8004 ecosystem)
- Link to `agent-identity` skill for the full workflow

### 9. Depend on `cast` (Foundry)

`cast` is already a dependency of `ethereum-networks` (via `rpc.sh`) and `local-ethereum-wallet` (via `tx-helper.sh`). The new `agent-identity` skill uses the same dependency — no new binary required.

**Verify cast availability in the OpenClaw pod:** The skill metadata declares `"requires": { "bins": ["cast"] }` and OpenClaw checks for binary availability. If cast is not installed, the skill should degrade gracefully with a clear error: "cast (Foundry) is required. Install: curl -L https://foundry.paradigm.xyz | bash && foundryup"

**cast features used:**
- `cast call` — read contract state
- `cast calldata` — encode function calldata for write operations
- `cast send` — NOT used directly (signing goes through Web3Signer)
- `cast logs` — query events
- `cast sig` — get function selectors
- `cast sig-event` — get event topic hashes
- `cast abi-decode` — decode return data
- `cast keccak` — hash data (for feedbackHash)
- `cast to-hex` / `cast from-hex` — unit conversion

### 10. IPFS integration for agentURI

**Current approach (IPFS-first):**

The `identity.sh prepare-registration` command generates the JSON. For pinning, we rely on the agent having access to an IPFS node. Two paths:

**Path A: In-cluster IPFS node**
- Deploy an IPFS node (kubo) as a k3d service in the `ipfs` namespace
- Expose at `ipfs.ipfs.svc.cluster.local:5001` (API) and port 8080 (gateway)
- `identity.sh` pins via `curl -X POST "http://ipfs:5001/api/v0/add" -F file=@registration.json`
- Returns CID for use as agentURI

**Path B: External pinning service**
- Use Pinata, web3.storage, or similar
- Agent provides API key via environment variable
- `identity.sh` wraps the pinning API

For now, **Path A is recommended** — it's local-first and doesn't require external accounts.

Add a `pin` command to `identity.sh`:

```bash
# Pin a file to IPFS and return the CID
identity.sh pin <file>
# → QmYourCID

# Pin registration JSON (combines prepare-registration + pin)
identity.sh pin-registration --name "MyAgent" --description "..." --services '[...]'
# → ipfs://QmYourCID
```

---

## Future: Traefik-Exposed Agent URI API

**Problem:** IPFS URIs are content-addressed and immutable. When an agent's services change, you need to re-pin, get a new CID, and call `setAgentURI` onchain (costs gas). For agents that update frequently, this is wasteful.

**Solution:** A small HTTP service behind Traefik that serves the agent registration JSON at a stable URL. The onchain `agentURI` points to this URL instead of IPFS. Updates are instant and free (no gas).

### Architecture

```
Internet
  │
  ▼
Cloudflare Tunnel (obol tunnel provision)
  │
  ▼
Traefik Gateway (traefik namespace)
  │  HTTPRoute: /agents/{agentId}/registration.json
  │  HTTPRoute: /agents/{agentId}/.well-known/agent-registration.json
  ▼
agent-uri-server (new service, agent-uri namespace)
  │  Reads registration JSON from ConfigMap or PVC
  │  Serves at stable URLs
  │  Handles .well-known/agent-registration.json for domain verification
  ▼
Storage: ConfigMap (small) or PVC (if scripts/images needed)
```

### How it works

1. **Agent registers with a URL agentURI** instead of IPFS:
   ```
   agentURI: https://<tunnel-domain>/agents/42/registration.json
   ```

2. **The agent-uri-server** is a minimal Go HTTP server (or even a static file server like nginx) that:
   - Serves `registration.json` for each agent from a config directory
   - Serves `.well-known/agent-registration.json` for domain verification
   - Supports hot-reload when config changes (via Reloader watching the ConfigMap)

3. **Updating the registration** is just a ConfigMap patch:
   ```bash
   kubectl patch configmap agent-42-registration -n agent-uri \
     --type merge -p '{"data":{"registration.json":"{...new JSON...}"}}'
   ```
   No gas, no IPFS re-pin, no `setAgentURI` call. The URL stays the same, the content changes.

4. **Traefik routing** via HTTPRoute:
   ```yaml
   apiVersion: gateway.networking.k8s.io/v1
   kind: HTTPRoute
   metadata:
     name: agent-uri
     namespace: agent-uri
   spec:
     parentRefs:
       - name: traefik-gateway
         namespace: traefik
     rules:
       - matches:
           - path:
               type: PathPrefix
               value: /agents/
         backendRefs:
           - name: agent-uri-server
             port: 8080
   ```

5. **Public access** via Cloudflare tunnel (already exists in the stack):
   - `obol tunnel provision` sets up the tunnel
   - Traefik already routes based on path prefix
   - The agent URI becomes: `https://<your-tunnel-domain>/agents/42/registration.json`

### CLI integration

```bash
# Deploy the agent-uri-server (one-time setup)
obol app install agent-uri

# Publish a registration (creates ConfigMap + serves at URL)
identity.sh publish-registration --agent-id 42 \
  --name "MyAgent" --description "..." --services '[...]'
# → https://<tunnel-domain>/agents/42/registration.json

# Update registration (patches ConfigMap, no gas)
identity.sh update-registration --agent-id 42 \
  --services '[...new services...]'

# Register onchain with the URL
identity.sh register --uri "https://<tunnel-domain>/agents/42/registration.json"
```

### Trade-offs vs IPFS

| Aspect | IPFS | Traefik API |
|--------|------|-------------|
| Immutability | Content-addressed, immutable | Mutable — URL stays, content changes |
| Update cost | New CID + `setAgentURI` (gas) | Free (ConfigMap patch) |
| Availability | Depends on IPFS pinning | Depends on tunnel/cluster uptime |
| Decentralization | Fully decentralized | Centralized to your cluster |
| Verification | CID = hash of content | Need `.well-known` domain verification |
| Best for | Stable, infrequent updates | Active agents with changing services |

### Hybrid approach (recommended future state)

1. **Pin to IPFS** as the canonical, immutable record
2. **Serve via Traefik** as the hot, mutable endpoint
3. **agentURI** points to the Traefik URL for fast updates
4. **Metadata** stores the IPFS CID as a backup: `setMetadata(agentId, "ipfs.backup", cidBytes)`
5. **Clients** can verify by checking the IPFS backup if the URL is down

---

## Implementation Order

| Phase | Work | Skills touched | Effort |
|-------|------|---------------|--------|
| **1** | Create `references/erc8004-methods.md` + download ABIs | standards | Small |
| **2** | Create `agent-identity` skill with SKILL.md + `identity.sh` | new skill | Large |
| **3** | Update `standards` SKILL.md — cast examples, ValidationRegistry, full lifecycle | standards | Medium |
| **4** | Update `ethereum-networks` — 8004 read examples, common-contracts.md | ethereum-networks | Small |
| **5** | Update `local-ethereum-wallet` — contract interaction example | local-ethereum-wallet | Small |
| **6** | Update `orchestration` — shell-based agent commerce flow | orchestration | Small |
| **7** | Update `ship` — expand AI agent archetype | ship | Small |
| **8** | Update `addresses` — add ValidationRegistry | addresses | Small |
| **9** | Add IPFS pin command to `identity.sh` | agent-identity | Medium |
| **10** | (Future) Build agent-uri-server + Traefik route | new infra | Large |

Phases 1-8 are the core skill updates. Phase 9 adds IPFS tooling. Phase 10 is the future Traefik API — outlined here for planning but not blocked on.

---

## Validation Criteria

- [ ] Agent can register an identity with `identity.sh register --uri <ipfs://...>` end-to-end
- [ ] Agent can query any agent's reputation with `identity.sh reputation <id>`
- [ ] Agent can give feedback after interacting with another agent
- [ ] Agent can update its own URI and metadata without Solidity knowledge
- [ ] Agent can query registration and feedback events
- [ ] All write operations confirm before sending (show calldata, gas estimate, target)
- [ ] All scripts are POSIX sh (no bashisms), work in the OpenClaw pod
- [ ] ABIs are available as reference files for complex struct encoding
- [ ] `standards` skill documents all three registries with cast-based examples
- [ ] Skills cross-reference correctly: standards ↔ agent-identity ↔ ethereum-networks ↔ local-ethereum-wallet
