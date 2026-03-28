# Obol Stack -- Architecture Document

> **Version:** 1.0.0
> **Date:** 2026-03-27
> **Companion to:** [SPEC.md](./SPEC.md)
> **Audience:** Seasoned developers, agentic workflows, and system integrators.

---

## Table of Contents

1. [Design Philosophy](#1-design-philosophy)
2. [C4 Diagrams](#2-c4-diagrams)
3. [Module Decomposition](#3-module-decomposition)
4. [Data Flow Diagrams](#4-data-flow-diagrams)
5. [Storage Architecture](#5-storage-architecture)
6. [Deployment Model](#6-deployment-model)
7. [Network Topology](#7-network-topology)
8. [Security Architecture](#8-security-architecture)

---

## 1. Design Philosophy

Five guiding principles govern every architectural decision in Obol Stack. They are listed in order of precedence -- when principles conflict, the higher-numbered principle yields to the lower.

### 1.1 Local-First Sovereignty

The operator's machine is the source of truth. All infrastructure runs inside a local k3d/k3s cluster, all state lives on the local filesystem under XDG-compliant paths, and no cloud account is required to start. Public exposure (Cloudflare tunnels) is opt-in and layered on top, never a prerequisite. This ensures the operator retains full custody of keys, models, and data at all times.

*SPEC cross-ref: Section 1.3 (System Constraints), Section 2.3 (Configuration Hierarchy).*

### 1.2 Configuration-Driven Infrastructure

Infrastructure is declared, not scripted. Two-stage templating (CLI flags to Go templates to Helmfile to Kubernetes manifests) ensures that every deployed resource traces back to a versioned configuration file. Embedded assets (`internal/embed/`) ship default configurations; operators override via flags or values files. Helmfile is the single deployment orchestrator -- there are no imperative `kubectl apply` calls in the steady-state path.

*SPEC cross-ref: Section 3.3.3 (Two-Stage Templating), Section 2.1 (High-Level Overview).*

### 1.3 Payment-Gated by Default

Every publicly exposed service is protected by x402 micropayments unless explicitly exempted. The ForwardAuth pattern means Traefik itself enforces payment before traffic ever reaches the upstream. This is not an afterthought bolt-on -- the payment gate is a first-class infrastructure primitive deployed alongside the service via the ServiceOffer reconciliation loop.

*SPEC cross-ref: Section 3.4 (Monetize -- Sell Side), Section 4.1 (x402 Payment Protocol).*

### 1.4 Bounded Trust, Bounded Spending

The system minimizes trust surfaces at every layer. The buy-side sidecar has zero signer access; it can only spend pre-signed vouchers, bounding maximum loss to N * price. The sell-side verifier delegates to an external facilitator for settlement, never holding funds. Wallet private keys live in encrypted keystores or hardware enclaves, accessed only through a remote-signer REST API. RBAC scopes the agent to exactly the Kubernetes verbs it needs.

*SPEC cross-ref: Section 7.2 (Payment Security), Section 7.3 (Wallet Security), Section 7.5 (RBAC).*

### 1.5 Progressive Disclosure

A single `obol stack up` gives operators a working cluster with auto-configured LLM routing, an AI agent, and local blockchain access. Advanced features -- selling inference, buying remote models, on-chain registration, Secure Enclave keys -- are activated incrementally through explicit CLI commands. Failures in optional subsystems (Ollama down, no cloud API key, tunnel unavailable) degrade gracefully with warnings, never blocking the core startup path.

*SPEC cross-ref: Section 3.1.3 (Startup Sequence), Section 8.2 (Graceful Degradation).*

---

## 2. C4 Diagrams

### 2.1 Context Diagram (Level 1)

The system boundary is the operator's machine. External systems interact via well-defined protocols.

```mermaid
C4Context
    title Obol Stack -- System Context

    Person(operator, "Operator", "Manages cluster via obol CLI")
    Person(buyer, "Remote Buyer", "Purchases inference via x402")

    System(obol, "Obol Stack", "Local k3d/k3s cluster with AI agent,<br/>payment-gated inference, blockchain networks")

    System_Ext(cloudflare, "Cloudflare", "Tunnel service for public exposure")
    System_Ext(facilitator, "x402 Facilitator", "Payment verification and settlement<br/>(facilitator.x402.rs)")
    System_Ext(base, "Base L2", "ERC-8004 identity registry,<br/>USDC settlement (Base Sepolia / Mainnet)")
    System_Ext(ollama, "Ollama", "Local LLM inference engine<br/>(host process)")
    System_Ext(chainlist, "ChainList API", "Public RPC endpoint discovery")
    System_Ext(cloud_llm, "Cloud LLM Providers", "Anthropic, OpenAI APIs")

    Rel(operator, obol, "Manages via CLI")
    Rel(buyer, obol, "x402 payments over HTTPS")
    Rel(obol, cloudflare, "HTTPS/QUIC tunnel")
    Rel(obol, facilitator, "HTTPS POST /verify")
    Rel(obol, base, "JSON-RPC (contract calls)")
    Rel(obol, ollama, "HTTP /api/tags, /v1/*")
    Rel(obol, chainlist, "HTTPS GET")
    Rel(obol, cloud_llm, "HTTPS /v1/*")
```

### 2.2 Container Diagram (Level 2)

Inside the k3d cluster, containers are organized by namespace. Each namespace represents a deployment unit with distinct responsibilities.

```mermaid
C4Container
    title Obol Stack -- Container Diagram (k3d Cluster)

    Person(operator, "Operator")
    Person(buyer, "Remote Buyer")

    System_Boundary(cluster, "k3d / k3s Cluster") {

        Container(traefik, "Traefik Gateway", "Gateway API", "Ingress controller.<br/>Routes local and public traffic.<br/>ForwardAuth to x402-verifier.")
        Container(cloudflared, "cloudflared", "Cloudflare Tunnel", "Exposes public routes<br/>to the internet.")
        Container(storefront, "Storefront", "busybox httpd", "Static landing page<br/>at tunnel hostname root.")

        Container(litellm, "LiteLLM", "Python, port 4000", "OpenAI-compatible proxy.<br/>Routes to Ollama, cloud,<br/>or paid/* via sidecar.")
        Container(x402_buyer, "x402-buyer", "Go sidecar, port 8402", "Buy-side payment attachment.<br/>Pre-signed ERC-3009 auths.<br/>Runs in LiteLLM Pod.")

        Container(x402_verifier, "x402-verifier", "Go, port 8080", "ForwardAuth middleware.<br/>Route matching, 402 responses,<br/>facilitator delegation.")

        Container(agent, "OpenClaw Agent", "Python", "AI agent singleton.<br/>Skills via PVC injection.<br/>monetize.py reconciler.")
        Container(remote_signer, "Remote Signer", "REST API, port 9000", "Keystore-backed signing.<br/>In-namespace access only.")

        Container(erpc, "eRPC", "Go, port 4000", "Blockchain RPC gateway.<br/>Multiplexes upstreams,<br/>caches eth_call.")
        Container(frontend, "Frontend", "React, nginx", "Dashboard UI.<br/>Local-only (obol.stack).")
        Container(prometheus, "Prometheus", "Monitoring", "Metrics collection.<br/>ServiceMonitor + PodMonitor.")
    }

    System_Ext(ollama, "Ollama (Host)")
    System_Ext(facilitator, "x402 Facilitator")
    System_Ext(internet, "Public Internet")

    Rel(operator, traefik, "HTTP :80 / :443")
    Rel(internet, cloudflared, "HTTPS/QUIC")
    Rel(cloudflared, traefik, "HTTP")
    Rel(buyer, cloudflared, "HTTPS")

    Rel(traefik, x402_verifier, "ForwardAuth POST /verify")
    Rel(traefik, litellm, "/services/* (after 200)")
    Rel(traefik, frontend, "/ (obol.stack)")
    Rel(traefik, erpc, "/rpc (obol.stack)")
    Rel(traefik, storefront, "/ (tunnel hostname)")

    Rel(litellm, ollama, "ollama/* models")
    Rel(litellm, x402_buyer, "paid/* models :8402")
    Rel(x402_buyer, internet, "x402 payment + request")
    Rel(x402_verifier, facilitator, "POST /verify")

    Rel(agent, litellm, "Inference requests")
    Rel(agent, remote_signer, "Sign transactions :9000")
```

### 2.3 Component Diagram -- Monetize Subsystem (Level 3)

The monetize subsystem is the most architecturally complex part of Obol Stack. It spans the CLI, a Kubernetes CRD, a Python reconciler, Traefik middleware, and the x402-verifier.

```mermaid
C4Component
    title Monetize Subsystem -- Component Diagram

    Container_Boundary(cli_boundary, "obol CLI") {
        Component(sell_cmd, "sell.go", "urfave/cli", "Parses flags, validates input,<br/>creates ServiceOffer CR,<br/>triggers tunnel activation.")
        Component(schemas_pkg, "schemas/", "Go", "ServiceOffer struct definitions,<br/>payment validation,<br/>price approximation.")
    }

    Container_Boundary(agent_boundary, "OpenClaw Agent Pod") {
        Component(reconciler, "monetize.py", "Python", "6-stage reconciliation loop.<br/>Watches ServiceOffer CRs.<br/>Creates child resources with<br/>ownerReferences for GC.")
    }

    Container_Boundary(k8s_boundary, "Kubernetes API") {
        Component(serviceoffer_crd, "ServiceOffer CRD", "obol.org/v1alpha1", "Declarative sell-side API.<br/>Spec: type, model, upstream,<br/>payment, path, registration.")
        Component(middleware, "Traefik Middleware", "traefik.io", "ForwardAuth middleware<br/>pointing to x402-verifier.")
        Component(httproute, "HTTPRoute", "gateway.networking.k8s.io", "Public route:<br/>/services/<name>/*<br/>No hostname restriction.")
        Component(pricing_cm, "x402-pricing ConfigMap", "x402 ns", "Route rules: pattern,<br/>price, wallet, chain.")
        Component(registration, "Registration Resources", "traefik ns", "ConfigMap + httpd + HTTPRoute<br/>for /.well-known/ and /skill.md")
    }

    Container_Boundary(verifier_boundary, "x402-verifier") {
        Component(verifier_core, "Verifier", "Go", "ForwardAuth handler.<br/>Route matching, 402 generation,<br/>facilitator delegation.")
        Component(watcher, "WatchConfig", "Go", "Polls pricing YAML every 5s.<br/>Atomic config swap on change.")
        Component(matcher, "Matcher", "Go", "First-match route resolution.<br/>Exact, prefix, glob patterns.")
    }

    Rel(sell_cmd, serviceoffer_crd, "kubectl apply")
    Rel(sell_cmd, schemas_pkg, "Validate + build CR")
    Rel(reconciler, serviceoffer_crd, "Watch (10s loop)")
    Rel(reconciler, middleware, "Stage 3: Create")
    Rel(reconciler, pricing_cm, "Stage 3: Patch routes[]")
    Rel(reconciler, httproute, "Stage 4: Create")
    Rel(reconciler, registration, "Stage 5: Create")
    Rel(watcher, pricing_cm, "Poll mtime (5s)")
    Rel(watcher, verifier_core, "Atomic Reload()")
    Rel(verifier_core, matcher, "Match URI to route")
```

---

## 3. Module Decomposition

Every Go package, its purpose, key dependencies, and SPEC cross-references.

### 3.1 CLI Layer

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `cmd/obol` | CLI entry point and command definitions | `main.go`, `sell.go`, `network.go`, `openclaw.go`, `model.go`, `bootstrap.go`, `update.go` | `urfave/cli/v3`, all `internal/` packages | 4.2 |

### 3.2 Core Infrastructure

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/config` | XDG-compliant configuration resolution | `config.go` | (stdlib only) | 2.3 |
| `internal/stack` | Cluster lifecycle (init, up, down, purge) | `stack.go`, `backend.go`, `backend_k3d.go`, `backend_k3s.go` | `config`, `embed`, `model`, `openclaw`, `tunnel`, `agent`, `dns` | 3.1 |
| `internal/embed` | Embedded assets (infrastructure, networks, skills) | `embed.go` | `embed` (stdlib) | 2.1, 3.6.2 |
| `internal/kubectl` | Kubernetes API wrapper with auto-KUBECONFIG | `kubectl.go` | `config` | 2.3 |
| `internal/ui` | Terminal UI (spinners, prompts, branded output) | `ui.go`, `spinner.go`, `prompt.go`, `brand.go`, `errors.go`, `exec.go`, `output.go`, `suggest.go` | (stdlib, terminal libs) | 8.1 |
| `internal/version` | Build version information | `version.go` | (stdlib only) | -- |
| `internal/update` | Self-update and dependency management | `update.go`, `github.go`, `charts.go`, `hint.go` | `version` | -- |
| `internal/dns` | Local DNS resolver for `obol.stack` hostname | `resolver.go` | (stdlib only) | 3.7.5 |

### 3.3 LLM and Inference

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/model` | LiteLLM gateway configuration and provider management | `model.go` | `config`, `kubectl` | 3.2 |
| `internal/inference` | Standalone x402 inference gateway (bare metal / VM) | `gateway.go`, `container.go`, `store.go`, `client.go`, `enclave_middleware.go` | `enclave`, `tee`, `x402` | 3.9 |
| `internal/enclave` | Apple Secure Enclave key management (P-256, ECIES) | `enclave.go`, `enclave_darwin.go`, `enclave_stub.go`, `ecies.go`, `sysctl_darwin.go` | (CGo / Security.framework on macOS) | 3.9.6 |
| `internal/tee` | TEE attestation (TDX, SNP, Nitro) and key management | `tee.go`, `key.go`, `coco.go`, `verify.go`, `attest_*.go` | (platform-specific) | 3.9.6, 7.4 |

### 3.4 Monetize (Sell Side)

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/x402` | x402-verifier: ForwardAuth, route matching, config hot-reload | `verifier.go`, `config.go`, `matcher.go`, `watcher.go`, `setup.go`, `validate.go`, `metrics.go` | `x402-go` (library) | 3.4.4, 3.4.5 |
| `internal/schemas` | ServiceOffer CRD types, payment validation, pricing math | `serviceoffer.go`, `payment.go`, `registration.go` | (stdlib only) | 3.4.3, 5.3 |
| `internal/embed/skills/monetize/` | Python reconciler (`monetize.py`) for 6-stage ServiceOffer reconciliation | `monetize.py`, `SKILL.md` | Kubernetes Python client | 3.4.2 |

### 3.5 Monetize (Buy Side)

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/x402/buyer` | x402-buyer sidecar: reverse proxy, pre-signed auth pool, state tracking | `proxy.go`, `signer.go`, `config.go`, `state.go`, `metrics.go` | `x402-go` | 3.5 |
| `cmd/x402-buyer` | Sidecar binary entry point | `main.go` | `internal/x402/buyer` | 3.5 |
| `internal/embed/skills/buy-inference/` | Agent skill for discovery and purchasing remote inference | `SKILL.md`, `scripts/buy.py` | Python, Kubernetes client | 3.5.2 |

### 3.6 Identity and Blockchain

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/erc8004` | ERC-8004 Identity Registry client (register, metadata, URI) | `client.go`, `types.go`, `abi.go` | `go-ethereum` | 3.8 |
| `internal/network` | Blockchain RPC gateway management (eRPC, ChainList, local nodes) | `network.go`, `erpc.go`, `rpc.go`, `chainlist.go`, `resolve.go`, `parser.go` | `config`, `kubectl`, `embed` | 3.3 |

### 3.7 Agent and Tunnel

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/openclaw` | OpenClaw agent deployment, wallet generation, version management | `openclaw.go`, `wallet.go`, `resolve.go` | `config`, `embed`, `kubectl` | 3.6 |
| `internal/agent` | Agent RBAC patching and singleton management | `agent.go` | `kubectl` | 7.5 |
| `internal/tunnel` | Cloudflare tunnel lifecycle (quick/dns modes, storefront, URL propagation) | `tunnel.go`, `state.go`, `provision.go`, `cloudflare.go`, `login.go`, `agent.go`, `stackid.go` | `config`, `kubectl` | 3.7 |

### 3.8 Applications

| Module | Purpose | Key Files | Dependencies | SPEC Section |
|--------|---------|-----------|-------------|-------------|
| `internal/app` | Helm chart application management (install, sync, list, delete) | `app.go`, `chart.go`, `artifacthub.go`, `metadata.go`, `resolve.go` | `config`, `kubectl`, `embed` | 4.2 |

---

## 4. Data Flow Diagrams

### 4.1 Stack Initialization and Startup

This diagram traces the full lifecycle from `obol stack init` through `obol stack up` to a running cluster with all services operational.

```mermaid
sequenceDiagram
    participant Op as Operator
    participant CLI as obol CLI
    participant Cfg as internal/config
    participant Emb as internal/embed
    participant Back as Backend (k3d/k3s)
    participant HF as Helmfile
    participant LLM as autoConfigureLLM
    participant OC as OpenClaw Setup
    participant Ag as agent.Init
    participant Tun as Tunnel

    Note over Op,Tun: Phase 1: Initialization (obol stack init)

    Op->>CLI: obol stack init [--backend k3d]
    CLI->>Cfg: Resolve paths (XDG / env / dev mode)
    CLI->>CLI: Generate petname cluster ID
    CLI->>CLI: Persist .stack-id, .stack-backend
    CLI->>Back: Init(cfg, stackID)
    Note over Back: Generate k3d.yaml / k3s config<br/>Resolve Ollama host for backend
    CLI->>Emb: Copy infrastructure defaults to $CONFIG_DIR/defaults/
    Note over Emb: Template substitution:<br/>{{OLLAMA_HOST}}, {{OLLAMA_HOST_IP}}, {{CLUSTER_ID}}

    Note over Op,Tun: Phase 2: Cluster Startup (obol stack up)

    Op->>CLI: obol stack up
    CLI->>Back: Up(cfg, stackID)
    Back-->>CLI: kubeconfig bytes
    CLI->>CLI: Write $CONFIG_DIR/kubeconfig.yaml

    Note over Op,Tun: Phase 3: Infrastructure Deployment

    CLI->>HF: syncDefaults() -- helmfile sync
    Note over HF: Deploys in order:<br/>1. Traefik (GatewayClass + Gateway)<br/>2. eRPC<br/>3. LiteLLM + x402-buyer sidecar<br/>4. x402-verifier<br/>5. Monitoring (Prometheus)<br/>6. Frontend<br/>7. cloudflared<br/>8. ServiceOffer CRD + RBAC

    Note over Op,Tun: Phase 4: Auto-Configuration

    CLI->>LLM: autoConfigureLLM()
    LLM->>LLM: Query Ollama /api/tags (host)
    LLM->>LLM: Detect cloud API keys (env vars)
    LLM->>LLM: Read ~/.openclaw/openclaw.json (agent model)
    LLM->>LLM: Patch litellm-config ConfigMap
    LLM->>LLM: Patch litellm-secrets Secret
    LLM->>LLM: Single LiteLLM restart

    CLI->>OC: SetupDefault()
    Note over OC: Deploy singleton agent<br/>Inject skills PVC<br/>($DATA_DIR/openclaw-<id>/openclaw-data/)

    CLI->>Ag: agent.Init() -- patchMonetizeBinding()
    Note over Ag: Ensure ClusterRoleBinding subjects<br/>include openclaw SA

    Note over Op,Tun: Phase 5: Tunnel Activation

    CLI->>Tun: Check tunnel state ($CONFIG_DIR/tunnel/cloudflared.json)
    alt DNS tunnel provisioned (persistent hostname)
        CLI->>Tun: EnsureRunning()
        Tun->>Tun: Propagate URL to agent, frontend, storefront
    else Quick tunnel (default)
        Note over Tun: Dormant -- activates on first obol sell
    end

    CLI-->>Op: Stack ready
```

*SPEC cross-ref: Section 3.1.2 (Operations), Section 3.1.3 (Startup Sequence), Section 3.2.4 (Logic).*

### 4.2 Sell-Side: ServiceOffer Creation to Public Route

This traces the complete path from an operator running `obol sell http` through the 6-stage reconciliation loop to a publicly accessible, payment-gated route.

```mermaid
sequenceDiagram
    participant Op as Operator
    participant CLI as obol sell http
    participant Val as schemas/ (validation)
    participant K8s as Kubernetes API
    participant Tun as Tunnel
    participant Rec as monetize.py (Reconciler)
    participant Ver as x402-verifier
    participant TF as Traefik
    participant Reg as ERC-8004 Registry

    Op->>CLI: obol sell http myapi --wallet 0x... --chain base-sepolia --price 0.001 --upstream svc --port 8080 --namespace ns

    CLI->>Val: Validate chain, price, wallet, upstream
    Val-->>CLI: ServiceOffer struct

    CLI->>K8s: Create ServiceOffer CR (openclaw-obol-agent ns)
    CLI->>Tun: EnsureTunnelForSell()
    Note over Tun: Start quick tunnel if dormant<br/>or verify DNS tunnel running

    Note over Rec: Reconciliation loop runs every 10 seconds

    rect rgb(240, 248, 255)
        Note over Rec: Stage 1: ModelReady
        Rec->>K8s: Read ServiceOffer spec.model
        Rec->>Rec: Validate model exists (inference type)<br/>or skip (HTTP type)
        Rec->>K8s: Update condition ModelReady=True
    end

    rect rgb(240, 255, 240)
        Note over Rec: Stage 2: UpstreamHealthy
        Rec->>K8s: GET upstream.service:port/healthPath
        Rec->>K8s: Update condition UpstreamHealthy=True
    end

    rect rgb(255, 248, 240)
        Note over Rec: Stage 3: PaymentGateReady
        Rec->>K8s: Create Traefik Middleware (ForwardAuth -> x402-verifier)
        Rec->>K8s: Patch x402-pricing ConfigMap (add route rule)
        Note over Ver: WatchConfig detects mtime change (5s poll)
        Ver->>Ver: Atomic Reload() with new routes
        Rec->>K8s: Update condition PaymentGateReady=True
    end

    rect rgb(248, 240, 255)
        Note over Rec: Stage 4: RoutePublished
        Rec->>K8s: Create HTTPRoute /services/myapi/* (no hostname restriction)
        Note over TF: Route live: /services/myapi/* -> ForwardAuth -> upstream
        Rec->>K8s: Update condition RoutePublished=True
    end

    rect rgb(255, 255, 240)
        Note over Rec: Stage 5: Registered
        Rec->>K8s: Create registration ConfigMap (agent-registration.json)
        Rec->>K8s: Create httpd Deployment + Service
        Rec->>K8s: Create HTTPRoute for /.well-known/ and /skill.md
        Rec->>Reg: Mint ERC-8004 NFT (via remote-signer)
        Rec->>K8s: Update condition Registered=True, status.agentId
    end

    rect rgb(240, 255, 255)
        Note over Rec: Stage 6: Ready
        Rec->>K8s: Set status.endpoint = tunnel_url/services/myapi
        Rec->>K8s: Update condition Ready=True
    end

    Op->>Op: obol sell status myapi -> Ready
```

*SPEC cross-ref: Section 3.4.2 (Sell-Side Flow), Section 3.4.3 (ServiceOffer CRD), Section 3.4.4 (x402-verifier).*

### 4.3 Buy-Side: Discovery to Paid Inference

This traces the agent's journey from discovering a remote seller to making paid inference requests through the local LiteLLM gateway.

```mermaid
sequenceDiagram
    participant Agent as OpenClaw Agent
    participant Buy as buy.py
    participant Seller as Remote Seller
    participant K8s as Kubernetes API
    participant LiteLLM as LiteLLM :4000
    participant Sidecar as x402-buyer :8402

    Note over Agent,Sidecar: Phase 1: Discovery

    Agent->>Buy: buy.py probe <seller-url>
    Buy->>Seller: GET /services/<name>/v1/models
    Seller-->>Buy: 402 PaymentRequired + PaymentRequirements JSON
    Note over Buy: Extract: price, wallet, chain, asset,<br/>available models

    Buy-->>Agent: Probe results (price, models)

    Note over Agent,Sidecar: Phase 2: Purchase (Pre-sign Authorizations)

    Agent->>Buy: buy.py buy --seller <url> --model <model> --count N
    Buy->>Buy: Generate N random nonces (32 bytes each)
    Buy->>Buy: Sign N ERC-3009 TransferWithAuthorization<br/>(via remote-signer at :9000)
    Note over Buy: Each auth: {from, to, value, validAfter,<br/>validBefore, nonce, signature}

    Buy->>K8s: Create/Patch x402-buyer-config ConfigMap<br/>(upstream URL, model, chain, price)
    Buy->>K8s: Create/Patch x402-buyer-auths ConfigMap<br/>(array of pre-signed auths)

    Note over Sidecar: Config watcher detects change<br/>Mutex-guarded Reload() rebuilds handlers

    Note over Agent,Sidecar: Phase 3: Paid Inference

    Agent->>LiteLLM: POST /v1/chat/completions<br/>model: "paid/<model>"
    LiteLLM->>LiteLLM: Route paid/* -> openai/* -> :8402/v1
    LiteLLM->>Sidecar: POST /v1/chat/completions<br/>model: "<model>"

    Sidecar->>Sidecar: Resolve model -> upstream handler
    Sidecar->>Seller: POST /services/<name>/v1/chat/completions
    Seller-->>Sidecar: 402 PaymentRequired

    Sidecar->>Sidecar: Pop pre-signed auth from pool (mutex)
    Sidecar->>Sidecar: Build X-PAYMENT header (base64 PaymentPayload)
    Sidecar->>Seller: Retry with X-PAYMENT header
    Seller-->>Sidecar: 200 OK + inference response

    Sidecar->>Sidecar: Mark nonce consumed (onConsume callback)
    Sidecar-->>LiteLLM: 200 OK
    LiteLLM-->>Agent: Chat completion response
```

*SPEC cross-ref: Section 3.5.2 (Buy-Side Flow), Section 3.5.3 (Architecture), Section 3.5.5 (Model Resolution).*

### 4.4 Payment Flow: x402 Request Lifecycle

This is the canonical request-level flow for a client paying for access to a service. It shows the interplay between Traefik, the verifier, the facilitator, and the upstream.

```mermaid
sequenceDiagram
    participant Client as Client / Buyer
    participant TF as Traefik Gateway
    participant Ver as x402-verifier
    participant Match as Route Matcher
    participant Fac as x402 Facilitator
    participant Chain as Base L2 (USDC)
    participant Up as Upstream Service

    Client->>TF: GET /services/myapi/data

    TF->>Ver: POST /verify<br/>X-Forwarded-Uri: /services/myapi/data<br/>X-Forwarded-Method: GET

    Ver->>Match: Match("/services/myapi/data")
    Match-->>Ver: RouteRule{price: "1000", wallet: "0x...", chain: "base-sepolia"}

    alt No X-PAYMENT header
        Ver-->>TF: 402 Payment Required
        Note over Ver: Response body:<br/>{x402Version: 1, accepts: [{<br/>  scheme: "exact",<br/>  network: "eip155:84532",<br/>  maxAmountRequired: "1000",<br/>  payTo: "0x...",<br/>  asset: "0x036C..." (USDC)<br/>}]}
        TF-->>Client: 402 + PaymentRequirements

        Note over Client: Sign ERC-3009<br/>TransferWithAuthorization<br/>(EIP-712 typed data)

        Client->>TF: GET /services/myapi/data<br/>X-PAYMENT: base64(PaymentPayload)
        TF->>Ver: POST /verify (with X-PAYMENT)
    end

    Ver->>Ver: Decode X-PAYMENT (base64 -> JSON)
    Ver->>Fac: POST /verify<br/>{payload, paymentRequirements}

    alt Facilitator: verify only mode
        Fac->>Fac: Validate signature (EIP-712)
        Fac->>Fac: Check authorization fields
        Fac-->>Ver: {valid: true, settled: false}
    else Facilitator: verify + settle
        Fac->>Chain: Submit TransferWithAuthorization tx
        Chain-->>Fac: Tx confirmed
        Fac-->>Ver: {valid: true, settled: true, txHash: "0x..."}
    end

    Ver-->>TF: 200 OK
    Note over Ver: Sets Authorization header<br/>if route has upstreamAuth

    TF->>Up: GET /data (+ Authorization header)
    Up-->>TF: 200 OK + response body
    TF-->>Client: 200 OK + response
```

*SPEC cross-ref: Section 4.1.1 (Request Flow), Section 4.1.2 (PaymentRequired Response), Section 4.1.3 (PaymentPayload).*

---

## 5. Storage Architecture

### 5.1 Filesystem State

All persistent state lives under three XDG-compliant directory trees. In development mode (`OBOL_DEVELOPMENT=true`), these collapse into `.workspace/`.

```
$OBOL_CONFIG_DIR/                          # ~/.config/obol or .workspace/config
├── .stack-id                              # Cluster petname (e.g., "fluffy-penguin")
├── .stack-backend                         # "k3d" or "k3s"
├── kubeconfig.yaml                        # Kubernetes API access
├── tunnel/
│   └── cloudflared.json                   # Tunnel state (mode, hostname, IDs)
├── defaults/                              # Embedded infrastructure (templated)
│   ├── helmfile.yaml
│   ├── base/templates/*.yaml
│   ├── cloudflared/
│   └── values/
└── networks/<type>/<id>/                  # Per-network deployment configs
    ├── helmfile.yaml
    └── values.yaml

$OBOL_DATA_DIR/                            # ~/.local/share/obol or .workspace/data
├── openclaw-<id>/
│   ├── openclaw-data/
│   │   └── .openclaw/skills/              # 23 embedded skills (host-path PVC)
│   └── keystore/                          # Web3 V3 encrypted keystores
└── local-path-provisioner/                # k3s PVC backing store (root-owned)

$OBOL_BIN_DIR/                             # ~/.local/bin or .workspace/bin
└── obol                                   # CLI binary
```

*SPEC cross-ref: Section 2.3 (Configuration Hierarchy), Section 5.1 (Configuration Files).*

### 5.2 Kubernetes ConfigMaps

ConfigMaps are the primary in-cluster configuration mechanism. They serve as the control plane for runtime behavior changes without Pod restarts (where hot-reload is supported).

| ConfigMap | Namespace | Key(s) | Purpose | Hot-Reload |
|-----------|-----------|--------|---------|-----------|
| `litellm-config` | `llm` | `config.yaml` | LiteLLM model_list, routing rules | No (restart required) |
| `x402-pricing` | `x402` | `pricing.yaml` | Verifier route rules, wallet, chain, facilitator URL | Yes (5s poll) |
| `x402-buyer-config` | `llm` | `config.json` | Buyer upstream definitions (URL, model, chain, price) | Yes (mutex reload) |
| `x402-buyer-auths` | `llm` | `auths.json` | Pre-signed ERC-3009 authorization pools | Yes (mutex reload) |
| `erpc-config` | `erpc` | `erpc.yaml` | RPC projects, networks, upstreams | No (restart required) |
| `obol-stack-config` | `obol-frontend` | `config.json` | Frontend dashboard configuration (tunnel URL) | Yes (volume mount) |
| `tunnel-storefront` | `traefik` | `index.html`, `mime.types` | Static HTML landing page content | Yes (volume mount) |

### 5.3 Kubernetes Secrets

| Secret | Namespace | Key(s) | Purpose |
|--------|-----------|--------|---------|
| `litellm-secrets` | `llm` | `LITELLM_MASTER_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` | LiteLLM authentication credentials |
| `x402-secrets` | `x402` | (verifier credentials) | Verifier operational secrets |
| `openclaw-wallet` | `openclaw-obol-agent` | Keystore JSON | Agent wallet encrypted private key |

### 5.4 Persistent Volume Claims

| PVC | Namespace | Backing | Purpose | Ownership |
|-----|-----------|---------|---------|----------|
| Skills PVC | `openclaw-obol-agent` | Host-path (`$DATA_DIR/openclaw-<id>/openclaw-data/`) | Skill injection into agent container | Root-owned (k3s provisioner) |
| Local-path PVCs | Various | Host-path (`$DATA_DIR/local-path-provisioner/`) | Blockchain node data, application state | Root-owned (`purge -f` to remove) |

### 5.5 Wallet Keystores

Wallet state spans both the filesystem and Kubernetes:

```
Filesystem:
  $DATA_DIR/openclaw-<id>/keystore/UTC--<timestamp>--<address>.json

Kubernetes:
  Secret/openclaw-wallet (openclaw-obol-agent ns)
    └── keystore.json  (same content, accessible to remote-signer Pod)

Remote Signer:
  Deployment/remote-signer (openclaw-obol-agent ns, port 9000)
    └── Loads keystore from mounted Secret
    └── REST API: POST /sign, GET /address
```

*SPEC cross-ref: Section 5.4 (Wallet), Section 7.3 (Wallet Security), Section 3.6.3 (Wallet Generation).*

---

## 6. Deployment Model

### 6.1 k3d Cluster Topology

```
┌─────────────────────────────────────────────────────────────┐
│  Host Machine                                               │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────────┐  │
│  │ Ollama   │  │ obol CLI │  │ Docker Desktop / Engine   │  │
│  │ :11434   │  │          │  │                            │  │
│  └──────────┘  └──────────┘  │  ┌──────────────────────┐ │  │
│                              │  │  k3d Cluster          │ │  │
│                              │  │  (k3s v1.35.1-k3s1)  │ │  │
│                              │  │                        │ │  │
│                              │  │  1 server node         │ │  │
│                              │  │  Port mappings:        │ │  │
│                              │  │    80:80   (HTTP)      │ │  │
│                              │  │    8080:80 (HTTP alt)  │ │  │
│                              │  │    443:443 (HTTPS)     │ │  │
│                              │  │    8443:443 (HTTPS alt)│ │  │
│                              │  │                        │ │  │
│                              │  └──────────────────────┘ │  │
│                              └──────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**Backend variants:**

| Property | k3d (default) | k3s (bare metal) |
|----------|--------------|------------------|
| Runtime | Docker container | Direct k3s binary |
| Ollama access | `host.docker.internal` (macOS) / `host.k3d.internal` (Linux) | `127.0.0.1` (loopback) |
| Port binding | Docker port mapping | Direct host binding |
| Data isolation | Docker volumes + host-path mounts | Direct filesystem |
| Backend switch | Destroys old cluster automatically | Destroys old cluster automatically |

*SPEC cross-ref: Section 2.4 (Backend Abstraction), Section 3.1.4 (Ollama Host Resolution).*

### 6.2 Namespace Layout

Each namespace represents a failure domain and RBAC boundary. Resources within a namespace share a security context.

```mermaid
graph TB
    subgraph "traefik"
        GC[GatewayClass: traefik]
        GW[Gateway: traefik-gateway<br/>:80, :443]
        CFD[Deployment: cloudflared]
        SF[Deployment: tunnel-storefront]
        SF_SVC[Service: tunnel-storefront :8080]
        SF_CM[ConfigMap: tunnel-storefront]
        SF_HR[HTTPRoute: tunnel-storefront]
    end

    subgraph "llm"
        LLMD[Deployment: litellm<br/>Containers: litellm :4000, x402-buyer :8402]
        LLC[ConfigMap: litellm-config]
        LLS[Secret: litellm-secrets]
        BC[ConfigMap: x402-buyer-config]
        BA[ConfigMap: x402-buyer-auths]
    end

    subgraph "x402"
        VD[Deployment: x402-verifier :8080]
        VP[ConfigMap: x402-pricing]
        VS[Secret: x402-secrets]
        VSM[ServiceMonitor: x402-verifier]
    end

    subgraph "openclaw-obol-agent"
        OC[Deployment: openclaw]
        RS[Deployment: remote-signer :9000]
        WS[Secret: openclaw-wallet]
    end

    subgraph "erpc"
        ED[Deployment: erpc :4000]
        EC[ConfigMap: erpc-config]
    end

    subgraph "obol-frontend"
        FD[Deployment: frontend]
        FC[ConfigMap: obol-stack-config]
    end

    subgraph "monitoring"
        PD[Prometheus Stack]
    end

    subgraph "network-petname (dynamic)"
        EX[Execution Layer :8545]
        CL[Consensus Layer]
    end

    subgraph "cluster-scoped"
        CRD[CRD: ServiceOffer obol.org]
        CR[ClusterRole: openclaw-monetize]
        CRB[ClusterRoleBinding: openclaw-monetize-binding]
    end
```

*SPEC cross-ref: Section 5.2 (Kubernetes Resources by Namespace).*

---

## 7. Network Topology

### 7.1 Traefik Gateway API Routing

Traefik operates as the single ingress point using the Kubernetes Gateway API (not legacy Ingress). All traffic classification happens at the Gateway level based on hostname and path.

```mermaid
flowchart TB
    subgraph "External Traffic"
        Internet((Public Internet))
        Local((Local Machine<br/>obol.stack))
    end

    Internet --> CF[cloudflared<br/>Tunnel Pod]
    CF --> GW

    Local --> GW[Traefik Gateway<br/>:80 / :443]

    GW --> ClassifyHostname{Hostname?}

    ClassifyHostname -->|"obol.stack"| LocalRoutes
    ClassifyHostname -->|"* (any / tunnel)"| PublicRoutes

    subgraph LocalRoutes["Local-Only Routes (hostnames: obol.stack)"]
        direction TB
        LR1["/ -> Frontend"]
        LR2["/rpc -> eRPC"]
    end

    subgraph PublicRoutes["Public Routes (no hostname restriction)"]
        direction TB
        PR1["/services/* -> ForwardAuth -> Upstream"]
        PR2["/.well-known/* -> ERC-8004 httpd"]
        PR3["/skill.md -> Service Catalog"]
        PR4["/ (tunnel host) -> Storefront"]
    end

    PR1 --> FA{x402 ForwardAuth}
    FA -->|"No X-PAYMENT"| R402[402 Payment Required]
    FA -->|"Valid X-PAYMENT"| R200[200 -> Upstream Service]
    FA -->|"No route match"| PASS[200 -> Pass Through]
```

### 7.2 Route Classification Rules

| Route | Hostname Restriction | Protection | Target | HTTPRoute Location |
|-------|---------------------|-----------|--------|-------------------|
| `/` | `obol.stack` | None (local network) | Frontend | `obol-frontend` ns |
| `/rpc` | `obol.stack` | None (local network) | eRPC | `erpc` ns |
| `/services/<name>/*` | None (public) | x402 ForwardAuth | Upstream service | `openclaw-obol-agent` ns |
| `/.well-known/agent-registration.json` | None (public) | None (read-only) | ERC-8004 httpd | `traefik` ns |
| `/skill.md` | None (public) | None (read-only) | Service catalog httpd | `traefik` ns |
| `/` (tunnel hostname) | None (public) | None (static HTML) | Storefront httpd | `traefik` ns |

**Security invariant:** Internal services (frontend, eRPC, LiteLLM admin, Prometheus) MUST have `hostnames: ["obol.stack"]` to prevent tunnel exposure. See Section 8.2 for trust boundary details.

### 7.3 Internal Service Communication

All internal traffic uses Kubernetes ClusterIP services with DNS resolution (`<svc>.<ns>.svc.cluster.local`).

```
┌─────────────────────────────────────────────────────────────────────┐
│ Cluster-Internal Traffic (ClusterIP, no external exposure)          │
│                                                                     │
│  LiteLLM :4000                                                      │
│    ├── ollama/* ──> Ollama Service :11434 ──> host Ollama           │
│    ├── paid/*   ──> x402-buyer :8402 (localhost, same Pod)          │
│    ├── anthropic/* ──> api.anthropic.com (egress)                   │
│    └── openai/*   ──> api.openai.com (egress)                       │
│                                                                     │
│  x402-buyer :8402                                                   │
│    └── upstream ──> Remote seller (egress, x402 payment attached)   │
│                                                                     │
│  x402-verifier :8080                                                │
│    └── facilitator ──> facilitator.x402.rs (egress, HTTPS)          │
│                                                                     │
│  OpenClaw Agent                                                     │
│    ├── LiteLLM ──> litellm.llm.svc:4000                            │
│    └── Remote Signer ──> remote-signer:9000 (same namespace)        │
│                                                                     │
│  eRPC :4000                                                         │
│    ├── Local nodes ──> <network>-execution.<ns>.svc:8545            │
│    └── Remote RPCs ──> ChainList endpoints (egress)                 │
│                                                                     │
│  Prometheus                                                         │
│    ├── x402-verifier ──> ServiceMonitor scrape /metrics             │
│    └── x402-buyer   ──> PodMonitor scrape /metrics                  │
└─────────────────────────────────────────────────────────────────────┘
```

*SPEC cross-ref: Section 2.2 (Routing Architecture), Section 6.2 (Internal Service Communication), Section 7.1 (Tunnel Exposure).*

---

## 8. Security Architecture

### 8.1 Trust Boundaries

The system has four trust boundaries, each with distinct threat models and protection mechanisms.

```mermaid
graph TB
    subgraph TB1["Trust Boundary 1: Host Machine"]
        CLI[obol CLI]
        Ollama[Ollama]
        Docker[Docker]
        Keystore["Wallet Keystores<br/>(encrypted, filesystem)"]
        SE["Secure Enclave<br/>(hardware, macOS)"]
    end

    subgraph TB2["Trust Boundary 2: k3d Cluster"]
        subgraph TB2a["TB 2a: Local-Only Zone"]
            FE[Frontend]
            ERPC[eRPC]
            Prom[Prometheus]
            LLMAdmin[LiteLLM Admin]
        end

        subgraph TB2b["TB 2b: Payment-Gated Zone"]
            Verifier[x402-verifier]
            Services["/services/* upstreams"]
        end

        subgraph TB2c["TB 2c: Agent Zone (RBAC-scoped)"]
            Agent[OpenClaw Agent]
            Signer[Remote Signer]
            Wallet[Wallet Secret]
        end
    end

    subgraph TB3["Trust Boundary 3: Tunnel (Public Internet)"]
        CF[cloudflared]
        Buyers[Remote Buyers]
    end

    subgraph TB4["Trust Boundary 4: External Services"]
        Fac[x402 Facilitator]
        Chain[Base L2]
        CloudLLM[Cloud LLM APIs]
    end

    TB3 -->|"Only: /services/*, /.well-known/,<br/>/skill.md, / (storefront)"| TB2b
    TB2b -->|"ForwardAuth"| Verifier
    TB2c -->|"RBAC: openclaw-monetize ClusterRole"| TB2b
    TB2c -->|"Port 9000, in-namespace only"| Signer
    Verifier -->|"HTTPS POST"| Fac
    Agent -->|"JSON-RPC"| Chain
```

### 8.2 Authentication and Authorization Flows

| Flow | Mechanism | Protection |
|------|-----------|-----------|
| **Public -> Service** | x402 payment (EIP-712 signed ERC-3009) | Facilitator verifies signature + settles on-chain. No payment = 402 rejection. |
| **Local -> Frontend/eRPC** | Hostname restriction (`obol.stack`) | Only reachable from local machine via hosts file or DNS resolver. Tunnel traffic cannot match. |
| **Agent -> Kubernetes API** | ServiceAccount `openclaw` + RBAC | `openclaw-monetize` ClusterRole: CRUD on ServiceOffers, Middlewares, HTTPRoutes, ConfigMaps, Services, Deployments. Read-only on Pods, Endpoints, logs. |
| **Agent -> Signing** | Remote-signer REST API (port 9000) | In-namespace only (no Service exposed outside namespace). Keystore decryption at signer startup. |
| **Buyer -> Remote Seller** | Pre-signed ERC-3009 auths via X-PAYMENT header | Zero signer access. Finite auth pool. Max loss = N * price per auth. |
| **CLI -> Cluster** | kubeconfig (auto-generated, file-permission protected) | `0600` permissions on kubeconfig. Port drift handled by regeneration. |

### 8.3 Wallet Isolation

Three distinct wallet isolation models serve different security requirements:

```
1. Software Wallet (Default)
   ┌─────────────────┐     ┌──────────────────┐     ┌─────────────┐
   │ Keystore File    │────>│ Remote Signer    │────>│ Agent Pod   │
   │ (scrypt + AES)   │     │ :9000            │     │ (REST only) │
   │ $DATA_DIR/...    │     │ In-namespace     │     │             │
   └─────────────────┘     └──────────────────┘     └─────────────┘
   Key at rest: encrypted. Key in use: signer memory only.

2. Secure Enclave (macOS, standalone gateway)
   ┌─────────────────┐     ┌──────────────────┐
   │ Apple SEP        │────>│ Inference Gateway│
   │ (P-256, never    │     │ (ECIES decrypt,  │
   │  exported)        │     │  ECDSA sign)     │
   └─────────────────┘     └──────────────────┘
   Key never leaves hardware. SIP enforced.

3. TEE (Linux, confidential computing)
   ┌─────────────────┐     ┌──────────────────┐
   │ TEE Enclave      │────>│ Inference Gateway│
   │ (TDX/SNP/Nitro)  │     │ (attestation +   │
   │ Key in-enclave   │     │  ECIES decrypt)  │
   └─────────────────┘     └──────────────────┘
   Key bound to attestation. Hardware-signed quote.
```

*SPEC cross-ref: Section 7.3 (Wallet Security), Section 7.4 (Enclave / TEE Security).*

### 8.4 Pre-Signed Authorization Pool (Buy Side)

The buy-side security model eliminates private key exposure from the sidecar entirely:

```
┌──────────────────────────────────────────────────────┐
│ buy.py (Agent context, has signer access)             │
│                                                       │
│  1. Generate N random 32-byte nonces                  │
│  2. For each nonce, sign ERC-3009 via remote-signer   │
│  3. Write signed auths to ConfigMap                   │
│                                                       │
│  Output: N pre-signed TransferWithAuthorization       │
│          Each authorizes exactly $price USDC transfer  │
└───────────────────────┬──────────────────────────────┘
                        │ ConfigMap (x402-buyer-auths)
                        v
┌──────────────────────────────────────────────────────┐
│ x402-buyer sidecar (NO signer access)                 │
│                                                       │
│  - Pops one auth per 402 response (mutex-guarded)     │
│  - Marks nonce consumed (StateStore, crash-safe)      │
│  - Cannot generate new auths                          │
│  - Cannot modify auth values                          │
│  - Max spend = N * price (bounded by pool size)       │
│  - Pool exhausted -> 404 (agent must pre-sign more)   │
└──────────────────────────────────────────────────────┘
```

*SPEC cross-ref: Section 3.5.3 (Architecture), Section 7.2 (Payment Security).*

### 8.5 RBAC Model

The `openclaw-monetize` ClusterRole is the sole RBAC grant for the agent. It follows the principle of least privilege across API groups:

| API Group | Resources | Verbs | Rationale |
|-----------|-----------|-------|-----------|
| `obol.org` | `serviceoffers`, `serviceoffers/status` | get, list, watch, create, update, patch, delete | Full lifecycle of sell-side CRDs |
| `traefik.io` | `middlewares` | get, list, create, update, patch, delete | ForwardAuth middleware for x402 gating |
| `gateway.networking.k8s.io` | `httproutes` | get, list, create, update, patch, delete | Public route publication |
| (core) | `configmaps`, `services`, `deployments` | get, list, create, update, patch, delete | Pricing ConfigMap, registration httpd, storefront |
| (core) | `pods`, `endpoints`, `pods/log` | get, list | Health checks, debugging (read-only) |

**Binding:** ClusterRoleBinding `openclaw-monetize-binding` binds to ServiceAccount `openclaw` in namespace `openclaw-obol-agent`. The `patchMonetizeBinding()` function in `internal/agent/agent.go` ensures the subjects array is populated, guarding against race conditions during initial cluster setup.

*SPEC cross-ref: Section 7.5 (RBAC), Section 5.2 (Kubernetes Resources).*

### 8.6 Threat Model Summary

| Threat | Mitigation | Residual Risk |
|--------|-----------|---------------|
| Tunnel exposes internal services | `hostnames: ["obol.stack"]` restriction on all local-only HTTPRoutes | Misconfiguration (test: never create public routes for internal services) |
| Replay attack on x402 payments | Random 32-byte nonces, `validBefore`/`validAfter` windows, facilitator deduplication | Facilitator availability |
| Buyer overspending | Pre-signed auth pool with finite size, nonce consumption tracking | Pool size set at purchase time |
| Wallet key extraction | Encrypted keystore (scrypt), remote-signer pattern, Secure Enclave (non-exportable) | Software wallet in memory during signing |
| Reconciler privilege escalation | ClusterRole scoped to specific API groups and verbs | Agent code compromise could create arbitrary routes |
| Supply chain (container images) | Pinned image tags (LiteLLM, k3s, OpenClaw), version consistency tests | Upstream image compromise before pin update |
| ConfigMap propagation delay | 60-120s k3d file watcher; 5s verifier poll | Brief window where stale config serves requests |

*SPEC cross-ref: Section 7.1 (Tunnel Exposure), Section 7.2 (Payment Security), Section 9.4 (Known Latencies).*
