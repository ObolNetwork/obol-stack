# CLAUDE.md

Guidance for Claude Code and Cursor agents working on the **Obol Stack operator dashboard** (`web/frontend` in the obol-stack monorepo, served at `http://obol.stack:8080`). It used to live in `ObolNetwork/obol-stack-front-end`; that repo's history was imported here. The repo-root `CLAUDE.md` covers the CLI, cluster and security invariants — read it too.

## Commands

Run from `web/frontend` (or `pnpm --dir web/frontend run <script>` from the repo root — CI calls `lint`, `test` and `build` that way):

```bash
corepack enable             # pnpm version pinned by packageManager
pnpm install --frozen-lockfile
pnpm run dev:local          # http://localhost:3000 — no cluster required
pnpm run dev                # http://obol.stack:3000 (needs /etc/hosts + cluster)
pnpm run lint
pnpm run test               # node --test on src/**/*.test.mjs
pnpm run build
pnpm run test:e2e:chromium  # Playwright (mocked APIs; see e2e/README.md)
```

Package manager: **pnpm** (`pnpm-lock.yaml`). Do not commit `package-lock.json`.

### Local dev without a cluster

`pnpm run dev:local` runs the UI standalone. Dashboard and marketplace show “Stack unreachable” without kubeconfig, but lint, unit tests, and Playwright e2e still pass.

### Host dev against a running stack

1. `obol stack up` (k3d cluster running; after a Docker Desktop restart, `obol stack up` again).
2. **Kubeconfig** — `KUBECONFIG=~/.config/obol/kubeconfig.yaml` (or `OBOL_CONFIG_DIR`) in `.env.local`; see `.env.example`.
3. **`/etc/hosts`**: `127.0.0.1 obol.stack`
4. `pnpm run dev` → `http://obol.stack:3000` (also starts the eRPC port-forward script).

### In-cluster dev image

The image is built from this directory (`Dockerfile`, build context `web/frontend`) and is a stack-owned image like `x402-verifier`:

- `OBOL_DEVELOPMENT=true obol stack up` builds `ghcr.io/obolnetwork/obol-stack-front-end:dev-<sha>` from `web/frontend` and imports it into k3d. Uncommitted changes reuse the committed tag — force a rebuild with `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=frontend`.
- Faster inner loop from the repo root: `just dev-frontend` (build → `k3d image import` → rollout restart; `dev-frontend-rebuild` = `--no-cache`, `dev-frontend-reset` = `obol stack up`).
- WalletConnect project ID (public, baked into the client bundle at build time): `NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID` / `WALLET_CONNECT_PROJECT_ID` env or `web/frontend/.env.local`; `scripts/build-frontend-image.sh` does the resolution.
- Releases: `.github/workflows/docker-publish-frontend.yml` publishes `ghcr.io/obolnetwork/obol-stack-front-end:<short-sha>` for every `main` commit; the CLI pins it via `internal/images` (placeholder in `internal/embed/infrastructure/values/obol-frontend.yaml.gotmpl`, see `docs/release-images.md`). Never hard-code a tag or digest there.
- `.dockerignore` and the front-end inputs in `.github/scripts/image-plan.sh` must agree: anything that can change the image must be an image-plan input.

## Architecture

| Layer                  | Role                                                      |
| ---------------------- | --------------------------------------------------------- |
| `src/app/`             | Next.js App Router pages + `src/app/api/*` route handlers |
| `src/components/`      | UI — atomic design (atoms / molecules / organisms)        |
| `src/lib/services/`    | Kubernetes, Prometheus, sell, agents, catalog clients     |
| `src/lib/marketplace/` | Browse filters, buy-command, settlement, ERC-8004 source  |
| `src/lib/sell/`        | CLI command builders, flow steps, upstream health         |
| `src/hooks/`           | Client data hooks (React Query)                           |

**Runtime**: Next.js 16.3, React 19, TypeScript strict, Node 24 LTS (`.nvmrc`). Styling is Stitches via `@obolnetwork/obol-ui` (no Tailwind) — use obol-ui components and theme tokens.

**Cluster access**: Route handlers run server-side with `KUBECONFIG`. Host dev reaches Traefik at `http://obol.stack:8080`; in-cluster code uses `*.svc.cluster.local` (see `src/lib/utils/cluster-url.ts`). Deployment values (env, service account, volumes): `internal/embed/infrastructure/values/obol-frontend.yaml.gotmpl`; HTTPRoute + RBAC: `internal/embed/infrastructure/base/templates/obol-frontend.yaml`.

## Product surfaces

- **Dashboard** — `HeroBand`, networks, metrics (`NodeOverview`), agent instances, registration card
- **Marketplace** — `/marketplace` — catalog from `/api/marketplace/services`; buy via CLI (`obol buy inference`)
- **Sell** — ServiceOffer CRUD via `/api/sell/*` (request bodies validated with TypeBox — see `src/lib/sell/create-request.ts`); `SellAgentModal` + marketplace `NewListingDialog`; flow steps mirror `obol sell` CLI
- **Agents** — Hermes default + sub-agents (`obol agent new`); agent listings joined in `agent-listing.ts`. Hermes is the only agent runtime (OpenClaw was removed in v0.16).

## Security

- The dashboard is **local-only**: its HTTPRoute is restricted to `hostnames: ["obol.stack"]` (guarded by `internal/embed/embed_httproute_hostnames_test.go`). Never expose it, or any `/api/*` route, through the Cloudflare tunnel.
- Mutating APIs: `rejectCrossOrigin` in `src/lib/middleware/origin-check.ts`
- Never commit `.env.local`, kubeconfigs, or API keys — the repo-root gitleaks workflow scans every PR
- `NEXT_PUBLIC_*` is browser-visible — WalletConnect project ID only
- Scoped reviews: invoke `.agents/skills/security-review-fe/SKILL.md`

## CI

Repo-root workflows: the PR lint/test workflow runs `pnpm --dir web/frontend run lint|test|build`; `docker-publish-frontend.yml` publishes the image; `gitleaks.yml` scans for secrets.

## Docs map

| File                                               | Purpose                                |
| -------------------------------------------------- | -------------------------------------- |
| [CONTRIBUTING.md](CONTRIBUTING.md)                 | PR checklist                           |
| [e2e/README.md](e2e/README.md)                     | Mocked vs integration Playwright tiers |
| [DESIGN.md](DESIGN.md)                             | Visual / component conventions         |
| [README.md](README.md)                             | Overview                               |
| `.agents/skills/obol-stack-front-end-dev/SKILL.md` | Full dev + cluster QA runbook          |

Implementation plans belong in the repo-root `plans/` (gitignored), not `docs/`.

<!-- BEGIN:nextjs-agent-rules -->

# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->
