/sc:workflow the ./plans/skills-system-redesign is a concatenation of my notes, and your plans (annotated by me answering your questions). I want you to study both, and take my choices into your implementation. Key things to consider are the refresh of how we do `default` openclaw instances (if we have none, prompt setup, 1 assume its a given, 2+ expect a name mid command you take out and use to route correctly ) in the obol cli. For compile time skills, we will copy them from obol-cli binary to the localhost path that corresponds with the openclaw-gateway's `~/.openclaw/skills`. For run time skill addition using the `obol openclaw skill` commands, lets try the approach of `kubectl exec ... ` running the openclaw-cli on the openclaw-gateway container, with the k8s secret auth token loaded etc. ask me any clarifying questions. don't overengineer features if you don't have to, we want the user to feel like they're using stock openclaw. output it as a new refined plan and keep this one. (Maybe do a cleaned version of this as an interim? we need to sort out the disjointed bits and multiple-choice etc)

_______________ [My notes] _______
Agent skills in obol openclaw

Ideas gathering phase:
Local folder, obol-cli command to zip to .tgz and push to config map. Openclaw chart to detect and uncompress.
github.com/ObolNetwork/skills 
Openclaw chart pulls these locally in an init script
Openclaw chart has helm sub packages which just contain skill repos? 
What’s the advantage? To manage dependencies helm natively? 
We create a derivative openclaw dockerfile, and embed skills in the image? 
Review opus’s design
Lots of configurability, needs a tl;dr. 
The idea of some skills in the cli so it can handle network/github api rate limits is cool. With local ollama someday you could have an offline, skill enabled obol agent. Should the skills just be in the chart though? Need to answer it about constraints
Some skills like using the stack itself may make more sense than the openclaw chart. The skill to use the stack is broader than that application. 


we should figure out how a helm chart can bundle a set of skills, that other apps can find at runtime. 
does the web3signer app expose a config map other namespaces can read? caps us at 1mb for all skills it exports
can they have shared disk across all apps (i.e. create a PV with them on it)? not easily but maybe if all the pvcs mount as read only that would work? 
serve them like a webserver and expose a standard service to find them? <http://skills.<app-name>.<namespace>.svc.cluster.local/
Reloading: “Changes to skills are picked up on the next agent turn when the watcher is enabled.” openclaw hot reloads files on disk
We’ll probably have to make this work for openclaw plugins almost as fast. 

Key note:
__________
Locations and precedence
Skills are loaded from three places:
Bundled skills: shipped with the install (npm package or OpenClaw.app)
Managed/local skills: ~/.openclaw/skills
Workspace skills: <workspace>/skills
If a skill name conflicts, precedence is: <workspace>/skills (highest) → ~/.openclaw/skills → bundled skills (lowest) Additionally, you can configure extra skill folders (lowest precedence) via skills.load.extraDirs in ~/.openclaw/openclaw.json.

__________

Actions:
We should sandbox skills by default maybe? (thats docker in k8s in docker though, so maybe asking for trouble? + routing difficulties to resources in the stack? 

Sandboxed skills + env vars
When a session is sandboxed, skill processes run inside Docker. The sandbox does not inherit the host process.env. Use one of:
agents.defaults.sandbox.docker.env (or per-agent agents.list[].sandbox.docker.env)
bake the env into your custom sandbox image
Global env and skills.entries.<skill>.env/apiKey apply to host runs only.


~/.openclaw/openclaw.json

{
  skills: {
    allowBundled: ["gemini", "peekaboo"],
    load: {
      extraDirs: ["~/Projects/agent-scripts/skills", "~/Projects/oss/some-skill-pack/skills"],
      watch: true,
      watchDebounceMs: 250,
    },
    install: {
      preferBrew: true,
      nodeManager: "npm", // npm | pnpm | yarn | bun (Gateway runtime still Node; bun not recommended)
    },
    entries: {
      "nano-banana-pro": {
        enabled: true,
        apiKey: "GEMINI_KEY_HERE",
        env: {
          GEMINI_API_KEY: "GEMINI_KEY_HERE",
        },
      },
      peekaboo: { enabled: true },
      sag: { enabled: false },
    },
  },
}



Conclusion:

We need to correctly set the openclaw config in our chart, and consider openclaw’s location precedence (above). If for example we put popular named skills in high inheritance places, that would put us in charge of the skill. (eth-wingman, etc) 
Management commands:
Stick to openclaw standard and map straight into the gateway. 
Requires a change to the obol openclaw CLI structure, I think its worth it. 
When obol openclaw is called, first, we count how many instances are installed
If none are installed, prompt the user to do obol agent init
If exactly one is installed, assume that is default, pipe the rest of the commands into the openclaw cli (temporary pod, or the on-host way we have now). It needs to be able to speak to the openclaw gateway. 
It needs to be coming from an IP that openclaw will accept for security reasons.
[I guess this depends on what part of the code writes the skill files. If its the CLI, then these files would appear on the host, and we’d be back to packaging them like i would like to avoid.]
1. We could exec on the openclaw container itself and do everything local to the container runtime, that should sort auth and folder writing perms?
2. Plan b, we could on the host write to: $HOME/.config/obol/applications/obol/openclaw/playful-rabbit/.openclaw/skills/<new-skill-name-here> and rely on openclaw’s hot reload behaviour
If more than one instance is installed, then we have to interpret the next word of command input as a petname, use it to decide the host path to write the skill to, or the correct gateway to kubectl exec on, before giving the remainder to a correctly configured openclaw cli (if needed)
 
Pre-installed skills:
We need:
[Must have] Ethereum Network (erpc)
[Nice to have] Ethereum Wallet (web3signer)
[Next Release] Obol Doctor (debugs the obol stack) (also will be an obol doctor)
[A plan for a skill to sensibly deploy stuff into the stack]
Open question:
How does llmspy handle the idea of telling llms about tools they can opt to use? Is it just dealt with at the openclaw layer and not a concern of the llm?
Same problem as always, how do we get this folder structure on the disk for the openclaw instance.  
We can start with:
Embedding the skills in obol-cli (golang) and copying them to the appropriate host directory upon creation/import of a new openclaw instance
Import should be cognizant of folder conficts in case this is an imported openclaw.
Will we have an obol/ namespace in ~/.openclaw/skills? is that standard?

Nice to haves / hardening:
We should see if we can get the sandboxed skills feature to work
Let people turn off telemetry? (for now we want plenty though to show adoption). 



Telemetry (install counts)
When you run clawhub sync while logged in, the CLI sends a minimal snapshot to compute install counts. You can disable this entirely:
export CLAWHUB_DISABLE_TELEMETRY=1




https://docs.openclaw.ai/tools/skills-config#skills-config


_______________ [Opus 4.6] _______

# Skills System Redesign: Remote Skills & Default Skill Packs

## Problem Statement

OpenClaw skills are currently filesystem-based directories with a `SKILL.md` file. The only way to get custom skills into a deployed OpenClaw instance is:

1. Have them locally on disk
2. Run `obol openclaw skills sync <id> --from <dir>` to package them into a ConfigMap
3. Run `obol openclaw sync <id>` to apply

There is no way to:
- Pull skills from public GitHub repositories
- Share skills across the community
- Ship default Obol-specific skills with the stack
- Manage skill dependencies or versions
- Add/remove individual skills without replacing the entire set

## Goals

1. **`npx`-like skill fetching** - `obol openclaw skills add github.com/org/repo` pulls skills from GitHub
2. **Default Obol skill pack** - ships useful Obol Stack skills out of the box (k8s ops, network management, monitoring)
3. **Skill registry resolution** - shorthand like `@obol/ethereum` resolves to a known GitHub location
4. **Declarative skill management** - skills can be listed in config for reproducible setups
5. **Backward compatible** - existing `skills sync --from <dir>` still works

## Current Architecture

### How OpenClaw Loads Skills (npm package internals)

```
Load precedence (highest → lowest):
1. <workspace>/skills/          — per-agent workspace skills
2. ~/.openclaw/skills/          — managed/local skills
3. Bundled skills (npm package) — 40+ built-in skills
4. skills.load.extraDirs        — additional paths from openclaw.json
```

Each skill is a directory containing `SKILL.md` with YAML frontmatter:

```markdown
---
name: my-skill
description: What it does
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
      env: ["KUBECONFIG"]
---

# Agent instructions for using this skill...
```

### How Obol Stack Delivers Skills Today

```
obol openclaw skills sync <id> --from <dir>
    │
    ├─ tar -czf skills.tgz -C <dir> .
    ├─ kubectl delete configmap openclaw-<id>-skills (if exists)
    ├─ kubectl create configmap openclaw-<id>-skills --from-file=skills.tgz=<tmp>
    └─ prints "To apply, re-sync: obol openclaw sync <id>"
```

The Helm chart (remote `obol/openclaw v0.1.3`) mounts this ConfigMap and extracts it into the pod's skills directory.

### Overlay Values (current)

```yaml
skills:
  enabled: true
  createDefault: true   # chart creates empty ConfigMap placeholder
```

### Key Constraints

- The Helm chart is **remote** (`obol/openclaw` from `obolnetwork.github.io/helm-charts/`), not in this repo ANSWER: You can update this chart, its adjacent to you in ../helm-charts. 
- Skills ConfigMap has a **1MB limit** (etcd object size limit) — fine for text-based SKILL.md files but limits total skill count ANSWER: lets modify folders on localhost, which are mapped straight into the pods PVs, and openclaw runs a file watcher so it will just detect and reload
- The pod needs skills at filesystem paths — whatever we do must end up as files in the container
- OpenClaw's `skills.load.extraDirs` config and `skills.entries` per-skill config are available levers. ANSWER: and knowing the right host path to write to to end up at ~/.openclaw/skills

---

## Proposed Design

### Architecture Overview

```
                                    ┌─────────────────────────────┐
                                    │   GitHub / Git Repositories │
                                    │                             │
                                    │  github.com/ObolNetwork/    │
                                    │    openclaw-skills/         │
                                    │  github.com/user/           │
                                    │    my-custom-skill/         │
                                    └──────────┬──────────────────┘
                                               │
                           ┌───────────────────┼───────────────────┐
                           │                   │                   │
                     ┌─────▼─────┐     ┌───────▼───────┐   ┌──────▼──────┐
                     │  CLI Fetch │     │  Init Container│   │  Declarative│
                     │  (dev UX)  │     │  (GitOps)     │   │  Config     │
                     └─────┬─────┘     └───────┬───────┘   └──────┬──────┘
                           │                   │                   │
                           ▼                   ▼                   ▼
                    ┌──────────────────────────────────────────────────┐
                    │           Local Skills Directory                 │
                    │  $CONFIG_DIR/applications/openclaw/<id>/skills/  │
                    │                                                  │
                    │  ├── @obol/                                      │
                    │  │   ├── kubernetes/SKILL.md                     │
                    │  │   ├── ethereum/SKILL.md                       │
                    │  │   └── monitoring/SKILL.md                     │
                    │  ├── @user/                                      │
                    │  │   └── custom-skill/SKILL.md                   │
                    │  └── skills.lock.json                            │
                    └──────────────────┬───────────────────────────────┘
                                       │
                                       │ obol openclaw skills sync <id>
                                       │ (tar → ConfigMap → helmfile sync)
                                       ▼
                              ┌──────────────────┐
                              │  OpenClaw Pod     │
                              │  /skills/ mount   │
                              └──────────────────┘
```

### Component 1: Skill Source Resolution (`internal/openclaw/skills/`)

A new `skills` subpackage that handles fetching skills from various sources.

#### Source Types

```go
// SkillSource represents a fetchable skill location
type SkillSource struct {
    Type    string // "github", "local", "builtin"
    Owner   string // GitHub org/user
    Repo    string // Repository name
    Path    string // Subdirectory within repo (optional)
    Ref     string // Git ref: tag, branch, commit (default: HEAD)
    Alias   string // Local name override
}
```

#### Resolution Rules

| Input | Resolves To |
|-------|-------------|
| `@obol/kubernetes` | `github.com/ObolNetwork/openclaw-skills/skills/kubernetes@latest` |
| `@obol/ethereum` | `github.com/ObolNetwork/openclaw-skills/skills/ethereum@latest` |
| `github.com/user/repo` | Clone entire repo, find all `SKILL.md` files |
| `github.com/user/repo/path/to/skill` | Clone repo, use specific subdirectory |
| `github.com/user/repo@v1.2.0` | Clone at specific tag |
| `./local/path` | Copy from local filesystem (existing behavior) |

#### Registry File

A simple JSON registry embedded in the obol CLI binary that maps shorthand names to GitHub sources:

```go
//go:embed skills-registry.json
var defaultRegistry []byte
```

```json
{
  "version": 1,
  "prefix": "@obol",
  "repository": "github.com/ObolNetwork/openclaw-skills",
  "skills": {
    "kubernetes": {
      "path": "skills/kubernetes",
      "description": "Kubernetes cluster operations via kubectl",
      "requires": { "bins": ["kubectl"] }
    },
    "ethereum": {
      "path": "skills/ethereum",
      "description": "Ethereum node management and monitoring",
      "requires": { "bins": ["kubectl"] }
    },
    "monitoring": {
      "path": "skills/monitoring",
      "description": "Prometheus/Grafana monitoring operations"
    },
    "network-ops": {
      "path": "skills/network-ops",
      "description": "Obol network install/sync/delete operations"
    },
    "tunnel": {
      "path": "skills/tunnel",
      "description": "Cloudflare tunnel management"
    }
  }
}
```

### Component 2: CLI Commands (`cmd/obol/openclaw.go`)

Expand the `skills` subcommand group:

```
obol openclaw skills
├── add <source> [--ref <tag>]      # Fetch skill(s) from GitHub or local path
├── remove <name>                    # Remove an installed skill
├── list [--remote]                  # List installed skills (or available @obol skills)
├── sync <id>                        # Push local skills dir → ConfigMap → pod
├── update [<name>|--all]            # Update skill(s) to latest version
└── init <id> [--defaults]           # Initialize skills dir with default Obol pack
```

#### `obol openclaw skills add` — the npx-like command

```bash
# Add from the Obol registry (shorthand)
obol openclaw skills add @obol/kubernetes
obol openclaw skills add @obol/ethereum @obol/monitoring

# Add from any public GitHub repo
obol openclaw skills add github.com/someuser/cool-skill
obol openclaw skills add github.com/someuser/skill-pack/skills/specific-one

# Add from GitHub with version pinning
obol openclaw skills add github.com/someuser/cool-skill@v2.0.0

# Add from local directory (replaces old --from behavior)
obol openclaw skills add ./my-local-skills/custom-skill

# Add all default Obol skills
obol openclaw skills add @obol/defaults
```

**Flow:**

```
obol openclaw skills add @obol/kubernetes
    │
    ├─ Resolve "@obol/kubernetes" → github.com/ObolNetwork/openclaw-skills/skills/kubernetes
    ├─ Sparse checkout (or GitHub API tarball) of just that path
    ├─ Validate: SKILL.md exists with valid frontmatter
    ├─ Copy to: $CONFIG_DIR/applications/openclaw/<id>/skills/@obol/kubernetes/
    ├─ Update skills.lock.json with source, ref, commit SHA
    ├─ Print: "✓ Added @obol/kubernetes"
    └─ Print: "Run 'obol openclaw skills sync <id>' to deploy"
```

#### `obol openclaw skills init` — bootstrap with defaults

```bash
# Initialize with the default Obol skill pack
obol openclaw skills init default --defaults

# This is equivalent to:
obol openclaw skills add @obol/defaults
obol openclaw skills sync default
```

#### `obol openclaw skills list`

```bash
$ obol openclaw skills list default
Installed skills for openclaw/default:

  @obol/kubernetes     Kubernetes cluster operations          v1.0.0 (up to date)
  @obol/ethereum       Ethereum node management               v1.0.0 (up to date)
  @obol/monitoring     Prometheus/Grafana operations           v1.0.0 (update: v1.1.0)
  custom-skill         My custom skill from local              local

Total: 4 skill(s)

$ obol openclaw skills list --remote
Available skills from @obol registry:

  @obol/kubernetes     Kubernetes cluster operations via kubectl
  @obol/ethereum       Ethereum node management and monitoring
  @obol/monitoring     Prometheus/Grafana monitoring operations
  @obol/network-ops    Obol network install/sync/delete operations
  @obol/tunnel         Cloudflare tunnel management
```

### Component 3: Skills Lock File

Track installed skills and their versions for reproducibility:

```json
{
  "version": 1,
  "skills": {
    "@obol/kubernetes": {
      "source": "github.com/ObolNetwork/openclaw-skills",
      "path": "skills/kubernetes",
      "ref": "v1.0.0",
      "commit": "abc123def456",
      "installed": "2026-02-18T12:00:00Z"
    },
    "@obol/ethereum": {
      "source": "github.com/ObolNetwork/openclaw-skills",
      "path": "skills/ethereum",
      "ref": "v1.0.0",
      "commit": "abc123def456",
      "installed": "2026-02-18T12:00:00Z"
    },
    "custom-skill": {
      "source": "local",
      "path": "/Users/dev/my-skills/custom-skill",
      "installed": "2026-02-18T14:00:00Z"
    }
  }
}
```

### Component 4: GitHub Fetching Strategy

Two approaches, use **GitHub API tarball** as primary (no git dependency):

```go
// Primary: GitHub API tarball download (no git required)
func fetchFromGitHub(owner, repo, path, ref string) (string, error) {
    // GET https://api.github.com/repos/{owner}/{repo}/tarball/{ref}
    // Extract only the files under {path}/
    // Return path to extracted directory
}

// Fallback: git sparse-checkout (for private repos or rate limiting)
func fetchViaGit(repoURL, path, ref string) (string, error) {
    // git clone --depth 1 --filter=blob:none --sparse <url>
    // git sparse-checkout set <path>
    // Return path to checked out directory
}
```

**Rate limiting**: GitHub API allows 60 requests/hour unauthenticated, 5000 with a token. For the `add` command this is fine (one request per skill add). Support `GITHUB_TOKEN` env var for authenticated requests.

### Component 5: Default Skills in Onboard Flow

Modify `Onboard()` to optionally install default skills:

```go
// In Onboard(), after writing overlay and helmfile:
if opts.Sync {
    // Install default Obol skills if skills dir is empty
    skillsDir := filepath.Join(deploymentDir, "skills")
    if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
        fmt.Println("Installing default Obol skills...")
        if err := installDefaultSkills(skillsDir); err != nil {
            fmt.Printf("Warning: could not install default skills: %v\n", err)
            // Non-fatal — continue with deployment
        }
    }
    // Skills sync happens as part of doSync
}
```

The default skills should be fetched from `@obol/defaults` (which maps to a curated list). If network is unavailable, fall back to a minimal embedded skill set.

### Component 6: Embedded Fallback Skills

For air-gapped or offline scenarios, embed a minimal set of skills directly in the CLI binary:

```go
//go:embed skills/kubernetes/SKILL.md
//go:embed skills/network-ops/SKILL.md
var embeddedSkills embed.FS
```

These serve as a fallback when GitHub is unreachable during `skills init --defaults`.

### Component 7: Overlay Values Enhancement

Update `generateOverlayValues()` to support skill configuration in the Helm values:

```yaml
skills:
  enabled: true
  createDefault: true
  # NEW: Configure per-skill settings via overlay
  entries:
    kubernetes:
      enabled: true
    ethereum:
      enabled: true
      env:
        ETHEREUM_NETWORK: "mainnet"
    monitoring:
      enabled: true
```

This maps to OpenClaw's `skills.entries` configuration, giving operators control over which skills are active and their per-skill environment.

### Component 8: Automatic Skills Sync on Deploy

Modify `doSync()` to automatically package and push skills if the local skills directory exists:

```go
func doSync(cfg *config.Config, id string) error {
    deploymentDir := deploymentPath(cfg, id)

    // Auto-sync skills if local skills directory exists
    skillsDir := filepath.Join(deploymentDir, "skills")
    if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
        entries, _ := os.ReadDir(skillsDir)
        // Only sync if there are actual skill directories (not just lock file)
        hasSkills := false
        for _, e := range entries {
            if e.IsDir() {
                hasSkills = true
                break
            }
        }
        if hasSkills {
            fmt.Println("Syncing skills to cluster...")
            if err := SkillsSync(cfg, id, skillsDir); err != nil {
                fmt.Printf("Warning: skills sync failed: %v\n", err)
            }
        }
    }

    // ... existing helmfile sync logic
}
```

This removes the two-step manual process. Adding a skill and syncing the deployment automatically picks it up.

---

## Proposed Obol Default Skills

These would live in `github.com/ObolNetwork/openclaw-skills`:

### `@obol/kubernetes`

```markdown
---
name: kubernetes
description: Kubernetes cluster operations for the Obol Stack
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
      env: ["KUBECONFIG"]
---

# Kubernetes Operations

You have access to kubectl configured for the Obol Stack k3d cluster.

## Capabilities
- List, describe, and inspect pods, services, deployments across all namespaces
- View pod logs and events
- Check resource usage and node status
- Debug failing pods (describe, logs, events)

## Conventions
- The stack uses k3d with namespaces per deployment
- Network deployments: `ethereum-<id>`, `helios-<id>`, `aztec-<id>`
- Infrastructure: `traefik`, `erpc`, `monitoring`, `llm`, `obol-frontend`
- Use `kubectl get all -n <namespace>` for namespace overview
```

### `@obol/ethereum`

```markdown
---
name: ethereum
description: Ethereum node management and monitoring
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
---

# Ethereum Node Management

Manage Ethereum network deployments in the Obol Stack.

## Capabilities
- Monitor execution and beacon client sync status
- Check peer counts and network connectivity
- View client logs for debugging
- Monitor disk usage and resource consumption
- Check chain head and sync progress

## Common Operations
- Sync status: `kubectl -n ethereum-<id> logs deploy/execution -f`
- Beacon status: `kubectl -n ethereum-<id> logs deploy/beacon -f`
- Resource usage: `kubectl -n ethereum-<id> top pods`
```

### `@obol/monitoring`

```markdown
---
name: monitoring
description: Prometheus and Grafana monitoring operations
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
---

# Monitoring Operations

Access Prometheus metrics and Grafana dashboards for the Obol Stack.

## Capabilities
- Query Prometheus for metrics
- Check alerting rules and firing alerts
- Monitor resource usage trends
- Access Grafana dashboards
```

### `@obol/network-ops`

```markdown
---
name: network-ops
description: Obol network deployment lifecycle operations
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
---

# Network Operations

Manage the full lifecycle of blockchain network deployments.

## Capabilities
- List installed network deployments
- Check deployment health and sync status
- Monitor resource consumption per deployment
- Assist with network configuration decisions
```

### `@obol/tunnel`

```markdown
---
name: tunnel
description: Cloudflare tunnel management for public access
metadata:
  openclaw:
    requires:
      bins: ["kubectl"]
---

# Tunnel Management

Manage Cloudflare tunnels for exposing Obol Stack services publicly.

## Capabilities
- Check tunnel status and connectivity
- View tunnel logs for debugging
- Monitor tunnel routes and DNS configuration
```

---

## Implementation Phases

### Phase 1: Core Skill Fetching (MVP)

**Files to create/modify:**

| File | Action | Description |
|------|--------|-------------|
| `internal/openclaw/skills/resolve.go` | Create | Source resolution (GitHub URL parsing, @obol shorthand) |
| `internal/openclaw/skills/fetch.go` | Create | GitHub tarball download + extraction |
| `internal/openclaw/skills/lock.go` | Create | Lock file read/write |
| `internal/openclaw/skills/registry.go` | Create | Embedded registry loading |
| `internal/openclaw/skills/skills-registry.json` | Create | Default @obol skill registry |
| `cmd/obol/openclaw.go` | Modify | Add `skills add`, `skills remove`, `skills list`, `skills update` subcommands |
| `internal/openclaw/openclaw.go` | Modify | Update `SkillsSync` to work with new skills dir layout |

**Deliverables:**
- `obol openclaw skills add <source>` works with GitHub URLs and @obol shorthand
- `obol openclaw skills remove <name>` removes a skill
- `obol openclaw skills list` shows installed skills
- Lock file tracks installed skills
- Existing `skills sync --from` still works

### Phase 2: Default Skills & Auto-Install

**Files to create/modify:**

| File | Action | Description |
|------|--------|-------------|
| `internal/openclaw/skills/defaults.go` | Create | Default skill installation logic |
| `internal/openclaw/skills/embedded/` | Create | Minimal embedded fallback skills |
| `internal/openclaw/openclaw.go` | Modify | Wire default skills into `Onboard()` flow |
| `internal/openclaw/openclaw.go` | Modify | Auto-sync skills in `doSync()` |

**Deliverables:**
- `obol openclaw skills init <id> --defaults` bootstraps default skills
- `Onboard()` installs defaults on first deploy (with network fallback to embedded)
- `doSync()` automatically packages skills if present
- No more two-step manual skills sync

### Phase 3: Skill Pack Repository

**External repository:** `github.com/ObolNetwork/openclaw-skills`

| Path | Description |
|------|-------------|
| `skills/kubernetes/SKILL.md` | K8s cluster operations |
| `skills/ethereum/SKILL.md` | Ethereum node management |
| `skills/monitoring/SKILL.md` | Prometheus/Grafana ops |
| `skills/network-ops/SKILL.md` | Network lifecycle management |
| `skills/tunnel/SKILL.md` | Cloudflare tunnel management |
| `README.md` | Contributing guide for community skills |

**Deliverables:**
- Public repo with curated Obol skills
- CI validation that all skills have valid SKILL.md frontmatter
- Tagged releases for version pinning

### Phase 4: Helm Chart Integration (Upstream)

Changes to the **remote** `obol/openclaw` Helm chart (separate repo):

- Support `skills.sources` in values for declarative skill fetching via init container
- Init container that can `git clone` or download skills from configured sources
- This enables GitOps workflows where skills are declared in values, not manually pushed

```yaml
# Future values-obol.yaml
skills:
  enabled: true
  sources:
    - name: obol-defaults
      repo: github.com/ObolNetwork/openclaw-skills
      ref: v1.0.0
      path: skills/
    - name: custom
      repo: github.com/myorg/my-skills
      ref: main
  entries:
    kubernetes:
      enabled: true
    ethereum:
      enabled: true
```

This phase requires coordination with the upstream openclaw Helm chart maintainers.

---

## Directory Layout (Post-Implementation)

```
$CONFIG_DIR/applications/openclaw/<id>/
├── values-obol.yaml
├── helmfile.yaml
├── values-obol.secrets.json
└── skills/                          # NEW: managed skills directory
    ├── skills.lock.json             # Tracks sources, versions, commits
    ├── @obol/                       # Namespaced by source
    │   ├── kubernetes/
    │   │   └── SKILL.md
    │   ├── ethereum/
    │   │   └── SKILL.md
    │   ├── monitoring/
    │   │   └── SKILL.md
    │   ├── network-ops/
    │   │   └── SKILL.md
    │   └── tunnel/
    │       └── SKILL.md
    └── @someuser/                   # Community skills
        └── custom-skill/
            └── SKILL.md
```

---

## CLI UX Examples

### First-time setup with defaults

```bash
$ obol agent init
Generated deployment ID: default
  ✓ Ollama detected at http://localhost:11434

✓ OpenClaw instance configured!
  Installing default Obol skills...
  ✓ Added @obol/kubernetes
  ✓ Added @obol/ethereum
  ✓ Added @obol/monitoring
  ✓ Added @obol/network-ops
  ✓ Added @obol/tunnel

Deploying to cluster...
  Syncing skills to cluster...
  ✓ Skills ConfigMap updated: openclaw-default-skills
  Running helmfile sync...

✓ OpenClaw installed with 5 default skills!
```

### Adding a community skill

```bash
$ obol openclaw skills add github.com/ethbuilder/validator-skill
Fetching github.com/ethbuilder/validator-skill...
  ✓ Found valid SKILL.md (name: validator-ops, description: Ethereum validator management)
  ✓ Added to skills/ethbuilder/validator-ops/

Run 'obol openclaw skills sync default' to deploy

$ obol openclaw skills sync default
Syncing skills to cluster...
  ✓ Skills ConfigMap updated: openclaw-default-skills
  Running helmfile sync...
✓ Skills deployed (6 skills)
```

### Updating skills

```bash
$ obol openclaw skills update --all
Checking for updates...
  @obol/kubernetes     v1.0.0 → v1.1.0  (updated)
  @obol/ethereum       v1.0.0            (up to date)
  @obol/monitoring     v1.0.0 → v1.0.1  (updated)
  @obol/network-ops    v1.0.0            (up to date)
  @obol/tunnel         v1.0.0            (up to date)

Updated 2 skill(s). Run 'obol openclaw skills sync default' to deploy.
```

---

## Open Questions

1. **ConfigMap size limit**: With many skills, we may hit the 1MB etcd limit. Should we split into multiple ConfigMaps or use a PVC-based approach for large skill sets?

2. **Skill dependencies**: Should skills be able to declare dependencies on other skills? (e.g., `@obol/ethereum` depends on `@obol/kubernetes`). Adds complexity but prevents broken skill chains.

3. **Private repository support**: Should we support `GITHUB_TOKEN` for private repos from day one, or add it later? The fetch code should accept it but the UX can wait.

4. **Helm chart init container (Phase 4)**: This requires upstream chart changes. Should we propose the chart changes early and develop in parallel, or wait until the CLI-side is proven?

5. **Skill validation**: Should `skills add` validate that the skill's `requires.bins` are available in the target pod image, or just warn? Strict validation prevents broken skills but may be too rigid.

6. **Community skill registry**: Beyond `@obol/` shorthand, should there be a discoverable registry (like npm) for community skills? Or is GitHub search + convention (`openclaw-skill-*` repos) sufficient?

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| GitHub API rate limiting | Medium | Low | Support `GITHUB_TOKEN`, cache downloads, embedded fallback |
| ConfigMap size limit | Low | Medium | Monitor archive sizes, split if needed, document limits |
| Upstream chart incompatibility | Low | High | Test against pinned chart version, coordinate with chart maintainers |
| Stale/broken community skills | Medium | Low | Validation on `skills add`, clear error messages, `skills check` command |
| Network unavailable during init | Medium | Medium | Embedded fallback skills, graceful degradation |

---

## Success Criteria

- [ ] `obol openclaw skills add @obol/kubernetes` fetches and installs the skill in <5 seconds
- [ ] `obol agent init` installs default skills automatically on first deploy
- [ ] `obol openclaw skills list` shows all installed skills with version info
- [ ] Community skills from arbitrary GitHub repos work without special configuration
- [ ] Existing `skills sync --from <dir>` workflow continues to work unchanged
- [ ] Default Obol skills provide meaningful agent capabilities for stack operations
- [ ] Skills survive pod restarts (ConfigMap-backed persistence)
- [ ] Lock file enables reproducible skill sets across environments


