# Obol Stack - Behaviors and Expectations

**Version**: 1.0.0-pr288
**Status**: Living document
**Last Updated**: 2026-03-29

This document defines the behavioral contract for Obol Stack on the PR `#288` baseline. Every behavior here maps to current or planned BDD scenarios in [features/](features/).

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

This is the behavioral contract for Obol Stack. It defines what the current branch should do, what it must not do, and how it should degrade when optional dependencies are absent.

### 1.2 Reading Guide

Behavior entries use:
- **Trigger**: what starts the behavior
- **Expected**: what the system should do
- **Rationale**: why the behavior matters

Cross-references use `SPEC SS X.Y`, pointing to [SPEC.md](SPEC.md).

### 1.3 Behavioral Priorities

The behavior model is ordered by actor priority:
1. local operator
2. agent developer
3. remote buyer

When tradeoffs conflict, operator safety and recoverability win.

---

## 2. Desired Behaviors

### 2.1 Stack Lifecycle

> SPEC SS 3.1

#### B-2.1.1: Stack initialization persists a stable cluster identity

**Trigger**: The operator runs `obol stack init`.
**Expected**: The CLI writes a stack ID, backend selection, and rendered defaults into the config directory. If `--force` is used against an existing stack, the stack ID is preserved unless the operator explicitly purges the stack.
**Rationale**: Persistent identity keeps local state, directory naming, and LiteLLM master-key derivation stable.

#### B-2.1.2: Stack startup deploys defaults before optional public exposure

**Trigger**: The operator runs `obol stack up`.
**Expected**: The cluster starts, baseline infrastructure is deployed through Helmfile, LiteLLM is auto-configured when possible, the default OpenClaw instance is prepared, and the tunnel remains dormant unless a persistent DNS tunnel was previously provisioned.
**Rationale**: Local operation is the primary mode. Public exposure must not be a prerequisite for core startup.

#### B-2.1.3: Purge preserves data unless the operator explicitly requests destruction

**Trigger**: The operator runs `obol stack purge`.
**Expected**: Config is removed and the cluster is destroyed, but persistent data survives unless `--force` is used.
**Rationale**: Wallets and agent state are valuable and must not be destroyed by the ordinary cleanup path.

### 2.2 LLM Routing

> SPEC SS 3.2

#### B-2.2.1: LiteLLM acts as the central operator-facing model gateway

**Trigger**: An OpenClaw instance or operator-configured route needs model access.
**Expected**: Requests are routed through LiteLLM rather than per-instance ad hoc provider wiring.
**Rationale**: Central routing reduces duplication, keeps provider config consistent, and enables the static buy-side `paid/*` namespace.

#### B-2.2.2: Model auto-configuration is best-effort, not mandatory

**Trigger**: `stack up` runs on a host with or without Ollama or cloud credentials.
**Expected**: When models or credentials are discoverable they are applied automatically; otherwise the stack still starts and the operator can configure providers later.
**Rationale**: Startup should remain recoverable even when optional provider dependencies are absent.

#### B-2.2.3: Custom OpenAI-compatible endpoints are validated before they are added

**Trigger**: The operator runs `obol model setup custom ...`.
**Expected**: The endpoint is validated before it becomes part of the LiteLLM route set.
**Rationale**: Broken model entries create confusing downstream failures for operators and agents.

### 2.3 Network Management

> SPEC SS 3.3

#### B-2.3.1: Local installable networks and remote RPC aliases remain distinct

**Trigger**: The operator uses `obol network install`, `list`, `add`, or `remove`.
**Expected**: Local deployable networks come only from embedded network bundles, while remote RPC aliases are resolved from the ChainList alias map and public RPC discovery flow.
**Rationale**: Treating these as separate prevents invalid support claims and operator confusion.

#### B-2.3.2: Public RPC writes are blocked by default

**Trigger**: The operator adds a remote chain without `--allow-writes`.
**Expected**: eRPC write methods remain blocked on that chain.
**Rationale**: Read-only defaults reduce the chance of accidental live transactions.

#### B-2.3.3: Network status reflects current command semantics, not idealized per-deployment views

**Trigger**: The operator runs `obol network status`.
**Expected**: The command reports current eRPC gateway health and upstream counts; it does not pretend to be a per-deployment local-node dashboard unless the implementation adds that contract.
**Rationale**: The spec must match the current CLI surface exactly.

### 2.4 OpenClaw Runtime

> SPEC SS 3.4

#### B-2.4.1: The default OpenClaw instance is the canonical agent runtime with static RBAC

**Trigger**: `stack up` completes on a branch where the default agent can be configured.
**Expected**: The `obol-agent` instance is created or re-synced. `agent.Init()` removes any legacy `HEARTBEAT.md`. RBAC is applied statically via k3s manifests (`openclaw-monetize-read` and `openclaw-monetize-write`), not patched at runtime. ServiceOffer reconciliation is handled by the `serviceoffer-controller` in the `x402` namespace.
**Rationale**: Separating reconciliation from the agent runtime improves reliability and eliminates heartbeat-based polling latency.

#### B-2.4.2: Additional OpenClaw instances remain operator-managed deployments

**Trigger**: The operator uses `obol openclaw onboard`, `sync`, `delete`, `dashboard`, `token`, `skills`, or wallet flows.
**Expected**: Instance selection, deployment directories, dashboard URLs, skills, and tokens are all managed through the CLI and persisted under managed config directories.
**Rationale**: OpenClaw instances are part of the platform control surface, not transient ad hoc workloads.

### 2.5 Sell-Side Monetization

> SPEC SS 3.5

#### B-2.5.1: Sell-side resources are created in the namespace the operator chose

**Trigger**: The operator runs `obol sell http <name> --namespace <ns> ...`.
**Expected**: The resulting `ServiceOffer` is created in `<ns>` and references the chosen upstream namespace explicitly.
**Rationale**: Namespace is an operator intent field and cannot be silently rewritten by the implementation or docs.

#### B-2.5.2: Reconciliation advances through six explicit stages via the serviceoffer-controller

**Trigger**: A `ServiceOffer` is created or updated.
**Expected**: The `serviceoffer-controller` in the `x402` namespace advances the offer through `ModelReady`, `UpstreamHealthy`, `PaymentGateReady`, `RoutePublished`, `Registered`, and `Ready`, with status updates visible to operators. Registration side effects are isolated into a `RegistrationRequest` CR.
**Rationale**: Operators need a clear progress model for debugging sell-side failures. The dedicated controller replaces the previous heartbeat-driven `monetize.py` skill loop.

#### B-2.5.3: Registration failure degrades gracefully when possible

**Trigger**: Registration is enabled but signer, gas, or RPC prerequisites are missing.
**Expected**: The service can remain payment-gated and publicly described with `Registered=True` and an `OffChainOnly` reason when that degraded path applies.
**Rationale**: Public discovery should not be all-or-nothing when the on-chain mint path is temporarily unavailable.

#### B-2.5.4: Probe verifies the payment gate without consuming buyer budget

**Trigger**: The operator runs `obol sell probe <name> -n <ns>`.
**Expected**: The command sends an unauthenticated request, expects a `402` pricing response, and confirms that the route is live and payment-gated.
**Rationale**: Operators need a cheap verification path before involving a real buyer flow.

#### B-2.5.5: Stopping a service pauses reconciliation without destroying state

**Trigger**: The operator runs `obol sell stop <name> -n <ns>`.
**Expected**: The `obol.org/paused` annotation is set on the ServiceOffer. The `serviceoffer-controller` skips reconciliation for paused offers, leaving existing routes and resources in place but preventing further stage progression.
**Rationale**: Operators need a reversible way to take a service offline without destroying the ServiceOffer or its child resources.

#### B-2.5.6: Deleting a service triggers finalizer-based cleanup and registration tombstoning

**Trigger**: The operator runs `obol sell delete <name> -n <ns>`.
**Expected**: The ServiceOffer is deleted. The `serviceoffer-controller` finalizer cleans up child Middleware, HTTPRoute, pricing routes, and registration resources. If the service had an active ERC-8004 registration, a `RegistrationRequest` with `desiredState: Tombstoned` is created to deactivate it on-chain.
**Rationale**: Automated cleanup prevents orphaned resources and ensures on-chain registration state stays consistent with cluster state.

### 2.6 Buy-Side Payments

> SPEC SS 3.6

#### B-2.6.1: Paid model routing uses a static public namespace

**Trigger**: An agent requests `paid/<remote-model>` through LiteLLM.
**Expected**: LiteLLM resolves the request to the buyer sidecar without requiring dynamic model-list rewrites for every purchased upstream.
**Rationale**: Static public naming keeps the buy-side integration simple and operationally stable.

#### B-2.6.2: Buyer runtime spending is bounded by the pre-signed auth pool

**Trigger**: The buyer sidecar serves paid requests.
**Expected**: It consumes only pre-signed authorizations and cannot mint new spend authority at runtime.
**Rationale**: This is the key safety property of the buy-side design.

#### B-2.6.3: Unmapped paid models fail explicitly

**Trigger**: A request arrives for `paid/<model>` that does not map to a purchased upstream.
**Expected**: The request fails with a clear not-found style response rather than silently drifting to another provider.
**Rationale**: Silent fallback would break spending and trust assumptions.

### 2.7 Tunnel, Discovery, Frontend, and Monitoring

> SPEC SS 3.7

#### B-2.7.1: Quick tunnel activation is demand-driven

**Trigger**: The stack starts without a pre-provisioned DNS tunnel.
**Expected**: Cloudflared remains dormant until a sell flow requires public exposure or the operator starts it manually.
**Rationale**: The operator should not pay the complexity cost of public exposure before it is needed.

#### B-2.7.2: Public discovery metadata reflects the current tunnel URL

**Trigger**: A tunnel URL becomes available or changes.
**Expected**: The stack updates `AGENT_BASE_URL` and syncs frontend-readable configuration so generated registration documents point at the current public origin.
**Rationale**: Discovery documents must describe reachable public endpoints.

#### B-2.7.3: Frontend remains local-only unless the architecture changes deliberately

**Trigger**: The operator accesses the dashboard.
**Expected**: The frontend is served under `obol.stack` and is not exposed by the public tunnel path.
**Rationale**: The frontend is an operator control surface, not a public buyer surface.

### 2.8 Managed Applications and Supporting Operations

> SPEC SS 3.8

#### B-2.8.1: Managed applications behave like named, persistent deployments

**Trigger**: The operator runs `obol app install`, `sync`, `list`, or `delete`.
**Expected**: Chart references are resolved, persisted under managed config paths, and deployed or removed through explicit CLI flows.
**Rationale**: Application management should match the rest of the stack’s declarative local-state model.

---

## 3. Undesired Behaviors

### 3.1 Exposure and Safety

#### U-3.1.1: Local-only operator routes are reachable through the public tunnel

**Trigger**: Route configuration removes or bypasses `obol.stack` hostname restrictions for frontend, eRPC, or monitoring.
**Expected**: The change is rejected or treated as a critical regression.
**Risk**: Public exposure of operator-only surfaces weakens the main trust boundary of the stack.

#### U-3.1.2: Remote RPC write capability is enabled by default

**Trigger**: A newly added public RPC upstream forwards write methods without explicit opt-in.
**Expected**: Write methods remain blocked unless the operator used `--allow-writes`.
**Risk**: Unintended live-chain transactions become possible through a read-mostly operator flow.

#### U-3.1.3: Buyer runtime receives live signing authority

**Trigger**: Runtime changes allow the sidecar to contact the remote signer or mint new spend approvals.
**Expected**: The runtime remains restricted to pre-signed authorizations only.
**Risk**: The bounded-spend trust model collapses.

### 3.2 Contract Drift

#### U-3.2.1: Documentation claims operator support that the CLI does not ship

**Trigger**: Specs or guides describe commands, flags, or supported chains that do not exist in the branch.
**Expected**: The canonical bundle is corrected to the current code surface, with future work moved into phased sections.
**Risk**: Operators make invalid assumptions and the spec stops being implementation-ready.

---

## 4. Edge Cases

### 4.1 Startup and Operator Recovery

#### E-4.1.1: No local model provider is immediately available

**Scenario**: The stack starts without Ollama models and without imported cloud credentials.
**Expected Handling**: Core infrastructure still starts; OpenClaw setup may be skipped or remain partially configured until the operator runs explicit provider setup.
**Rationale**: Provider absence should not destroy the local operator path.

#### E-4.1.2: Helmfile sync fails during startup

**Scenario**: Default infrastructure deployment fails mid-startup.
**Expected Handling**: The stack automatically runs a cleanup-oriented shutdown path.
**Rationale**: A half-started cluster is more dangerous than a failed startup.

### 4.2 Payments and Registration

#### E-4.2.1: Registration wallet has no gas

**Scenario**: A service is ready for publication but the registration wallet cannot submit an on-chain transaction.
**Expected Handling**: The service degrades to `OffChainOnly` rather than disappearing entirely.
**Rationale**: Discovery metadata is still valuable even when chain settlement is temporarily blocked.

#### E-4.2.2: Buyer auth pool is exhausted

**Scenario**: A purchased upstream has no remaining signed authorizations.
**Expected Handling**: Requests fail explicitly until the operator or agent refills the pool.
**Rationale**: Silent fallback would break billing and hide a capacity problem.

### 4.3 Selection Ambiguity

#### E-4.3.1: Multiple deployments of the same type exist

**Scenario**: The operator has multiple OpenClaw instances, app deployments, or network deployments.
**Expected Handling**: Commands auto-select only when there is exactly one unambiguous target; otherwise they require the operator to specify the target.
**Rationale**: Ambiguous automation is more dangerous than an extra required argument.

---

## 5. Performance Expectations

| Behavior | Target | Measurement | Degradation Handling |
|----------|--------|-------------|---------------------|
| ChainList discovery | bounded by 15s timeout | `internal/network/chainlist.go` timeout | operator retries with custom endpoint |
| Tunnel startup | bounded by 30s rollout wait | `tunnel.EnsureRunning()` rollout status | local path remains available |
| LiteLLM restart | bounded by 90s rollout wait | `model.RestartLiteLLM()` rollout status | operator reruns provider setup or inspects deployment |
| Buyer metrics visibility | 30s scrape interval | PodMonitor config | stale metrics do not block inference |

---

## 6. Guardrail Definitions

### 6.1 Non-Negotiable Guardrails

| Guardrail | Rule | Enforcement | Violation Response |
|-----------|------|-------------|-------------------|
| Local-only surfaces | Frontend, eRPC, and monitoring stay behind `obol.stack` hostname restrictions | route templates, review, spec bundle | treat as critical regression |
| Static paid namespace | Buy-side public names remain `paid/<remote-model>` | LiteLLM config model, buyer sidecar routing | reject drifting implementations |
| Namespace fidelity | `sell http --namespace <ns>` creates the `ServiceOffer` in `<ns>` | CLI manifest generation | treat mismatched docs or code as bug |
| Phase discipline | future behavior must live in phased sections or ADR follow-ups | canonical root-level bundle and hooks | block or fix before merge |
