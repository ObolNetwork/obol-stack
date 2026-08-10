@bdd @phase0
Feature: ERC-8004 discovery
  As a buyer or permissionless indexer
  I want service providers to publish standard identity metadata
  So that I can discover paid agents without a central registry service

  # References: SPEC Sections 4.4, 4.5, 5.7; B&E B20-B22, U6, E7

  Background:
    Given the stack is running
    And an AgentIdentity exists at "x402/default"

  @integration
  Scenario: Registration-enabled offer publishes a well-known document
    Given a ServiceOffer has registration.enabled true
    When the controller reconciles the offer
    Then a RegistrationRequest exists
    And "/.well-known/agent-registration.json" is served
    And the document includes x402Support, active status, services, and registrations when known

  @manual
  Scenario: External registration updates identity status
    Given the operator submits an ERC-8004 registration transaction
    When the controller observes the matching chain event
    Then AgentIdentity status records the chain and agentId
    And ServiceOffer status reflects the agentId

  @fast
  Scenario: Controller does not mint registration transactions
    When registration is enabled
    Then the controller publishes the document
    But the controller does not submit a chain transaction

  @fast
  Scenario: Shared registration owner propagates status
    Given two ServiceOffers share the default AgentIdentity
    And one offer owns the RegistrationRequest
    When the owner registration status changes
    Then the non-owner offer reports shared registration status

  @integration
  Scenario: Pending on-chain registration still appears in catalog
    Given an offer is operationally ready
    And condition "Registered" is "False" with reason "AwaitingExternalRegistration"
    When "/api/services.json" is rendered
    Then the offer is included
    And "registrationPending" is true
