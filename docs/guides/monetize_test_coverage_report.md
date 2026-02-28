# Monetize Subsystem — Test Coverage Report

**Branch**: `fix/review-hardening` (off `feat/secure-enclave-inference`)
**Date**: 2026-02-27
**Total integration tests**: 46 across 3 files

---

## Section Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                     TEST PYRAMID                                 │
│                                                                  │
│                        ▲                                         │
│                       ╱ ╲       Phase 8: FULL (1)                │
│                      ╱   ╲      ← tunnel+Ollama+x402-rs+EIP-712 │
│                     ╱─────╲                                      │
│                    ╱       ╲    Phase 5+: Real Facilitator (1)    │
│                   ╱         ╲   ← real x402-rs, real EIP-712     │
│                  ╱───────────╲                                   │
│                 ╱             ╲  Phase 6+7: Tunnel + Fork (5)    │
│                ╱               ╲ ← real Ollama, mock facilitator │
│               ╱─────────────────╲                                │
│                 ╱             ╲  Phase 4+5: Payment + E2E (8)    │
│                ╱               ╲ ← mock facilitator, real gate   │
│               ╱─────────────────╲                                │
│              ╱                   ╲  Phase 3: Routing (6)         │
│             ╱                     ╲ ← real Traefik, Anvil RPC    │
│            ╱───────────────────────╲                             │
│           ╱                         ╲  Phase 2: RBAC + Recon (6) │
│          ╱                           ╲ ← real agent in pod       │
│         ╱─────────────────────────────╲                          │
│        ╱                               ╲  Phase 1: CRD (7)      │
│       ╱                                 ╲ ← schema validation   │
│      ╱───────────────────────────────────╲                       │
│     ╱                                     ╲  Base: Inference (12)│
│    ╱_______________________________________╲ ← Ollama + skills   │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

---

## Phase 1 — CRD Lifecycle (7 tests)

**What it covers**: ServiceOffer custom resource schema validation, CRUD operations, printer columns, status subresource isolation.

**Realism**: Low (data-plane only, no reconciliation or traffic).

```
┌─────────────────────────────────────────────────────┐
│                   TEST BOUNDARY                      │
│                                                      │
│  kubectl apply ──▶ ┌──────────────────┐              │
│                    │  ServiceOffer CR │              │
│  kubectl get   ──▶ │  (obol.org CRD)  │              │
│                    └──────────────────┘              │
│  kubectl patch ──▶       │                           │
│  kubectl delete──▶       ▼                           │
│                    API Server validates:              │
│                    ✓ wallet regex (^0x[0-9a-fA-F]{40}$)│
│                    ✓ status subresource isolation     │
│                    ✓ printer columns (TYPE, PRICE)    │
│                                                      │
│  ┌─────────────────────────────────────────────┐     │
│  │ NOT TESTED: reconciler, routing, payment    │     │
│  └─────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `CRD_Exists` | CRD installed in cluster |
| `CRD_CreateGet` | Spec fields round-trip correctly |
| `CRD_List` | kubectl list works |
| `CRD_StatusSubresource` | Status patch doesn't mutate spec |
| `CRD_WalletValidation` | Invalid wallet rejected by API server |
| `CRD_PrinterColumns` | `kubectl get` shows TYPE, PRICE, NETWORK |
| `CRD_Delete` | CR deletion works |

**Gap vs real world**: No agent involvement. A real user runs `obol sell http`, not raw kubectl.

---

## Phase 2 — RBAC + Reconciliation (6 tests)

**What it covers**: Split RBAC roles exist and are bound, agent can read/write CRs from inside pod, reconciler handles unhealthy upstreams, idempotent re-processing.

**Realism**: Medium (real agent pod, real RBAC, but no traffic or payment).

```
┌─────────────────────────────────────────────────────────────────┐
│                      TEST BOUNDARY                               │
│                                                                  │
│  ┌─────────────┐    RBAC Check     ┌─────────────────────────┐  │
│  │ Test Runner  │ ────────────────▶ │ ClusterRole:            │  │
│  │ (kubectl get)│                   │  openclaw-monetize-read │  │
│  └─────────────┘                   │  openclaw-monetize-wkld │  │
│        │                            │ Role:                   │  │
│        │                            │  openclaw-x402-pricing  │  │
│        │                            └─────────────────────────┘  │
│        │                                                         │
│        │  kubectl exec                                           │
│        ▼                                                         │
│  ┌─────────────────────────────────┐                             │
│  │ obol-agent pod                  │                             │
│  │  monetize.py process <name>     │──▶ ServiceOffer CR         │
│  │  monetize.py process --all      │   (status conditions)      │
│  │  monetize.py list               │                             │
│  └─────────────────────────────────┘                             │
│        │                                                         │
│        ▼                                                         │
│  UpstreamHealthy=False (no real upstream)                        │
│  HEARTBEAT_OK (no pending offers)                                │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐    │
│  │ NOT TESTED: Traefik routing, x402 gate, payment, tunnel │    │
│  └──────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `RBAC_ClusterRolesExist` | Split RBAC roles deployed by k3s manifests |
| `RBAC_BindingsPatched` | `obol agent init` patches all 3 bindings |
| `Monetize_ListEmpty` | Agent skill lists zero offers |
| `Monetize_ProcessAllEmpty` | Heartbeat returns OK with no work |
| `Monetize_ProcessUnhealthy` | Sets UpstreamHealthy=False for missing svc |
| `Monetize_Idempotent` | Second reconcile doesn't error |

**Gap vs real world**: No upstream service exists. Reconciliation never reaches PaymentGateReady or RoutePublished.

---

## Phase 3 — Routing with Anvil Upstream (6 tests)

**What it covers**: Full 6-condition reconciliation with a real upstream (Anvil fork), Traefik Middleware + HTTPRoute creation, traffic forwarding, owner-reference cascade on delete.

**Realism**: Medium-High (real cluster networking, real Traefik, real upstream). No payment gate yet.

```
┌─────────────────────────────────────────────────────────────────────┐
│                         TEST BOUNDARY                                │
│                                                                      │
│  ┌──────────┐                                                        │
│  │ Anvil    │ ◀── Host machine (port N)                             │
│  │ (fork of │     forking Base Sepolia                               │
│  │ base-sep)│                                                        │
│  └────┬─────┘                                                        │
│       │ ClusterIP + EndpointSlice                                    │
│       │ (anvil-rpc.test-ns.svc)                                      │
│       ▼                                                              │
│  ┌──────────────────────────────────────────────────────────┐        │
│  │ k3d cluster                                              │        │
│  │                                                          │        │
│  │  Agent reconciles:                                       │        │
│  │  ✓ UpstreamHealthy  (HTTP health-check to Anvil)        │        │
│  │  ✓ PaymentGateReady (Middleware created)                 │        │
│  │  ✓ RoutePublished   (HTTPRoute created)                  │        │
│  │  ✓ Ready                                                 │        │
│  │                                                          │        │
│  │  ┌─────────────┐     ┌──────────────┐     ┌──────────┐  │        │
│  │  │ Traefik GW  │────▶│ HTTPRoute    │────▶│ Anvil    │  │        │
│  │  │ :8080       │     │ /services/x  │     │ upstream │  │        │
│  │  └─────────────┘     └──────────────┘     └──────────┘  │        │
│  │                                                          │        │
│  │  curl POST obol.stack:8080/services/x                    │        │
│  │  → eth_blockNumber response from Anvil ✓                 │        │
│  └──────────────────────────────────────────────────────────┘        │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────┐      │
│  │ NOT TESTED: x402 ForwardAuth (no facilitator), no 402     │      │
│  └────────────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `Route_AnvilUpstream` | Anvil responds locally |
| `Route_FullReconcile` | All 4 conditions reach True |
| `Route_MiddlewareCreated` | ForwardAuth Middleware exists |
| `Route_HTTPRouteCreated` | HTTPRoute has correct parentRef |
| `Route_TrafficRoutes` | HTTP through Traefik reaches Anvil |
| `Route_DeleteCascades` | ownerRef GC cleans up derived resources |

**Gap vs real world**: No payment gate. Requests go straight through without x402 gating. Free endpoint, not monetized.

---

## Phase 4 — Payment Gate (4 tests)

**What it covers**: x402-verifier health, 402 response without payment, 402 response body format (x402 spec compliance), 200 response with mock payment.

**Realism**: Medium-High. Real x402-verifier, real Traefik ForwardAuth. Mock facilitator always says `isValid: true`.

```
┌──────────────────────────────────────────────────────────────────────┐
│                          TEST BOUNDARY                                │
│                                                                       │
│  ┌───────┐    POST /services/x    ┌──────────┐   ForwardAuth         │
│  │Client │ ─────────────────────▶ │ Traefik  │ ──────────────▶       │
│  │(test) │                        │ Gateway  │                │       │
│  └───────┘                        └──────────┘                │       │
│      │                                 │                      ▼       │
│      │                                 │              ┌──────────────┐│
│      │  No X-PAYMENT header            │              │ x402-verifier││
│      │  ──────────────────▶            │              │ (real pod)   ││
│      │                                 │              │              ││
│      │  ◀── 402 + pricing JSON         │              │ Checks:      ││
│      │                                 │              │ ✓ route match││
│      │                                 │              │ ✓ has header ││
│      │  X-PAYMENT: <mock base64>       │              │ ✓ call facil.││
│      │  ──────────────────▶            │              │              ││
│      │                                 │              │   ┌────────┐ ││
│      │                                 │              │   │ Mock   │ ││
│      │  ◀── 200 + Anvil response       │              │   │ Facil. │ ││
│      │                                 │              │   │ always │ ││
│      │                                 │              │   │ valid  │ ││
│      │                                 │              │   └────────┘ ││
│      │                                 │              └──────────────┘│
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐     │
│  │ MOCK: facilitator (no real signature validation)            │     │
│  │ MOCK: payment header (fake JSON, not real EIP-712)          │     │
│  └──────────────────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `PaymentGate_VerifierHealthy` | /healthz and /readyz return 200 |
| `PaymentGate_402WithoutPayment` | No payment → 402 |
| `PaymentGate_RequirementsFormat` | 402 body matches x402 spec |
| `PaymentGate_200WithPayment` | Mock payment → 200 |

**Gap vs real world**: The facilitator never validates the EIP-712 signature. Any well-formed JSON base64 header passes. Wire format bugs (string vs int types) are invisible.

---

## Phase 5 — Full E2E CLI-Driven (3 tests)

**What it covers**: `obol sell http` CLI → CR creation → agent reconciliation → 402 → 200 → `obol sell list/status/delete`. Heartbeat auto-reconciliation (90s wait).

**Realism**: High for the CLI path. Still uses mock facilitator for payment.

```
┌──────────────────────────────────────────────────────────────────────┐
│                          TEST BOUNDARY                                │
│                                                                       │
│  ┌────────────────┐                                                   │
│  │ obol sell│                                                  │
│  │ offer my-qwen   │ ──▶ ServiceOffer CR                             │
│  │ --type inference │                                                 │
│  │ --model qwen3    │                                                 │
│  │ --per-request .. │                                                 │
│  └────────────────┘                                                   │
│         │                                                             │
│         ▼                                                             │
│  ┌──────────────────────────────────────────────────────────────────┐ │
│  │ Agent pod (autonomous reconciliation)                            │ │
│  │                                                                  │ │
│  │  monetize.py process ──▶ 6 conditions ──▶ Ready=True            │ │
│  │                                                                  │ │
│  │  OR: heartbeat cron (every 30min) auto-reconciles               │ │
│  └──────────────────────────────────────────────────────────────────┘ │
│         │                                                             │
│         ▼                                                             │
│  ┌──────────────────────────────────────────────────────────────────┐ │
│  │ obol sell list       → shows offer                          │ │
│  │ obol sell status     → shows all conditions                 │ │
│  │ obol sell delete     → cleans up CR + derived resources     │ │
│  └──────────────────────────────────────────────────────────────────┘ │
│                                                                       │
│  Still uses mock facilitator for payment verification.                │
└──────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `E2E_OfferLifecycle` | Full CLI → create → reconcile → pay → delete |
| `E2E_HeartbeatReconciles` | Cron-driven reconciliation without manual trigger |
| `E2E_ListAndStatus` | CLI query commands work |

**Gap vs real world**: Mock facilitator. No real model (Anvil upstream, not Ollama).

---

## Phase 6 — Tunnel E2E + Ollama (2 tests)

**What it covers**: Real Ollama inference through the full stack, including Cloudflare tunnel accessibility. Agent-autonomous offer management.

**Realism**: Very High for the local path. Tunnel tests require CF credentials.

```
┌───────────────────────────────────────────────────────────────────────────┐
│                            TEST BOUNDARY                                  │
│                                                                           │
│  ┌─────────┐    POST /services/x/v1/chat/completions                     │
│  │ Client  │ ────────────────────────────────────────▶                    │
│  └─────────┘                                         │                    │
│       │                                              ▼                    │
│       │    ┌──────────┐   ForwardAuth   ┌──────────────────┐             │
│       │    │ Traefik  │ ──────────────▶ │ x402-verifier    │             │
│       │    │ Gateway  │                 │ → mock facilitator│             │
│       │    └──────────┘                 └──────────────────┘             │
│       │         │                                                        │
│       │         │ payment valid                                          │
│       │         ▼                                                        │
│       │    ┌──────────┐                                                  │
│       │    │ Ollama   │ ← REAL model (qwen3:0.6b)                       │
│       │    │ (llm ns) │   REAL inference response                        │
│       │    └──────────┘                                                  │
│       │                                                                  │
│       │    Also tests via tunnel:                                        │
│       │    ┌─────────────────────┐                                       │
│       │    │ Cloudflare Tunnel   │ ← if CF credentials configured       │
│       │    │ https://<domain>    │                                        │
│       │    └─────────────────────┘                                       │
│       │                                                                  │
│  ┌────────────────────────────────────────────────────────────────┐      │
│  │ REAL: Ollama inference, Traefik routing, x402-verifier         │      │
│  │ MOCK: facilitator (still always-valid)                         │      │
│  │ OPTIONAL: CF tunnel (skipped without credentials)              │      │
│  └────────────────────────────────────────────────────────────────┘      │
└───────────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `Tunnel_OllamaMonetized` | Real model → real inference → mock payment → response |
| `Tunnel_AgentAutonomousMonetize` | Agent creates/manages offer without CLI |

**Gap vs real world**: Mock facilitator. Real-world buyers send real EIP-712 signatures.

---

## Phase 7 — Fork Validation with Mock Facilitator (2 tests)

**What it covers**: Anvil-fork-backed upstream with mock facilitator verify/settle tracking, agent error recovery from bad upstream state.

**Realism**: Medium-High. Real on-chain environment (forked), but fake payment validation.

```
┌──────────────────────────────────────────────────────────────────────┐
│                         TEST BOUNDARY                                 │
│                                                                       │
│  ┌──────────┐                              ┌─────────────────┐       │
│  │ Anvil    │ ◀── fork of Base Sepolia     │ Mock Facilitator│       │
│  │ (real    │     real block numbers       │ ✓ /verify       │       │
│  │ chain    │     real chain ID 84532      │   → always valid│       │
│  │ state)   │                              │ ✓ /settle       │       │
│  └──────────┘                              │   → always ok   │       │
│       │                                    │ Tracks call     │       │
│       │ EndpointSlice                      │ counts only     │       │
│       ▼                                    └─────────────────┘       │
│  ┌───────────────────────────────────┐            │                  │
│  │ Full reconciliation pipeline      │            │                  │
│  │ ✓ UpstreamHealthy (Anvil health)  │            │                  │
│  │ ✓ PaymentGateReady                │            │                  │
│  │ ✓ RoutePublished                  │            │                  │
│  │ ✓ Ready                           │◀───────────┘                  │
│  │                                   │                               │
│  │ Also tests:                       │                               │
│  │ ✓ Pricing route in ConfigMap      │                               │
│  │ ✓ Delete cleans up pricing route  │                               │
│  │ ✓ Agent self-heals from bad state │                               │
│  └───────────────────────────────────┘                               │
│                                                                       │
│  ┌──────────────────────────────────────────────────────────────┐     │
│  │ MOCK: facilitator (no signature validation, no USDC check)  │     │
│  │ MOCK: payment header (fake JSON blob)                       │     │
│  └──────────────────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `Fork_FullPaymentFlow` | 402 → 200 with mock, verify/settle called |
| `Fork_AgentSkillIteration` | Agent recovers from unreachable upstream |

**Gap vs real world**: Facilitator never validates signatures. USDC balance irrelevant.

---

## Phase 5+ — Real Facilitator Payment (1 test) ← CLOSEST TO PRODUCTION

**What it covers**: The entire payment cryptography stack. Real x402-rs facilitator binary, real EIP-712 TransferWithAuthorization signatures, real USDC balance on Anvil fork, real signature validation.

**Realism**: Very High. The only mock remaining is the chain settlement (Anvil resets after test).

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         TEST BOUNDARY                                     │
│                                                                           │
│  ┌──────────┐    Buyer: Anvil Account[0]                                 │
│  │ go test  │    10 USDC minted via anvil_setStorageAt                   │
│  │          │                                                             │
│  │ Signs real EIP-712                                                    │
│  │ TransferWithAuthorization                                             │
│  │ (ERC-3009)                                                            │
│  │                                                                        │
│  │ ┌─────────────────────────────────────┐                               │
│  │ │ TypedData:                          │                               │
│  │ │   domain: USD Coin / v2 / 84532     │                               │
│  │ │   from: buyer address               │                               │
│  │ │   to: seller address                │                               │
│  │ │   value: "1000" (0.001 USDC)        │                               │
│  │ │   validAfter: "0"  ← STRING!        │                               │
│  │ │   validBefore: "4294967295" ← STRING│                               │
│  │ │   nonce: random 32 bytes            │                               │
│  │ └─────────────────────────────────────┘                               │
│  └──────────┘                                                             │
│       │                                                                   │
│       │  X-PAYMENT: base64(envelope)                                     │
│       ▼                                                                   │
│  ┌──────────┐   ForwardAuth    ┌──────────────────┐                      │
│  │ Traefik  │ ───────────────▶ │ x402-verifier    │                      │
│  │ Gateway  │                  │ (real pod)        │                      │
│  └──────────┘                  └────────┬─────────┘                      │
│       │                                 │                                 │
│       │                                 │ POST /verify                    │
│       │                                 ▼                                 │
│       │                        ┌──────────────────┐                      │
│       │                        │ x402-rs          │ ← REAL binary        │
│       │                        │ facilitator      │                      │
│       │                        │                  │                      │
│       │                        │ ✓ Decodes header │                      │
│       │                        │ ✓ Validates EIP  │                      │
│       │                        │   712 signature  │                      │
│       │                        │ ✓ Checks USDC    │                      │
│       │                        │   balance on     │                      │
│       │                        │   Anvil fork     │                      │
│       │                        │ ✓ Returns        │                      │
│       │                        │   isValid: true  │                      │
│       │                        └────────┬─────────┘                      │
│       │                                 │                                 │
│       │                                 │ connected to:                   │
│       │                                 ▼                                 │
│       │                        ┌──────────────────┐                      │
│       │                        │ Anvil Fork       │ ← REAL chain state   │
│       │                        │ (Base Sepolia)   │                      │
│       │                        │ chain ID: 84532  │                      │
│       │                        │                  │                      │
│       │                        │ Has USDC balance  │                      │
│       │                        │ for buyer address │                      │
│       │                        └──────────────────┘                      │
│       │                                                                   │
│       │ 200 OK                                                            │
│       ▼                                                                   │
│  Response from Anvil (eth_blockNumber)                                   │
│                                                                           │
│  ┌───────────────────────────────────────────────────────────────────┐    │
│  │ REAL: x402-rs binary, EIP-712 signing, USDC state, verifier,    │    │
│  │       Traefik ForwardAuth, agent reconciliation, CRD lifecycle   │    │
│  │ SIMULATED: chain (Anvil fork, not mainnet), settlement (no      │    │
│  │            actual USDC transfer, Anvil state resets)             │    │
│  └───────────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `Fork_RealFacilitatorPayment` | Real EIP-712 → real x402-rs → real validation → 200 |

**Gap vs real world**: Settlement doesn't transfer real USDC (Anvil fork resets). No real L1/L2 block confirmation. No Cloudflare tunnel in this test.

---

## Phase 8 — Full Stack: Tunnel + Ollama + Real Facilitator (1 test) ← PRODUCTION EQUIVALENT

**What it covers**: Everything. Real Ollama inference, real x402-rs facilitator, real EIP-712 signatures, USDC-funded Anvil fork, and requests entering through the Cloudflare quick tunnel's dynamic `*.trycloudflare.com` URL.

**Realism**: Maximum. This is a production sell-side scenario with the only difference being Anvil (not mainnet) and a quick tunnel (not a persistent named tunnel).

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              TEST BOUNDARY                                    │
│                                                                               │
│  BUYER (test runner)                                                         │
│  ┌──────────────────────────────────────────────────────────────────────┐    │
│  │ 1. Signs real EIP-712 TransferWithAuthorization (ERC-3009)          │    │
│  │    domain: USD Coin / v2 / 84532                                    │    │
│  │    from: 0xf39F... (Anvil account[0], funded with 10 USDC)         │    │
│  │    to: 0x7099... (seller)                                           │    │
│  │    value: "1000" (0.001 USDC)                                       │    │
│  │    nonce: random 32 bytes                                           │    │
│  └──────────────────────────────────────────────────────────────────────┘    │
│       │                                                                      │
│       │ POST https://<random>.trycloudflare.com/services/test-tunnel-real/   │
│       │      /v1/chat/completions                                            │
│       │ X-PAYMENT: base64(real EIP-712 envelope)                             │
│       ▼                                                                      │
│  ┌──────────────────────────────────────┐                                    │
│  │ Cloudflare Edge (quick tunnel)      │ ← REAL Cloudflare infrastructure   │
│  │ *.trycloudflare.com                 │   dynamic URL, non-persistent       │
│  │ TLS termination                     │                                     │
│  └────────────────┬─────────────────────┘                                    │
│                   │ cloudflared connector (k3d pod)                           │
│                   ▼                                                          │
│  ┌──────────────────────────────────────┐                                    │
│  │ Traefik Gateway (:443 internal)     │ ← REAL Traefik, Gateway API        │
│  │ HTTPRoute: /services/test-tunnel-*  │                                     │
│  │ ForwardAuth middleware              │                                     │
│  └────────────────┬─────────────────────┘                                    │
│                   │ ForwardAuth request                                       │
│                   ▼                                                          │
│  ┌──────────────────────────────────────┐                                    │
│  │ x402-verifier (2 replicas, PDB)     │ ← REAL verifier pod                │
│  │ Extracts X-PAYMENT header           │                                     │
│  │ Looks up pricing route in ConfigMap │                                     │
│  │ Calls facilitator /verify           │                                     │
│  └────────────────┬─────────────────────┘                                    │
│                   │ POST /verify                                              │
│                   ▼                                                          │
│  ┌──────────────────────────────────────┐                                    │
│  │ x402-rs facilitator (host process)  │ ← REAL Rust binary                 │
│  │                                      │                                    │
│  │ ✓ Decodes x402 V1 envelope          │                                    │
│  │ ✓ Recovers signer from EIP-712 sig  │                                    │
│  │ ✓ Checks USDC balance on Anvil      │                                    │
│  │ ✓ Validates nonce not replayed       │                                    │
│  │ ✓ Returns isValid: true + payer     │                                    │
│  └────────────────┬─────────────────────┘                                    │
│                   │ connected to:                                             │
│                   ▼                                                          │
│  ┌──────────────────────────────────────┐                                    │
│  │ Anvil Fork (host process)            │ ← REAL chain state (Base Sepolia) │
│  │ chain ID: 84532                      │   USDC balances, nonce tracking    │
│  │ 10 USDC minted to buyer              │                                    │
│  └──────────────────────────────────────┘                                    │
│                                                                               │
│  ◀── verifier returns 200 (payment valid)                                    │
│                   │                                                           │
│                   ▼ Traefik forwards to upstream                              │
│  ┌──────────────────────────────────────┐                                    │
│  │ Ollama (llm namespace)               │ ← REAL model inference             │
│  │ model: qwen2.5 / qwen3:0.6b         │   actual LLM generation            │
│  │                                      │                                    │
│  │ POST /v1/chat/completions            │                                    │
│  │ → "say hello in one word"            │                                    │
│  │ ← {"choices":[{"message":...}]}     │                                    │
│  └──────────────────────────────────────┘                                    │
│                                                                               │
│  ◀── 200 + inference response returned to buyer via tunnel                   │
│                                                                               │
│  ┌───────────────────────────────────────────────────────────────────────┐    │
│  │ REAL: tunnel, Traefik, x402-verifier, x402-rs, EIP-712, USDC,       │    │
│  │       Ollama, agent reconciliation, CRD, RBAC, Gateway API          │    │
│  │ SIMULATED: chain (Anvil fork, not mainnet), settlement              │    │
│  │ NOT PERSISTENT: quick tunnel URL changes on restart                  │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────────┘
```

| Test | What It Proves |
|------|----------------|
| `Tunnel_RealFacilitatorOllama` | Buyer → CF tunnel → x402 gate → real EIP-712 validation → real Ollama inference → response via tunnel |

**What makes this different from every other test**:

| Component | Phase 6 (existing) | Phase 5+ (Anvil) | Phase 8 (this) |
|-----------|-------------------|-------------------|----------------|
| Inference | Real Ollama | Anvil RPC | Real Ollama |
| Facilitator | Mock (always valid) | Real x402-rs | Real x402-rs |
| Payment signature | Fake JSON blob | Real EIP-712 | Real EIP-712 |
| USDC balance | N/A | Minted on Anvil | Minted on Anvil |
| Entry point | obol.stack:8080 | obol.stack:8080 | **\*.trycloudflare.com** |
| TLS | None (HTTP) | None (HTTP) | **Real TLS** (CF edge) |

**Gap vs real world**: Quick tunnel URL is ephemeral (not a persistent `myagent.example.com`). USDC settlement doesn't transfer real tokens (Anvil resets). No real L1/L2 block finality.

---

## Base Tests — Inference + Skills (12 tests)

**What they cover**: Ollama/Anthropic/OpenAI/Google/Zhipu inference through llmspy, skill staging and injection, skill visibility in pod, skill-driven agent responses.

**Realism**: Very High for inference path. These are the "does the AI actually work" tests.

Not directly part of the monetize subsystem, but they validate the upstream service that gets monetized.

---

## Realism Comparison Matrix

```
                    CRD  RBAC  Agent  Traefik  x402    Facil.  EIP-712  USDC   Ollama  Tunnel  TLS
                    ───  ────  ─────  ───────  ────    ──────  ───────  ────   ──────  ──────  ───
Phase 1 (CRD)        ✓
Phase 2 (RBAC)       ✓    ✓     ✓
Phase 3 (Route)      ✓    ✓     ✓      ✓
Phase 4 (Gate)       ✓    ✓     ✓      ✓       ✓     MOCK    MOCK
Phase 5 (E2E)        ✓    ✓     ✓      ✓       ✓     MOCK    MOCK
Phase 6 (Tunnel)     ✓    ✓     ✓      ✓       ✓     MOCK    MOCK              ✓       ✓      ✓
Phase 7 (Fork)       ✓    ✓     ✓      ✓       ✓     MOCK    MOCK     N/A
Phase 5+ (Real)      ✓    ✓     ✓      ✓       ✓     REAL    REAL     REAL
Phase 8 (FULL)       ✓    ✓     ✓      ✓       ✓     REAL    REAL     REAL     ✓       ✓      ✓

  ✓ = real component    MOCK = simulated    REAL = production-equivalent
```

---

## What's Still Not Tested

| Gap | Impact | Mitigation |
|-----|--------|------------|
| **Real USDC settlement** | Anvil fork doesn't persist transfers | Would need Base Sepolia testnet with real USDC faucet |
| **Persistent named tunnel** | Quick tunnel URL is ephemeral | Phase 8 uses quick tunnel; persistent requires `obol tunnel provision` with CF credentials |
| **Concurrent buyers** | All tests are single-buyer | Add load test with multiple signed payments |
| **ERC-8004 registration** | `obol sell register` not tested end-to-end | Would need real Base Sepolia tx (gas costs) |
| **Price change hot-reload** | Agent updates price in CR → verifier picks up new amount | Test exists partially in Phase 4 format checks |
| **Buy-side flow** | No buyer CLI/SDK test | Planned as next phase |

---

## Running the Tests

```bash
# Prerequisites
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/../../.workspace/config
export OBOL_BIN_DIR=$(pwd)/../../.workspace/bin
export OBOL_DATA_DIR=$(pwd)/../../.workspace/data

# Phase 1-3: CRD + RBAC + Routing (fast, ~2min)
go test -tags integration -v -timeout 5m \
    -run 'TestIntegration_CRD_|TestIntegration_RBAC_|TestIntegration_Monetize_|TestIntegration_Route_' \
    ./internal/openclaw/

# Phase 4-5: Payment gate + E2E (medium, ~5min)
go test -tags integration -v -timeout 10m \
    -run 'TestIntegration_PaymentGate_|TestIntegration_E2E_' \
    ./internal/openclaw/

# Phase 6: Tunnel + Ollama (slow, ~8min, needs Ollama model cached)
go test -tags integration -v -timeout 15m \
    -run 'TestIntegration_Tunnel_' \
    ./internal/openclaw/

# Phase 7: Fork validation (medium, ~5min)
go test -tags integration -v -timeout 10m \
    -run 'TestIntegration_Fork_FullPaymentFlow|TestIntegration_Fork_AgentSkillIteration' \
    ./internal/openclaw/

# Phase 5+: Real facilitator (medium, ~5min, needs x402-rs)
export X402_RS_DIR=/path/to/x402-rs
go test -tags integration -v -timeout 15m \
    -run 'TestIntegration_Fork_RealFacilitatorPayment' \
    ./internal/openclaw/

# Phase 8: FULL — tunnel + Ollama + real facilitator (~8min, needs everything)
export X402_RS_DIR=/path/to/x402-rs
go test -tags integration -v -timeout 15m \
    -run 'TestIntegration_Tunnel_RealFacilitatorOllama' \
    ./internal/openclaw/

# x402 verifier standalone E2E
go test -tags integration -v -timeout 10m \
    -run 'TestIntegration_PaymentGate' \
    ./internal/x402/

# All monetize tests
go test -tags integration -v -timeout 20m ./internal/openclaw/
```
