@bdd @phase0
Feature: Agent CRD runtime
  As a service provider
  I want durable Hermes child agents declared as CRDs
  So that I can sell or manage isolated agent services

  # References: SPEC Sections 4.2, 5.3; B&E B5-B8, E1-E2

  Background:
    Given the stack is running
    And the Agent CRD is installed

  @integration
  Scenario: Create a CRD-backed Hermes agent
    When the operator runs "obol agent new quant --model qwen --skills buy-x402 --objective task --create-wallet"
    Then namespace "agent-quant" exists
    And host seed files exist under the agent Hermes home
    And the Agent resource exists in namespace "agent-quant"
    And the controller provisions Hermes runtime resources

  @fast
  Scenario: Agent without a model does not become ready
    Given an Agent has no spec.model and no status.pinnedModel
    When the controller reconciles the Agent
    Then condition "Provisioned" is "False" with reason "ModelUnpinned"
    And condition "Ready" is "False"

  @integration
  Scenario: Agent wallet creation is stable
    Given an Agent has spec.wallet.create true
    When the controller reconciles the Agent twice
    Then one remote-signer keystore Secret exists
    And status.walletAddress remains the same

  @fast
  Scenario: Deleting an Agent waits for cleanup
    Given an Agent has controller-managed child resources
    When the Agent is deleted
    Then the finalizer remains until child resources are removed
    And the finalizer is removed after cleanup
