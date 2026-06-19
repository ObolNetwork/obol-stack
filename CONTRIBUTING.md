# Contributing to obol-stack-front-end

Thank you for helping improve the Obol Stack dashboard. This doc is the short path; agent skills in `.agents/skills/` have deeper runbooks.

## Prerequisites

- Node.js 22+, pnpm 10.30+
- A live cluster is **not required** for UI contributions — `pnpm run dev:local` runs the frontend standalone and all tests pass without one
- If you have access to **obol-stack**, run `obol stack up` to get a local cluster, then add `127.0.0.1 obol.stack` to `/etc/hosts` and use `pnpm run dev` for the full experience

## Setup

```bash
pnpm install
cp .env.example .env.local   # fill WalletConnect / optional overrides
pnpm run dev:local           # or pnpm run dev with cluster port-forwards
```

## Before opening a PR

```bash
pnpm run lint
pnpm run test
pnpm run build
pnpm run scan:secrets   # optional; requires gitleaks — CI runs this too
```

Playwright (optional locally): `pnpm run test:e2e:chromium`

## Branch naming

Use prefixes: `feat/`, `fix/`, `chore/`, `docs/`, `research/`.

## Commits

Conventional commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `security:` with optional scope.

## What we review

| Area                   | Expectation                                                                                                     |
| ---------------------- | --------------------------------------------------------------------------------------------------------------- |
| **Security**           | No secrets in git; API routes validate origin where mutating; tunnel-exposed routes stay payment-gated          |
| **Cluster access**     | Server code uses in-cluster DNS; host dev uses `obol.stack:8080` fallbacks (see `src/lib/utils/cluster-url.ts`) |
| **Marketplace / sell** | ServiceOffer CRUD via `/api/sell/*`; CLI parity documented in UI copy blocks                                    |
| **Tests**              | Add `*.test.mjs` for pure logic under `src/lib/`                                                                |

## CI (what runs when)

| Workflow                | PR                                  | `main` push              |
| ----------------------- | ----------------------------------- | ------------------------ |
| `test.yml`              | lint, unit tests, tsc, `next build` | + Docker smoke test      |
| `playwright.yml`        | Chromium E2E (mocked APIs)          | same                     |
| `secret-scan.yml`       | gitleaks history scan               | same                     |
| `build-push-deploy.yml` | —                                   | multi-arch image publish |

Superseded runs are cancelled via `concurrency: cancel-in-progress` — only the latest commit per PR/branch is built.

## Security reports

Do **not** file vulnerabilities as public issues. Email **security@obol.tech** — see [.github/SECURITY.md](.github/SECURITY.md).

## Agent skills (Cursor / Claude)

- `.agents/skills/obol-stack-front-end-dev/` — local dev, cluster deploy, marketplace QA
- `.agents/skills/security-review-fe/` — scoped security review (reduces noisy findings)

## Related repos

- **obol-stack** — CLI, x402, ServiceOffer controller (private repo — internal use only)
- [obol-stack-docs](https://github.com/ObolNetwork/obol-stack-docs) — user documentation
