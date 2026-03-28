# References:
#   SPEC.md Section 3.5 — Monetize Buy Side
#   SPEC.md Section 4.1 — x402 Payment Protocol
#   SPEC.md Section 7.2 — Payment Security

Feature: Buy-Side Payments
  As an AI agent
  I want to purchase inference from remote x402-gated sellers using pre-signed vouchers
  So that I can access paid models without exposing a hot wallet

  Background:
    Given the cluster is running
    And the x402-buyer sidecar is running in the "litellm" Deployment
    And a remote seller is available at "https://seller.example.com/services/qwen"
    And the seller prices inference at "0.001" USDC per request on "base-sepolia"

  # -------------------------------------------------------------------
  # Probe and discovery
  # -------------------------------------------------------------------

  Scenario: Probe discovers seller pricing via 402 response
    When the agent runs "buy.py probe https://seller.example.com/services/qwen"
    Then the probe sends a request to the seller endpoint
    And the seller responds with HTTP 402 and PaymentRequirements
    And the probe extracts:
      | field              | value          |
      | scheme             | exact          |
      | network            | eip155:84532   |
      | maxAmountRequired  | 1000           |
      | payTo              | 0xSELLER       |
      | asset              | 0x036CbD...    |
    And the agent receives the pricing information

  Scenario: Probe handles non-402 seller response
    Given the seller endpoint responds with HTTP 200 (no payment required)
    When the agent runs "buy.py probe https://seller.example.com/services/free"
    Then the probe reports the endpoint does not require payment

  # -------------------------------------------------------------------
  # Pre-signed ERC-3009 vouchers
  # -------------------------------------------------------------------

  Scenario: Pre-signed ERC-3009 vouchers stored in ConfigMap
    When the agent runs "buy.py buy --count 10 --seller https://seller.example.com/services/qwen"
    Then 10 ERC-3009 TransferWithAuthorization vouchers are pre-signed
    And the vouchers are stored in the "x402-buyer-auths" ConfigMap in "llm" namespace
    And each voucher contains:
      | field       | description                                  |
      | signature   | EIP-712 typed signature                      |
      | from        | buyer wallet address                         |
      | to          | seller payTo address                         |
      | value       | price per request in base units              |
      | validAfter  | 0 (immediately valid)                        |
      | validBefore | max uint256 (no expiry)                      |
      | nonce       | unique random 32-byte nonce                  |
    And the "x402-buyer-config" ConfigMap contains the upstream mapping for the seller

  Scenario: Buyer config maps model to upstream
    Given vouchers have been pre-signed for seller "seller-qwen"
    When the "x402-buyer-config" ConfigMap is inspected
    Then it contains an upstream entry:
      | field       | value                                           |
      | url         | https://seller.example.com/services/qwen        |
      | remoteModel | qwen3.5:9b                                      |
      | network     | base-sepolia                                    |
      | payTo       | 0xSELLER                                        |

  # -------------------------------------------------------------------
  # Paid request flow
  # -------------------------------------------------------------------

  Scenario: Paid request consumes one voucher and forwards to seller
    Given the buyer has 5 pre-signed vouchers for upstream "seller-qwen"
    When the agent sends a chat completion request for model "paid/qwen3.5:9b"
    Then LiteLLM routes the request to the x402-buyer sidecar at ":8402"
    And the sidecar strips the "paid/" prefix to resolve model "qwen3.5:9b"
    And the sidecar forwards the request to the seller
    And the seller responds with HTTP 402
    And the sidecar pops one voucher from the pool
    And the sidecar retries the request with the X-PAYMENT header
    And the seller responds with HTTP 200 and the inference result
    And the remaining voucher count is 4

  Scenario: Paid request with openai prefix is resolved correctly
    Given the buyer has vouchers for upstream "seller-qwen"
    When a request arrives for model "paid/openai/qwen3.5:9b"
    Then the sidecar strips both "paid/" and "openai/" prefixes
    And resolves to model "qwen3.5:9b"
    And routes to the correct upstream

  Scenario: Voucher consumption is atomic
    Given the buyer has 1 pre-signed voucher for upstream "seller-qwen"
    When two concurrent requests arrive for model "paid/qwen3.5:9b"
    Then exactly one request consumes the voucher
    And the other request receives an error indicating pool exhaustion

  # -------------------------------------------------------------------
  # Voucher pool exhaustion
  # -------------------------------------------------------------------

  Scenario: Voucher pool exhaustion returns error
    Given the buyer has 0 pre-signed vouchers for upstream "seller-qwen"
    When a request arrives for model "paid/qwen3.5:9b"
    Then the sidecar returns an error: "pre-signed auth pool exhausted"
    And no request is forwarded to the seller

  Scenario: No purchased upstream mapped returns error
    Given no buyer config exists for model "paid/unknown-model"
    When a request arrives for model "paid/unknown-model"
    Then the sidecar returns an error: "no purchased upstream mapped"

  # -------------------------------------------------------------------
  # Sidecar status and observability
  # -------------------------------------------------------------------

  Scenario: Sidecar /status endpoint reports remaining vouchers
    Given the buyer started with 10 vouchers for "seller-qwen"
    And 3 vouchers have been consumed
    When I send a GET request to the sidecar at "/status"
    Then the response is JSON with:
      | upstream     | remaining | spent |
      | seller-qwen  | 7         | 3     |

  Scenario: Sidecar /healthz returns liveness status
    When I send a GET request to the sidecar at "/healthz"
    Then the response is HTTP 200

  Scenario: Sidecar /metrics exposes Prometheus metrics
    When I send a GET request to the sidecar at "/metrics"
    Then the response contains Prometheus-format metrics
    And a PodMonitor in the "llm" namespace scrapes the sidecar

  # -------------------------------------------------------------------
  # State persistence across restarts
  # -------------------------------------------------------------------

  Scenario: Consumed nonces survive sidecar restart
    Given the buyer has consumed 3 vouchers with specific nonces
    When the x402-buyer sidecar is restarted
    Then the StateStore reloads consumed nonces
    And the previously consumed vouchers are not reused
    And the remaining voucher count reflects prior consumption

  # -------------------------------------------------------------------
  # Security properties
  # -------------------------------------------------------------------

  Scenario: Sidecar has zero signer access
    Given the x402-buyer sidecar is running
    Then the sidecar container has no private key mounted
    And the sidecar can only use pre-signed authorizations from ConfigMaps
    And maximum loss is bounded to N * price where N is the voucher count
