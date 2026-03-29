@bdd
Feature: OpenClaw runtime and agent capabilities
  As an agent developer
  I want a canonical elevated OpenClaw runtime plus separately managed instances
  So that automation and custom agents share the same safe deployment model

  # References: SPEC Section 3.4 (OpenClaw Runtime and Agent Capabilities), B&E Section 2.4 (OpenClaw Runtime)

  Background:
    Given the stack has completed baseline startup

  @phase1 @fast
  Scenario: The default elevated runtime is prepared automatically
    Given the operator has not created any extra OpenClaw instances
    When the stack deploys its defaults
    Then the canonical elevated OpenClaw runtime is prepared for obol-agent workflows
    And the runtime receives the elevated capabilities required by shipped skills

  @phase1 @fast
  Scenario: Additional instances remain operator-managed deployments
    Given the operator has created one or more named OpenClaw instances
    When the operator syncs or deletes an instance
    Then the action targets the named deployment the operator selected
    And other instances remain unchanged

  @phase1
  Scenario Outline: Operator surfaces resolve to the correct OpenClaw instance
    Given the operator targets the instance <instance_id>
    When the operator uses the <surface> command
    Then the command returns data for <instance_id>

    Examples:
      | instance_id | surface   |
      | obol-agent  | token     |
      | obol-agent  | dashboard |
      | my-agent    | token     |
