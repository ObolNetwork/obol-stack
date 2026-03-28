# References:
#   SPEC.md Section 3.4 — Monetize Sell Side
#   SPEC.md Section 4.1 — x402 Payment Protocol
#   SPEC.md Section 5.3 — ServiceOffer CRD Schema
#   SPEC.md Section 7.1 — Tunnel Exposure
#   SPEC.md Section 3.4.4 — x402-verifier (ForwardAuth)

Feature: Sell-Side Monetization
  As an operator
  I want to sell access to cluster services via x402 micropayments
  So that I can earn USDC for every inference request served

  Background:
    Given the cluster is running
    And a wallet is configured with address "0xSELLER"
    And the chain is set to "base-sepolia"

  # -------------------------------------------------------------------
  # ServiceOffer creation
  # -------------------------------------------------------------------

  Scenario: obol sell http creates a ServiceOffer CR
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm"
    Then a ServiceOffer CR named "myapi" is created in "openclaw-obol-agent" namespace
    And the ServiceOffer spec contains:
      | field                  | value          |
      | payment.scheme         | exact          |
      | payment.network        | base-sepolia   |
      | payment.payTo          | 0xSELLER       |
      | payment.price.perRequest | 0.001        |
      | upstream.service       | litellm        |
      | upstream.port          | 4000           |
      | upstream.namespace     | llm            |
      | path                   | /services/myapi |

  Scenario: obol sell http with per-mtok pricing
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --per-mtok 1.00 --upstream litellm --port 4000 --namespace llm"
    Then a ServiceOffer CR named "myapi" is created
    And the ServiceOffer spec contains:
      | field                    | value |
      | payment.price.perMTok    | 1.00  |

  Scenario: obol sell http with health path
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm --health-path /health/readiness"
    Then the ServiceOffer spec has upstream.healthPath "/health/readiness"

  Scenario: obol sell http activates tunnel on first sell
    Given no tunnel is currently active
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm"
    Then EnsureTunnelForSell() is called
    And the quick-mode tunnel is activated

  Scenario: obol sell http rejects unsupported chain
    When I run "obol sell http myapi --wallet 0xSELLER --chain ethereum-mainnet --price 0.001 --upstream litellm --port 4000 --namespace llm"
    Then the command fails with "unsupported chain"

  Scenario: obol sell http rejects non-HTTPS facilitator
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm --facilitator http://example.com"
    Then the command fails with "facilitator URL must use HTTPS"

  # -------------------------------------------------------------------
  # 6-stage reconciliation
  # -------------------------------------------------------------------

  Scenario: Stage 1 — ModelReady
    Given a ServiceOffer CR "myapi" exists with type "inference" and model "qwen3.5:9b"
    When the reconciler evaluates stage 1
    Then the condition "ModelReady" is set to "True" if the model is available in LiteLLM
    And the condition "ModelReady" is set to "False" with a message if the model is not available

  Scenario: Stage 2 — UpstreamHealthy
    Given the ServiceOffer CR "myapi" has condition "ModelReady" = "True"
    And the upstream service "litellm" in namespace "llm" is healthy at "/health/readiness"
    When the reconciler evaluates stage 2
    Then the condition "UpstreamHealthy" is set to "True"

  Scenario: Stage 2 — UpstreamHealthy fails on unhealthy upstream
    Given the ServiceOffer CR "myapi" has condition "ModelReady" = "True"
    And the upstream service "litellm" in namespace "llm" returns 503 at "/health/readiness"
    When the reconciler evaluates stage 2
    Then the condition "UpstreamHealthy" is set to "False" with a message indicating the health check failed

  Scenario: Stage 3 — PaymentGateReady
    Given the ServiceOffer CR "myapi" has condition "UpstreamHealthy" = "True"
    When the reconciler evaluates stage 3
    Then a Traefik Middleware resource of type ForwardAuth is created
    And the "x402-pricing" ConfigMap is updated with a route entry for "/services/myapi/*"
    And the route entry contains the price, wallet, and chain from the ServiceOffer
    And the condition "PaymentGateReady" is set to "True"

  Scenario: Stage 4 — RoutePublished
    Given the ServiceOffer CR "myapi" has condition "PaymentGateReady" = "True"
    When the reconciler evaluates stage 4
    Then an HTTPRoute resource is created for path "/services/myapi"
    And the HTTPRoute references the ForwardAuth Middleware
    And traffic matching "/services/myapi/*" is routed to the upstream service
    And the condition "RoutePublished" is set to "True"

  Scenario: Stage 5 — Registered (ERC-8004 on-chain)
    Given the ServiceOffer CR "myapi" has condition "RoutePublished" = "True"
    And registration is enabled in the ServiceOffer spec
    And the tunnel URL is available
    When the reconciler evaluates stage 5
    Then an ERC-8004 registration is submitted on Base Sepolia
    And the status field "agentId" is set to the minted token ID
    And the status field "registrationTxHash" is set
    And a ConfigMap with agent-registration.json is created
    And an httpd Deployment serves /.well-known/agent-registration.json
    And the condition "Registered" is set to "True"

  Scenario: Stage 5 — Registration degrades without ETH
    Given the ServiceOffer CR "myapi" has condition "RoutePublished" = "True"
    And the wallet has zero ETH for gas
    When the reconciler evaluates stage 5
    Then the registration degrades to OffChainOnly mode
    And the /.well-known/agent-registration.json is still served
    But no on-chain transaction is submitted

  Scenario: Stage 6 — Ready
    Given all 5 prior conditions are "True"
    When the reconciler evaluates stage 6
    Then the condition "Ready" is set to "True"
    And the status field "endpoint" is set to the full public URL

  Scenario: Reconciled resources have ownerReferences for auto-GC
    Given a ServiceOffer CR "myapi" has reached "Ready" state
    When I inspect the Middleware, HTTPRoute, ConfigMap, and httpd Deployment
    Then each resource has an ownerReference pointing to the ServiceOffer CR
    And deleting the ServiceOffer cascades deletion to all owned resources

  # -------------------------------------------------------------------
  # x402-verifier behavior
  # -------------------------------------------------------------------

  Scenario: x402-verifier responds 402 with pricing for unauthenticated requests
    Given the route "/services/myapi/*" is configured with price "0.001" USDC
    When a request arrives at "/services/myapi/data" without an X-PAYMENT header
    Then the x402-verifier responds with HTTP 402
    And the response body contains PaymentRequirements JSON with:
      | field              | value                    |
      | x402Version        | 1                        |
      | accepts[0].scheme  | exact                    |
      | accepts[0].network | eip155:84532             |
      | accepts[0].maxAmountRequired | 1000           |

  Scenario: x402-verifier passes through requests with valid payment
    Given the route "/services/myapi/*" is configured with price "0.001" USDC
    When a request arrives at "/services/myapi/data" with a valid X-PAYMENT header
    Then the x402-verifier delegates verification to the facilitator
    And the facilitator confirms the payment is valid
    And the x402-verifier responds with HTTP 200
    And the upstream receives the request with an Authorization header

  Scenario: x402-verifier passes through unmatched routes for free
    Given the route "/services/myapi/*" is configured in pricing
    When a request arrives at "/health" which matches no pricing route
    Then the x402-verifier responds with HTTP 200
    And the request proceeds to the upstream without payment

  Scenario: x402-verifier hot-reloads pricing config
    Given the x402-verifier is running with route "/services/old/*"
    When the "x402-pricing" ConfigMap is updated to add route "/services/new/*"
    And 5 seconds elapse for the config watcher poll
    Then the verifier accepts the new route "/services/new/*"
    And the old route "/services/old/*" is no longer active

  # -------------------------------------------------------------------
  # Pricing models
  # -------------------------------------------------------------------

  Scenario: perRequest pricing is used directly
    Given a ServiceOffer with price.perRequest = "0.001"
    When the reconciler creates the pricing route
    Then the route price is "1000" (0.001 USDC in base units)
    And the route priceModel is "perRequest"

  Scenario: perMTok pricing is converted at 1000 tokens per request
    Given a ServiceOffer with price.perMTok = "1.00"
    When the reconciler creates the pricing route
    Then the effective perRequest price is perMTok / 1000
    And the route contains both perMTok and the approximated perRequest
    And approxTokensPerRequest is set to 1000

  # -------------------------------------------------------------------
  # CLI management commands
  # -------------------------------------------------------------------

  Scenario: obol sell list shows active ServiceOffers
    Given ServiceOffers "myapi" and "myinference" exist
    When I run "obol sell list"
    Then the output lists both ServiceOffers with their status

  Scenario: obol sell status shows reconciliation progress
    Given a ServiceOffer "myapi" is stuck at stage 2 (UpstreamHealthy = False)
    When I run "obol sell status myapi"
    Then the output shows each condition with its status
    And the "UpstreamHealthy" condition shows the failure message

  Scenario: obol sell delete removes ServiceOffer and owned resources
    Given a ServiceOffer "myapi" exists at "Ready" state
    When I run "obol sell delete myapi"
    Then the ServiceOffer CR is deleted
    And all owned resources (Middleware, HTTPRoute, ConfigMaps) are garbage collected
