@bdd @phase0
Feature: Release smoke
  As a maintainer
  I want release flows to validate the real seller and buyer journeys
  So that changes do not regress agent commerce primitives

  # References: SPEC Section 9; B&E G6

  Background:
    Given the repository is on a release candidate branch

  @fast
  Scenario: Static validation passes
    When maintainers run shell syntax checks and Go unit tests
    Then "bash -n flows/*.sh" succeeds
    And focused Go tests for CLI, controller, x402, buyer, ERC-8004, agent, and tunnel pass

  @integration
  Scenario: USDC dual-stack flow passes
    When flow-11 runs
    Then Alice sells a paid service
    And Bob discovers and buys it
    And payment settlement and balance deltas are asserted

  @integration
  Scenario: Live OBOL flow passes when enabled
    Given live OBOL smoke env vars and funded wallets are available
    When flow-14 or flow-15 runs
    Then Bob pays Alice with OBOL on Base Sepolia
    And the settlement transfer is verified

  @integration
  Scenario: Agent ServiceOffer smoke passes
    When flow-16 runs
    Then an Agent CRD is declared
    And the ServiceOffer type is "agent"
    And the 402 metadata includes agent runtime fields
