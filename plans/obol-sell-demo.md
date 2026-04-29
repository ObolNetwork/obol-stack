# `obol sell demo` — Implementation Plan

**Status**: Implemented (pending review)

## Summary

An `obol sell demo <type>` command that deploys a demo HTTP backend into the
cluster, creates a ServiceOffer to payment-gate it, and prints copy-paste "try
it" instructions showing how to make an x402 payment.

Three demo types for v1, ranked by complexity:

| Demo     | Price (USDC/req) | What it does |
|----------|-------------------|--------------|
| `hello`  | 0.00001           | Echoes x402 payment headers back as proof-of-payment |
| `blocks` | 0.0001            | Queries local eRPC for latest block, gas price, chain info |
| `oracle` | 0.001             | Chain analysis: gas statistics, tx volume, utilization (pure Go + RPC, no LLM) |

A public-facing tunnel storefront (Next.js + Tailwind) replaces the busybox
landing page, showing active services and try-it code snippets.

## Decisions

- **Oracle demo**: Pure Go + eRPC (no LiteLLM dependency). Fetches last 5 blocks,
  computes gas stats (min/max/avg in gwei), tx volume, gas utilization percentage.
- **Namespace**: Shared `demo` namespace for all demo services.
- **Frontend**: Next.js with Tailwind CSS, Obol dark theme colors from obol-stack-front-end.
  Uses standalone output for Docker. Fetches `/api/services.json` for service data.
- **Payment chain**: Defaults to `base` (production).
- **Pricing**: Tiered by complexity — 5 zeros, 4, 3.
- **Image builds**: demo-server added to docker-publish-x402.yml matrix. Public
  storefront gets its own workflow (docker-publish-storefront.yml). Both added to
  localImages for OBOL_DEVELOPMENT builds.

## Components Built

### 1. Demo Server (`cmd/demo-server/` + `internal/demo/`)
- Go HTTP server, DEMO_TYPE env selects handler
- `internal/demo/demo.go` — shared types, response envelope, payment header extraction
- `internal/demo/hello.go` — proof-of-payment echo
- `internal/demo/blocks.go` — eRPC chain data (concurrent RPC calls)
- `internal/demo/oracle.go` — chain analysis with gas stats
- `Dockerfile.demo-server` — distroless multi-stage build
- Image: `ghcr.io/obolnetwork/demo-server:latest`

### 2. CLI Command (`cmd/obol/sell.go`)
- `obol sell demo <hello|blocks|oracle>` with --wallet, --chain, --price, --name flags
- Deploys K8s Namespace + Deployment + Service via kubectl apply
- Creates ServiceOffer CR with registration enabled
- Ensures tunnel is active
- Prints try-it instructions (curl, Python x402 SDK, agent prompt, x402 protocol explanation)
- Demo cleanup on `obol sell delete` when namespace=demo

### 3. Services JSON API (`internal/serviceoffercontroller/`)
- `buildServiceCatalogJSON()` generates structured JSON from ready ServiceOffers
- Added to `obol-skill-md` ConfigMap alongside skill.md
- HTTPRoute at `/api/services.json` for public access
- Includes isDemo flag (namespace=demo), endpoint, price, type, description

### 4. Public Storefront (`web/public-storefront/`)
- Next.js 16 + Tailwind CSS 4 + TypeScript
- Obol dark theme (#091011 bg, #2FE4AB green, #162A40 blue)
- ServiceCard component with expandable code snippets
- PaymentFlow component explaining x402 protocol
- Fetches from `/api/services.json` via server-side rendering
- `Dockerfile.public-storefront` — node build + standalone runner
- Image: `ghcr.io/obolnetwork/obol-stack-public-storefront:latest`
- Replaces busybox storefront in tunnel.go CreateStorefront()

### 5. CI/CD
- demo-server added to `.github/workflows/docker-publish-x402.yml` (build + security scan)
- `.github/workflows/docker-publish-storefront.yml` — new workflow for storefront
- Both images added to `localImages` in stack.go for OBOL_DEVELOPMENT builds

### 6. Tests
- `internal/demo/demo_test.go` — 5 tests (hello handler, blocks with mock/no RPC, oracle with mock, envelope)
- `internal/serviceoffercontroller/render_test.go` — 3 new tests (JSON generation, empty, HTTPRoute)
- All tests pass, no regressions in existing test suite

## File Index

```
New files:
  cmd/demo-server/main.go
  internal/demo/demo.go
  internal/demo/hello.go
  internal/demo/blocks.go
  internal/demo/oracle.go
  internal/demo/demo_test.go
  Dockerfile.demo-server
  Dockerfile.public-storefront
  web/public-storefront/package.json
  web/public-storefront/tsconfig.json
  web/public-storefront/next.config.ts
  web/public-storefront/postcss.config.mjs
  web/public-storefront/src/app/globals.css
  web/public-storefront/src/app/layout.tsx
  web/public-storefront/src/app/page.tsx
  web/public-storefront/src/types.ts
  web/public-storefront/src/components/ServiceCard.tsx
  web/public-storefront/src/components/PaymentFlow.tsx
  .github/workflows/docker-publish-storefront.yml

Modified files:
  cmd/obol/sell.go                          — added sellDemoCommand + helpers
  internal/stack/stack.go                   — added demo-server + storefront to localImages
  internal/serviceoffercontroller/render.go — added services.json builder + HTTPRoute
  internal/serviceoffercontroller/render_test.go — added JSON/route tests
  internal/serviceoffercontroller/controller.go — generate+apply services.json
  internal/tunnel/tunnel.go                 — storefront now uses Next.js image
  .github/workflows/docker-publish-x402.yml — added demo-server to matrix
  plans/obol-sell-demo.md                   — this file
```
