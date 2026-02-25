# Skills System Redesign v2 — Final Implementation Record

> Distilled from v1 notes + Opus analysis. All open questions resolved. Implementation complete.
> The original `skills-system-redesign.md` is preserved as-is for reference.

---

## Guiding Principles

1. **Stock openclaw feel** — the user should not notice they're in a k8s pod. Lean on native openclaw CLI for skill management.
2. **Don't overengineer** — no custom registries, no git sparse-checkout, no lock files for MVP. Ship the simplest thing that works.
3. **Two delivery channels**: compile-time (embedded in obol binary, staged to host, pushed as ConfigMap) and runtime (`kubectl exec` running native openclaw-cli in-pod).
4. **Smart default resolution** — 0 instances: prompt setup. 1 instance: assume it. 2+ instances: require name.

---

## Architecture

```
                          ┌─────────────────────────────┐
                          │  obol CLI binary             │
                          │  (embedded SKILL.md files)   │
                          └────────────┬────────────────┘
                                       │
              ┌────────────────────────┼────────────────────────┐
              │                        │                        │
     ┌────────▼────────┐    ┌─────────▼─────────┐   ┌─────────▼─────────┐
     │  obol openclaw   │    │  obol openclaw     │   │  obol openclaw    │
     │  onboard / sync  │    │  skills add/remove │   │  skills list      │
     │  (compile-time)  │    │  (runtime)         │   │  (runtime)        │
     └────────┬────────┘    └─────────┬─────────┘   └─────────┬─────────┘
              │                        │                        │
              │ stageDefaultSkills     │ kubectl exec           │ kubectl exec
              │ → host config dir      │ -c openclaw            │ -c openclaw
              │ syncStagedSkills       │ openclaw skills add    │ openclaw skills list
              │ → ConfigMap            │ (native openclaw CLI)  │ (native openclaw CLI)
              │                        │                        │
              └────────────────────────┼────────────────────────┘
                                       │
                              ┌────────▼────────┐
                              │  OpenClaw Pod    │
                              │  ConfigMap mount │
                              │  + PVC-backed    │
                              │  ~/.openclaw/    │
                              │    skills/       │
                              └─────────────────┘
```

### How skills reach the pod

| Channel | Mechanism | When | Persistence |
|---------|-----------|------|-------------|
| **Compile-time** (Obol defaults) | Embedded → staged to `$CONFIG_DIR/.../skills/` → pushed as ConfigMap via `SkillsSync()` | Every `doSync()` (onboard and sync) | ConfigMap — chart mounts it |
| **Runtime add/remove** | `kubectl exec -c openclaw deploy/openclaw -- node openclaw.mjs skills add <pkg>` | User runs `obol openclaw skills add ...` | PVC — survives restarts |
| **Runtime list** | `kubectl exec -c openclaw deploy/openclaw -- node openclaw.mjs skills list` | User runs `obol openclaw skills list` | Read-only |

### Why ConfigMap over kubectl cp

The initial implementation used `kubectl cp` to copy skills directly into the pod. This required the pod to be Running, which fails on first deploy when the image pull takes >60s. The ConfigMap approach:
- Works without waiting for the pod (namespace is sufficient)
- Skills are available when the pod starts (chart's init container extracts them)
- Self-healing: `doSync()` stages defaults if missing, pushes every sync
- The host-path PV backing each PVC remains a fallback if ConfigMap hits limits

---

## Part 1: Default Instance Resolution

### Implementation: `internal/openclaw/resolve.go`

```go
func ResolveInstance(cfg *config.Config, args []string) (id string, remaining []string, err error)
func ListInstanceIDs(cfg *config.Config) ([]string, error)
```

- **0 instances** → error: `no OpenClaw instances found — run 'obol agent init' to create one`
- **1 instance** → auto-select, return args unchanged
- **2+ instances** → consume `args[0]` if it matches an instance name, else error listing all

Wired into all subcommands: `sync`, `setup`, `delete`, `token`, `dashboard`, `cli`, `skills`.

Not needed for: `onboard` (creates new), `list` (shows all).

### Tests: `internal/openclaw/resolve_test.go`

9 unit tests covering all 0/1/2+ scenarios, including edge cases (no args, unknown name).

---

## Part 2: Compile-Time Skills (Default Obol Skills)

### What we embed

```
internal/embed/skills/
├── hello/
│   └── SKILL.md
└── ethereum/
    └── SKILL.md
```

### Delivery (two-stage: stage on host, push as ConfigMap)

**Stage 1 — `stageDefaultSkills(deploymentDir)`** (called during `Onboard()` before sync, and inside `doSync()` for self-healing):

- Writes embedded skills to `$CONFIG_DIR/applications/openclaw/<id>/skills/`
- **Skips** if `skills/` directory already exists (user customisation takes precedence)

**Stage 2 — `syncStagedSkills(cfg, id, deploymentDir)`** (called inside `doSync()` after helmfile sync):

- Checks `skills/` dir has subdirectories
- Calls existing `SkillsSync()` to package into ConfigMap `openclaw-<id>-skills`
- Chart's `extract-skills` init container unpacks it on pod (re)start

**Self-healing**: `doSync()` calls `stageDefaultSkills()` before `syncStagedSkills()`. Instances created before the skills feature get defaults on their next sync.

### Files

| File | Status |
|------|--------|
| `internal/embed/skills/hello/SKILL.md` | Created |
| `internal/embed/skills/ethereum/SKILL.md` | Created |
| `internal/embed/embed.go` | Modified — `skillsFS`, `CopySkills()`, `GetEmbeddedSkillNames()` |
| `internal/openclaw/openclaw.go` | Modified — `stageDefaultSkills()`, `syncStagedSkills()`, wired into `Onboard()` + `doSync()` |

---

## Part 3: Runtime Skill Management (`obol openclaw skills`)

### CLI structure

```
obol openclaw skills [instance-name]
├── add <package-or-path>     → kubectl exec -c openclaw ... node openclaw.mjs skills add <pkg>
├── remove <name>             → kubectl exec -c openclaw ... node openclaw.mjs skills remove <name>
├── list                      → kubectl exec -c openclaw ... node openclaw.mjs skills list
└── sync --from <dir>         → packages local dir as ConfigMap (existing SkillsSync mechanism)
```

### Implementation

Thin wrappers in `internal/openclaw/openclaw.go`:

```go
func SkillAdd(cfg, id, args)    → cliViaKubectlExec(cfg, ns, ["skills", "add", ...args])
func SkillRemove(cfg, id, args) → cliViaKubectlExec(cfg, ns, ["skills", "remove", ...args])
func SkillList(cfg, id)         → cliViaKubectlExec(cfg, ns, ["skills", "list"])
```

`cliViaKubectlExec` uses `-c openclaw` to explicitly target the main container (pod has an `extract-skills` init container that confuses the default container selection).

### Files

| File | Status |
|------|--------|
| `cmd/obol/openclaw.go` | Modified — `skills` subcommand group with `add`, `remove`, `list`, `sync` |
| `internal/openclaw/openclaw.go` | Modified — `SkillAdd()`, `SkillRemove()`, `SkillList()` |

---

## Part 4: CLI Structure (Final)

```
obol openclaw
├── onboard [--id <id>] [--force] [--no-sync]
├── sync [instance-name]
├── setup [instance-name]
├── list
├── delete [instance-name]
├── token [instance-name]
├── dashboard [instance-name]
├── cli [instance-name] [-- <openclaw-args>]
└── skills [instance-name]
    ├── add <package-or-path>
    ├── remove <name>
    ├── list
    └── sync --from <dir>
```

All subcommands (except `onboard` and `list`) auto-resolve the instance when only one exists.

---

## Part 5: Default Obol Skill Content

### `hello` (SKILL.md)

Smoke test. Says hello when invoked, confirms skills are loaded.

### `ethereum` (SKILL.md)

Ethereum JSON-RPC access via eRPC. Key details:
- Base URL: `http://erpc.erpc.svc.cluster.local:4000`
- Discovery: `GET /` returns config with connected networks
- RPC pattern: `POST /rpc/<network>` with standard JSON-RPC
- Read-only: no write transactions
- Common methods: `eth_blockNumber`, `eth_syncing`, `eth_getBalance`, `eth_call`, `eth_chainId`, etc.

---

## Decisions Made (resolving v1 open questions)

| Question | Decision | Rationale |
|---|---|---|
| ConfigMap 1MB limit | **Not a concern for MVP** — text SKILL.md files are tiny | Can switch to PVC host-path if needed |
| Skill dependencies | **No** | Skills are independent instruction files |
| Private repo support | **Deferred** — `kubectl exec openclaw skills add` handles natively | Pod fetches from wherever openclaw-cli can |
| Helm chart init container | **Already exists** — `extract-skills` init container unpacks ConfigMap | No chart changes needed |
| Skill validation | **No** — trust skill author | Broken skills just won't work |
| Community skill registry | **Not for MVP** | GitHub repos are sufficient |
| Lock file | **Not for MVP** | Skills are embedded (versioned with binary) or runtime-added |
| GitHub fetching in obol CLI | **Not for MVP** | openclaw-cli in pod does this natively |
| Skill naming | **Plain names** — `hello`, `ethereum` | No `@obol/` prefix needed |
| Sandboxed skills | **Not for MVP** | Docker-in-k8s-in-Docker is fragile |
| Host-path PV for skills | **Fallback option** | Every PVC gets a hostPath PV; can write directly if ConfigMap hits limits |
| `skill` vs `skills` | **`skills` (plural)** | Matches openclaw-cli convention (`node openclaw.mjs skills ...`) |
| kubectl cp vs ConfigMap | **ConfigMap** | No pod readiness dependency; self-healing on every sync |
| Container targeting | **`-c openclaw` explicit** | Pod has `extract-skills` init container; must target main container |

---

## What We Built

1. **`ResolveInstance()`** — smart instance selection (0/1/2+ logic) for all openclaw subcommands
2. **2 embedded SKILL.md files** — `hello`, `ethereum`
3. **`stageDefaultSkills()` + `syncStagedSkills()`** — two-stage delivery: host staging → ConfigMap push
4. **Self-healing in `doSync()`** — stages defaults for pre-existing instances on next sync
5. **`obol openclaw skills add/remove/list`** — thin wrappers around `kubectl exec -c openclaw ... openclaw skills ...`
6. **`-c openclaw`** in `cliViaKubectlExec()` — explicit container targeting

### Files created
- `internal/openclaw/resolve.go`
- `internal/openclaw/resolve_test.go`
- `internal/embed/skills/hello/SKILL.md`
- `internal/embed/skills/ethereum/SKILL.md`

### Files modified
- `internal/embed/embed.go` — skills embed + `CopySkills()` + `GetEmbeddedSkillNames()`
- `internal/openclaw/openclaw.go` — staging, syncing, skill CLI wrappers, `-c openclaw`
- `cmd/obol/openclaw.go` — `ResolveInstance` refactor, `skills` subcommand group

---

## Future Work (Phase 4+)

| Skill | Priority | Notes |
|-------|----------|-------|
| `obol-wallet` | Nice to have | Web3Signer operations |
| `obol-doctor` | Next release | Stack health diagnostics |
| `obol-tunnel` | Future | Cloudflare tunnel management |
| `obol-deploy` | Future | Deploy apps/networks into the stack |

When the skill set grows beyond ~10 skills or community contributions start, consider extracting to `github.com/ObolNetwork/openclaw-skills`.
