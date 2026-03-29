@bdd
Feature: Tunnel, discovery, and public exposure
  As a local operator
  I want public routes to be optional and narrowly scoped
  So that local control surfaces remain private while discoverable services can still be published

  # References: SPEC Section 3.7 (Tunnel, Discovery, Frontend, and Monitoring), B&E Section 2.7 (Tunnel, Discovery, Frontend, and Monitoring)

  Background:
    Given the stack can run with or without a public tunnel

  @phase1 @fast
  Scenario: Quick tunnels are activated on demand
    Given the operator has not provisioned a persistent DNS tunnel
    When the stack starts
    Then the quick tunnel remains dormant until a public route needs it
    And local-only operation remains available immediately

  @phase1 @fast
  Scenario: Discovery metadata follows the active tunnel URL
    Given a public service has discovery metadata
    When the active tunnel URL changes
    Then discovery metadata is refreshed to reflect the current public address
    And stale public URLs are not treated as canonical

  @phase1
  Scenario Outline: Operator surfaces remain local-only unless the architecture changes deliberately
    Given the operator inspects the <surface>
    When the platform computes public exposure rules
    Then <surface> remains local-only

    Examples:
      | surface    |
      | frontend   |
      | eRPC       |
      | monitoring |
