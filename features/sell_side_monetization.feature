@bdd @phase0
Feature: Sell-side monetization
  As a service provider
  I want to publish x402-gated services from ServiceOffers
  So that buyers can pay per request without a centralized marketplace

  # References: SPEC Sections 4.1, 5.4, 5.5; B&E B9-B14, B25, E3, E5

  Background:
    Given the stack is running
    And x402-verifier and serviceoffer-controller are installed

  @integration
  Scenario: HTTP ServiceOffer converges to a paid route
    Given an upstream Service responds healthy
    When the seller creates a ServiceOffer for that upstream
    Then conditions "ModelReady", "UpstreamHealthy", "PaymentGateReady", and "RoutePublished" become "True"
    And the HTTPRoute points at the shared x402 gateway

  @integration
  Scenario: Agent ServiceOffer advertises runtime metadata
    Given Agent "quant" is Ready with model "qwen" and skill "buy-x402"
    When the seller creates a ServiceOffer of type "agent"
    Then status.agentResolution contains model "qwen"
    And an unpaid request returns HTTP 402
    And the 402 accepts extra contains agentModel, agentSkills, and agentRuntime

  @fast
  Scenario: Unhealthy upstream blocks route publication
    Given a ServiceOffer points to an upstream that returns HTTP 500
    When the controller reconciles the offer
    Then condition "UpstreamHealthy" is "False"
    And condition "RoutePublished" is not "True"

  @fast
  Scenario: Direct verifier access fails closed
    When a request calls x402-verifier "/verify" without "X-Forwarded-Uri"
    Then the verifier returns HTTP 403

  @integration
  Scenario: Paused offer leaves the public catalog
    Given a ServiceOffer is operationally ready
    When annotation "obol.org/paused" is set to "true"
    Then route children are removed or disabled
    And the offer is absent from "/api/services.json"
