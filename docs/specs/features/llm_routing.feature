# References:
#   SPEC.md Section 3.2 — LLM Routing
#   SPEC.md Section 3.6.4 — Cloud Provider Detection
#   SPEC.md Section 3.5 — Monetize Buy Side (paid inference routing)

Feature: LLM Routing
  As an operator
  I want the LiteLLM gateway to auto-discover and route to all available LLM providers
  So that the OpenClaw agent can use local, cloud, and paid remote models through a single endpoint

  Background:
    Given the cluster is running
    And the LiteLLM Deployment exists in the "llm" namespace

  # -------------------------------------------------------------------
  # Auto-detection of Ollama models
  # -------------------------------------------------------------------

  Scenario: Auto-detect Ollama models during stack up
    Given Ollama is running on the host with models:
      | model         |
      | qwen3.5:9b    |
      | llama3.2:3b   |
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains entries for "qwen3.5:9b" and "llama3.2:3b"
    And each Ollama model entry has provider "ollama" and api_base pointing to the Ollama service
    And the LiteLLM Deployment is restarted exactly once

  Scenario: Auto-configure skips Ollama when not running
    Given Ollama is not running on the host
    When "obol stack up" runs autoConfigureLLM
    Then no Ollama model entries are added to "litellm-config"
    And a warning is logged: Ollama not available
    And the stack up continues without failure

  Scenario: Auto-configure updates models on subsequent stack up
    Given the cluster was previously started with Ollama model "qwen3.5:9b"
    And Ollama now has models "qwen3.5:9b" and "deepseek-r1:7b"
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains entries for both models
    And the LiteLLM Deployment is restarted

  # -------------------------------------------------------------------
  # Cloud provider detection from environment variables
  # -------------------------------------------------------------------

  Scenario: Detect Anthropic provider from ANTHROPIC_API_KEY
    Given the environment variable "ANTHROPIC_API_KEY" is set
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains a wildcard entry "anthropic/*"
    And the "litellm-secrets" Secret contains the Anthropic API key

  Scenario: Detect Anthropic provider from CLAUDE_CODE_OAUTH_TOKEN
    Given the environment variable "CLAUDE_CODE_OAUTH_TOKEN" is set
    And "ANTHROPIC_API_KEY" is not set
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains a wildcard entry "anthropic/*"
    And the "litellm-secrets" Secret contains the OAuth token as the Anthropic key

  Scenario: Detect OpenAI provider from OPENAI_API_KEY
    Given the environment variable "OPENAI_API_KEY" is set
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains a wildcard entry "openai/*"
    And the "litellm-secrets" Secret contains the OpenAI API key

  Scenario: Detect cloud provider from OpenClaw agent model preference
    Given the file "~/.openclaw/openclaw.json" specifies agent model "anthropic/claude-sonnet-4-6"
    And the environment variable "ANTHROPIC_API_KEY" is set
    When "obol stack up" runs autoConfigureLLM
    Then the Anthropic provider is auto-configured

  Scenario: No cloud provider configured when API keys are absent
    Given no cloud provider API keys are set in the environment
    When "obol stack up" runs autoConfigureLLM
    Then no cloud provider entries are added to "litellm-config"
    And a warning is logged for each missing provider
    And the stack up continues without failure

  # -------------------------------------------------------------------
  # Manual model setup
  # -------------------------------------------------------------------

  Scenario: Manual provider setup via obol model setup
    Given the cluster is running
    When I run "obol model setup --provider anthropic"
    And I provide the API key
    Then the "litellm-config" ConfigMap is patched with the Anthropic wildcard entry
    And the "litellm-secrets" Secret is updated
    And the LiteLLM Deployment is restarted

  # -------------------------------------------------------------------
  # Custom endpoint validation
  # -------------------------------------------------------------------

  Scenario: Custom endpoint passes reachability test
    Given a custom inference endpoint is running at "http://myhost:8080/v1"
    When I run "obol model setup custom --name my-model --endpoint http://myhost:8080/v1 --model gpt-4"
    Then the endpoint reachability test passes
    And the "litellm-config" ConfigMap contains the custom model entry
    And the LiteLLM Deployment is restarted

  Scenario: Custom endpoint fails reachability test
    Given no service is running at "http://unreachable:8080/v1"
    When I run "obol model setup custom --name my-model --endpoint http://unreachable:8080/v1 --model gpt-4"
    Then the command fails with a reachability error
    And the "litellm-config" ConfigMap is not modified

  # -------------------------------------------------------------------
  # Model ranking
  # -------------------------------------------------------------------

  Scenario: Cloud providers are preferred over local Ollama
    Given Ollama is running with model "qwen3.5:9b"
    And the environment variable "ANTHROPIC_API_KEY" is set
    When "obol stack up" runs autoConfigureLLM
    Then the "litellm-config" ConfigMap contains entries for both providers
    And cloud provider entries appear before Ollama entries in the model list

  # -------------------------------------------------------------------
  # Paid inference routing through buyer sidecar
  # -------------------------------------------------------------------

  Scenario: Paid inference routes through buyer sidecar
    Given the "litellm-config" ConfigMap contains the permanent "paid/*" entry
    When a request arrives for model "paid/qwen3.5:9b"
    Then LiteLLM routes the request to "http://127.0.0.1:8402/v1"
    And the x402-buyer sidecar handles payment attachment

  Scenario: LiteLLM config contains permanent paid catch-all
    When the "litellm-config" ConfigMap is loaded
    Then it contains a model entry with name "paid/*"
    And the entry has provider "openai" and api_base "http://127.0.0.1:8402/v1"

  # -------------------------------------------------------------------
  # Error states
  # -------------------------------------------------------------------

  Scenario: Model setup fails when cluster is not running
    Given the cluster is not running
    When I run "obol model setup --provider anthropic"
    Then the command fails with "cluster not running"

  Scenario: Model setup fails with empty model list
    Given the cluster is running
    And Ollama has no models loaded
    When I run "obol model setup --provider ollama"
    Then the command fails with "no models to configure"
