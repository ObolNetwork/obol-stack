@leaderboard @api
Feature: Leaderboard API
  The reward engine exposes a REST API showing per-round
  rankings, cumulative earnings, and worker performance
  history.

  Background:
    Given the reward engine is running
    And 3 completed rounds exist in the history

  Rule: Current round leaderboard reflects live state

    Scenario: Leaderboard shows workers ranked by influence
      Given round 4 is in progress
      And worker "0xAAA" has influence 0.45
      And worker "0xBBB" has influence 0.35
      And worker "0xCCC" has influence 0.20
      When GET /leaderboard is called
      Then the response contains 3 workers in descending influence order
      And each entry includes worker address, influence, and estimated reward

    Scenario: Leaderboard includes innovator rankings
      Given algorithm "muon-v3" has 60% adoption
      And algorithm "adamw-base" has 40% adoption
      When GET /leaderboard?role=innovator is called
      Then the response shows innovators ranked by adoption percentage

  Rule: Historical round data is queryable

    Scenario: Completed round data includes settlement details
      When GET /round/3 is called
      Then the response includes:
        | field              | description                        |
        | round_id           | 3                                  |
        | pool_amount        | total USDC in the reward pool      |
        | worker_rewards     | per-worker capture amounts         |
        | innovator_rewards  | per-innovator adoption earnings    |
        | operator_reward    | operator share                     |
        | escrow_tx_hash     | authorize() transaction hash       |
        | capture_tx_hashes  | list of capture() transaction hashes|
        | void_tx_hash       | void() transaction hash            |
        | round_start        | ISO 8601 timestamp                 |
        | round_end          | ISO 8601 timestamp                 |

    Scenario: Round history respects retention limit
      Given the retention is set to 100 rounds
      And 150 rounds have completed
      When GET /round/10 is called
      Then a 404 is returned
      When GET /round/51 is called
      Then the round data is returned

  Rule: Cumulative earnings are tracked per participant

    Scenario: Worker cumulative earnings span multiple rounds
      Given worker "0xAAA" earned 42 USDC in round 1
      And worker "0xAAA" earned 35 USDC in round 2
      And worker "0xAAA" earned 50 USDC in round 3
      When GET /leaderboard?cumulative=true is called
      Then worker "0xAAA" shows total earnings of 127 USDC

    Scenario: Leaderboard is empty before first round completes
      Given no rounds have completed yet
      When GET /leaderboard is called
      Then the response contains an empty workers list
      And the response includes round_in_progress=true
