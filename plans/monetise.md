# Obol Agent: Autonomous Compute Monetization

**Branch:** `feat/secure-enclave-inference` | **Date:** 2026-02-25 | **Status:** Architecture proposal

---

## 1. The Goal

A singleton OpenClaw instance — the **obol-agent** — deployed via `obol agent init`, autonomously monetizes compute resources running in the Obol Stack. A user (or the frontend) declares *what* to expose via a Custom Resource; the obol-agent handles *everything else*: model pulling, health validation, payment gating, public exposure, on-chain registration, and status reporting.

No separate controller binary. No Go operator. The obol-agent is a regular OpenClaw instance with elevated RBAC and the `monetize` skill. Only one obol-agent can exist per cluster; other OpenClaw instances retain standard read-only access.

---

## 2. How It Works

```
                  ┌──────────────────────────────────┐
                  │  User / Frontend / obol CLI       │
                  │                                    │
                  │  kubectl apply -f offer.yaml       │
                  │  OR: frontend POST to k8s API      │
                  │  OR: obol monetize create ...       │
                  └──────────┬───────────────────────────┘
                             │ creates CR
                             ▼
              ┌────────────────────────────────────┐
              │  ServiceOffer CR                    │
              │  apiVersion: obol.network/v1alpha1 │
              │  kind: ServiceOffer                │
              └──────────┬───────────────────────────┘
                         │ read by
                         ▼
              ┌────────────────────────────────────┐
              │  obol-agent (singleton OpenClaw)    │
              │  namespace: openclaw-<id>           │
              │                                    │
              │  Cron job (every 60s):             │
              │    python3 monetize.py process --all│
              │                                    │
              │  `monetize` skill:                 │
              │  1. Read ServiceOffer CRs          │
              │  2. Pull model (if runtime=ollama) │
              │  3. Health-check upstream service   │
              │  4. Create ForwardAuth Middleware   │
              │  5. Create HTTPRoute               │
              │  6. Register on ERC-8004           │
              │  7. Update CR status               │
              └────────────────────────────────────┘
```

The obol-agent uses its mounted ServiceAccount token to talk to the Kubernetes API — the same pattern `kube.py` already uses for read-only monitoring, but extended with write operations for Middleware and HTTPRoute resources.

The reconciliation loop is built on OpenClaw's native **cron system**: a `{ kind: "every", everyMs: 60000 }` job runs `monetize.py process --all` every 60 seconds. No sidecar, no K8s CronJob — the cron scheduler runs inside the OpenClaw Gateway process and persists across pod restarts.

---

## 3. Why Not a Separate Controller

| Concern | Go operator (controller-runtime) | OpenClaw with `monetize` skill |
|---------|----------------------------------|--------------------------------|
| New binary to build/maintain | Yes — new cmd/, Dockerfile, CI | No — skill is a SKILL.md + Python script |
| Hot-updatable logic | No — rebuild + redeploy image | Yes — update skill files on PVC |
| Error handling | Hardcoded retry/backoff | AI reasons about failures, adapts |
| Watch loop | Built-in informer cache | Built-in cron: `monetize.py process --all` every 60s |
| Dependencies | controller-runtime, kubebuilder, code-gen | stdlib Python (`urllib`, `json`, `ssl`) |
| Existing infrastructure | Needs new Deployment, SA, RBAC | Uses existing OpenClaw pod, SA, skill system |

The traditional operator pattern is the right answer when you need guaranteed sub-second reconciliation with leader election. For monetization lifecycle (deploy → expose → register → monitor), OpenClaw acting on ServiceOffer CRs via skills is simpler and leverages everything already built.

---

## 4. The CRD

```yaml
apiVersion: obol.network/v1alpha1
kind: ServiceOffer
metadata:
  name: qwen-inference
  namespace: openclaw-default         # lives alongside the OpenClaw instance
spec:
  # What to serve
  model:
    name: Qwen/Qwen3.5-35B-A3B       # Ollama model tag to pull
    runtime: ollama                    # runtime that serves the model

  # Upstream service (Ollama already running in-cluster)
  upstream:
    service: ollama                    # k8s Service name
    namespace: openclaw-default        # where the service runs
    port: 11434
    healthPath: /api/tags              # endpoint to probe after pull

  # How to price it
  pricing:
    amount: "0.50"
    unit: MTok                         # per million tokens
    currency: USDC
    chain: base

  # Who gets paid
  wallet: "0x1234...abcd"

  # Public path
  path: /services/qwen-inference

  # On-chain advertisement
  register: true
```

```yaml
status:
  conditions:
    - type: ModelReady
      status: "True"
      reason: PullCompleted
      message: "Qwen/Qwen3.5-35B-A3B pulled and loaded on ollama"
    - type: UpstreamHealthy
      status: "True"
      reason: HealthCheckPassed
      message: "Model responds to inference at ollama.openclaw-default.svc:11434"
    - type: PaymentGateReady
      status: "True"
      reason: MiddlewareCreated
      message: "ForwardAuth middleware x402-qwen-inference created"
    - type: RoutePublished
      status: "True"
      reason: HTTPRouteCreated
      message: "Exposed at /services/qwen-inference via traefik-gateway"
    - type: Registered
      status: "True"
      reason: ERC8004Registered
      message: "Registered on Base (tx: 0xabc...)"
    - type: Ready
      status: "True"
      reason: AllConditionsMet
  endpoint: "https://stack.example.com/services/qwen-inference"
  observedGeneration: 1
```

**Design:**
- **Namespace-scoped** — the CR lives in the same namespace as the upstream service. This preserves OwnerReference cascade (garbage collection on delete) and avoids cross-namespace complexity. The obol-agent's ClusterRoleBinding lets it watch ServiceOffers across all namespaces via `GET /apis/obol.network/v1alpha1/serviceoffers` (cluster-wide list).
- **Conditions, not Phase** — [deprecated by API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties). Conditions give granular insight into which step failed.
- **Status subresource** — prevents users from accidentally overwriting status. ([docs](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource))
- **Same-namespace as upstream** — the Middleware and HTTPRoute are created alongside the upstream service. OwnerReferences work (same namespace), so deleting the ServiceOffer garbage-collects the route and middleware. ([docs](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/))

### CRD installation

The CRD manifest is embedded in the infrastructure helmfile (same pattern as `obol-agent.yaml`) and applied during `obol stack init`. No kubebuilder, no code-gen — just a static YAML manifest.

---

## 5. The `monetize` Skill

```
internal/embed/skills/monetize/
├── SKILL.md                    # Teaches OpenClaw when and how to use this skill
├── scripts/
│   └── monetize.py             # K8s API client for ServiceOffer lifecycle
└── references/
    └── x402-pricing.md         # Pricing strategies, chain selection
```

### SKILL.md (summary)

Teaches OpenClaw:
- When a user asks to monetize a service, create a ServiceOffer CR
- When asked to check monetization status, read ServiceOffer CRs and report conditions
- When asked to process offers, run the monetization workflow (health → gate → route → register)
- When asked to stop monetizing, delete the ServiceOffer CR (garbage collection handles cleanup)

### kube.py extension

`kube.py` gains write helpers (`api_post`, `api_patch`, `api_delete`) alongside its existing `api_get`. The read-only contract is preserved by convention: `kube.py` commands remain read-only; `monetize.py` imports the shared helpers and adds write operations. Pure Python stdlib — no new dependencies.

Why not a K8s MCP server? The mounted ServiceAccount token already gives direct API access. An MCP server (e.g., Red Hat's `containers/kubernetes-mcp-server`) adds a sidecar container, image pull, and Helm chart changes for what amounts to wrapping the same REST calls. It's a known upgrade path if K8s operations outgrow script-based tooling, but adds no value today.

### monetize.py

```
python3 monetize.py offers                      # list ServiceOffer CRs
python3 monetize.py process <name>              # run full workflow for one offer
python3 monetize.py process --all               # process all pending offers
python3 monetize.py status <name>               # show conditions
python3 monetize.py create <name> --upstream .. # create a ServiceOffer CR
python3 monetize.py delete <name>               # delete CR (cascades cleanup)
```

Each `process` invocation:

1. **Read the ServiceOffer CR** from the k8s API
2. **Pull the model** — if `spec.model.runtime == ollama`, `POST /api/pull` to Ollama
3. **Health-check** — verify model responds at `<service>.<namespace>.svc:<port>`
4. **Create/update Middleware** — Traefik ForwardAuth pointing at `x402-verifier.x402.svc:8080/verify`
5. **Create/update HTTPRoute** — `parentRef: traefik-gateway`, path from spec, backend = upstream service, filter = the Middleware
6. **ERC-8004 registration** — if `spec.register`, call `signer.py` to sign and submit the registration tx
7. **Update CR status** — set conditions and endpoint

All via the k8s REST API using the mounted ServiceAccount token. No kubectl, no client-go, no external dependencies.

---

## 6. What Gets Created Per ServiceOffer

All resources are created in the **same namespace** as the upstream service (and the ServiceOffer CR). OwnerReferences on the ServiceOffer handle cleanup.

| Resource | Purpose |
|----------|---------|
| `Middleware` (traefik.io/v1alpha1) | ForwardAuth to `x402-verifier.x402.svc:8080/verify` — gates the upstream with payment |
| `HTTPRoute` (gateway.networking.k8s.io/v1) | Routes `spec.path` from Traefik Gateway to upstream, through the Middleware |

That's it. Two resources. The upstream service already runs. The x402 verifier already runs. The Gateway already runs. The tunnel already runs.

### Why no new namespace

The upstream service already has a namespace. Creating a new namespace per offer would mean:
- Cross-namespace OwnerReferences don't work ([docs](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/))
- Need ReferenceGrant for cross-namespace backend refs in HTTPRoute ([docs](https://gateway-api.sigs.k8s.io/api-types/referencegrant/))
- Broader RBAC (namespace create/delete permissions)

Instead: Middleware and HTTPRoute live alongside the upstream. Delete the ServiceOffer CR → Kubernetes cascades the deletion.

### Cross-namespace HTTPRoute → Gateway

The HTTPRoute references `traefik-gateway` in the `traefik` namespace. No ReferenceGrant needed — the Gateway's `allowedRoutes.namespaces.from: All` handles this. ([Gateway API docs](https://gateway-api.sigs.k8s.io/guides/multiple-ns/))

### Middleware locality

Traefik's `ExtensionRef` in HTTPRoute is a `LocalObjectReference` — Middleware must be in the same namespace as the HTTPRoute. The skill creates it there. ([traefik#11126](https://github.com/traefik/traefik/issues/11126))

---

## 7. RBAC: Singleton obol-agent vs Regular OpenClaw

### Two tiers of access

| | obol-agent (singleton) | Regular OpenClaw instances |
|---|---|---|
| **Deployed by** | `obol agent init` | `obol openclaw onboard` |
| **RBAC** | `openclaw-monetize` ClusterRole | Namespace-scoped read-only Role (chart default) |
| **Skills** | All default skills + `monetize` | Default skills only |
| **Cron** | `monetize.py process --all` every 60s | No monetization cron |
| **Count** | Exactly one per cluster | Zero or more |

Only the obol-agent gets the elevated ClusterRole. `obol agent init` enforces the singleton constraint — it refuses to create a second obol-agent if one already exists.

### obol-agent ClusterRole

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: openclaw-monetize
rules:
  # Read/write ServiceOffer CRs
  - apiGroups: ["obol.network"]
    resources: ["serviceoffers"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["obol.network"]
    resources: ["serviceoffers/status"]
    verbs: ["get", "update", "patch"]

  # Create Middleware and HTTPRoute in service namespaces
  - apiGroups: ["traefik.io"]
    resources: ["middlewares"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
  - apiGroups: ["gateway.networking.k8s.io"]
    resources: ["httproutes"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]

  # Read pods/services/endpoints/deployments for health checks (any namespace)
  - apiGroups: [""]
    resources: ["pods", "services", "endpoints"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
```

This is bound to OpenClaw's ServiceAccount via ClusterRoleBinding — the skill needs to read services and create routes across namespaces (e.g., check health of Ollama in `openclaw-default`, create a route for an Ethereum node in `ethereum-knowing-wahoo`).

### What is explicitly NOT granted

| Excluded | Why |
|----------|-----|
| `secrets` (cluster-wide) | OpenClaw has secrets access in its own namespace only (chart default) |
| `rbac.authorization.k8s.io/*` | Cannot modify its own permissions |
| `namespaces` create/delete | Doesn't create namespaces |
| `deployments` create/update | Doesn't create workloads — gates existing ones |
| `configmaps` create (cluster-wide) | Reads config for diagnostics, doesn't write it |

### How this gets applied

The ClusterRole and ClusterRoleBinding are added to the OpenClaw helmfile generation in `internal/openclaw/openclaw.go`, same as the existing `rbac.create: true` overlay. When `obol openclaw onboard` runs, the chart deploys these RBAC resources alongside the pod.

**Ref:** [RBAC Good Practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)

### Fix the existing `admin` RoleBinding

The per-network `agent-rbac.yaml` currently binds the `admin` ClusterRole, which includes Secrets and RBAC manipulation. Replace with a scoped ClusterRole (read pods/services + write Middleware/HTTPRoute).

---

## 8. Admission Policy Guardrail

Defense-in-depth via [ValidatingAdmissionPolicy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/) (GA in k8s 1.30, available in k3s 1.31):

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: openclaw-monetize-guardrail
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["traefik.io"]
        apiVersions: ["v1alpha1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["middlewares"]
      - apiGroups: ["gateway.networking.k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["httproutes"]
  matchConditions:
    - name: is-openclaw
      expression: >-
        request.userInfo.username.startsWith("system:serviceaccount:openclaw-")
  validations:
    # HTTPRoutes must reference traefik-gateway only
    - expression: >-
        object.spec.parentRefs.all(ref,
          ref.name == "traefik-gateway" && ref.?namespace.orValue("traefik") == "traefik"
        )
      message: "OpenClaw can only attach routes to traefik-gateway"
    # Middlewares must use ForwardAuth to x402-verifier only
    - expression: >-
        !has(object.spec.forwardAuth) ||
        object.spec.forwardAuth.address.startsWith("http://x402-verifier.x402.svc")
      message: "ForwardAuth must point to x402-verifier"
```

Even if RBAC allows creating any Middleware, the admission policy ensures OpenClaw can only create ForwardAuth rules pointing at the legitimate x402 verifier. A prompt injection can't make it route traffic to an attacker-controlled auth endpoint.

---

## 9. The Full Flow

```
1. User: "Monetize Qwen3.5-35B-A3B on Ollama at $0.50 per M token on Base"

2. OpenClaw (using monetize skill) creates the ServiceOffer CR:
   python3 monetize.py create qwen-inference \
     --model Qwen/Qwen3.5-35B-A3B --runtime ollama \
     --upstream ollama --namespace openclaw-default --port 11434 \
     --price 0.50 --unit MTok --chain base --wallet 0x... --register
   → Creates ServiceOffer CR via k8s API

3. OpenClaw processes the offer:
   python3 monetize.py process qwen-inference

   Step 1: Pull the model through Ollama
     POST http://ollama.openclaw-default.svc:11434/api/pull
       {"name": "Qwen/Qwen3.5-35B-A3B"}
     → Streams download progress, waits for completion
     → sets condition: ModelReady=True

   Step 2: Health-check the model is loaded
     POST http://ollama.openclaw-default.svc:11434/api/generate
       {"model": "Qwen/Qwen3.5-35B-A3B", "prompt": "ping", "stream": false}
     → 200 OK, model responds
     → sets condition: UpstreamHealthy=True

   Step 3: Create ForwardAuth Middleware
     POST /apis/traefik.io/v1alpha1/namespaces/openclaw-default/middlewares
     → ForwardAuth → x402-verifier.x402.svc:8080/verify
     → sets condition: PaymentGateReady=True

   Step 4: Create HTTPRoute
     POST /apis/gateway.networking.k8s.io/v1/namespaces/openclaw-default/httproutes
     → parentRef: traefik-gateway, path: /services/qwen-inference
     → filter: ExtensionRef to Middleware
     → backendRef: ollama:11434
     → sets condition: RoutePublished=True

   Step 5: ERC-8004 registration
     python3 signer.py ... (signs registration tx)
     → sets condition: Registered=True

   Step 6: Update status
     PATCH /apis/obol.network/v1alpha1/.../serviceoffers/qwen-inference/status
     → Ready=True, endpoint=https://stack.example.com/services/qwen-inference

4. User: "What's the status?"
   python3 monetize.py status qwen-inference
   → Shows conditions table + endpoint + model info

5. External consumer pays and calls:
   POST https://stack.example.com/services/qwen-inference/v1/chat/completions
   X-Payment: <x402 header>
   → Traefik → ForwardAuth (x402-verifier) → Ollama (Qwen3.5-35B-A3B)
```

---

## 10. What the `obol` CLI Does

The CLI becomes a thin CRD client — no deployment logic, no helmfile:

```bash
obol monetize create --upstream ollama --price 0.001 --chain base
# → creates ServiceOffer CR (same as kubectl apply)

obol monetize list
# → kubectl get serviceoffers (formatted)

obol monetize status qwen-inference
# → shows conditions, endpoint, pricing

obol monetize delete qwen-inference
# → deletes CR (OwnerReference cascades cleanup)
```

The frontend can do the same via the k8s API directly.

---

## 11. What We Keep, What We Drop, What We Add

| Component | Action | Reason |
|-----------|--------|--------|
| `cmd/x402-verifier/` | **Keep** | ForwardAuth verifier — the payment gate |
| `internal/x402/` | **Keep** | Verifier handler |
| `internal/erc8004/` | **Keep** | On-chain registration (called by `monetize.py` via `signer.py`) |
| `internal/enclave/` | **Keep** | Secure Enclave signing (orthogonal to monetization) |
| `internal/inference/gateway.go` | **Drop** | Inline x402 middleware — replaced by ForwardAuth |
| `internal/inference/store.go` | **Drop** | Deployment config on disk — replaced by CRD |
| `obol-agent.yaml` (busybox pod) | **Drop** | OpenClaw IS the agent; no separate placeholder pod |
| `agent-rbac.yaml` (`admin` binding) | **Replace** | Scoped ClusterRole instead of `admin` |
| `cmd/obol/service.go` | **Simplify** | Thin CRD client |
| `cmd/obol/monetize.go` | **Simplify** | Thin CRD client |
| `internal/embed/skills/monetize/` | **Add** | New skill: SKILL.md + `monetize.py` + references |
| ServiceOffer CRD manifest | **Add** | Intent interface, applied during `obol stack init` |
| ValidatingAdmissionPolicy | **Add** | Guardrail on what OpenClaw can create |
| `openclaw-monetize` ClusterRole | **Add** | Scoped write access for Middleware/HTTPRoute |

---

## 12. Resolved Decisions

| Question | Decision | Rationale |
|----------|----------|-----------|
| **Polling vs event-driven** | OpenClaw cron job, every 60s | OpenClaw has a built-in cron scheduler (`{ kind: "every", everyMs: 60000 }`). No sidecar, no K8s CronJob — runs inside the Gateway process. Jobs persist across restarts via `~/.openclaw/cron/jobs.json`. |
| **Multi-instance** | Singleton obol-agent | Only one obol-agent per cluster, enforced by `obol agent init`. Other OpenClaw instances keep read-only RBAC and no `monetize` skill. No coordination problem. |
| **CRD scope** | Namespace-scoped | OwnerReference cascade works (same namespace as Middleware/HTTPRoute). The obol-agent's ClusterRoleBinding lets it list ServiceOffers across all namespaces. Standard `kubectl get serviceoffers -A` works. |
| **K8s API access** | Extend `kube.py` with write helpers | `kube.py` gains `api_post`, `api_patch`, `api_delete` alongside `api_get`. `monetize.py` imports the shared helpers. Pure stdlib, zero new dependencies. K8s MCP server (Red Hat `containers/kubernetes-mcp-server`) is a known upgrade path but unnecessary today. |

---

## References

| Topic | Link |
|-------|------|
| Custom Resource Definitions | https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/ |
| CRD status subresource | https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource |
| API conventions (conditions) | https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md |
| RBAC | https://kubernetes.io/docs/reference/access-authn-authz/rbac/ |
| RBAC good practices | https://kubernetes.io/docs/concepts/security/rbac-good-practices/ |
| ValidatingAdmissionPolicy | https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/ |
| OwnerReferences | https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/ |
| Cross-namespace routing (Gateway API) | https://gateway-api.sigs.k8s.io/guides/multiple-ns/ |
| ReferenceGrant | https://gateway-api.sigs.k8s.io/api-types/referencegrant/ |
| Accessing API from a pod | https://kubernetes.io/docs/tasks/run-application/access-api-from-pod/ |
| Pod Security Standards | https://kubernetes.io/docs/concepts/security/pod-security-standards/ |
| Service account tokens | https://kubernetes.io/docs/concepts/security/service-accounts/ |
| Traefik ForwardAuth | https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/ |
| Traefik Middleware locality | https://github.com/traefik/traefik/issues/11126 |
