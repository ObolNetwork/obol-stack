# Proposal: storefront routing & offer architecture for real multistores

**Status:** proposal — decision requested before teams build on the current URL contract
**Baseline reviewed:** `be0a237` (main, 2026-07-03)
**Author context:** learnings from operating a live, paying multistore on obol-stack —
three x402-gated offers (a 90-route trading-intelligence REST API at $0.001/call, a
16-tool solar-economics API at $0.05/call, and a public Hermes agent at $0.01/turn),
each on its own public subdomain, with agents acting as paying buyers of sibling
offers. Every claim below carries a file:line citation against `be0a237`; each was
independently adversarially verified (72 findings checked, 0 refuted).

---

## TL;DR — the one decision that is hard to reverse

**The x402 market is origin-keyed; obol-stack is path-keyed.** x402scan and agentcash
group resources **per origin** and crawl `GET <origin>/openapi.json`. Main hard-wires
the opposite topology: every offer is a hostname-less `PathPrefix /services/<name>`
route on one shared origin (`render.go:604`, `types.go:441`), discovery is **one
aggregate** `openapi.json` across all offers (`openapi.go:26`, route at
`render.go:425`), and multi-hostname tunnels serve the identical shared surface on
every hostname (`tunnel.go:1232`, `cmd/obol/tunnel_domain.go:154`).

The consequence, observed live: any per-origin crawler sees every offer on the stack
mixed into one listing — mixed pricing, mixed attribution, agents and data APIs in one
blob (we call this the *3-service leak*; agentcash showed our chat agent bundled with
two unrelated APIs until we hand-built per-offer subdomains, after which discovery
correctly showed "Routes: 1").

To run a real store today we hand-maintain **~16 HTTPRoutes, a static discovery
ConfigMap, and a Traefik middleware** that the controller and CLI do not know about
and actively fight (force server-side-apply on generated routes, a catch-all
storefront route re-seized on every `stack up`, template steamrolling on upgrade).
The URL contract — `/services/<name>` paths, catalog links, ERC-8004 service
endpoints — is what buyers, indexers and on-chain registrations bind to. Changing it
after third parties integrate is a breaking migration. **Per-offer origins should
land before that happens.**

---

## What main already gets right (verified — no relitigation needed)

The payment core is in good shape. Credit where due; none of these need changes
beyond the noted nits:

| Verified on `be0a237` | Evidence |
|---|---|
| Inline paid-gateway proxy (verifier is the offer backend; verify → proxy → settle-after-success) replaced fragile ForwardAuth for offers | `verifier.go:238`, `render.go:599-617` |
| x402 v2 `PAYMENT-SIGNATURE` accepted alongside v1 `X-PAYMENT`, with regression tests; re-landed in #690 and contained in the pinned verifier image | `forwardauth.go:215-224`, `forwardauth_test.go:185`, `x402.yaml:265` |
| Shared-origin discovery routes are **structurally** free (Exact-match HTTPRoutes straight to the catalog httpd, never through the verifier) | `render.go:425-513` |
| Paid public agent: verifier injects the per-namespace Hermes key only after payment approval | `serviceoffer_source.go:115`, `verifier.go:609` |
| Fail-closed: unmatched URIs under a known paid prefix 403; `readyz` held until the first route-table apply | `verifier.go:161` |
| `down`/`purge` fail closed when gate-ready offers exist (post-mortem of the 2026-05-22 outage) | `stack/safety.go:74`, `stack.go:274` |
| Skill-catalog rollout de-flapped by content hash (263ae19, a42e9c9) | `catalog.go:56` |
| SOUL template ships an adversarial-inputs / anti-disclosure section | `agentruntime/soul.go:47` |
| Logo preflight, inline data-URI logos, OpenAPI contact email for x402scan listing quality | `storefront/logocheck.go:46`, `openapi.go:105` |
| In-band x402-paid MCP (`obol sell mcp`): verify → execute → settle *inside* the tool call, so buyers are never charged for failed tools | `x402mcp/server.go` |

Also verified as **no longer true** of main, so our own fork can retire the patches:
the v2-header silent-402 (fixed, see above), trailing-slash/canonical-URL settle
fragility (settle matching ignores the resource URL entirely — `forwardauth.go:455`),
and CLI-added tunnel hostnames being destroyed by `stack up` (persisted since
4c6ddeb — `tunnel/hostnames.go:125`).

---

## P1 — Per-offer origins (`spec.hostname`) **[the critical-path decision]**

### Problem

* No hostname anywhere in the offer path: CRD has no field (`types.go:441`),
  generated routes carry no `hostnames:` stanza (`render.go:604`), so every offer
  answers on **every** tunnel hostname.
* The only free openapi is the aggregate at the shared origin root
  (`render.go:425`, `openapi.go:182`) → the 3-service leak for every per-origin
  crawler.
* No `/.well-known/x402` anywhere in the tree (repo-wide grep; only
  `agent-registration.json` exists) — the crawler fallback path 404s.
* The tunnel storefront **pre-empts** the fix: `CreateStorefront` force-applies one
  catch-all `PathPrefix /` HTTPRoute over **all** tracked hostnames on every
  `stack up`/hostname add (`tunnel.go:1349`), so even a hand-built per-offer origin
  has its root contested and re-seized.
* ERC-8004: a single AgentIdentity document merges every offer under ONE elected
  "owner" offer's name/description (`identity_render.go:57,83`) — mixed on-chain
  attribution for unrelated products.

### Proposed change

1. `ServiceOfferSpec.hostname` (optional). When set:
   - offer HTTPRoute rendered with `hostnames: [<hostname>]` and path `/` (rewrite
     to upstream as today); shared-origin `/services/<name>` kept as the default
     and as a back-compat alias.
   - per-offer discovery bundle rendered into the offer's namespace (or catalog
     ConfigMap keys): `openapi.json` **scoped to that offer** (paths rooted at `/`,
     `servers: [https://<hostname>]`), `/.well-known/x402` (resource list from the
     same offer data), and a minimal landing at `/` — each as Exact-match,
     verifier-bypassing routes on that hostname.
2. `obol tunnel hostname add <host> --offer <ns>/<name>` records the binding;
   `CreateStorefront` **excludes** offer-bound hostnames from the catch-all route
   (or honors an `obol.org/origin-owner` annotation) and drops `--force-conflicts`
   there so an operator-owned root surfaces as a conflict instead of being seized.
3. Aggregate `openapi.json`/`skill.md`/storefront remain the shared-origin surface
   (mall directory); per-offer origins are the discovery-crawl surface (the shops).
4. `spec.registration.identityRef` so an offer can register under its own
   AgentIdentity; per-offer identities publish hostname-scoped
   `agent-registration.json`.

Touches: `monetizeapi/types.go`, `render.go`, `openapi.go`, `catalog.go`,
`serviceoffer_source.go` (rules keyed per hostname), `tunnel.go`,
`identity_render.go`. Verifier matching already tolerates it (`Resource.URL` is
built from `X-Forwarded-Host` per request).

### Why now

Path→origin later is a URL-contract break for every integrated buyer, crawler
listing, and minted ERC-8004 endpoint. The reference pattern is proven live (see
appendix) — this proposal is a generalization of manifests that have been serving
paid traffic for weeks.

---

## P2 — The offer route table (`spec.routes[]` / OpenAPI fragment)

### Problem

Discovery operations are synthesized from `spec.type` alone: inference/agent → one
chat-completions POST; **everything else → exactly one generic POST** with "Shape is
not specified in phase 1" (`openapi.go:289,236`). A 90-route GET/query-param data
API is advertised as a single opaque POST — buyer agents cannot plan calls against
it. The verifier likewise gets exactly one RouteRule/price per offer
(`serviceoffer_source.go:173`): no per-route pricing, no free sub-paths, no MCP tool
catalog. This is why our data offers serve their own openapi and MCP tool list.

The `stream:false` incident (62f11c9) is the drift class this creates: the schema
was fixed, but `x-guidance`, two `skill.md` strings, and the `buyprompts` CallShape
**still advertise `stream:true`** (`openapi.go:126`, `render.go:1045,1538`,
`buyprompts.go:167`) — four surfaces, no single source of truth.

### Proposed change

1. `spec.routes[] {path, method, summary, params?/schemaRef?, price?, free?}` —
   *one* table per offer driving all four surfaces: generated openapi paths,
   verifier RouteRules (per-route price override, free carve-outs), the MCP tool
   catalog (`mcp-tools.json`, served free per offer), and skill.md entries. Interim
   phase 1: `spec.openapiRef` (ConfigMap ref) merged verbatim under the offer's
   path with payment-info injected per operation.
2. Extract the shared "route surface" package consumed by `render.go`,
   `openapi.go`, `serviceoffer_source.go`, `buyprompts` — with a golden test that
   the three renderings enumerate identical route sets. Fold the streaming
   advisory into it (single const; flip all surfaces to `false`).
3. `x-payment-info.accepts[]` completeness: add `scheme:"exact"` and
   `maxTimeoutSeconds` (schema marks them required; `paymentInfoAccept` omits both
   — `openapi.go:418`) so pay-without-402 is actually possible.
4. Marry `spec.routes[]` to the existing in-band paid-MCP (`x402mcp`): generate a
   *typed* tool per route instead of today's single untyped
   `additionalProperties:true` proxy tool (`x402mcp/server.go:134`). The in-band
   verify→execute→settle flow is the right payment shape; it lacks only the
   catalog.

---

## P3 — Agent runtime config is CR-owned, not template-owned

### Problem (bit us twice in prod, mid-incident)

`AgentSpec` is `{runtime, model, skills, objective, wallet}` (`types.go:669`) —
no MCP servers, no `max_turns`, no timeouts. The hermes `config.yaml` is a
hardcoded `fmt.Sprintf` template (`agent_render.go:120-143`) and the ConfigMap is
**fully rewritten** on every agent reconcile (`agent.go:472`; ConfigMap is not in
the create-only set, `agent.go:498-504`). Any hand-added `mcp_servers` block is
wiped and `max_turns` reset to 30 by *any* Agent event, requeue, or controller
restart. The wipe is **delayed and silent**: config is copied by an init container
at pod start (`agent_render.go:272`), so a live agent keeps working until some later
restart, then loses its tooling. 263ae19/a42e9c9 do **not** address this (catalog
churn only).

Compounding defaults, all pentest-proven on our deployment: `max_turns: 30` is a
30-paid-tool-call burn budget for one under-specified prompt (prod value: 8); no
per-tool or MCP timeout exists (keepalive hangs); the LiteLLM master key is embedded
verbatim in the checksummed config (`agent_render.go:124`), so key rotation bounces
every single-replica Recreate agent with hard downtime (`agent_render.go:243,255`);
the init container also reverts the shipped `buy <model> --set-default` flow on
every restart (`agent_render.go:272` vs `buy-x402/SKILL.md:20`).

### Proposed change

1. CRD fields: `spec.mcpServers[]`, `spec.maxTurns`, `spec.toolTimeouts`,
   rendered by `renderHermesConfig`. (A working `mcpServers` implementation incl.
   CRD schema + deepcopy exists downstream: commit `5427f4d`.)
2. Escape hatch for everything else: honor `obol.org/config-unmanaged` (switch the
   ConfigMap to create-only, same mechanism as Secrets) or deep-merge an
   operator-owned overlay key.
3. Defaults for paid public agents: `max_turns` ~8, MCP/tool timeout defaults;
   move the LiteLLM key out of the checksummed config (Secret env) so rotation
   doesn't roll pods; make config-seed merge (seed-if-absent / overlay
   controller-owned keys) instead of `cp` over.
4. Wallet lifecycle: `obol agent wallet fund`, a low-balance status condition, and
   remote-signer spend-rate policy (today any code with `REMOTE_SIGNER_TOKEN` can
   sign unbounded transfers — `render.go:1138`).

---

## P4 — Free paths, limits, and free public agents (small CRD additions)

* **`spec.freePaths[]`** — Exact-match rules rendered straight to the upstream
  Service, excluded from the verifier pattern. Today *everything* under an offer's
  prefix is gated (`render.go:611`); per-offer `/healthz`, `/openapi.json`,
  `/mcp/tools` require hand-built routes that force-SSA reverts if they touch the
  generated route (`controller.go:1363` applies with `Force=true` every reconcile
  — hand-edits survive only under a *different resource name*, an undocumented
  invariant).
* **`spec.limits {maxInFlight, rps}`** — render a Traefik `inFlightReq`/`rateLimit`
  Middleware and attach via ExtensionRef. The controller already wires a Middleware
  dynamic client and never uses it (`controller.go:133`); the unbounded-concurrency
  hole is pentest-proven (prod fix: hand-attached `inFlightReq amount:4`). Default a
  small `maxInFlight` for `type=agent`. Add pod resource requests/limits to
  `agentPodSpec` while there (`agent_render.go:241`).
* **Free public agents**: `price 0`/`spec.free` renders the route to the agent
  Service with a `RequestHeaderModifier` injecting the resolved bearer — the free
  sibling of the now-native paid pattern. Today it's hand-built only
  (`agent_render.go:77`).

---

## P5 — Reconcile safety for clusters with live revenue

Verified mechanics on main: `stack up` unconditionally re-renders the embedded
defaults tree and `helmfile sync`s it (`stack.go:459`), re-syncs the default agent
every run (`hermes.go:112`), and replays recorded intent — with **no plan/diff/dry-run
and no live-services gate on `up`** (only `down`/`purge` are gated). Drift protection
is per-field whack-a-mole (LiteLLM merge `stack.go:1449`, `command_allowlist`
`hermes.go:1193`) while `--force-conflicts`/SSA-Force is the default at 8+ call
sites. Embedded digest pins revert operator image pins on every upgrade
(`defaults.go:87,117`); the promised `HERMES_IMAGE` env override is a comment — the
function ignores it (`agent_render.go:631-635`). The minimum upgrade unit for the
payment gate is the whole `base` release (`helmfile.yaml:124`). Backup exports a
fixed allowlist and is blind to operator HTTPRoutes/Middlewares
(`stackbackup/cluster.go:25-34`).

Proposed, in order of value:

1. `obol stack up --dry-run` = `helmfile diff` (binary already shipped) + a printed
   list of non-helm resources `up` will rewrite; wire `DiscoverLiveServices` into
   `up` (confirm gate when gate-ready offers exist, mirroring down/purge).
2. Default SSA **without** Force for CLI/controller-managed resources + one
   documented `obol.org/unmanaged` annotation checked by every
   regenerate-and-apply path. One generic mechanism replaces the next five bespoke
   merges.
3. Operator image-pin overlay (`~/.obol/defaults-overrides/images.yaml`) merged
   after `CopyInfrastructure`; make `hermesImage()` actually read `HERMES_IMAGE`.
4. Split verifier+controller out of `base` into their own helmfile release so the
   payment path can be rolled independently; skip agent re-sync when the rendered
   inputs hash unchanged (the a42e9c9 pattern) + `--skip-agents`.
5. Backup: sweep non-`obol.org/managed-by` HTTPRoutes/Middlewares into
   `operator-resources.json`.

---

## Verified one-liners & small fixes (mechanical, no design needed)

| Fix | Where |
|---|---|
| `HandleVerify` funnel metrics count only `X-Payment` — v2 payments invisible in the funnel | `verifier.go:224` (mirror `verifier.go:282`) |
| `no_matching_requirement` 402 has no log line (the exact silent-402 diagnosis pain) | `forwardauth.go:~260` |
| ForwardAuth path matching includes the query string → `/rpc?method=x` free-passes; exact patterns have no fail-closed backstop | `verifier.go:142`, `matcher.go:33` |
| Verifier route table is first-match in name-sort order, pathconflict only catches *equal* paths → nested offer paths capture each other's traffic | `matcher.go:19`, `pathconflict.go:54` (sort by specificity) |
| Reserved-path denylist (`/api`, `/openapi.json`, `/skill.md`, `/rpc`, `/.well-known`…) in pathconflict + CLI preflight | `pathconflict.go:24`, `sell.go:4648` |
| `stream:true` still advertised in x-guidance, 2× skill.md, buyprompts (schema alone was fixed) | `openapi.go:126`, `render.go:1045,1538`, `buyprompts.go:167` |
| `x-payment-info.accepts[]` missing `scheme` + `maxTimeoutSeconds` | `openapi.go:418` |
| Settle-failure discards the already-executed upstream response and advertises retry → double side effects on non-idempotent POSTs; needs idempotency guidance or replay cache | `forwardauth.go:605-630` |
| `sell update` can't touch registration metadata; resume ledger replays stale manifests over hand patches | `sell.go:2757,4770` |
| `x-context-window` advisory for chat offers | `openapi.go:524` |
| Scalar docs UI depends on jsdelivr at runtime — blank page on air-gapped clusters | `scalar_html.go:75` |
| Per-offer branding (icon/contact) — today one global StorefrontProfile brands everything | `storefront/record.go:15` |
| SOUL.md guardrail ratchet: seeded once, never re-ratcheted on existing agents | `agentruntime/soul.go:17` |

---

## Appendix — the live reference topology this proposal generalizes

What one offer looks like today on our production cluster (hand-built; names
sanitized to the `hyperliquid-data` offer). This is the shape P1 turns into
generated resources:

```yaml
# 1. Per-origin host route: public /v1 rewritten into the verifier's path-world
kind: HTTPRoute                      # hand-built today; P1 generates this
metadata: {name: hyperliquid-data-host, namespace: hl-intel}
spec:
  hostnames: [hyperliquid-data.v1337.org]
  rules:
  - matches: [{path: {type: Exact, value: /openapi.json}}]      # discovery: FREE
    backendRefs: [{name: hl-intel, port: 8092}]                 # bypasses verifier
    filters: [{type: URLRewrite, urlRewrite: {path: {replaceFullPath: /openapi.json}}}]
  - matches: [{path: {type: PathPrefix, value: /v1}}]           # paid surface
    backendRefs: [{name: x402-verifier, namespace: x402, port: 8080}]
    filters:
    - {type: RequestHeaderModifier, requestHeaderModifier: {set: [
        {name: X-Forwarded-Proto, value: https},
        {name: X-Forwarded-Host, value: hyperliquid-data.v1337.org}]}}
    - {type: URLRewrite, urlRewrite: {path: {
        type: ReplacePrefixMatch, replacePrefixMatch: /services/hyperliquid-data/v1}}}
  - matches: [{path: {type: PathPrefix, value: /}}]             # landing
    backendRefs: [{name: hl-intel, port: 8092}]
---
# 2. Structurally-free ops/discovery Exact routes (never touch the verifier)
kind: HTTPRoute
metadata: {name: hyperliquid-data-public, namespace: hl-intel}
spec:
  hostnames: [hyperliquid-data.v1337.org]
  rules:   # all Exact → hl-intel:8092 directly
  - {matches: [{path: {type: Exact, value: /}}]}
  - {matches: [{path: {type: Exact, value: /.well-known/x402}}]}
  - {matches: [{path: {type: Exact, value: /healthz}}]}
  - {matches: [
      {path: {type: Exact, value: /v1/openapi.json}},
      {path: {type: Exact, value: /v1/stats}},
      {path: {type: Exact, value: /v1/mcp/tools}},
      {path: {type: Exact, value: /v1/verify}}]}
---
# 3. Paid public agent on its own origin, with a concurrency guard
kind: HTTPRoute
metadata: {name: hyperliquid-analyst-host, namespace: agent-hyperliquid-analyst}
spec:
  hostnames: [hyperliquid-analyst.v1337.org]
  rules:
  - matches: [{path: {type: PathPrefix, value: /v1}}]
    backendRefs: [{name: x402-verifier, namespace: x402, port: 8080}]
    filters:
    - {type: ExtensionRef, extensionRef: {group: traefik.io, kind: Middleware,
        name: hyperliquid-analyst-inflight}}   # inFlightReq amount: 4 (pentest fix)
    - {type: RequestHeaderModifier, requestHeaderModifier: {set: [
        {name: X-Forwarded-Proto, value: https},
        {name: X-Forwarded-Host, value: hyperliquid-analyst.v1337.org}]}}
    - {type: URLRewrite, urlRewrite: {path: {
        type: ReplacePrefixMatch, replacePrefixMatch: /services/hyperliquid-analyst/v1}}}
```

Plus (also hand-built): a static per-origin discovery ConfigMap for the agent
(`analyst-openapi.json`, `analyst-wellknown-x402.json`) served via rewrite routes —
because an agent offer has no backend able to serve its own openapi; P1's per-offer
discovery bundle replaces exactly this.

Under this topology, agentcash/x402scan discovery per origin is correct ("Routes:
1" for the agent), all three offers list independently, and the paid surfaces have
been settling real USDC on base for weeks.
