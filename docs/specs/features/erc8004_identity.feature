# References:
#   SPEC.md Section 3.8 — ERC-8004 Identity
#   SPEC.md Section 3.4.2 — Sell-Side Flow (Stage 5: Registered)
#   SPEC.md Section 7.1 — Tunnel Exposure (/.well-known)
#   SPEC.md Section 5.3 — ServiceOffer CRD Schema (registration spec)

Feature: ERC-8004 Identity
  As an AI agent operator
  I want to register my agent on-chain using the ERC-8004 Identity Registry
  So that other agents and users can discover and verify my agent's identity

  Background:
    Given the cluster is running
    And a wallet is available with a private key
    And the Base Sepolia RPC endpoint is reachable

  # -------------------------------------------------------------------
  # Agent registration on Base Sepolia
  # -------------------------------------------------------------------

  Scenario: Agent registers on Base Sepolia Identity Registry
    Given the wallet has sufficient ETH for gas on Base Sepolia
    When I run "obol sell register --name my-agent --private-key-file /path/to/keyfile"
    Then a Register transaction is submitted to the Identity Registry at "0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D"
    And the transaction mints an ERC-721 NFT for the agent
    And the returned agentId is the minted token ID
    And the agent URI is set to the tunnel URL "/.well-known/agent-registration.json"

  Scenario: Registration during sell-side reconciliation (Stage 5)
    Given a ServiceOffer CR "myapi" has reached stage 4 (RoutePublished)
    And registration is enabled with name "My Inference Agent"
    And the tunnel URL is "https://stack.example.com"
    When the reconciler evaluates stage 5
    Then the agent is registered on Base Sepolia
    And the ServiceOffer status is updated with:
      | field                | value                |
      | agentId              | <minted token ID>    |
      | registrationTxHash   | <transaction hash>   |
    And the condition "Registered" is set to "True"

  Scenario: Registration submits correct agent metadata
    When the agent is registered with:
      | field       | value                          |
      | name        | My Inference Agent              |
      | description | Sells qwen3.5:9b inference      |
      | image       | https://example.com/icon.png    |
    Then the registration transaction includes the metadata
    And the agent URI points to the /.well-known endpoint

  # -------------------------------------------------------------------
  # Registration JSON at /.well-known
  # -------------------------------------------------------------------

  Scenario: Registration JSON served at /.well-known endpoint
    Given an agent has been registered with agentId "42"
    And the tunnel is active at "https://stack.example.com"
    When a GET request is made to "https://stack.example.com/.well-known/agent-registration.json"
    Then the response is HTTP 200 with Content-Type "application/json"
    And the JSON body conforms to the AgentRegistration schema:
      | field           | value                                                      |
      | type            | https://eips.ethereum.org/EIPS/eip-8004#registration-v1   |
      | name            | My Inference Agent                                         |
      | x402Support     | true                                                       |
      | active          | true                                                       |
    And the "registrations" array contains:
      | agentId | agentRegistry                                        |
      | 42      | 0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D          |
    And the "services" array contains at least one service endpoint

  Scenario: Registration JSON includes supported trust mechanisms
    Given the ServiceOffer has supportedTrust ["reputation"]
    When the registration JSON is served
    Then the "supportedTrust" array contains "reputation"

  Scenario: Registration JSON httpd Deployment is minimal
    Given the registration JSON has been published
    When I inspect the httpd Deployment in "traefik" namespace
    Then it uses a busybox image serving the ConfigMap content
    And an HTTPRoute routes "/.well-known/agent-registration.json" to the httpd Service

  # -------------------------------------------------------------------
  # Metadata update
  # -------------------------------------------------------------------

  Scenario: Metadata update via SetMetadata
    Given an agent is registered with agentId "42"
    When SetMetadata is called with:
      | key           | value                       |
      | description   | Updated inference service    |
      | version       | 2.0                         |
    Then a SetMetadata transaction is submitted to the registry
    And the on-chain metadata is updated for agentId "42"

  Scenario: Agent URI update via SetAgentURI
    Given an agent is registered with agentId "42"
    And the tunnel hostname changes to "new-stack.example.com"
    When SetAgentURI is called with the new URI
    Then the on-chain agent URI is updated to "https://new-stack.example.com/.well-known/agent-registration.json"

  Scenario: Read metadata from registry
    Given an agent is registered with agentId "42" and metadata key "description" = "Inference service"
    When GetMetadata is called for agentId "42" and key "description"
    Then the returned value is "Inference service"

  Scenario: Read token URI from registry
    Given an agent is registered with agentId "42"
    When TokenURI is called for agentId "42"
    Then the returned URI is the agent's metadata endpoint

  # -------------------------------------------------------------------
  # Degraded mode without ETH
  # -------------------------------------------------------------------

  Scenario: Registration degrades to OffChainOnly without ETH
    Given the wallet has zero ETH on Base Sepolia
    When the reconciler evaluates registration (stage 5)
    Then no on-chain Register transaction is submitted
    And the /.well-known/agent-registration.json is still created and served
    But the "registrations" array in the JSON is empty
    And the condition "Registered" is set to "True" with reason "OffChainOnly"

  Scenario: OffChainOnly agent upgrades to on-chain after funding
    Given the agent was registered in OffChainOnly mode
    And the wallet has been funded with ETH
    When the reconciler re-evaluates registration
    Then the on-chain Register transaction is submitted
    And the "registrations" array is populated with the agentId
    And the condition "Registered" reason is updated to "OnChain"

  # -------------------------------------------------------------------
  # Error states
  # -------------------------------------------------------------------

  Scenario: Registration fails when RPC is unreachable
    Given the Base Sepolia RPC endpoint is unreachable
    When the reconciler evaluates registration
    Then the error "erc8004: dial" is recorded
    And the condition "Registered" is set to "False"
    And the reconciler retries on the next loop

  Scenario: Registration fails when transaction is not mined
    Given the RPC endpoint is reachable but the network is congested
    When the Register transaction is submitted
    Then the error "erc8004: wait mined" may occur
    And the reconciler retries on the next loop

  Scenario: Registration uses correct contract address per chain
    When registering on Base Sepolia
    Then the contract address "0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D" is used
