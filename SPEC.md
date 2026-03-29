# Obol Stack Technical Specification

**Version**: 1.0.0-pr288
**Status**: Living document
**Last Updated**: 2026-03-29

This document is the authoritative technical specification for Obol Stack on the PR `#288` integration baseline. It describes the system that is actually implemented on this branch, with future work isolated into explicit phased rollout items and ADR follow-ups.

Primary actor priority:
- Local operator
- Agent developer
- Remote buyer

---

## Table of Contents

1. [Introduction](#1-introduction)
2. [System Architecture](#2-system-architecture)
3. [Core Subsystems](#3-core-subsystems)
4. [API / Protocol Definition](#4-api--protocol-definition)
5. [Data Model](#5-data-model)
6. [Integration Points](#6-integration-points)
7. [Security Model](#7-security-model)
8. [Error Handling](#8-error-handling)
9. [Performance and Operations](#9-performance-and-operations)
10. [Phased Rollout](#10-phased-rollout)
11. [Testing Strategy](#11-testing-strategy)

---

## 1. Introduction

### 1.1 Purpose

Obol Stack is a local-first Kubernetes platform for running AI agent infrastructure, blockchain connectivity, payment-gated services, and public discovery from a single operator-controlled machine. This specification defines the expected structure and behavior of the stack as shipped on the PR `#288` branch.

### 1.2 Scope

The system:
- Initializes and manages a local `k3d` or `k3s` cluster from an XDG-compliant CLI.
- Deploys default infrastructure: Traefik, eRPC, LiteLLM, Cloudflare tunnel connector, monitoring, frontend, and OpenClaw.
- Lets the operator configure local and cloud model providers through a central LiteLLM gateway.
- Lets the operator install local blockchain nodes and add remote RPC upstreams to eRPC.
- Runs OpenClaw instances with embedded skills, wallet management, and an elevated default `obol-agent`.
- Sells local services through x402 payment gates and optional ERC-8004 registration.
- Buys remote x402-gated inference through a bounded-risk sidecar pattern.
- Exposes local-only and public routes with different trust boundaries.
- Installs arbitrary Helm charts as managed applications.

The system does **not**:
- Operate as a hosted multi-tenant SaaS control plane.
- Assume public exposure is required for the core local operator path.
- Guarantee exact token metering for every pricing model in the current phase.
- Treat every chain known to eRPC or `internal/x402` as an operator-supported sell-side CLI chain.
- Replace direct Kubernetes administration for users who want bespoke cluster changes outside Obol-managed paths.

### 1.3 Personas

| Persona | Goal | Primary Interfaces |
|--------|------|--------------------|
| Local operator | Bring up the stack, manage infra, expose services, inspect health | `obol` CLI, `http://obol.stack`, tunnel URL |
| Agent developer | Deploy and tune OpenClaw instances, skills, wallets, model routes | `obol openclaw`, `obol model`, embedded skills |
| Remote buyer | Discover and pay for a service or remote model | x402-gated HTTP endpoints, `paid/<model>` through LiteLLM |

### 1.4 Terminology and Glossary

| Term | Definition |
|------|-----------|
| **Stack ID** | Petname-based identifier persisted in `$OBOL_CONFIG_DIR/.stack-id`; used for cluster identity and LiteLLM master key derivation. |
| **Backend** | Local cluster runtime: `k3d` (Docker-based) or `k3s` (bare-metal). |
| **ServiceOffer** | Namespaced CRD (`obol.org/v1alpha1`) describing a sell-side service, payment terms, route path, provenance, and registration metadata. |
| **eRPC** | In-cluster blockchain RPC gateway that multiplexes local node and public RPC upstreams. |
| **LiteLLM** | Central OpenAI-compatible model gateway in the `llm` namespace. |
| **OpenClaw instance** | A deployed AI agent runtime managed through `obol openclaw ...`. |
| **obol-agent** | The canonical default OpenClaw instance with elevated RBAC and a heartbeat-based reconciliation loop. |
| **x402-verifier** | ForwardAuth service that matches routes, emits `402 Payment Required`, and delegates verification to a facilitator. |
| **x402-buyer** | Sidecar in the LiteLLM pod that attaches pre-signed payment headers to paid upstream requests. |
| **Remote signer** | In-cluster signing service used by OpenClaw and registration flows; separate from the buyer sidecar. |
| **AGENT_BASE_URL** | Environment variable injected into the default agent deployment so generated registration documents use the current tunnel URL. |

### 1.5 System Constraints

| Constraint | Detail |
|-----------|--------|
| **Local-first execution** | The operator machine is the source of truth; cluster state, skills, wallet material, and configuration are rooted in local XDG paths. |
| **Actor priority** | The local operator path takes precedence over agent-developer ergonomics, which in turn take precedence over remote-buyer convenience. |
| **Backend exclusivity** | The stack supports exactly one active backend per config directory: `k3d` or `k3s`. Backend switching must tear down the old cluster first. |
| **Public exposure is optional** | The quick tunnel is dormant by default and only activates on sell flows unless a persistent DNS tunnel was provisioned. |
| **Chain domains are distinct** | Local installable networks, eRPC remote RPC aliases, sell-side payment chains, and ERC-8004 registration networks are related but not interchangeable. |
| **Least-public routing** | Frontend, eRPC, monitoring, and other operator surfaces are local-only under `hostnames: ["obol.stack"]`; public tunnel surfaces are intentionally narrower. |
| **Destructive cleanup is explicit** | `stack purge` preserves data by default; deleting root-owned persistent data requires `--force` and `sudo`. |
| **Phase discipline** | Future work must be recorded in explicit phase sections or ADR follow-ups, not blended into current-shipping behavior. |

---

## 2. System Architecture

### 2.1 High-Level Overview

Obol Stack is a single-node, operator-managed platform with three concentric planes:

1. **Control plane**: the `obol` CLI and XDG filesystem state.
2. **Cluster plane**: Traefik, LiteLLM, eRPC, OpenClaw, x402 services, frontend, monitoring, and Cloudflare tunnel connector.
3. **External plane**: Ollama and cloud LLM providers, ChainList, x402 facilitator, Cloudflare, and EVM chains used for payment or registration.

### 2.2 Module Decomposition

| Module | Purpose | Key Dependencies |
|--------|---------|-----------------|
| `cmd/obol` | User-facing CLI surface | `internal/*`, `urfave/cli/v3` |
| `internal/stack` | Stack init/up/down/purge, backend management, default infra sync | `internal/embed`, `internal/model`, `internal/openclaw`, `internal/agent`, `internal/tunnel` |
| `internal/model` | LiteLLM provider configuration and model synchronization | Kubernetes ConfigMaps/Secrets, Ollama, cloud APIs |
| `internal/network` | Local node deployment and eRPC remote upstream management | Embedded network charts, ChainList, eRPC ConfigMap |
| `internal/openclaw` | Instance onboarding, overlays, dashboard, token, skills, wallet flows | Helmfile, embedded skills, DNS, LiteLLM |
| `internal/agent` | Elevates the default OpenClaw instance with monetization RBAC and heartbeat behavior | Kubernetes RBAC, local data volume |
| `internal/x402` | Sell-side verifier, pricing config, watcher, setup, metrics | x402 facilitator, Traefik ForwardAuth |
| `internal/x402/buyer` | Buy-side paid upstream proxy with pre-signed auth pools | LiteLLM, remote sellers, ConfigMaps |
| `internal/erc8004` | Registration clients, network registry, types, signer integration | eRPC, registry contracts, remote signer |
| `internal/tunnel` | Quick and DNS Cloudflare tunnel lifecycle | cloudflared, Cloudflare APIs, frontend ConfigMap |
| `internal/app` | Managed application install/sync/list/delete | ArtifactHub, OCI/HTTP charts, Helmfile |

### 2.3 Critical Lifecycles

#### 2.3.1 Operator Startup Lifecycle

1. `obol stack init` creates config directories, chooses a backend, writes `.stack-id` and `.stack-backend`, and materializes default infrastructure templates.
2. `obol stack up` starts the local cluster and writes `kubeconfig.yaml`.
3. `syncDefaults()` deploys baseline infrastructure via Helmfile.
4. `autoConfigureLLM()` patches LiteLLM for detected Ollama models and imported cloud credentials.
5. `openclaw.SetupDefault()` creates or re-syncs the default `obol-agent` instance.
6. `agent.Init()` patches monetization RBAC and injects `HEARTBEAT.md`.
7. DNS is configured for `obol.stack` and, if provisioned, a persistent tunnel is started.

#### 2.3.2 Sell-Side Lifecycle

1. The operator creates a sell surface using `obol sell http ...` or `obol sell inference ...`.
2. A `ServiceOffer` CR or host-side gateway deployment is created and persisted.
3. The `monetize.py` reconciler evaluates the offer through `ModelReady`, `UpstreamHealthy`, `PaymentGateReady`, `RoutePublished`, `Registered`, and `Ready`.
4. Traefik routes public traffic through x402 ForwardAuth.
5. If registration is enabled, `/.well-known/agent-registration.json` is published and on-chain registration is attempted.

#### 2.3.3 Buy-Side Lifecycle

1. The agent probes a seller to read its 402 pricing response.
2. The agent pre-signs a bounded batch of ERC-3009 authorizations through the remote signer.
3. Buyer config and auth pools are stored in `llm` namespace ConfigMaps.
4. LiteLLM receives a request for `paid/<remote-model>` and forwards to the local sidecar.
5. The sidecar retries the upstream request with `X-PAYMENT`, consumes one auth, and tracks remaining budget.

---

## 3. Core Subsystems

### 3.1 Stack Lifecycle

#### 3.1.1 Purpose

Provide a single CLI-managed entry point for provisioning, starting, stopping, and destroying the full local stack.

#### 3.1.2 Inputs and Outputs

Inputs:
- XDG or `OBOL_*` path environment variables.
- Backend selection (`k3d` or `k3s`).
- Local prerequisites such as Docker or local filesystem access.

Outputs:
- Stack config under `$OBOL_CONFIG_DIR`.
- Persistent data under `$OBOL_DATA_DIR`.
- Runtime state under `$OBOL_STATE_DIR`.
- A working kubeconfig and running cluster.

#### 3.1.3 Startup Sequence

`stack up` is intentionally opinionated:
- Start backend.
- Write kubeconfig.
- Run Helmfile over embedded defaults.
- Auto-configure LiteLLM.
- Create or refresh default OpenClaw.
- Apply agent capabilities.
- Configure local DNS.
- Start tunnel only when persistent DNS state already exists.

A Helmfile failure is treated as fatal and triggers an automatic `stack down` cleanup path.

#### 3.1.4 Shutdown and Purge

- `stack down` stops the cluster and DNS helper but preserves config and data.
- `stack purge` destroys cluster state and removes config.
- `stack purge --force` additionally removes persistent data and prompts for wallet backup before destruction.

### 3.2 LLM Routing and Provider Management

#### 3.2.1 Purpose

Centralize model routing through one OpenAI-compatible gateway so OpenClaw instances and paid model paths use a single runtime interface.

#### 3.2.2 Provider Model

Supported provider classes on this branch:
- `ollama`
- `anthropic`
- `openai`
- custom OpenAI-compatible endpoints

Key properties:
- LiteLLM config lives in `litellm-config` ConfigMap in namespace `llm`.
- Provider secrets live in `litellm-secrets`.
- Auto-discovery during `stack up` is best-effort, not required for later manual setup.
- After provider changes, configured models are synchronized back into OpenClaw overlays to avoid route drift.

#### 3.2.3 Static Paid Namespace

The buy-side path is intentionally static:
- Public model names are always `paid/<remote-model>`.
- LiteLLM keeps a permanent wildcard route.
- Purchased model changes update buyer ConfigMaps, not LiteLLM topology.

This keeps the payment path isolated from the rest of model routing.

### 3.3 Network Management and eRPC

#### 3.3.1 Chain Domains

Obol Stack uses four separate chain domains:

| Domain | Current Source of Truth | Examples |
|-------|--------------------------|----------|
| Local installable networks | `internal/embed/networks/` | `ethereum`, `aztec` |
| eRPC remote RPC aliases | `internal/network/chainlist.go` | `base`, `mainnet`, `polygon`, `avalanche`, `hoodi` |
| Sell-side payment chains | `cmd/obol/sell.go` | `base-sepolia`, `base`, `ethereum` |
| ERC-8004 registration chains | `internal/erc8004/networks.go` | `base-sepolia`, `base`, `ethereum` |

Documentation and behavior must not collapse these into a single “supported networks” statement.

#### 3.3.2 Local Networks

Local installable networks are embedded Helmfile/chart bundles. On this branch:
- `ethereum`
- `aztec`

`obol network install <network>` renders `values.yaml` from annotated templates, copies the network bundle into `$OBOL_CONFIG_DIR/networks/<network>/<id>/`, and waits for explicit `network sync` to deploy it.

#### 3.3.3 Remote RPC Networks

`obol network add <chain-name-or-id>` uses ChainList to fetch public HTTPS RPCs, filters and ranks them, and writes them into eRPC configuration. By default:
- only free/public HTTPS endpoints are accepted
- full-tracking endpoints are rejected
- write methods remain blocked

`network remove` removes ChainList-sourced upstreams for a chain without touching local node upstreams or custom endpoints.

#### 3.3.4 Route Exposure

eRPC is exposed locally at `http://obol.stack/rpc` behind Traefik. Traffic is still passed through the x402 middleware path, but the verifier returns `200` for unmatched routes or routes with no active pricing rule.

### 3.4 OpenClaw Runtime and Agent Capabilities

#### 3.4.1 Purpose

Manage AI agent instances as first-class stack workloads with operator-controlled overlays, credentials, skills, and wallets.

#### 3.4.2 Instance Model

OpenClaw instances are stored under:
- `$OBOL_CONFIG_DIR/applications/openclaw/<id>/`

Each instance has:
- Helmfile deployment metadata
- Obol overlay values
- optional imported provider/channel settings
- skill injection into persistent volume paths

The canonical default instance is `obol-agent`. It is re-synced idempotently by `stack up`.

#### 3.4.3 Agent Elevation

`agent.Init()` does not create a separate controller binary. Instead it:
- patches monetization ClusterRoleBindings and a pricing RoleBinding
- injects `HEARTBEAT.md` into the default agent workspace so heartbeat cycles run `monetize.py process --all --quick`

This makes monetization behavior part of the default agent runtime, not a parallel control plane.

#### 3.4.4 Operator Surfaces

Key instance operations:
- onboard or scaffold
- sync
- retrieve or regenerate gateway token
- open dashboard
- manage skills
- backup or restore wallet material
- shell out to the embedded OpenClaw CLI

### 3.5 Sell-Side Monetization

#### 3.5.1 Purpose

Expose local services through x402 payment gates and optional ERC-8004 public discovery without requiring a separate Kubernetes operator binary.

#### 3.5.2 Operator Commands

Current sell-side CLI surface:
- `sell inference`
- `sell http`
- `sell list`
- `sell status`
- `sell probe`
- `sell stop`
- `sell delete`
- `sell pricing`
- `sell register`

#### 3.5.3 ServiceOffer CRD

`ServiceOffer` is the declarative contract for sell-side workloads. Required fields are:
- `spec.upstream`
- `spec.payment`

Optional but meaningful fields are:
- `spec.type`
- `spec.model`
- `spec.provenance`
- `spec.path`
- `spec.registration`

Status includes:
- `conditions[]`
- `endpoint`
- `agentId`
- `registrationTxHash`

#### 3.5.4 Reconciliation Stages

The current skill-driven reconcile loop uses these stages:
1. `ModelReady`
2. `UpstreamHealthy`
3. `PaymentGateReady`
4. `RoutePublished`
5. `Registered`
6. `Ready`

Registration is intentionally degradable. If the signer, RPC path, or gas funding is unavailable, the service can remain public and payment-gated with `Registered=True` and reason `OffChainOnly`.

#### 3.5.5 Pricing Models

Current pricing models on this branch:
- `perRequest`
- `perMTok`
- `perHour`
- `perEpoch` in schema, but not a first-class operator flow yet

Current enforcement reality:
- `perRequest` is direct
- `perMTok` is approximated to a request price using `1000` tokens per request
- `perHour` is approximated to a request price using `5` minutes per request in the current monetization skill

These approximations are current implementation behavior, not future exact-metering guarantees.

#### 3.5.6 Standalone Inference Gateway

`sell inference` supports two related paths:
- standalone host-side x402-gated gateway
- cluster-aware mode, where a host-side gateway is wrapped by a `ServiceOffer` and cluster routing

Optional attestation-related inputs already exist on this branch:
- macOS Secure Enclave
- Linux TEE backends
- provenance metadata for experiment output

### 3.6 Buy-Side Remote Inference

#### 3.6.1 Purpose

Allow agents to pay for remote x402-gated inference without giving the runtime access to live signing keys.

#### 3.6.2 Design

The buy-side path uses:
- a pre-signing step through the remote signer
- `x402-buyer-config` ConfigMap
- `x402-buyer-auths` ConfigMap
- an `x402-buyer` sidecar in the LiteLLM pod
- a static public model namespace `paid/<remote-model>`

Runtime properties:
- zero signer access in the sidecar
- bounded spending equal to remaining auth count times unit price
- OpenAI-compatible reverse proxy interfaces
- `/healthz`, `/status`, and `/metrics` endpoints

### 3.7 Tunnel, Discovery, Frontend, and Monitoring

#### 3.7.1 Tunnel Modes

Current tunnel modes:
- `quick`: dormant until a sell flow requires public exposure
- `dns`: persistent hostname-based tunnel created via browser login or API-token provisioning

When a tunnel URL becomes available, the stack updates:
- `AGENT_BASE_URL` on the default agent deployment
- the frontend configuration ConfigMap

#### 3.7.2 Public vs Local Routes

Local-only operator routes:
- `http://obol.stack/`
- `http://obol.stack/rpc`
- monitoring and internal admin surfaces via hostname restriction

Public tunnel routes:
- `/services/<name>/...`
- `/.well-known/agent-registration.json`
- storefront and machine-readable service catalog surfaces

#### 3.7.3 Frontend and Monitoring

The stack ships:
- `obol-frontend` namespace for the dashboard
- `monitoring` namespace with kube-prometheus-stack
- a PodMonitor for the buyer sidecar

The frontend is allowed to discover namespaces, pods, ConfigMaps, Secrets, and `ServiceOffer` resources through an explicit ClusterRoleBinding.

### 3.8 Application Management and Supporting Operations

#### 3.8.1 Managed Applications

`obol app install/sync/list/delete` lets operators treat arbitrary Helm charts as managed workloads under `$OBOL_CONFIG_DIR/applications/<app>/<id>/`.

Supported chart references:
- `repo/chart`
- `repo/chart@version`
- `https://.../*.tgz`
- `oci://...`

#### 3.8.2 Supporting Operations

The branch also includes:
- update and upgrade commands
- flow scripts validating sell and buy paths
- optional subprojects such as `reth-erc8004-indexer`
- embedded skills for autoresearch-related workloads

These supporting operations are part of the repository surface, but not all of them are yet first-class operator workflows.

---

## 4. API / Protocol Definition

### 4.1 CLI Surface

| Surface | Current Commands |
|--------|-------------------|
| Stack | `stack init`, `stack up`, `stack down`, `stack purge` |
| Agent | `agent init` |
| Models | `model setup`, `model status`, `model sync`, `model pull`, `model list`, `model remove` |
| Networks | `network list`, `network install`, `network sync`, `network delete`, `network add`, `network remove`, `network status` |
| OpenClaw | `openclaw onboard`, `sync`, `token`, `list`, `delete`, `setup`, `dashboard`, `skills`, `wallet`, `cli` |
| Sell | `sell inference`, `http`, `list`, `status`, `probe`, `stop`, `delete`, `pricing`, `register` |
| Tunnel | `tunnel status`, `login`, `provision`, `restart`, `stop`, `logs` |
| Apps | `app install`, `sync`, `list`, `delete` |
| Operations | `update`, `upgrade`, `version`, Kubernetes passthrough commands |

### 4.2 Kubernetes API and CRDs

| Interface | Kind | Purpose |
|----------|------|---------|
| `obol.org/v1alpha1` | `ServiceOffer` | Declares sell-side services, pricing, provenance, and registration metadata |
| `gateway.networking.k8s.io/v1` | `HTTPRoute` | Exposes frontend, eRPC, public services, and registration document routes |
| `traefik.io/v1alpha1` | `Middleware` | ForwardAuth integration for x402 payment checks |
| `monitoring.coreos.com/v1` | `PodMonitor` | Scrapes buyer sidecar metrics |

### 4.3 HTTP and Routing Surfaces

| Surface | Location | Audience | Notes |
|--------|----------|----------|------|
| Frontend | `http://obol.stack/` | Local operator | Local-only hostname restriction |
| eRPC | `http://obol.stack/rpc` | Local operator, agent workloads | Route goes through Traefik middleware path |
| Public service routes | `https://<tunnel>/services/<name>/...` | Remote buyer | x402-gated |
| Registration document | `https://<tunnel>/.well-known/agent-registration.json` | Discovery clients | Public, no ForwardAuth |
| Buyer sidecar health | `http://127.0.0.1:8402/healthz` | In-cluster | Sidecar-local |
| Buyer sidecar status | `http://127.0.0.1:8402/status` | In-cluster | Sidecar-local |
| Buyer metrics | `/metrics` on buyer sidecar | Monitoring | Scraped by PodMonitor |

### 4.4 Authentication and Authorization

- OpenClaw dashboard and API access use a per-instance gateway token retrievable from `obol openclaw token`.
- Public sell-side routes rely on x402 payment verification rather than a user session.
- Kubernetes mutating actions are performed through local operator credentials or specific service accounts with explicit RBAC.
- The buyer sidecar authenticates payments with pre-signed vouchers, not a live signer.

### 4.5 Rate Limiting and Quotas

There is no global user quota service in the current branch. Effective limits are:
- finite pre-signed auth pools on the buy side
- route-level pricing configured in x402 verifier
- workload capacity imposed by local cluster resources and upstream services

---

## 5. Data Model

### 5.1 Filesystem Layout

| Path | Purpose |
|------|---------|
| `$OBOL_CONFIG_DIR/.stack-id` | Persistent stack identity |
| `$OBOL_CONFIG_DIR/.stack-backend` | Active backend selection |
| `$OBOL_CONFIG_DIR/kubeconfig.yaml` | Cluster access for passthrough tools and CLI operations |
| `$OBOL_CONFIG_DIR/defaults/` | Rendered default infrastructure bundle |
| `$OBOL_CONFIG_DIR/networks/<type>/<id>/` | Local network deployment config |
| `$OBOL_CONFIG_DIR/applications/<app>/<id>/` | Managed application or OpenClaw instance config |
| `$OBOL_DATA_DIR/` | Persistent volumes, wallet data, OpenClaw workspaces |
| `$OBOL_STATE_DIR/` | Runtime logs and mutable state |

### 5.2 Kubernetes Namespaces and Core Resources

| Namespace | Core Resources | Purpose |
|----------|----------------|---------|
| `traefik` | Traefik, cloudflared, gateway | Ingress and tunnel connector |
| `llm` | LiteLLM, x402-buyer sidecar, buyer PodMonitor | Model gateway and buy-side runtime |
| `erpc` | eRPC, HTTPRoute, metadata ConfigMap | Blockchain RPC gateway |
| `x402` | x402-verifier, pricing config | Sell-side payment verification |
| `openclaw-obol-agent` | Default OpenClaw agent, remote signer | Canonical agent runtime |
| `openclaw-<id>` | Additional OpenClaw instances | User-managed agent runtimes |
| `obol-frontend` | Frontend deployment, HTTPRoute, RBAC | Dashboard |
| `monitoring` | Prometheus stack | Metrics and observability |
| `reloader` | Stakater Reloader | Config/secret-triggered restarts |

### 5.3 Key ConfigMaps, Secrets, and Documents

| Object | Purpose |
|-------|---------|
| `litellm-config` | Model routing table for LiteLLM |
| `litellm-secrets` | Cloud provider API keys |
| `erpc-config` | eRPC upstream and network definitions |
| `obol-stack-config` | Frontend-readable stack metadata, including tunnel URL |
| `x402-pricing` | Sell-side route pricing for the verifier |
| `x402-buyer-config` | Buy-side upstream mapping |
| `x402-buyer-auths` | Pre-signed authorization pools |
| `cloudflared-tunnel-token` | DNS tunnel token for persistent Cloudflare tunnel |
| `so-<name>-registration` ConfigMap | Generated `agent-registration.json` for a ServiceOffer |

### 5.4 Data Lifecycle

- Stack identity and backend selection are created at `stack init` and persist until purge.
- Local network and application configs are created before cluster deployment and reused across syncs.
- Wallet material persists across `stack down` and ordinary deletes; explicit backup and restore flows exist for OpenClaw wallets.
- Buy-side auth pools are consumed monotonically and must be refilled.
- Registration JSON is regenerated when a ServiceOffer changes or registration status changes.

---

## 6. Integration Points

| System | Protocol | Purpose | Failure Mode |
|--------|----------|---------|-------------|
| Ollama | HTTP | Local model serving and discovery | Auto-config skips or later requests fail until operator configures a provider |
| Anthropic / OpenAI | HTTPS | Cloud model routing through LiteLLM | Provider remains unavailable; other routes continue |
| ChainList | HTTPS | Public RPC discovery for eRPC | `network add` fails or requires custom endpoint |
| x402 facilitator | HTTPS | Payment verification and settlement | Sell-side requests fail verification or operator runs verify-only/local test paths |
| Cloudflare | Browser auth, API, tunnel transport | Public exposure | Stack remains locally usable without tunnel |
| EVM chains via eRPC | JSON-RPC | Payments, registration, discovery queries | Registration degrades to off-chain or buyer/seller requests fail upstream |
| ArtifactHub / chart repos / OCI | HTTPS | Managed app installation | App install fails; core stack remains unaffected |

---

## 7. Security Model

### 7.1 Threat Model

Primary threats:
- accidental public exposure of operator-only routes
- live signing key exposure to runtime components
- unintended mainnet write forwarding
- silent documentation drift that misstates operator guarantees
- orphaned or half-started infrastructure after failed deploys

### 7.2 Wallet and Signing Boundaries

- The buyer sidecar has no live signer access.
- Registration and other signing flows prefer the remote signer and may fall back to a private key file only when explicitly invoked.
- Secure Enclave and Linux TEE support exist for standalone inference paths, but are optional.

### 7.3 Public Exposure Guardrails

- Frontend, eRPC, monitoring, and similar operator surfaces are local-only under `obol.stack`.
- Public tunnel routes are intentionally narrower and centered on payment-gated services and discovery metadata.
- Quick tunnels are not started eagerly on `stack up`.

### 7.4 Payment Trust Model

- x402 payment proofs are verified through a facilitator.
- Non-HTTPS facilitator URLs are rejected except for loopback and container-internal development hosts.
- Route matching is explicit; unmatched routes pass through without payment requirements.

### 7.5 RBAC Model

- Default agent elevation is explicit and applied by `agent.Init()`.
- Frontend has a narrow but meaningful ClusterRole for discovery and `ServiceOffer` CRUD.
- Sell-side resource cleanup relies on namespaced ownership and cluster-scoped permissions where required.

---

## 8. Error Handling

### 8.1 Error Categories

| Category | Example | Handling |
|----------|---------|---------|
| Prerequisite failure | backend missing, cluster not running | CLI exits non-zero with remediation hint |
| Partial deployment failure | Helmfile sync fails | stack auto-runs cleanup path |
| Unsupported chain-domain input | using an eRPC-only alias in a sell-side command | command fails with supported chain list |
| Upstream health failure | `ServiceOffer` upstream is unhealthy | reconcile stops before route publish |
| Registration failure | signer unavailable or wallet unfunded | degrade to `OffChainOnly` where supported |
| Buyer budget exhaustion | no remaining auths for `paid/<model>` | request path fails until refill |
| Tunnel unavailability | quick or DNS tunnel cannot start | local stack remains usable; public path degraded |

### 8.2 Error Response Contracts

- CLI failures are non-zero exits with human-readable hints.
- x402 verifier emits HTTP `402 Payment Required` with pricing metadata.
- Buy-side proxy returns HTTP `404` when no purchased upstream matches the requested `paid/<model>`.
- Registration degradation is recorded in `ServiceOffer.status.conditions`.

### 8.3 Retry and Recovery

- Model and provider configuration can be re-run safely.
- Network sync and app sync are explicit operator actions.
- Buyer auth pools can be refilled without rebuilding LiteLLM topology.
- Tunnel restarts are explicit and cheap for quick tunnels.

---

## 9. Performance and Operations

### 9.1 Operational Bounds

| Metric | Current Bound | Measurement |
|--------|---------------|-------------|
| ChainList fetch timeout | 15 seconds | `internal/network/chainlist.go` timeout |
| Tunnel rollout wait | 30 seconds | `tunnel.EnsureRunning()` rollout status |
| LiteLLM rollout wait | 90 seconds | `model.RestartLiteLLM()` rollout status |
| Buyer metrics scrape interval | 30 seconds | PodMonitor definition |
| `perMTok` approximation | 1000 tokens/request | monetization skill constant |
| `perHour` approximation | 5 minutes/request | monetization skill constant |

### 9.2 Observability

- Prometheus stack is part of the default infrastructure.
- Buyer sidecar exports metrics for auth pools and payment attempts.
- Tunnel status, OpenClaw token flows, and sell status all have dedicated CLI surfaces.

---

## 10. Phased Rollout

### Phase 1: PR288 Baseline

- Local-first stack lifecycle with `k3d` and `k3s`
- Default infrastructure deployment through Helmfile
- LiteLLM as the central model gateway
- eRPC local and remote RPC management
- OpenClaw instance lifecycle and the elevated `obol-agent`
- Sell-side x402 routes, `ServiceOffer` reconcile loop, `sell probe`, and optional ERC-8004 registration
- Buy-side `paid/*` remote inference path with bounded-risk sidecar
- Quick and DNS tunnel modes
- Local frontend and monitoring stack
- Managed application install/sync/list/delete

### Phase 2: Explicit Follow-Ups

- Replace approximation-based pricing for `perMTok` and `perHour` with exact metering where supported.
- Add operator-safe JSON, headless, and introspection surfaces to the CLI before promoting broader agent or MCP control paths.
- Package `reth-erc8004-indexer` as a first-class managed application instead of a repository-adjacent subproject.
- Promote autoresearch worker and coordinator workflows from skill-level building blocks into operator-visible flows with clearer provenance surfaces.
- Tighten reconcile and heartbeat latency rather than relying on the current default cadence.
- Extend and document multi-chain sell-side support only when the CLI, verifier, and registration surfaces agree on the contract.
- Extend monetized publication beyond the current inference-centric path only after explicit isolation, ownership, and routing rules are specified.
- Validate the buy-side path more deeply through LiteLLM-routed hands-off tests and in-pod skill smoke coverage.
- Enforce canonical spec drift checks through Codex hooks and CI.

---

## 11. Testing Strategy

### 11.1 Test Levels

| Level | Tooling | What It Covers |
|-------|---------|----------------|
| Unit | `go test ./...` | Core package logic, serializers, matchers, config handling |
| Integration | package-level integration tests | Kubernetes-backed paths, OpenClaw flows, x402 verifier paths |
| Flow / E2E | `flows/flow-06`, `07`, `08`, `10` | Sell setup, verify, buy path, anvil facilitator loop |
| Skill smoke | `tests/skills_smoke_test.py`, focused Python tests | Embedded skill assets and runtime contracts |
| BDD spec | `features/*.feature` plus existing executable features | Behavioral contract for current and future implementation |

### 11.2 Test Data Strategy

- Use deterministic local stack IDs and local config dirs in tests where possible.
- Prefer fixture-based ChainList data for RPC selection tests.
- Treat real public tunnel URLs, facilitator endpoints, and on-chain registration as integration concerns, not unit-test assumptions.

### 11.3 CI/CD Integration

- Code changes that alter the operator, agent, buyer, seller, tunnel, or routing contract must update this root-level bundle.
- Operator guides in `docs/guides/` may remain for context, but they are not authoritative once this bundle exists.
