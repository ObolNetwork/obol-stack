# Proposal: agent-driven storefront deployment — an admission validity backbone + a typed MCP front door

**Status:** proposal — stacks on [multistore-storefront-routing](./multistore-storefront-routing.md)
(P1 per-offer origins, P2 route table, P3 CR-owned agent config)
**Baseline:** main `be0a237`; every code claim below is file:line-cited against it.
**Note on rigor:** an earlier draft framed this as an MCP broker alone. Adversarial
review (3 reviewers vs `be0a237`) showed the broker was overselling itself as *the*
validation path when native Kubernetes gives a stronger one. This version splits the
design accordingly and corrects the citations that pass had flagged.

---

## TL;DR — two parts, one goal ("robust and predictable to be valid")

Making an agent's storefront deployment **valid** and making it **ergonomic** are
two different jobs, best solved by two different mechanisms:

1. **Validity backbone — a `ValidatingWebhookConfiguration`** that runs the full
   check battery (price-required, cross-offer path first-claimant, grant bounds,
   reserved paths, logo/contact, design tokens) **synchronously at admission, for
   every writer** — the CLI's `kubectl apply`, an agent's raw
   `POST /apis/obol.org/…` (which is how agents create offers *today*, `monetize.py:472`),
   and the MCP server alike. This is the part that makes deployment *predictable to
   be valid*: an invalid ServiceOffer is **rejected at write time**, not discovered
   later by a buyer hitting a $0-gated offer. A webhook — unlike the CEL-only
   ValidatingAdmissionPolicy already in the tree — *can* list other objects, so it
   can do the path-conflict and grant lookups the VAP structurally cannot
   ("a ValidatingAdmissionPolicy cannot do this — VAPs cannot list other cluster
   objects", `pathconflict.go:17`).

2. **Ergonomic front door — a `storefront-mcp` server** giving permissioned agents
   *typed* tools with plan / preview / apply / status semantics, so an LLM deploys
   by calling `storefront_plan_offer` and reading a structured diff instead of
   hand-writing CR YAML and decoding admission errors. Its unique value over "just
   let the agent `kubectl apply --dry-run=server`" is exactly what a webhook can't
   give: **rendered-bytes preview** of the buyer-visible landing/openapi/402
   (`storefront_preview_landing`), grant-scoped **discovery of one's own charter**
   (`storefront_whoami`), and **convergence probes** (applied ≠ serving).

The backbone enforces validity for everyone; the front door makes it pleasant for
agents. Neither invents a new privilege — agents already have ServiceOffer write
power (mother agent: `obol-agent-monetize-rbac.yaml:52`; the raw path:
`monetize.py:472`); what's missing is *validation on that path*, which today is
**zero** (the CLI's preflights are `package main`, unexported, and never run for a
non-CLI writer).

```
            ┌─────────────────────────────── validity backbone ───────────────────────────────┐
CLI apply ─┐                                                                                    │
raw agent ─┼─► kube-apiserver ─► ValidatingWebhookConfiguration (storefront-validate) ──reject──┤
MCP apply ─┘                        price-required · pathconflict(list) · grant · reserved       │
            └──────────────────────────────────────────────────────┬──────────────────────────┘
                                                                    ▼ admitted
                                        serviceoffer-controller (unchanged privileged fan-out)

agent ─► storefront-mcp (typed tools) ─► same apiserver ─► same webhook   (ergonomic front door)
             whoami · plan · preview · apply · status · retire
```

---

## 1. Grounding — what main actually does (verified)

* **Agents already create offers, unvalidated.** The `sell` skill POSTs CRs straight
  at the apiserver with the pod's mounted SA token (`monetize.py:472` via
  `kube.py:32` "No kubectl required"). The mother agent SA holds ServiceOffer +
  PurchaseRequest CRUD (`obol-agent-monetize-rbac.yaml:52`). **Generated child agents
  have a mounted token and zero RBAC** (`agent_render.go:76` renders no Role/Binding;
  repo-wide grep confirms) — so "let the agent do it" means either the over-powered
  mother or a powerless child.
* **No validation reaches that path.** The path-collision and price checks are
  `preflightOfferPathCollision` / `resolvePriceTable` — both `package main`,
  unexported, kubectl-shelling (`sell.go:4651`, `sell.go:3687`). `resolvePriceTable`
  is the *only* place "price required" is enforced (`sell.go:3719`); the schema layer
  returns `"0", nil` for an empty table (`payment.go:141`), and the CRD requires the
  `price` *object* but leaves all four sub-fields optional
  (`serviceoffer-crd.yaml:192-221`, `required: [network, payTo, price]`), so
  `price: {}` is admitted and silently gates at $0.
* **Admission today is shape-only and doesn't cover offers.** The single VAP
  `openclaw-resource-guard` matches only `openclaw-`/`hermes-` SA prefixes (child
  `agent-*` SAs are *outside* it) and guards middlewares/HTTPRoutes/namespaces/
  secrets/Agents — **ServiceOffers are not in its resourceRules**
  (`obol-agent-admission-policy.yaml:20-36,39`). And CEL VAPs can't list other
  objects (`pathconflict.go:17`), so first-claimant path conflict is unreachable
  there by construction.
* **The MCP plumbing is vendored and unused.** `modelcontextprotocol/go-sdk v1.3.0`
  (go.mod:17), streamable-HTTP handler with a per-request server getter
  (`x402mcp/server.go:149`, matching `NewStreamableHTTPHandler`), bearer-auth
  middleware `auth.RequireBearerToken` (`go-sdk auth/auth.go:69`) +
  `TokenInfoFromContext` (`auth/auth.go:55`) — zero in-tree imports. Reference
  streamable client: `client.Connect(ctx, &StreamableClientTransport{Endpoint}, nil)`
  (`flows/clients/mcp-paid-client.go:72`).
* **The validation building blocks are already exported and CLI-free** in
  `internal/storefront`: `PreflightLogoURL` (`logocheck.go:46`), `ValidateLogoURL` /
  `ValidateContactEmail` / `MergeProfile` / `ConfigMapManifest` (`profile.go`).
  `schemas.PriceTable` + `EffectiveRequestPriceE` are exported (`payment.go:128`).
  `pickPathConflict` is a **pure** function (`pathconflict.go:42`,
  `func(offer, others []*ServiceOffer) string`) — but its caller feeds `others` from
  the controller's shared informer, so a separate process must supply its own
  cluster-wide ServiceOffer list.
* **Network + apply mechanics check out for colocation in `x402`.** The traefik
  gateway `web` listener sets `namespacePolicy.from: All` (`helmfile.yaml:81`), so a
  Service+HTTPRoute in `x402` needs no ReferenceGrant, mirroring the x402-verifier
  (`render.go:581`). The controller writes ServiceOffer `.status` via the status
  subresource only (`controller.go:1371`), never `.spec` via SSA — so a webhook/MCP
  and the controller never fight over `.spec`. Agent-pod egress reaches `traefik`
  (any port) and same-namespace, but `x402` is **ingress-only** from agents
  (`agent_render.go:546-591`) — so the MCP is *reached through the gateway route*,
  not by direct pod-to-`x402` dialing.

---

## 2. Why not just native primitives? (the maintainer's first question)

A fair challenge: most of this is reducible to Kubernetes-native pieces. The honest
accounting, and why the answer is "use them, plus a thin MCP":

| Job | Native primitive | Verdict |
|---|---|---|
| Authn of the caller | Mounted SA token (apiserver-verified) | **Use it** for the raw path. The MCP adds a *grant* token only to scope agents more tightly than their SA (see §3). |
| Race-free apply | `resourceVersion` optimistic concurrency (409 Conflict) | **Use it** — drop the hand-rolled planId-drift check for same-object edits; keep a plan only as an *ergonomic* preview (§5). |
| Reject invalid specs | `ValidatingWebhookConfiguration` (+ `kubectl apply --dry-run=server` for a preflight) | **Use it — this is the backbone.** A webhook runs the cross-object checks a VAP can't, for all writers. |
| Cross-namespace offer RBAC | ClusterRole or per-namespace Role/RoleBinding | See §3 — the honest version is a ClusterRole; tenant isolation is application/webhook logic, not RBAC. |
| Rendered preview of buyer bytes | — none — | **Only the MCP/controller can do this.** A webhook validates; it doesn't render a landing page. |
| LLM-friendly typed tool calls | — none — | **Only MCP.** Agents are far better at typed tool calls than at raw apiserver JSON + admission-error round-trips. |

So the design is **not** "a broker instead of native validation." It's: put validity
in a webhook (native, universal), and put *ergonomics + preview + grant-scoping* in
the MCP. The MCP's `plan`/`apply` become thin conveniences over
`--dry-run=server` + a normal apply; the webhook is what actually guarantees
validity, including when an agent bypasses the MCP and POSTs raw (which it can do
today and will keep being able to do).

---

## 3. Permission model — corrected, with honest RBAC

Four mechanisms, but **not** four independently-enforced layers over every property —
the earlier draft overclaimed that. What each actually enforces:

| Mechanism | Enforces | Honest limit |
|---|---|---|
| **Grant token** (per-agent bearer, §4) | *Which agent* is calling the MCP | Only gates the MCP door; the agent's SA can still POST raw (webhook catches that path) |
| **Webhook** (`storefront-validate`) | *Validity + grant bounds* (price/quota/namespace/hostname) and path first-claimant — **for all writers**, via live cross-object lists | Async-free but adds an apiserver dependency; must fail-open-or-closed deliberately (we fail **closed** for storefront-mcp SA writes, **open** with a warning event for CLI to avoid bricking operators) |
| **Service RBAC** | The MCP process's *reach* | **Necessarily a ClusterRole** over ServiceOffers cluster-wide — cross-namespace offer CRUD has no namespaced form here without minting per-grant RoleBindings. **Tenant isolation between agents is therefore application + webhook logic, not RBAC.** Stated plainly, not dressed up. |
| **Controller backstop** | Everything else | Unchanged: `RoutePublished=False/PathConflict`, drain/finalizer, catalog render |

Two ways to make layer-3 honest rather than hand-wavy — pick one at implementation:
(a) accept a cluster-scoped MCP ClusterRole and rely on the webhook to enforce
"this grant may only write in these namespaces" (simplest; the webhook already has to
do grant lookups); or (b) have the serviceoffer-controller mint a per-grant
`Role`+`RoleBinding` in each `offerNamespace` when a `StorefrontGrant` is created, so
the MCP SA is genuinely scoped. (b) is more work and needs the controller to hold
`rolebindings.create`; (a) is the lazy correct default given the webhook exists.

Deliberate non-goals unchanged: the MCP gets **no** factory powers (agents/namespaces
stay with the factory RBAC+VAP pair) and **holds no funds** (§7).

Writes use SSA with field-manager `storefront-mcp`, **no `Force`** (parent P5), an
`obol.org/created-by: agent.<ns>.<name>` label, and a Kubernetes Event on the offer.

---

## 4. `StorefrontGrant` — the permission object

Namespaced in `x402`, operator-created, reconciled by the serviceoffer-controller:

```yaml
apiVersion: obol.org/v1alpha1
kind: StorefrontGrant
metadata: {name: hyperliquid-analyst, namespace: x402}
spec:
  subject: {kind: Agent, namespace: agent-hyperliquid-analyst, name: hyperliquid-analyst}
  offerNamespaces: [agent-hyperliquid-analyst]
  maxOffers: 3
  types: [http, agent]
  pricePerRequest: {min: "0.0001", max: "1.00"}   # USDC bounds
  hostnamePatterns: ["*.agents.v1337.org"]         # P1 spec.hostname allowlist
  allowProfile: false                              # store-wide branding stays operator-only
  registration: {allowed: true, requiresApproval: true}
status:
  tokenSecretRef: {name: storefront-mcp-token, namespace: agent-hyperliquid-analyst}
  offersInUse: 1
  conditions: [...]
```

The controller mints a random bearer token into a Secret in the agent's namespace
(the per-agent `hermes-api-server` pattern, `agent.go:535`). **The webhook reads the
grant** to enforce bounds regardless of writer; the MCP reads it for `whoami` and
pre-checks. Once P3's `spec.mcpServers[]` exists, the controller injects the
storefront-mcp entry into the agent's config with the token *sourced from the Secret*,
never inline (inline secrets leak and bounce pods on the checksum annotation —
the LiteLLM-key lesson, `agent_render.go:124/255`).

**Token mechanics (corrected):** `auth.RequireBearerToken` rejects a token whose
verifier returns no non-zero, non-expired `Expiration` (`auth/auth.go:121`), so the
verifier must synthesize a rolling expiry for an otherwise-static Secret. To bound
revocation latency, the verifier watches the token Secret via an informer (not a TTL
poll): grant deletion → controller deletes the Secret → the watch invalidates the
cached token within propagation latency, not "instantly on next call" as the earlier
draft claimed. Short-lived TokenRequest-API tokens are a later hardening (§7).

---

## 5. Tool surface — 9 tools (validate folded into plan)

Full machine-readable schemas: [`storefront-mcp-tools.json`](./storefront-mcp-tools.json).
Offer-spec input schemas are derived mechanically from the ServiceOffer CRD structural
schema (itself generated from `internal/monetizeapi` by `just generate`,
controller-gen v0.16.5), so the tool contract and the validation contract can't drift.

**Read / discover** (no side effects):

| Tool | Purpose |
|---|---|
| `storefront_whoami` | The grant: namespaces, remaining quota, allowed types, price bounds, hostname patterns, profile/registration permissions. Plan within the charter instead of learning limits by rejection. |
| `storefront_list_offers` / `storefront_get_offer` | Offers under the grant: spec + conditions (`RoutePublished`, `PaymentGateReady`, `Draining`) + public URLs + `resumeCovered` |
| `storefront_offer_status` | Conditions **plus convergence probes**: is the offer's entry in the rendered `services.json` (the `waitForPublishedCatalog` pattern, `sell_info.go:538`), and a live 402/200 probe of the public URL. Applied ≠ serving. |
| `storefront_preview_landing` | Render the exact buyer-visible bytes — landing HTML, offer-scoped `openapi.json`, `/.well-known/x402` — from a plan or live offer, no apply. **The MCP's headline capability a webhook can't provide.** (Meaningful once parent P1 lands the per-offer origin; before that it previews the openapi/well-known only — see phasing.) |

**Write** (plan → apply; the webhook is the real gate):

| Tool | Purpose |
|---|---|
| `storefront_plan_offer` | `--dry-run=server`-backed: submit the spec to the apiserver dry-run so the **webhook** returns the authoritative findings, then normalize + diff live state → `{planId, action, diff, findings, approvalRequired}`. `dryRun:true` skips diff/planId for a pure validity check (this replaces the separate validate tool). No mutation. |
| `storefront_apply_plan` | Apply by `planId`. Uses **`resourceVersion` optimistic concurrency** — a 409 from the apiserver (someone changed the object) forces a re-plan; the webhook re-runs path-conflict against *live* state at admission, so a sibling grabbing the path between plan and apply is caught **by the webhook**, not missed (the earlier planId-only check couldn't see sibling objects). Emits the audit Event; returns conditions + a poll hint. |
| `storefront_set_profile` | Store-wide branding (`StorefrontProfile`), grant-gated by `allowProfile`, reusing `MergeProfile`/`ValidateLogoURL`/`ValidateContactEmail`/`ConfigMapManifest` verbatim. Plan/apply like offers. |
| `storefront_retire_offer` | `mode: drain` patches `spec.drainAt`(+grace) — route/gate stay up for in-flight buyers, discovery flips `available=false`, controller tears routes at expiry; `mode: delete` requires Drained (finalizer serializes route teardown + ERC-8004 handoff, `controller.go:529-663`). `confirm` must echo the offer name. |

Plan TTL is **30 minutes** (raised from 10 after review), and a plan carrying
`approvalRequired` or a recorded `preview_landing` never hard-expires — `apply_plan`
re-validates live state in place rather than forcing a blind re-plan, so an
operator's review window doesn't invalidate the agent's plan.

These are **management** tools; auth is the grant token, not x402. The per-tool
`PaymentWrapper` from `x402mcp` stays available for *seller-facing* paid tools later
(parent P2.4) — paid and free tools already coexist on one server
(`x402mcp/server.go:116-146`).

---

## 6. Structured design control — tokens with a copy-safe sanitizer

A `design` block on the ServiceOffer, beside `spec.listing` (already framed "Cosmetic
only — it does not affect routing, pricing, or payment", `serviceoffer-crd.yaml:100`):

```yaml
spec:
  design:
    template: terminal        # enum: minimal | terminal | data — fixed set (cf. runtime enum)
    accent: "#2fe4ab"         # ^#[0-9a-fA-F]{6}$
    heroTitle: "Hyperliquid Intelligence"                          # free text, ≤80
    heroTagline: "90 trading-intelligence endpoints, $0.001/call"  # free text, ≤200
    icon: "data:image/png;base64,…"                                # ValidateLogoURL rules; ≤256 KiB
```

**Sanitizer — corrected.** The existing `sanitizeDisplayToken` allowlist is
`^[A-Za-z0-9._:/-]+$` (`paymentrequired.go:24`) — no space, comma, or `$`. It exists
to keep k8s-name/model-id tokens safe inside *copy-pasteable shell commands*, and is
applied today only to `d.Model` at two sites. Reusing it for hero copy would mangle
every realistic title ("Hyperliquid Intelligence" → placeholder). Design copy is free
text that lands in **HTML, not a shell command**, so it needs its own rule:
HTML-escape on render (the landing/402 are `html/template`, which auto-escapes) plus a
**reject-don't-mangle** validator that bounds length and forbids control chars and
raw `<`/`>`/`&` in the *source*, surfacing a `design-copy-invalid` finding rather than
silently collapsing to a placeholder. The `template.HTML` Lede rule
(`paymentrequired.go:337`) — "no PaymentDisplay strings interpolated raw" — is the
XSS precedent to preserve: hero copy is a normal escaped field, never `template.HTML`.

**Render sites** (all existing machinery, but they don't exist until Phase 2 — see
phasing; a plan with `design` on a pre-Phase-2 cluster returns a
`design-not-yet-rendered` **warning** so an agent isn't silently no-op'd):

1. **Per-offer landing** — parent P1's origin `/` document: one more ConfigMap key +
   Exact route, the `catalog.go` content-hash pattern; `scalar_html.go` is the
   HTML-in-Go + CSS-custom-property precedent the `accent` token maps onto.
2. **402 page** — today hardcodes Obol branding and never reads StorefrontProfile
   (`paymentrequired.go:278-283`); its palette is a hand-copied mirror of the
   storefront tokens (template line 25). Thread `accent/icon/heroTitle` through
   `RouteRule → PaymentDisplay`, the same plumbing that already carries
   `OfferDescription`/`AgentSkills` (`serviceoffer_source.go:175` → `verifier.go:507`).
   This turns the mirrored palette into data — *reducing* the documented drift hazard.
3. **Storefront cards** — `design.accent`/`icon` on `ServiceCatalogEntry`
   (`service_catalog.go:21`) for per-offer tinting on the shared origin.

Tokens-not-HTML is a deliberate predictability/injection call: the agent picks a
template enum and a few bounded fields; it never supplies markup.

---

## 7. Approvals — on-chain and store-wide stay human-gated

* **On-chain registration** (ERC-8004) costs agent-wallet gas and its cleanup is a
  finalizer-serialized multi-step (`controller.go:628-663`). With
  `registration.requiresApproval: true` a plan containing registration returns
  `approvalRequired: true`; apply parks the CR with registration disabled and the
  controller sets `PendingApproval`. **Neither an `obol sell approve` command nor an
  automated gas-balance preflight exists today** — the register command only prints a
  human-readable "make sure it has a small balance" note (`sell.go:3091`). Phase 3
  would add both: an `obol sell approve <offer>` and a programmatic pre-broadcast
  balance check. Until then approval is an operator `kubectl` edit of a documented
  condition/annotation.
* **Store-wide profile** (`allowProfile: false` default): rebranding the whole mall
  is not one shop's call.

---

## 8. Robustness gaps surfaced honestly

* **Resume-ledger orphaning — moved into Phase 0, not deferred.** The host ledger
  (`<ConfigDir>/sell-http/…`, replayed by `resumePersistedServiceOffers`,
  `sell.go:4859`) is written *only* by the CLI's `persistServiceOffer` and is
  unreachable from a pod. An MCP-created offer would therefore **silently vanish on
  the next `stack down`/`stack up`** — a routine workflow, not an edge case. That is
  unacceptable for a "robust" claim, so Phase 0 ships an **in-cluster offer record**:
  the controller writes each reconciled offer's minimal manifest into a per-offer
  ConfigMap (label `obol.org/managed-by: serviceoffer-controller`), and `stack up`'s
  replay reads *both* the host ledger and these ConfigMaps. This also fixes the CLI's
  own dual-write fragility (`record.go:21` "the host file is the source of truth") and
  the "MCP mutates a ledgered offer → stale replay clobbers it" hazard, because there
  is then one in-cluster source of truth. (`--adopt-agent-offers` does **not** exist
  today; this replaces the need for it.)
* **`price: {}` $0 hole** (`payment.go:141`, `serviceoffer-crd.yaml:192-221`): closed
  by the webhook for every writer, and by the parent proposal's CRD `minProperties`
  fix at the schema layer.
* **Token lifetime**: static Secrets with informer-bounded revocation now (§4);
  TokenRequest short-lived tokens later.
* **Webhook availability**: a down webhook must not brick the cluster — scope its
  `ValidatingWebhookConfiguration` to `obol.org/serviceoffers` only, `failurePolicy:
  Fail` for the storefront-mcp SA (agents must be validated) but with a short
  `timeoutSeconds` and an operator break-glass label, matching how the tree already
  reasons about fail-closed vs fail-open (`verifier.go` readyz gating).

---

## 9. Phasing (revised so each phase is safe to run)

| Phase | Ships | Depends on |
|---|---|---|
| **0** | The **`ValidatingWebhookConfiguration` + validation binary** (reuses `PreflightLogoURL`/`ValidateContactEmail`/`schemas.PriceTable` + an exported `pickPathConflict` + its own cluster-wide ServiceOffer list) — this alone closes the validity gap for the *existing raw-agent path*. **In-cluster offer record** so nothing orphans on `stack up`. `StorefrontGrant` CRD + controller (token mint, quota). | nothing upstream |
| **1** | `storefront-mcp` Deployment in `x402` (the ~40-line `x402mcp` server pattern + `auth.RequireBearerToken` + informer-backed token verify); tools `whoami/list/get/plan/apply/status/retire`; traefik route; a `storefront` skill (SKILL.md + streamable-HTTP script, the `buy-x402` shape) so agents use it before P3 renders `mcpServers`. | Phase 0 |
| **2** | `spec.design` block + per-offer landing render + 402/card consumption; `preview_landing` becomes fully meaningful; `set_profile`. | parent P1 (origins); Phase 1 |
| **3** | Registration approvals (`obol sell approve` + gas preflight, both net-new); TokenRequest rotation. | Phases 1–2, parent P5 |

The ordering matters: **Phase 0 delivers the robustness win (validity for all writers
+ no orphaning) with no MCP at all.** The MCP is the ergonomic layer that follows.
A maintainer skeptical of the broker can adopt Phase 0 alone and already be strictly
better off.

---

## Appendix — worked flows (phase-labeled)

**Phase 1 flow (path-routed, no design/preview yet):**
```
agent → storefront_whoami
      ← {namespaces:[agent-analyst], quota:2/3, price:0.0001–1.00, hostnames:*.agents.v1337.org}
agent → storefront_plan_offer {name:funding-feed, type:http, upstream:{…},
        payment:{network:base, payTo:0x…, price:{perRequest:"0.002"}},
        registration:{description:"Predicted funding-rate feed…"}}
      ← {planId:"sha256:…", action:create, approvalRequired:false, findings:[{severity:warning,code:logo-cors}]}
        # findings came from the webhook via --dry-run=server — authoritative, same gate a raw apply hits
agent → storefront_apply_plan {planId}
      ← {applied:true, resumeCovered:true, conditions:[RoutePublished=Unknown]}   # in-cluster record → survives stack up
agent → storefront_offer_status {name:funding-feed}
      ← {RoutePublished:True, PaymentGateReady:True, catalogPublished:true,
         probe:{url:"https://…/services/funding-feed", status:402, ok:true}}      # gated & discoverable — done
```

**Phase 2 adds** (per-offer origin + design + preview):
```
agent → storefront_plan_offer {…, hostname:funding.agents.v1337.org,
        design:{template:data, accent:"#2fe4ab", heroTitle:"Funding Feed"}}
      ← {planId:"…", findings:[]}
agent → storefront_preview_landing {planId}
      ← {landingHtml:"…", openapiJson:{…}, wellKnownX402:{…}}   # exact buyer-visible bytes, pre-apply
agent → storefront_apply_plan {planId}
      ← {applied:true, …}
```

Every step is typed, webhook-validated, diffed, auditable, and (Phase 2) previewable —
and none of it grants a privilege the stack didn't already hand out more dangerously.
