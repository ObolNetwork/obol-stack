# References:
#   SPEC.md Section 3.1 — Stack Lifecycle
#   SPEC.md Section 2.4 — Backend Abstraction
#   SPEC.md Section 5.1 — Configuration Files
#   SPEC.md Section 1.3 — System Constraints

Feature: Stack Lifecycle
  As an operator
  I want to manage the full lifecycle of my local Kubernetes cluster
  So that I can run decentralized AI infrastructure reproducibly

  Background:
    Given Docker is running
    And the obol CLI is installed

  # -------------------------------------------------------------------
  # obol stack init
  # -------------------------------------------------------------------

  Scenario: Stack init generates cluster ID and writes config
    When I run "obol stack init"
    Then a petname cluster ID is generated
    And the file "$OBOL_CONFIG_DIR/.stack-id" contains the cluster ID
    And the file "$OBOL_CONFIG_DIR/.stack-backend" contains "k3d"
    And embedded infrastructure defaults are copied to "$OBOL_CONFIG_DIR/defaults/"
    And template variables "OLLAMA_HOST", "OLLAMA_HOST_IP", "CLUSTER_ID" are substituted in defaults

  Scenario: Stack init resolves absolute paths for Docker volume mounts
    When I run "obol stack init"
    Then all paths in the generated k3d config are absolute
    And no relative paths appear in volume mount declarations

  Scenario: Stack init preserves existing cluster ID on force reinit
    Given I have previously run "obol stack init"
    And the cluster ID is "fluffy-penguin"
    When I run "obol stack init --force"
    Then the cluster ID remains "fluffy-penguin"
    And the backend config is regenerated

  Scenario: Stack init with k3s backend
    When I run "obol stack init --backend k3s"
    Then the file "$OBOL_CONFIG_DIR/.stack-backend" contains "k3s"
    And the Ollama host is resolved as "127.0.0.1"

  Scenario: Stack init fails without Docker when using k3d backend
    Given Docker is not running
    When I run "obol stack init --backend k3d"
    Then the command fails with "prerequisites check failed"

  # -------------------------------------------------------------------
  # obol stack up
  # -------------------------------------------------------------------

  Scenario: Stack up creates k3d cluster and deploys infrastructure
    Given I have run "obol stack init"
    When I run "obol stack up"
    Then a k3d cluster is created with the persisted stack ID
    And kubeconfig is written to "$OBOL_CONFIG_DIR/kubeconfig.yaml"
    And helmfile sync deploys infrastructure to the cluster
    And the following namespaces exist:
      | namespace            |
      | traefik              |
      | llm                  |
      | x402                 |
      | openclaw-obol-agent  |
      | erpc                 |
      | obol-frontend        |
      | monitoring           |

  Scenario: Stack up auto-configures LiteLLM with Ollama models
    Given I have run "obol stack init"
    And Ollama is running on the host with model "qwen3.5:9b"
    When I run "obol stack up"
    Then the "litellm-config" ConfigMap in "llm" namespace contains model "qwen3.5:9b"
    And the LiteLLM Deployment is restarted once

  Scenario: Stack up deploys OpenClaw agent with skills
    Given I have run "obol stack init"
    When I run "obol stack up"
    Then the OpenClaw Deployment exists in "openclaw-obol-agent" namespace
    And skills are injected via host-path PVC
    And the "openclaw-monetize" ClusterRoleBinding is patched with the openclaw ServiceAccount

  Scenario: Stack up auto-starts DNS tunnel when provisioned
    Given I have run "obol stack init"
    And a DNS tunnel is provisioned with hostname "stack.example.com"
    When I run "obol stack up"
    Then the Cloudflare tunnel is started
    And the tunnel URL is propagated to "AGENT_BASE_URL" on the OpenClaw Deployment

  Scenario: Stack up keeps quick tunnel dormant until first sell
    Given I have run "obol stack init"
    And no DNS tunnel is provisioned
    When I run "obol stack up"
    Then the Cloudflare tunnel is not started
    And the cloudflared Deployment has zero replicas

  Scenario: Stack up is idempotent
    Given I have run "obol stack init" and "obol stack up"
    And the cluster is running
    When I run "obol stack up" again
    Then the cluster remains in a healthy state
    And no duplicate resources are created
    And all existing services remain accessible

  Scenario: Stack up cleans up on helmfile sync failure
    Given I have run "obol stack init"
    And helmfile sync will fail due to a malformed template
    When I run "obol stack up"
    Then the command fails
    And the cluster is automatically stopped via Down()

  Scenario: Stack up binds expected ports
    Given I have run "obol stack init"
    And ports 80, 8080, 443, and 8443 are available
    When I run "obol stack up"
    Then the k3d cluster binds host ports 80, 8080, 443, and 8443

  Scenario: Stack up fails when ports are occupied
    Given I have run "obol stack init"
    And port 80 is already in use by another service
    When I run "obol stack up"
    Then the command fails with "port(s) already in use"

  # -------------------------------------------------------------------
  # obol stack down
  # -------------------------------------------------------------------

  Scenario: Stack down deletes cluster but preserves config
    Given the cluster is running
    When I run "obol stack down"
    Then the k3d cluster is deleted
    And the file "$OBOL_CONFIG_DIR/.stack-id" still exists
    And the file "$OBOL_CONFIG_DIR/kubeconfig.yaml" still exists
    And the directory "$OBOL_CONFIG_DIR/defaults/" still exists

  Scenario: Stack down stops the DNS resolver
    Given the cluster is running
    And the DNS resolver for "obol.stack" is active
    When I run "obol stack down"
    Then the DNS resolver is stopped

  # -------------------------------------------------------------------
  # obol stack purge
  # -------------------------------------------------------------------

  Scenario: Stack purge removes config directory
    Given the cluster is running
    When I run "obol stack purge"
    Then the k3d cluster is destroyed
    And the directory "$OBOL_CONFIG_DIR" is removed
    But the directory "$OBOL_DATA_DIR" still exists

  Scenario: Stack purge with force removes root-owned PVCs
    Given the cluster is running
    And root-owned PVC data exists in "$OBOL_DATA_DIR"
    When I run "obol stack purge --force"
    Then the k3d cluster is destroyed
    And the directory "$OBOL_CONFIG_DIR" is removed
    And the directory "$OBOL_DATA_DIR" is removed via sudo

  Scenario: Stack purge prompts for wallet backup
    Given the cluster is running
    And a wallet exists at "$OBOL_DATA_DIR/openclaw-<id>/keystore/"
    When I run "obol stack purge"
    Then the user is prompted to back up the wallet before proceeding
