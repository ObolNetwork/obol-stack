# Agent Services: Autonomous x402-Gated HTTP Endpoints

**Goal:** A skill that lets OpenClaw deploy its own HTTP services into the cluster, gate them with x402 payments, register them with ERC-8004, expose them to the public internet, and monitor earnings — turning the agent from a tool-user into an autonomous economic actor.

---

## Why This Is The One

The Obol Stack already has every piece:

| Capability | How it exists today |
|------------|-------------------|
| Wallet | Web3Signer in-cluster, `signer.py` for signing |
| Onchain identity | `agent-identity` skill, ERC-8004 registration |
| Kubernetes cluster | k3d with Traefik gateway |
| Public internet access | Cloudflare tunnel (`obol tunnel`) |
| x402 payment infrastructure | `inference-gateway` binary, Go x402 SDK, Coinbase facilitator |
| Blockchain nodes | eRPC gateway routing to local/remote nodes |

What's missing: **the agent can't deploy a service, price it, and collect payment.** This skill closes that gap.

---

## Existing Precedent: The Inference Gateway

The `inference` network (`internal/embed/networks/inference/`) already implements this exact pattern:

1. User specifies a model, price, wallet, and chain
2. Helmfile deploys: Ollama pod + x402 gateway pod + Service + HTTPRoute + metadata ConfigMap
3. Gateway wraps Ollama's OpenAI-compatible API with x402 payment verification
4. Traefik routes `/inference-<id>/v1/*` to the gateway
5. Cloudflare tunnel makes it publicly accessible
6. Frontend discovers it via the metadata ConfigMap

**The `agent-services` skill generalises this pattern** from "inference only" to "any HTTP handler the agent writes."

---

## Architecture

```
OpenClaw pod (writes handler + config)
  │
  │  1. Agent writes handler.py (business logic)
  │  2. identity.sh registers with ERC-8004
  │  3. service.sh deploys via helmfile
  │
  ▼
agent-service-<name> namespace
  ┌─────────────────────────────┐
  │  Pod: agent-svc-<name>      │
  │  ┌────────────────────────┐ │
  │  │ x402-proxy (sidecar)   │ │  ← Verifies payment, settles via facilitator
  │  │ port 8402              │ │
  │  └──────────┬─────────────┘ │
  │             │ proxy_pass    │
  │  ┌──────────▼─────────────┐ │
  │  │ handler.py (main)      │ │  ← Agent's business logic (plain HTTP)
  │  │ port 8080              │ │
  │  └────────────────────────┘ │
  │                             │
  │  ConfigMap: handler-code    │  ← Agent's Python handler
  │  ConfigMap: svc-metadata    │  ← Pricing, endpoints, description
  │  Service: agent-svc-<name>  │  ← ClusterIP, port 8402
  │  HTTPRoute: agent-svc-<name>│  ← /services/<name>/* → port 8402
  └─────────────────────────────┘
         │
         ▼
  Traefik Gateway (traefik namespace)
         │
         ▼
  Cloudflare Tunnel → https://<domain>/services/<name>/*
```

### Why a Sidecar Proxy?

The agent writes **plain HTTP handlers** — no x402 awareness needed. A sidecar `x402-proxy` container handles all payment logic:

1. Receives inbound request
2. If no payment header → responds `402 Payment Required` with pricing
3. If payment header present → verifies signature via facilitator
4. If valid → proxies request to handler on `localhost:8080`
5. Settles payment onchain via facilitator
6. Returns handler response with `PAYMENT-RESPONSE` header

**Benefits:**
- Agent doesn't need to understand x402 protocol internals
- Same proxy image reused across all services (already exists as `inference-gateway`)
- Handler can be any language/framework — just serve HTTP on port 8080
- Payment config is environment variables, not code

### The x402 Proxy Image

The existing `inference-gateway` (`cmd/inference-gateway/main.go`) is already a generic x402 reverse proxy. It takes `--upstream`, `--wallet`, `--price`, `--chain`, `--facilitator` flags and wraps any upstream HTTP service with x402 payment gates.

**Reuse strategy:** The inference gateway image (`ghcr.io/obolnetwork/inference-gateway`) can proxy any upstream, not just Ollama. For `agent-services`, the upstream is `http://localhost:8080` (the agent's handler running in the same pod).

If needed, we can extract the generic proxy into its own image (`ghcr.io/obolnetwork/x402-proxy`) later. For now, the inference gateway binary works as-is.

---

## Skill Structure

```
agent-services/
├── SKILL.md
├── scripts/
│   └── service.sh              # Deploy, list, update, teardown, monitor
├── templates/
│   ├── helmfile.yaml.gotmpl    # Helmfile template for service deployment
│   ├── handler.py.tmpl         # Minimal Python handler scaffold
│   └── metadata.json.tmpl      # Service metadata template
└── references/
    └── x402-server-patterns.md # Pricing strategies, facilitator config, chain selection
```

### `service.sh` Commands

```bash
# === Lifecycle ===

# Deploy a new service from a handler file
sh scripts/service.sh deploy \
  --name weather-api \
  --handler ./my_handler.py \
  --price 0.10 \
  --chain base \
  --wallet 0xYourAddress \
  --description "Real-time weather data" \
  --register                        # auto-register endpoint with ERC-8004

# Deploy with the scaffold template (agent fills in the handler later)
sh scripts/service.sh scaffold --name weather-api
# → Creates handler.py from template, agent edits it, then deploys

# Update handler code (patches ConfigMap, restarts pod)
sh scripts/service.sh update --name weather-api --handler ./updated_handler.py

# Update pricing (patches gateway config, no restart needed)
sh scripts/service.sh set-price --name weather-api --price 0.05

# Tear down a service (deletes namespace + all resources)
sh scripts/service.sh teardown --name weather-api

# === Discovery ===

# List deployed services with status and URLs
sh scripts/service.sh list

# Show service details (pricing, endpoints, health, earnings)
sh scripts/service.sh status --name weather-api

# === Monitoring ===

# Check USDC earnings for a service's wallet
sh scripts/service.sh earnings --name weather-api

# View service logs
sh scripts/service.sh logs --name weather-api [--tail 100]

# Health check
sh scripts/service.sh health --name weather-api
```

### How `deploy` Works Internally

```
1. Validate inputs (handler file exists, chain supported, wallet valid)

2. Create deployment directory:
   $CONFIG_DIR/services/<name>/
   ├── helmfile.yaml      ← generated from template
   ├── handler.py         ← copied from --handler
   └── values.yaml        ← generated (price, chain, wallet, etc.)

3. Run helmfile sync:
   helmfile -f $CONFIG_DIR/services/<name>/helmfile.yaml sync

   This creates:
   - Namespace: agent-svc-<name>
   - ConfigMap: handler-code (contains handler.py)
   - ConfigMap: svc-metadata (pricing, description, endpoints)
   - Deployment: agent-svc-<name> (2 containers: handler + x402 proxy)
   - Service: agent-svc-<name> (ClusterIP, port 8402)
   - HTTPRoute: agent-svc-<name> (path: /services/<name>/*)

4. Wait for pod ready

5. If --register flag:
   sh scripts/identity.sh --from $WALLET register \
     --uri "ipfs://$(pin metadata.json)"
   # Or update existing agent's service endpoints
```

### Handler Template (`handler.py.tmpl`)

The agent gets a minimal scaffold to fill in. No x402 awareness needed — just return HTTP responses.

```python
#!/usr/bin/env python3
"""
Agent service handler — {{.Name}}
{{.Description}}

This runs behind an x402 payment proxy. Requests that reach this
handler have already been paid for. Just return the data.

Serve on port 8080 (the proxy forwards paid requests here).
"""
import json
from http.server import HTTPServer, BaseHTTPRequestHandler


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        """Handle GET requests."""
        # TODO: implement your service logic here
        data = {"message": "Hello from {{.Name}}"}

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def do_POST(self):
        """Handle POST requests."""
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b""

        # TODO: process the request body
        data = {"received": len(body)}

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        """Structured logging."""
        print(f"[{{.Name}}] {args[0]}")


if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", 8080), Handler)
    print(f"[{{.Name}}] Serving on :8080")
    server.serve_forever()
```

### Helmfile Template (`helmfile.yaml.gotmpl`)

```yaml
releases:
  - name: agent-svc-{{ .Values.name }}
    namespace: agent-svc-{{ .Values.name }}
    createNamespace: true
    chart: bedag/raw
    version: 2.1.0
    values:
      - resources:
          # --- Handler code as ConfigMap ---
          - apiVersion: v1
            kind: ConfigMap
            metadata:
              name: handler-code
            data:
              handler.py: |
{{ .Values.handlerCode | indent 16 }}

          # --- Service metadata for discovery ---
          - apiVersion: v1
            kind: ConfigMap
            metadata:
              name: svc-metadata
              labels:
                app.kubernetes.io/part-of: obol.stack
                obol.stack/app: agent-service
                obol.stack/service-name: {{ .Values.name }}
            data:
              metadata.json: |
                {
                  "name": "{{ .Values.name }}",
                  "description": "{{ .Values.description }}",
                  "pricing": {
                    "pricePerRequest": "{{ .Values.price }}",
                    "currency": "USDC",
                    "chain": "{{ .Values.chain }}"
                  },
                  "endpoints": {
                    "external": "{{ .Values.publicURL }}/services/{{ .Values.name }}",
                    "internal": "http://agent-svc-{{ .Values.name }}.agent-svc-{{ .Values.name }}.svc.cluster.local:8402"
                  }
                }

          # --- Deployment: handler + x402 proxy sidecar ---
          - apiVersion: apps/v1
            kind: Deployment
            metadata:
              name: agent-svc-{{ .Values.name }}
            spec:
              replicas: 1
              selector:
                matchLabels:
                  app: agent-svc-{{ .Values.name }}
              template:
                metadata:
                  labels:
                    app: agent-svc-{{ .Values.name }}
                spec:
                  containers:
                    # Handler container — agent's business logic
                    - name: handler
                      image: python:3.12-slim
                      command: ["python3", "/app/handler.py"]
                      ports:
                        - containerPort: 8080
                      volumeMounts:
                        - name: handler-code
                          mountPath: /app
                      readinessProbe:
                        httpGet:
                          path: /
                          port: 8080
                        initialDelaySeconds: 3
                        periodSeconds: 5

                    # x402 proxy sidecar — payment verification + settlement
                    - name: x402-proxy
                      image: ghcr.io/obolnetwork/inference-gateway:latest
                      args:
                        - --listen=:8402
                        - --upstream=http://localhost:8080
                        - --wallet={{ .Values.wallet }}
                        - --price={{ .Values.price }}
                        - --chain={{ .Values.chain }}
                        - --facilitator={{ .Values.facilitator }}
                      ports:
                        - containerPort: 8402
                      readinessProbe:
                        httpGet:
                          path: /health
                          port: 8402
                        initialDelaySeconds: 5
                        periodSeconds: 10

                  volumes:
                    - name: handler-code
                      configMap:
                        name: handler-code

          # --- Service ---
          - apiVersion: v1
            kind: Service
            metadata:
              name: agent-svc-{{ .Values.name }}
            spec:
              selector:
                app: agent-svc-{{ .Values.name }}
              ports:
                - port: 8402
                  targetPort: 8402
                  name: x402

          # --- HTTPRoute (Traefik) ---
          - apiVersion: gateway.networking.k8s.io/v1
            kind: HTTPRoute
            metadata:
              name: agent-svc-{{ .Values.name }}
            spec:
              parentRefs:
                - name: traefik-gateway
                  namespace: traefik
                  sectionName: web
              rules:
                - matches:
                    - path:
                        type: PathPrefix
                        value: /services/{{ .Values.name }}
                  filters:
                    - type: URLRewrite
                      urlRewrite:
                        path:
                          type: ReplacePrefixMatch
                          replacePrefixMatch: /
                  backendRefs:
                    - name: agent-svc-{{ .Values.name }}
                      port: 8402
```

---

## Integration With Existing Skills

| Skill | Integration point |
|-------|------------------|
| `agent-identity` | `--register` flag calls `identity.sh register` or `identity.sh set-uri` to advertise the service endpoint in ERC-8004 |
| `local-ethereum-wallet` | Wallet address for x402 payment settlement; `signer.py` for any onchain operations |
| `ethereum-networks` | `rpc.sh` to check USDC balance, query payment transactions, verify settlement |
| `obol-stack` | `kube.py` to monitor service pod health, logs, events |
| `standards` | x402 protocol reference, pricing strategies, facilitator documentation |

---

## RBAC Requirements

The OpenClaw pod currently has **read-only access to its own namespace**. To deploy services, it needs:

### Option A: Expand OpenClaw's RBAC (Simple, Less Isolated)

Add a ClusterRole that lets OpenClaw create resources in `agent-svc-*` namespaces:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: openclaw-service-deployer
rules:
  - apiGroups: [""]
    resources: ["namespaces", "configmaps", "services"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["gateway.networking.k8s.io"]
    resources: ["httproutes"]
    verbs: ["get", "list", "create", "update", "delete"]
```

### Option B: Deploy via `obol` CLI (Preferred, Uses Existing Patterns)

Don't give OpenClaw direct k8s write access. Instead:

1. `service.sh` writes the helmfile + handler to the **host PVC** (same pattern as skills injection)
2. A lightweight controller or CronJob watches for new service definitions and runs `helmfile sync`
3. Or: the agent calls `obol` CLI via the existing passthrough pattern

**Recommended: Option B** — it follows the existing principle that OpenClaw doesn't mutate cluster state directly. The `obol` binary handles deployment, OpenClaw handles the intent.

In practice, `service.sh deploy` would:
1. Write helmfile + handler + values to `$DATA_DIR/services/<name>/`
2. Call the `obol` CLI wrapper (already available in `$PATH`) to run helmfile sync
3. The `obol` CLI has full kubeconfig access and handles the deployment

This mirrors how `obol network install` + `obol network sync` work — config is staged, then synced.

---

## Service Lifecycle

### Deploy
```
Agent writes handler → service.sh deploy → helmfile sync → pod running → HTTPRoute active → tunnel exposes → ERC-8004 registered
```

### Update Handler
```
Agent edits handler → service.sh update → ConfigMap patched → pod restarted → same URL, new logic
```

### Update Price
```
service.sh set-price → x402 proxy config updated → restarts sidecar only → price change takes effect
```

### Teardown
```
service.sh teardown → helmfile destroy → namespace deleted → ERC-8004 URI updated (mark inactive)
```

### Monitor
```
service.sh earnings → rpc.sh checks USDC balance → shows delta since deployment
service.sh status → pod health + request count + uptime + reputation score
```

---

## Pricing Strategies (Reference Material)

The `x402-server-patterns.md` reference would cover:

### Scheme: `exact` (Live)
Fixed price per request. Simple, predictable.
```
Price: $0.10 USDC per weather query
Price: $0.001 USDC per data point
```

### Scheme: `upto` (Emerging)
Client authorises a maximum, server settles actual cost. Critical for metered services:
```
LLM inference: max $0.50, settle per token generated
Compute jobs: max $1.00, settle per second of runtime
Data queries: max $0.10, settle per row returned
```

### Free Tier Pattern
Set price to 0 for discovery/reputation building. Upgrade later:
```bash
# Start free to build reputation
sh scripts/service.sh deploy --name weather-api --handler ./handler.py --price 0 --register

# After building reputation, add pricing
sh scripts/service.sh set-price --name weather-api --price 0.05
```

### Chain Selection
| Chain | Gas cost per settlement | Best for |
|-------|------------------------|----------|
| Base | ~$0.001 | Consumer services, micropayments |
| Base Sepolia | Free (testnet) | Development, testing |
| Polygon | ~$0.005 | Medium-value services |
| Avalanche | ~$0.01 | Higher-value services |

---

## Implementation Order

| Phase | Work | Effort | Dependencies |
|-------|------|--------|-------------|
| **1** | Create `agent-services` SKILL.md | Small | None |
| **2** | Create `service.sh` — scaffold + deploy + teardown | Large | Helmfile template |
| **3** | Create helmfile.yaml.gotmpl + handler.py.tmpl | Medium | Inference gateway image |
| **4** | Create `x402-server-patterns.md` reference | Small | None |
| **5** | Add `service.sh` — update, set-price, list, status | Medium | Phase 2 |
| **6** | Add `service.sh` — earnings monitoring, logs, health | Small | Phase 2 |
| **7** | Add `--register` flag (ERC-8004 integration) | Small | `agent-identity` skill |
| **8** | Add RBAC / obol CLI integration for deployment | Medium | Decision on Option A vs B |
| **9** | Test end-to-end: deploy → pay → earn → rate cycle | Large | All phases |

### Phase 1-4 delivers a working MVP. Phases 5-9 add polish and integration.

---

## Validation Criteria

- [ ] Agent can scaffold a handler template with `service.sh scaffold`
- [ ] Agent can deploy a handler that serves HTTP on a public URL
- [ ] Unauthenticated requests receive `402 Payment Required` with pricing info
- [ ] Paid requests (valid x402 signature) reach the handler and return data
- [ ] Payment settles onchain (USDC transferred to agent's wallet)
- [ ] Agent can update handler code without changing the URL
- [ ] Agent can update pricing without redeploying
- [ ] Agent can tear down a service cleanly
- [ ] Agent can list deployed services with status
- [ ] Agent can check USDC earnings
- [ ] `--register` flag creates/updates ERC-8004 registration with service endpoint
- [ ] Service is discoverable by other agents via ERC-8004 + reputation queries
- [ ] All scripts are POSIX sh, work in the OpenClaw pod
- [ ] Follows existing Obol Stack patterns (helmfile, namespace isolation, Traefik HTTPRoute)

---

## Open Questions

1. **x402 proxy image:** Reuse `inference-gateway` as-is, or extract a generic `x402-proxy` image? The inference gateway already accepts `--upstream` so it works, but the name is misleading for non-inference services.

2. **Handler language:** Start with Python-only (stdlib HTTPServer, no dependencies)? Or support a generic Docker image where the agent provides a Dockerfile?

3. **ConfigMap size limit:** Handler code goes in a ConfigMap (1MB limit). For larger services, should we use the PVC injection pattern instead? 1MB is generous for a Python handler but could be limiting for services with bundled data.

4. **Multi-endpoint services:** One handler = one service = one price? Or support multiple endpoints with different prices within a single service? The x402 middleware can be configured per-path.

5. **Service discovery by other agents:** Beyond ERC-8004 registration, should there be an in-cluster service registry (ConfigMap-based, like the inference metadata pattern) so co-located agents can discover each other without going onchain?

6. **Auto-restart on failure:** Should the skill configure liveness probes to auto-restart crashed handlers? The template includes readiness probes but not liveness.

7. **Rate limiting:** Should there be built-in rate limiting to prevent abuse even with x402 payments? Or is the payment itself sufficient protection?
