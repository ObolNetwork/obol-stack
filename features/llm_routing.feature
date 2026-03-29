@bdd
Feature: LLM routing and provider management
  As a local operator
  I want one model gateway for local, cloud, and paid inference routes
  So that OpenClaw instances and buyers see a consistent model contract

  # References: SPEC Section 3.2 (LLM Routing and Provider Management), B&E Section 2.2 (LLM Routing)

  Background:
    Given the stack has a cluster-wide LiteLLM deployment

  @phase1 @fast
  Scenario: LiteLLM is the central operator-facing gateway
    Given an OpenClaw instance needs model access
    When the instance sends inference traffic through the platform
    Then the request is routed through LiteLLM
    And provider-specific credentials remain centralized at the cluster gateway

  @phase1 @fast
  Scenario: Invalid custom endpoints are rejected before publication
    Given the operator supplies a custom OpenAI-compatible endpoint
    When the operator runs model setup for that endpoint
    Then the endpoint is validated before it is added to the route set
    And broken provider entries are not published to downstream consumers

  @phase1
  Scenario Outline: Model namespaces resolve to the correct upstream class
    Given LiteLLM is configured for <namespace_type>
    When a request targets the model namespace <model_name>
    Then the platform routes the request to <upstream_class>

    Examples:
      | namespace_type      | model_name      | upstream_class          |
      | local Ollama        | llama3.2:3b     | the local model runtime |
      | cloud Anthropic     | claude-sonnet-4-5-20250929 | the Anthropic API |
      | cloud OpenAI        | gpt-4o          | the OpenAI API          |
      | buy-side paid route | paid/qwen3.5:9b | the x402 buyer sidecar  |
