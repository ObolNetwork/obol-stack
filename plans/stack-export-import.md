# Stack-wide export/import + sellable service packaging — design review

Status: Phase 1 implemented 2026-06-10 (`internal/stackbackup/`, `obol stack
export|import`, purge-time full-backup prompt). Phases 2–3 not yet scheduled.

Implementation findings (2026-06-10):

- `obol sell http`/`sell inference` offers were ALREADY persisted host-side
  (`<ConfigDir>/sell-http/`, sell-inference descriptors) and replayed by
  `resumeSellOffers` after `stack up` — part of Phase 2 predates this plan.
  The config component captures those stores for free; `sell agent`/`sell
  mcp`, `obol model`, and `obol network add` mutations remain etcd/CM-only
  and are covered by the cluster-harvest component until record-on-write
  lands for them.
- Brains are captured by quiescing agent deployments (scale to 0, wait on
  the deployments' own status — NOT all pods in the namespace; the
  remote-signer never terminates) and streaming the host data dirs into the
  tar. This avoids any dependency on the hermes in-pod backup CLI and
  yields a clean-shutdown SQLite state (`.clean_shutdown` present in the
  restored copy during smoke testing).
- CRD-agent namespaces keep Opaque secrets (incl. remote-signer material)
  cluster-side only; the cluster component harvests them per agent-*
  namespace (validated: 2 secrets from agent-demo-quant).
- Smoke test on the live dev cluster surfaced pitfall 16 in a new form:
  any chart re-render on a pre-rc13 cluster swaps the root-chown init for
  uid-1000 + fsGroup and the old 10000-owned volume kills the pod
  (`Init:CrashLoopBackOff`, `mkdir /data/.hermes: Permission denied`).
  Non-destructive recovery used here: `docker exec <k3d-node> chown -R
  1000:1000 /data/<ns>/hermes-data` + delete pod.

## Problem

1. We only back up private keys, and only sometimes: `obol agent wallet backup/restore`
   exists for Hermes and OpenClaw, and `obol stack purge --force` prompts for a wallet
   backup — but **only for OpenClaw** (`internal/openclaw/wallet_backup.go:464`,
   `PromptBackupBeforePurge`; Hermes has no equivalent). Agent brains, etcd-resident
   CRs, and imperatively-mutated ConfigMaps are all lost on cluster recreation.
2. We've drifted from "everything is in helmfiles": `obol sell`, `obol buy`, `obol
   model setup`, `obol network add` all create or mutate cluster state that has no
   host-side source of truth.
3. Separately: we want a way to package a *working seller setup* so one operator can
   share it with another as a good starting point.

## State inventory (what lives where, what survives)

| State | Location | Created by | Survives `down` | Survives recreate | Restorable today |
|---|---|---|---|---|---|
| Wallet keystores | `$DATA_DIR/<ns>/remote-signer-keystores/` + password in `values-remote-signer.yaml` | agent onboard | yes | path-dependent | yes — `agent wallet backup/restore` (`internal/walletbackup`) |
| Agent brains (sessions, memory, `state.db`, workspace, skills) | PVC backed by `$DATA_DIR/<ns>/hermes-data/.hermes` | Hermes pod | yes (host dir) | fragile (PV rebinding, UID/ownership — see rc12→v0.10 hostPath issue) | partially — `hermes snapshot`/`hermes backup` exist in-pod, but obol doesn't wrap extraction to host |
| ServiceOffer CRs | etcd only | `obol sell *` | no | no | **no** — nothing on disk |
| RegistrationRequest CRs | etcd only | sell/register flow | no | no | **no** |
| Agent CRs | etcd + host seed files (`applications/agent/<name>/`, SOUL.md, skills) | `obol agent new` | seeds: yes | seeds: yes | mostly — re-apply from host seeds |
| PurchaseRequest CRs + buyer auth CMs | etcd only (`llm` ns CMs) | buy.py / controller | no | no | no — but auths are time-boxed (validBefore), mostly worthless after restore anyway |
| litellm-config model_list | CM in `llm` ns | stack up auto-config + `obol model setup/prefer`, `buy inference` | no | partially (`preserveLiteLLMConfigForHelm`, `internal/stack/stack.go:1442`, preserves across `stack up` but not recreate) | no |
| litellm-secrets (provider API keys) | Secret in `llm` ns | `obol model setup` | no | no | no |
| eRPC upstreams (remote RPCs, write-routing) | CM in `erpc` ns | `obol network add/remove` | no | no | no |
| hermes-config model preference | CM, mutated in-pod by `buy inference --agent` | no | no | no |
| Host config dir (helmfiles, values, wallet.json metadata, stack id) | `$CONFIG_DIR` | init/onboard/install | yes | n/a (it *is* the source) | yes — `agent/network/app sync` re-renders |
| Tunnel credentials (persistent tunnel) | host config / CF | `obol tunnel setup` | yes | yes | yes |
| Chain data | network PVCs | clients | yes | fragile | re-syncable; exclude from backup by default |

Two distinct failure classes:

- **Class A — no source of truth anywhere** (etcd-only CRs, mutated CMs/Secrets).
  Architectural drift; fix is record-on-write + harvest-on-export.
- **Class B — source exists but no packaging** (brains, keystores, config dir).
  Fix is an orchestrated archive.

## Proposal

### Phase 1 — `obol stack export` / `obol stack import`

One archive (`obol-stack-backup-<stackid>-<ts>.tar.gz`, optionally whole-archive
AES-256-GCM-encrypted reusing the `internal/walletbackup` scheme) with a versioned
`manifest.json` (archive version, obol version, stack id, component list, per-component
versions). Composed of independent exporters behind a common interface:

```go
type Component interface {
    Name() string
    Export(ctx context.Context, cfg config.Config, dir string) (*ComponentManifest, error)
    Import(ctx context.Context, cfg config.Config, dir string, opts ImportOpts) error
}
```

Components, in export order:

1. **wallets** — all instances across hermes/openclaw/CRD agents, existing
   `walletbackup.File` wire format embedded per instance. Wallet section is always
   encrypted even if the rest of the archive isn't.
2. **brains** — per agent. Cluster running: exec the runtime's own WAL-safe backup
   (`hermes backup` → zip in pod) and `kubectl cp` it out — never tar a live
   `state.db` from the host (WAL torn-state). Cluster down: tar
   `$DATA_DIR/<ns>/hermes-data` from host with a "best-effort, cluster was down"
   manifest flag. OpenClaw: same pattern against `/data/.openclaw`.
3. **cluster-resources** — `kubectl get -o yaml`, stripped of
   `resourceVersion`/`uid`/`creationTimestamp`/`managedFields`/`status`:
   ServiceOffers, RegistrationRequests, Agent CRs, `litellm-config`,
   `litellm-secrets`, eRPC CM, `x402-pricing`. PurchaseRequests + buyer auth CMs:
   **skip by default** (pre-signed auths expire via `validBefore`; the supported
   recovery is re-running `buy.py buy`/`process` after restore), `--include
   purchases` to override.
4. **host-config** — `$CONFIG_DIR/applications/`, `$CONFIG_DIR/networks/`
   (values.yaml etc.), `.stack-id`, tunnel credentials. Cheap; makes import able to
   re-render via existing `sync` paths.

Excluded always: chain data, registry cache, kubeconfig (regenerated).

**Import** (idempotent, resumable, per-component `--only`/`--skip`):

1. Restore host-config first — including `.stack-id`, so namespaces/petnames and
   `$DATA_DIR` paths line up with the archive.
2. `obol stack init` (no-op if config restored) → `obol stack up`.
3. Restore wallets (existing restore path: keystore + Secret + signer restart).
4. Restore brains: scale agent deployment to 0 → write data (or run `hermes import
   <zip> --force` in-pod) → scale up. Fix ownership for non-root pods (UID 1000).
5. Apply cluster-resources in dependency order: Agent CRs → wait Ready →
   litellm/eRPC CMs+Secrets → ServiceOffers → controller reconciles middleware,
   routes, registration.
6. Print a report: restored / skipped / needs-manual (e.g. "re-buy paid inference",
   "re-register ERC-8004 if hostname changed").

**Purge integration**: `obol stack purge` prompts to run a full `stack export` (not
just OpenClaw wallets), TTY-gated like today. This alone would have saved the lost
agent brains.

### Phase 2 — record-on-write (close the drift)

The deeper fix for Class A: every CR/CM mutation the CLI makes also writes its
manifest to the host, mirroring what `applications/<app>/<id>/helmfile.yaml` already
does for apps:

- `obol sell *` → write the ServiceOffer manifest to
  `$CONFIG_DIR/applications/offers/<ns>/<name>.yaml` (delete on `sell delete`).
- `obol model setup/prefer/remove` → persist desired model_list +
  provider-key *references* (not the keys themselves) host-side.
- `obol network add/remove` → persist eRPC upstream spec host-side.

Then `obol stack up` can reconcile these on cluster creation, `stack export` becomes
mostly "tar the config dir", and we're back to "host disk is the source of truth" —
the property we lost when we drifted from helmfiles. Phase 1's live-cluster harvest
stays as belt-and-braces for anything created before Phase 2 or out-of-band.

### Phase 3 — sellable service packages

**Keep packaging separate from backup.** A backup is *my stack* (keys, brains,
secrets — must be encrypted, never shared). A package is a *parameterized template*
(must contain no keys, no pay-to address, no brain).

Answer to "could it be helmfiles": **yes, mostly.** Agent CRs and ServiceOffers are
plain K8s manifests; a Helm chart can template both, and `obol app install` already
resolves ArtifactHub/URL/OCI chart refs and renders a per-instance helmfile
(`internal/app/app.go`). Note `obol-packages` (the repo) is the TypeScript SDK
monorepo — not a distribution vehicle for this; OCI (ghcr.io) via existing helm
machinery is.

A **seller bundle** = OCI-published Helm chart containing:

- `templates/agent.yaml` — Agent CR (skills, objective, model as values)
- `templates/serviceoffer.yaml` — ServiceOffer (price, token, chain, **payTo as a
  required value with no default**)
- optional upstream subcharts (e.g. the thing being sold)
- `values.schema.json` so install can prompt/validate

Gaps vs today's `obol app install`: it assumes one chart/one namespace and has no
install-time parameter prompting or wallet provisioning. So add a thin wrapper —
either extend `app install` or add `obol sell install <oci-ref>` — that prompts for
pay-to/price/model (or `--set`), optionally provisions a wallet
(`--create-wallet` reusing agent-CR wallet flow), then helmfile-syncs.

Bridge from backup to packaging: `obol sell export <name>` — take a live
ServiceOffer (+ linked Agent CR), strip identity (payTo, wallet, registration,
provenance), parameterize, and emit a chart skeleton. "Share my working setup"
becomes: run it, push to ghcr, send the ref.

## Edge cases to design for

- **SQLite WAL**: never copy a live `state.db` file; use the runtime's backup API.
- **Pre-signed auths**: time-boxed; restoring them is usually restoring garbage.
  On-chain nonces prevent double-spend, so it's safe-but-stale. Default skip.
- **ERC-8004 / hostname coupling**: on-chain registration is keyed to the tunnel
  domain. Import on a new hostname ⇒ flag "re-run `obol sell register`".
- **Stack identity**: namespaces embed petnames; data-dir paths embed namespaces.
  Import must restore `.stack-id` and instance ids before `stack up`, or nothing
  lines up.
- **Ownership/UID**: v0.10 non-root pods (UID 1000); restored brain files need
  correct ownership (the rc12→v0.10 hostPath/fsGroup issue is the cautionary tale).
- **Version skew**: manifest carries archive + obol versions; import refuses or
  migrates on mismatch. Brains additionally carry the hermes version.
- **Secrets hygiene**: archive contains provider API keys, keystore passwords,
  tunnel creds. Encrypt whole archive by default; require explicit `--no-encrypt`.
- **Size**: brains/workspaces can be large; hermes backup already excludes caches —
  surface per-component sizes in the export summary.
- **Partial failure on import**: per-component checkpointing in a state file so a
  failed import can resume rather than half-restore twice.

## Suggested sequencing

1. **Now (small, high value)**: purge-time full-export prompt covering Hermes (the
   exact loss that already happened), and `stack export/import` v1 with the four
   components above. Reuses `walletbackup`, `hermes backup/import`, `kubectl cp`,
   existing sync paths — mostly orchestration, little new machinery.
2. **Next**: record-on-write for `sell`/`model`/`network` mutations + `stack up`
   reconciliation. Shrinks export to "config dir + brains + wallets" and re-earns
   the helmfile-era property.
3. **Then**: seller bundles as OCI helm charts + `obol sell export` generator.
   Cleanly downstream of 1–2 because the manifest inventory and host-side
   persistence are exactly what a bundle template is generated from.
