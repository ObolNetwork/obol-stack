@bdd @phase0
Feature: Stack lifecycle and model routing
  As an operator
  I want the local stack and model gateway to converge from CLI commands
  So that agents can use stable local infrastructure

  # References: SPEC Sections 5.1, 5.2; B&E B1-B4

  Background:
    Given a host with Obol Stack prerequisites installed

  @fast
  Scenario: Initialize a clean stack
    When the operator runs "obol stack init"
    Then the stack ID and backend choice are persisted
    And embedded infrastructure defaults are copied

  @integration
  Scenario: Start an initialized stack
    Given the stack has been initialized
    When the operator runs "obol stack up"
    Then the selected Kubernetes backend is running
    And kubeconfig is written
    And base namespaces and CRDs are installed
    And host DNS is configured best-effort

  @fast
  Scenario: Configure and prefer a model
    Given LiteLLM is reachable
    When the operator configures a provider model
    And the operator prefers that model
    Then the LiteLLM config lists the model before lower-ranked entries
    And Hermes sync uses the configured inventory

  @fast
  Scenario: Concrete paid model can be selected
    Given LiteLLM contains "paid/*" and "paid/remote-qwen"
    When the default agent model is selected
    Then "paid/*" is skipped
    And "paid/remote-qwen" is eligible
