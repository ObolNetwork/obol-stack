@bdd @phase0
Feature: Tunnel and public storefront
  As a service provider
  I want paid services exposed through a constrained public tunnel
  So that buyers can discover and access only the intended public surfaces

  # References: SPEC Sections 3.2, 5.8, 6; B&E B23-B24, U1, E6

  Background:
    Given the stack is running

  @integration
  Scenario: Tunnel startup propagates public URL
    When the operator starts or restarts the tunnel
    Then the active tunnel URL is recorded
    And "obol-stack-config" contains the tunnel URL
    And public catalog surfaces use the new base URL

  @integration
  Scenario: Storefront is created for the tunnel hostname
    Given the tunnel has a public HTTPS URL
    When CreateStorefront runs
    Then a hostname-pinned HTTPRoute exists in namespace "traefik"
    And the storefront reads services from "obol-skill-md.x402.svc"

  @fast
  Scenario: Internal routes remain private
    When public tunnel routes are rendered
    Then frontend, eRPC, LiteLLM, and monitoring routes are not exposed without hostname restriction

  @manual
  Scenario: Quick tunnel URL change warns operator
    Given the stack uses a quick tunnel URL
    When a destructive action would stop the cluster or tunnel
    Then the operator is warned that registered URLs may break
