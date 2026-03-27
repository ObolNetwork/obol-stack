@rounds @state
Feature: Round-over-round state continuity
  The reward pool, uncaptured funds, and participant state
  carry over correctly between rounds. No funds are lost
  or double-counted during transitions.

  Background:
    Given the autoresearch chart is deployed
    And the reward pool percentage is 30%

  Rule: Uncaptured funds roll into the next round

    Scenario: Voided funds increase the next round's pool
      Given round 1 had 100 USDC in the pool
      And 70 USDC was captured to workers
      And void() returned 30 USDC to the platform wallet
      And 50 USDC of new x402 payments arrived during round 1
      When round 2 begins
      Then the pool for round 2 is 15 USDC from new payments plus the 30 USDC rollover
      And authorize() locks 45 USDC in escrow

    Scenario: Unadopted innovator share rolls into next round
      Given round 1 had 20 USDC in the innovator pool
      And algorithm "untested-v1" had 0% adoption
      And 5 USDC of the innovator pool was unadopted
      When round 2 begins
      Then the unadopted 5 USDC is added to round 2's innovator pool

  Rule: Round transitions are atomic

    Scenario: No gap between rounds allows work to go unrecorded
      Given round 1 is ending
      And worker "0xAAA" submits a proof at the round boundary
      When the round transitions
      Then the proof is attributed to round 1 if submitted before the cutoff
      Or attributed to round 2 if submitted after the cutoff
      And the proof is never lost or double-counted

    Scenario: Authorize for new round happens after void of previous round
      Given round 1 is completing
      When captures and void are executed for round 1
      Then authorize() for round 2 is called only after void() confirms
      And there is no period where two rounds have active escrow authorizations

  Rule: Worker state resets each round

    Scenario: Worker's influence is recalculated fresh each round
      Given worker "0xAAA" had 80% influence in round 1
      And worker "0xBBB" joins in round 2 with equal qualifier count
      When round 2 influence is calculated
      Then round 1 influence values have no effect
      And both workers compete on round 2 qualifiers only

    Scenario: Worker who was excluded in round N can rejoin in round N+1
      Given worker "0xCCC" failed verification in round 3
      And worker "0xCCC" received no capture in round 3
      When round 4 begins
      Then worker "0xCCC" is eligible to submit benchmarks
      And their round 3 failure does not affect round 4 influence

  Rule: Platform wallet balance is tracked across rounds

    Scenario: Cumulative earnings are auditable from on-chain events
      Given 5 rounds have completed
      When the audit script reads all authorize/capture/void/reclaim events
      Then the sum of all captures equals total worker + innovator + operator payouts
      And the sum of all voids equals total uncaptured rollover
      And the platform wallet balance matches expected remainder
