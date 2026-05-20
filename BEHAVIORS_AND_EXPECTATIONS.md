# Obol Stack - Behaviors and Expectations

**Version**: 1.0.0-draft
**Status**: Draft
**Last Updated**: 2026-05-20

Behavior contract for [SPEC.md](SPEC.md). Every ID below should map to at least one scenario under [features/](features/).

## 1. Desired Behaviors

### 1.1 Stack and Models

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B1 | Operator runs `obol stack init` on a clean config dir. | Stack ID, backend choice, kube defaults, and embedded infra are written. | Stack state must be reproducible. | 5.1 |
| B2 | Operator runs `obol stack up` after init. | k3d/k3s starts, kubeconfig is written, infra syncs, host DNS is configured best-effort. | A single command should converge the local stack. | 5.1 |
| B3 | Operator configures models. | LiteLLM config persists provider models and Hermes can be synced to the current inventory. | Agents need a stable OpenAI-compatible route. | 5.2 |
| B4 | Operator prefers a model. | Default agent-model selection uses the preferred order and skips only literal `paid/*`. | Purchased concrete `paid/<model>` entries are valid. | 5.2 |

### 1.2 Agent Runtime

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B5 | Operator creates `obol agent new <name>` with CRD flags. | CLI seeds host files, applies namespace and `Agent`, then controller provisions Hermes. | Child agents are durable K8s resources. | 5.3 |
| B6 | Agent has empty first-time model. | Controller sets `ModelUnpinned` and does not mark Ready. | Silent model selection would hide bad setup. | 4.2 |
| B7 | Agent requests wallet creation. | Controller creates/reuses keystore Secret, remote-signer Deployment/Service, and publishes wallet address. | Agent revenue or signing address must be stable. | 4.2 |
| B8 | Agent is deleted. | Finalizer tears down child resources before removing the CR. | Avoid orphaned child runtimes. | 4.2 |

### 1.3 Sell-Side Commerce

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B9 | Seller creates `ServiceOffer` for HTTP/inference. | Controller checks upstream, applies payment gate and route, then status conditions converge. | Sell intent must become a paid route. | 5.4 |
| B10 | Seller creates `ServiceOffer type=agent`. | Controller waits for referenced Agent, then derives upstream/model and writes `agentResolution`. | Buyers must know the actual agent model/skills. | 5.5 |
| B11 | Buyer probes unpaid paid route. | Response is HTTP 402 with x402 v2 pricing and asset metadata. | Payment discovery is the protocol entry point. | 3.3 |
| B12 | Agent-backed offer is probed. | 402 includes `agentModel`, `agentSkills`, and `agentRuntime`. | MVP utility is "pay for a Hermes agent turn on this model". | 3.3 |
| B13 | Offer is paused. | Payment gate and route are removed or disabled; offer does not appear in public catalog. | Pausing must stop new paid traffic. | 7.1 |
| B14 | Offer is deleted. | Finalizer removes route children and registration cleanup/tombstone runs when needed. | Public discovery must not retain stale live services. | 5.4, 5.7 |

### 1.4 Buy-Side Commerce

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B15 | Buyer runs `obol buy inference`. | Host preflight probes pricing, optionally verifies identity, then dispatches to in-pod `buy.py`. | Signing stays with the agent wallet path. | 5.6 |
| B16 | `buy.py` creates PurchaseRequest with auths. | Controller validates price, writes buyer config/auth ConfigMaps, adds `paid/<model>`, reloads sidecar. | Paid model calls should become transparent to the agent. | 5.6 |
| B17 | Agent calls LiteLLM using `paid/<model>`. | x402-buyer maps to remote model, attaches payment, retries, and forwards seller response/body/status. | Paid remote inference must behave like local inference. | 5.6 |
| B18 | Auth pool has no auths. | PurchaseRequest is not Ready and sidecar status shows no remaining spend. | Max loss is bounded by pre-signed auth count. | 4.3 |
| B19 | PurchaseRequest is deleted with remaining auths. | Controller drains or waits before removing route/config. | Deletion must not silently orphan spendable auths. | 4.3 |

### 1.5 Discovery and Tunnel

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B20 | Registration enabled. | Controller publishes registration JSON and creates RegistrationRequest. | ERC-8004 discovery must be standards-native. | 5.7 |
| B21 | On-chain registration appears. | Controller updates AgentIdentity and ServiceOffer status with agentId/tx hash. | Indexers need chain-bound identity data. | 5.7 |
| B22 | Registration tx is pending but route is live. | `/skill.md` and `/api/services.json` include the offer with `registrationPending=true`. | Usable paid services should not disappear from the storefront. | 4.6 |
| B23 | Tunnel starts or restarts. | Tunnel URL propagates to ConfigMap, agent base URL, catalog, and storefront. | Public endpoints must be current. | 5.8 |
| B24 | Public user opens tunnel root. | Storefront renders services from `/api/services.json`. | Public entrypoint is the usable catalog, not a blank gateway. | 5.8 |

### 1.6 OBOL Payment Asset

| ID | Trigger | Expected | Rationale | SPEC |
|----|---------|----------|-----------|------|
| B25 | Seller uses `--token OBOL`. | ServiceOffer includes OBOL address, 18 decimals, Permit2 transfer method, and EIP-712 domain. | Buyers must sign the right asset. | 4.1, 6 |
| B26 | Buyer selects token that differs from seller pricing. | Buy preflight fails with asset mismatch and suggests the correct token when known. | Prevent wrong-asset auth pools. | 5.6 |

## 2. Undesired Behaviors

| ID | Trigger | Expected Instead | Risk | SPEC |
|----|---------|------------------|------|------|
| U1 | Public tunnel route exposes frontend, eRPC, LiteLLM, or monitoring. | Keep those routes hostname-restricted to `obol.stack`. | Public local control-plane exposure. | 6 |
| U2 | x402-buyer receives private keys or remote-signer URL. | Only pre-signed auths may reach the sidecar. | Unbounded buyer wallet loss. | 6 |
| U3 | ForwardAuth route settles raw direct `X-PAYMENT` as production path. | Use x402-buyer or standalone `sell inference` settlement path. | Verify-only route can mislead settlement semantics. | 1.1, 5.6 |
| U4 | Code treats `PurchaseRequest` as escrow. | Treat it as bounded authorization inventory. | Incorrect economic/security assumptions. | 1.1, 4.3 |
| U5 | `spec.skills` is documented as confinement. | Document it as seed data only. | Regulated service may overclaim safety. | 1.1, 6 |
| U6 | Controller mints ERC-8004 registration transactions. | Operator submits with `obol sell register`; controller observes. | Hidden custody/gas side effects. | 5.7 |
| U7 | Multiple x402-buyer replicas share one local auth state. | Keep LiteLLM replicas at 1 until state is shared. | Double-spend or inconsistent counters. | 8 |
| U8 | Offer enters public catalog while paused, deleting, or route/payment/upstream false. | Exclude from catalog. | Buyers discover unusable services. | 4.6 |

## 3. Edge Cases

| ID | Scenario | Expected Handling | SPEC |
|----|----------|-------------------|------|
| E1 | ServiceOffer references missing Agent. | `WaitingForAgent`, no route publication. | 5.5 |
| E2 | Agent CR is terminating and demo sell reruns. | CLI reports terminating Agent and suggests wait or force delete. | 5.5 |
| E3 | Upstream health returns 5xx. | `UpstreamHealthy=False`, route not published. | 5.4 |
| E4 | `x402-verifier` has no pricing file. | Starts with defaults and waits for kube route source. | 5.4 |
| E5 | Direct verifier call lacks `X-Forwarded-Uri`. | Verifier returns 403 fail-closed. | 3.3 |
| E6 | Quick tunnel URL changes. | Destructive actions warn; restart syncs dependent surfaces. | 5.8 |
| E7 | Registration owner is another offer. | Non-owner offer follows owner RegistrationRequest status. | 5.7 |
| E8 | Buy endpoint returns non-402 during probe. | PurchaseRequest `Probed=False NotPaymentGated`; host preflight fails. | 5.6 |
| E9 | Duplicate PurchaseRequest model exists. | New request `Configured=False DuplicateModel`. | 4.3 |
| E10 | OBOL requested on unsupported chain. | Token resolution fails before ServiceOffer/PurchaseRequest is accepted. | 6 |

## 4. Performance Expectations

| ID | Behavior | Target | Degradation Handling |
|----|----------|--------|----------------------|
| P1 | Controller convergence | Requeue non-ready offers/purchases every 5s. | Status explains blocked condition. |
| P2 | Upstream health | 2s per probe. | Mark unhealthy and retry. |
| P3 | Buy pricing probe | 15s. | Fail preflight or mark `Probed=False`. |
| P4 | Registration fetch | 5s. | User can retry with `--no-verify-identity` in dev. |
| P5 | Tunnel readiness | 5 minutes default. | Return explicit rollout/log error. |

## 5. Guardrails

| ID | Rule | Enforcement | Violation Response |
|----|------|-------------|--------------------|
| G1 | No new contracts for MVP features. | Design review and ADR. | Reject spec/code path. |
| G2 | Public tunnel allowlist only. | Route templates, tests, review. | Block merge. |
| G3 | Buyer sidecar has no signer. | Deployment env/volume review and tests. | Block merge. |
| G4 | Agent CRD status must come from controller. | CLI only applies spec. | Block direct status writes outside tests. |
| G5 | OBOL asset metadata must stay in token registry and offer/payment specs. | Token tests and buy preflight. | Reject incomplete token path. |
| G6 | Spec changes with behavior changes. | PR checklist and BDD references. | Request docs/tests update. |
