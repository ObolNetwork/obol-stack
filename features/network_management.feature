@bdd
Feature: Network management and eRPC
  As a local operator
  I want local chain deployments and remote RPC aliases to remain distinct
  So that network support claims and routing behavior stay accurate

  # References: SPEC Section 3.3 (Network Management and eRPC), B&E Section 2.3 (Network Management)

  Background:
    Given the operator has a running stack with eRPC available

  @phase1 @fast
  Scenario: Installable networks come only from embedded bundles
    Given the operator wants to deploy a local network
    When the operator lists installable networks
    Then only embedded deployable network bundles are shown
    And remote RPC aliases are not presented as local deployments

  @phase1 @fast
  Scenario: Remote RPC aliases default to read-only forwarding
    Given the operator adds a remote chain without allow-writes
    When requests are routed through eRPC for that chain
    Then write methods remain blocked by default
    And read-only RPC methods continue to work

  @phase1
  Scenario Outline: Network status matches current command semantics
    Given the operator has <deployment_state>
    When the operator runs network status
    Then the command reports <status_surface>

    Examples:
      | deployment_state                     | status_surface                       |
      | local and remote networks configured | global eRPC health and upstreams     |
      | no named local deployment selected   | the current gateway summary contract |
