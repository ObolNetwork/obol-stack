# References:
#   SPEC.md Section 3.7 — Tunnel Management
#   SPEC.md Section 7.1 — Tunnel Exposure (Security Model)
#   SPEC.md Section 2.2 — Routing Architecture
#   SPEC.md Section 3.7.6 — Storefront Resources

Feature: Tunnel Exposure
  As an operator
  I want the Cloudflare tunnel to expose only payment-gated and discovery endpoints
  So that internal services remain protected while public services are accessible

  Background:
    Given the cluster is running
    And the Traefik Gateway is deployed in the "traefik" namespace

  # -------------------------------------------------------------------
  # Quick mode tunnel activation
  # -------------------------------------------------------------------

  Scenario: Quick mode tunnel activates on first sell command
    Given no tunnel is currently active
    And the tunnel mode is "quick"
    When I run "obol sell http myapi --wallet 0xSELLER --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm"
    Then the quick tunnel is activated
    And the tunnel URL is a random "*.trycloudflare.com" hostname
    And the cloudflared Deployment is scaled to 1 replica

  Scenario: Quick mode tunnel stays dormant during stack up
    Given the tunnel mode is "quick"
    When I run "obol stack up"
    Then the cloudflared Deployment has zero replicas
    And no tunnel URL is assigned

  Scenario: Quick mode tunnel URL changes on restart
    Given the quick tunnel is active with URL "https://abc123.trycloudflare.com"
    When I run "obol tunnel restart"
    Then the tunnel URL changes to a new "*.trycloudflare.com" hostname
    And the new URL is propagated to all consumers

  # -------------------------------------------------------------------
  # DNS mode tunnel
  # -------------------------------------------------------------------

  Scenario: DNS mode tunnel with stable hostname
    Given I have run "obol tunnel login --hostname stack.example.com"
    When I run "obol stack up"
    Then the tunnel is automatically started
    And the tunnel URL is "https://stack.example.com"
    And the URL persists across restarts

  Scenario: DNS tunnel state is persisted
    Given a DNS tunnel is provisioned with hostname "stack.example.com"
    When I inspect "$OBOL_CONFIG_DIR/tunnel/cloudflared.json"
    Then the state contains:
      | field     | value               |
      | mode      | dns                 |
      | hostname  | stack.example.com   |

  # -------------------------------------------------------------------
  # URL propagation
  # -------------------------------------------------------------------

  Scenario: Tunnel URL propagated to agent AGENT_BASE_URL
    Given the tunnel is active with URL "https://stack.example.com"
    When the tunnel URL is propagated
    Then the OpenClaw Deployment in "openclaw-obol-agent" namespace has env var "AGENT_BASE_URL" set to "https://stack.example.com"

  Scenario: Tunnel URL propagated to frontend ConfigMap
    Given the tunnel is active with URL "https://stack.example.com"
    When the tunnel URL is propagated
    Then the "obol-stack-config" ConfigMap in "obol-frontend" namespace contains the tunnel URL

  Scenario: Tunnel URL propagated to storefront
    Given the tunnel is active with URL "https://stack.example.com"
    When the tunnel URL is propagated
    Then the storefront resources are created in the "traefik" namespace

  # -------------------------------------------------------------------
  # Internal services NOT accessible via tunnel
  # -------------------------------------------------------------------

  Scenario: Frontend is not accessible via tunnel hostname
    Given the tunnel is active
    When a request arrives via the tunnel hostname for path "/"
    Then the request is routed to the storefront landing page
    And the frontend application is NOT served
    Because the frontend HTTPRoute has hostnames restricted to "obol.stack"

  Scenario: eRPC is not accessible via tunnel hostname
    Given the tunnel is active
    When a request arrives via the tunnel hostname for path "/rpc"
    Then the request does NOT reach the eRPC gateway
    Because the eRPC HTTPRoute has hostnames restricted to "obol.stack"

  Scenario: LiteLLM admin is not exposed via any route
    Given the tunnel is active
    When a request arrives via the tunnel hostname for any path
    Then LiteLLM admin endpoints are never reachable
    Because no HTTPRoute exists for LiteLLM without hostname restrictions

  Scenario: Prometheus monitoring is not accessible via tunnel
    Given the tunnel is active
    When a request arrives via the tunnel hostname for monitoring paths
    Then the monitoring endpoints are NOT reachable
    Because monitoring HTTPRoutes have hostnames restricted to "obol.stack"

  Scenario: Internal services remain accessible locally via obol.stack
    Given the tunnel is active
    When a request arrives with Host header "obol.stack" for path "/"
    Then the frontend application is served
    And when a request arrives with Host header "obol.stack" for path "/rpc"
    Then the eRPC gateway handles the request

  # -------------------------------------------------------------------
  # /services/* accessible and x402-gated via tunnel
  # -------------------------------------------------------------------

  Scenario: Public service route is accessible via tunnel with payment
    Given the tunnel is active
    And a ServiceOffer "myapi" is in "Ready" state
    When a request arrives via the tunnel hostname for path "/services/myapi/data" with valid payment
    Then the x402-verifier validates the payment
    And the request is forwarded to the upstream service
    And the upstream responds successfully

  Scenario: Public service route returns 402 without payment via tunnel
    Given the tunnel is active
    And a ServiceOffer "myapi" is in "Ready" state
    When a request arrives via the tunnel hostname for path "/services/myapi/data" without payment
    Then the x402-verifier returns HTTP 402 with PaymentRequirements

  # -------------------------------------------------------------------
  # Discovery endpoints via tunnel
  # -------------------------------------------------------------------

  Scenario: Agent registration JSON accessible via tunnel
    Given the tunnel is active
    And an ERC-8004 registration has been published
    When a request arrives via the tunnel hostname for "/.well-known/agent-registration.json"
    Then the response contains the AgentRegistration JSON
    And the JSON includes:
      | field        | type    |
      | type         | string  |
      | name         | string  |
      | x402Support  | true    |
      | active       | true    |
      | services     | array   |
      | registrations| array   |

  Scenario: Skill catalog accessible via tunnel
    Given the tunnel is active
    And a /skill.md route is published
    When a request arrives via the tunnel hostname for "/skill.md"
    Then the response contains the machine-readable service catalog

  # -------------------------------------------------------------------
  # Storefront landing page
  # -------------------------------------------------------------------

  Scenario: Storefront landing page served at tunnel root
    Given the tunnel is active with URL "https://stack.example.com"
    When a request arrives at "https://stack.example.com/"
    Then the storefront static HTML page is served
    And the storefront is served by the busybox httpd in the "traefik" namespace

  Scenario: Storefront resources are created correctly
    Given the tunnel is active
    When the storefront is deployed
    Then the following resources exist in the "traefik" namespace:
      | kind        | name               |
      | ConfigMap   | tunnel-storefront  |
      | Deployment  | tunnel-storefront  |
      | Service     | tunnel-storefront  |
      | HTTPRoute   | tunnel-storefront  |
    And the Deployment uses busybox httpd with 5m CPU and 8Mi RAM requests

  # -------------------------------------------------------------------
  # Tunnel management
  # -------------------------------------------------------------------

  Scenario: obol tunnel status shows tunnel state
    Given the quick tunnel is active with URL "https://abc123.trycloudflare.com"
    When I run "obol tunnel status"
    Then the output shows the tunnel mode as "quick"
    And the output shows the current tunnel URL

  Scenario: obol tunnel logs shows cloudflared output
    Given the tunnel is active
    When I run "obol tunnel logs"
    Then the output streams logs from the cloudflared pod
