# E2E Tests

## Two tiers

| Tier                     | What it covers                                             | Needs cluster?        | When it runs                      |
| ------------------------ | ---------------------------------------------------------- | --------------------- | --------------------------------- |
| **Mocked** (current)     | UI rendering — correct display given an API response shape | No                    | Every PR via CI                   |
| **Integration** (future) | Real API responses — catches backend regressions           | Yes (`obol stack up`) | Manually, or a dedicated workflow |

Most tests in this directory are **mocked** — they intercept cluster-dependent API calls
(`/api/dashboard`, `/api/marketplace/services`) with `page.route()` so they pass in CI
without a live cluster. They verify rendering logic, not API correctness.

`api.spec.ts` is the exception — it hits real routes and accepts both `200` and `503`,
so it works either way.

## Running against a real cluster

If you have `obol-stack` running (`obol stack up`) and `127.0.0.1 obol.stack` in `/etc/hosts`:

```bash
BASE_URL=http://obol.stack:3000 pnpm run test:e2e:chromium
```

The mocks still intercept in this mode. To skip mocks and hit real APIs, remove the
`page.route()` calls in the relevant spec files — or add a separate `*.integration.spec.ts`
alongside them.

## Running in CI (no cluster)

```bash
CI=true pnpm run test:e2e:chromium
```

This builds the app first (`pnpm run build`), then starts the standalone server via
`pnpm run start:ci` before running tests.
