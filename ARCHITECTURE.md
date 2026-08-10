# Obol Stack Architecture

**Version**: 1.0.0-draft
**Status**: Draft
**Last Updated**: 2026-05-20

Visual companion to [SPEC.md](SPEC.md). This file is intentionally compact: use it to orient agents, then jump to the referenced SPEC sections.

## 1. Principles

| ID | Principle | Consequence |
|----|-----------|-------------|
| AP1 | Local-first control plane | k3d/k3s, local config dirs, embedded charts, CRDs as state. |
| AP2 | Standards-native commerce | x402 for payments, ERC-8004 for discovery, no central bazaar. |
| AP3 | Intent via CRDs | CLI writes intent; controllers converge resources and status. |
| AP4 | Bounded buyer risk | Pre-signed auth pools, no runtime signer in x402-buyer. |
| AP5 | Public by allowlist | Tunnel exposes only catalog, registration, and paid service routes. |

## 2. System Context

```mermaid
flowchart LR
    Operator["Operator"] --> CLI["obol CLI"]
    Seller["Service Provider"] --> CLI
    Buyer["Buyer Agent"] --> BuySkill["buy-x402 skill"]
    CLI --> Stack["Local Obol Stack"]
    BuySkill --> Stack
    Stack --> Ollama["Host Ollama or provider APIs"]
    Stack --> Facilitator["x402 Facilitator"]
    Stack --> Registry["ERC-8004 Identity Registry"]
    Indexer["Permissionless Indexer"] --> Registry
    Indexer --> WellKnown["/.well-known/agent-registration.json"]
    Buyer --> PublicRoutes["Public tunnel routes"]
    PublicRoutes --> Stack
```

References: SPEC 1, 3.

## 3. Runtime Containers

```mermaid
flowchart TB
    subgraph Host["Host machine"]
        CLI["obol CLI"]
        DataDir["config and data dirs"]
        HostOllama["Ollama or OpenAI-compatible server"]
    end

    subgraph Cluster["k3d or k3s cluster"]
        subgraph LLM["namespace: llm"]
            LiteLLM["LiteLLM"]
            Buyer["x402-buyer sidecar"]
            BuyerCM["buyer config and auth ConfigMaps"]
            OllamaSvc["ollama Service and Endpoints"]
        end

        subgraph X402["namespace: x402"]
            Verifier["x402-verifier"]
            Controller["serviceoffer-controller"]
            Catalog["skill.md and services.json httpd"]
            IdentityDoc["registration document httpd"]
        end

        subgraph Traefik["namespace: traefik"]
            Gateway["Traefik Gateway"]
            Cloudflared["cloudflared"]
            Storefront["public storefront"]
        end

        subgraph DefaultAgent["namespace: hermes-obol-agent"]
            MasterHermes["default Hermes"]
            MasterSigner["remote-signer"]
        end

        subgraph ChildAgent["namespace: agent-name"]
            ChildHermes["child Hermes"]
            ChildPVC["hermes-data PVC"]
            ChildSigner["optional remote-signer"]
        end
    end

    CLI --> DataDir
    CLI --> Controller
    DataDir --> ChildPVC
    HostOllama --> OllamaSvc --> LiteLLM
    LiteLLM --> Buyer
    Controller --> Verifier
    Controller --> Catalog
    Controller --> IdentityDoc
    Gateway --> Verifier
    Cloudflared --> Gateway
    Storefront --> Catalog
    MasterHermes --> LiteLLM
    ChildHermes --> LiteLLM
```

References: SPEC 2, 4.

## 4. Module Decomposition

| Module | Runtime | State | SPEC |
|--------|---------|-------|------|
| CLI | host process | config dir, kube API | 3 |
| Stack lifecycle | host process plus Helmfile | stack ID, kubeconfig, defaults | 5.1 |
| LiteLLM | `llm` Deployment | ConfigMap, Secret | 5.2 |
| Agent CRD reconciler | serviceoffer-controller | Agent status, child resources | 5.3 |
| ServiceOffer reconciler | serviceoffer-controller | ServiceOffer status, routes | 5.4 |
| x402 verifier/proxy | `x402-verifier` | in-memory route rules | 3.3, 5.4 |
| PurchaseRequest reconciler | serviceoffer-controller | ConfigMaps, LiteLLM route | 5.6 |
| x402-buyer | LiteLLM sidecar | auth ConfigMap, pod-local consumed state | 5.6 |
| ERC-8004 renderer | serviceoffer-controller plus httpd | AgentIdentity status, registration doc | 5.7 |
| Tunnel/storefront | cloudflared plus Next.js | tunnel state, storefront resources | 5.8 |

## 5. Sell Agent Flow

```mermaid
sequenceDiagram
    participant O as Operator
    participant CLI as obol CLI
    participant API as Kubernetes API
    participant C as serviceoffer-controller
    participant A as Agent namespace
    participant V as x402-verifier
    participant T as Traefik
    participant B as Buyer

    O->>CLI: obol agent new quant --model M --skills S --create-wallet
    CLI->>API: apply Namespace and Agent
    C->>API: watch Agent
    C->>A: apply Hermes PVC, ConfigMap, Secret, Deployment, Service
    C->>A: optional remote-signer
    C->>API: update Agent status endpoint, wallet, Ready
    O->>CLI: obol sell agent quant --price P --token OBOL
    CLI->>API: apply ServiceOffer type=agent
    C->>API: resolve Agent ref
    C->>API: write status.agentResolution
    C->>T: apply HTTPRoute and ReferenceGrant
    V->>API: watch ServiceOffer routes
    B->>T: unpaid request
    T->>V: ForwardAuth
    V-->>B: 402 with agentModel, agentSkills, agentRuntime
```

References: SPEC 5.3, 5.5.

## 6. Sell HTTP/Inference Flow

```mermaid
sequenceDiagram
    participant O as Operator
    participant CLI as obol CLI
    participant API as Kubernetes API
    participant C as serviceoffer-controller
    participant U as Upstream Service
    participant V as x402-verifier
    participant T as Traefik

    O->>CLI: obol sell http or sell inference
    CLI->>API: apply ServiceOffer
    C->>API: add finalizer and status
    C->>U: GET healthPath
    C->>API: apply ReferenceGrant
    C->>API: verify x402-verifier Service and Deployment
    C->>API: apply HTTPRoute
    C->>API: apply RegistrationRequest if enabled
    V->>API: watch RoutePublished ServiceOffers
    T->>V: ForwardAuth for /services/name/*
```

References: SPEC 5.4.

## 7. Buy Paid Inference Flow

```mermaid
sequenceDiagram
    participant U as User
    participant CLI as obol buy inference
    participant H as default Hermes pod
    participant Py as buy.py
    participant API as Kubernetes API
    participant C as serviceoffer-controller
    participant L as LiteLLM
    participant X as x402-buyer
    participant S as Seller endpoint
    participant F as x402 Facilitator

    U->>CLI: buy endpoint, model, budget
    CLI->>S: pricing probe
    CLI->>S: optional registration fetch
    CLI->>H: exec buy.py buy
    Py->>S: probe 402
    Py->>Py: sign bounded auth pool
    Py->>API: create PurchaseRequest
    C->>S: probe and validate pricing
    C->>API: write buyer ConfigMaps
    C->>L: add paid/model route
    C->>X: reload
    H->>L: request model paid/model
    L->>X: proxy request
    X->>S: request, receive 402, attach X-PAYMENT, retry
    S->>F: verify and settle
    S-->>X: paid response
    X-->>L: response
```

References: SPEC 5.6.

## 8. Discovery and Registration Flow

```mermaid
sequenceDiagram
    participant C as serviceoffer-controller
    participant API as Kubernetes API
    participant D as registration document httpd
    participant R as ERC-8004 registry
    participant CLI as obol sell register
    participant I as Indexer

    C->>API: ensure AgentIdentity x402/default
    C->>API: create RegistrationRequest owner
    C->>D: publish agent-registration.json
    CLI->>R: submit registration transaction
    C->>R: observe matching registration
    C->>API: update AgentIdentity status registrations
    I->>R: index agentId
    I->>D: fetch registration JSON
```

References: SPEC 5.7.

## 9. Public Network Topology

```mermaid
flowchart LR
    Internet["Internet"] --> CF["Cloudflare Tunnel"]
    CF --> Gateway["Traefik Gateway"]
    Gateway --> Services["/services/*"]
    Gateway --> Skill["/skill.md"]
    Gateway --> APIJSON["/api/services.json"]
    Gateway --> WellKnown["/.well-known/agent-registration.json"]
    Gateway --> Home["/ storefront"]
    Services --> Verifier["x402-verifier"]
    Skill --> Catalog["catalog httpd"]
    APIJSON --> Catalog
    WellKnown --> Identity["identity httpd"]
    Home --> Storefront["Next.js storefront"]
```

Internal-only surfaces must remain hostname-restricted to `obol.stack`: frontend, eRPC, LiteLLM, monitoring.

References: SPEC 3.2, 5.8, 6.

## 10. Storage

| Store | Entities | Notes |
|-------|----------|-------|
| Host config dir | stack ID, backend, kubeconfig, tunnel state | Local operator state. |
| Host data dir | Hermes homes, child agent seed files, PVC backing data | Local-path provisioner maps into pods. |
| Kubernetes API | CRDs, child resources, status | Main control-plane state. |
| Secrets | LiteLLM key, remote-signer keystores, API tokens | Namespaced; avoid copying into docs/logs. |
| ConfigMaps | LiteLLM config, buyer config/auths, catalogs | Controller writes per-purchase keys. |
| Pod emptyDir | x402-buyer consumed state | Reason LiteLLM replicas stay at 1. |
| Chain | ERC-8004 identity NFT/URI | Observed by controller, not minted by controller. |
