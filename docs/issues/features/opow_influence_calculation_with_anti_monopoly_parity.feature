@opow @critical
Feature: OPOW influence calculation with anti-monopoly parity
  The reward engine computes per-worker influence using a parity
  formula that penalizes concentration on a single challenge.
  Workers must diversify across all active challenges to maximize
  their earnings.

  Background:
    Given the imbalance multiplier is 3.0

  Rule: Diversified workers earn more than concentrated workers

    Scenario: Equally diversified worker has zero penalty
      Given 4 active challenges
      And worker "0xAAA" has qualifier fractions:
        | challenge | fraction |
        | c001      |     0.25 |
        | c002      |     0.25 |
        | c003      |     0.25 |
        | c004      |     0.25 |
      When influence is calculated
      Then worker "0xAAA" imbalance is 0.0
      And worker "0xAAA" penalty factor is 1.0

    Scenario: Fully concentrated worker is severely penalized
      Given 4 active challenges
      And worker "0xBBB" has qualifier fractions:
        | challenge | fraction |
        | c001      |     1.00 |
        | c002      |     0.00 |
        | c003      |     0.00 |
        | c004      |     0.00 |
      When influence is calculated
      Then worker "0xBBB" imbalance is 1.0
      And the imbalance-multiplier product is 3.0
      And worker "0xBBB" penalty factor is less than 0.05

    Scenario: Concentrated worker earns less despite equal total output
      Given 2 active challenges and a worker pool of 100 USDC
      And worker "0xAAA" submitted 50 proofs to c001 and 50 to c002
      And worker "0xBBB" submitted 100 proofs to c001 and 0 to c002
      When influence is calculated and rewards are distributed
      Then worker "0xAAA" earns more than worker "0xBBB"
      And the ratio of earnings exceeds 5:1

    Scenario Outline: Parity penalty scales with concentration
      Given 2 active challenges
      And a worker has qualifier fractions <f1> and <f2>
      When influence is calculated
      Then the penalty factor is approximately <penalty>

      Examples:
        | f1   | f2   | penalty |
        | 0.50 | 0.50 |    1.00 |
        | 0.70 | 0.30 |    0.62 |
        | 0.90 | 0.10 |    0.15 |
        | 1.00 | 0.00 |    0.05 |

  Rule: Influence values are normalized across all workers

    Scenario: Total influence sums to 1.0
      Given 3 workers with varying qualifier fractions
      When influence is calculated for all workers
      Then the sum of all influence values equals 1.0

    Scenario: Single worker in a round gets full influence
      Given 1 worker who participated in all active challenges
      When influence is calculated
      Then that worker's influence is 1.0
      And they receive the entire worker pool

  Rule: Single-challenge rounds disable the parity penalty

    Scenario: With only one active challenge all workers get zero imbalance
      Given 1 active challenge
      And worker "0xAAA" has qualifier fraction 0.8 in c001
      And worker "0xBBB" has qualifier fraction 0.2 in c001
      When influence is calculated
      Then worker "0xAAA" imbalance is 0.0
      And worker "0xBBB" imbalance is 0.0
      And influence is proportional to qualifier count only

  Rule: New challenges phase in gradually

    Scenario: Newly added challenge does not immediately penalize existing workers
      Given 2 active challenges c001 and c002
      And challenge c003 is added with a phase-in period of 100 blocks
      And worker "0xAAA" has proofs in c001 and c002 but not c003
      When influence is calculated at block 10 of the phase-in
      Then worker "0xAAA" receives a blended penalty
      And the c003 weight is 10% of its final weight
      And the penalty is less severe than after full phase-in
