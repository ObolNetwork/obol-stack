# Step E (dedicated): sellable agent-business charts in ../helm-charts

Status: plan, written 2026-06-11. Spun out of
plans/agent-business-architecture.md §E. A separate pass; depends on
Steps A–C landing first (declared files, isolation, virtual keys).

## Goal

Let an operator stand up a coherent, locked-down "agent business" — one
agent (or upstream) in one namespace, fronted by several ServiceOffers — in
one command, then refine it through their master agent:

```
obol app install obol/eth-data-desk        # fetch chart, write editable values
#   edit payTo + prices (or be prompted)
obol app sync eth-data-desk/<id>           # controller materialises agent + offers
#   then iterate conversationally via the master agent:
#   "raise the AMA price to 0.05 and drop the gas offer"
#   → master agent edits the ServiceOffers (it has factory RBAC), they reconcile
```

## Why this is mostly chart-authoring, not new stack code

The pieces already exist:

- **CRDs ship with the stack** (agent-crd, serviceoffer-crd,
  registrationrequest-crd, agentidentity-crd, purchaserequest-crd in
  internal/embed/infrastructure/base/templates/). So a chart installed into
  an Obol Stack can ASSUME the CRDs are present — it ships none of its own
  (avoids CRD-ownership fights with the stack on upgrade). These charts are
  Obol-Stack-only by definition; document that, guard NOTES.txt with a
  CRD-presence check.
- **`obol app install` already does the install** (internal/app): resolves
  OCI/repo/URL refs, fetches default values, writes an editable
  `values.yaml` + helmfile to `$CONFIG_DIR/applications/<app>/<id>/`,
  `helmfile sync` into namespace `<app>-<id>`. A business chart is just a
  chart — no new install path needed for v1.
- **The master agent has factory RBAC** (obol-agent-monetize-rbac.yaml:
  CRUD serviceoffers, create agents/namespaces/scoped secrets). So "refine
  the business via the agent" is already authorized — it edits the
  ServiceOffer CRs the chart created.
- **Record-on-write + resume (Phases A/B)** mean the Agent + ServiceOffer
  CRs the chart produces are host-recorded and survive cluster recreation,
  and the install's helmfile re-syncs via `obol app sync`. A business is
  durable through machinery we already built.

A business chart's actual job is therefore thin and declarative: render
**one Agent CR + N ServiceOffer CRs** (and any owned http upstream
Deployments/Services). The in-cluster controller materialises the
deployment, signer, NetworkPolicy, payment gates, routes, and registration
from those CRs. The chart declares intent; the controller does the work.

## Chart architecture: a library base + concrete businesses

Helm library chart (`type: library`) is the right mechanism for "a base
others inherit" — helm-charts has none today; `obol-app` (runs arbitrary
images in the stack, has a values.schema.json) is the closest precedent and
the structural template to copy.

- **`obol-agent-business`** (library chart): named templates
  - `obol-agent-business.agent` → an Agent CR with safe defaults baked in:
    `wallet.create: true`, `egress.proxy: true` (Step C3 — public-facing
    agents hold no secrets), a generous default `budget` + `models`
    allowlist (Step C1) the operator tightens, skills/objective from values.
  - `obol-agent-business.agentOffer` → a `type: agent` ServiceOffer
    pointing at the agent's ref (price/path/token/registration from values).
  - `obol-agent-business.httpOffer` → a `type: http` ServiceOffer + the
    upstream Deployment/Service it fronts.
  - shared helpers: namespace/labels, payTo plumbing (required value, no
    default), per-offer path derivation (must be collision-free — the
    Step B preflight + controller PathConflict condition backstop this).
  Concrete charts add it as a `dependencies:` entry and call the templates,
  so every business inherits the same isolation + cost defaults while
  declaring its own offers. Bumping the library propagates good defaults to
  all businesses.

- **Concrete business charts** depend on the library and supply values.

## Two exemplary businesses to ship (+ a minimal starter)

Grounded in skills that already exist in internal/embed/skills/.

1. **`gas-oracle`** (minimal, http-only — the first quickstart): no agent,
   one `type: http` ServiceOffer fronting a tiny deterministic endpoint
   (gas price from eRPC). Demonstrates the simplest possible business and
   the http-offer path with zero LLM cost. Good "hello world".

2. **`eth-data-desk`** (the multi-offer showcase): one Agent CR pinned to
   chain-reading skills (addresses, gas, ethereum-networks, indexing) in
   one namespace, fronted by SEVERAL offers — exactly the
   "N offers → one agent/namespace" shape the user wants:
   - a `type: agent` AMA offer (ask anything about chain state, per-request
     LLM price),
   - a `type: http` cheap lookup offer (address labels / gas oracle) for
     non-LLM calls at a lower price.
   Shows agent+http mixed in one namespace, per-offer pricing, and the
   path-collision guard doing real work.

3. **`research-desk`** (premium single-offer): an autoresearch-coordinator
   agent selling one `type: agent` deep-research offer — higher price,
   longer `maxTimeoutSeconds`, budget caps tuned for expensive multi-step
   work (and a real test of the streaming-through-Cloudflare-100s
   discipline). Shows budget/model caps mattering.

## Gaps to close (small, scoped)

1. **payTo at install time.** A business needs a recipient; there's no safe
   default. v1: `values.schema.json` marks `payTo` required (install fails
   the schema check until set), operator edits `values.yaml` before
   `app sync`. v2 (enhancement, flagged in the main architecture doc):
   a thin `obol app install --set payTo=…` or an install-time prompt
   wrapper that also offers wallet provisioning.
2. **CRD-presence guard.** NOTES.txt / a pre-install check that fails
   clearly outside an Obol Stack ("this chart requires the Obol Stack CRDs;
   install into an `obol stack`").
3. **"Refine via master agent" skill.** A small skill/runbook that teaches
   the master agent to enumerate a business's ServiceOffers and patch them
   (price, path, drainAt, skills) — using the factory RBAC it already has.
   This is what turns a static chart into a living business the operator
   steers conversationally. Likely an embedded skill update, not a chart.
4. **Discoverability.** `obol app install obol/<business>` implies an
   `obol` ArtifactHub repo / OCI namespace publishing these. helm-charts
   already releases to ArtifactHub (badge in README) — add the business
   charts to that pipeline; ghcr OCI is the alternative the main doc noted.

## Quickstart guide (docs deliverable)

A docs/ guide per business + one overview:
1. `obol app install obol/eth-data-desk`
2. set `payTo` (+ optionally tighten prices/budget/models) in the written
   values.yaml
3. `obol app sync eth-data-desk/<id>`
4. `obol sell list` to see the offers go Ready; `obol sell register` if
   on-chain identity is wanted
5. iterate: ask the master agent to refine the offers; changes reconcile
   live and are host-recorded so they survive a recreate.

## Sequencing

Strictly after A–C: the charts should ship the Step C defaults
(`egress.proxy`, budget, model allowlist) as the safe baseline for
public-facing agents, or they'd stand up under-secured businesses. Order:
library chart + gas-oracle (proves the pipeline) → eth-data-desk (the
multi-offer showcase) → research-desk → refine-skill + quickstart docs.

## Review boundaries when building

- "business chart renders only CRs (+ owned http upstreams), never its own
  copies of the stack CRDs".
- "every rendered offer has a distinct path (chart-level helper) — no
  self-collision within a business".
- "agent CR carries egress.proxy + budget + model allowlist by default".
- "install fails clearly without payTo set, and outside an Obol Stack".
- "master-agent refine skill edits ServiceOffers within its existing RBAC —
  no new cluster-wide grants".
