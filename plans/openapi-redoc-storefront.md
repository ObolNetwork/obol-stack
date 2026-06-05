# OpenAPI + Scalar UI on the tunnel

Status: planned (phase 1 ready to implement; phase 2 deferred)
Owner: TBD
Date: 2026-06-03

## Goal

Make sold services legible to humans and to API tooling by publishing:

- `/openapi.json` — aggregate OpenAPI 3.1 document describing every operationally-ready ServiceOffer
- `/api` — Scalar API reference UI, loaded from CDN, lightly themed in Obol greens, with full OG metadata so links unfurl nicely

Both routes are reachable on the local cluster (`obol.stack:8080`) and through the public Cloudflare tunnel, matching the existing `/skill.md` and `/api/services.json` posture.

Underspec'd output in pass 1 is acceptable. We tighten the schema in phase 2 once we see what's missing from the rendered page.

## Non-goals

- Per-offer `/services/<name>/openapi.json` (phase 3, skipped until someone asks)
- Probing upstreams to auto-discover paths (`http` upstreams can have arbitrary path shapes — not scalable, not in scope)
- Operator-authored OpenAPI fragments (phase 2)
- Replacing `/skill.md` or `/api/services.json` (kept; OpenAPI is additive)

## Inputs (current state)

- **ServiceOfferSpec** carries no shape metadata today — just `Type` (inference|fine-tuning|http|agent), `Model{name,runtime}`, `Upstream{...}`, `Payment{...}`, `Path`, ERC-8004 `Registration{...}`. (`internal/monetizeapi/types.go:100-149`)
- **Catalog architecture** is the right pattern to extend: `obol-skill-md` ConfigMap + busybox httpd Deployment + Service + HTTPRoutes for `/skill.md` and `/api/services.json`, all rebuilt by `reconcileSkillCatalog()` on every ServiceOffer change. (`internal/serviceoffercontroller/render.go:248-454`, `controller.go:1108-1169`)
- **Tunnel URL** flows via the `obol-frontend/obol-stack-config` ConfigMap's `tunnelURL` field. A controller informer watches it and re-enqueues every offer + registration on change (`controller.go:199-203`, `:306-324`, `:1323-1337`). The aggregate spec's `servers[]` regenerates automatically.
- **x402 402 body** is `x402types.PaymentRequired` with `X402Version: 2`, fields `error`, `resource{url,description}`, `accepts[]PaymentRequirements`, `extensions{}`. (`internal/x402/paymentrequired.go:119-130`)
- **Brand assets**: primary green `#2FE4AB`, background `#091011`. OG image already exists as a Next.js dynamic route at `/og-payment-required.png` in `web/public-storefront`.

## Phase 1 — ship the minimum (heuristic spec + Scalar UI)

Self-contained diff in `internal/serviceoffercontroller/render.go` + tests. No CRD changes, no CLI changes.

### 1. Aggregate `openapi.json` generation

New function `buildOpenAPIDocument(offers []*ServiceOffer, baseURL string) ([]byte, error)` next to `buildSkillCatalogMarkdown` / `buildServiceCatalogJSON`. Same `offerOperationallyReady` filter as the other two surfaces so all three stay consistent.

Document skeleton:

```json
{
  "openapi": "3.1.0",
  "info": {
    "title": "Obol Stack — paid services",
    "version": "1",
    "description": "x402 payment-gated services advertised by this operator.",
    "x-obol-stack-version": "<commit/version>"
  },
  "servers": [
    { "url": "<tunnelURL>", "description": "Public tunnel" },
    { "url": "http://obol.stack:8080", "description": "Local cluster" }
  ],
  "tags": [ /* derived from union of all offers' registration.skills + .domains */ ],
  "paths": { /* per-offer, see below */ },
  "components": {
    "schemas": {
      "X402PaymentRequired":  { /* mirrors x402types.PaymentRequired, X402Version=2 */ },
      "X402PaymentRequirements": { /* mirrors PaymentRequirements */ },
      "OpenAIChatCompletionsRequest":  { /* OpenAI canonical */ },
      "OpenAIChatCompletionsResponse": { /* OpenAI canonical */ }
    },
    "responses": {
      "PaymentRequired": {
        "description": "x402 payment required. Body matches X402PaymentRequired (X402Version 2). Include X-PAYMENT header on the retry.",
        "headers": { "X-PAYMENT-REQUIRED": { "schema": { "type": "string" } } },
        "content": { "application/json": { "schema": { "$ref": "#/components/schemas/X402PaymentRequired" } } }
      }
    },
    "securitySchemes": {
      "x402Payment": { "type": "apiKey", "in": "header", "name": "X-PAYMENT",
        "description": "Base64-encoded x402 payment payload. See https://www.x402.org" }
    }
  },
  "security": [ { "x402Payment": [] } ]
}
```

#### Per-offer path emission heuristic

The `Type` field selects the request/response shape. Heuristics only — phase 2 lets operators override.

| Offer `type` | Verb | Path under `EffectivePath()` | Request shape | Response shape |
|---|---|---|---|---|
| `inference` | POST | `/v1/chat/completions` | `$ref OpenAIChatCompletionsRequest` | `$ref OpenAIChatCompletionsResponse` |
| `agent` | POST | `/v1/chat/completions` | `$ref OpenAIChatCompletionsRequest` | `$ref OpenAIChatCompletionsResponse` |
| `fine-tuning` | POST | `<path>` | multipart/form-data, generic | generic 200 (text) |
| `http` | POST | `<path>` | `application/json` body, generic schema | generic 200 (text) |

Every operation gets:

- `summary` = offer name
- `description` = `offer.spec.registration.description` (or a fallback by type)
- `tags` = `offer.spec.registration.skills ∪ offer.spec.registration.domains`
- `responses["402"]` = `$ref #/components/responses/PaymentRequired`
- `responses["200"]` = appropriate by type
- `x-x402-payment` extension (we own this convention):
  ```yaml
  x-x402-payment:
    scheme: <payment.scheme>            # "exact"
    network: <payment.network>          # CAIP-2 form preferred, fallback to legacy alias
    payTo:   <payment.payTo>
    asset:   <USDC|OBOL contract address>
    price:
      # whichever of these is set on the offer:
      perRequest: "0.001"
      perMTok:    "1.50"
      perHour:    "0.10"
  ```
- `security: [{ x402Payment: [] }]`

When no offers are ready, emit a valid empty doc: same skeleton, `paths: {}`, plus an `info.description` note explaining the operator has no live services. Scalar renders this gracefully.

### 2. Scalar UI shell (`api/index.html`)

~40 lines of HTML. CDN-loaded Scalar bundle with SRI pin (pin the exact version, do not chase `latest`):

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Obol Stack — API reference</title>

  <!-- OpenGraph -->
  <meta property="og:title"       content="Obol Stack — API reference" />
  <meta property="og:description" content="x402 payment-gated services advertised by this operator." />
  <meta property="og:image"       content="/og-payment-required.png" />
  <meta property="og:type"        content="website" />
  <meta name="twitter:card"       content="summary_large_image" />
  <meta name="theme-color"        content="#091011" />

  <style>
    :root {
      --scalar-color-1: #e5f9f1;
      --scalar-color-2: #b5ecd3;
      --scalar-color-accent: #2FE4AB;
      --scalar-background-1: #091011;
      --scalar-background-2: #0d1618;
      --scalar-background-accent: #2FE4AB;
      --scalar-border-color: #1a2426;
    }
    body { margin: 0; background: #091011; }
  </style>
</head>
<body>
  <script id="api-reference" data-url="/openapi.json"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@<pinned-version>"
          integrity="sha384-<pinned-sri>" crossorigin="anonymous"></script>
</body>
</html>
```

Notes:

- **Scalar over Redoc** because Scalar 1) renders `x-` extensions inline (so `x-x402-payment` shows up next to each operation without custom plugins), 2) handles OpenAPI 3.1 + JSON Schema 2020-12 natively, 3) themes via CSS custom properties — no fork needed.
- **Theme** keeps it light: accent + background only. Don't try to redesign Scalar's typography.
- **OG image** reuses the storefront's existing `/og-payment-required.png` rather than minting a new one. That file is already served from the storefront pod under the same hostname, so `<meta property="og:image" content="/og-payment-required.png">` resolves correctly on both local and tunnel.

### 3. Wire-up in `render.go`

Extend the existing catalog primitives:

- `buildSkillCatalogConfigMap(skillMD, servicesJSON, openAPIJSON, redocHTML string)` — add `openapi.json` and `api/index.html` keys to `data{}` and add the matching MIME entries to `httpd.conf`.
- `buildSkillCatalogDeployment` — extend the `content` volume `items` to project the two new keys to `/www/openapi.json` and `/www/api/index.html`.
- Add `buildOpenAPIHTTPRoute()` (Exact `/openapi.json`) and `buildAPIDocsHTTPRoute()` (Exact `/api` AND Exact `/api/`, both rules pointing at the catalog Service). `/api/services.json` (Exact, already present) wins over both because Gateway API resolves Exact-vs-Exact by literal match, not prefix.
- `reconcileSkillCatalog()` calls the new generator alongside `buildSkillCatalogMarkdown` / `buildServiceCatalogJSON` and applies the two extra HTTPRoutes.

Total surface: ~250 lines of new Go + the HTML shell + golden-file tests.

### 4. Tests

- `render_test.go`: golden test asserting the JSON shape for the three offer types (inference, agent, http). Validate output with `github.com/pb33f/libopenapi` (OpenAPI 3.1 capable; kin-openapi is 3.0 only — not suitable). Assert `x-x402-payment` is present on every operation and the `$ref` to `PaymentRequired` resolves.
- Empty-cluster test: zero ready offers → valid spec with `paths: {}`.
- Tunnel-URL change test: changing the source ConfigMap's `tunnelURL` causes the rebuilt spec's `servers[0].url` to update (this is implicitly covered by the existing catalog informer test pattern; add an assertion to it).
- Path-precedence test: confirm `/api/services.json` is still reachable when both `/api` HTTPRoutes are applied. Can be a unit test of the Gateway API spec we emit, not a live cluster test.

### 5. Security posture

- HTTPRoutes have NO `hostnames:` restriction (matches `/skill.md` and `/api/services.json` — meant to be tunnel-reachable). Add a one-line comment in `buildOpenAPIHTTPRoute` and `buildAPIDocsHTTPRoute` documenting this so future "tighten all routes" cleanups don't accidentally lock the tunnel out.
- CDN script tag must use SRI integrity hash; pin to an exact Scalar version. Renovate will bump it.
- Spec contains payment addresses + chain selectors — already public information advertised on `/skill.md` and ERC-8004. No new exposure.

## Phase 2 — make ServiceOffer actually express shape

Deferred. Lands after phase 1 is in production and we've looked at the rendered page enough to know where the underspec hurts.

### CRD additions

Add optional `api` block to `ServiceOfferSpec`:

```go
type ServiceOfferSpec struct {
    // ... existing fields ...
    API *ServiceOfferAPI `json:"api,omitempty"`
}

type ServiceOfferAPI struct {
    // Shape preset for inference / agent. Default: openai.
    // +kubebuilder:validation:Enum=openai
    Type string `json:"type,omitempty"`

    // For type=http. Default ["POST"].
    Methods []string `json:"methods,omitempty"`

    // Inline OpenAPI fragment merged under spec.path. YAML is the
    // expected authoring format inside the CR (Kubernetes-native);
    // the controller marshals it into the aggregate JSON spec.
    // When empty, phase 1 heuristics apply.
    Inline string `json:"inline,omitempty"`
}
```

Defaults when unset:

- `inference` / `agent` → `type: openai`, `methods: [POST]`, path `/v1/chat/completions` under `spec.path`
- `http` → `methods: [POST]`, content-type `application/json`, no further structure

### CLI surface

For `obol sell inference` and `obol sell agent`-equivalent commands:

```
--api-type openai          # only choice today; placeholder for future shapes
```

For `obol sell http`:

```
--api-methods GET,POST     # comma-separated HTTP verbs the upstream supports
--api-inline @spec.yaml    # operator BYO OpenAPI fragment (YAML or JSON)
```

Deliberately no `--api-content-type` flag — for `http`, JSON is the only supported request body in phase 2.

### Open questions for phase 2 (do not pre-commit)

- **`http` with many paths / JSON-RPC upstreams.** A single ServiceOffer is "one path prefix → one upstream" today. Two plausible directions:
  1. Allow `--api-inline @spec.yaml` to list multiple sub-paths under the prefix; the controller splices each as `<prefix>/<sub-path>` into the aggregate.
  2. Special-case JSON-RPC with an `--api-jsonrpc-methods eth_call,eth_getBalance,...` flag that auto-generates a single `POST <prefix>` with a oneOf-of-methods request body. Cleaner for the JSON-RPC case but doesn't generalize.
  Probe-the-upstream auto-discovery is out — not scalable, not reliable.
- **Should phase 2 add a `summary` / `description` field to `ServiceOfferAPI` distinct from `registration.description`?** Probably yes — `registration.description` is the ERC-8004 elevator pitch, OpenAPI wants per-operation detail.
- **CRD versioning.** This is the first breaking-ish addition to ServiceOffer since GA. Confirm whether we bump to `v1beta2` or keep adding optional fields under `v1beta1`. Default position per `docs/observability.md` philosophy: add as optional and stay on `v1beta1`.

## Phase 3 — skipped

Per-offer `/services/<name>/openapi.json` routes. Defer until a buyer asks. The aggregate is enough for discovery and for pointing a generator at one operator.

## Resolved design questions (from initial review)

1. **`agent` shape.** Same as `inference` — OpenAI chat completions. Heuristic table updated.
2. **`x-x402-payment` extension.** We're the only ones putting x402 pricing into OpenAPI today. Mirror `x402types.PaymentRequired` (X402Version 2) verbatim in `components.schemas` — that's the canonical x402.org wire format. The custom extension is just a per-operation snapshot of what would land in `accepts[0]` so Scalar can render it without resolving the response body.
3. **Renderer.** Scalar, pinned version + SRI.
4. **Tunnel URI change.** Already covered: controller informer watches `obol-frontend/obol-stack-config.tunnelURL`, every change re-enqueues all offers, `reconcileSkillCatalog` regenerates `servers[]` automatically. Add an assertion to the existing catalog rebuild test.
5. **Theming.** `#2FE4AB` accent, `#091011` background, via Scalar's CSS custom properties. No font changes, no layout changes.
6. **OG metadata.** Reuses storefront's `/og-payment-required.png`. Add explicit `og:title`, `og:description`, `og:image`, `og:type`, `twitter:card`, `theme-color`.

## Risks

- **CDN reachability.** Scalar from jsdelivr is a third-party dependency on the public storefront path. If jsdelivr is down or blocked in some geography, `/api` shows a blank page. Acceptable for v1; phase 2 can vendor the bundle into the catalog ConfigMap if we ever care. Note added to the HTML shell comment.
- **OpenAPI 3.1 + libopenapi maturity.** `pb33f/libopenapi` is the right choice but newer than kin-openapi. Pin to a known-good version, treat upgrades like any other dep bump.
- **Scalar version drift.** SRI pin makes silent upgrades impossible; Renovate bumps create a visible PR per upgrade.
- **`/api` Exact vs `/api/services.json` Exact.** Documented above — Gateway API exact-vs-exact resolves by literal match, so both routes coexist. Add a path-precedence test so a future Gateway API version doesn't silently break this.
- **Phase 1 underspec'd POSTs.** Buyers using a code generator against the phase 1 spec for an `http` offer will get a generic `any`-body POST. That's the explicit trade — we ship discoverability now, tighten later. Document this in the spec's `info.description`.

## Implementation checklist (phase 1)

- [ ] `internal/serviceoffercontroller/render.go`: `buildOpenAPIDocument`, `buildOpenAPIHTTPRoute`, `buildAPIDocsHTTPRoute`. Extend `buildSkillCatalogConfigMap` and `buildSkillCatalogDeployment`.
- [ ] `internal/serviceoffercontroller/openapi_components.go` (new): JSON-Schema constants for `X402PaymentRequired`, `X402PaymentRequirements`, `OpenAIChatCompletionsRequest`, `OpenAIChatCompletionsResponse`. Mirror coinbase/x402 Go types; keep in sync via a generation note.
- [ ] `internal/serviceoffercontroller/render_test.go`: goldens for inference / agent / http / empty.
- [ ] `internal/serviceoffercontroller/controller.go`: call the new generator from `reconcileSkillCatalog`, apply the two new HTTPRoutes.
- [ ] Scalar HTML shell embedded as a constant in `render.go` (no new file). Pinned Scalar version + SRI hash in the same file so Renovate sees both.
- [ ] Smoke test in `flows/`: hit `/openapi.json` and `/api` after `obol stack up` + a `obol sell http` deployment, assert 200 + content-type.
- [ ] Update `docs/guides/monetize-inference.md` end-of-flow to mention "your service now appears at /api on the tunnel".

## Implementation checklist (phase 2, deferred)

- [ ] CRD: add `ServiceOfferAPI` to `internal/monetizeapi/types.go`, regenerate, update `internal/schemas/serviceoffer.go`.
- [ ] Controller: composer that merges per-offer fragment under `EffectivePath()`. Heuristic stays as fallback when `spec.api` is empty.
- [ ] CLI: `--api-type`, `--api-methods`, `--api-inline` on relevant `obol sell` subcommands.
- [ ] Decide JSON-RPC story (see open questions).
- [ ] Migration: existing ServiceOffers continue to work unchanged (heuristic path).
