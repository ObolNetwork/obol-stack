# Obol Stack Architecture

**Version**: 1.0.0-pr288
**Status**: Living document
**Last Updated**: 2026-03-29

This document is the structural companion to [SPEC.md](SPEC.md). It focuses on component boundaries, data flow, deployment topology, and trust boundaries for the PR `#288` baseline.

---

## Table of Contents

1. [Design Philosophy](#1-design-philosophy)
2. [Component Diagrams](#2-component-diagrams)
3. [Module Decomposition](#3-module-decomposition)
4. [Data Flow Diagrams](#4-data-flow-diagrams)
5. [Storage Architecture](#5-storage-architecture)
6. [Deployment Model](#6-deployment-model)
7. [Network Topology](#7-network-topology)
8. [Security Architecture](#8-security-architecture)

---

## 1. Design Philosophy

Obol Stack is built around these principles:

1. **Local-first sovereignty**: the operator machine remains the source of truth for cluster, wallet, and skill state.
2. **Single operator entry point**: the `obol` CLI is the primary control surface for lifecycle, routing, applications, and monetization.
3. **Centralized protocol translation**: LiteLLM centralizes model routing, Traefik centralizes HTTP routing, and eRPC centralizes chain access.
4. **Bounded trust**: payment execution, signing, and public routing are split into separate components with different privileges.
5. **Phased extensibility**: experimental or not-yet-fully-integrated surfaces are explicit phase follow-ups rather than hidden assumptions.

System constraints are defined in [SPEC.md](SPEC.md#15-system-constraints).

---

## 2. Component Diagrams

### 2.1 C4 Context Diagram

```mermaid
C4Context
    title Obol Stack - System Context

    Person(operator, "Local Operator", "Starts the stack, manages services, inspects health")
    Person(agent_dev, "Agent Developer", "Deploys and tunes OpenClaw instances and skills")
    Person(remote_buyer, "Remote Buyer", "Pays for public services or remote models")

    System(obol, "Obol Stack", "Local-first agent and infrastructure platform")

    System_Ext(ollama, "Ollama", "Local host model runtime")
    System_Ext(cloud_llm, "Cloud LLM APIs", "Anthropic and OpenAI providers")
    System_Ext(chainlist, "ChainList", "Public RPC discovery")
    System_Ext(facilitator, "x402 Facilitator", "Payment verification and settlement")
    System_Ext(cloudflare, "Cloudflare", "Tunnel control plane and edge")
    System_Ext(chains, "EVM Chains", "Payment and registration settlement")
    System_Ext(charts, "ArtifactHub / OCI / Helm Repos", "Managed application sources")

    Rel(operator, obol, "CLI + browser")
    Rel(agent_dev, obol, "CLI + embedded skills")
    Rel(remote_buyer, obol, "HTTPS paid requests")
    Rel(obol, ollama, "HTTP")
    Rel(obol, cloud_llm, "HTTPS")
    Rel(obol, chainlist, "HTTPS")
    Rel(obol, facilitator, "HTTPS")
    Rel(obol, cloudflare, "Browser auth, API, tunnel traffic")
    Rel(obol, chains, "JSON-RPC via eRPC")
    Rel(obol, charts, "HTTPS / OCI pull")
```

### 2.2 C4 Container Diagram

```mermaid
C4Container
    title Obol Stack - Container Diagram

    Person(operator, "Local Operator")
    Person(remote_buyer, "Remote Buyer")

    System_Boundary(host, "Operator Machine") {
        Container(cli, "obol CLI", "Go", "Lifecycle, routing, apps, monetization")
        Container_Boundary(cluster, "Local k3d/k3s Cluster") {
            Container(traefik, "Traefik", "Gateway API", "Ingress and route dispatch")
            Container(cloudflared, "cloudflared", "Cloudflare Tunnel", "Public ingress bridge")
            Container(litellm, "LiteLLM", "Python", "OpenAI-compatible model gateway")
            Container(buyer, "x402-buyer", "Go sidecar", "Attaches pre-signed payments")
            Container(erpc, "eRPC", "Go", "Blockchain RPC gateway")
            Container(verifier, "x402-verifier", "Go", "ForwardAuth payment checks")
            Container(soctl, "serviceoffer-controller", "Go", "Reconciles ServiceOffer and RegistrationRequest CRs")
            Container(agent, "OpenClaw", "OpenClaw runtime", "Agent instances and skills")
            Container(frontend, "Frontend", "React app", "Operator dashboard")
            ContainerDb(prom, "Prometheus", "Monitoring stack", "Metrics and scrape targets")
        }
    }

    System_Ext(ollama, "Ollama")
    System_Ext(facilitator, "x402 Facilitator")
    System_Ext(chain, "EVM Chain")

    Rel(operator, cli, "Runs commands")
    Rel(operator, traefik, "Uses obol.stack")
    Rel(remote_buyer, cloudflared, "HTTPS")
    Rel(cloudflared, traefik, "HTTP")
    Rel(traefik, frontend, "Route /")
    Rel(traefik, erpc, "Route /rpc")
    Rel(traefik, verifier, "ForwardAuth /verify")
    Rel(traefik, litellm, "Route public services after auth")
    Rel(litellm, buyer, "paid/* route")
    Rel(litellm, ollama, "ollama/* models")
    Rel(verifier, facilitator, "Verify payment")
    Rel(erpc, chain, "JSON-RPC")
    Rel(agent, erpc, "Chain queries")
    Rel(soctl, verifier, "Derives dynamic routes from ServiceOffers")
    Rel(soctl, agent, "Reconciles offers created by agent")
    Rel(prom, buyer, "Scrapes /metrics")
```

### 2.3 Component Diagram: Sell-Side Control Loop

```mermaid
C4Component
    title Sell-Side Control Loop

    Component(cli, "sell commands", "Go", "Creates ServiceOffers and local gateways")
    Component(offer, "ServiceOffer CRD", "Kubernetes API", "Declarative sell contract")
    Component(soctl, "serviceoffer-controller", "Go", "Informer-based reconcile loop in x402 ns")
    Component(rr, "RegistrationRequest CRD", "Kubernetes API", "Isolates ERC-8004 side effects")
    Component(ver, "x402-verifier", "Go", "Payment gate with kube route source")
    Component(route, "HTTPRoute / Middleware", "Gateway API + Traefik", "Traffic publication")

    Rel(cli, offer, "Create / update / pause / delete")
    Rel(offer, soctl, "Watch / status patch")
    Rel(soctl, ver, "Derives dynamic pricing routes")
    Rel(soctl, route, "Create route + middleware")
    Rel(soctl, rr, "Create / update registration request")
    Rel(rr, soctl, "Watch / reconcile publication + on-chain registration")
```

---

## 3. Module Decomposition

| Module | Responsibility | SPEC Reference |
|--------|----------------|----------------|
| `internal/stack` | Backend lifecycle and default infrastructure deployment | Section 3.1 |
| `internal/model` | Central LiteLLM routing and provider patching | Section 3.2 |
| `internal/network` | eRPC, local network deployments, public RPC management | Section 3.3 |
| `internal/openclaw` | OpenClaw overlays, tokens, skills, wallets, dashboards | Section 3.4 |
| `internal/agent` | Cleans up legacy heartbeat from the default agent | Section 3.4 |
| `cmd/serviceoffer-controller` + `internal/serviceoffercontroller` | ServiceOffer and RegistrationRequest reconciliation | Section 3.5 |
| `internal/monetizeapi` | Go types for ServiceOffer, RegistrationRequest, and GVR constants | Section 3.5 |
| `cmd/obol/sell.go` + `internal/x402` | Sell-side operator CLI and verifier paths | Section 3.5 |
| `internal/x402/buyer` | Buy-side sidecar runtime | Section 3.6 |
| `internal/tunnel` | Quick and DNS tunnel lifecycle | Section 3.7 |
| `internal/app` | Managed Helm-chart workloads | Section 3.8 |

---

## 4. Data Flow Diagrams

### 4.1 Stack Startup

```mermaid
sequenceDiagram
    participant O as Operator
    participant CLI as obol CLI
    participant B as Backend
    participant H as Helmfile
    participant L as LiteLLM
    participant OC as OpenClaw
    participant A as agent.Init
    participant T as Tunnel

    O->>CLI: obol stack up
    CLI->>B: Up()
    B-->>CLI: kubeconfig
    CLI->>H: sync defaults
    H-->>CLI: baseline infrastructure ready
    CLI->>L: autoConfigureLLM()
    CLI->>OC: SetupDefault()
    CLI->>A: remove legacy HEARTBEAT.md
    CLI->>T: start only if persistent DNS tunnel exists
    CLI-->>O: obol.stack ready
```

### 4.2 Sell-Side Publication

```mermaid
sequenceDiagram
    participant O as Operator
    participant CLI as sell command
    participant K as Kubernetes API
    participant C as serviceoffer-controller
    participant V as x402-verifier
    participant G as Traefik / Gateway API

    O->>CLI: obol sell http ...
    CLI->>K: create ServiceOffer
    C->>K: watch ServiceOffer
    C->>K: patch status ModelReady / UpstreamHealthy
    C->>K: create Middleware + HTTPRoute
    C->>V: verifier picks up dynamic route (kube source)
    C->>K: create RegistrationRequest
    C->>K: publish agent-registration.json + attempt on-chain registration
    C->>K: patch Ready
```

### 4.3 Buy-Side Request

```mermaid
sequenceDiagram
    participant A as Agent
    participant S as Remote Signer
    participant C as ConfigMaps
    participant L as LiteLLM
    participant B as x402-buyer
    participant Seller as Remote Seller
    participant F as Facilitator

    A->>Seller: probe without payment
    Seller-->>A: 402 pricing
    A->>S: pre-sign N auths
    A->>C: store upstream config + auth pool
    A->>L: request model paid/<remote-model>
    L->>B: forward request
    B->>Seller: request without payment
    Seller-->>B: 402
    B->>Seller: retry with X-PAYMENT
    Seller->>F: verify payment
    F-->>Seller: verification result
    Seller-->>B: 200 response
    B-->>L: inference result
```

---

## 5. Storage Architecture

### 5.1 Overview

State is intentionally split between:
- local XDG filesystem state managed by the CLI
- Kubernetes resources in the local cluster
- external chain state and facilitator state that the stack references but does not own

### 5.2 Schema Summary

| Store | Entity | Key Fields | Purpose |
|-------|--------|-----------|---------|
| Local config dir | stack metadata | `.stack-id`, `.stack-backend`, `kubeconfig.yaml` | Stack identity and runtime targeting |
| Local config dir | deployment config | `applications/<app>/<id>`, `networks/<type>/<id>` | Declarative deployment inputs |
| Kubernetes API | `ServiceOffer` | `spec.upstream`, `spec.payment`, `status.conditions` | Sell-side contract and reconcile status |
| Kubernetes API | `RegistrationRequest` | `spec.serviceOfferName`, `spec.desiredState`, `status.phase` | ERC-8004 publication and on-chain registration lifecycle |
| Kubernetes ConfigMaps | routing and pricing state | LiteLLM, eRPC, x402, buyer config | Dynamic runtime routing |
| Kubernetes Secrets | provider creds and tunnel token | API keys, tunnel token | Sensitive runtime inputs |

---

## 6. Deployment Model

### 6.1 Deployment Diagram

```mermaid
graph TD
    subgraph "Operator Host"
        CLI["obol CLI"]
        XDG["XDG config/data/state"]
        OLLAMA["Ollama (optional)"]
    end

    subgraph "Local k3d / k3s Cluster"
        TRAEFIK["Traefik + Gateway"]
        CLOUDFLARED["cloudflared"]
        LLM["LiteLLM + x402-buyer"]
        ERPC["eRPC"]
        X402["x402-verifier"]
        SOCTL["serviceoffer-controller"]
        OCA["OpenClaw / obol-agent"]
        FE["Frontend"]
        MON["Monitoring"]
    end

    CLI --> XDG
    CLI --> TRAEFIK
    CLI --> OCA
    CLI --> ERPC
    LLM --> OLLAMA
    CLOUDFLARED --> TRAEFIK
    TRAEFIK --> FE
    TRAEFIK --> ERPC
    TRAEFIK --> X402
    SOCTL --> X402
    TRAEFIK --> LLM
```

### 6.2 Infrastructure Requirements

| Resource | Requirement | Notes |
|----------|-------------|-------|
| Local runtime | Docker for `k3d` or direct host support for `k3s` | Backend-specific prerequisites |
| Filesystem | Writable XDG config/data/state dirs | Required for persistent stack state |
| Network | Local loopback plus outbound HTTPS | Needed for providers, ChainList, facilitator, Cloudflare |
| Optional Cloudflare account | Required only for persistent DNS tunnel | Quick tunnel path can remain local-first |

---

## 7. Network Topology

- `obol.stack` is the local operator hostname.
- Frontend and eRPC are intentionally bound behind `hostnames: ["obol.stack"]`.
- Public service routes flow through Cloudflare tunnel to Traefik, then through x402 ForwardAuth before reaching an upstream.
- Buyer-side `paid/*` traffic stays inside the cluster until the sidecar contacts a remote seller.
- Registration JSON is intentionally public and bypasses ForwardAuth.

---

## 8. Security Architecture

### 8.1 Trust Boundaries

Trust boundaries exist between:
- operator host and local cluster
- local-only routes and public tunnel routes
- remote signer and buyer sidecar
- x402 verification and upstream service execution
- local filesystem state and external chain/facilitator systems

### 8.2 Authentication Flow

```mermaid
sequenceDiagram
    participant Buyer as Remote Buyer
    participant Traefik as Traefik
    participant Verifier as x402-verifier
    participant Fac as Facilitator
    participant Upstream as Service

    Buyer->>Traefik: HTTPS request
    Traefik->>Verifier: ForwardAuth /verify
    Verifier->>Fac: validate X-PAYMENT
    Fac-->>Verifier: result
    Verifier-->>Traefik: 200 or 402
    Traefik->>Upstream: only after 200
```

### 8.3 Data Encryption

| Data | At Rest | In Transit |
|------|---------|-----------|
| Provider API keys | Kubernetes Secret | HTTPS to provider APIs |
| Wallet and backup material | Local data dir, optional encrypted backup | Local filesystem or remote signer API |
| Tunnel traffic | Cloudflare-managed | HTTPS / QUIC |
| Payment proofs | Not persisted by the sidecar beyond auth pool state | HTTPS to seller / facilitator |
