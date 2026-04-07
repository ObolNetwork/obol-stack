@integration
Feature: x402 Payment Flow — Real User Journey
  As an operator running the Obol Stack
  I want to sell inference and have buyers pay for it
  So that the full production path is verified end-to-end

  # This test follows the exact user journey. Every step maps to a real CLI
  # command or controller-owned cluster behavior.
  #
  # Run (full bootstrap — creates cluster from scratch):
  #   go test -tags integration -v -run TestBDDIntegration -timeout 20m ./internal/x402/
  #
  # Run (skip bootstrap — use existing cluster):
  #   OBOL_INTEGRATION_SKIP_BOOTSTRAP=true OBOL_TEST_MODEL=qwen3.5:9b \
  #   go test -tags integration -v -run TestBDDIntegration -timeout 10m ./internal/x402/

  Background:
    Given an Anvil fork of Base Sepolia is running
    And the buyer has 10 USDC on the fork
    And a facilitator is running against the fork
    And the x402-verifier is patched to use the facilitator
    And a buyer with Anvil key "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"

  # ─── Sell-side: the operator creates a ServiceOffer via CLI ─────────

  @integration @local @sell
  Scenario: Operator sells inference via CLI and the controller reconciles
    When the operator runs "obol sell http" to create a ServiceOffer
    And the controller reconciles the ServiceOffer
    Then the ServiceOffer status is "Ready"
    And a Middleware "x402-bdd-test" exists in the offer namespace
    And an HTTPRoute "so-bdd-test" exists in the offer namespace

  # ─── Buy-side: unpaid request gets 402 ──────────────────────────────

  @integration @local @payment-gate
  Scenario: Unpaid request returns 402 with pricing
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    And the response body contains x402Version 1
    And the response body contains a valid accepts array

  # ─── Buy-side: paid request returns real inference ──────────────────

  @integration @local @payment
  Scenario: Paid request returns real inference
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    When the buyer signs an EIP-712 payment from the 402 response
    And the buyer sends the paid POST to the priced route
    Then the response status is 200
    And the response contains a real inference result
    And the facilitator received at least 1 verify call

  # ─── Buy-side: discovery-driven payment ─────────────────────────────

  @integration @local @discovery
  Scenario: Full discovery-to-payment cycle
    When the buyer sends an unpaid POST to the priced route
    Then the response status is 402
    And the 402 response contains payTo and price and network
    When the buyer constructs payment from the discovered pricing
    And the buyer sends the paid POST to the priced route
    Then the response status is 200
    And the response contains non-empty inference content

  # ─── Buy-side: through Cloudflare tunnel ────────────────────────────

  @integration @tunnel
  Scenario: Paid request through Cloudflare tunnel
    Given the Cloudflare tunnel is reachable
    When the buyer sends an unpaid POST through the tunnel
    Then the response status is 402
    When the buyer signs an EIP-712 payment from the 402 response
    And the buyer sends the paid POST through the tunnel
    Then the response status is 200
    And the response contains a real inference result

  # ─── Buy-side: discover via tunnel → probe → verify ──────────────

  @integration @tunnel @discover
  Scenario: Agent discovers registered service through tunnel
    Given the Cloudflare tunnel is reachable
    When the agent fetches the registration JSON from the tunnel
    Then the registration contains x402Support
    And the registration contains a service endpoint
    And the registration contains OASF skills
    And the registration contains OASF domains
    When the agent probes the tunnel service endpoint
    Then the probe returns 402 with pricing info

  # ─── Cleanup: operator deletes the offer ────────────────────────────

  @integration @local @cleanup
  Scenario: Operator deletes ServiceOffer and resources are cleaned up
    When the operator deletes the ServiceOffer via CLI
    Then the ServiceOffer no longer exists
    And no Middleware exists for the offer
    And no HTTPRoute exists for the offer
