@integration
Feature: x402 Payment Flow — Full Infrastructure
  As a buyer agent
  I want to pay for real inference through a self-provisioned stack
  So that the full production path is verified end-to-end

  # Self-contained: TestMain bootstraps cluster, deploys ServiceOffer,
  # waits for reconciliation. Each scenario starts its own Anvil fork,
  # facilitator, and patches the verifier. No manual setup needed.
  #
  # Run:
  #   go test -tags integration -v -run TestBDDIntegration -timeout 15m ./internal/x402/

  Background:
    Given an Anvil fork of Base Sepolia is running
    And the buyer has 10 USDC on the fork
    And a facilitator is running against the fork
    And the x402-verifier is patched to use the facilitator
    And a buyer with Anvil key "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"

  @integration @local @payment-gate
  Scenario: Unpaid request returns 402 with pricing
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    And the response body contains x402Version 1
    And the response body contains a valid accepts array

  @integration @local @payment
  Scenario: Paid request returns real inference
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    When the buyer signs an EIP-712 payment from the 402 response
    And the buyer sends the paid POST to the priced route
    Then the response status is 200
    And the response contains a real inference result
    And the facilitator received at least 1 verify call

  @integration @local @discovery
  Scenario: Full discovery-to-payment cycle
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    And the 402 response contains payTo and price and network
    When the buyer constructs payment from the discovered pricing
    And the buyer sends the paid POST to the priced route
    Then the response status is 200
    And the response contains non-empty inference content

  @integration @tunnel
  Scenario: Paid request through Cloudflare tunnel
    Given the Cloudflare tunnel is reachable
    When the buyer sends an unpaid POST through the tunnel
    Then the response status is 402
    When the buyer signs an EIP-712 payment from the 402 response
    And the buyer sends the paid POST through the tunnel
    Then the response status is 200
    And the response contains a real inference result
