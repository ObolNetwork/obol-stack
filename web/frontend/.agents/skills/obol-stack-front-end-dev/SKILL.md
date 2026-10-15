---
name: obol-stack-front-end-dev
description: Local development and QA for the Obol Stack Next.js dashboard. Use when working on marketplace, sell/buy UX, agent cards, API routes, Playwright, or deploying the dev image to a k3d cluster.
---

# obol-stack-front-end development (`web/frontend`)

Run commands from `web/frontend` in the obol-stack repo.

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

`OBOL_DEVELOPMENT=true obol stack up` builds `web/frontend` as `ghcr.io/obolnetwork/obol-stack-front-end:dev-<sha>` and imports it (uncommitted changes: `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=frontend`). Faster loop, from the repo root:

```bash
just dev-frontend          # build → k3d image import → rollout restart
just dev-frontend-rebuild  # same, docker build --no-cache
just dev-frontend-reset    # obol stack up (restore the pinned image)
```

On a dev stack `just dev-frontend` reuses the pinned dev tag, so `obol stack up` keeps your build; on a release stack it repoints the deployment at `:dev` until the next `obol stack up`.

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
- Secret scan: repo-root gitleaks workflow (`.gitleaks.toml`)
- Plans / design notes → repo-root `plans/` (gitignored); user docs → `docs/` or obol-stack-docs
