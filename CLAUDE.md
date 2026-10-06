# CLAUDE.md

Guidance for Claude Code and Cursor agents working in **obol-stack-front-end**.

## Commands

```bash
pnpm install
pnpm run dev:local          # http://localhost:3000 — no cluster required
pnpm run dev                # http://obol.stack:3000 (needs /etc/hosts + cluster)
pnpm run lint
pnpm run test               # node --test on src/**/*.test.mjs
pnpm run build
pnpm run test:e2e:chromium  # Playwright (mocked APIs; see e2e/README.md)
pnpm run scan:secrets       # gitleaks (same as CI secret-scan.yml)
```

Package manager: **pnpm** (`pnpm-lock.yaml`). Do not commit `package-lock.json`.

### Local dev without a cluster

`pnpm run dev:local` runs the UI standalone. Dashboard and marketplace show “Stack unreachable” without kubeconfig, but lint, unit tests, and Playwright e2e still pass.

### Full cluster-backed dev

When you have **obol-stack** running (`obol stack up`):

1. **k3d cluster running** — `k3d cluster start <clusterName>` (re-start after Docker Desktop restarts).
2. **Kubeconfig** — default `~/.config/obol/kubeconfig.yaml`, or copy to `/tmp/kc-<name>.yaml` if the app expects that path (wiped on Mac restart).
3. **`/etc/hosts`**:

   ```
   127.0.0.1 obol.stack
   127.0.0.1 monitoring.obol.stack
   ```

4. `pnpm run dev` → `http://obol.stack:3000` (also starts eRPC port-forward script).

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

**Cluster access**: Route handlers run server-side with `KUBECONFIG`. Host dev reaches Traefik at `http://obol.stack:8080`; in-cluster code uses `*.svc.cluster.local` (see `src/lib/utils/cluster-url.ts`).

## Product surfaces

- **Dashboard** — `HeroBand`, networks, metrics (`NodeOverview`), agent instances, registration card
- **Marketplace** — `/marketplace` — catalog from `/api/marketplace/services`; buy via CLI (`obol buy inference`)
- **Sell** — ServiceOffer CRUD via `/api/sell/*` (request bodies validated with TypeBox — see `src/lib/sell/create-request.ts`); `SellAgentModal` + marketplace `NewListingDialog`; flow steps mirror `obol sell` CLI
- **Agents** — Hermes default + sub-agents (`obol agent new`); agent listings joined in `agent-listing.ts`

## Security

- Mutating APIs: `rejectCrossOrigin` in `src/lib/middleware/origin-check.ts`
- Never commit `.env.local`, kubeconfigs, or API keys — CI runs **gitleaks** (`.gitleaks.toml`)
- `NEXT_PUBLIC_*` is browser-visible — WalletConnect project ID only
- Scoped reviews: invoke `.agents/skills/security-review-fe/SKILL.md`

## CI

Workflows under `.github/workflows/`:

| Workflow          | PR                                  | `main` push         |
| ----------------- | ----------------------------------- | ------------------- |
| `test.yml`        | lint, unit tests, tsc, `next build` | + Docker smoke test |
| `playwright.yml`  | Chromium E2E (mocked)               | same                |
| `secret-scan.yml` | gitleaks history scan               | same                |

`concurrency: cancel-in-progress` drops superseded runs.

## Docs map

| File                                               | Purpose                                |
| -------------------------------------------------- | -------------------------------------- |
| [CONTRIBUTING.md](CONTRIBUTING.md)                 | PR checklist, branch policy, CI matrix |
| [e2e/README.md](e2e/README.md)                     | Mocked vs integration Playwright tiers |
| [DEVELOPMENT.md](DEVELOPMENT.md)                   | Skaffold / Kind deploy                 |
| [README.md](README.md)                             | User-facing overview                   |
| `.agents/skills/obol-stack-front-end-dev/SKILL.md` | Full dev + cluster QA runbook          |

Implementation plans belong in `plans/`, not `docs/`.

## Related

- **obol-stack** — CLI, x402 verifier, ServiceOffer controller, public storefront
- Set `OBOL_STACK_DIR` / use sibling checkout for cross-repo work

<!-- BEGIN:nextjs-agent-rules -->

# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->
