# Contributing to the Obol Stack dashboard (`web/frontend`)

Thank you for helping improve the Obol Stack dashboard. Repo-wide conventions (branches, commits, review scope) are in the root `CLAUDE.md` / `CONTRIBUTING.md`; agent skills in `.agents/skills/` have deeper runbooks.

## Prerequisites

- Node.js 24 LTS (`.nvmrc`), pnpm via corepack (`packageManager` pins the version)
- A live cluster is **not required** for UI contributions — `pnpm run dev:local` runs the frontend standalone and all tests pass without one
- With a stack (`obol stack up`), add `127.0.0.1 obol.stack` to `/etc/hosts` and use `pnpm run dev` for the full experience

## Setup

```bash
cd web/frontend
pnpm install
cp .env.example .env.local   # fill WalletConnect / optional overrides
pnpm run dev:local           # or pnpm run dev with cluster port-forwards
```

## Before opening a PR

```bash
pnpm run lint
pnpm run test
pnpm run build
```

Playwright (optional locally): `pnpm run test:e2e:chromium`

## What we review

| Area                   | Expectation                                                                                                     |
| ---------------------- | --------------------------------------------------------------------------------------------------------------- |
| **Security**           | No secrets in git; API routes validate origin where mutating; the dashboard stays local-only (`obol.stack`)      |
| **Cluster access**     | Server code uses in-cluster DNS; host dev uses `obol.stack:8080` fallbacks (see `src/lib/utils/cluster-url.ts`) |
| **Marketplace / sell** | ServiceOffer CRUD via `/api/sell/*`; CLI parity documented in UI copy blocks                                    |
| **Tests**              | Add `*.test.mjs` for pure logic under `src/lib/`                                                                |

## Security reports

Do **not** file vulnerabilities as public issues. Email **security@obol.tech**.

## Agent skills (Cursor / Claude)

- `.agents/skills/obol-stack-front-end-dev/` — local dev, cluster deploy, marketplace QA
- `.agents/skills/security-review-fe/` — scoped security review (reduces noisy findings)
