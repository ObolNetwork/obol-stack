@e2e @slow
Feature: End-to-end autoresearch round
  A complete round from escrow authorization through worker
  experiments to reward distribution and settlement.

  Background:
    Given the autoresearch chart is deployed with default values
    And an Anvil fork of Base Sepolia is running
    And the platform wallet holds 500 USDC
    And 2 GPU workers are registered on ERC-8004:
      | address | skill                          | gpu       |
      | 0xW001  | devops_mlops/model_versioning  | NVIDIA T4 |
      | 0xW002  | devops_mlops/model_versioning  | NVIDIA A10 |
    And 1 innovator submitted algorithm "muon-opt-v2" for neuralnet_optimizer

  Scenario: Complete round with two honest workers
    # Round setup
    Given 100 USDC of x402 payments were collected in the previous round
    When a new round begins
    Then 30 USDC is authorized in escrow

    # Worker experiments
    When worker "0xW001" precommits a benchmark with 50 nonces
    And worker "0xW002" precommits a benchmark with 50 nonces
    And both workers submit Merkle roots over their results
    And the verifier samples 5 nonces from each worker
    And both workers submit valid Merkle proofs
    Then both workers are recorded as qualifiers

    # Reward calculation
    When the round duration expires
    Then the reward engine computes influence for both workers
    And both workers have balanced challenge participation
    And influence is split proportionally to qualifier count

    # Settlement
    When captures are executed
    Then worker "0xW001" receives their earned USDC via capture()
    And worker "0xW002" receives their earned USDC via capture()
    And innovator "muon-opt-v2" receives adoption-weighted USDC
    And the operator receives 10% of the pool
    And void() returns any remainder to the platform wallet
    And the leaderboard API shows both workers with correct earnings
    And the next round begins with a new authorization

  Scenario: Round where one worker submits fraudulent proofs
    Given 100 USDC of x402 payments were collected
    When a new round begins
    Then 30 USDC is authorized in escrow

    When worker "0xW001" submits valid proofs for all sampled nonces
    And worker "0xW002" submits a proof with a quality mismatch
    Then worker "0xW001" is a qualifier
    And worker "0xW002" is excluded

    When captures are executed
    Then worker "0xW001" receives the entire worker pool share
    And worker "0xW002" receives nothing
    And void() returns worker "0xW002"'s unclaimed share to the platform
