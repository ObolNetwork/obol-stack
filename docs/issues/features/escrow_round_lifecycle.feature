@escrow @critical
Feature: Escrow round lifecycle
  The escrow round manager locks USDC in the Commerce Payments
  AuthCaptureEscrow contract at the start of each round and
  distributes earnings to verified workers at round end.

  Background:
    Given the autoresearch chart is deployed on a k3s cluster
    And an Anvil fork of Base Sepolia is running
    And the platform wallet holds 1000 USDC
    And the AuthCaptureEscrow contract is at "0xBdEA0D1bcC5966192B070Fdf62aB4EF5b4420cff"
    And the reward pool percentage is 30%

  Rule: Funds must be locked before any work begins

    Scenario: Round starts with successful escrow authorization
      Given 200 USDC of x402 payments were collected in the previous round
      When a new round begins
      Then the escrow round manager calls authorize() for 60 USDC
      And the AuthCaptureEscrow capturableAmount equals 60 USDC
      And the authorizationExpiry is set to round end plus 1 hour grace
      And workers can verify the commitment on-chain

    Scenario: Round start fails when platform wallet has insufficient USDC
      Given the platform wallet holds 0 USDC
      When a new round begins
      Then the escrow round manager logs an authorization failure
      And no work is accepted for this round
      And the previous round's uncaptured funds are not affected

  Rule: Workers are paid proportionally to verified influence

    # NOTE: The AuthCaptureEscrow receiver is FIXED per PaymentInfo.
    # All captures from one authorize() go to the SAME receiver address.
    # We use a RewardDistributor contract as the single receiver,
    # which then splits USDC to individual workers via ERC20 transfers.
    #
    # Flow: authorize(receiver=RewardDistributor) → capture(full worker pool)
    #       → RewardDistributor.distribute(workers[], amounts[])

    Scenario: Two verified workers receive proportional rewards
      Given a round with 100 USDC authorized in escrow
      And the escrow receiver is the RewardDistributor contract
      And worker "0xAAA" has 60% influence
      And worker "0xBBB" has 40% influence
      And both workers passed commit-reveal verification
      When the round completes
      Then capture() is called once for 70 USDC to the RewardDistributor
      And the RewardDistributor transfers 42 USDC to "0xAAA"
      And the RewardDistributor transfers 28 USDC to "0xBBB"
      And the platform fee receiver gets 2% of the capture
      And void() is called for the remaining 30 USDC
      And the remaining USDC returns to the platform wallet

    Scenario: Unverified worker receives no distribution
      Given a round with 100 USDC authorized in escrow
      And worker "0xAAA" passed verification with 100% influence
      And worker "0xCCC" failed commit-reveal verification
      When the round completes
      Then capture() sends the worker pool to the RewardDistributor
      And the RewardDistributor transfers funds only to "0xAAA"
      And worker "0xCCC" receives nothing
      And void() returns the uncaptured remainder to the platform wallet

    Scenario: Round with no verified workers voids entirely
      Given a round with 100 USDC authorized in escrow
      And no workers submitted valid proofs
      When the round completes
      Then no capture() is called
      And void() returns the full 100 USDC to the platform wallet

  Rule: Funds are always recoverable

    Scenario: Platform reclaims funds after manager crash
      Given a round with 100 USDC authorized in escrow
      And the escrow round manager process has crashed
      When the authorizationExpiry passes
      Then the platform wallet calls reclaim() directly
      And the full 100 USDC returns to the platform wallet
      And no operator signature is required

    Scenario: Operator refunds a worker after post-capture fraud discovery
      Given worker "0xAAA" received a 42 USDC capture in round 5
      And fraud is discovered within the refund window
      When the operator calls refund() for 42 USDC
      Then 42 USDC returns to the platform wallet
      And the refund is recorded in the round history
