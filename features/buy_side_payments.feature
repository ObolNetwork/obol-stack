@bdd @phase0
Feature: Buy-side x402 payments
  As a buyer agent
  I want to buy remote paid inference through bounded auth pools
  So that I can call paid models without exposing my signer at runtime

  # References: SPEC Sections 4.3, 5.6; B&E B15-B19, B26, E8-E9

  Background:
    Given the stack is running
    And the default Hermes agent has the buy-x402 skill

  @integration
  Scenario: Buy command creates a ready PurchaseRequest
    Given a seller endpoint returns HTTP 402 with matching token pricing
    When the user runs "obol buy inference remote --seller URL --model qwen --budget 1"
    Then buy.py creates a PurchaseRequest with pre-signed auths
    And the controller writes buyer config and auth ConfigMaps
    And LiteLLM exposes "paid/qwen"

  @integration
  Scenario: Paid model request spends one auth
    Given PurchaseRequest "remote" is Ready with remaining auths
    When Hermes calls LiteLLM with model "paid/qwen"
    Then x402-buyer attaches an X-PAYMENT header
    And the seller response status and body are propagated
    And sidecar status eventually reports one fewer remaining auth

  @fast
  Scenario: Non-payment-gated endpoint is rejected
    Given a PurchaseRequest endpoint returns HTTP 200 to the pricing probe
    When the controller reconciles the PurchaseRequest
    Then condition "Probed" is "False" with reason "NotPaymentGated"

  @fast
  Scenario: Duplicate public model is rejected
    Given PurchaseRequest "a" owns model "qwen"
    When PurchaseRequest "b" also requests model "qwen"
    Then PurchaseRequest "b" has condition "Configured" "False" with reason "DuplicateModel"

  @fast
  Scenario: Token mismatch fails before signing
    Given seller pricing requires OBOL
    When the user buys with token "USDC"
    Then the host preflight fails with an asset mismatch
    And no new PurchaseRequest is created
