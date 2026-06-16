# CLAUDE.md

Guidance for Claude Code and Cursor agents working in **obol-stack-front-end**.

## Commands

```bash
pnpm install
pnpm run dev:local          # http://localhost:3000
pnpm run dev                # http://obol.stack:3000 (needs /etc/hosts + cluster)
pnpm run lint
pnpm run test               # node --test on src/**/*.test.mjs
pnpm run build
pnpm run test:e2e:chromium  # Playwright (optional)
bash scripts/scan-git-secrets.sh
```

Package manager: **pnpm** (`pnpm-lock.yaml`). Do not commit `package-lock.json`.

## Architecture

| Layer                  | Role                                                      |
| ---------------------- | --------------------------------------------------------- |
| `src/app/`             | Next.js App Router pages + `src/app/api/*` route handlers |
| `src/components/`      | UI — atomic design (atoms / molecules / organisms)        |
| `src/lib/services/`    | Kubernetes, Prometheus, sell, agents, catalog clients     |
| `src/lib/marketplace/` | Browse filters, buy-command builders, ERC-8004 source     |
| `src/hooks/`           | Client data hooks (React Query)                           |

**Runtime**: Next.js 16, React 19, TypeScript strict, `@obolnetwork/obol-ui`, Tailwind v4.

**Cluster access**: Route handlers run server-side with `KUBECONFIG`. Host dev reaches Traefik at `http://obol.stack:8080`; in-cluster code uses `*.svc.cluster.local` (see `src/lib/utils/cluster-url.ts`).

## Product surfaces

- **Dashboard** — networks, metrics, agent instances, registration card
- **Marketplace** — `/marketplace` — catalog from `/api/marketplace/services`; buy inference via CLI copy (`obol buy inference`)
- **Sell** — ServiceOffer CRUD via `/api/sell/*`; UI mirrors `obol sell` CLI steps
- **Agents** — Hermes default + sub-agents (`obol agent new`); sell via `type=agent` offers

## Security

- Mutating APIs: `rejectCrossOrigin` in `src/lib/middleware/origin-check.ts`
- Never commit `.env.local`, kubeconfigs, or API keys
- `NEXT_PUBLIC_*` is browser-visible — WalletConnect project ID only
- Scoped reviews: invoke `.agents/skills/security-review-fe/SKILL.md`

## CI

Workflows under `.github/workflows/`. PRs run lint + unit tests + build; Docker image smoke test runs on `main` only. `concurrency: cancel-in-progress` drops superseded runs.

## Docs map

| File                                               | Purpose                                |
| -------------------------------------------------- | -------------------------------------- |
| [CONTRIBUTING.md](CONTRIBUTING.md)                 | PR checklist, branch policy, CI matrix |
| [DEVELOPMENT.md](DEVELOPMENT.md)                   | Skaffold / Kind deploy                 |
| [README.md](README.md)                             | User-facing overview                   |
| `.agents/skills/obol-stack-front-end-dev/SKILL.md` | Full dev + cluster QA runbook          |

Implementation plans belong in `plans/`, not `docs/`.

## Related

- **obol-stack** — CLI, x402 verifier, ServiceOffer controller, public storefront
- Set `OBOL_STACK_DIR` / use sibling checkout for cross-repo work
