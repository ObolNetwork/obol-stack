@bdd
Feature: Buy-side remote inference
  As a remote buyer
  I want paid remote models to resolve through a bounded-risk payment sidecar
  So that I can purchase inference without receiving direct access to signing authority

  # References: SPEC Section 3.6 (Buy-Side Remote Inference), B&E Section 2.6 (Buy-Side Payments)

  Background:
    Given the cluster-wide LiteLLM gateway exposes a static paid model namespace

  @phase1 @fast
  Scenario: Paid model routing uses the static paid namespace
    Given a remote model has been configured for paid access
    When a buyer requests that model through LiteLLM
    Then the request resolves through the static paid namespace
    And payment handling is delegated to the buyer sidecar

  @phase1 @fast
  Scenario: Spending is bounded by the pre-signed auth pool
    Given the buyer sidecar has a finite pool of pre-signed authorizations
    When the sidecar forwards paid requests
    Then it uses only the available authorizations in that pool
    And it fails explicitly instead of escalating to live signing authority

  @phase1
  Scenario Outline: Unmapped paid models fail explicitly
    Given the buyer requests <model_name>
    When no remote payment mapping exists for that model
    Then the request fails with an explicit unmapped-model error

    Examples:
      | model_name         |
      | paid/unknown-model |
      | paid/missing-offer |
