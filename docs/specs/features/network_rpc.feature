# References:
#   SPEC.md Section 3.3 — Network / RPC Gateway
#   SPEC.md Section 3.3.3 — Two-Stage Templating
#   SPEC.md Section 3.3.4 — Write Method Blocking
#   SPEC.md Section 6.1 — External Services (ChainList API)

Feature: Network RPC Gateway
  As an operator
  I want to manage blockchain RPC routing through the eRPC gateway
  So that my cluster can interact with multiple blockchain networks reliably

  Background:
    Given the cluster is running
    And the eRPC Deployment exists in the "erpc" namespace
    And the "erpc-config" ConfigMap exists in the "erpc" namespace

  # -------------------------------------------------------------------
  # Add public RPCs from ChainList
  # -------------------------------------------------------------------

  Scenario: Add public RPCs from ChainList by chain ID
    When I run "obol network add 1"
    Then the eRPC config is patched with chain ID 1 (Ethereum Mainnet)
    And public RPC endpoints from ChainList are added as upstreams
    And each upstream has an ID prefixed with "chainlist-"
    And a network entry with "evm.chainId: 1" is added to the project

  Scenario: Add multiple chains
    When I run "obol network add 1"
    And I run "obol network add 137"
    Then the eRPC config contains upstreams for both chain ID 1 and chain ID 137
    And network entries exist for both chains

  Scenario: Adding the same chain twice is idempotent
    Given chain ID 1 is already configured in eRPC
    When I run "obol network add 1"
    Then the eRPC config is not duplicated for chain ID 1
    And existing upstreams are preserved

  # -------------------------------------------------------------------
  # Add custom RPC endpoint
  # -------------------------------------------------------------------

  Scenario: Add custom RPC endpoint for a chain
    When I run "obol network add 1 --endpoint https://my-node.example.com/rpc"
    Then the eRPC config contains a custom upstream with the provided endpoint
    And the upstream is associated with chain ID 1

  Scenario: Custom endpoint is validated before adding
    When I run "obol network add 1 --endpoint https://unreachable.example.com/rpc"
    Then the endpoint reachability is checked
    And if the endpoint is unreachable, a warning is displayed

  # -------------------------------------------------------------------
  # Write method blocking
  # -------------------------------------------------------------------

  Scenario: Write methods are blocked by default
    Given chain ID 1 is configured in eRPC without --allow-writes
    When an eth_sendRawTransaction request arrives at eRPC for chain 1
    Then eRPC blocks the request
    And returns an error indicating write methods are not allowed

  Scenario: Write methods are allowed with --allow-writes flag
    When I run "obol network add 1 --allow-writes"
    Then the eRPC config for chain ID 1 allows eth_sendRawTransaction
    And write requests are forwarded to the upstream

  Scenario: Local Ethereum nodes always have writes blocked
    Given a local Ethereum node is deployed as "ethereum-fluffy-penguin"
    And the node is registered as a priority upstream in eRPC
    When an eth_sendRawTransaction request arrives for the local node's chain
    Then the write method is blocked on the local upstream
    And the write request is routed to remote upstreams instead

  # -------------------------------------------------------------------
  # Remove chain RPCs
  # -------------------------------------------------------------------

  Scenario: Remove chain RPCs from eRPC
    Given chain ID 1 is configured in eRPC with multiple upstreams
    When I run "obol network remove 1"
    Then all upstreams for chain ID 1 are removed from the eRPC config
    And the network entry for chain ID 1 is removed

  Scenario: Remove non-existent chain is a no-op
    Given chain ID 999 is not configured in eRPC
    When I run "obol network remove 999"
    Then the command completes without error
    And the eRPC config is unchanged

  # -------------------------------------------------------------------
  # eRPC status and listing
  # -------------------------------------------------------------------

  Scenario: eRPC status shows upstream counts
    Given the eRPC config has:
      | chain | upstream_count |
      | 1     | 3              |
      | 137   | 2              |
    When I run "obol network list"
    Then the output lists configured chains with their upstream counts:
      | chain_id | name             | upstreams |
      | 1        | Ethereum Mainnet | 3         |
      | 137      | Polygon Mainnet  | 2         |

  # -------------------------------------------------------------------
  # Local Ethereum node deployment
  # -------------------------------------------------------------------

  Scenario: Install local Ethereum node with two-stage templating
    When I run "obol network install ethereum --id fluffy-penguin"
    Then Stage 1 renders values.yaml from values.yaml.gotmpl with CLI flags
    And Stage 2 runs "helmfile sync" with the rendered values and id "fluffy-penguin"
    And the node is deployed in namespace "ethereum-fluffy-penguin"
    And the node is registered as a priority upstream in eRPC

  Scenario: Install local Ethereum node with auto-generated petname
    When I run "obol network install ethereum"
    Then a petname ID is auto-generated
    And the node is deployed in namespace "ethereum-<petname>"

  Scenario: Local node registered as priority upstream in eRPC
    Given a local Ethereum node "ethereum-fluffy-penguin" is deployed
    Then the eRPC config contains an upstream with:
      | field    | value                                                                      |
      | id       | local-ethereum-fluffy-penguin                                              |
      | endpoint | http://ethereum-execution.ethereum-fluffy-penguin.svc.cluster.local:8545   |

  # -------------------------------------------------------------------
  # Network sync and status
  # -------------------------------------------------------------------

  Scenario: Network sync re-runs helmfile for deployed network
    Given a local Ethereum node is deployed with id "fluffy-penguin"
    When I run "obol network sync ethereum fluffy-penguin"
    Then helmfile sync is re-run for the "ethereum-fluffy-penguin" deployment

  Scenario: Network status shows deployment health
    Given a local Ethereum node is deployed with id "fluffy-penguin"
    When I run "obol network status ethereum fluffy-penguin"
    Then the output shows the deployment status of the Ethereum node
    And includes pod readiness and sync status

  Scenario: Network delete removes deployment and eRPC upstream
    Given a local Ethereum node is deployed with id "fluffy-penguin"
    When I run "obol network delete ethereum fluffy-penguin"
    Then the namespace "ethereum-fluffy-penguin" is deleted
    And the local upstream for "fluffy-penguin" is removed from the eRPC config
