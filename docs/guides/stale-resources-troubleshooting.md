# Stale Resource Troubleshooting Runbook

This runbook helps operators and agents diagnose stale Kubernetes resources in Obol Stack and recover cleanly when `obol stack up` appears healthy but routes/images are stale.

## Scope

Use this when you see issues such as:

- `obol.stack` serves old UI or wrong backend.
- `503 Service Temporarily Unavailable` from nginx.
- tunnel root (`https://<trycloudflare>/`) returns "no available server".
- pods stuck in `ImagePullBackOff` for Obol-managed images.
- `obol sell demo` times out while waiting for pod readiness.

---

## 1) Quick triage commands

Run these first:

```bash
obol kubectl get pods -A -o wide
obol kubectl get deploy -A
obol kubectl get svc -A
obol kubectl get httproutes.gateway.networking.k8s.io -A
obol kubectl get ingress -A
```

If a pod is not ready:

```bash
obol kubectl describe pod -n <ns> <pod>
obol kubectl get events -n <ns> --sort-by=.lastTimestamp
```

---

## 2) Detect legacy ingress-nginx conflicts

Symptoms:

- `curl -I http://obol.stack` returns nginx 503.
- old `Ingress` objects still exist in `default`.

Check:

```bash
obol kubectl get ingress -A
obol kubectl get deploy,svc -n default | rg "ingress-nginx|obol-frontend-obol-app|erpc"
```

Expected on modern stack:

- `HTTPRoute` resources (Gateway API) are present.
- no legacy nginx ingress objects for `obol.stack`.

If legacy resources exist, remove them:

```bash
obol kubectl delete ingress obol-frontend-obol-app -n default --ignore-not-found
obol kubectl delete ingress erpc -n default --ignore-not-found
obol kubectl delete deployment ingress-nginx-controller -n default --ignore-not-found
obol kubectl delete service ingress-nginx-controller -n default --ignore-not-found
```

Verify:

```bash
curl -I http://obol.stack
curl -I http://obol.stack:8080
```

Both should be `200` from current frontend (Traefik path).

---

## 3) Fix tunnel storefront failures

Symptoms:

- tunnel root returns "no available server".
- `tunnel-storefront` pod in `ImagePullBackOff`.

Check:

```bash
obol kubectl get pods -n traefik
obol kubectl describe pod -n traefik <tunnel-storefront-pod>
```

If error is image tag not found:

```bash
obol kubectl set image deployment/tunnel-storefront -n traefik \
  storefront=ghcr.io/obolnetwork/obol-stack-public-storefront:latest
obol kubectl rollout status deployment/tunnel-storefront -n traefik --timeout=180s
```

Verify:

```bash
curl -I https://<your-trycloudflare-domain>/
```

---

## 4) Fix demo pod scheduling timeouts

Symptoms:

- `obol sell demo` fails waiting for pod readiness.
- pod remains `Pending`.

Check scheduler reason:

```bash
obol kubectl describe pod -n demo -l app=demo-hello
obol kubectl describe node <node-name>
```

Common cause: `Insufficient memory`.

Free memory by removing high-request workloads not needed for this test, then wait for rollout:

```bash
obol kubectl rollout status deployment/demo-hello -n demo --timeout=180s
```

---

## 5) Image freshness model (important)

`obol stack up` is deterministic and does not always "pull latest":

- Some images are pinned by version tag in values files.
- Some are digest-pinned.
- Some are commit-derived via `images.Resolve(...)` (using obol binary `GitCommit`).

If commit-derived image tags are not published for that commit SHA, runtime pull failures occur.

---

## 5b) LiteLLM 401 due to accidental OpenAI placeholders

Observed failure pattern:

- chat fails with `Incorrect API key provided: <placeholder>`
- LiteLLM config contains a placeholder-style model alias and matching placeholder `OPENAI_API_KEY`

Why this happens:

- auto cloud-provider detection can import a stale/default agent model from `~/.openclaw/openclaw.json`
- if shell env has a placeholder OpenAI key, LiteLLM can be patched with invalid credentials

Mitigations:

- set intended provider key (for Anthropic: `ANTHROPIC_API_KEY`) before `obol stack up`
- run `obol model prefer <your-model>` then `obol model sync`
- remove wrong aliases with `obol model remove <name>`

---

## 6) Agent checklist for stale resources

Use this checklist in order:

1. `obol kubectl get ingress -A` -> must be empty (or expected non-obol custom ingresses only).
2. `obol kubectl get httproutes.gateway.networking.k8s.io -A` -> confirm `obol-frontend` and service routes exist.
3. `obol kubectl get pods -n traefik` -> `traefik`, `cloudflared`, and `tunnel-storefront` healthy.
4. `obol kubectl describe pod ...` for any `Pending`/`ImagePullBackOff`.
5. `curl -I http://obol.stack` and `curl -I http://obol.stack:8080` -> expect `200`.
6. `curl -I https://<tunnel-host>/` -> expect `200`.
7. if image pull fails, verify referenced tag exists in registry and compare to `obol version` `Git Commit`.

---

## 7) Code-level hardening recommendations

Current hardening implemented:

- `stack up` runs ongoing reconciliation checks every run for known stale ingress conflicts.
- Default mode is non-destructive (warn only). Auto-clean is opt-in with `OBOL_STACK_AUTO_CLEAN_LEGACY=true`.

Additional recommended hardening:

- Ensure publish workflows produce short-SHA images for all commits that can be deployed by commit-derived refs.
- Prefer immutable pins (digest or explicit version tags) over `:latest` for client reproducibility.
- Add CI checks that verify deploy-time image refs exist in registry before release.
