# 402 + storefront follow-ups for `oisin/openapi`

Tracking what we deliberately punted on while shipping the type-aware 402 page.
`oisin/openapi` HAS landed (Scalar UI + /openapi.json are live) — the items
below are now actionable: walk through each and decide which to fold into the
spec-driven render path vs. leave on the current in-binary template.

## Context (where things stand today)

- `ServiceOffer.spec.registration.description` is the single per-offer
  description field. It already plumbs through to `/api/services.json`,
  `/skill.md`, and (as of this branch) the 402 HTML page + OG/meta.
- The 402 HTML template (`internal/x402/templates/payment_required.html`)
  uses `buildTypeCopy` in `internal/x402/paymentrequired.go` to pick a
  per-type lede + primary card + secondary prompts. The branches are:
  - **inference**: primary card is a copy-able `obol buy inference …`
    shell snippet.
  - **agent**: chat-completions example body lives in the "Pay manually"
    card; primary card is suppressed.
  - **http** (default): single Obol-Agent prompt CTA.
- The storefront (`web/public-storefront`) already binds
  `service.description`, but its long-form copy is mostly static.
- `obol sell ... --description` is now the canonical flag;
  `--register-description` is kept as an alias.

## When openapi lands, decide on each of these

### 1. Source of truth for service docs

Right now operators put a one-line description in `--description` and
nothing else. The openapi branch introduces real OpenAPI specs per
service. We should:

- Pick the spec as the source of truth for everything richer than a
  single sentence (parameters, request/response shapes, error codes).
- Keep `--description` as the short summary (the OpenAPI `info.summary`
  equivalent). It still drives meta/OG and the storefront card subtitle.
- Wire the controller / `serviceoffer-controller` to publish the spec
  somewhere the 402 page can deep-link to (probably
  `<storefront>/services/<name>/spec` or
  `<base>/services/<name>/openapi.json`).

### 2. 402 page: deep-link to the spec, drop the inlined examples

For **agent** offers in particular, the current "Pay manually" card ships
an inlined chat-completions example. Once specs exist:

- Replace the inlined body with a one-liner: "See the full request/response
  contract at <spec URL>." Keep one minimal example for "I can read but
  not click."
- For **inference**, the `obol buy inference` CLI snippet still wins as
  the primary CTA — keep it. But link to the spec for "what does the
  upstream actually accept?" so callers don't have to assume vanilla
  chat-completions.
- For **http**, defer entirely to the spec. The Pay-with-Obol prompt
  becomes "Use the buy-x402 skill against …; see <spec URL> for the
  request shape."

### 3. Storefront: render OpenAPI parameters + examples per service

`ServiceCard` currently shows `name / type / description / model / endpoint`.
Once specs exist, expand the expanded panel to render:

- Operation summary + parameter list (path, query, body).
- One worked request example per language tab (Python/JS).
- A "Try it" button that prefills the existing `BuyWithCode` snippet with
  the actual request body, not just the URL.

### 4. CLI: `--openapi <path>` on `obol sell`

Add a flag that points at a local OpenAPI document. Either:

- Inline it into the ServiceOffer (`spec.openapi` — needs a CRD field), or
- Publish it as a sibling ConfigMap the controller mounts/serves at the
  spec URL.

The latter is probably right — keeps the CRD lean, lets operators rotate
the spec without touching the offer.

### 5. ERC-8004 registration document

ERC-8004 already has a `description` field. Once we publish an OpenAPI
spec per service, add an OASF-shaped reference to it inside the
registration document so discovery callers can pull the contract without
the storefront.

### 6. Stuff I touched on this branch that the openapi work should
   double-check

- `RouteRule.Description` now means "user-facing service description"
  (was previously a debug label `"ServiceOffer <name>"`). If the
  openapi branch starts emitting richer text, route it through the same
  field so the existing 402 + storefront paths pick it up for free.
- `PaymentDisplay.OfferType`, `OfferName`, `OfferDescription`, `Model`
  are the per-route bits the renderer cares about today. If openapi
  introduces more (e.g. a `SpecURL`), add it next to these.
- `buildTypeCopy` is the central branch point — extend its `typeCopy`
  struct rather than scattering `{{if .OfferType …}}` through the
  template.
- `cmd/obol/sell.go` rejects `/` in `--model` for `sell inference`
  because LiteLLM's `paid/*` wildcard only matches one path segment.
  If openapi-derived inference services start declaring model IDs
  differently, keep the same constraint.

## Shorten the per-agent namespace (`agent-<name>` → flat)

Filed here because it surfaced during 402-page work — keep with the rest
of the storefront/discovery cleanup.

Today every Agent CR lives in its own `agent-<name>` namespace and that
namespace doubles as:

- the offer namespace (the confused-deputy guard at
  `internal/serviceoffercontroller/agent_resolver.go:38` forces
  `spec.agent.ref.namespace == offer.namespace`),
- the per-agent runtime namespace (Hermes Deployment/Service/PVC/Secret
  all named generically — `hermes`, `hermes-data`, `hermes-api-server`
  — at `internal/serviceoffercontroller/agent.go:392`),
- the host data path prefix (`HostHomePath`,
  `cfg.DataDir/agent-<name>/...`).

Operators see `agent-quant`, `agent-hello-2`, etc. spew out of
`kubectl get ns`, and demo offers can't sit in a shared `demo`
namespace alongside the http demos.

To fix:

1. Include the agent name in every per-agent runtime resource name
   (`hermes-quant`, `hermes-quant-data`, `remote-signer-quant`, …) so
   multiple agents can share a namespace without collision.
2. Update the agent reconciler's runtime-update logic and label
   selectors (`obol.org/agent`,
   `app.kubernetes.io/instance`) accordingly.
3. Re-key `HostHomePath` and friends off agent name (or `<ns>/<name>`)
   rather than `Namespace(name)` so host data paths don't collide when
   multiple agents share a namespace.
4. Decide what to do with the confused-deputy guard. If we want the
   demo path to put the Agent CR directly in `demo`, the guard stays as
   "same namespace required" and the namespace just becomes `demo`
   instead of `agent-<name>`. If we want a richer split (e.g. ops
   namespace + per-agent name), we'd need a different cross-namespace
   trust story.
5. Migration: dev clusters can be `kubectl delete ns agent-*`'d before
   the change, no production state to preserve (per
   2026-06-04 confirmation: `demo quant` has never shipped in a
   tagged release).

The label `obol.org/demo=true` introduced in this branch is the
interim signal — drop it once steps 1–4 land.

## Out of scope for this follow-up

- Sub-resource paths on a single service (we still have one HTTPRoute
  per offer; multi-operation routing is openapi territory).
- Per-operation pricing (today an offer has one price for all paths
  under its prefix). The openapi spec might want to declare per-op
  prices; that's a CRD + controller change, not a 402-template change.
