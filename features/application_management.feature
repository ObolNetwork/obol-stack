@bdd
Feature: Application management
  As a local operator
  I want named managed applications that can be installed, synced, listed, and deleted
  So that supporting workloads follow the same lifecycle discipline as the rest of the stack

  # References: SPEC Section 3.8 (Application Management and Supporting Operations), B&E Section 2.8 (Managed Applications and Supporting Operations)

  Background:
    Given the operator has a running stack and access to supported application sources

  @phase1 @fast
  Scenario: Installing an application creates a named managed deployment
    Given the operator selects a supported application source
    When the operator installs an application with a name
    Then the platform records that name as the persistent application identity
    And later sync and delete operations target that same managed deployment

  @phase1 @fast
  Scenario: Deleting an application removes only the selected deployment
    Given multiple managed applications exist
    When the operator deletes one named application
    Then only that application's deployment artifacts are removed
    And unrelated applications remain intact

  @phase1
  Scenario Outline: Sync applies the current desired source state to a named application
    Given the operator has an installed application from <source_kind>
    When the operator runs app sync for that application
    Then the deployment is reconciled against <source_kind>

    Examples:
      | source_kind |
      | helm chart  |
      | OCI chart   |
      | local path  |
