# Agent businesses: rc15 reconciliation, creation-path unification, isolation roadmap

Status: design + integration plan, written 2026-06-11 after reviewing
`release/rc15-integration` (14 commits from c56fee5) against
`feat/stack-export-import` (2 commits from c56fee5). Companion to
`plans/stack-export-import.md`.

## 1. Branch reconciliation: rebase plan

The two branches solved overlapping but mostly complementary problems:

| Problem | rc15-integration | feat/stack-export-import |
|---|---|---|
| Host-reboot offer recovery | ✅ `obol sell resume` + systemd boot unit (`--install-boot-unit`, waitForClusterAPI, RemainAfterExit cgroup fix) | ✗ (stack-up only) |
| Offer ledger covers all types | ✅ type-aware ledger: http bare manifest, agent/demo as v1 List bundles (ns + offer) | partial (agent offers as bare manifests) |
| Ledger truth on update/stop/delete | ✅ refreshPersistedServiceOffer on `sell update/stop`; deleteCRDAgent sweeps offers + ledger; inference tombstones | ✗ |
| Agent CR persistence + replay | ✗ | ✅ `$CONFIG_DIR/agents/<name>.yaml`, `agentcrd.ResumeAll` before offer replay |
| Model config persistence | ✗ | ✅ `internal/model/record.go`, reconciled in stack up after autoConfigureLLM |
| eRPC upstream persistence | ✗ | ✅ `internal/network/record.go`, replayed at stack up |
| Full stack backup/restore | ✗ | ✅ `internal/stackbackup`, `obol stack export/import`, purge prompt |
| Replay-order guard test | ✗ | ✅ `stackup_resume_guard_test.go` |

**Decision: rebase `feat/stack-export-import` onto `release/rc15-integration`,
keeping rc15's entire sell-ledger machinery and layering the feat branch's
record-on-write + stackbackup on top.** rc15's offer work is a superset of the
feat branch's sell-side persistence and includes lifecycle fixes from
adversarial review that the feat branch lacks. The two compose cleanly:
`agentcrd.ResumeAll` re-applies Agent CRs first, then rc15's ledger replays
offers (its List bundles' namespace entries are idempotent no-ops once the
agent exists).

Conflict resolutions (file by file):

1. `cmd/obol/main.go` (stack up action): insert
   `network.ReconcileRecordedRPCs` → `agentcrd.ResumeAll` BEFORE rc15's
   `resumeSellOffers`. Also wire the same two calls into `obol sell resume`'s
   action (reboot recovery must restore agents before offers too — rc15's
   boot unit currently replays offers against possibly-missing Agent CRs;
   this is a real fix to their branch, not just a merge artifact).
2. `cmd/obol/sell_agent.go`: DROP the feat branch's `persistSellHTTPOffer`
   call — rc15 already persists agent offers via
   `persistServiceOffer(...agentOfferBundle(...))`, which additionally
   bundles the namespace (better for the cluster-was-wiped case). Keep
   rc15's version.
3. `cmd/obol/agent.go` `deleteCRDAgent`: keep BOTH cleanups — rc15's
   ServiceOffer CR deletion + ledger sweep, plus feat's
   `agentcrd.RemoveManifest`. `agent update` keeps feat's record refresh
   (rc15 has no agent record at all).
4. `cmd/obol/stackup_resume_guard_test.go`: extend to also assert the
   `sell resume` action replays agents before offers.
5. `CLAUDE.md` / plans: merge (document both `sell resume` and the
   record-on-write layers).
6. `internal/stackbackup`: lands unchanged; its config-dir copy now also
   captures rc15's richer ledger for free. Check `import --cluster-only`
   ordering still holds (it does — it applies agents.json before
   serviceoffers.json from its own harvest).

Sequence: `git rebase release/rc15-integration feat/stack-export-import`,
resolve per the above, full test suite + a stack-recreate smoke (export →
purge → import → up → verify offers + agents return).

## 2. Agent creation paths: inventory and unification

Seven distinct paths exist today, reducible to two render strategies:

| # | Path | Renders | Namespace | Wallet | Agent CR? | Sellable via `sell agent`? |
|---|---|---|---|---|---|---|
| 1 | `stack up` default Hermes (`hermes.SetupDefault`) | host helmfile | `hermes-obol-agent` | host keystore dir | **no** | **no** (no CR to ref) |
| 2 | `agent new` (no name, legacy hermes) | host helmfile | `hermes-<id>` | host keystore dir | no | no |
| 3 | `agent new <name>` (CRD) | in-cluster controller | `agent-<name>` | in-cluster Secret | yes | yes |
| 4 | `openclaw onboard` | host helmfile | `openclaw-<id>` | host keystore / cloud keys | no | no |
| 5 | `agent new --runtime openclaw` | host helmfile | `openclaw-<id>` | same as 4 | no | no |
| 6 | `sell agent <name>` inline create (rc15: TTY create-and-sell) | controller | `agent-<name>` | in-cluster Secret | yes | yes |
| 7 | `sell demo` agent-backed (`runAgentBackedDemo`) | controller | `agent-demo-*` | in-cluster Secret | yes | yes |

(An 8th — autonomous in-cluster spawning via the mother agent's
agent-factory RBAC — is provisioned for in RBAC/admission policy but has no
CLI/controller path yet.)

**The UX trap the user suspected is real, but inverted**: `obol sell agent`
ONLY works for CR-backed agents (paths 3/6/7). The default stack agent and
every legacy-onboarded agent (paths 1/2/4/5) have no Agent CR and cannot be
sold without hand-writing a `spec.upstream` ServiceOffer. So the flagship
agent a user gets from `stack up` is the one agent they can't easily sell.

Unification plan (tech-debt reduction, in order of value):

a. **Make the CRD path canonical.** All new capability lands on the
   controller-rendered path. The record-on-write work (feat branch) already
   makes CR creation effectively declarative: the CLI is now "write a small
   manifest + apply it", and the host file is the replayable source of
   truth. That is the right end-state shape — the imperative command
   becomes sugar over a declared file, which also makes Phase-3 bundles
   (OCI charts containing Agent + ServiceOffer templates) natural.
b. **Migrate the default stack agent to a CR** (`stack up` applies an Agent
   CR named e.g. `obol-agent` instead of running the host-helmfile
   onboard). This deletes the largest duplicated render path (host
   `writeDeploymentFiles` vs controller `agentManifests`), makes the
   default agent sellable, and unifies wallet provisioning on the
   in-cluster Secret model (export/import already handles both). Needs:
   migration story for existing hermes-obol-agent installs (keystore →
   Secret; data PVC re-home or keep namespace name via CR field).
c. **Deprecate paths 2 and 5** (legacy `agent new` variants) with warnings
   pointing at `agent new <name>`; keep `openclaw onboard` as the one
   blessed OpenClaw entry until/unless OpenClaw gets a runtime field on the
   Agent CRD.
d. **Extract shared helpers** flagged in review: V3-keystore generation
   (hermes host vs openclaw in-memory), hermes-config rendering (host
   `generateConfig` vs controller `renderHermesConfig` — two formats today),
   SOUL/skills seeding (duplicated in agent_crd.go and sell_agent.go),
   namespace+CR apply (duplicated in agent_crd.go and sell_agent.go).

Security considerations of the declarative shift: the host manifest store
(`$CONFIG_DIR/agents/`) is applied with the operator's kubeconfig at stack
up, so it adds no new privilege — and the existing
ValidatingAdmissionPolicy still shape-checks every replayed CR (agent-*
namespace pattern, hermes-only runtime, gateway/middleware pinning).
Two real considerations: (1) replay-resurrection — every delete path must
clean its record (both branches do for their own stores; the rebase keeps
both cleanups); (2) the record files inherit ConfigDir trust — same trust
level as kubeconfig itself, which lives next to them.

UX after unification: one mental model — `obol agent new <name>` declares,
`obol sell agent <name>` monetizes, `obol agent update` re-declares, files
under `$CONFIG_DIR/agents/` are the durable truth, `stack up` makes reality
match the files. rc15's inline create-and-sell prompt already collapses the
two commands for the happy path.

## 3. Multiple locked-down agent businesses: gap analysis

Target: N http offers + M agent offers per business, each business in its
own namespace, reachable only via Traefik/x402, allowed egress only to
LiteLLM, eRPC, its own remote-signer; Prometheus scrapes it; nothing else.

What already works:
- Several ServiceOffers CAN target one Agent CR / one upstream today
  (distinct names/paths; per-offer pricing). Per-offer revenue metrics
  exist (`obol_x402_verifier_charged_requests_total` + recording rules).
- Per-namespace remote-signer with per-namespace keystore Secret.
- Strong creation-time guardrails: ValidatingAdmissionPolicy pins gateway,
  ForwardAuth target, namespace label shape; mother-agent RBAC is
  shape-constrained; sub-agent SAs have no cluster bindings.

Critical gaps (ranked):
1. **No NetworkPolicy anywhere** (attempted, reverted on k3d/Flannel due to
   apiserver-endpoint targeting). Today agent A's pod can call agent B's
   hermes API (8642) AND agent B's remote-signer (9000).
2. **Remote-signer REST API has no auth.** Combined with gap 1 this is
   cross-business fund theft. Fix is small: bearer token from an
   in-namespace Secret, injected by the controller into both signer and
   hermes config.
3. **All agents share the LiteLLM master key** (controller copies
   LITELLM_MASTER_KEY into every hermes-config). No per-agent budgets,
   model allowlists, or spend attribution.
4. Per-agent observability: x402-buyer spend metrics are gateway-wide, not
   per-agent; hermes/remote-signer export no metrics.
5. Silent path shadowing: two offers with the same path = first-match-wins
   with no warning. Controller should reject/flag collisions at reconcile.

### The model-routing / cost-management control surface

LiteLLM virtual keys are the natural mechanism and require no new proxy:
- Controller, at Agent reconcile: `POST /key/generate` (with master key)
  → per-agent key with `models: [...]` (allowlist), `max_budget`,
  `budget_duration`, `rpm/tpm` limits; store in agent-ns Secret; render
  into hermes-config instead of the master key. Rotate on demand.
- Agent CRD grows: `spec.model` (exists) + `spec.models` (allowlist),
  `spec.budget {amount, period}`, optional `spec.rateLimit`.
- Spend attribution falls out: LiteLLM tracks per-key spend; surface it via
  LiteLLM's metrics/endpoint into the monitoring stack, and label paid
  upstream traffic per agent where the buyer sidecar is involved.
- `buy inference --agent X` already does per-agent paid-route pinning
  in-pod; with per-agent keys this becomes properly scoped rather than
  cosmetic.

### NetworkPolicy shape (per agent namespace)

- Ingress: from `traefik` ns (Traefik pods) on 8642; from `monitoring` ns
  (Prometheus) on metrics ports; same-namespace traffic (hermes ↔ signer).
- Egress: kube-dns; `llm` ns :4000; `erpc` ns :4000; same-ns signer :9000;
  (optionally `x402` ns :8080). Default-deny otherwise.
- The earlier revert was about the FRONTEND egress policy needing apiserver
  access — agent namespaces don't need the apiserver at all (the mother
  agent in `hermes-obol-agent` does; give IT a wider policy or none
  initially). So the k3d/Flannel apiserver problem doesn't block the agent-
  namespace policies; ship those first.
- Note: mother agent (privileged, factory RBAC, apiserver access) vs
  business agents (no apiserver, locked egress) vs outside agents (pure
  buyers over the tunnel) becomes an explicit three-tier trust model worth
  documenting in CLAUDE.md once implemented.

## 3.5 Decisions from review (2026-06-11)

- **Default agent stays unsellable — feature, not bug.** It is more
  privileged than business agents and shouldn't be sold directly. DROP the
  "migrate default stack agent to a CR" item from unification; keep the
  legacy-path deprecations and shared-helper extraction.
- **Phase 3 / bundles split into a separate pass.** Target home for demo
  "self-contained agent business" bundles: the sibling `../helm-charts`
  repo (ObolNetwork/helm-charts). Write a handoff plan, don't start it.
- **Remote-signer auth**: source lives in the sibling `../remote-signer`
  repo — bearer-token enforcement is a cross-repo change (signer side
  there; token generation/injection in serviceoffercontroller here).
- **LiteLLM virtual keys: proceed** (generous default caps users can
  tighten; imperative CLI + declarative CRD fields underneath). Two checks
  before building: (1) enforcement is LiteLLM-side — hermes only sees
  upstream 4xx/429 when a cap trips; verify hermes fails acceptably and add
  a budget-nearing alert so the cap is backstop, not UX. (2) the
  alternative (hermes-native multi-provider upstream management, dropping
  LiteLLM for sub-agents) was considered and deferred — less control in our
  software; virtual keys keep enforcement in-stack.
- **Buyer-side per-agent spend metrics: deprioritized.** Seller-side prom
  labels only if cheap (per-offer labels already exist).
- **Path collisions: fix.** Preference: reject before anything starts.
  Caveat: ValidatingAdmissionPolicy cannot list other cluster objects, so
  pure-VAP cross-offer uniqueness isn't possible — use CLI preflight
  (list existing offers before apply) + controller Degraded condition as
  the enforcement pair.
- **Egress model: public internet allowed, cluster locked.** Agent
  namespaces may reach the internet (skills fetch URLs etc.) but inside the
  cluster only the allowlist: kube-dns, llm:4000, erpc:4000, own
  remote-signer:9000 (+ x402 verifier if needed). NetworkPolicy shape:
  allow egress to 0.0.0.0/0 with `except` cluster pod/service CIDRs, plus
  explicit in-cluster allows. Ship agent-namespace policies first; frontend
  policy (the k3d/Flannel apiserver revert) stays deferred.
- **Trust tiers: four, not three.** (0) meta agent — Claude Code outside
  the stack, operator-level, interacts via CLI/kubeconfig like a smart
  user; (1) mother agent — in-stack, factory RBAC, apiserver access;
  (2) business agents — no apiserver, locked egress, sellable;
  (3) outside agents — pure buyers over the tunnel. Document in CLAUDE.md
  when B lands.
- **Future (plans-only): secret-injecting egress proxy** alongside seller
  agents (e.g. github.com/ironsh/iron-proxy) so agents are one step removed
  from secrets they could be tricked into exfiltrating — the agent calls
  the proxy, the proxy holds/injects the credential.
- **Known edge — wallet regeneration on replay**: rc15's ledger
  deliberately excludes Agent CRs to avoid wallet orphaning. agentcrd
  ResumeAll re-applies agents with `wallet.create: true`; against a wiped
  cluster WITHOUT a prior `stack import` the controller mints a fresh
  wallet and funds at the old address are stranded (import restores the
  keystore Secret; plain replay doesn't). ResumeAll warns when it re-applies
  a wallet-bearing agent whose keystore Secret is absent.

## 4. Sequenced plan

- **A. Rebase + reconcile (approved, in progress):** rebase feat onto rc15
  per §1, including the agents-before-offers fix to `sell resume` and the
  wallet-regeneration warning in ResumeAll. Outcome: one branch with reboot
  recovery + full record-on-write + export/import.
- **B. Isolation MVP (approved, 2/3 landed 2026-06-11):**
  - ✅ Agent-namespace NetworkPolicy: controller renders `agent-isolation`
    alongside every Agent's primitives (`buildAgentNetworkPolicy`,
    internal/serviceoffercontroller/agent_render.go) + RBAC grant in
    x402.yaml. LIVE-VERIFIED on agent-demo-quant: cross-ns pod → signer
    9000 / hermes 8642 blocked both directions; same-ns signer, LiteLLM,
    eRPC, Traefik, apiserver (post-DNAT via the internet rule — the k3d
    apiserver problem from the frontend revert genuinely doesn't apply),
    and public internet all confirmed working from the agent pod.
    Learnings: NetworkPolicy ports match POST-DNAT pod targetPorts, so
    the Traefik rule is namespace-only (named targetPorts web/websecure
    aren't portably matchable numerically); host-backed Services (ollama →
    host.k3d.internal) ride the internet rule by construction — fine, they
    are host resources. k3s's embedded netpol controller is enabled on the
    stack's k3d (only local-storage/traefik are disabled). NOTE: the
    in-cluster controller image must be rebuilt (next dev `stack up`
    auto-builds) before NEW agents get the policy; demo-quant got it
    hand-applied during verification.
  - ✅ Path collisions: CLI preflight (`preflightOfferPathCollision`,
    cmd/obol/sell.go — wired into sell http, sell http --from-json, sell
    agent) + controller backstop (`findPathConflict`,
    internal/serviceoffercontroller/pathconflict.go —
    PaymentGateReady/RoutePublished=False reason=PathConflict,
    first-claimant-wins by creationTimestamp with ns/name tie-break,
    deletes stale route children, 30s re-poll since no event edge fires
    when the older offer goes away).
  - ✅ Remote-signer bearer auth (implemented 2026-06-11, both repos;
    ACTIVATION PENDING a signer image release):
    - ../remote-signer: `SIGNER__AUTH__TOKEN` (config.rs AuthSettings) +
      route_layer middleware on /api/v1/* (router.rs; constant-time
      compare, health endpoints exempt, unset = auth-disabled
      back-compat). 3 new integration tests; 18/18 pass.
    - obol-stack: keystore Secret gains an `authToken` key (fresh mints +
      one-shot backfill for pre-auth Secrets, never rotated by reconcile);
      signer Deployment gets SIGNER__AUTH__TOKEN and the hermes
      Deployment gets REMOTE_SIGNER_TOKEN, both via OPTIONAL secretKeyRefs
      so pre-backfill Secrets and wallet-less agents can't wedge pod
      startup; signer.py attaches `Authorization: Bearer` when the env is
      set (buy.py rides its helpers; coordinate.py declares but never
      calls the signer directly); remote-signer-api.md documents auth.
    - Rollout: release a remote-signer image with the auth code, bump
      `remoteSignerImage` in agent_wallet.go (currently v0.3.0 — ignores
      the env, so the stack-side injection is live-safe today), and the
      enforcement turns on per-agent with no further migration.
    - Follow-up: host-rendered signers (master hermes + openclaw, chart
      from ../helm-charts) need the same env plumbed through
      values-remote-signer.yaml + chart — until then those signers stay
      auth-disabled (token env absent = back-compat path).
- **C. Cost/control surface (approved with checks):** per-agent LiteLLM
  virtual keys with generous default budget + model allowlist via new Agent
  CRD fields; verify hermes behavior on cap-trip first; budget-nearing
  alert; seller-side metric labels only if cheap.
- **D. Unification (trimmed):** deprecate legacy onboard paths; extract
  shared keystore/config-render/seed helpers. Default agent stays
  host-rendered and unsellable by design.
- **E. Sellable bundles (separate pass, plan-only handoff):** OCI charts of
  Agent CR + ServiceOffer templates, demo "agent business" bundles living
  in ../helm-charts. Enabled by A (declared files) + C (budgets make shared
  bundles safe). Include the iron-proxy secret-isolation idea in that
  pass's design.

Suggested review boundaries when implementing B/C (per repo convention):
"agent A pod cannot reach agent B signer/API (NetworkPolicy denies, test via
in-cluster curl)", "signer rejects unauthenticated sign requests",
"controller never writes the master key into an agent namespace",
"per-agent key cannot exceed CR budget", "two offers with colliding paths →
second marked Degraded, not silently shadowed".
