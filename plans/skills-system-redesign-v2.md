# Skills System Redesign v2 — Refined Implementation Plan

> Distilled from v1 notes + Opus analysis, with all open questions resolved.
> The original `skills-system-redesign.md` is preserved as-is for reference.

---

## Guiding Principles

1. **Stock openclaw feel** — the user should not notice they're in a k8s pod. Lean on native openclaw CLI for skill management.
2. **Don't overengineer** — no custom registries, no git sparse-checkout, no lock files for MVP. Ship the simplest thing that works.
3. **Two delivery channels**: compile-time (embedded in obol binary, copied at deploy) and runtime (`kubectl exec` running native openclaw-cli in-pod).
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
     │  onboard         │    │  skill add/remove  │   │  skill list       │
     │  (compile-time)  │    │  (runtime)         │   │  (runtime)        │
     └────────┬────────┘    └─────────┬─────────┘   └─────────┬─────────┘
              │                        │                        │
              │ kubectl cp             │ kubectl exec           │ kubectl exec
              │ embedded skills →      │ openclaw skill add     │ openclaw skill list
              │ pod ~/.openclaw/skills │ (native openclaw CLI)  │ (native openclaw CLI)
              │                        │                        │
              └────────────────────────┼────────────────────────┘
                                       │
                              ┌────────▼────────┐
                              │  OpenClaw Pod    │
                              │  PVC-backed      │
                              │  ~/.openclaw/    │
                              │    skills/       │
                              │  (file watcher)  │
                              └─────────────────┘
```

### How skills reach the pod

| Channel | Mechanism | When | Persistence |
|---------|-----------|------|-------------|
| **Compile-time** (Obol defaults) | `kubectl cp` embedded SKILL.md files → pod `~/.openclaw/skills/` | `obol openclaw onboard` (after pod ready) | PVC — survives restarts |
| **Runtime add/remove** | `kubectl exec deploy/openclaw -- node openclaw.mjs skill add <pkg>` | User runs `obol openclaw skill add ...` | PVC — survives restarts |
| **Runtime list** | `kubectl exec deploy/openclaw -- node openclaw.mjs skill list` | User runs `obol openclaw skill list` | Read-only |

OpenClaw's built-in file watcher detects changes and hot-reloads skills on the next agent turn.

### Why this works

- `copyWorkspaceToPod()` already proves `kubectl cp` into `/data/.openclaw/` works and persists via PVC.
- `cliViaKubectlExec()` already proves `kubectl exec ... node openclaw.mjs <args>` works for arbitrary openclaw CLI commands.
- No chart changes needed for basic skill delivery.
- No ConfigMap 1MB limit (PVC-backed storage).
- No host-path volume mounts to configure.

---

## Part 1: Default Instance Resolution

Refresh how `obol openclaw <subcommand>` resolves which instance to target.

### Current behavior
Every subcommand requires an explicit `<id>` argument: `obol openclaw sync <id>`, `obol openclaw setup <id>`, etc.

### New behavior

```
instances = listDeploymentDirs()   // read $CONFIG_DIR/applications/openclaw/

switch len(instances) {
case 0:
    print("No OpenClaw instances found. Run 'obol agent init' to create one.")
    exit(1)
case 1:
    id = instances[0]              // auto-select the only one
    // proceed with subcommand
default:
    // check if user provided an ID as the first arg
    if args[0] matches a known instance name:
        id = args[0]
        args = args[1:]            // consume it
    else:
        print("Multiple instances found. Specify one: " + join(instances))
        exit(1)
}
```

### Implementation

Add a helper function `resolveInstance(cfg, args)` that returns `(id string, remainingArgs []string, err error)`:

```go
// internal/openclaw/resolve.go

func ResolveInstance(cfg *config.Config, args []string) (string, []string, error) {
    instances, err := ListInstanceIDs(cfg)
    if err != nil {
        return "", nil, err
    }

    switch len(instances) {
    case 0:
        return "", nil, fmt.Errorf("no OpenClaw instances found — run 'obol agent init' to create one")
    case 1:
        return instances[0], args, nil
    default:
        if len(args) > 0 {
            for _, inst := range instances {
                if args[0] == inst {
                    return inst, args[1:], nil
                }
            }
        }
        return "", nil, fmt.Errorf("multiple instances found, specify one: %s", strings.Join(instances, ", "))
    }
}
```

Wire this into all subcommands that currently take `<id>`: `sync`, `setup`, `delete`, `token`, `dashboard`, `cli`, `skill`.

**Commands that don't need it**: `onboard` (creates new), `list` (shows all).

### Files to modify

| File | Change |
|------|--------|
| `internal/openclaw/resolve.go` | **New** — `ResolveInstance()`, `ListInstanceIDs()` |
| `cmd/obol/openclaw.go` | Refactor all subcommands to use `ResolveInstance()` instead of `c.Args().Get(0)` |

---

## Part 2: Compile-Time Skills (Default Obol Skills)

### What we embed

Minimal SKILL.md files for Obol-specific agent capabilities. These are plain text — small, no binary dependencies.

```
internal/embed/skills/
├── hello/
│   └── SKILL.md
└── ethereum/
    └── SKILL.md
```

### Skill content (MVP — 2 skills)

**`hello`** (must have):
- says hi when the user calls it
- test to get things working

**`ethereum`** (must have):
- Skill knows the http address to the erpc service
- Skill can do many simple reads of a JSON RPC by connected network
- Skill does not worry about write transactions yet

### How they're delivered (two-stage: stage on host, push as ConfigMap)

**Stage 1 — During `Onboard()`, immediately after creating the deployment directory** (no cluster needed):

```go
// Write embedded skills to the deployment config directory on the host
stageDefaultSkills(deploymentDir)
// Result: $CONFIG_DIR/applications/openclaw/<id>/skills/hello/SKILL.md
//         $CONFIG_DIR/applications/openclaw/<id>/skills/ethereum/SKILL.md
```

**Stage 2 — After `doSync()` completes** (namespace now exists):

```go
// Push staged skills to the cluster via the existing ConfigMap mechanism
skillsDir := filepath.Join(deploymentDir, "skills")
if hasSkillDirs(skillsDir) {
    SkillsSync(cfg, id, skillsDir)  // creates ConfigMap openclaw-<id>-skills
}
```

The chart mounts the ConfigMap into the pod. The stack's reloader component detects the ConfigMap change and triggers a pod restart if needed.

**Why not `kubectl cp`?** The pod may not be ready for 60-120+ seconds on first deploy (image pull). The ConfigMap approach works without waiting for the pod — skills are in the ConfigMap before the pod starts, or picked up on restart via reloader.

**Staged skills persist** at `$CONFIG_DIR/applications/openclaw/<id>/skills/`. They are re-pushed on every `obol openclaw sync` automatically.

### Files to create/modify

| File | Change |
|------|--------|
| `internal/embed/skills/hello/SKILL.md` | **New** — Hello skill |
| `internal/embed/skills/ethereum/SKILL.md` | **New** — Ethereum RPC Obol App skill |
| `internal/embed/embed.go` | Add `//go:embed skills` for the skills FS |
| `internal/openclaw/openclaw.go` | Add `stageDefaultSkills()`, wire into `Onboard()` + auto-push in doSync |

---

## Part 3: Runtime Skill Management (`obol openclaw skill`)

### CLI structure

Replace the current `obol openclaw skills sync <id> --from <dir>` with a streamlined `skill` subcommand group that maps directly to the native openclaw CLI:

```
obol openclaw skill [instance-name]
├── add <package-or-path>     → kubectl exec ... openclaw skill add <package-or-path>
├── remove <name>             → kubectl exec ... openclaw skill remove <name>
├── list                      → kubectl exec ... openclaw skill list
└── sync --from <dir>         → kubectl cp (existing behavior, kept for backward compat)
```

> Note: `skill` (singular) not `skills` — matches stock openclaw CLI convention.
> The old `skills sync` stays as a hidden alias for backward compatibility.

### How each command works

**`obol openclaw skill add <package>`**:
```go
func SkillAdd(cfg *config.Config, id string, args []string) error {
    // 1. Resolve instance (auto if single)
    // 2. kubectl exec -n openclaw-<id> deploy/openclaw -- \
    //        node openclaw.mjs skill add <args...>
    // 3. Stream stdout/stderr to user
    return cliViaKubectlExec(cfg, id, append([]string{"skill", "add"}, args...))
}
```

**`obol openclaw skill remove <name>`**:
```go
func SkillRemove(cfg *config.Config, id string, args []string) error {
    return cliViaKubectlExec(cfg, id, append([]string{"skill", "remove"}, args...))
}
```

**`obol openclaw skill list`**:
```go
func SkillList(cfg *config.Config, id string) error {
    return cliViaKubectlExec(cfg, id, []string{"skill", "list"})
}
```

**`obol openclaw skill sync --from <dir>`** (backward compat):
Keep the existing `SkillsSync()` ConfigMap approach as a fallback, but also add a `kubectl cp` path for direct file copy.

### Auth handling

The `cliViaKubectlExec()` function already handles auth — it uses the KUBECONFIG to kubectl exec into the pod. The pod itself has the openclaw gateway token in its environment (from the Helm chart's Secret mount). No additional auth wiring needed.

### Files to modify

| File | Change |
|------|--------|
| `cmd/obol/openclaw.go` | Replace `skills` subcommand group with `skill` (singular). Add `add`, `remove`, `list` subcommands. Keep `sync --from` as hidden compat. |
| `internal/openclaw/openclaw.go` | Add thin wrappers `SkillAdd()`, `SkillRemove()`, `SkillList()` that call `cliViaKubectlExec()` |

---

## Part 4: CLI Restructuring

### Before (current)

```
obol openclaw
├── onboard [--id <id>] [--force] [--no-sync]
├── sync <id>
├── setup <id>
├── list
├── delete <id>
├── token <id>
├── dashboard <id>
├── cli <id> [-- <openclaw-args>]
└── skills
    └── sync <id> --from <dir>
```

### After

```
obol openclaw
├── onboard [--id <id>] [--force] [--no-sync]
├── sync [instance-name]                          # auto-resolves if single
├── setup [instance-name]                         # auto-resolves if single
├── list
├── delete [instance-name]                        # auto-resolves if single
├── token [instance-name]                         # auto-resolves if single
├── dashboard [instance-name]                     # auto-resolves if single
├── cli [instance-name] [-- <openclaw-args>]      # auto-resolves if single
└── skill [instance-name]                         # auto-resolves if single
    ├── add <package-or-path>
    ├── remove <name>
    ├── list
    └── sync --from <dir>                         # backward compat (hidden)
```

Key changes:
1. Instance ID is now optional everywhere (auto-resolved)
2. `skills` → `skill` (matches openclaw-cli convention)
3. `skill add/remove/list` are new subcommands
4. Old `skills sync` kept as hidden alias

---

## Part 5: Default Obol Skill Content

### `hello` (SKILL.md)

```markdown
---
name: hello
description: A simple greeting skill to verify skills are working
---

# Hello World

When the user asks you to say hello or test the skills system, respond with a friendly greeting confirming that the Obol Stack skills are loaded and working.

## Usage
- Respond to "hello", "hi", "test skills", or similar prompts
- Confirm the skill loaded correctly and the agent can see it
```

### `ethereum` (SKILL.md)

```markdown
---
name: ethereum
description: Ethereum JSON-RPC access via the Obol Stack eRPC gateway
---

# Ethereum RPC via eRPC

Query Ethereum networks through the Obol Stack's eRPC gateway.

## eRPC Gateway

The eRPC service provides a unified JSON-RPC proxy for all connected Ethereum networks. The base URL is always `http://erpc.erpc.svc.cluster.local:4000` inside the cluster.

- **Config/discovery**: `GET http://erpc.erpc.svc.cluster.local:4000/` — returns the eRPC configuration schema including all connected networks and their endpoints
- **RPC endpoint pattern**: `http://erpc.erpc.svc.cluster.local:4000/rpc/<network>`

## Discovering Connected Networks

Fetch the eRPC root config to discover which networks are available:

```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/
```

Parse the response to find project IDs — each project ID is a `<network>` you can query.

## JSON-RPC Queries

All queries use standard Ethereum JSON-RPC. Send POST requests to `http://erpc.erpc.svc.cluster.local:4000/rpc/<network>`.

### Common read methods
- `eth_blockNumber` — latest block number
- `eth_syncing` — sync status (false if synced)
- `eth_getBalance` — account balance (params: address, block)
- `eth_getBlockByNumber` — block details (params: block number, full txs bool)
- `eth_getTransactionReceipt` — transaction receipt (params: tx hash)
- `eth_call` — read-only contract call (params: call object, block)
- `net_peerCount` — connected peer count
- `eth_gasPrice` — current gas price
- `eth_chainId` — chain identifier

### Example: get latest block number on mainnet
```bash
curl -s http://erpc.erpc.svc.cluster.local:4000/rpc/mainnet \
  -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Limitations
- Read-only queries only — no write transactions (eth_sendTransaction, eth_sendRawTransaction)
- Network availability depends on what the user has installed via `obol network install`
```


---

## Implementation Phases

### Phase 1: Default Instance Resolution (small, unblocks everything)

**Scope**: Add `ResolveInstance()` helper, refactor all subcommands to use it.

**Files**:
- `internal/openclaw/resolve.go` (new)
- `cmd/obol/openclaw.go` (modify)

**Tests**:
- `internal/openclaw/resolve_test.go` — unit tests for 0/1/2+ instance scenarios

**Estimate**: ~2-3 hours

---

### Phase 2: Compile-Time Default Skills

**Scope**: Embed 2 SKILL.md files (`hello`, `ethereum`), copy to pod on onboard.

**Files**:
- `internal/embed/skills/hello/SKILL.md` (new)
- `internal/embed/skills/ethereum/SKILL.md` (new)
- `internal/embed/embed.go` (modify — add skills embed)
- `internal/openclaw/openclaw.go` (modify — add `copyDefaultSkillsToPod()`, wire into `Onboard()`)

**Tests**:
- Unit test that embedded skills FS contains expected files
- Integration test that onboard copies skills to pod (extend existing integration tests)

**Estimate**: ~3-4 hours

---

### Phase 3: Runtime Skill Management CLI

**Scope**: `obol openclaw skill add/remove/list` via `kubectl exec`.

**Files**:
- `cmd/obol/openclaw.go` (modify — new `skill` subcommand group)
- `internal/openclaw/openclaw.go` (modify — thin wrappers calling `cliViaKubectlExec`)

**Tests**:
- Integration test: `obol openclaw skill list` returns output from pod
- Integration test: `obol openclaw skill add` + `skill list` shows it

**Backward compat**:
- Keep `skills sync --from <dir>` as hidden command (no removal)

**Estimate**: ~2-3 hours

---

### Phase 4: Skill Content Refinement + More Skills (follow-up)

**Scope**: Iterate on SKILL.md content quality, add more skills as the stack grows.

**Future skills** (not in MVP):
- `obol-wallet` — Web3Signer operations (nice to have)
- `obol-doctor` — Stack health diagnostics (next release)
- `obol-tunnel` — Cloudflare tunnel management
- `obol-deploy` — Deploy apps/networks into the stack

**No separate repo needed yet** — skills are embedded in the obol binary. When the skill set grows beyond ~10 skills or community contributions start, consider extracting to `github.com/ObolNetwork/openclaw-skills`.

---

## Decisions Made (resolving v1 open questions)

| Question from v1 | Decision | Rationale |
|---|---|---|
| ConfigMap 1MB limit | **Not a concern** — use PVC storage via `kubectl cp` and `kubectl exec` | Skills persist on pod PVC, not in ConfigMap |
| Skill dependencies between skills | **No** — not for MVP | Overengineering; skills are independent instruction files |
| Private repo support | **Not for MVP** — `kubectl exec openclaw skill add` handles this natively if openclaw supports it | The pod can fetch from wherever openclaw-cli can |
| Helm chart init container for skills | **Not needed** — `kubectl cp` after pod ready | Simpler, no chart changes required for MVP |
| Skill validation (bins available in pod) | **No validation** — trust the skill author | Keep it simple; broken skills just won't work |
| Community skill registry | **Not for MVP** — GitHub repos are sufficient | No custom registry infrastructure |
| Lock file for reproducibility | **Not for MVP** — skills are either embedded (versioned with obol binary) or added at runtime | Lock files add complexity for little gain at this stage |
| GitHub fetching in obol CLI | **Not for MVP** — `kubectl exec openclaw skill add` does this natively | Don't reimplement what openclaw-cli already does |
| Namespace for skills (`@obol/`) | **Plain names** — `hello`, `ethereum`, etc. | No prefix needed; embedded skills are obviously ours, community skills come via openclaw-cli natively |
| Sandboxed skills | **Not for MVP** — Docker-in-k8s-in-Docker is asking for trouble | Revisit when there's a real security concern |
| Host-path volume mount for skills | **Not for MVP** — `kubectl cp` + `kubectl exec` is sufficient | Avoids k3d config and chart changes |

---

## What We're NOT Building

To keep scope tight:

- No GitHub fetching in the obol CLI (openclaw-cli in the pod handles this natively)
- No skills lock file
- No skills registry JSON
- No `skills update` command (use `skill remove` + `skill add`)
- No `skills init --defaults` as separate command (defaults installed during onboard)
- No host-path volume mounts for skills ANSWER: we already have that because every PVC a chart asks for gets filled by a hostPath PV, so if we do get stuck in implementation or permission management or something, maybe we can try this
- No Helm chart changes for skills delivery
- No custom skill packaging/tarballing (except backward-compat `sync --from`)
- No `@obol/` namespace resolution

---

## Summary: What We ARE Building

1. **`ResolveInstance()`** — smart instance selection (0/1/2+ logic) for all openclaw subcommands
2. **2 embedded SKILL.md files** — ethereum, hello
3. **`copyDefaultSkillsToPod()`** — copies embedded skills to pod on onboard (like workspace copy)
4. **`obol openclaw skill add/remove/list`** — thin wrappers around `kubectl exec ... openclaw skill ...`
5. **Backward compat** — `obol openclaw skills sync --from <dir>` still works (hidden)

Total new Go code: ~200-300 lines. Total new files: 4-5. Reuses existing patterns (`kubectl cp`, `cliViaKubectlExec`).
