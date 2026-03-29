@rewards
Feature: Reward pool distribution across roles
  The reward engine splits the pool among innovators, workers,
  and operators according to configured percentages. Worker
  distribution is influence-weighted. Innovator distribution
  is adoption-weighted.

  Background:
    Given the pool split is 20% innovators, 70% workers, 10% operators
    And a round with 100 USDC in the reward pool

  Rule: Pool splits match configured percentages

    Scenario: Standard round distributes to all three roles
      When the round completes with verified workers
      Then 20 USDC is allocated to innovators
      And 70 USDC is allocated to workers
      And 10 USDC is allocated to operators

  Rule: Workers earn by influence

    Scenario: Workers are paid proportionally to influence
      Given the worker pool is 70 USDC
      And worker "0xAAA" has influence 0.6
      And worker "0xBBB" has influence 0.4
      When worker rewards are distributed
      Then worker "0xAAA" earns 42 USDC
      And worker "0xBBB" earns 28 USDC

  Rule: Innovators earn by adoption

    Scenario: Algorithm author earns when workers adopt their code
      Given the innovator pool is 20 USDC for the neuralnet_optimizer challenge
      And algorithm "fast-muon-v3" by innovator "0xINN1" has 75% adoption
      And algorithm "baseline-adamw" by innovator "0xINN2" has 25% adoption
      When innovator rewards are distributed
      Then innovator "0xINN1" earns 15 USDC
      And innovator "0xINN2" earns 5 USDC

    Scenario: Unadopted algorithm earns nothing
      Given the innovator pool is 20 USDC
      And algorithm "untested-v1" has 0% adoption
      When innovator rewards are distributed
      Then the author of "untested-v1" earns 0 USDC
      And the unadopted share rolls into the next round

  Rule: Gamma scaling adjusts for challenge count

    Scenario Outline: Reward scales with number of active challenges
      Given gamma parameters a=1.0, b=0.5, c=0.3
      And <n> challenges are active
      When the gamma value is calculated
      Then the scaling factor is approximately <gamma>

      Examples:
        | n | gamma |
        | 1 |  0.63 |
        | 3 |  0.80 |
        | 7 |  0.94 |
