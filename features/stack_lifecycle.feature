@bdd
Feature: Stack lifecycle
  As a local operator
  I want to initialize, start, stop, and purge the stack safely
  So that I can control the local platform without losing important state unexpectedly

  # References: SPEC Section 3.1 (Stack Lifecycle), B&E Section 2.1 (Stack Lifecycle)

  Background:
    Given the operator is using the obol CLI against a local workspace

  @phase1 @fast
  Scenario: Initialize and start a new stack
    Given no stack config exists yet
    When the operator runs stack init and then stack up
    Then the CLI persists a stable stack identity and backend choice
    And baseline infrastructure is deployed before any optional public exposure

  @phase1 @fast
  Scenario: Purge without force preserves persistent data
    Given a stack has existing config and persistent data
    When the operator runs stack purge without force
    Then the cluster state and config are removed
    And persistent data remains available for later recovery

  @phase1
  Scenario Outline: Startup tolerates missing optional provider dependencies
    Given the host <provider_state>
    When the operator runs stack up
    Then the stack reaches a usable baseline
    And provider setup can be completed <recovery_path>

    Examples:
      | provider_state                          | recovery_path                |
      | has discoverable local models           | automatically during startup |
      | lacks local models or cloud credentials | later through model setup    |
