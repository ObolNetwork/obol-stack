# Obol Stack Technical Specification

**Version**: 1.0.0-draft
**Status**: Draft
**Last Updated**: 2026-05-20
**Audience**: coding agents and maintainers

This is the authoritative, token-efficient implementation spec for Obol Stack. Prefer section IDs over copied prose when prompting agents. Visuals live in [ARCHITECTURE.md](ARCHITECTURE.md). Test oracles live in [BEHAVIORS_AND_EXPECTATIONS.md](BEHAVIORS_AND_EXPECTATIONS.md) and [features/](features/).

## 1. Scope

### 1.1 Product Contract

Obol Stack is a local-first Kubernetes stack that lets AI agents run infrastructure, expose paid services, publish discovery metadata, and buy paid remote inference through x402.

| ID | In Scope |
|----|----------|
| S1 | Local stack lifecycle on k3d or standalone k3s. |
| S2 | LiteLLM model routing over host Ollama, cloud providers, and purchased `paid/<model>` routes. |
| S3 | Hermes as default agent runtime; OpenClaw remains supported as a legacy/optional runtime. |
| S4 | Declarative `Agent` CRD for durable Hermes sub-agents with seeded skills, soul, PVC, API key, and optional wallet. |
| S5 | Sell-side `ServiceOffer` CRD for x402-gated HTTP, inference, fine-tuning, and agent-backed services. |
| S6 | Buy-side `PurchaseRequest` CRD as a bounded pre-signed auth pool for paid inference. |
| S7 | ERC-8004 identity publication through `AgentIdentity` and `RegistrationRequest`. |
| S8 | Public Cloudflare tunnel, storefront, `/skill.md`, `/api/services.json`, and `/.well-known/agent-registration.json`. |
| S9 | Release and smoke validation through `flows/` and Go tests. |

| ID | Anti-Scope |
|----|------------|
| A1 | No centralized service bazaar or custodial marketplace. Discovery is standards-native. |
| A2 | No new smart contracts for the MVP. Use deployed ERC-20, Permit2, x402, and ERC-8004 contracts only. |
| A3 | No slashing, escrow, or court/arbitration logic in phase 1. Reputation and future income are the penalty surface. |
| A4 | `PurchaseRequest` is not escrow. It is a bounded pre-signed authorization pool. |
| A5 | Raw direct `X-PAYMENT` through Traefik ForwardAuth is not the production buy path. Use `x402-buyer` or standalone `sell inference`. |
| A6 | `spec.skills` seeds an agent. It is not a security sandbox. |
| A7 | The stack does not guarantee medical, legal, or other regulated policy behavior without explicit skills and middleware. |

### 1.2 Actors

| Actor | Goal | Primary Interfaces |
|-------|------|--------------------|
| Operator | Run stack, models, agents, tunnel. | `obol stack`, `obol model`, `obol agent`, `obol tunnel` |
| Service Provider | Sell model, HTTP, or agent turns. | `obol sell *`, `ServiceOffer` |
| Buyer Agent | Discover, verify, and pay for remote services. | `obol buy inference`, `buy-x402`, `PurchaseRequest` |
| Controller | Reconcile CRDs into cluster resources and status. | informers, dynamic client |
| x402 Facilitator | Verify and settle token auths. | x402 HTTP API |
| ERC-8004 Indexer | Discover agent identity and services. | on-chain registry plus registration JSON |

### 1.3 Glossary

| Term | Definition |
|------|------------|
| Agent | Namespaced `obol.org/v1alpha1` CRD representing a durable Hermes runtime. |
| AgentIdentity | Durable operator identity document. Outlives ServiceOffers and can tombstone. |
| Agent Resolution | Controller-populated view of an agent offer: model, skills, runtime, endpoint. |
| ForwardAuth | Traefik auth hook used by `x402-verifier` to deny unpaid requests with HTTP 402. |
| Hermes | Default agent runtime image `nousresearch/hermes-agent:v2026.5.7`. |
| Mother Agent | Operator-controlled agent that may create child `Agent` CRs. Product concept; current code exposes the CRD and CLI substrate. |
| PurchaseRequest | Buyer-side CRD declaring endpoint, model, payment terms, and pre-signed auth pool. |
| ServiceOffer | Seller-side CRD declaring paid service intent and discovery metadata. |
| x402-buyer | LiteLLM sidecar that spends pre-signed auths and proxies paid inference. |
| x402-verifier | Sell-side verifier/proxy that emits 402 and gates or settles paid routes. |

### 1.4 Hard Constraints

| ID | Constraint | Impact |
|----|------------|--------|
| C1 | No additional smart contracts. | Use token registry, Permit2/EIP-3009, ERC-8004 Identity Registry. |
| C2 | Local-first stack. | State is local config, host data dirs, Kubernetes CRDs, PVCs, ConfigMaps, Secrets. |
| C3 | Seller gets 100 percent x402 payment. | No protocol middleman fee logic in stack. |
| C4 | Buyer runtime has zero signer access. | Agent signs auths before creating `PurchaseRequest`; sidecar only consumes auths. |
| C5 | Public tunnel must expose only safe surfaces. | Public routes: `/services/*`, `/skill.md`, `/api/services.json`, `/.well-known/agent-registration.json`, storefront `/`. |
| C6 | Agent skills are seed data. | Do not advertise `spec.skills` as confinement. Use separate sandbox/policy work for regulated services. |
| C7 | Single x402-buyer pod. | LiteLLM deployment remains replicas=1 while consumed-auth state is pod-local. |
| C8 | Controller is source of status truth. | CLIs apply intent and read status; they do not mark resources ready. |
| C9 | Token metadata must be explicit for non-default assets. | OBOL offers must include asset address, decimals, transfer method, EIP-712 metadata. |
| C10 | Specs are coding-agent inputs. | Keep docs concise, stable IDs, minimal examples, no long narrative duplication. |

## 2. System Map

### 2.1 Modules

| ID | Module | Responsibility | Key Paths |
|----|--------|----------------|-----------|
| M1 | CLI | User command surface. | `cmd/obol/*.go` |
| M2 | Stack lifecycle | k3d/k3s init/up/down/purge, defaults sync, host DNS. | `internal/stack/`, `internal/defaults/`, `internal/dns/` |
| M3 | Model routing | LiteLLM config, providers, rank/prefer, default Hermes model sync. | `internal/model/`, `internal/embed/infrastructure/base/templates/llm.yaml` |
| M4 | Runtime onboarding | Legacy Hermes/OpenClaw instances and default obol-agent. | `internal/hermes/`, `internal/openclaw/`, `internal/agentruntime/` |
| M5 | Agent CRD | Host seed plus controller-provisioned Hermes child runtime. | `internal/agentcrd/`, `internal/serviceoffercontroller/agent*.go` |
| M6 | Sell control plane | Reconcile ServiceOffers into routes, gates, registration, catalogs. | `internal/serviceoffercontroller/`, `internal/monetizeapi/` |
| M7 | Sell data plane | x402 verifier/proxy, 402 metadata, route matching, settlement. | `cmd/x402-verifier/`, `internal/x402/` |
| M8 | Buy control plane | PurchaseRequest reconcile into buyer config/auths and LiteLLM paid route. | `internal/serviceoffercontroller/purchase*.go` |
| M9 | Buy data plane | Sidecar proxy that spends auths and calls paid upstreams. | `cmd/x402-buyer/`, `internal/x402/buyer/` |
| M10 | ERC-8004 | Registration ABI/client, `.well-known` document, on-chain status. | `internal/erc8004/`, `internal/serviceoffercontroller/identity*.go` |
| M11 | Tunnel/storefront | Cloudflared quick/persistent tunnel and public catalog UI. | `internal/tunnel/`, `web/public-storefront/` |
| M12 | QA | User-journey flows and integration tests. | `flows/`, `internal/*_test.go` |

### 2.2 Runtime Namespaces

| Namespace | Owner | Main Resources |
|-----------|-------|----------------|
| `llm` | base stack | `ollama` Service+Endpoints, LiteLLM, `x402-buyer`, buyer ConfigMaps |
| `x402` | base stack | `x402-verifier`, `serviceoffer-controller`, catalogs, default `AgentIdentity` |
| `traefik` | base stack | Gateway, cloudflared, public storefront |
| `hermes-obol-agent` | default agent | master Hermes, remote-signer |
| `agent-<name>` | Agent CRD | child Hermes, PVC, API Secret, optional remote-signer |
| service namespace | seller | upstream service and ServiceOffer when not agent-backed |

## 3. External Interfaces

### 3.1 CLI Surface

| Area | Commands | Spec Sections |
|------|----------|---------------|
| Stack | `obol stack init/up/down/purge/status` | 5.1 |
| Models | `obol model setup/status/token/sync/pull/list/prefer/discover/remove` | 5.2 |
| Agents | `obol agent init/new/update/sync/setup/auth/list/delete/wallet` | 5.3 |
| Sell | `obol sell inference/http/agent/demo/list/status/test/stop/update/delete/pricing/register/identity/info` | 5.4, 5.5 |
| Buy | `obol buy inference` | 5.6 |
| Tunnel | `obol tunnel status/restart/stop/logs/login/provision/setup` | 5.8 |
| Networks/Apps | `obol network *`, `obol app *` | existing README/CLI help until this bundle is extended |

### 3.2 Public HTTP Surface

| Path | Producer | Purpose | Public? |
|------|----------|---------|---------|
| `/services/<name>/*` | ServiceOffer HTTPRoute | Paid x402 service route. | Yes |
| `/skill.md` | serviceoffer-controller | Markdown service catalog. | Yes |
| `/api/services.json` | serviceoffer-controller | Machine-readable service catalog. | Yes |
| `/.well-known/agent-registration.json` | AgentIdentity renderer | ERC-8004 registration document. | Yes |
| `/` | public storefront | Catalog UI for tunnel hostname. | Yes |
| `/rpc`, frontend, LiteLLM | base infra | Local stack surfaces. | No public tunnel exposure |

### 3.3 x402 402 Contract

`x402-verifier` returns `x402Version: 2`, `accepts[]`, and asset extensions. For agent-backed offers it must add:

| Field | Source |
|-------|--------|
| `accepts[].extra.agentModel` | `ServiceOffer.status.agentResolution.model` |
| `accepts[].extra.agentSkills` | `ServiceOffer.status.agentResolution.skills` |
| `accepts[].extra.agentRuntime` | `ServiceOffer.status.agentResolution.runtime` |
| asset transfer method/domain | `ServiceOffer.spec.payment.asset` or chain default |

## 4. Data Model

### 4.1 ServiceOffer

Group/version/kind: `obol.org/v1alpha1`, `ServiceOffer`, namespaced.

| Field | Required | Meaning |
|-------|----------|---------|
| `spec.type` | no | `http`, `inference`, `fine-tuning`, `agent`; default `http`. |
| `spec.agent.ref.name/namespace` | agent only | Referenced `Agent` CR. |
| `spec.model.name/runtime` | inference/fine-tuning | LLM metadata. Agent offers derive this from Agent status. |
| `spec.upstream.service/namespace/port` | non-agent | In-cluster backend service. |
| `spec.upstream.healthPath` | no | Health probe path; effective default `/`. CRD default says `/health`. |
| `spec.payment.network/payTo/price` | yes | x402 payment terms. |
| `spec.payment.asset.*` | no | Explicit settlement token metadata. Needed for OBOL/Permit2. |
| `spec.path` | no | Public prefix; default `/services/<name>`. |
| `spec.registration.*` | no | ERC-8004 metadata and OASF discovery tags. |
| `spec.provenance` | no | Experiment/model provenance for registration/catalog. |

Conditions: `ModelReady`, `UpstreamHealthy`, `PaymentGateReady`, `RoutePublished`, `Registered`, `Ready`.

Agent-only status: `status.agentResolution.{model,skills,runtime,endpoint}`.

### 4.2 Agent

Group/version/kind: `obol.org/v1alpha1`, `Agent`, namespaced. CLI convention: name `<name>`, namespace `agent-<name>`.

| Field | Meaning |
|-------|---------|
| `spec.runtime` | `hermes` only today. |
| `spec.model` | LiteLLM model to pin. Empty keeps status unready until pinned. |
| `spec.skills[]` | Seeded embedded skills. Not sandboxing. |
| `spec.objective` | Seed text rendered into `soul.md` on first host seed. |
| `spec.wallet.create` | Provision per-agent remote-signer and publish `status.walletAddress`. |
| `status.phase` | `Pending`, `Provisioning`, `Ready`, `Failed`. |
| `status.pinnedModel` | Effective model in use. |
| `status.endpoint` | Internal Hermes URL, usually `http://hermes.<ns>.svc.cluster.local:8642`. |

Agent child resources: Namespace, ServiceAccount, PVC `hermes-data`, ConfigMap `hermes-config`, Secret `hermes-api-server`, Deployment `hermes`, Service `hermes`, optional remote-signer Secret/Deployment/Service.

### 4.3 PurchaseRequest

Group/version/kind: `obol.org/v1alpha1`, `PurchaseRequest`, namespaced.

| Field | Required | Meaning |
|-------|----------|---------|
| `spec.endpoint` | yes | Full x402-gated inference endpoint. |
| `spec.model` | yes | Remote model exposed locally as `paid/<model>`. |
| `spec.count` | yes | Intended auth count, max 2500. |
| `spec.preSignedAuths[]` | runtime required | Fully signed x402 payments; legacy ERC-3009 fields still supported. |
| `spec.autoRefill.*` | no | Agent-managed refill intent. Controller does not sign. |
| `spec.payment.*` | yes | Network, payTo, atomic price, asset metadata. |
| `status.publicModel` | output | Published LiteLLM model name. |
| `status.remaining/spent` | output | Reconciled sidecar counters, not a live source of truth. |

Reconcile stages: `Probed`, `AuthsLoaded`, `Configured`, `Ready`, plus `Deleting` during drain.

### 4.4 RegistrationRequest

Group/version/kind: `obol.org/v1alpha1`, `RegistrationRequest`, namespaced.

| Field | Meaning |
|-------|---------|
| `spec.serviceOfferName/Namespace` | Owning offer. |
| `spec.desiredState` | `Active` or `Tombstoned`. |
| `spec.chain` | ERC-8004 network alias. |
| `status.phase` | `Publishing`, `Registering`, `AwaitingExternalRegistration`, `Registered`, `OffChainOnly`, `Tombstoned`. |
| `status.publishedUrl` | Public `.well-known` URL. |
| `status.agentId/registrationTxHash/...` | On-chain registration observation. |

### 4.5 AgentIdentity

Group/version/kind: `obol.org/v1alpha1`, `AgentIdentity`, namespaced. Default identity: `x402/default`.

| Field | Meaning |
|-------|---------|
| `spec` | Empty by design today. |
| `status.registrations[].chain` | Registration chain alias. |
| `status.registrations[].agentId` | ERC-8004 token ID for that chain. |

Deleting the last offer must tombstone the document instead of deleting identity history.

### 4.6 Service Catalog JSON

`/api/services.json` is an array of `schemas.ServiceCatalogEntry`.

| Field | Meaning |
|-------|---------|
| `name`, `namespace`, `type`, `model` | Offer identity and model metadata. |
| `endpoint` | Full public service URL. |
| `price`, `priceRaw`, `priceUnit`, `priceAtomicUnits` | Display and signing price data. |
| `payTo`, `network`, `caip2Network`, `chainId` | Payment target and chain. |
| `asset.{address,symbol,decimals,transferMethod,eip712Domain}` | Signing metadata. |
| `registrationPending` | Operationally ready but ERC-8004 tx pending. |

## 5. Core Flows

### 5.1 Stack Lifecycle

1. `obol stack init` writes stack ID, backend config, kube defaults, and embedded infra into config dir.
2. `obol stack up` starts k3d/k3s, writes kubeconfig, refreshes defaults, sets hosts, applies Helmfile/base templates, configures LiteLLM/default agent when possible, and warns if public tunnel URL may change.
3. `obol stack down` stops cluster and DNS resolver after quick-tunnel loss confirmation.
4. `obol stack purge --force` destroys cluster and local config/data after wallet backup prompts.

### 5.2 Model Routing

1. `obol model setup` configures Ollama, Anthropic, OpenAI, or custom providers into LiteLLM.
2. LiteLLM always includes wildcard `paid/* -> openai/* -> http://127.0.0.1:8402/v1`.
3. `obol model prefer` defines order used by agent default selection.
4. `obol model sync` re-renders stack-managed Hermes config from LiteLLM inventory.

### 5.3 Agent CRD Creation

1. `obol agent new <name> --model <m> --skills a,b --objective <text> --create-wallet`.
2. CLI validates name and skills, creates namespace `agent-<name>`, seeds host files under `<dataDir>/agent-<name>/hermes-data/.hermes/`.
3. CLI applies `Agent` CR. Controller adds finalizer, validates, pins model, reads LiteLLM key, applies Hermes resources, optionally provisions remote-signer.
4. Controller sets `status.endpoint`, `status.walletAddress`, and `Ready=True` only when deployment and wallet requirements are satisfied.

### 5.4 Sell HTTP/Inference

1. Seller runs `obol sell http` or `obol sell inference`.
2. CLI resolves token/chain/payTo/pricing, ensures CA bundle, applies `ServiceOffer`.
3. Controller adds finalizer, checks model/upstream, applies x402 `ReferenceGrant`, waits for `x402-verifier`, applies `HTTPRoute`.
4. If registration enabled, controller creates or shares a `RegistrationRequest` and publishes identity resources.
5. Ready requires all six ServiceOffer conditions true. Catalog visibility requires operational readiness: model, upstream, payment gate, route.

### 5.5 Sell Agent

1. Seller creates an `Agent` or lets interactive `obol sell agent <name>` create one.
2. `obol sell agent` builds `ServiceOffer{type: agent, spec.agent.ref}` in the agent namespace. It defaults `payTo` to the agent wallet, then host remote-signer, then requires explicit `--pay-to`.
3. Controller resolves the referenced Agent. If missing/not ready, offer stays `WaitingForAgent`.
4. When Agent is ready, controller synthesizes in-memory upstream/model, writes `status.agentResolution`, and normal ServiceOffer reconciliation proceeds.
5. x402 402 responses must advertise model, skills, and runtime through `accepts[].extra`.

### 5.6 Buy Paid Inference

1. `obol buy inference` probes seller pricing and optionally verifies ERC-8004 identity.
2. CLI dispatches into the default Hermes pod: `python3 buy-x402/scripts/buy.py buy ...`.
3. `buy.py` signs N auths locally and creates/updates a `PurchaseRequest`.
4. Controller probes endpoint, validates price, loads auths, writes one JSON key per purchase into `x402-buyer-config` and `x402-buyer-auths`, hot-adds `paid/<model>` to LiteLLM, and reloads sidecar.
5. Runtime request to LiteLLM with model `paid/<model>` routes to x402-buyer, which attaches `X-PAYMENT`, retries, and marks auths consumed only after attempt handling.

### 5.7 ERC-8004 Registration

1. ServiceOffer registration metadata renders into the shared AgentIdentity document.
2. Controller publishes `/.well-known/agent-registration.json` from a busybox httpd child stack.
3. `obol sell register` submits on-chain registration externally. Controller observes chain via eRPC and writes AgentIdentity status.
4. Multiple offers share `x402/default`. Non-owner offers reference the owner RegistrationRequest status.
5. Tombstone path keeps historical registration but publishes `active:false`, `x402Support:false`.

### 5.8 Tunnel and Storefront

1. `obol tunnel restart`, sell commands, or persistent tunnel setup starts cloudflared.
2. Tunnel URL is injected into default agent and `obol-frontend/obol-stack-config`.
3. `CreateStorefront` deploys Next.js storefront in `traefik`, hostname-pinned to tunnel host.
4. Storefront reads `http://obol-skill-md.x402.svc:8080/api/services.json`.

## 6. Security Model

| Boundary | Risk | Control |
|----------|------|---------|
| Public internet to cluster | Exposing local dashboard/RPC. | Hostname-restricted internal HTTPRoutes; public routes limited by C5. |
| Buyer to seller | Non-payment or replay. | x402 facilitator verification, per-route price/asset, auth consumption tracking. |
| Buyer sidecar | Signer theft. | Sidecar receives only pre-signed auths, no private key or remote-signer URL. |
| Agent runtime | Prompt/tool abuse. | Current MVP seeds skills but does not sandbox; high-risk services require separate policy/sandbox. |
| Controller RBAC | Over-broad cluster mutation. | Controller owns CRD child resources; agent gets minimal compatibility CRUD/read. |
| ERC-8004 identity | Fake service endpoint. | Buyer verifies service endpoint and agent ID against priced chain. |
| OBOL payments | Wrong asset or signing domain. | Token registry and explicit asset metadata in ServiceOffer/PurchaseRequest. |
| Tunnel | Stale quick URL. | Tunnel state sync, restart propagation, quick-tunnel loss warning. |

## 7. Error and Status Contract

### 7.1 ServiceOffer Reasons

| Condition | Common Reasons | Recovery |
|-----------|----------------|----------|
| `ModelReady=False` | `MissingModel`, `WaitingForAgent` | Pin model or wait for Agent. |
| `UpstreamHealthy=False` | `MissingService`, `Unhealthy`, `WaitingForAgent` | Fix Service/health path/runtime. |
| `PaymentGateReady=False` | `WaitingForUpstream`, `ApplyFailed`, `WaitingForGateway`, `Paused` | Fix upstream, verifier, RBAC, or unpause. |
| `RoutePublished=False` | `WaitingForPaymentGate`, `ApplyFailed`, `Paused` | Fix payment gate or route apply issue. |
| `Registered=False` | `Pending`, `AwaitingExternalRegistration`, `IdentityError`, `WaitingForRoute` | Publish/fund/register or inspect identity resources. |
| `Ready=False` | `Reconciling`, `WaitingForAgent` | Inspect the first false dependency condition. |

### 7.2 Agent Reasons

| Condition | Common Reasons | Recovery |
|-----------|----------------|----------|
| `Validated=False` | `UnsupportedRuntime`, `InvalidSkillEntry` | Fix spec. |
| `Provisioned=False` | `ModelUnpinned`, `ProvisionError`, `WaitingForDeployment` | Pin model, inspect deployment/events. |
| `Ready=False` | `WalletError`, `WaitingForWallet`, `WaitingForDeployment` | Inspect remote-signer Secret/Deployment and status. |

### 7.3 PurchaseRequest Reasons

| Condition | Common Reasons | Recovery |
|-----------|----------------|----------|
| `Probed=False` | `ProbeError`, `NotPaymentGated`, `InvalidPricing`, `PricingMismatch` | Fix endpoint/model/payment terms. |
| `AuthsLoaded=False` | `NoAuths` | Ensure `buy.py` embedded signed auths. |
| `Configured=False` | `DuplicateModel`, `ConfigWriteError`, `AuthsWriteError`, `NoAuths` | Rename purchase/model or inspect ConfigMaps. |
| `Ready=False` | `SidecarNotReady`, `RuntimeSyncing` | Wait/reload sidecar or inspect `/status`. |
| `Deleting=True` | `Draining`, `RuntimeCleanupPending` | Wait for remaining auths or sidecar removal. |

## 8. Performance and Ops Targets

| ID | Target | Measurement |
|----|--------|-------------|
| P1 | Controller requeues converging offers/purchases every 5s. | Status transitions without spec mutation. |
| P2 | Cloudflared readiness budget defaults to 5 minutes. | `tunnel.WaitReady`. |
| P3 | Upstream health probe timeout is 2s. | ServiceOffer `UpstreamHealthy`. |
| P4 | Purchase pricing probe timeout is 15s. | `PurchaseRequest` and host buy preflight. |
| P5 | Registration document fetch timeout is 5s. | `obol buy inference` identity preflight. |
| P6 | x402-buyer remains single-replica until consumed auth state is shared. | LiteLLM deployment replicas. |
| P7 | Public catalog excludes paused, deleting, and operationally-not-ready offers. | `/skill.md`, `/api/services.json`. |

## 9. Testing Strategy

| Level | Required Coverage |
|-------|-------------------|
| Unit | CLI flag validation, CRD rendering, route matching, token registry, buyer encoding, ERC-8004 ABI/client, tunnel state. |
| Controller | ServiceOffer, Agent, PurchaseRequest, AgentIdentity, renderers, finalizers, cleanup. |
| BDD/integration | x402 local fork, seller/buyer path, tunnel path, discovery. |
| Smoke flows | `flows/flow-01` through `flow-16` plus `flows/release-smoke.sh`. |

Focused commands:

```bash
go test ./cmd/obol ./internal/serviceoffercontroller ./internal/x402 ./internal/x402/buyer ./internal/erc8004 ./internal/agentcrd ./internal/agentruntime ./internal/tunnel -count=1
go test -tags integration -v -run TestBDDIntegration -timeout 20m ./internal/x402/
bash -n flows/*.sh
git diff --check
```

## 10. Phases

| Phase | Scope | Exit Criteria |
|-------|-------|---------------|
| Phase 0 | Current main: stack, sell HTTP/inference/agent, buy inference, OBOL/USDC, ERC-8004, tunnel catalog. | Unit tests and relevant flow pass. |
| Phase 1 | Mother-agent product hardening: in-cluster agent factory flow, approval cards, budget policy, lifecycle cleanup UX. | Permissioned manager can create/update/delete child Agents without host CLI dependency. |
| Phase 2 | Service quality and reputation: benchmarks, reputation weighting, provider staking signal, anti-gaming policy. | Indexers can rank offers using verifiable metadata without centralized curation. |
| Phase 3 | Advanced payment economics: optional escrow-like x402 primitives if standard matures; LP/revenue automation off-chain first. | No new contracts, simulations demonstrate stakeholder-positive behavior. |
