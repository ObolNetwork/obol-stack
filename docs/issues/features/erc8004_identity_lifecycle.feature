@erc8004 @identity
Feature: ERC-8004 identity lifecycle during rounds
  Workers are identified by ERC-8004 agent NFTs on Base.
  The system must handle registration, metadata updates,
  NFT transfers, and deactivation gracefully during
  active rounds.

  Background:
    Given the ERC-8004 Identity Registry is at "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
    And the OASF skill filter is "devops_mlops/model_versioning"

  Rule: Only registered agents can participate

    Scenario: Worker with valid ERC-8004 registration is discovered
      Given worker "0xW001" holds agent NFT token ID 12345
      And the NFT metadata includes skill "devops_mlops/model_versioning"
      And the registration JSON at .well-known/agent-registration.json is valid
      When the coordinator discovers workers
      Then worker "0xW001" appears in the results
      And the worker's x402 endpoint is read from the registration services list

    Scenario: Worker without ERC-8004 registration is excluded
      Given worker "0xW002" has no agent NFT
      When the coordinator discovers workers
      Then worker "0xW002" does not appear in the results

    Scenario: Worker with wrong OASF skill is filtered out
      Given worker "0xW003" holds agent NFT token ID 12346
      And the NFT metadata includes skill "communication/chat" but not "devops_mlops/model_versioning"
      When the coordinator discovers workers with skill filter
      Then worker "0xW003" does not appear in the results

  Rule: Metadata updates are reflected in discovery

    Scenario: Worker updates best_val_bpb in registration metadata
      Given worker "0xW001" registered with best_val_bpb of 3.5
      When worker "0xW001" calls URIUpdated with best_val_bpb of 3.1
      And the discovery cache TTL expires
      Then the coordinator sees worker "0xW001" with best_val_bpb 3.1

    Scenario: Worker adds a new OASF skill to their registration
      Given worker "0xW004" registered with skill "data_processing/etl"
      When worker "0xW004" updates metadata to add "devops_mlops/model_versioning"
      Then worker "0xW004" becomes discoverable by the coordinator

  Rule: Identity is snapshotted at benchmark acceptance

    # At the moment a worker's benchmark is accepted (precommit confirmed),
    # the verifier snapshots: ownerOf(tokenId), payout wallet (from
    # registration JSON), and registration metadata. All reward routing
    # for that round uses the SNAPSHOT, not live on-chain state.
    # This prevents mid-round transfers or metadata updates from
    # redirecting or nullifying rewards after work is accepted.

    Scenario: Snapshot captures payout wallet at benchmark acceptance
      Given worker "0xW001" holds agent NFT token ID 12345
      And the registration JSON lists payout wallet "0xPAY1"
      When worker "0xW001"'s precommit is accepted by the verifier
      Then the verifier snapshots owner "0xW001" and payout "0xPAY1"
      And rewards for this round are routed to "0xPAY1" regardless of later changes

    Scenario: NFT transferred mid-round does not redirect rewards
      Given worker "0xW001" is a qualifier in the current round
      And the snapshot records payout wallet "0xPAY1"
      When worker "0xW001" transfers their agent NFT to "0xNEW"
      And "0xNEW" updates the registration payout to "0xPAY_NEW"
      And the round completes
      Then the RewardDistributor sends "0xW001"'s share to "0xPAY1"
      And "0xNEW" is the registered owner for subsequent rounds

    Scenario: Worker deactivates registration mid-round
      Given worker "0xW001" is a qualifier in the current round
      And worker "0xW001" sets registration active=false
      When the round completes
      Then the distribution includes "0xW001"'s verified work from this round
      And "0xW001" is excluded from discovery in the next round

    Scenario: Metadata URI update mid-round does not affect current snapshot
      Given worker "0xW001" is a qualifier with snapshotted best_val_bpb 3.2
      When worker "0xW001" calls URIUpdated with best_val_bpb 2.8
      Then the current round still uses the snapshotted 3.2
      And the next round's discovery will reflect 2.8

    Scenario: Burned agent NFT removes worker from future rounds only
      Given worker "0xW001" holds agent NFT token ID 12345
      And worker "0xW001" is a qualifier in the current round
      When the NFT is burned (transferred to address zero)
      Then the current round's rewards are still distributed per snapshot
      And worker "0xW001" is removed from all discovery backends
      And "0xW001" cannot participate in subsequent rounds

  Rule: Registration JSON schema is validated

    Scenario: Malformed registration JSON is rejected
      Given worker "0xW005" has a tokenURI pointing to invalid JSON
      When the discovery client fetches the registration
      Then worker "0xW005" is skipped with a schema validation warning
      And discovery continues with remaining workers

    Scenario: Registration JSON with missing x402 endpoint is skipped
      Given worker "0xW006" has valid registration JSON
      But the services list contains no x402-compatible endpoint
      When the coordinator discovers workers
      Then worker "0xW006" is excluded
      And a warning is logged about missing x402 endpoint
