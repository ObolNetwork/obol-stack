@verification @critical
Feature: Commit-reveal work verification
  Workers commit to results via a Merkle root before learning
  which nonces will be sampled. This prevents retroactive
  fabrication of results.

  Background:
    Given the verifier is running
    And the neuralnet_optimizer challenge is active
    And the sample count is 5 nonces per benchmark

  Rule: Honest workers pass verification

    Scenario: Worker with valid proofs becomes a qualifier
      Given worker "0xAAA" precommits a benchmark with 100 nonces
      And the verifier assigns a random hash and track
      When worker "0xAAA" submits a Merkle root over 100 results
      And the verifier samples 5 nonces for verification
      And worker "0xAAA" submits valid Merkle proofs for all 5
      Then worker "0xAAA" is recorded as a qualifier
      And the benchmark quality scores are accepted

    Scenario: Re-execution confirms claimed quality
      Given worker "0xAAA" claims val_bpb of 3.2 for nonce 42
      When the verifier re-executes nonce 42 with the same settings
      Then the re-executed val_bpb matches the claimed 3.2
      And the proof is accepted

  Rule: Dishonest workers fail verification

    Scenario: Invalid Merkle proof is rejected
      Given worker "0xCCC" submitted a Merkle root
      And the verifier sampled nonces [7, 23, 45, 61, 89]
      When worker "0xCCC" submits a proof for nonce 23 that does not match the root
      Then the verification fails for worker "0xCCC"
      And worker "0xCCC" is excluded from qualifiers for this round
      And no escrow capture is made for worker "0xCCC"

    Scenario: Worker who inflates quality scores is caught
      Given worker "0xCCC" claims val_bpb of 2.8 for nonce 42
      When the verifier re-executes nonce 42 with the same settings
      And the re-executed val_bpb is 3.5
      Then the quality mismatch is detected
      And the verification fails for worker "0xCCC"

    Scenario: Worker who times out on proof submission is excluded
      Given worker "0xCCC" submitted a Merkle root
      And the verifier sampled 5 nonces
      When worker "0xCCC" does not submit proofs within 300 seconds
      Then worker "0xCCC" is excluded from qualifiers
      And the round proceeds without them

  Rule: Sampling is fair and deterministic

    Scenario: Nonce sampling is deterministic from the round seed
      Given the same benchmark settings and random hash
      When nonces are sampled twice
      Then the same 5 nonces are selected both times

    Scenario: Worker cannot predict which nonces will be sampled
      Given the random hash is derived from a future block hash
      When the worker commits their Merkle root
      Then the sampled nonces have not yet been determined
