# OpenAPI + Scalar UI on the tunnel

Status: phase 1 shipped; phase 2 deferred (design below)
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

## Phase 1 — SHIPPED

The heuristic OpenAPI 3.1 spec + Scalar UI are live: `buildOpenAPIDocument`
(`internal/serviceoffercontroller/openapi.go`), component schemas in
`openapi_components.go`, `scalarHTML()` wiring in `controller.go`, and the
`openapi.json` route in `render.go`. The catalog advertises the clamped
`maxTimeoutSeconds` the 402 wire enforces. Original phase-1 design and
checklist removed from this doc — see git history if needed.

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

## Implementation checklist (phase 2, deferred)

- [ ] CRD: add `ServiceOfferAPI` to `internal/monetizeapi/types.go`, regenerate, update `internal/schemas/serviceoffer.go`.
- [ ] Controller: composer that merges per-offer fragment under `EffectivePath()`. Heuristic stays as fallback when `spec.api` is empty.
- [ ] CLI: `--api-type`, `--api-methods`, `--api-inline` on relevant `obol sell` subcommands.
- [ ] Decide JSON-RPC story (see open questions).
- [ ] Migration: existing ServiceOffers continue to work unchanged (heuristic path).
