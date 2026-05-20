@bdd @phase0
Feature: OBOL payment asset
  As a service provider
  I want OBOL to be a first-class x402 payment token
  So that agent services can be priced in OBOL without extra contracts

  # References: SPEC Sections 4.1, 5.4, 5.6, 6; B&E B25-B26, E10

  Background:
    Given the token registry supports USDC and OBOL

  @fast
  Scenario: OBOL sell command writes explicit asset metadata
    When a seller creates an OBOL-priced ServiceOffer
    Then spec.payment.asset.symbol is "OBOL"
    And spec.payment.asset.decimals is 18
    And spec.payment.asset.transferMethod is "permit2"
    And EIP-712 name and version are set

  @integration
  Scenario: OBOL 402 response is signable
    Given an OBOL-priced ServiceOffer is RoutePublished
    When a buyer probes the paid route
    Then the 402 response advertises the OBOL contract address
    And the x402 extensions include the Permit2 signing metadata

  @fast
  Scenario: Unsupported OBOL chain is rejected
    When the seller requests OBOL on an unsupported chain
    Then token resolution fails
    And no ServiceOffer is applied

  @fast
  Scenario: Buyer cannot pay wrong token
    Given seller pricing advertises OBOL
    When the buyer requests token "USDC"
    Then buy preflight rejects the request before signing
