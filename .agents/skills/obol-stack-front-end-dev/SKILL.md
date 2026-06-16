---
name: obol-stack-front-end-dev
description: Local development and QA for the Obol Stack Next.js dashboard. Use when working on marketplace, sell/buy UX, agent cards, API routes, Playwright, or deploying the dev image to a k3d cluster.
---

# obol-stack-front-end development

## Quick commands

```bash
pnpm install
pnpm run dev:local              # localhost:3000
pnpm run dev                    # obol.stack:3000 + eRPC port-forward
pnpm run test && pnpm run build
```

## Cluster-backed dev

The dashboard reads the cluster via `KUBECONFIG` (from `obol` or `NEXT_PUBLIC_KUBECONFIG`).

| Surface              | API / data source                                                                |
| -------------------- | -------------------------------------------------------------------------------- |
| Obol Services        | `/api/services.json` via `MARKETPLACE_CATALOG_URL` or in-cluster `obol-skill-md` |
| Sell / ServiceOffers | `/api/sell/*` → Kubernetes API                                                   |
| Agents               | `/api/agents/*`                                                                  |
| Marketplace          | `/api/marketplace/services`                                                      |

From the Mac host when the cluster is up: `http://obol.stack:8080` (Traefik). In-pod code uses `*.svc.cluster.local`.

## Deploy dev image to k3d

When the stack has dev registries (`OBOL_DEVELOPMENT=true`):

```bash
# from obol-stack repo
FRONTEND_DIR=../obol-stack-front-end just dev-frontend-rebuild
k3d image import localhost:54103/obol-stack-front-end:dev -c <cluster-name>
```

`obol stack up` may revert the deployment pin — re-import after stack upgrades.

## Key directories

```
src/app/api/          # Route handlers (Node runtime)
src/components/       # atoms → molecules → organisms
src/lib/marketplace/  # Catalog, filters, buy-command builders
src/lib/sell/         # CLI command builders + flow steps
src/lib/services/     # Kubernetes / Prometheus clients
```

## Tests

Unit: `pnpm run test` (`node --test` on `src/**/*.test.mjs`).

E2E: `pnpm run test:e2e:chromium` (needs built app + Playwright browsers).

## Conventions

- pnpm only; commit `pnpm-lock.yaml`, not `package-lock.json`
- Mutating API routes: use `rejectCrossOrigin` from `src/lib/middleware/origin-check.ts`
- Plans / design notes → `plans/`; user docs → coordinate with obol-stack-docs
