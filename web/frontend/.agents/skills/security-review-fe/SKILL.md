---
name: security-review-fe
description: Security review for Obol Stack dashboard (web/frontend) PRs. Use when reviewing auth, API routes, marketplace/sell flows, tunnel exposure, or before reporting vulnerabilities. Minimises false positives by scoping to real attack surfaces.
---

# Frontend security review (obol-stack-front-end)

## In scope (review these)

1. **API route handlers** (`src/app/api/**/route.ts`)
   - Mutations use `rejectCrossOrigin` (CSRF)
   - No shell/command injection from user input
   - Kubernetes client uses in-cluster config; no arbitrary kubeconfig paths from untrusted input

2. **Secrets**
   - No `.env`, keys, or tokens committed
   - `NEXT_PUBLIC_*` vars are public by design — never put API secrets there
   - Logs redact RPC URLs / tokens (see `scrub` patterns in marketplace errors)

3. **Tunnel / Traefik exposure**
   - This app is usually **local-only** (`hostnames: ["obol.stack"]` on stack routes)
   - Do not add routes that expose admin surfaces on the Cloudflare tunnel hostname
   - x402-gated `/services/*` is stack-side (Go, outside web/frontend) — flag only if FE adds public proxies

4. **Sell / wallet flows**
   - `pay-to` addresses validated as `0x` + 40 hex
   - Namespace allowlist on `/api/sell/create`
   - `POST /api/marketplace/probe` — URL host allowlist (`obol.stack`, tunnel, cluster DNS); no arbitrary SSRF

5. **x402 probe**
   - Probe route only fetches `/services/*` paths on allowlisted hosts
   - Do not widen allowlist to arbitrary external domains without review

## Out of scope (do not flag as FE bugs)

| Finding                                                | Why skip                                                                         |
| ------------------------------------------------------ | -------------------------------------------------------------------------------- |
| Missing auth on read-only cluster metrics              | Dashboard assumes trusted local operator + kubeconfig                            |
| `dangerouslySetInnerHTML` in docs/skills               | Only if unsanitized user HTML — DOMPurify paths are OK                           |
| Dependency CVEs without exploit path in browser bundle | Track via Dependabot; don't block PR on transitive dev-only CVEs unless critical |
| ERC-8004 / x402 protocol design                        | Belongs in the Go stack code, not web/frontend                                             |

## Checklist (PR comment template)

```
- [ ] No secrets in diff
- [ ] New POST/PATCH/DELETE routes call rejectCrossOrigin
- [ ] User-controlled URLs not passed to server-side fetch without allowlist
- [ ] No new public-hostname HTTPRoutes documented as intentional
- [ ] Wallet / chain config uses existing sell-assets registry
```

## Reporting

Valid issues → security@obol.tech. Invalid scanner noise → close with "out of scope" referencing table above.
