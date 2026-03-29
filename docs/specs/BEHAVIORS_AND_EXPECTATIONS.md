# Obol Stack -- Behaviors and Expectations

**Version**: 1.0.0
**Status**: Living document
**Last Updated**: 2026-03-27

This document defines the behavioral contract for Obol Stack. Every behavior described here maps to one or more testable scenarios expressible as Gherkin. Cross-references point to [SPEC.md](SPEC.md) where the underlying system is defined. Existing BDD feature files live in [features/](features/) and `internal/x402/features/`.

---

## Table of Contents

1. [Introduction](#1-introduction)
2. [Desired Behaviors](#2-desired-behaviors)
3. [Undesired Behaviors](#3-undesired-behaviors)
4. [Edge Cases](#4-edge-cases)
5. [Performance Expectations](#5-performance-expectations)
6. [Guardrail Definitions](#6-guardrail-definitions)

---

## 1. Introduction

### 1.1 Purpose

This document is the behavioral specification for Obol Stack. It defines what the system should do, what it must not do, how it handles edge cases, and what performance it must achieve.

It serves as:
- A contract between the product and engineering teams
- The source of truth for BDD feature file scenarios
- A test oracle for integration and adversarial testing
- A guardrail reference that CI and code review can enforce

### 1.2 How to Read This Document

**Desired behaviors** (Section 2) follow this format:
- **Trigger**: What user action or system state initiates the behavior
- **Expected**: What the system should do
- **Rationale**: Why this behavior matters

**Undesired behaviors** (Section 3) add:
- **Risk**: What goes wrong if this behavior occurs

**Edge cases** (Section 4) describe unusual or boundary scenarios with expected handling.

**Cross-references** use the notation `SPEC SS X.Y` to reference sections in [SPEC.md](SPEC.md). For example, `SPEC SS 3.1` refers to Section 3.1 (Stack Lifecycle).

Every behavior in this document MUST be expressible as a Gherkin `Given / When / Then` scenario. Inline Gherkin examples are included for critical behaviors.

### 1.3 Relationship to SPEC.md

| This Document | SPEC.md |
|---------------|---------|
| Describes *what should happen* | Describes *how things are built* |
| Trigger / Expected / Rationale | Architecture, data model, APIs |
| Test-oriented (Gherkin-compatible) | Implementation-oriented |
| Guardrails are non-negotiable | Constraints are structural |

---

## 2. Desired Behaviors

### 2.1 Stack Lifecycle

> SPEC SS 3.1 -- Stack Lifecycle

#### B-2.1.1: Stack initialization generates a unique cluster identity

**Trigger**: Operator runs `obol stack init`.
**Expected**: The CLI generates a petname-based stack ID, resolves absolute paths for ConfigDir/DataDir/BinDir, writes the backend config file (`.stack-backend`), and copies embedded infrastructure defaults with template substitution (`{{OLLAMA_HOST}}`, `{{OLLAMA_HOST_IP}}`, `{{CLUSTER_ID}}`). The stack ID is persisted at `$OBOL_CONFIG_DIR/.stack-id`.
**Rationale**: Unique naming prevents namespace collisions between clusters. Absolute paths are required because Docker volume mounts reject relative paths.

```gherkin
Scenario: Stack init creates unique identity
  Given no stack has been initialized
  When the operator runs "obol stack init"
  Then a ".stack-id" file exists in the config directory
  And the stack ID is a two-word petname
  And a ".stack-backend" file exists with value "k3d"
  And the defaults directory contains rendered infrastructure templates
```

#### B-2.1.2: Stack init with --force preserves existing stack ID

**Trigger**: Operator runs `obol stack init --force` on an already-initialized stack.
**Expected**: The cluster config is regenerated, but the existing stack ID is preserved. The previous backend's cluster is destroyed before initializing the new one.
**Rationale**: Preserving the stack ID maintains continuity for data directories and PVC paths. Destroying the old backend prevents orphaned Docker containers.

#### B-2.1.3: Stack up deploys full infrastructure and auto-configures LLM

**Trigger**: Operator runs `obol stack up` after `obol stack init`.
**Expected**: The CLI creates the k3d/k3s cluster, exports the kubeconfig, runs `syncDefaults()` (helmfile sync for all infrastructure), auto-configures LiteLLM with detected Ollama models and cloud provider API keys (single restart), deploys the OpenClaw agent singleton, patches agent RBAC, and starts the DNS tunnel if provisioned. The stack is fully operational after completion.
**Rationale**: One command must bring the entire stack from zero to operational. Auto-configuration eliminates manual `obol model setup` for the common case.

```gherkin
Scenario: Stack up brings cluster to operational state
  Given the stack has been initialized
  When the operator runs "obol stack up"
  Then the k3d cluster is running
  And a kubeconfig file exists in the config directory
  And the Traefik gateway is accepting connections on port 80
  And LiteLLM is running in the "llm" namespace
  And the x402-verifier is running in the "x402" namespace
  And the OpenClaw agent is running in the "openclaw-obol-agent" namespace
```

#### B-2.1.4: Stack down preserves config and data

**Trigger**: Operator runs `obol stack down`.
**Expected**: The cluster is stopped. Config directory, data directory, and all PVCs are preserved. The DNS resolver is stopped.
**Rationale**: Operators expect to stop and restart without losing state. PVC data (wallets, skills, blockchain data) is valuable.

#### B-2.1.5: Stack purge removes cluster and config

**Trigger**: Operator runs `obol stack purge`.
**Expected**: The cluster is destroyed and the config directory is removed. Root-owned PVC data in the data directory is NOT removed unless `--force` is passed (which invokes sudo).
**Rationale**: Root-owned local-path-provisioner directories cannot be removed by regular users. The `--force` flag makes the destructive scope explicit.

#### B-2.1.6: Helmfile sync failure triggers automatic cleanup

**Trigger**: `helmfile sync` fails during `obol stack up`.
**Expected**: The cluster is automatically stopped via `Down()`. The operator receives a clear error message and can fix the issue before retrying.
**Rationale**: A partially deployed cluster is worse than no cluster. Auto-cleanup prevents orphaned resources.

---

### 2.2 LLM Routing

> SPEC SS 3.2 -- LLM Routing

#### B-2.2.1: Auto-configuration detects Ollama models

**Trigger**: `obol stack up` runs `autoConfigureLLM()` and Ollama is running on the host.
**Expected**: The CLI queries `http://localhost:11434/api/tags`, discovers available models, adds them to the `litellm-config` ConfigMap as `ollama/<model>` entries pointing at `http://ollama.llm.svc:11434`, and restarts LiteLLM exactly once.
**Rationale**: Agent chat must work immediately after stack up without manual model configuration.

```gherkin
Scenario: LLM auto-configuration detects Ollama models
  Given Ollama is running with models "qwen3.5:9b" and "llama3.2:3b"
  When the operator runs "obol stack up"
  Then the litellm-config ConfigMap contains an entry for "qwen3.5:9b"
  And the litellm-config ConfigMap contains an entry for "llama3.2:3b"
  And LiteLLM was restarted exactly once
```

#### B-2.2.2: Auto-configuration detects cloud provider API keys

**Trigger**: `obol stack up` runs `autoConfigureLLM()` and `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` is set in the environment.
**Expected**: The detected provider is added to `litellm-config` as a wildcard entry (e.g., `anthropic/*`) and the API key is stored in `litellm-secrets`. LiteLLM restart is batched with Ollama model configuration (single restart).
**Rationale**: Cloud providers should be available to the agent without manual setup when keys are present.

#### B-2.2.3: Paid inference routes through x402-buyer sidecar

**Trigger**: A request for model `paid/<remote-model>` arrives at LiteLLM.
**Expected**: LiteLLM matches the `paid/*` catch-all entry and proxies to `http://127.0.0.1:8402/v1`. The x402-buyer sidecar handles payment attachment and upstream routing.
**Rationale**: The static `paid/*` route means no LiteLLM fork or dynamic config is needed for buy-side payments. The sidecar pattern keeps payment logic isolated.

#### B-2.2.4: Manual model setup validates before adding

**Trigger**: Operator runs `obol model setup custom --name foo --endpoint http://example.com --model bar`.
**Expected**: The CLI validates the endpoint is reachable and the model exists before adding it to LiteLLM config.
**Rationale**: Prevents broken routes in LiteLLM that would cause silent inference failures.

---

### 2.3 Network Management

> SPEC SS 3.3 -- Network / RPC Gateway

#### B-2.3.1: Adding a chain by ID fetches public RPCs

**Trigger**: Operator runs `obol network add <chain-id>`.
**Expected**: The CLI queries the ChainList API for public RPC endpoints for the given chain ID, adds them as upstreams in the `erpc-config` ConfigMap, registers the network, and restarts eRPC. Write methods (`eth_sendRawTransaction`) are blocked by default.
**Rationale**: ChainList provides curated public endpoints. Blocking writes by default prevents accidental mainnet transactions.

```gherkin
Scenario: Adding a chain blocks write methods by default
  Given the stack is running
  When the operator runs "obol network add 1"
  Then the erpc-config contains upstreams for chain ID 1
  And write methods are blocked on all upstreams for chain ID 1
```

#### B-2.3.2: Adding a chain with --allow-writes enables write methods

**Trigger**: Operator runs `obol network add <chain-id> --allow-writes`.
**Expected**: Write methods are allowed on the configured upstreams for that chain.
**Rationale**: Some use cases (transaction submission, contract deployment) require write access. The flag makes this an explicit opt-in.

#### B-2.3.3: Local Ethereum nodes register as priority upstreams with writes blocked

**Trigger**: `obol network install ethereum` deploys a local execution layer node.
**Expected**: The local node is registered in eRPC as a priority upstream via `RegisterERPCUpstream()`, but write methods are blocked on the local upstream. Write requests are routed to remote upstreams instead.
**Rationale**: Local nodes provide low-latency reads. Writes to a local-only node would not propagate to the real network.

#### B-2.3.4: Removing a chain cleans up all upstreams

**Trigger**: Operator runs `obol network remove <chain-id>`.
**Expected**: All upstreams for that chain ID are removed from `erpc-config`. The network entry is also removed. eRPC is restarted.
**Rationale**: Clean removal prevents stale routing entries.

---

### 2.4 Sell-Side Monetization

> SPEC SS 3.4 -- Monetize: Sell Side

#### B-2.4.1: Selling an HTTP service creates a ServiceOffer CR

**Trigger**: Operator runs `obol sell http <name> --wallet 0x... --chain base-sepolia --price 0.001 --upstream <svc> --port <port> --namespace <ns>`.
**Expected**: A `ServiceOffer` CR is created in the `openclaw-obol-agent` namespace with the specified payment terms, upstream reference, and path (`/services/<name>`). The CLI also calls `EnsureTunnelForSell()` to activate the tunnel if dormant.
**Rationale**: The ServiceOffer CRD is the declarative API. The tunnel must be active for public access.

```gherkin
Scenario: Operator sells HTTP service via CLI
  Given the stack is running
  When the operator runs "obol sell http myapi --wallet 0xABC --chain base-sepolia --price 0.001 --upstream litellm --port 4000 --namespace llm"
  Then a ServiceOffer "myapi" exists in namespace "openclaw-obol-agent"
  And the ServiceOffer has payment.payTo "0xABC"
  And the ServiceOffer has payment.network "base-sepolia"
  And the ServiceOffer has path "/services/myapi"
```

#### B-2.4.2: Agent reconciles ServiceOffer through 6 stages

**Trigger**: A `ServiceOffer` CR exists and the `monetize.py` reconciler is running.
**Expected**: The reconciler watches for ServiceOffer CRs and progresses them through 6 stages (every 10 seconds):

1. **ModelReady** -- Model availability verified (inference type) or skipped (HTTP type).
2. **UpstreamHealthy** -- Health check passes against `upstream.healthPath`.
3. **PaymentGateReady** -- Traefik `Middleware` (ForwardAuth) and pricing route in `x402-pricing` ConfigMap are created.
4. **RoutePublished** -- `HTTPRoute` created routing `/services/<name>/*` through the ForwardAuth middleware to the upstream.
5. **Registered** -- ERC-8004 on-chain registration and `/.well-known/agent-registration.json` published.
6. **Ready** -- All conditions met, endpoint URL set in status.

All created resources have `ownerReferences` pointing to the ServiceOffer for automatic garbage collection.
**Rationale**: The 6-stage reconciliation provides observability into the sell-side pipeline. OwnerReferences ensure clean deletion.

```gherkin
Scenario: Agent reconciles ServiceOffer to Ready
  Given a ServiceOffer "myapi" exists
  When the agent reconciles the ServiceOffer
  Then the ServiceOffer status condition "ModelReady" is "True"
  And the ServiceOffer status condition "UpstreamHealthy" is "True"
  And the ServiceOffer status condition "PaymentGateReady" is "True"
  And the ServiceOffer status condition "RoutePublished" is "True"
  And the ServiceOffer status condition "Registered" is "True"
  And the ServiceOffer status condition "Ready" is "True"
  And a Middleware "x402-myapi" exists in the offer namespace
  And an HTTPRoute "so-myapi" exists in the offer namespace
```

#### B-2.4.3: x402-verifier returns 402 for unpaid requests to priced routes

**Trigger**: An HTTP request arrives at a path matching a pricing route in `x402-pricing` ConfigMap, without an `X-PAYMENT` header.
**Expected**: Traefik forwards the request to the x402-verifier via ForwardAuth. The verifier matches the `X-Forwarded-Uri` against configured routes (first match wins), finds a price, and returns HTTP 402 with a `PaymentRequirements` JSON body containing `x402Version`, `accepts` array (scheme, network, maxAmountRequired, resource, asset, payTo, maxTimeoutSeconds).
**Rationale**: The 402 response tells clients exactly how to pay. This is the core x402 protocol handshake.

```gherkin
Scenario: Unpaid request returns 402 with pricing
  Given a priced route exists at "/services/myapi/*" with price "1000"
  When a client sends a POST to "/services/myapi/v1/chat/completions" without X-PAYMENT
  Then the response status is 402
  And the response body contains "x402Version" with value 1
  And the response body contains an "accepts" array with the route price
```

#### B-2.4.4: x402-verifier passes paid requests after facilitator verification

**Trigger**: An HTTP request with a valid `X-PAYMENT` header arrives at a priced route.
**Expected**: The verifier extracts the payment payload, delegates to the x402 facilitator for verification, and upon success returns 200 OK to Traefik (which then forwards to the upstream). The verifier optionally sets an `Authorization` header for upstream auth.
**Rationale**: Payment verification is delegated to the facilitator to avoid on-chain logic in the hot path.

#### B-2.4.5: x402-verifier passes unpriced routes without payment

**Trigger**: An HTTP request arrives at a path that does NOT match any pricing route.
**Expected**: The verifier returns 200 OK immediately (free route).
**Rationale**: Not all routes behind the ForwardAuth middleware require payment. Discovery endpoints and health checks should be freely accessible.

#### B-2.4.6: Pricing config hot-reloads without restart

**Trigger**: The `x402-pricing` ConfigMap is updated (e.g., new route added by reconciler).
**Expected**: `WatchConfig()` detects the file modification within 5 seconds (polling interval), parses the new config, and atomically swaps it via `Verifier.Reload()`. In-flight requests are not affected.
**Rationale**: Adding or removing services should not require verifier downtime. Atomic pointer swap ensures lock-free reads on the hot path.

#### B-2.4.7: Per-million-token pricing approximated as per-request in Phase 1

**Trigger**: Operator sets `--per-mtok 1.00` on `obol sell http`.
**Expected**: The effective per-request price is calculated as `perMTok / 1000` (using `ApproxTokensPerRequest = 1000`). Both `perMTok` and the computed `perRequest` (as `price`) are stored in the pricing route.
**Rationale**: Phase 1 does not have exact token metering. A fixed approximation of 1000 tokens per request provides a reasonable baseline.

#### B-2.4.8: Deleting a ServiceOffer cleans up all owned resources

**Trigger**: Operator runs `obol sell delete <name>`.
**Expected**: The ServiceOffer CR is deleted. All resources with ownerReferences (Middleware, HTTPRoute, pricing route ConfigMap entry, registration resources) are garbage-collected by Kubernetes.
**Rationale**: Clean deletion prevents orphaned routes and stale pricing entries.

---

### 2.5 Buy-Side Payments

> SPEC SS 3.5 -- Monetize: Buy Side

#### B-2.5.1: Buyer probe discovers pricing from 402 response

**Trigger**: Agent runs `buy.py probe` against a remote seller endpoint.
**Expected**: The probe sends an unpaid request, receives a 402 response, and extracts pricing information (payTo, price, network, asset) from the `PaymentRequirements` body.
**Rationale**: Discovery-driven purchasing lets agents find and pay for services without hardcoded pricing.

```gherkin
Scenario: Buyer discovers pricing via probe
  Given a remote seller is serving at "https://seller.example.com/services/qwen"
  When the agent runs "buy.py probe" against the seller endpoint
  Then the probe returns 402 with pricing info
  And the pricing contains payTo, price, and network
```

#### B-2.5.2: Buyer pre-signs ERC-3009 authorizations into ConfigMaps

**Trigger**: Agent runs `buy.py buy` with a seller endpoint and count.
**Expected**: The agent pre-signs N `TransferWithAuthorization` (ERC-3009) vouchers using the wallet private key, stores them in the `x402-buyer-auths` ConfigMap, and configures the upstream in `x402-buyer-config` ConfigMap. The sidecar hot-reloads the new config.
**Rationale**: Pre-signing moves the expensive signing operation out of the hot path. Bounded pool size limits maximum financial exposure.

#### B-2.5.3: Paid inference through sidecar attaches payment on 402

**Trigger**: LiteLLM proxies a `paid/<model>` request to the x402-buyer sidecar, which forwards to the remote seller and receives a 402.
**Expected**: The sidecar intercepts the 402, pops one pre-signed authorization from the pool, constructs an `X-PAYMENT` header, and retries the request. The seller verifies payment and returns the inference result. The sidecar returns the result to LiteLLM, which returns it to the agent.
**Rationale**: Transparent payment attachment means the agent sees standard OpenAI API responses. The sidecar handles the full x402 handshake internally.

```gherkin
Scenario: Paid inference through sidecar
  Given the buyer has 5 pre-signed authorizations for "seller-qwen"
  When the agent requests model "paid/qwen3.5:9b"
  Then LiteLLM proxies to the x402-buyer sidecar
  And the sidecar sends an unpaid request to the seller
  And the seller returns 402
  And the sidecar pops one authorization and retries with X-PAYMENT
  And the seller returns 200 with inference content
  And the agent receives the inference result
  And the buyer has 4 remaining authorizations
```

#### B-2.5.4: Model resolution strips prefixes correctly

**Trigger**: A request for `paid/openai/qwen3.5:9b` arrives at the x402-buyer sidecar.
**Expected**: The sidecar strips `paid/` and `openai/` prefixes to resolve `qwen3.5:9b`, looks up the model in `modelRoutes`, and dispatches to the correct upstream handler.
**Rationale**: LiteLLM adds `openai/` when routing through the `paid/*` catch-all. The sidecar must handle both prefixed and bare model names.

#### B-2.5.5: Sidecar exposes status, health, and metrics endpoints

**Trigger**: Monitoring or liveness probes query the sidecar.
**Expected**: `/status` returns JSON with remaining/spent auth counts per upstream. `/healthz` returns 200 for liveness. `/metrics` returns Prometheus-format metrics scraped via `PodMonitor`.
**Rationale**: Observability into auth pool state is critical for operational awareness. Prometheus integration enables alerting on low pool levels.

---

### 2.6 Tunnel Management

> SPEC SS 3.7 -- Tunnel Management

#### B-2.6.1: Quick tunnel activates on first sell

**Trigger**: Operator runs `obol sell http` and no DNS tunnel is provisioned.
**Expected**: The quick tunnel mode activates, starting the cloudflared pod. A random `*.trycloudflare.com` URL is assigned. The URL is propagated to the OpenClaw agent (`AGENT_BASE_URL`), the frontend ConfigMap, and the storefront. The tunnel URL is ephemeral and changes on restart.
**Rationale**: Quick mode provides zero-configuration public access. Activation on first sell means the tunnel is dormant until needed, reducing attack surface.

```gherkin
Scenario: Quick tunnel activates on first sell
  Given the stack is running with no DNS tunnel provisioned
  And the tunnel is dormant
  When the operator runs "obol sell http myapi ..."
  Then the cloudflared pod is running in the "traefik" namespace
  And a quick tunnel URL is assigned
  And AGENT_BASE_URL is set on the OpenClaw Deployment
```

#### B-2.6.2: DNS tunnel persists across restarts

**Trigger**: Operator runs `obol tunnel login --hostname stack.example.com`, provisions the tunnel, and later restarts the stack.
**Expected**: After `obol stack up`, the DNS tunnel automatically starts with the same stable hostname. Tunnel state (mode, hostname, accountID, zoneID, tunnelID) is persisted at `$OBOL_CONFIG_DIR/tunnel/cloudflared.json`.
**Rationale**: Stable hostnames are required for on-chain ERC-8004 registration and consistent discovery URLs.

#### B-2.6.3: Tunnel URL propagation updates all consumers

**Trigger**: A tunnel becomes active (either quick or DNS).
**Expected**: The tunnel URL is propagated to 4 consumers:
1. OpenClaw agent `AGENT_BASE_URL` environment variable.
2. Frontend `obol-stack-config` ConfigMap in `obol-frontend` namespace.
3. Agent overlay Helmfile state values.
4. Storefront landing page content.

**Rationale**: Multiple components need the tunnel URL. Centralized propagation prevents URL drift.

#### B-2.6.4: Storefront deploys at tunnel hostname root

**Trigger**: Tunnel becomes active.
**Expected**: `CreateStorefront()` deploys 4 resources in the `traefik` namespace: a ConfigMap with HTML content, a busybox httpd Deployment (5m CPU, 8Mi RAM), a ClusterIP Service on port 8080, and an HTTPRoute for the tunnel hostname root (`/`).
**Rationale**: The storefront provides a human-readable landing page for visitors who navigate to the tunnel URL directly.

---

### 2.7 ERC-8004 Identity

> SPEC SS 3.8 -- ERC-8004 Identity

#### B-2.7.1: On-chain registration mints agent NFT

**Trigger**: The reconciler reaches stage 5 (Registered) for a ServiceOffer with `registration.enabled: true`, or the operator runs `obol sell register`.
**Expected**: The ERC-8004 client calls `Register(ctx, key, agentURI)` on the Identity Registry contract (Base Sepolia: `0xEA0fE4FCF9E3017a24d9Db6e0e39B552c8648B9D`), minting an NFT. The returned `agentId` (token ID) and `registrationTxHash` are stored in the ServiceOffer status.
**Rationale**: On-chain identity enables decentralized agent discovery and reputation.

```gherkin
Scenario: Agent registers on-chain via ERC-8004
  Given the ServiceOffer "myapi" is at stage "RoutePublished"
  And the wallet has sufficient Base Sepolia ETH for gas
  When the reconciler processes stage 5 (Registered)
  Then an agent NFT is minted on the Identity Registry
  And the ServiceOffer status has a non-empty "agentId"
  And the ServiceOffer status has a non-empty "registrationTxHash"
```

#### B-2.7.2: Registration document served at well-known endpoint

**Trigger**: A client fetches `/.well-known/agent-registration.json` via the tunnel.
**Expected**: The endpoint returns an `AgentRegistration` JSON document containing the agent name, description, services, x402Support (true), registrations (agentId + registry address), and supportedTrust array.
**Rationale**: The well-known endpoint is the standard ERC-8004 discovery mechanism.

#### B-2.7.3: Metadata update via SetAgentURI

**Trigger**: Agent metadata changes (e.g., new service added, description updated).
**Expected**: The ERC-8004 client calls `SetAgentURI(ctx, key, agentId, newURI)` to update the on-chain metadata pointer.
**Rationale**: On-chain metadata must stay current with the agent's actual capabilities.

---

### 2.8 Security

> SPEC SS 7 -- Security Model

#### B-2.8.1: Local-only routes restricted by hostname

**Trigger**: Any HTTPRoute for an internal service (frontend, eRPC, monitoring).
**Expected**: The HTTPRoute has `hostnames: ["obol.stack"]`, ensuring the route only matches requests with `Host: obol.stack`. Requests arriving via the tunnel (with the tunnel hostname) do not match.
**Rationale**: Internal services contain sensitive data (blockchain RPCs, inference admin, Prometheus metrics) and must not be reachable from the public internet.

```gherkin
Scenario: Frontend is not accessible via tunnel
  Given the tunnel is active with hostname "stack.example.com"
  When a client sends GET "/" with Host "stack.example.com"
  Then the response is the storefront landing page
  And the response is NOT the frontend dashboard

Scenario: Frontend is accessible locally
  When a client sends GET "/" with Host "obol.stack"
  Then the response is the frontend dashboard
```

#### B-2.8.2: RBAC binding patched by agent init

**Trigger**: `obol agent init` runs during `obol stack up`.
**Expected**: `patchMonetizeBinding()` ensures the `openclaw-monetize-binding` ClusterRoleBinding has the `openclaw` ServiceAccount in `openclaw-obol-agent` namespace as a subject. This grants the agent CRUD access to ServiceOffers, Middlewares, HTTPRoutes, ConfigMaps, Services, and Deployments.
**Rationale**: The agent needs these permissions for the 6-stage reconciliation. Patching at init time handles the race condition where the binding may exist with empty subjects.

---

## 3. Undesired Behaviors

### 3.1 Security Violations

#### U-3.1.1: Internal services exposed via tunnel (CRITICAL)

**Trigger**: An HTTPRoute for the frontend, eRPC, LiteLLM admin, or monitoring is created without `hostnames: ["obol.stack"]` restriction.
**Expected**: This MUST NOT happen. All internal service HTTPRoutes MUST include the hostname restriction. Code review and CI must reject any change that removes hostname restrictions from internal routes.
**Risk**: Exposing the frontend exposes cluster management. Exposing eRPC exposes blockchain RPCs (potentially including write-enabled chains). Exposing LiteLLM admin exposes inference configuration and API keys. Exposing Prometheus exposes internal metrics and potentially secrets. This is the highest-severity security violation in the system.

```gherkin
Scenario: Internal HTTPRoutes must have hostname restrictions
  Given the stack is running
  When I inspect the HTTPRoute for the frontend
  Then it has hostnames containing "obol.stack"
  When I inspect the HTTPRoute for eRPC
  Then it has hostnames containing "obol.stack"
```

### 3.2 LLM Configuration

#### U-3.2.1: Model without tool support assigned to agent

**Trigger**: A model that does not support function/tool calling is configured as the agent's primary model.
**Expected**: The system should warn the operator that the model lacks tool support, as the OpenClaw agent relies on tool calling for skill execution and infrastructure management.
**Risk**: The agent silently fails to use skills, producing degraded responses with no indication of the root cause.

#### U-3.2.2: drop_params silently strips tool definitions

**Trigger**: LiteLLM's `drop_params` setting is enabled (default in some configs) and the model does not natively support tool parameters.
**Expected**: The system should NOT silently strip tool definitions. If a model does not support tools, the error should surface rather than being hidden.
**Risk**: Tool calls appear to succeed but the model never sees the tool definitions, leading to non-functional agent behavior that is extremely difficult to diagnose.

### 3.3 Infrastructure Drift

#### U-3.3.1: Kubeconfig port drift after restart

**Trigger**: The k3d cluster is restarted and the API server is assigned a different port.
**Expected**: The kubeconfig should be refreshed during `obol stack up`. If the port drifts and the kubeconfig is stale, all kubectl operations fail.
**Risk**: All CLI commands that interact with the cluster fail with connection errors. The fix is `k3d kubeconfig write <name> -o $CONFIG_DIR/kubeconfig.yaml --overwrite`, but the operator may not know this.

#### U-3.3.2: RBAC binding empty subjects race condition

**Trigger**: `obol agent init` runs before k3s has fully applied the `openclaw-monetize-binding` ClusterRoleBinding manifest.
**Expected**: The `patchMonetizeBinding()` function should handle this by creating or patching the binding. If the race occurs and is not handled, the agent has no permissions.
**Risk**: The 6-stage reconciliation silently fails on all stages that require Kubernetes API access (stages 3-6). The ServiceOffer status shows unhelpful error messages.

### 3.4 Caching and Staleness

#### U-3.4.1: eRPC cache staleness for balance queries

**Trigger**: A paid request settles on-chain, and `buy.py balance` is called within 10 seconds.
**Expected**: The balance query may return a stale value because eRPC caches `eth_call` results for 10 seconds (unfinalized block TTL).
**Risk**: The agent or operator sees an incorrect USDC balance. This is cosmetic (the on-chain state is correct) but confusing. Operators should be aware of the ~10-second lag.

---

## 4. Edge Cases

### 4.1 Infrastructure Dependencies

#### E-4.1.1: No Ollama running during stack up

**Scenario**: The operator runs `obol stack up` but Ollama is not installed or not running on the host.
**Expected Handling**: `autoConfigureLLM()` fails to reach `http://localhost:11434/api/tags`. The failure is non-fatal: a warning is printed, and LiteLLM starts without local model entries. The operator can install/start Ollama later and run `obol model setup` manually.
**Rationale**: Ollama is not a hard dependency. Cloud-only configurations are valid. The stack must be operational even without local inference.

```gherkin
Scenario: Stack up without Ollama
  Given Ollama is not running
  When the operator runs "obol stack up"
  Then a warning is printed about Ollama not being available
  And LiteLLM is running with no Ollama model entries
  And the stack is otherwise fully operational
```

#### E-4.1.2: No cloud provider API keys available

**Scenario**: Neither `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, nor `OPENAI_API_KEY` is set in the environment.
**Expected Handling**: `autoConfigureLLM()` prints a warning for each missing provider. LiteLLM starts with only Ollama models (if available) or an empty model list. The operator can add keys later via `obol model setup`.
**Rationale**: Cloud API keys are not required. Local-only inference with Ollama is a valid configuration.

### 4.2 Blockchain Operations

#### E-4.2.1: Wallet lacks Base Sepolia ETH for registration

**Scenario**: The reconciler reaches stage 5 (Registered) but the agent wallet has insufficient ETH to pay gas for the ERC-8004 registration transaction.
**Expected Handling**: The `Register()` call fails with a transaction error. The reconciler logs the error, sets the `Registered` condition to `False` with a message indicating insufficient gas, and retries on the next loop (10 seconds). The ServiceOffer remains at stage 4 (RoutePublished) -- the service is functional but not registered on-chain.
**Rationale**: Gas availability is an external dependency. The service should still work (stages 1-4 are complete) even if on-chain registration is pending.

#### E-4.2.2: All discovery backends unavailable

**Scenario**: The x402 facilitator, ChainList API, and blockchain RPC endpoints are all unreachable.
**Expected Handling**: Each subsystem degrades independently:
- Facilitator unavailable: x402-verifier cannot verify payments, returns 500. Existing free routes still work.
- ChainList unavailable: `obol network add <chain-id>` fails with an error. Custom endpoints (`--endpoint`) still work.
- RPC unavailable: eRPC returns errors for blockchain queries. Local nodes (if deployed) still serve reads.

**Rationale**: External service failures should not cascade. Each failure is isolated to its subsystem.

### 4.3 Timing and Propagation

#### E-4.3.1: ConfigMap propagation delay (60-120 seconds)

**Scenario**: The reconciler updates the `x402-pricing` ConfigMap with a new route, but the x402-verifier does not see the change for up to 120 seconds.
**Expected Handling**: The k3d file watcher takes 60-120 seconds to propagate ConfigMap changes to mounted volumes. The verifier's `WatchConfig()` polls every 5 seconds for file modification time changes. Net worst-case delay: ~125 seconds from ConfigMap update to verifier reload. During this window, requests to the new route will pass through unpriced (free).
**Rationale**: This is a known k3d limitation. The window is short and the failure mode is permissive (free access, not blocked access). For immediate effect, the operator can force a pod restart.

```gherkin
Scenario: Pricing route available after propagation delay
  Given the x402-verifier is running
  When a new pricing route is added to the x402-pricing ConfigMap
  Then within 125 seconds the verifier serves the new pricing route
  And requests to the route return 402 until payment is provided
```

#### E-4.3.2: ExternalName services with Traefik Gateway API

**Scenario**: An operator creates an ExternalName Service expecting it to work as a Traefik upstream via Gateway API HTTPRoutes.
**Expected Handling**: ExternalName services do NOT work with Traefik Gateway API. The operator must use a ClusterIP Service with manually managed Endpoints instead.
**Rationale**: This is a known Traefik limitation. The `obol sell http` command creates ClusterIP Services to avoid this issue.

### 4.4 Payment Edge Cases

#### E-4.4.1: Pre-signed auth pool exhausted

**Scenario**: All pre-signed ERC-3009 authorizations for a given upstream have been consumed.
**Expected Handling**: The `PreSignedSigner.Sign()` call returns an error. The x402-buyer sidecar returns a 404 for the model, indicating no purchased upstream is available. The agent must run `buy.py` to pre-sign additional authorizations.
**Rationale**: Bounded pool size is a security feature (maximum loss = N * price). Exhaustion is an expected operational event, not an error.

```gherkin
Scenario: Auth pool exhaustion returns 404
  Given the buyer has 1 remaining authorization for "seller-qwen"
  When the agent makes a paid request that consumes the last authorization
  Then the request succeeds
  When the agent makes another paid request
  Then the sidecar returns 404 for the model
  And the /status endpoint shows 0 remaining for "seller-qwen"
```

#### E-4.4.2: Quick tunnel URL changes after restart

**Scenario**: The cluster is restarted (`obol stack down` then `obol stack up`) while in quick tunnel mode.
**Expected Handling**: The quick tunnel gets a new random `*.trycloudflare.com` URL. All URL consumers are re-propagated with the new URL. Any previous ERC-8004 registration with the old URL becomes stale.
**Rationale**: Quick mode is explicitly ephemeral. Operators needing stable URLs should use DNS mode (`obol tunnel login --hostname`).

---

## 5. Performance Expectations

| Behavior | Target | Measurement | Degradation Handling |
|----------|--------|-------------|---------------------|
| x402 ForwardAuth verify (no payment) | < 5ms | Time from ForwardAuth request to 402 response (local) | Lock-free `atomic.Pointer` config reads; pre-resolved chain map |
| x402 ForwardAuth verify (with payment) | < 600ms | Includes facilitator round-trip (100-500ms network) | Facilitator timeout; returns 500 on timeout |
| x402-buyer auth pop | < 1ms | Single mutex lock + O(1) pool pop per `Sign()` call | Mutex contention only under extreme concurrency |
| Route matching (verifier) | < 1ms | First-match short-circuit; no regex per request | Degenerate case: many routes, all glob patterns |
| Buyer model routing | < 1ms | `sync.RWMutex` concurrent reads; rebuild only on `Reload()` | Write lock held briefly during config reload |
| Pricing config hot-reload | < 10s | Poll interval (5s) + parse + atomic swap | Worst case: 5s poll + parse time; old config serves during swap |
| ConfigMap propagation (k3d) | 60-120s | k3d file watcher interval | Force pod restart for immediate effect |
| Quick tunnel URL availability | 10-20s | Time from pod start to URL assignment | Cloudflare registration latency; retry on failure |
| Helmfile sync (initial) | 2-5 min | Full infrastructure deployment | Progress reported via Helmfile output |
| LiteLLM restart | 10-30s | Pod termination + startup | In-flight requests may fail during restart window |
| `obol stack up` (cold start) | 3-7 min | Cluster creation + helmfile sync + auto-config | Depends on Docker image cache state |

---

## 6. Guardrail Definitions

### 6.1 Network Security

| Guardrail | Rule | Enforcement | Violation Response |
|-----------|------|-------------|-------------------|
| Hostname restrictions on internal HTTPRoutes | Frontend, eRPC, monitoring, and LiteLLM admin HTTPRoutes MUST have `hostnames: ["obol.stack"]` | Code review; embedded template validation; `embed_crd_test.go` | Block PR merge; revert if deployed |
| Public routes limited to safe endpoints | Only `/services/*`, `/.well-known/*`, `/skill.md`, and `/` (storefront) may lack hostname restrictions | Template review; BDD integration tests | Block PR merge |
| Facilitator URL must use HTTPS | `ValidateFacilitatorURL()` rejects non-HTTPS facilitator URLs (loopback exempted for testing) | Runtime validation in CLI | CLI returns error; operation aborted |

### 6.2 Payment Security

| Guardrail | Rule | Enforcement | Violation Response |
|-----------|------|-------------|-------------------|
| x402 payment verification before resource access | Every request to `/services/*` MUST pass through x402 ForwardAuth | Traefik Middleware with ForwardAuth; reconciler creates Middleware in stage 3 | Middleware missing = route not published (stage 4 blocks) |
| Bounded spending on buyer sidecar | Maximum financial exposure = N * price, where N = pre-signed auth count | Finite pool in `PreSignedSigner`; no signer access in sidecar | Pool exhaustion returns 404; no additional spending possible |
| Replay protection | Every ERC-3009 authorization uses a unique random 32-byte nonce | `StateStore` tracks consumed nonces; `crypto/rand` for generation | Double-spend attempt rejected by contract |
| Zero signer access in buyer sidecar | The x402-buyer sidecar MUST NOT have access to any private key | Architecture: sidecar receives only pre-signed vouchers via ConfigMap | No private key mounted, injected, or accessible |

### 6.3 Configuration Integrity

| Guardrail | Rule | Enforcement | Violation Response |
|-----------|------|-------------|-------------------|
| KUBECONFIG auto-set for all K8s tools | `obol kubectl`, `obol helm`, `obol helmfile`, `obol k9s` MUST set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml` | CLI passthrough implementation sets env before exec | Without this, tools use default kubeconfig and target wrong cluster |
| OpenClaw version pinning consistency | Version in `OPENCLAW_VERSION` file, `openclawImageTag` Go const, and `obolup.sh` MUST agree | `TestOpenClawVersionConsistency` unit test | Test failure blocks CI |
| Two-stage templating separation | Stage 1 (CLI flags to Go templates) and Stage 2 (Helmfile to K8s) MUST NOT be mixed | Code review; template structure in `internal/embed/networks/` | Mixing stages causes unpredictable template rendering |
| Absolute paths in Docker volume mounts | All paths passed to Docker/k3d MUST be absolute | Resolved at `obol stack init` time; `config.Config` stores absolute paths | Relative paths cause Docker mount failures |

### 6.4 Data Safety

| Guardrail | Rule | Enforcement | Violation Response |
|-----------|------|-------------|-------------------|
| Wallet backup before purge | `PromptBackupBeforePurge()` MUST run before `obol stack purge` when wallets exist | CLI implementation checks for keystore files | Operator prompted to backup; can force with flag |
| Config hot-reload preserves previous on error | If parsing a new config file fails, the verifier/buyer MUST keep the previous valid config | Error handling in `WatchConfig()` and `Reload()` | Log error; continue serving with old config |
| OwnerReferences on reconciler-created resources | All Kubernetes resources created by `monetize.py` MUST have ownerReferences pointing to the ServiceOffer | Reconciler implementation sets ownerReferences on every create | Missing ownerReferences cause resource leaks on ServiceOffer deletion |
| Backend switching destroys old cluster | Changing from k3d to k3s (or vice versa) via `obol stack init --backend` MUST destroy the old backend first | `Init()` checks `.stack-backend` and calls `Destroy()` on mismatch | Orphaned Docker containers or k3s processes |
