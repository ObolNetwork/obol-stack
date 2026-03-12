# Deploy x402-Gated Web App — Agent Prompt Template

Paste this into the obol-agent chat, customising the variables at the top.

## Key architecture decisions

- **Namespace: `llm`** — deploy alongside LiteLLM so the Deployment can mount `litellm-secrets` directly (Secrets are namespace-scoped, can't cross namespaces)
- **Image: `python:3.12-slim`** — public image, no build needed. k3d pulls it automatically.
- **App code in ConfigMap** — agent writes Python source to a ConfigMap, mounts it into the container at `/app`. Same pattern as the `.well-known/agent-registration.json` busybox httpd.
- **LiteLLM auth** — the Deployment reads `LITELLM_MASTER_KEY` from Secret `litellm-secrets` in `llm` namespace via `secretKeyRef`. Internal calls to LiteLLM don't go through x402.
- **x402 gating** — handled by the ServiceOffer CR. monetize.py creates the Traefik ForwardAuth Middleware + HTTPRoute at `/services/<name>/*`. Traefik strips the prefix (`ReplacePrefixMatch: /`) before forwarding, so the app just serves at `/`.
- **RBAC** — the agent already has cluster-wide RBAC for Deployments, Services, ConfigMaps, and ServiceOffers. No changes needed.

---

## Prompt

```
Deploy a payment-gated web application into the cluster. Follow these steps exactly.

### Step 1: Create ConfigMap with app code

Create a ConfigMap named `cv-enhancer-app` in namespace `llm`.

It must contain a single key `app.py` — a Python HTTP server (stdlib only: http.server, urllib, json, os, sys). The server must:

**GET /**
Render a dark-themed HTML page with:
- Title: "CV Enhancer" with a green "x402" badge
- Subtitle: "Upload your resume and receive a polished, professional version"
- A <textarea> (name="input") for pasting resume text
- A submit button that disables + shows spinner on click
- After submission: a result section with <pre> showing the enhanced resume and a "Copy" button
- Footer: "Model: qwen3.5:9b · Payment: x402 USDC"
- Style: #0a0a0a background, #e0e0e0 text, system fonts, max-width 720px, rounded borders

**POST /**
- Parse form body for `input` field (URL-encoded)
- Call LiteLLM at: POST {LITELLM_URL}/v1/chat/completions
  - Authorization: Bearer {LITELLM_KEY}
  - Body: model={MODEL_NAME}, messages=[system prompt, user input], temperature=0.7
  - System prompt: "You are an expert career consultant and professional resume writer. The user will paste their CV/resume. Rewrite it to be more professional, impactful, and well-structured. Use strong action verbs, quantify achievements where possible, improve formatting with clear sections (Summary, Experience, Education, Skills), and fix any grammar issues. Return ONLY the improved resume text, no commentary."
- Extract response: try choices[0].message.content first, fall back to reasoning_content
- Re-render the page with the result shown
- On error: show error message in a red box, don't crash

**GET /health**
Return: {"status": "ok"} with Content-Type application/json

All config via env vars: LITELLM_URL, LITELLM_KEY, MODEL_NAME, PORT (default 8080).
Log each request to stderr.

Use the Kubernetes API (via kube.py helpers or direct urllib calls) to create this ConfigMap:

```python
import sys, os
sys.path.insert(0, "/data/.openclaw/skills/obol-stack/scripts")
from kube import load_sa, make_ssl_context, api_post, api_get, api_patch

token, my_ns = load_sa()
ssl_ctx = make_ssl_context()

# Read the app.py source code you just wrote
app_source = '''... your Python code here ...'''

configmap = {
    "apiVersion": "v1",
    "kind": "ConfigMap",
    "metadata": {"name": "cv-enhancer-app", "namespace": "llm"},
    "data": {"app.py": app_source}
}

path = "/api/v1/namespaces/llm/configmaps"
existing = api_get(f"{path}/cv-enhancer-app", token, ssl_ctx, quiet=True)
if existing:
    api_patch(f"{path}/cv-enhancer-app", configmap, token, ssl_ctx, patch_type="merge")
else:
    api_post(path, configmap, token, ssl_ctx)
```

### Step 2: Create Deployment

Create Deployment `cv-enhancer` in namespace `llm`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cv-enhancer
  namespace: llm
  labels:
    app: cv-enhancer
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cv-enhancer
  template:
    metadata:
      labels:
        app: cv-enhancer
    spec:
      containers:
        - name: app
          image: python:3.12-slim
          command: ["python", "/app/app.py"]
          ports:
            - containerPort: 8080
          env:
            - name: LITELLM_URL
              value: "http://litellm.llm.svc.cluster.local:4000"
            - name: LITELLM_KEY
              valueFrom:
                secretKeyRef:
                  name: litellm-secrets
                  key: LITELLM_MASTER_KEY
            - name: MODEL_NAME
              value: "qwen3.5:9b"
            - name: PORT
              value: "8080"
          volumeMounts:
            - name: app-code
              mountPath: /app
              readOnly: true
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "128Mi"
              cpu: "200m"
      volumes:
        - name: app-code
          configMap:
            name: cv-enhancer-app
```

Create via K8s API (same kube.py pattern).

### Step 3: Create Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: cv-enhancer
  namespace: llm
spec:
  type: ClusterIP
  selector:
    app: cv-enhancer
  ports:
    - port: 8080
      targetPort: 8080
```

### Step 4: Create ServiceOffer

```yaml
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: cv-enhancer
  namespace: llm
spec:
  type: http
  upstream:
    service: cv-enhancer
    namespace: llm
    port: 8080
    healthPath: /health
  payment:
    scheme: exact
    network: base-sepolia
    payTo: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    price:
      perRequest: "0.01"
  registration:
    enabled: true
    name: "CV Enhancer"
    description: "AI-powered resume enhancement via x402 micropayments"
    skills:
      - "natural_language_processing/text_generation"
    domains:
      - "technology/artificial_intelligence"
      - "professional_services/career"
```

### Step 5: Reconcile

Run the monetize reconciler to create the x402 middleware and HTTPRoute:

```bash
python3 /data/.openclaw/skills/sell/scripts/monetize.py process cv-enhancer --namespace llm
```

### Step 6: Verify

1. Check the pod is running: `kubectl get pods -n llm -l app=cv-enhancer`
2. Check the ServiceOffer status: `kubectl get serviceoffers.obol.org cv-enhancer -n llm -o yaml`
3. Wait for all conditions to be True (UpstreamHealthy, PaymentGateReady, RoutePublished)
4. The app will be accessible at: /services/cv-enhancer/

### Important constraints

- All K8s resources should be created via the kube.py helpers (api_post, api_get, api_patch), NOT kubectl
- The app.py MUST use only Python stdlib — no pip, no requirements.txt
- Do NOT hardcode the LiteLLM key — use secretKeyRef in the Deployment env
- Properly escape curly braces in Python strings that contain HTML/CSS (double them: {{ }})
- Handle qwen3.5 thinking mode: check for both `content` and `reasoning_content` in the response
- The HTML form action should be "" (empty string) so it POSTs to the same URL regardless of path prefix
```
