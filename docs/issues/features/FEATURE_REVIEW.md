# Feature File Review: Gherkin Best Practices, ERC-8004 & x402 Edge Cases

## Files Reviewed
1. multi_tier_worker_discovery_with_fallback.feature (47 lines)
2. reward_pool_distribution_across_roles.feature (59 lines)
3. end_to_end_autoresearch_round.feature (59 lines)
4. opow_influence_calculation_with_anti_monopoly_parity.feature (69 lines)
5. escrow_round_lifecycle.feature (76 lines)
6. commit_reveal_work_verification.feature (63 lines)

---

## 1. GHERKIN BEST PRACTICES ISSUES

### 1a. Missing tags for filtering
- discovery.feature: has @discovery, good
- reward.feature: has @rewards, good
- e2e.feature: has @e2e @slow, good
- opow.feature: has @opow @critical, good
- escrow.feature: has @escrow @critical, good
- verification.feature: has @verification @critical, good
- ISSUE: No @erc8004 or @x402 tags on relevant scenarios. These cross-cutting
  concerns should be tagged for protocol-specific test runs.

### 1b. Over-long E2E scenario
- end_to_end_autoresearch_round.feature "Complete round with two honest workers"
  has ~25 steps with inline comments. Gherkin best practice says scenarios should
  be 5-10 steps max. The comments (# Round setup, # Worker experiments, etc.)
  are a code smell indicating this should be split into focused scenarios or use
  a scenario outline.
- RECOMMENDATION: Split into "Round initialization with escrow", "Worker
  experiments and verification", "Reward settlement" as separate scenarios
  chained via shared state, or keep as a single narrative but trim to essential
  assertions.

### 1c. Magic numbers without context
- escrow.feature line 39: "42 USDC" and "28 USDC" appear without showing the
  math. A reader must compute 100 * 0.7 * 0.6 = 42 themselves. Add a comment
  or use a scenario outline with formula reference.
- opow.feature: penalty values (0.65, 0.11, 0.05) lack formula reference.
  Consider adding a comment showing the parity formula being applied.

### 1d. Inconsistent Background granularity
- escrow.feature Background specifies contract address "0xBdEA0D1bcC5...",
  which is good for precision.
- discovery.feature Background uses only OASF skill filter but no contract
  address or chain ID. Since the ERC-8004 contract is at a fixed address on
  Base, this should be specified.

### 1e. No "Rule:" groupings in some files
- e2e.feature lacks Rule: groupings entirely. Even for an E2E feature, Rules
  help organize the phases (setup, execution, settlement).

---

## 2. ERC-8004 METADATA READING GAPS (Question 1)

### What exists:
- discovery.feature mentions "ERC-8004 NFT metadata is read for each agent"
  (line 24) and "workers with the model_versioning skill are returned" but
  this is extremely shallow.

### What is MISSING:

#### 2a. No tokenURI resolution scenario
The ERC-8004 spec requires calling tokenURI(tokenId) to get the registration
JSON URL. There is no scenario testing:
- tokenURI returns a valid HTTPS URL
- tokenURI returns an IPFS URL (needs gateway resolution)
- tokenURI returns empty/malformed data
- tokenURI call reverts (token burned or contract paused)

RECOMMENDED SCENARIO:
```gherkin
Scenario: Discovery resolves tokenURI to registration JSON
  Given worker "0xW001" has ERC-8004 token ID 12345
  When the coordinator calls tokenURI(12345)
  Then a valid registration JSON URL is returned
  And the JSON is fetched and parsed

Scenario: Discovery handles IPFS tokenURI with gateway fallback
  Given worker "0xW001" has tokenURI "ipfs://Qm..."
  When the coordinator resolves the tokenURI
  Then the IPFS gateway is used to fetch the registration JSON
  And the registration is successfully parsed
```

#### 2b. No registration JSON schema validation scenario
The ERC-8004 AgentRegistration document has specific required fields (name,
description, services[], supportedTrust[]). No scenario tests:
- JSON missing required fields
- JSON with unknown/extra fields
- JSON with invalid service types
- JSON with services[].endpoint that is unreachable

RECOMMENDED SCENARIO:
```gherkin
Scenario: Discovery rejects agent with malformed registration JSON
  Given agent "0xW003" has registration JSON missing "services" field
  When the coordinator parses the registration
  Then agent "0xW003" is excluded from discovery results
  And a warning is logged with the token ID and missing field
```

#### 2c. No OASF taxonomy filtering scenario
The Background says `the OASF skill filter is "devops_mlops/model_versioning"`
but there is NO scenario testing:
- How the skill filter maps to registration JSON fields
- What happens when an agent has multiple skills (partial match)
- What happens when an agent has no skills listed
- Hierarchical taxonomy matching (e.g., "devops_mlops/*" wildcard)
- The ServiceOffer CRD has services[].name with types: web, A2A, MCP, OASF,
  ENS, DID, email — but the feature file never references OASF as a service type

RECOMMENDED SCENARIOS:
```gherkin
Scenario: Discovery filters agents by OASF taxonomy path
  Given agent "0xW001" has OASF service with skill "devops_mlops/model_versioning"
  And agent "0xW002" has OASF service with skill "security/threat_detection"
  When the coordinator discovers workers with filter "devops_mlops/model_versioning"
  Then only agent "0xW001" is returned

Scenario: Discovery supports wildcard OASF taxonomy matching
  Given agent "0xW001" has skill "devops_mlops/model_versioning"
  And agent "0xW002" has skill "devops_mlops/container_orchestration"
  When the coordinator discovers workers with filter "devops_mlops/*"
  Then both agents are returned

Scenario: Agent with no OASF service entry is excluded from skill-filtered queries
  Given agent "0xW003" has only a "web" service entry (no OASF)
  When the coordinator discovers workers with any OASF skill filter
  Then agent "0xW003" is not in the results
```

---

## 3. x402 PaymentRequirements & ESCROW SCHEME (Question 2)

### What exists:
- escrow.feature tests authorize(), capture(), void(), reclaim(), refund()
- e2e.feature mentions "x402 payments were collected" as a precondition
- ServiceOffer CRD defines payment.scheme as "exact" (only enum value)

### What is MISSING:

#### 3a. No PaymentRequirements generation scenario
The ServiceOffer CRD (serviceoffer-crd.yaml) defines x402 PaymentRequirements
fields (payTo, network, scheme, maxTimeoutSeconds, price) but NO feature tests:
- PaymentRequirements struct generation from ServiceOffer spec
- CAIP-2 network resolution ("base-sepolia" -> "eip155:84532")
- maxTimeoutSeconds enforcement
- Price calculation (perRequest vs perMTok vs perHour)

RECOMMENDED SCENARIOS:
```gherkin
@x402
Scenario: ServiceOffer generates valid x402 PaymentRequirements
  Given a ServiceOffer with network "base-sepolia" and payTo "0xAAA"
  And price.perRequest is "0.01"
  And scheme is "exact"
  When the reconciler generates PaymentRequirements
  Then the network field is "eip155:84532" (CAIP-2)
  And the payTo field is "0xAAA"
  And the maxAmountRequired matches "0.01" in USDC base units

Scenario: Escrow scheme PaymentRequirements includes authorization metadata
  Given the escrow round manager is preparing round 7
  And the reward pool is 60 USDC
  When PaymentRequirements are generated for the escrow scheme
  Then the scheme field is "escrow"
  And the authorizationId references the current round
  And the maxAmountRequired equals 60 USDC
  And the authorizationExpiry is set to round_end + grace_period
```

#### 3b. No "exact" vs "escrow" scheme distinction
The CRD only allows scheme: "exact". But the escrow round lifecycle clearly
uses a different payment flow (authorize/capture/void). There is no scenario
showing how the two schemes coexist:
- Worker earns via x402 "exact" scheme (instant per-request payments)
- Platform collects those payments, then uses escrow for reward distribution
- The feature files treat these as independent but never show the handoff

RECOMMENDED SCENARIO:
```gherkin
Scenario: x402 exact payments flow into escrow pool for next round
  Given workers served 200 x402 "exact" scheme requests in round N
  And the total collected USDC is 200
  When round N+1 begins
  Then the escrow pool is 200 * 30% = 60 USDC
  And the escrow authorization uses the "escrow" scheme internally
  And workers can verify the pool amount on-chain
```

---

## 4. MISSING EDGE CASE SCENARIOS (Question 3)

### 4a. Worker re-registration mid-round
NO SCENARIO EXISTS. Critical gap because ERC-8004 allows updating registration
at any time.

```gherkin
@erc8004
Scenario: Worker updates ERC-8004 registration mid-round
  Given worker "0xW001" is participating in round 5
  And worker "0xW001" updates their registration JSON to remove the
    "devops_mlops/model_versioning" skill
  When the round completes
  Then worker "0xW001" still qualifies for round 5 (snapshot at round start)
  But worker "0xW001" is NOT discovered for round 6

Scenario: Worker re-registers with a different address mid-round
  Given worker "0xW001" is participating in round 5
  And worker "0xW001" registers a new ERC-8004 token with address "0xW001b"
  When the round completes
  Then only the original registration is used for round 5 settlement
```

### 4b. ERC-8004 NFT transfer during round
NO SCENARIO EXISTS. Since ERC-8004 tokens are NFTs, they can be transferred.
This could cause:
- Worker loses ownership of their identity mid-round
- New owner could try to claim rewards
- The payTo address no longer matches the NFT owner

```gherkin
@erc8004
Scenario: ERC-8004 NFT transferred during active round
  Given worker "0xW001" owns ERC-8004 token 12345
  And worker "0xW001" is a qualifier in round 5
  When token 12345 is transferred to "0xATTACKER" during the round
  Then capture() still pays "0xW001" (the address that did the work)
  And the new NFT owner "0xATTACKER" receives nothing for round 5

Scenario: Discovery uses token ownership snapshot at round start
  Given worker "0xW001" owned token 12345 at block 1000 (round start)
  And token 12345 was transferred to "0xW002" at block 1005
  When the coordinator discovers workers at round start
  Then "0xW001" is the registered worker, not "0xW002"
```

### 4c. x402 facilitator timeout
NO SCENARIO EXISTS. The ServiceOffer CRD has maxTimeoutSeconds (default: 300).

```gherkin
@x402
Scenario: x402 payment verification times out
  Given a ServiceOffer with maxTimeoutSeconds of 300
  And a buyer sends a payment header
  When the x402 facilitator does not respond within 300 seconds
  Then the payment is considered failed
  And the request is rejected with HTTP 402
  And no USDC is deducted from the buyer

Scenario: x402 facilitator timeout during round does not affect escrow
  Given the escrow authorization for round 5 is already locked
  And an x402 facilitator timeout occurs during the round
  Then the escrow authorization remains valid
  And workers can still submit proofs
  But the affected request is not counted toward x402 revenue for round 6
```

### 4d. BaseScan rate limiting
NO SCENARIO EXISTS. BaseScan free tier is 5 req/sec. With 18,512 ERC-8004
holders, pagination + metadata fetching will hit limits.

```gherkin
@discovery
Scenario: BaseScan API returns HTTP 429 rate limit
  Given the coordinator is using BaseScan for discovery
  And the BaseScan API returns HTTP 429 after 5 requests
  When the coordinator discovers workers
  Then the coordinator implements exponential backoff
  And retries after the Retry-After header period
  And eventually returns partial results with a warning

Scenario: BaseScan rate limiting causes fallback to 8004scan
  Given the coordinator is using BaseScan for discovery
  And the BaseScan API consistently returns HTTP 429
  When 3 consecutive retries fail
  Then the coordinator falls back to 8004scan.io
  And workers are still discovered successfully
```

### 4e. Chain reorg affecting indexer data
NO SCENARIO EXISTS. The Reth indexer syncs Base chain data. Base has finality
~2 seconds but reorgs do happen.

```gherkin
@discovery
Scenario: Chain reorg removes a recent ERC-8004 registration
  Given the Reth indexer has synced to block 1000
  And agent "0xNEW" was registered at block 999
  When a 2-block reorg occurs at block 999
  And the new chain does not contain the registration transaction
  Then the indexer re-processes blocks 999-1000
  And agent "0xNEW" is removed from discovery results

Scenario: Coordinator uses confirmation depth for registration finality
  Given the Reth indexer requires 12-block confirmation depth
  And a new registration appears at block 1000
  When the current block is 1005 (only 5 confirmations)
  Then the registration is not yet included in discovery results
  When the current block reaches 1012
  Then the registration becomes discoverable
```

---

## 5. LEADERBOARD API FEATURE (Question 4)

YES, a leaderboard feature is needed. Currently:
- The issue doc says "Exposes leaderboard API (GET /leaderboard, GET /round/:id)"
- The e2e feature mentions it once (line 43): "the leaderboard API shows both
  workers with correct earnings"
- But there is NO dedicated feature file testing the leaderboard API

RECOMMENDED: New file `leaderboard_api.feature`:

```gherkin
@leaderboard @api
Feature: Leaderboard API
  The reward engine exposes a leaderboard API that shows per-worker
  earnings, influence, and qualification status across rounds.

  Background:
    Given the autoresearch chart is deployed
    And rounds 1-3 have completed with verified workers

  Rule: Current round leaderboard shows live state

    Scenario: GET /leaderboard returns ranked workers by cumulative earnings
      When a client requests GET /leaderboard
      Then workers are returned sorted by total USDC earned descending
      And each entry includes address, total_earned, rounds_participated, avg_influence

    Scenario: GET /leaderboard includes only ERC-8004 registered workers
      Given worker "0xAAA" is registered on ERC-8004
      And worker "0xBBB" was registered but token was burned
      When a client requests GET /leaderboard
      Then worker "0xAAA" appears in results
      And worker "0xBBB" does not appear

  Rule: Per-round details are queryable

    Scenario: GET /round/:id returns round-specific data
      When a client requests GET /round/3
      Then the response includes:
        | field               | type    |
        | round_id            | integer |
        | pool_usdc           | decimal |
        | num_qualifiers      | integer |
        | num_excluded        | integer |
        | captures            | array   |
        | voided_usdc         | decimal |
        | escrow_tx_hash      | string  |

    Scenario: GET /round/:id for non-existent round returns 404
      When a client requests GET /round/9999
      Then the response status is 404

  Rule: Leaderboard reflects escrow settlement accurately

    Scenario: Leaderboard updates only after capture() confirms on-chain
      Given round 5 just completed
      And capture() has been called for worker "0xAAA"
      But the transaction is still pending
      When a client requests GET /leaderboard
      Then worker "0xAAA" earnings do NOT include round 5 yet
      When the capture transaction confirms
      Then worker "0xAAA" earnings include round 5
```

---

## 6. ROUND-OVER-ROUND STATE FEATURE (Question 5)

YES, a round-over-round state feature is needed. Currently:
- reward.feature line 45 mentions "the unadopted share rolls into the next round"
- The issue doc describes void() returning uncaptured funds for the next round
- But there is NO feature testing the cumulative/rollover mechanics

RECOMMENDED: New file `round_over_round_state.feature`:

```gherkin
@rounds @state
Feature: Round-over-round state (pool rollover, cumulative earnings)
  The reward engine maintains state across rounds, rolling uncaptured
  funds into the next round's pool and tracking cumulative worker
  performance.

  Background:
    Given the reward pool percentage is 30%
    And the platform wallet holds 1000 USDC

  Rule: Uncaptured funds roll into the next round

    Scenario: Voided USDC from round N increases round N+1 pool
      Given round 1 collected 200 USDC in x402 payments
      And the round 1 pool was 60 USDC
      And only 40 USDC was captured (void returned 20 USDC)
      When round 2 begins
      Then the round 2 pool includes the 20 USDC rollover
      And the total round 2 pool is (round2_x402_revenue * 30%) + 20 USDC

    Scenario: Fully captured round has zero rollover
      Given round 1 pool was 60 USDC
      And all 60 USDC was captured across workers
      When round 2 begins
      Then the round 2 pool is exactly (round2_x402_revenue * 30%)

  Rule: Cumulative earnings are tracked per worker

    Scenario: Worker earnings accumulate across rounds
      Given worker "0xAAA" earned 42 USDC in round 1
      And worker "0xAAA" earned 35 USDC in round 2
      When the cumulative earnings are queried
      Then worker "0xAAA" has total earnings of 77 USDC

    Scenario: Worker who skips a round retains prior earnings
      Given worker "0xAAA" earned 42 USDC in round 1
      And worker "0xAAA" did not participate in round 2
      When the cumulative earnings are queried after round 2
      Then worker "0xAAA" still has total earnings of 42 USDC

  Rule: Round numbering is monotonic and gap-free

    Scenario: Failed round start still increments round counter
      Given round 5 completed successfully
      And round 6 failed to authorize escrow (insufficient funds)
      When the next successful authorization occurs
      Then it is labeled round 7 (round 6 is recorded as failed)
      And round 6 shows zero pool and zero captures in history
```

---

## 7. ERC-8004 IDENTITY REVOCATION/DEACTIVATION MID-ROUND (Question 6)

NO SCENARIOS EXIST. This is a critical gap. ERC-8004 tokens can be:
- Burned (destroying the identity)
- Transferred (changing ownership)
- Have their registration JSON updated (removing services)

RECOMMENDED SCENARIOS (add to discovery.feature or new file):

```gherkin
@erc8004 @critical
Rule: ERC-8004 identity changes during active round

  Scenario: Worker's ERC-8004 token is burned mid-round
    Given worker "0xW001" has ERC-8004 token 12345
    And worker "0xW001" is a qualifier in round 5 with verified proofs
    When token 12345 is burned during the round
    Then worker "0xW001" STILL receives their capture for round 5
      (work was verified before identity was revoked)
    But worker "0xW001" is NOT discovered for round 6

  Scenario: Worker's ERC-8004 registration JSON is updated to remove services
    Given worker "0xW001" has OASF service "devops_mlops/model_versioning"
    And worker "0xW001" is participating in round 5
    When worker "0xW001" updates their registration JSON to remove all services
    Then round 5 continues using the snapshot taken at round start
    And worker "0xW001" is excluded from round 6 discovery

  Scenario: ERC-8004 contract is paused during active round
    Given the ERC-8004 contract at 0x8004...9432 is paused
    And round 5 has already started with discovered workers
    When the round completes
    Then captures are still executed (escrow is independent of ERC-8004)
    But round 6 discovery fails with "ERC-8004 contract paused" error

  Scenario: Worker address is sanctioned/blocklisted mid-round
    Given worker "0xW001" is a qualifier in round 5
    When address "0xW001" appears on a sanctions list
    Then the escrow round manager skips capture for "0xW001"
    And the uncaptured amount is voided back to the platform
    And the event is logged with the sanctions reason
```

---

## 8. ADDITIONAL MISSING SCENARIOS

### 8a. Concurrent round edge cases
```gherkin
Scenario: Two rounds cannot be active simultaneously
  Given round 5 is in progress with active escrow authorization
  When the system attempts to start round 6
  Then the start is rejected with "round 5 still active"
  And no new escrow authorization is created
```

### 8b. Gas price spike during settlement
```gherkin
Scenario: Gas price spike during capture phase
  Given round 5 has 10 workers to pay
  And 5 captures have succeeded
  When the Base gas price exceeds the configured maxGasPrice
  Then remaining captures are queued for retry
  And the escrow authorization has not expired yet
  And captures resume when gas price drops
```

### 8c. Zero-worker round
```gherkin
Scenario: Round starts but no workers respond
  Given round 5 authorized 60 USDC in escrow
  And no workers submitted precommitments
  When the round duration expires
  Then void() returns all 60 USDC to the platform
  And the round is recorded with zero qualifiers
```

### 8d. Duplicate ERC-8004 registrations (same address, multiple tokens)
```gherkin
Scenario: Worker holds multiple ERC-8004 tokens with same skill
  Given address "0xW001" owns tokens 12345 and 12346
  And both tokens have "devops_mlops/model_versioning" skill
  When the coordinator discovers workers
  Then "0xW001" appears only once (deduplicated by address)
  And the most recent registration is used
```

---

## 9. SUMMARY OF RECOMMENDATIONS

| Priority | Gap | Affected File(s) | Action |
|----------|-----|-------------------|--------|
| P0 | No tokenURI resolution testing | discovery.feature | Add 3 scenarios |
| P0 | No OASF taxonomy filtering tests | discovery.feature | Add 3 scenarios |
| P0 | No ERC-8004 identity revocation mid-round | NEW: erc8004_identity_lifecycle.feature | Create file |
| P0 | No NFT transfer during round | NEW or discovery.feature | Add 2 scenarios |
| P1 | No PaymentRequirements generation tests | NEW: x402_payment_requirements.feature | Create file |
| P1 | No leaderboard API feature | NEW: leaderboard_api.feature | Create file |
| P1 | No round-over-round state feature | NEW: round_over_round_state.feature | Create file |
| P1 | No BaseScan rate limiting scenarios | discovery.feature | Add 2 scenarios |
| P1 | No chain reorg scenarios | discovery.feature | Add 2 scenarios |
| P2 | No x402 facilitator timeout scenarios | escrow.feature or NEW | Add 2 scenarios |
| P2 | No gas spike during settlement | escrow.feature | Add 1 scenario |
| P2 | E2E scenario too long | e2e.feature | Refactor |
| P3 | Missing @erc8004 @x402 tags | All files | Add tags |
| P3 | Magic numbers without context | escrow.feature, opow.feature | Add comments |

Total: 6 existing files need enhancements, 3-4 new feature files recommended.
