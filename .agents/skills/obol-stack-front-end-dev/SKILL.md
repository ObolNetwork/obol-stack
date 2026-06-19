---
name: obol-stack-front-end-dev
description: Local development and QA for the Obol Stack Next.js dashboard. Use when working on marketplace, sell/buy UX, agent cards, API routes, Playwright, or deploying the dev image to a k3d cluster.
---

# obol-stack-front-end development

## Quick commands

```bash
pnpm install
pnpm run dev:local              # localhost:3000 — no cluster
pnpm run dev                    # obol.stack:3000 + eRPC port-forward
pnpm run test && pnpm run build
pnpm run test:e2e:chromium      # mocked APIs — see e2e/README.md
```

## Standalone vs cluster

| Mode       | Command                              | Cluster?                            |
| ---------- | ------------------------------------ | ----------------------------------- |
| UI-only    | `pnpm run dev:local`                 | No — shows Stack unreachable states |
| Full stack | `pnpm run dev` after `obol stack up` | Yes — `/etc/hosts` + kubeconfig     |

## Cluster-backed dev

The dashboard reads the cluster via `KUBECONFIG` (from `obol` or `NEXT_PUBLIC_KUBECONFIG`).

| Surface              | API / data source                                                                 |
| -------------------- | --------------------------------------------------------------------------------- |
| Obol Services        | `/api/services.json` via `MARKETPLACE_CATALOG_URL` or in-cluster `obol-skill-md`  |
| Sell / ServiceOffers | `/api/sell/*` → Kubernetes API                                                    |
| Agents               | `/api/agents/*` — instances include `listing` / `pendingOffer` from agent-listing |
| Marketplace          | `/api/marketplace/services` — ERC-8004 + local catalog                            |
| x402 probe           | `POST /api/marketplace/probe` — unpaid 402 check (host allowlist)                 |

From the Mac host when the cluster is up: `http://obol.stack:8080` (Traefik). In-pod code uses `*.svc.cluster.local` — see `src/lib/utils/cluster-url.ts`.

## Deploy dev image to k3d

When the stack has dev registries (`OBOL_DEVELOPMENT=true`):

```bash
# from obol-stack repo
FRONTEND_DIR=../obol-stack-front-end just dev-frontend-rebuild
k3d image import localhost:54103/obol-stack-front-end:dev -c <cluster-name>
obol kubectl rollout restart deployment/obol-frontend-obol-app -n obol-frontend
```

`obol stack up` may revert the deployment pin — re-import after stack upgrades.

## Key directories

```
src/app/api/          # Route handlers (Node runtime)
src/components/       # atoms → molecules → organisms (HeroBand, SellAgentModal, …)
src/lib/marketplace/  # Catalog, filters, buy-command, x402-probe
src/lib/sell/         # CLI command builders + flow steps + upstream-health
src/lib/services/     # Kubernetes / Prometheus / agent-listing / registration
e2e/                  # Playwright — mocked tier documented in e2e/README.md
```

## Tests

Unit: `pnpm run test` (`node --test` on `src/**/*.test.mjs` including `src/lib/sell/` and `src/lib/utils/`).

E2E: `pnpm run test:e2e:chromium` — mocks `/api/dashboard` and `/api/marketplace/services` in CI.

## Conventions

- pnpm only; commit `pnpm-lock.yaml`, not `package-lock.json`
- Mutating API routes: use `rejectCrossOrigin` from `src/lib/middleware/origin-check.ts`
- Secret scan: `pnpm run scan:secrets` (gitleaks) — same as CI
- Plans / design notes → `plans/`; user docs → coordinate with obol-stack-docs
