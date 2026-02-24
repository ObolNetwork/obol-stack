# CLAUDE.md

## Project Overview

Obol Stack: framework for AI agents running decentralised infrastructure locally. CLI manages a k3d cluster with OpenClaw AI agent, dynamically deployable blockchain networks, and Cloudflare tunnel access. Each network install creates a uniquely-namespaced deployment allowing multiple simultaneous instances.

## Build, Test, and Run Commands

### Building

```bash
just build                                    # Build with version info
go build -o .workspace/bin/obol ./cmd/obol    # Build to specific location
go build ./...                                # Check compilation
```

### Testing

```bash
go test ./...    # All unit tests
go test -v -run 'TestBuildLLMSpyRoutedOverlay_Anthropic' ./internal/openclaw/   # Single test

# Integration tests (requires running cluster + Ollama)
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol   # MUST rebuild after code changes
go test -tags integration -v -timeout 15m ./internal/openclaw/
go test -tags integration -v -run 'TestIntegration_OllamaInference' -timeout 10m ./internal/openclaw/
```

Integration tests use `//go:build integration` and skip gracefully when prerequisites missing.

### Cluster & Dev Mode

```bash
just up          # obol cluster init + up
just down        # obol cluster down + purge
just install     # Run obolup.sh
just clean       # Remove build artifacts
OBOL_DEVELOPMENT=true ./obolup.sh   # One-time dev setup, uses .workspace/
```

## Architecture Overview

### Two-Part System

1. **obolup.sh** - Bootstrap installer
2. **obol CLI** - Go binary for stack/network management

### Core Design Principles

1. **Deployment-centric**: Unique deployment instance per network install with own namespace
2. **Local-first**: Runs on local machine via k3d (Kubernetes in Docker)
3. **XDG-compliant**: Standard Linux filesystem layout
4. **Unique namespaces**: Petname-generated IDs (e.g., `ethereum-nervous-otter`)
5. **Two-stage templating**: CLI flags -> Go templates -> Helmfile -> K8s resources
6. **Development mode**: `.workspace/` directory with `go run` wrapper

### Routing and Gateway API

Traefik with Kubernetes Gateway API. Controller in `traefik` namespace, GatewayClass `traefik`, Gateway `traefik-gateway`.

HTTPRoutes: `/` -> `obol-frontend`, `/rpc` -> `erpc`, `/ethereum-<id>/execution`, `/ethereum-<id>/beacon`, `/aztec-<id>`, `/helios-<id>`

## Bootstrap Installer: obolup.sh

Self-contained bash script: validates Docker, creates XDG directories, installs `obol` CLI + pinned dependencies (kubectl, helm, k3d, helmfile, k9s), configures PATH and /etc/hosts, optionally bootstraps cluster.

### Installation Modes

**Production** (`bash <(curl -s https://stack.obol.org)`): Config `~/.config/obol/`, Data `~/.local/share/obol/`, Binaries `~/.local/bin/`

**Development** (`OBOL_DEVELOPMENT=true ./obolup.sh`): All in `.workspace/`, wrapper runs `go run ./cmd/obol`

### Dependency Management

Pinned versions (lines 50-57): kubectl 1.35.0, helm 3.19.4, k3d 5.8.3, helmfile 1.2.3, k9s 0.50.18, helm-diff 3.14.1

Smart install: check global binary -> symlink if version >= pinned -> else download pinned version.

### Binary Installation

**Dev mode** (lines 281-306): wrapper at `$OBOL_BIN_DIR/obol` runs `go run -a ./cmd/obol "$@"`

**Prod mode** (lines 408-466): `OBOL_RELEASE` env var controls — `latest` (default) tries GitHub release then source, specific tag downloads that release. Build from source (lines 361-406) clones repo and builds with ldflags.

### System Configuration

**PATH** (lines 1160-1223): auto-detects shell profile, interactive prompts or `OBOL_MODIFY_PATH=yes`, supports `curl | bash` via `/dev/tty`

**/etc/hosts** (lines 995-1069): adds `127.0.0.1 obol.stack`, requires sudo, graceful fallback

**Bootstrap** (lines 1226-1297): interactive post-install offers `obol bootstrap` (hidden cmd: `stack init` + `stack up` + browser launch)

## Obol CLI: cmd/obol/main.go

Framework: urfave/cli/v2 with custom help template

```
obol
├── stack {init, up, down, purge}
├── network {list, install {ethereum, helios, aztec}, delete}
├── model {setup, status}
├── openclaw {onboard, setup, sync, list, delete, dashboard, token, cli, skills {list, add, remove, sync}}
├── kubectl/helm/helmfile/k9s (passthrough with KUBECONFIG)
├── app {install, sync, list, delete}
├── tunnel {status, login, provision, restart, logs}
├── agent {init}
├── inference {serve}
├── version
└── bootstrap (hidden)
```

### Network Command Implementation

**Dynamic subcommand generation** (lines 62-146): reads embedded networks from `internal/embed/networks/`, parses `helmfile.yaml.gotmpl` annotations, auto-generates CLI flags:
```yaml
# @enum mainnet,sepolia,hoodi
# @default mainnet
# @description Blockchain network to deploy
- network: {{.Network}}
```
Becomes `--network` flag with enum validation and default.

### Passthrough Commands

Pattern (lines 130-286):
```go
{
    Name:            "kubectl",
    SkipFlagParsing: true,
    Action: func(c *cli.Context) error {
        kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
        cmd := exec.Command(kubectlPath, c.Args().Slice()...)
        cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
        cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
        return cmd.Run()
    },
}
```
Auto-sets KUBECONFIG, preserves exit codes. Binary location: `$OBOL_BIN_DIR/<tool>`.

### Configuration System

`internal/config/config.go`:
```go
type Config struct {
    ConfigDir string  // ~/.config/obol or .workspace/config
    DataDir   string  // ~/.local/share/obol or .workspace/data
    BinDir    string  // ~/.local/bin or .workspace/bin
}
```
Precedence: `OBOL_CONFIG_DIR` -> `XDG_CONFIG_HOME/obol` -> `~/.config/obol`. `OBOL_DEVELOPMENT=true` switches to `.workspace/`.

## Network Management System

### Embedded Networks

Location: `internal/embed/networks/` — ethereum (values.yaml.gotmpl, helmfile.yaml, Chart.yaml, templates/), helios, aztec (values.yaml.gotmpl, helmfile.yaml each).

### Two-Stage Templating

**Stage 1** (Go templates -> values.yaml): `values.yaml.gotmpl` with annotations processed by CLI into plain `values.yaml`:
```yaml
# @enum mainnet,sepolia,hoodi
# @default mainnet
network: {{.Network}}

# @enum reth,geth,nethermind,besu,erigon,ethereumjs
# @default reth
executionClient: {{.ExecutionClient}}
```
`id` is NOT in values.yaml — passed separately via directory structure.

**Stage 2** (Helmfile processes values): `helmfile.yaml` uses `{{ .Values.* }}` syntax:
```yaml
releases:
  - name: ethereum-pvcs
    namespace: ethereum-{{ .Values.id }}
    values:
      - network: '{{ .Values.network }}'
        executionClient: '{{ .Values.executionClient }}'
```
Receives values from `--state-values-file values.yaml --state-values-set id=<id>`, substitutes references, generates final K8s YAML.

### Unique Namespace Pattern

Pattern: `<network>-<id>`. ID is user-specified (`--id prod`) or auto-generated petname (`github.com/dustinkirkland/golang-petname`). Stored in directory structure: `~/.config/obol/networks/<network>/<id>/`. Passed to Helmfile via `--state-values-set id=<id>`.

Benefits: multiple simultaneous deployments, isolated resources, independent lifecycle, simple cleanup (delete namespace), predictable naming.

```bash
obol network install ethereum --network=mainnet                        # auto-ID: knowing-wahoo -> ethereum-knowing-wahoo
obol network install ethereum --id prod --network=mainnet              # user ID: prod -> ethereum-prod
obol network install ethereum --id hoodi-test --network=hoodi          # both run simultaneously
```

### Network Configuration Flow

**Install** (config generation only):
```
obol network install ethereum --network=hoodi --execution-client=geth --id my-node
  -> Check dir exists: ~/.config/obol/networks/ethereum/my-node/ (fail unless --force)
  -> Parse values.yaml.gotmpl -> extract fields + annotations (sorted by line number)
  -> Collect overrides (id separate, not as template field)
  -> Template values.yaml.gotmpl: populate {{.Network}}, {{.ExecutionClient}} (NOT {{.Id}})
  -> Validate YAML syntax
  -> Write to: ~/.config/obol/networks/ethereum/my-node/values.yaml
  -> Copy helmfile.yaml.gotmpl, Chart.yaml, templates/
```

**Sync** (deployment):
```
obol network sync ethereum/my-node
  -> Extract id from path: "my-node"
  -> helmfile sync --state-values-file values.yaml --state-values-set id=my-node
  -> Deploys to namespace: ethereum-my-node
```

**Delete**: delete K8s namespace -> delete PVCs -> remove config dir

## Directory Structure

### Production Layout

```
~/.config/obol/
├── k3d.yaml, .cluster-id, kubeconfig.yaml
├── defaults/ {helmfile.yaml, base/ {Chart.yaml, templates/local-path.yaml}, values/ {erpc.yaml.gotmpl, obol-frontend.yaml.gotmpl}}
└── networks/<network>/<id>/ {values.yaml, helmfile.yaml, Chart.yaml, templates/}

~/.local/bin/ {obol, kubectl, helm, k3d, helmfile, k9s, obolup.sh}

~/.local/share/obol/<cluster-id>/networks/ {ethereum_knowing-wahoo/, ethereum_prod/, helios_united-bison/, aztec_staging/}
```

### Development Layout

```
.workspace/
├── bin/ {obol (go run wrapper), kubectl, helm, k3d, helmfile, k9s}
├── config/ {k3d.yaml, .cluster-id, kubeconfig.yaml, defaults/, networks/}
└── data/networks/ {ethereum_nervous-otter/, helios_laughing-elephant/}
```

## Stack Lifecycle

### Init (`obol stack init`)

`internal/stack/stack.go`: Generate cluster ID (petname) -> get absolute paths -> read embedded k3d config -> replace `{{CLUSTER_ID}}`, `{{DATA_DIR}}`, `{{CONFIG_DIR}}` -> write k3d.yaml -> copy defaults -> store .cluster-id. Template from `internal/embed/k3d-config.yaml` must use absolute paths (Docker requirement), resolved at init not runtime.

### Up (`obol stack up`)

Read .cluster-id -> verify k3d.yaml -> `k3d cluster create --config k3d.yaml` (1 server + 3 agents, volume mounts, ports 8080:80 + 8443:443) -> k3s auto-applies defaults manifests -> export kubeconfig.

k3d config: image `rancher/k3s:v1.31.4-k3s1`, label `obol.cluster-id={{CLUSTER_ID}}`, feature gate `KubeletInUserNamespace=true`, ulimit `nofile 26677`.

### Down (`obol stack down`)

`k3d cluster delete <name>`. Preserves config and data directories.

### Purge (`obol stack purge`)

Runs down -> removes config dir -> `--force` removes data dir. Always preserves `$OBOL_BIN_DIR`. `-f` required for root-owned PVCs.

## Default Stack Resources

Location: `~/.config/obol/defaults/`. Auto-deployed on `stack up` via k3s manifests mount (`/var/lib/rancher/k3s/server/manifests/defaults/`). Uses k3s HelmChart CRD.

Components: Base (local-path provisioner), ERPC (ns: `erpc`, route: `/rpc`), Obol Frontend (ns: `obol-frontend`, route: `/`), Cloudflared (ns: `traefik`), Monitoring (Prometheus + kube-prometheus-stack, ns: `monitoring`), Reloader (ConfigMap/Secret watch).

## Dynamic eRPC Upstream Management

Local Ethereum nodes auto-register as highest-priority eRPC upstream. Reads hit local node first; writes (`eth_sendRawTransaction`, `eth_sendTransaction`) blocked on local and routed to remote providers.

Key functions (`internal/network/erpc.go`): `RegisterERPCUpstream()` (after sync, position 0), `DeregisterERPCUpstream()` (before delete), `patchERPCUpstream()` (reads/patches ConfigMap, restarts deployment).

Chain IDs: mainnet=1, hoodi=560048, sepolia=11155111. Write protection via `ignoreMethods` + `selectionPolicy` routing writes to `obol-rpc-mainnet`.

```
obol network sync ethereum/my-node
  -> helmfile sync (deploys execution + consensus clients)
  -> RegisterERPCUpstream(cfg, "ethereum", "my-node")
    -> patches erpc-config ConfigMap: adds local-ethereum-my-node upstream at position 0
    -> restarts eRPC deployment
  -> reads route: local node (priority) -> obol-rpc-mainnet (fallback)
  -> writes route: obol-rpc-mainnet only (local node blocks write methods)
```

## LLM Configuration Architecture

Two-tier: cluster-wide llmspy proxy handles provider communication; each app instance sees simplified single-provider view.

### Tier 1: Global llmspy Gateway (`llm` namespace)

Shared OpenAI-compatible proxy routing to Ollama/Anthropic/OpenAI. Defined in `internal/embed/infrastructure/base/templates/llm.yaml`:

| Resource | Type | Purpose |
|----------|------|---------|
| `llm` | Namespace | LLM infrastructure |
| `llmspy-config` | ConfigMap | `llms.json` + `providers.json` |
| `llms-secrets` | Secret | API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) — empty default |
| `llmspy` | Deployment | `ghcr.io/obolnetwork/llms:3.0.32-obol.1-rc.1`, port 8000 |
| `llmspy` | Service | `llmspy.llm.svc.cluster.local:8000` |
| `ollama` | Service (ExternalName) | Routes to host Ollama via `{{OLLAMA_HOST}}` |

**ConfigureLLMSpy()** (`internal/model/model.go`): patches Secret with API key -> enables provider in ConfigMap llms.json -> restarts Deployment (60s timeout).

CLI (`cmd/obol/model.go`): `obol model setup --provider=anthropic --api-key=sk-...`, `obol model status`. Interactive prompt if flags omitted. Ollama enabled by default; cloud providers disabled until configured. Init container copies ConfigMap to writable emptyDir.

### Tier 2: Per-Instance Application Config

Each app instance has own model config from Helm chart values. Helmfile merges: `values.yaml` (chart defaults) then `values-obol.yaml` (overlay from `generateOverlayValues()`).

Provider rendering (`_helpers.tpl` lines 167-189): iterates `.Values.models`, emits enabled providers with `baseUrl`, `apiKey`, `models` array. `api` field emitted only if non-empty (required for llmspy routing).

### The llmspy-Routed Overlay Pattern

On cloud provider setup: (1) `llm.ConfigureLLMSpy()` patches cluster-wide gateway, (2) `buildLLMSpyRoutedOverlay()` creates overlay with "llmspy" provider pointing at gateway, cloud model listed with `llmspy/` prefix, `api: openai-completions`, default "ollama" disabled.

```
App -> model: "llmspy/claude-sonnet-4-5-20250929", api: "openai-completions"
  -> llmspy (llm ns, :8000) -> resolves to anthropic provider -> Anthropic API
```

Overlay example (`values-obol.yaml`):
```yaml
models:
  llmspy:
    enabled: true
    baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
    api: openai-completions
    apiKeyEnvVar: LLMSPY_API_KEY
    apiKeyValue: llmspy-default
    models:
      - id: claude-sonnet-4-5-20250929
        name: Claude Sonnet 4.5
  ollama: {enabled: false}
  anthropic: {enabled: false}
  openai: {enabled: false}
```

Default Ollama path still uses "ollama" provider name pointing at llmspy.

### LLM Summary

| Aspect | Tier 1 (llmspy) | Tier 2 (App instance) |
|--------|-----------------|----------------------|
| Scope | Cluster-wide | Per-deployment |
| Namespace | `llm` | `<app>-<id>` |
| Config | ConfigMap `llmspy-config` | ConfigMap `<release>-config` |
| Secrets | Secret `llms-secrets` | Secret `<release>-secrets` |
| Configure via | `obol model setup` | `obol openclaw setup <id>` |
| Providers | Real (Ollama, Anthropic, OpenAI) | Cloud: "llmspy" virtual; Default: "ollama" via llmspy |
| API field | N/A (provider-native) | `openai-completions` for llmspy routing |

## OpenClaw Skills System

SKILL.md files (with optional scripts/references) giving the AI agent domain capabilities. Ships 20 embedded skills, supports runtime management via CLI.

### Delivery: Host-Path PVC Injection

Writes to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/` -> maps to `/data/.openclaw/skills/` in container via k3d volumes + local-path-provisioner. No 1MB ConfigMap limit, works before pod readiness, survives restarts, supports binaries.

### Default Skills (20)

**Infrastructure**:

| Skill | Contents | Purpose |
|-------|----------|---------|
| `ethereum-networks` | SKILL.md, scripts/{rpc.sh,rpc.py}, references/{erc20-methods.md,common-contracts.md} | Read-only Ethereum queries via cast/eRPC |
| `obol-stack` | SKILL.md, scripts/kube.py | K8s cluster diagnostics via ServiceAccount API |
| `distributed-validators` | SKILL.md, references/api-examples.md | Obol DVT monitoring, operator audit, exit coordination |

**Ethereum Development**:

| Skill | Purpose |
|-------|---------|
| `addresses` | Verified contract addresses across chains (DeFi, tokens, bridges, ERC-8004) |
| `building-blocks` | OpenZeppelin patterns, DEX, oracles, access control |
| `concepts` | State machines, incentive design, gas mechanics, EOAs vs contracts |
| `gas` | Gas optimization, L2 fees, estimation |
| `indexing` | The Graph, Dune, event indexing |
| `l2s` | L2 comparison (Base, Arbitrum, Optimism, zkSync) |
| `orchestration` | End-to-end dApp build (Scaffold-ETH 2) + AI agent commerce |
| `security` | Vulnerability patterns, reentrancy, flash loans, MEV protection |
| `standards` | ERC-8004, x402, EIP-3009, EIP-7702, ERC-4337 |
| `ship` | Onchain vs offchain architecture, chain selection, agent patterns |
| `testing` | Foundry testing (unit, fuzz, fork, invariant) |
| `tools` | Foundry, Hardhat, Scaffold-ETH 2, verification |
| `wallets` | EOAs, Safe multisig, EIP-7702, key safety for AI agents |

**Frontend & UX**: `frontend-playbook` (IPFS/Vercel/ENS deployment), `frontend-ux` (wallet connection, tx flows), `qa` (testing strategy, CI/CD), `why` (why Ethereum + AI agent angle)

All Ethereum dev + frontend skills contain only SKILL.md.

### Skill Delivery Flow

```
Onboard/Sync:
  1. stageDefaultSkills(deploymentDir) -> copies from internal/embed/skills/ (skips if exists)
  2. injectSkillsToVolume(cfg, id, deploymentDir) -> copies to host PVC path
  3. doSync() -> helmfile sync -> OpenClaw auto-discovers on startup
```

### Instance Resolution

`ResolveInstance()` for all subcommands except `onboard`/`list`: 0 instances -> error, 1 -> auto-select, 2+ -> CLI arg must match.

### CLI Commands

```bash
obol openclaw skills list                   # list installed skills
obol openclaw skills add <package>          # add via openclaw CLI in pod
obol openclaw skills remove <name>          # remove via openclaw CLI in pod
obol openclaw skills sync                   # re-inject embedded defaults
obol openclaw skills sync --from <dir>      # push custom skills
```

## Network Install Implementation Details

### Template Field Parser

`internal/network/parser.go` - `ParseTemplateFields()`: reads `values.yaml.gotmpl`, parses Go template AST for field references, extracts `@enum`, `@default`, `@description` annotations. Generates `TemplateField{Name, FlagName, DefaultValue, EnumValues, Description, Required}`. Fields sorted by line number for deterministic ordering.

### CLI Flag Generation

`cmd/obol/network.go` - `buildNetworkInstallCommands()`: for each network, parses template fields, builds `cli.Flag` with enum validation, creates dynamic subcommand. Flag naming: `ExecutionClient` -> `--execution-client`.

### Install Implementation

`internal/network/network.go` - `Install()`: generate ID -> check dir (fail unless --force) -> parse template -> build data map (no id) -> template values.yaml.gotmpl -> validate YAML (`gopkg.in/yaml.v3`) -> write values.yaml -> copy files -> sync deploys via `helmfile sync --state-values-file values.yaml --state-values-set id=<id>`.

### Validation and Safety

- **Overwrite protection**: fails if dir exists unless `--force`/`-f`
- **YAML validation**: parsed before write, catches malformed values early
- **Deterministic ordering**: fields sorted by line number for consistent `--help`

## Key Implementation Patterns

### Environment Variables

Precedence: `OBOL_CONFIG_DIR` -> `XDG_CONFIG_HOME` -> `~/.config/obol`.
```bash
if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
    OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$WORKSPACE_DIR/config}"
else
    OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$XDG_CONFIG_HOME/obol}"
fi
```

### Binary Discovery

Three-tier: global binary -> existing in OBOL_BIN_DIR -> download. Version comparison via `version_ge()`, symlinks if sufficient.

### Kubeconfig

All passthrough commands auto-set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml`.

### Error Handling

Graceful degradation: failed deps continue with warnings, helm-diff non-blocking, PATH falls back to manual instructions.

## Development Workflow

```bash
OBOL_DEVELOPMENT=true ./obolup.sh   # one-time
# edit code, run immediately (no compilation): obol network list
# data in .workspace/
```

### Adding New Networks

1. Create `internal/embed/networks/<name>/helmfile.yaml.gotmpl` with annotations
2. Build or use dev mode
3. CLI auto-generates `obol network install <name>` with flags

### Testing Networks

```bash
obol network list
obol network install ethereum --help
obol network install ethereum --network=hoodi --execution-client=geth
obol kubectl get namespaces | grep ethereum
obol kubectl get all -n ethereum-<id>
obol network delete ethereum-<id> --force
```

## Critical Design Constraints

1. **Absolute paths required**: Docker volume mounts need absolute paths (`filepath.Abs()`)
2. **Template resolution timing**: k3d config values substituted at `init`, not `up`
3. **Unique namespaces**: prevent resource collisions
4. **Two-stage templating**: Stage 1 (CLI) -> Stage 2 (Helmfile) separation is critical
5. **Local source of truth**: config on disk enables future management

### Common Pitfalls

1. Relative paths in k3d config fail with Docker
2. k3d.yaml must have absolute paths before cluster creation
3. Namespace collisions without unique namespaces
4. Root-owned PVCs need `-f` flag to remove
5. Unquoted YAML special chars (`:`, `[`, `{`) break syntax

### Future Work

**ERPC**: extract to separate helmfile, auto-discover endpoints, dynamic registration, unified RPC

**Networks**: `obol network list --installed`, `update`, `logs`, better namespace discovery

## References

### Key Files

**Bootstrap/CLI**: `obolup.sh`, `cmd/obol/main.go`

**Core**: `internal/config/config.go`, `internal/stack/stack.go`, `internal/network/network.go`, `internal/embed/embed.go`

**LLM/OpenClaw**: `internal/model/model.go` (ConfigureLLMSpy), `cmd/obol/model.go`, `internal/embed/infrastructure/base/templates/llm.yaml`, `internal/openclaw/openclaw.go` (Setup, interactiveSetup, generateOverlayValues, buildLLMSpyRoutedOverlay), `internal/openclaw/import.go` (DetectExistingConfig, TranslateToOverlayYAML), `internal/openclaw/chart/` (values.yaml, templates/_helpers.tpl)

**Embedded assets**: `internal/embed/k3d-config.yaml`, `internal/embed/networks/` (ethereum/, helios/, aztec/ helmfile.yaml.gotmpl), `internal/embed/defaults/`, `internal/embed/infrastructure/`, `internal/embed/skills/`

**Skills**: `internal/openclaw/resolve.go` (ResolveInstance, ListInstanceIDs), `internal/embed/skills/ethereum-networks/` (SKILL.md + scripts/ + references/), `internal/embed/skills/obol-stack/` (SKILL.md + scripts/kube.py), `internal/embed/skills/distributed-validators/` (SKILL.md + references/), `internal/embed/skills/addresses/`, `internal/embed/skills/*/SKILL.md` (17 additional), `internal/embed/embed_skills_test.go`, `internal/openclaw/skills_injection_test.go`, `tests/skills_smoke_test.py`, `internal/openclaw/openclaw.go` (stageDefaultSkills, injectSkillsToVolume, skillsVolumePath, SkillAdd/Remove/List/Sync), `cmd/obol/openclaw.go`

**Testing**: `internal/openclaw/integration_test.go` (Ollama, Anthropic, OpenAI inference through llmspy), `internal/openclaw/overlay_test.go`, `internal/openclaw/import_test.go`, `internal/openclaw/resolve_test.go`, `internal/stack/stack_test.go`, `internal/tunnel/tunnel_test.go`, `internal/dns/resolver_test.go`

**Build/Version**: `justfile`, `VERSION`, `internal/version/version.go`

**CI/CD** (`.github/workflows/`): `release.yml` (multi-platform builds, GitHub releases), `docker-publish-openclaw.yml` (Docker image + Trivy scan)

**Docs**: `README.md`, `plan.md`, `CONTRIBUTING.md`

**Developer Skills**: `.agents/skills/obol-stack-dev/` (LLM routing dev/test/validate)

### External Dependencies

**Required**: Docker 20.10.0+, Go 1.25+ (building from source)

**Installed by obolup.sh**: kubectl 1.35.0, helm 3.19.4, k3d 5.8.3, helmfile 1.2.3, k9s 0.50.18, helm-diff 3.14.1

**Go packages**: `github.com/urfave/cli/v2`, `github.com/dustinkirkland/golang-petname`, stdlib `embed`

## Updating This File

Update when: major architecture changes, new systems/patterns introduced, significant implementation changes, new workflows established. Confirm with user before updating.

## Related Codebases

| Resource | Path | Description |
|----------|------|-------------|
| obol-stack-front-end | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` | Next.js web dashboard |
| obol-stack-docs | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-docs` | MkDocs documentation site |
| OpenClaw | `/Users/bussyjd/Development/Obol_Workbench/openclaw` | OpenClaw AI assistant (upstream) |
| llmspy | `/Users/bussyjd/Development/R&D/llmspy` | LLM proxy/router (upstream) |
