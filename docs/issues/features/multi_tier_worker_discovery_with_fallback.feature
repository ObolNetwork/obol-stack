@discovery
Feature: Multi-tier worker discovery with fallback
  The coordinator discovers GPU workers through a prioritized
  chain of discovery backends. If the preferred backend is
  unavailable, it falls back to the next tier automatically.

  Background:
    Given the OASF skill filter is "devops_mlops/model_versioning"

  Rule: Discovery uses the highest-priority available backend

    Scenario: Coordinator uses Reth indexer when available
      Given the reth-erc8004-indexer is deployed in the cluster
      And the indexer has synced past the latest registration
      When the coordinator discovers workers
      Then the query goes to the Reth indexer API
      And workers with the model_versioning skill are returned

    Scenario: Coordinator falls back to BaseScan when indexer is down
      Given the reth-erc8004-indexer is not deployed
      And a BaseScan API key is configured
      When the coordinator discovers workers
      Then the query goes to the BaseScan API
      And ERC-8004 NFT metadata is read for each agent
      And workers with the model_versioning skill are returned

    Scenario: Coordinator falls back to 8004scan as last resort
      Given the reth-erc8004-indexer is not deployed
      And no BaseScan API key is configured
      When the coordinator discovers workers
      Then the query goes to 8004scan.io
      And workers with the model_versioning skill are returned

    Scenario: All backends unavailable produces a clear error
      Given no discovery backends are reachable
      When the coordinator discovers workers
      Then a "no discovery backend available" error is returned
      And the round proceeds with zero workers

  Rule: Discovery results are cached to reduce API calls

    Scenario: Repeated queries within TTL use cached results
      Given the cache TTL is 300 seconds
      And a discovery query succeeded 60 seconds ago
      When the coordinator discovers workers again
      Then no external API call is made
      And the cached results are returned
