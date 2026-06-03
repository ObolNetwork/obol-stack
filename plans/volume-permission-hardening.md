# Cross-Platform Kubernetes Volume-Permission Hardening Plan — obol-stack

**Scope:** k3d (macOS) and native k3s (Linux). **Trigger:** recurring permission failures; concrete reproducer = demo-quant spawned agent stuck `Provisioning` because its Deployment lacks the root chown init container the master Hermes has.

**Verification basis:** all file:line references below were re-read against the working tree, and the core mechanism was proven empirically on the live cluster's real Linux filesystem (see "Empirical validation" below).

---

## 0. TL;DR — what shipped in this PR + the missing root-cause piece

### The decisive finding the static audit missed: `fakeowner`

The bug is **masked on k3d/macOS and bites on k3s/Linux** — which is exactly why it has been intermittent and hard to pin down. Inside the k3d node, the data dir mounts as:

```
/run/host_mark/Users  /data  fakeowner  rw,nosuid,nodev,relatime,fakeowner
```

`fakeowner` is Docker Desktop's VirtioFS layer. It **fakes ownership and does not enforce POSIX permission checks** — a non-root pod (UID 10000) can `mkdir` inside a `1000:1000`/`0755` dir regardless of UID. So on the developer's Mac everything "works." On native k3s/Linux, on CI, on remote QA boxes (the DGX Sparks), and on any k3d backend where the data dir is a real volume rather than a Docker-Desktop bind mount, `/data` is a real ext4/overlay filesystem that **does** enforce ownership — and the same workload fails with `Permission denied`. This is the "plagued with permissions issues all the time" pattern: green locally, red everywhere that matters.

`fsGroup` is genuinely a no-op on these `hostPath`-typed local-path PVs in *both* environments (verified: the kubelet left a volume root at `1000:1000` despite `fsGroup:10000` + `fsGroupChangePolicy:OnRootMismatch`). What varies between environments is only whether the *underlying filesystem enforces* the ownership the provisioner set — never whether `fsGroup` rescues it.

### Empirical validation (live k3d node, real Linux overlay fs via `docker exec -u`)

| Case | Result |
|---|---|
| Controller-shaped pod (UID 10000) **with** root chown init, on local-path PVC | reached `Running`; volume cleanly owned `10000:10000` |
| UID 10000 `mkdir` on a `1000:1000`/`0755` dir (real Linux fs) | `Permission denied` — **the exact demo-quant crash** |
| Same, **after** `chown -R 10000:10000` (the fix) | `mkdir` succeeded — pod starts normally |
| x402-buyer **UID 1000** writing a `1000:1000`/`2775` state dir | wrote `consumed.json` — **buyer fix works** |
| x402-buyer **UID 65532** (old behavior) on `1000:1000` dir | `Permission denied` — **proves the buyer was latent-broken on Linux** |

### Changes shipped

| Change | File | Why |
|---|---|---|
| New shared helper `RootChownInitContainer` / `ChownCommand` | `internal/k8sperm/chowninit.go` (+test) | single source of truth; stops the master/spawned paths drifting again |
| Root chown init on spawned agents (P0, demo-quant) | `internal/serviceoffercontroller/agent_render.go` | spawned agents now match master Hermes; controller is in-cluster and cannot host-chown |
| Fix the test that *enforced* the bug (count 1→2) | `internal/serviceoffercontroller/agent_render_test.go` | locks the chown init in, asserts it runs before `profile-seed` |
| x402-buyer runs as UID/GID 1000 (P0) | `internal/embed/infrastructure/base/templates/llm.yaml` | restricted-PSS-safe: aligns with the provisioner owner (root init forbidden in `llm`) |
| Assert buyer UID + fix stale comment | `internal/embed/embed_buyer_state_test.go`, `llm.yaml` | guards the regression; comment claimed emptyDir, it's a PVC since `5c9a879e` |
| Provisioner volumes group-writable + setgid (`0775`/`2775`) (P1) | `internal/embed/infrastructure/base/templates/local-path.yaml` (+test) | forward-compat broad net; lets GID-1000 workloads write without a root init |

### Deferred (documented, not blindly changed)

- **Master Hermes refactor onto the shared helper** — it already has a correct, test-pinned inline root chown init (`internal/hermes/hermes.go`). It renders YAML via `fmt.Sprintf`, not `map[string]any`, so converting it is a larger, riskier change with no behavior gain. The shared helper now exists for it to adopt later.
- **OpenClaw** — image default UID is 1000, which *coincidentally matches* the provisioner, so it works on both backends today. An explicit `runAsUser/runAsGroup: 1000` pin would make that robustness intentional rather than accidental; deferred to avoid touching the optional runtime untested.
- **Aztec network** (`aztecprotocol/aztec`) — image UID unknown; flagged HIGH-risk-if-non-root but NOT changed blind, as a wrong securityContext would break the chart. Needs an image-UID check first. Ethereum/consensus clients self-heal via the upstream chart's `initChownData` root init.

---

## 1. Root cause

obol-stack persists workload state on `rancher/local-path-provisioner:v0.0.30` (`internal/embed/infrastructure/base/templates/local-path.yaml:45`, `:110`). The provisioner is the only thing that touches a freshly-provisioned volume's ownership, and it does so with a **hardcoded** chown in its embedded setup script:

```
internal/embed/infrastructure/base/templates/local-path.yaml:28-32
  mkdir -m 0755 -p "${VOL_DIR}"
  chown -R 1000:1000 "${VOL_DIR}"
```

Four facts collide to produce the bug:

1. **Hardcoded provisioner target = `1000:1000`.** Every fresh PVC lands owned by UID/GID 1000, inside the k3d/k3s node, on *both* backends. There is no override annotation anywhere in the tree (verified: no `volumeType`/`defaultVolumeType` annotation on the StorageClass or any PVC), so the provisioner creates the PV as the v0.0.30 default `hostPath` type.

2. **`fsGroup` is a genuine no-op on these volumes.** For `hostPath`-typed PVs the in-tree kubelet Mounter returns `Managed: false`, so the kubelet skips `SetVolumeOwnership` entirely — `fsGroup` and `fsGroupChangePolicy: OnRootMismatch` never run. This holds identically on k3d-macOS and native k3s-Linux (same kubelet binary). (Adversarial verdict #1, *supported*. Precision caveat: `fsGroup` *would* work if anyone annotated the SC/PVC `volumeType: local`, but nothing does, so the no-op is real here.)

3. **Per-workload UID diversity.** The workloads do **not** agree on a UID:
   - local-path provisioner target: **1000**
   - OpenClaw: **1000** (USER node) — happens to match
   - Hermes (master + spawned): **10000**
   - x402-buyer / verifier / controller / litellm: **65532** (distroless nonroot)
   - eRPC: **10001**; frontend/storefront: **1001**; ethereum clients: **10001**

   Only OpenClaw's 1000 coincidentally matches the provisioner. Everything else that writes a fresh local-path PVC and runs at a different UID **cannot write it** unless something re-owns the directory.

4. **Inconsistent root-chown-init usage.** The *only* deterministic remediation that works across both platforms is a root-privileged init container that runs `chown -R <uid>:<gid> /data` before the main container starts. Master Hermes has it (`internal/hermes/hermes.go:783-803`, `init-hermes-perms`, `runAsUser:0`, `chown -R 10000:10000 /data`) and a test pins it. But the *spawned agent* path renders only a non-root `profile-seed` init (`internal/serviceoffercontroller/agent_render.go:234-276`, inherits pod UID 10000), and a test (`agent_render_test.go:201`) actively *locks the count to exactly 1*, forbidding a root chown. The x402-buyer sidecar writes a local-path PVC at UID 65532 with no chown at all.

**Why it bites cross-platform specifically:** the host-side CLI repair functions that paper over this on the developer's Mac are all **k3d-gated and chown to the *host* user**, not the pod UID:
- `internal/hermes/hermes.go:1300-1317` (`ensureVolumeWritable`) → `if backendName != "k3d" { return }`, chowns to `os.Getuid()/os.Getgid()`.
- `internal/openclaw/openclaw.go:822-840` (`ensureVolumeWritable`) → same k3d gate, host UID.
- `internal/hermes/hermes.go:1319-1334` (`fixRuntimeVolumeOwnership`) → on k3s does a bare `os.Chown` that silently no-ops unless the CLI runs as root.

So on k3s-Linux, the host-side safety net evaporates (no docker exec, CLI usually not root) and the **in-pod root chown init container becomes the *only* line of defense**. The demo-quant agent and the x402-buyer have *no* root init container, so they break the moment fsGroup is the only mechanism left — which is always, on local-path. The controller is distroless and in-cluster: it has **zero** `chown`/`docker`/`exec.Command` calls (grep-clean), so it cannot repair host ownership the way the CLI does for master Hermes. The former `obol agent repair-perms` CLI workaround was removed from the tree.

This is the exact regression history in adversarial verdict #3: PR #514 removed the Hermes root chown init, the 10000 container hit `mkdir: /data/.hermes/home: Permission denied` and crashlooped, and commit `eb985bdb` re-added the deterministic root chown. The spawned-agent path never got the same fix — that is demo-quant.

---

## 2. The uniform mechanism

### Recommendation: a shared root-chown-init helper for non-PSS-restricted namespaces, + UID alignment for the two PSS-restricted namespaces. Do NOT try to make one trick cover both.

The PSS constraint forces a two-track answer (adversarial verdict #2, *supported*):

- **`x402` and `llm` enforce `pod-security.kubernetes.io/enforce: restricted`** (`x402.yaml:9-18`, `llm.yaml:24-32`). A `runAsUser:0` init container is **rejected at admission** there. Root chown is **forbidden**.
- **`hermes-obol-agent`, `agent-<name>`, `openclaw-<id>` carry no PSS labels** and the cluster default enforce level is `privileged` (no AdmissionConfiguration in `k3d-config.yaml`). Root chown is **allowed**.

#### Track A — non-restricted namespaces (agents, openclaw, signer): one shared root-chown-init helper

Add a single reusable helper that emits a root chown init container and apply it consistently to **master Hermes, spawned agents, OpenClaw, and any future PVC writer in an unlabeled namespace**. This is the bulletproof path because it is platform-independent: it runs *inside the pod on every start*, before the main container, regardless of k3d vs k3s, regardless of what the provisioner chowned to, regardless of volumeType. Master Hermes already proves this works in production. The helper just removes the per-workload copy-paste drift that let the spawned-agent path fall behind.

**Why this over the alternatives for Track A:**
- *fsGroup alone* — rejected. Proven no-op on hostPath local-path; this is the exact thing #514 regressed on.
- *Aligning every workload to UID 1000 to match the provisioner* — rejected for agents. Hermes is deliberately 10000 and that's pinned by tests + the security model (`TestGenerateValues_UsesHermesNativeNames`, `TestAgentManifests_DeploymentUsesFSGroup`). Re-UID'ing Hermes to 1000 is a larger, riskier blast radius than adding an init container, and would still leave the buyer (65532) broken.
- *Host-side CLI chown* — rejected as the *primary* mechanism. It's k3d-only and chowns to the host user, not the pod UID; it does nothing durable on k3s and nothing at all for controller-spawned agents. Keep it strictly as the existing best-effort k3d fallback, not the load-bearing fix.

#### Track B — PSS-restricted namespaces (`llm`, `x402`): align UID with what the provisioner gives, no root init

Root chown is forbidden here, so the durable fix is to make the workload's writable-PVC UID match the provisioner's `1000:1000`, OR make the provisioner's chown target configurable and set it to the workload UID.

- **`x402-verifier`, `serviceoffer-controller`** — `risk: none`, no PVC. No change.
- **`litellm` main container** — `risk: none`, only emptyDir. No change.
- **`x402-buyer` sidecar (HIGH, currently broken)** — runs at 65532, writes `x402-buyer-state` local-path PVC. The provisioner chowns it `1000:1000`, fsGroup is a no-op, 65532 can't write. The container's own comment (`llm.yaml:305-307`) still claims `/state` "is already an emptyDir mount" — a stale comment that predates commit `5c9a879e` converting it to a PVC, and is direct evidence the conversion shipped without re-checking ownership. **Fix without breaking restricted PSS:** make the local-path provisioner setup script chown to a *shared group* and `chmod g+ws`, then give the buyer `fsGroup: 65532` won't help (no-op). The clean restricted-safe option is **(b1)**: change the provisioner setup script to `chown -R 0:0` + `chmod -R 0777` *only if* we want universal writability (too loose), OR **(b2, recommended)**: add a dedicated tiny local-path PVC chowned to 65532 via a **configurable provisioner setup**, OR **(b3, simplest & recommended)**: keep the buyer state on an `emptyDir` and accept the documented re-spend-on-restart tradeoff was the *reason* for the PVC — so instead **make the provisioner setup script chmod `0777` on the buyer's path only** is not possible (path-pattern is global).

  **Decision for the buyer:** the least-invasive restricted-safe fix is to **make the local-path provisioner setup script group-writable and add a supplemental group**, but because the provisioner setup is global and path-blind, the pragmatic, shippable choice is **(c)** below.

#### (c) Make the local-path provisioner setup configurable + group-writable — the cross-cutting hardening that helps both tracks

Change `local-path.yaml:28-32` from a hardcoded `chown -R 1000:1000` + `0755` to a **group-writable** mode so any fsGroup-honoring path (CSI/`local`-type, or future real-K8s) and any workload sharing the GID can write:

```
mkdir -m 0775 -p "${VOL_DIR}"
chown -R 1000:1000 "${VOL_DIR}"
chmod -R g+ws "${VOL_DIR}"
```

This is defense-in-depth: it does **not** fix the hostPath-no-op-on-65532 case by itself (65532 is neither owner nor in group 1000), but it (i) removes fragility for any workload that can be given `runAsGroup: 1000` / a supplemental group 1000, and (ii) makes the `local`-volumeType migration path (where fsGroup *does* work) immediately correct. The truly bulletproof buyer fix remains Track-A-style root chown — **which we can't do under restricted PSS** — so the recommended buyer remediation is: **set `runAsGroup` and `fsGroup` to 1000 and run the buyer as UID 1000** (it's a Go static binary; UID is set by pod securityContext, the distroless image default is overridable), letting it match the provisioner's `1000:1000`. That keeps restricted PSS satisfied (non-zero UID, no privilege escalation) and needs no root init.

#### (d) fsGroup + OnRootMismatch as defense-in-depth where honored

Keep `fsGroup` + `fsGroupChangePolicy: OnRootMismatch` everywhere it's already set (it's harmless on hostPath and correct on `local`/CSI). This is already the Hermes posture and the right belt-and-suspenders stance — but it must **never** be the sole mechanism on a writable local-path PVC. Document this invariant next to each securityContext.

### Final pick

- **Agents (master + spawned), OpenClaw, remote-signer:** one shared `rootChownInitContainer(...)` helper (Track A). This is the primary, bulletproof, cross-platform fix and directly closes demo-quant.
- **x402-buyer (restricted `llm`):** run as UID/GID **1000** to match the provisioner + keep fsGroup as defense-in-depth (Track B/c). No root init (forbidden).
- **Provisioner setup script:** make it `0775` + `chmod g+ws` for forward-compat and to support the buyer's GID-1000 alignment (c).
- **Keep** existing host-side k3d CLI chowns as best-effort fallback only.

This withstands all three adversarial verdicts: it does not rely on fsGroup where it no-ops (#1), it never places a root init in a restricted namespace (#2), and it gives both confirmed at-risk workloads (#3) a deterministic owner-match path.

---

## 3. Fix matrix

| Workload | Namespace | PSS | Writes PVC? | Current mechanism | Risk | EXACT fix | Priority |
|---|---|---|---|---|---|---|---|
| **hermes (spawned agent)** | `agent-<name>` | none | yes (local-path) | only non-root `profile-seed` init (UID 10000); no chown; host-side repair removed | **HIGH (broken — demo-quant)** | Add root chown init via shared helper in `agentPodSpec` → `internal/serviceoffercontroller/agent_render.go:339-341` (prepend before `profile-seed`). Update `agent_render_test.go:201` (count 1→2) and add assertion for the chown init. | **P0** |
| **x402-buyer sidecar** | `llm` | restricted | yes (`x402-buyer-state` local-path) | UID 65532, no chown init (root forbidden); stale comment claims emptyDir | **HIGH (broken)** | Run buyer at UID/GID **1000** to match provisioner: add container `securityContext.runAsUser/runAsGroup: 1000` (or pod-level) at `llm.yaml:308-312`; keep `fsGroup: 1000` (pod sc `llm.yaml:186-192` already 65532 — set buyer-specific). Fix stale comment `llm.yaml:305-307`. | **P0** |
| **l2-sequencer (aztec-node)** | `aztec-<id>` | none | yes (local-path) | no securityContext, relies on upstream chart; no chown init | **HIGH (fragile)** | Set pod securityContext + chart `initChownData` in `internal/embed/networks/aztec/helmfile.yaml.gotmpl:11-13`; if upstream chart lacks chown init, inject one. Confirm aztec image UID first. | **P1** |
| **openclaw** | `openclaw-<id>` | none | yes (local-path) | UID 1000 (matches provisioner) but no fsGroup, no chown init; best-effort host `fixVolumeOwnership` (k3d only) | **MEDIUM** | Add root chown init via shared helper at openclaw chart values (`internal/openclaw/openclaw.go` helm values) chowning `1000:1000`; add `fsGroup: 1000` defense-in-depth. Removes k3s timing flake. | **P1** |
| **ethereum exec/consensus** | `ethereum-<id>` | none | yes (local-path) | UID 10001, upstream chart `initChownData` chowns 10001:10001 (root init present) | MEDIUM (redundant but works) | No functional fix needed; init chown handles it despite provisioner's 1000:1000. Optionally document. | P2 |
| **hermes (master)** | `hermes-obol-agent` | none | yes (local-path) | root `init-hermes-perms` chown 10000:10000 + fsGroup OnRootMismatch + host fallback | none (solid) | Refactor to shared helper (no behavior change) at `internal/hermes/hermes.go:783-803`. Keep `TestGenerateValues_UsesHermesNativeNames` green. | P2 (refactor) |
| **remote-signer (all ns)** | `hermes-<id>`/`agent-<name>`/`openclaw-<id>` | none | no (Secret) | fsGroup 1000, Secret projection | none | No change (Secret volume, no chown needed). | — |
| litellm main | `llm` | restricted | no (emptyDir) | UID 65532, emptyDir | none | No change. | — |
| x402-verifier, serviceoffer-controller | `x402` | restricted | no | UID 65532, ConfigMap RO | none | No change. | — |
| obol-frontend | `obol-frontend` | none | yes (emptyDir) | fsGroup 1001, emptyDir | none | No change (emptyDir is pod-local writable). | — |
| eRPC, cloudflared, storefront, node-exporter, traefik | various | none | no | n/a | none/low | No change. | — |
| local-path-provisioner | `kube-system` | none | no | runs root by design | none | Edit its **setup script** (see code changes) — `0775` + `g+ws`. | P1 |

---

## 4. Code changes

### New shared helper

Add to a shared package usable by both `internal/serviceoffercontroller` and `internal/hermes`. Since `agent_render.go` builds `map[string]any` unstructured specs and `hermes.go` builds YAML via `fmt.Sprintf`, the cleanest single source of truth is a small package returning the **unstructured map**, with hermes consuming it via its own renderer or migrating hermes to the map form. Proposed signature:

```go
// internal/k8sperm/chowninit.go  (new package)
package k8sperm

// RootChownInitContainer returns an init-container spec (as map[string]any for
// unstructured manifests) that runs as root and recursively chowns each mount
// path to uid:gid before the main container starts. Use ONLY in namespaces that
// do NOT enforce restricted PSS (agent-*, hermes-*, openclaw-*). NEVER in llm/x402.
//
// name:   container name (e.g. "init-perms")
// image:  image to run the chown in (reuse the workload image to avoid an extra pull)
// uid,gid: target ownership (must equal the main container's runAsUser/runAsGroup)
// mounts: volume mounts to chown; each {name, mountPath}
func RootChownInitContainer(name, image string, uid, gid int64, mounts []ChownMount) map[string]any

type ChownMount struct {
	Name      string
	MountPath string
}
```

The container sets `securityContext: { runAsUser: 0, runAsGroup: 0, runAsNonRoot: false }`, `command: [sh, -ec, "chown -R <uid>:<gid> <each mountPath>"]`.

### Edits

1. **`internal/serviceoffercontroller/agent_render.go`** — the demo-quant fix.
   - In `agentPodSpec` (`:330-403`), change `initContainers` (`:339-341`) from:
     ```go
     "initContainers": []any{ buildAgentProfileInitContainer() },
     ```
     to prepend the root chown init:
     ```go
     "initContainers": []any{
         k8sperm.RootChownInitContainer("init-perms", hermesImage(), hermesContainerUID, hermesContainerGID,
             []k8sperm.ChownMount{{Name: "data", MountPath: "/data"}}),
         buildAgentProfileInitContainer(),
     },
     ```
   - Init order matters: root chown must run before `profile-seed` (which runs as 10000 and does `mkdir`/`tar`).

2. **`internal/hermes/hermes.go:783-803`** — refactor `init-hermes-perms` to call the shared helper (no behavior change; keeps the proven mechanism, removes drift). Optional in the same PR, but do it to guarantee the two paths never diverge again.

3. **`internal/embed/infrastructure/base/templates/llm.yaml`** — x402-buyer.
   - Add container-level `securityContext.runAsUser: 1000` + `runAsGroup: 1000` to the `x402-buyer` container (`:308-312`), matching the provisioner's chown target. Keep `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]` so restricted PSS stays satisfied.
   - Note: pod-level `fsGroup` is 65532 (`:190`); since buyer needs 1000, set it container-level or split. Verify the buyer binary doesn't require write to paths owned by 65532 (it only writes `/state`).
   - Fix the stale comment at `:305-307` (it says `/state` is emptyDir; it's a PVC since `5c9a879e`).

4. **`internal/embed/infrastructure/base/templates/local-path.yaml:28-32`** — make group-writable:
   ```
   mkdir -m 0775 -p "${VOL_DIR}"
   chown -R 1000:1000 "${VOL_DIR}"
   chmod -R g+ws "${VOL_DIR}"
   ```

5. **`internal/embed/networks/aztec/helmfile.yaml.gotmpl:11-13`** — add a pod securityContext and ensure the upstream chart's chown init runs (or inject one). Confirm `aztecprotocol/aztec:4.3.0` UID first (currently unknown).

6. **`internal/openclaw/openclaw.go`** — add a root chown init (via the shared helper) to the OpenClaw helm values it renders, chowning `1000:1000`; keeps k3s working without relying on the k3d-only host `fixVolumeOwnership`.

### Tests to update / add

- **`internal/serviceoffercontroller/agent_render_test.go:201`** — `TestAgentManifests_ProfileSeedInitContainer` asserts `len(inits) != 1`. **Must change to 2** and add an assertion that init[0] is the root chown (`runAsUser: 0`, `chown -R 10000:10000 /data`). This is the test that currently *enforces* the bug.
- **`internal/serviceoffercontroller/agent_render_test.go` (new)** — `TestAgentManifests_RootChownInit`: assert the chown init runs as UID 0, targets 10000:10000, mounts `data`, and runs **before** `profile-seed`.
- **`internal/hermes/hermes_test.go`** — `TestGenerateValues_UsesHermesNativeNames` (`:122-183`) pins `init-hermes-perms` / `chown -R 10000:10000 /data` / `runAsUser: 0`. Keep green after the helper refactor (update string expectations only if the container name changes).
- **`internal/k8sperm/chowninit_test.go` (new)** — table test for the helper: correct command string, root securityContext, multi-mount chown.
- **`internal/embed/embed_test.go` or a new local-path test** — assert the provisioner setup script contains `chmod -R g+ws` and `0775`.
- **`internal/serviceoffercontroller/render_builders_test.go` `assertRestrictedPSS`** — unchanged (Agent deployments are NOT restricted, so they're not run through it; confirm the x402-buyer change doesn't trip any restricted-PSS test in `internal/x402`).

---

## 5. Quick workaround (unblock demo-quant NOW)

The spawned agent's PVC is owned `1000:1000` but Hermes runs as 10000. Patch the live Deployment to add a one-shot root chown init container, then let it roll. Replace `agent-quant` with the actual namespace.

```bash
export KUBECONFIG="$OBOL_CONFIG_DIR/kubeconfig.yaml"   # dev: .workspace/config/kubeconfig.yaml
NS=agent-quant   # actual demo-quant namespace
IMG=$(kubectl -n "$NS" get deploy hermes -o jsonpath='{.spec.template.spec.containers[0].image}')

kubectl -n "$NS" patch deploy hermes --type=json -p="[
  {\"op\":\"add\",\"path\":\"/spec/template/spec/initContainers/0\",\"value\":{
    \"name\":\"init-perms\",
    \"image\":\"$IMG\",
    \"imagePullPolicy\":\"IfNotPresent\",
    \"securityContext\":{\"runAsUser\":0,\"runAsGroup\":0,\"runAsNonRoot\":false},
    \"command\":[\"sh\",\"-ec\",\"chown -R 10000:10000 /data\"],
    \"volumeMounts\":[{\"name\":\"data\",\"mountPath\":\"/data\"}]
  }}
]"

kubectl -n "$NS" rollout status deploy/hermes --timeout=180s
```

This is permitted because `agent-<name>` enforces no restricted PSS (verdict #2). **Note:** the serviceoffer-controller will overwrite this Deployment on its next reconcile (it re-renders from `agentPodSpec`), so this is strictly a stopgap until the code fix lands — the controller is the source of truth.

Alternative non-patch unblock (k3d only, from host): directly chown the host path the provisioner created:
```bash
docker exec k3d-<stackid>-server-0 chown -R 10000:10000 /data/agent-quant/hermes-data
kubectl -n agent-quant delete pod -l app.kubernetes.io/name=hermes
```

---

## 6. Verification plan

**Assert the no-op premise first (both backends):** confirm the PV is `hostPath` (not `local`), proving fsGroup truly no-ops and the chown init is load-bearing:
```bash
PV=$(kubectl -n agent-quant get pvc hermes-data -o jsonpath='{.spec.volumeName}')
kubectl get pv "$PV" -o jsonpath='{.spec.hostPath}{"\n"}{.spec.local}{"\n"}'  # expect hostPath set, local empty
```

**Unit / render tests (run on any platform, no cluster):**
```bash
go test ./internal/serviceoffercontroller/... ./internal/hermes/... ./internal/k8sperm/... ./internal/embed/...
```
Assert: spawned-agent Deployment has 2 init containers, first is root chown to 10000:10000 before profile-seed; helper emits correct command; provisioner script has `g+ws`.

**k3d-macOS (the failing reproducer):**
1. `OBOL_DEVELOPMENT=true just up` (rebuilds local images, picks up new provisioner ConfigMap — recreate cluster so the registry/ConfigMap is re-applied).
2. Create a spawned agent (the demo-quant path / an Agent CR). Assert it reaches `Ready`, not stuck `Provisioning`.
3. `kubectl -n agent-<name> get pods` → no `Init:CrashLoopBackOff`, no `mkdir: /data/.hermes/home: Permission denied` in init logs.
4. `kubectl -n agent-<name> exec deploy/hermes -- stat -c '%u:%g' /data` → `10000:10000`.
5. x402-buyer: `kubectl -n llm get pod -l app=litellm` Ready; `kubectl -n llm exec <litellm-pod> -c x402-buyer -- ...` is impossible (distroless), so instead port-forward `18402:8402` and hit `/status` (CLAUDE.md pattern) — assert it loads/persists state. Then verify the PVC is owned correctly: `docker exec k3d-<id>-server-0 stat -c '%u:%g' /data/llm/x402-buyer-state` → `1000:1000` and buyer (now UID 1000) writes `consumed.json`.

**k3s-Linux (the backend without the host-side safety net):**
1. Install via `obolup.sh` on a Linux box with the k3s backend.
2. Repeat steps 2–4 above. This is the critical case: there is **no** k3d docker-exec fallback and the CLI usually isn't root, so the in-pod root chown init is the *only* mechanism. If the agent reaches Ready here, the fix is proven backend-independent.
3. Buyer: confirm UID-1000 alignment works (provisioner already chowns 1000:1000, so no chown needed and no root init needed — satisfies restricted PSS).

**Flow / smoke:**
- Run the spawned-agent / marketplace flow (`flows/flow-17` per memory, or the sell+buy full-cycle smoke). Assert a freshly-spawned sub-agent serves inference and the paid-buy loop (buyer state PVC) survives a buyer pod restart without re-spend errors — that's the original reason the buyer became a PVC (`5c9a879e`).
- **Restart-survival assertion** (proves the buyer PVC fix end-to-end): do one paid call, `kubectl -n llm rollout restart deploy/litellm`, confirm `/status` `spent` count persisted and no `400` re-spend cascade.

**Regression guard:** ensure no new root init container leaked into `llm`/`x402` — admission would reject it, so a green deploy in those namespaces is itself the proof.

---

## 7. Image UID appendix

| Image | Default UID | Default GID | Method | Notes |
|---|---|---|---|---|
| ghcr.io/obolnetwork/demo-server | 65532 | 65532 | docker-inspect | distroless static-debian12:nonroot; no writable volume |
| ghcr.io/obolnetwork/serviceoffer-controller (@503016b) | 65532 | 65532 | docker-inspect | distroless nonroot; stateless, no PVC |
| ghcr.io/obolnetwork/x402-buyer (@f5d94fc) | 65532 | 65532 | docker-inspect | distroless nonroot; **writes x402-buyer-state PVC — UID mismatch w/ provisioner 1000 (HIGH)** |
| ghcr.io/obolnetwork/x402-verifier (@46e63fd) | 65532 | 65532 | docker-inspect | distroless nonroot; ConfigMaps RO only |
| ghcr.io/obolnetwork/obol-stack-public-storefront | 1001 | 1001 | dockerfile (USER nextjs) | node:22-alpine; no writable volume |
| obolnetwork/obol-stack-front-end:v0.1.26 | 1001 | 1001 | docker-inspect (dev sibling) | Next.js standalone; emptyDir only |
| autoresearch-worker:dev | 1000 | 1000 | dockerfile (USER worker) | not deployed by core stack; VOLUME /data chowned 1000:1000 |
| ghcr.io/obolnetwork/openclaw:2026.4.21 | 1000 | 1000 | dockerfile (USER node) | matches provisioner 1000:1000; writes /data/.openclaw PVC |
| ghcr.io/obolnetwork/openclaw-base | 1000 | 1000 | base-image-known (USER node) | upstream base |
| ghcr.io/obolnetwork/litellm:sha-9b3e569 | root(0) | root(0) | base-image-known | Obol fork of BerriAI; pod sc overrides to 65532; emptyDir only |
| ghcr.io/obolnetwork/remote-signer:v0.3.0 | unknown | unknown | unknown | mounts Secret RO; fsGroup 1000; verify with docker inspect after pull |
| nousresearch/hermes-agent:v2026.5.28 | unknown | unknown | unknown | image default irrelevant — pod sc forces 10000:10000 + root chown init |
| ghcr.io/erpc/erpc:0.0.64 | root(0) | — | base-image-known | pod sc sets 10001; no PVC |
| cloudflare/cloudflared:2026.5.2 | 65532 | 65532 | base-image-known | distroless base-debian; no PVC |
| quay.io/prometheus/node-exporter:v1.11.0 | 65534 | 65534 | base-image-known | USER nobody; hostPath RO only |
| rancher/local-path-provisioner:v0.0.30 | root(0) | — | base-image-known | **chowns every provisioned volume to 1000:1000** (the root-cause baseline) |
| rancher/mirrored-library-busybox:1.36.1 | root(0) | — | base-image-known | provisioner helper pod; must be root to chown VOL_DIR |
| busybox:1.36 (controller httpd) | 0 | — | base-image-known | controller pins sc to 1000:1000 (restricted PSS in x402); ConfigMap RO |
| rancher/k3s:v1.35.1-k3s1 | 0 | — | docker-inspect | cluster node; root by design |
| paradigmxyz/reth:v2.2.0 (+ geth/nethermind/besu/erigon) | unknown | — | unknown | ethpandaops chart; runs 10001 w/ initChownData root chown |
| lighthouse/prysm/teku/nimbus/lodestar | unknown | — | unknown | ethpandaops chart; 10001 + initChownData |
| aztecprotocol/aztec:4.3.0 | unknown | — | unknown | **no sc in helmfile; HIGH risk if non-root w/o chown init — confirm UID before fix** |
| kube-prometheus-stack subcharts (prom/alertmanager/grafana/ksm) | 65534 / 472 | varies | unknown | chart defaults; prom fsGroup 2000, grafana 472; not pinned in repo |
| traefik (chart default) | 65532 | — | base-image-known | no persistent PVC in this setup |

**Key cross-reference:** provisioner target **1000:1000** matches only OpenClaw (1000) and the controller-pinned httpd (1000). It mismatches Hermes (10000 — fixed by root init) and x402-buyer (65532 — currently broken, fix by re-UID to 1000). Ethereum clients (10001) self-heal via the upstream chart's `initChownData` root init.

---

**Report file:** write this to `/Users/bussyjd/Development/Obol_Workbench/obol-stack/plans/volume-permission-hardening.md`. Primary edit targets for the PR: `/Users/bussyjd/Development/Obol_Workbench/obol-stack/internal/serviceoffercontroller/agent_render.go:339-341` (P0 demo-quant), `/Users/bussyjd/Development/Obol_Workbench/obol-stack/internal/serviceoffercontroller/agent_render_test.go:201` (test that enforces the bug), `/Users/bussyjd/Development/Obol_Workbench/obol-stack/internal/embed/infrastructure/base/templates/llm.yaml:305-312` (P0 buyer), and `/Users/bussyjd/Development/Obol_Workbench/obol-stack/internal/embed/infrastructure/base/templates/local-path.yaml:28-32` (provisioner hardening).