@bdd
Feature: Sell-side monetization
  As a local operator
  I want to expose priced services through the serviceoffer-controller reconciliation loop
  So that public buyers can discover and pay for bounded compute or HTTP endpoints

  # References: SPEC Section 3.5 (Sell-Side Monetization), B&E Section 2.5 (Sell-Side Monetization)

  Background:
    Given the operator has a running stack with the elevated agent runtime available

  @phase1 @fast
  Scenario: A ServiceOffer is created in the namespace the operator chose
    Given the operator creates a sell-side offer with an explicit namespace
    When the CLI submits the ServiceOffer resource
    Then the resource is written into that namespace
    And downstream pricing and routing assets are derived from that resource

  @phase1 @fast
  Scenario: Probe verifies the payment gate without spending buyer funds
    Given a sell-side offer has published its payment route
    When the operator runs sell probe against that offer
    Then the command confirms the payment gate is reachable
    And no paid inference budget is consumed

  @phase1
  Scenario Outline: Pricing models remain explicit about their current billing contract
    Given a sell-side offer uses the pricing model <pricing_model>
    When the offer is reconciled successfully
    Then the route publishes payment terms for <pricing_model>
    And operators can inspect the current pricing contract through status surfaces

    Examples:
      | pricing_model |
      | perRequest    |
      | perMTok       |
      | perHour       |

  @phase2
  Scenario: Exact token metering supplements the pre-request payment gate
    Given an inference offer uses per-token pricing
    When phase 2 exact metering is enabled for that route
    Then pre-request authorization still happens before execution
    And post-response usage updates the seller-side accounting surfaces
