@bdd
Feature: Frontend and monitoring surfaces
  As a local operator
  I want observability and browser surfaces that match the platform's local-first posture
  So that I can inspect the stack without accidentally publishing operator-only interfaces

  # References: SPEC Section 3.7 (Tunnel, Discovery, Frontend, and Monitoring), B&E Section 2.7 (Tunnel, Discovery, Frontend, and Monitoring)

  Background:
    Given the stack has deployed its default frontend and monitoring components

  @phase1 @fast
  Scenario: Frontend stays on the local hostname by default
    Given the operator opens the stack frontend
    When no explicit architecture change has been made for public exposure
    Then the frontend is served through the local hostname contract
    And the public tunnel does not expose that interface

  @phase1 @fast
  Scenario: Monitoring remains an operator-only surface
    Given Prometheus-backed monitoring is installed
    When buyers access public monetized services
    Then monitoring data remains separate from buyer-facing endpoints
    And operator diagnostics stay inside the local control plane

  @phase1
  Scenario Outline: Status surfaces expose operational data through the intended channel
    Given the operator inspects <surface>
    When the platform reports health or runtime state
    Then the operator receives <operational_view>

    Examples:
      | surface      | operational_view             |
      | sell status  | pricing and reconciliation   |
      | model status | provider and route readiness |
      | tunnel       | current tunnel activation    |
