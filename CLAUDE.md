# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Obol Stack is a framework for AI agents to run decentralised infrastructure locally. It provides a simplified CLI experience for managing a k3d cluster with an AI agent (OpenClaw), dynamically deployable blockchain networks, and public access via Cloudflare tunnels. Each network installation creates a uniquely-namespaced deployment, allowing multiple instances of the same network type to run simultaneously.

## Build, Test, and Run Commands

### Building

```bash
# Build with version info (recommended)
just build

# Build to specific location (e.g., for integration tests)
go build -o .workspace/bin/obol ./cmd/obol

# Build all packages (check compilation)
go build ./...
```

### Testing

```bash
# Run all unit tests
go test ./...

# Run a single test
go test -v -run 'TestBuildLLMSpyRoutedOverlay_Anthropic' ./internal/openclaw/

# Run integration tests (requires running cluster + Ollama)
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol   # MUST rebuild after code changes
go test -tags integration -v -timeout 15m ./internal/openclaw/

# Run a specific integration test
go test -tags integration -v -run 'TestIntegration_OllamaInference' -timeout 10m ./internal/openclaw/
```

Integration tests use `//go:build integration` and skip gracefully when prerequisites (cluster, Ollama, API keys) are missing.

### Cluster Management

```bash
just up          # obol cluster init + up
just down        # obol cluster down + purge
just install     # Run obolup.sh
just clean       # Remove build artifacts
```

### Development Mode

```bash
OBOL_DEVELOPMENT=true ./obolup.sh   # One-time setup, uses .workspace/ directory
# Changes to Go code reflected immediately via `go run` wrapper
```

## Architecture Overview

### Two-Part System

1. **obolup.sh** - Bootstrap installer that sets up the environment
2. **obol CLI** - Go-based binary for stack and network management

### Core Design Principles

1. **Deployment-centric**: Each network installation creates a unique deployment instance with its own namespace
2. **Local-first**: Runs entirely on local machine using k3d (Kubernetes in Docker)
3. **XDG-compliant**: Follows Linux filesystem standards for configuration
4. **Unique namespaces**: Petname-generated IDs prevent naming conflicts (e.g., `ethereum-nervous-otter`)
5. **Two-stage templating**: CLI flags → Go templates → Helmfile → Kubernetes resources
6. **Development mode**: Local `.workspace/` directory with `go run` wrapper for rapid development

### Routing and Gateway API

Obol Stack uses Traefik with the Kubernetes Gateway API for HTTP routing.

- Controller: Traefik Helm chart (`traefik` namespace)
- GatewayClass: `traefik`
- Gateway: `traefik-gateway` in `traefik` namespace
- HTTPRoute patterns:
  - `/` → `obol-frontend`
  - `/rpc` → `erpc`
  - `/ethereum-<id>/execution` and `/ethereum-<id>/beacon`
  - `/aztec-<id>` and `/helios-<id>`

## Bootstrap Installer: obolup.sh

### Purpose

The bootstrap installer is a self-contained bash script that:
- Validates prerequisites (Docker daemon)
- Creates XDG-compliant directory structure
- Installs the `obol` CLI binary
- Installs pinned dependency versions (kubectl, helm, k3d, helmfile, k9s)
- Configures system (PATH, /etc/hosts)
- Optionally bootstraps the cluster

### Installation Modes

#### Production Mode (Default)
```bash
bash <(curl -s https://stack.obol.org)
```

Uses XDG Base Directory specification:
- Config: `~/.config/obol/`
- Data: `~/.local/share/obol/`
- Binaries: `~/.local/bin/`

#### Development Mode
```bash
OBOL_DEVELOPMENT=true ./obolup.sh
```

Uses local workspace:
- All files: `.workspace/`
- Installs wrapper script that runs `go run ./cmd/obol`
- No compilation needed - changes reflected immediately

### Dependency Management

**Pinned versions** (lines 50-57):
```bash
KUBECTL_VERSION="1.35.0"
HELM_VERSION="3.19.4"
K3D_VERSION="5.8.3"
HELMFILE_VERSION="1.2.3"
K9S_VERSION="0.50.18"
HELM_DIFF_VERSION="3.14.1"
```

**Smart installation logic**:
1. Check for global binary (outside OBOL_BIN_DIR)
2. If found and version >= pinned version, create symlink
3. Otherwise, download pinned version to OBOL_BIN_DIR
4. Handle broken symlinks gracefully

### Binary Installation Strategies

**Development mode** (lines 281-306):
- Creates wrapper script at `$OBOL_BIN_DIR/obol`
- Wrapper runs `go run -a ./cmd/obol "$@"`
- Finds project root automatically
- No compilation needed

**Production mode** (lines 408-466):
- Controlled by `OBOL_RELEASE` environment variable
- `OBOL_RELEASE=latest` (default): Try download latest release, fallback to build from source
- `OBOL_RELEASE=v0.1.0`: Download specific release
- Downloads prebuilt binaries from GitHub releases
- Falls back to building from source if download fails

**Build from source** (lines 361-406):
- Clones repository
- Injects version information via ldflags
- Builds with `go build -ldflags "..." ./cmd/obol`

### System Configuration

**PATH configuration** (lines 1160-1223):
- Auto-detects shell profile (.bashrc, .zshrc, .bash_profile, etc.)
- Interactive mode: Prompts user to auto-add or show manual instructions
- Non-interactive mode: Respects `OBOL_MODIFY_PATH=yes` environment variable
- Works with `curl | bash` via `/dev/tty` detection

**/etc/hosts configuration** (lines 995-1069):
- Adds `127.0.0.1 obol.stack` entry
- Requires sudo privileges
- Graceful handling: manual instructions if sudo fails
- Checks existing entries to avoid duplicates

### Bootstrap Flow

**Post-install prompt** (lines 1226-1297):
- Interactive mode: Offers to start cluster immediately
- Runs `obol bootstrap` command (hidden command in CLI)
- Bootstrap command handles `stack init` + `stack up` + browser launch
- Fallback: Shows manual instructions

## Obol CLI: cmd/obol/main.go

### Architecture

**CLI Framework**: urfave/cli/v3 with custom help template

**Command Structure**:
```
obol
├── stack (lifecycle management)
│   ├── init
│   ├── up
│   ├── down
│   └── purge
├── network (deployment management)
│   ├── list
│   ├── install
│   │   ├── ethereum (dynamically generated)
│   │   ├── helios (dynamically generated)
│   │   └── aztec (dynamically generated)
│   └── delete
├── model (LLM provider management)
│   ├── setup
│   └── status
├── openclaw (OpenClaw AI assistant)
│   ├── onboard
│   ├── setup
│   ├── sync
│   ├── list
│   ├── delete
│   ├── dashboard
│   ├── token
│   ├── cli
│   └── skills (manage OpenClaw skills)
│       ├── list
│       ├── add
│       ├── remove
│       └── sync
├── kubectl (passthrough with KUBECONFIG)
├── helm (passthrough with KUBECONFIG)
├── helmfile (passthrough with KUBECONFIG)
├── k9s (passthrough with KUBECONFIG)
├── app (application management)
│   ├── install
│   ├── sync
│   ├── list
│   └── delete
├── tunnel (Cloudflare tunnel management)
│   ├── status
│   ├── login
│   ├── provision
│   ├── restart
│   └── logs
├── agent (AI agent management)
│   └── init
├── service (x402 inference gateway)
│   ├── create   (create deployment config)
│   ├── deploy   (create container + start gateway; --vm for Apple Containerization)
│   ├── list     (list saved deployments)
│   ├── info     (show deployment details)
│   ├── delete   (remove deployment + optionally purge SE key)
│   ├── pubkey   (print Secure Enclave public key)
│   └── serve    (start gateway directly from flags; no deployment record)
├── version
└── bootstrap (hidden, used by installer)
```

### Network Command Implementation

**Dynamic subcommand generation** (lines 62-146):
1. Reads embedded networks from `internal/embed/networks/`
2. Parses each network's `helmfile.yaml.gotmpl` for environment variable annotations
3. Generates CLI flags automatically from annotations:
   ```yaml
   # @enum mainnet,sepolia,hoodi
   # @default mainnet
   # @description Blockchain network to deploy
   - network: {{.Network}}
   ```
   Becomes: `--network` flag with enum validation and default value

**Network install flow**:
1. User runs: `obol network install ethereum --network=hoodi --execution-client=geth`
2. CLI collects flag values into `overrides` map
3. Validates enum constraints
4. Calls `network.Install(cfg, "ethereum", overrides)`
5. Network package:
   - Creates temp directory
   - Copies embedded network files
   - Sets environment variables from overrides
   - Runs `helmfile sync` with environment variables
   - Cleans up temp directory

### Passthrough Commands

**Pattern** (lines 130-286):
```go
{
    Name:            "kubectl",
    SkipFlagParsing: true,  // Pass all args directly to kubectl
    Action: func(ctx context.Context, cmd *cli.Command) error {
        kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

        // Note: local var named 'proc' to avoid shadowing the cmd *cli.Command parameter
        proc := exec.Command(kubectlPath, cmd.Args().Slice()...)
        proc.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
        proc.Stdin = os.Stdin
        proc.Stdout = os.Stdout
        proc.Stderr = os.Stderr

        return proc.Run()
    },
}
```

**Benefits**:
- User doesn't need to manually set KUBECONFIG
- Seamless integration with existing kubectl/helm workflows
- Exit codes preserved from underlying commands
- Binary location: `$OBOL_BIN_DIR/<tool>`

### Configuration System

**Config package** (`internal/config/config.go`):
```go
type Config struct {
    ConfigDir string  // ~/.config/obol or .workspace/config
    DataDir   string  // ~/.local/share/obol or .workspace/data
    BinDir    string  // ~/.local/bin or .workspace/bin
}
```

**Environment variable precedence**:
1. `OBOL_CONFIG_DIR` (override)
2. `XDG_CONFIG_HOME/obol` (XDG standard)
3. `~/.config/obol` (default)

**Development mode detection**:
- `OBOL_DEVELOPMENT=true` switches to `.workspace/` directories
- No state directory in development mode (logs removed)

## Network Management System

### Embedded Networks

**Location**: `internal/embed/networks/`

**Structure**:
```
networks/
├── ethereum/
│   ├── values.yaml.gotmpl   # Configuration template with annotations
│   ├── helmfile.yaml        # Deployment logic (pure Helmfile syntax)
│   ├── Chart.yaml           # Optional local chart
│   └── templates/           # Optional Kubernetes resources
├── helios/
│   ├── values.yaml.gotmpl
│   └── helmfile.yaml
└── aztec/
    ├── values.yaml.gotmpl
    └── helmfile.yaml
```

### Two-Stage Templating

**Stage 1: CLI Flag Templating** (Go templates → values.yaml)

`values.yaml.gotmpl` contains configuration fields with annotations:
```yaml
# @enum mainnet,sepolia,hoodi
# @default mainnet
# @description Blockchain network to deploy
network: {{.Network}}

# @enum reth,geth,nethermind,besu,erigon,ethereumjs
# @default reth
executionClient: {{.ExecutionClient}}
```

CLI processes this template and generates `values.yaml`:
```yaml
network: mainnet
executionClient: reth
```

**Note**: `id` is NOT in `values.yaml` - it's passed separately via directory structure.

**Stage 2: Helmfile Templating** (Helmfile processes values)

`helmfile.yaml` references values using Helmfile syntax:
```yaml
releases:
  - name: ethereum-pvcs
    namespace: ethereum-{{ .Values.id }}  # Dynamic namespace
    values:
      - network: '{{ .Values.network }}'
        executionClient: '{{ .Values.executionClient }}'
```

When `helmfile sync --state-values-file values.yaml` runs:
- Reads values from `values.yaml`
- Substitutes `{{ .Values.* }}` references
- Generates final Kubernetes YAML
- Applies to cluster in unique namespace

### Unique Namespace Pattern

**Namespace generation**:
- Pattern: `<network>-<id>`
- ID can be user-specified (`--id prod`) or auto-generated (petname like `knowing-wahoo`)
- Uses `github.com/dustinkirkland/golang-petname` for auto-generation
- Examples:
  - `ethereum-knowing-wahoo` (auto-generated)
  - `ethereum-prod` (user-specified with `--id prod`)
  - `helios-united-bison` (auto-generated)
  - `aztec-staging` (user-specified)

**ID as deployment identifier**:
- `id` is NOT in `values.yaml` or `values.yaml.gotmpl` (special case)
- Determined by directory structure: `~/.config/obol/networks/<network>/<id>/`
- CLI auto-generates petname if `--id` flag not provided
- Passed to Helmfile via `--state-values-set id=<id>` during sync
- Helmfile enforces namespace: `namespace: {{ .Values.id }}`

**Benefits**:
1. **Multiple deployments**: Run mainnet + testnet simultaneously
2. **Isolated resources**: Each deployment has dedicated CPU, memory, storage
3. **Independent lifecycle**: Update/delete one without affecting others
4. **Simple cleanup**: Delete namespace removes all resources
5. **Predictable naming**: User controls ID for production deployments

**Example**:
```bash
# Auto-generated ID (development)
obol network install ethereum --network=mainnet
# Generated deployment ID: knowing-wahoo
# Creates: ~/.config/obol/networks/ethereum/knowing-wahoo/
# Namespace: ethereum-knowing-wahoo

# User-specified ID (production)
obol network install ethereum --id prod --network=mainnet
# Creates: ~/.config/obol/networks/ethereum/prod/
# Namespace: ethereum-prod

# Multiple deployments with different configs
obol network install ethereum --id mainnet-01
obol network install ethereum --id hoodi-test --network=hoodi
# Both run simultaneously, isolated in separate namespaces
```

### Network Configuration Flow

1. **Install** (config generation only):
   ```
   obol network install ethereum --network=hoodi --execution-client=geth --id my-node
        ↓
   Check if directory exists: ~/.config/obol/networks/ethereum/my-node/ (fail unless --force)
        ↓
   Parse values.yaml.gotmpl → extract field definitions + annotations (sorted by line number)
        ↓
   Collect CLI flag values into overrides map (id collected separately, not as template field)
        ↓
   Template values.yaml.gotmpl: Populate {{.Network}}, {{.ExecutionClient}} (NOT {{.Id}})
        ↓
   Validate YAML syntax of generated content
        ↓
   Write values.yaml to: ~/.config/obol/networks/ethereum/my-node/values.yaml
        ↓
   Copy helmfile.yaml.gotmpl as-is (no templating)
        ↓
   Copy other files (Chart.yaml, templates/)
   ```

2. **Sync** (deployment):
   ```
   obol network sync ethereum/my-node
        ↓
   Extract id from directory path: "my-node"
        ↓
   Run: helmfile sync --state-values-file values.yaml --state-values-set id=my-node
        ↓
   Helmfile reads values.yaml + receives id via --state-values-set
        ↓
   Substitutes {{ .Values.* }} in helmfile.yaml.gotmpl (including {{ .Values.id }})
        ↓
   Deploys to namespace: ethereum-my-node
   ```

3. **Delete**:
   ```
   obol network delete ethereum/knowing-wahoo
        ↓
   Delete Kubernetes namespace (removes all resources)
        ↓
   Delete PVCs and persistent data
        ↓
   Remove: ~/.config/obol/networks/ethereum/knowing-wahoo/
   ```

## Directory Structure

### Production Layout

```
~/.config/obol/
├── k3d.yaml                       # Generated k3d config (absolute paths)
├── .cluster-id                    # Petname-generated cluster identifier
├── kubeconfig.yaml                # Exported cluster kubeconfig
├── defaults/                      # Default stack resources (ERPC, frontend)
│   ├── helmfile.yaml
│   ├── base/                      # Base Kubernetes resources
│   │   ├── Chart.yaml
│   │   └── templates/
│   │       └── local-path.yaml
│   └── values/                    # Configuration templates
│       ├── erpc.yaml.gotmpl
│       └── obol-frontend.yaml.gotmpl
└── networks/                      # Installed network deployments
    ├── ethereum/
    │   ├── knowing-wahoo/         # First ethereum deployment
    │   │   ├── values.yaml        # Generated config (plain YAML)
    │   │   ├── helmfile.yaml      # Deployment logic (copied as-is)
    │   │   ├── Chart.yaml
    │   │   └── templates/
    │   └── prod/                  # Second ethereum deployment
    │       ├── values.yaml
    │       ├── helmfile.yaml
    │       ├── Chart.yaml
    │       └── templates/
    ├── helios/
    │   └── united-bison/
    │       ├── values.yaml
    │       └── helmfile.yaml
    └── aztec/
        └── staging/
            ├── values.yaml
            └── helmfile.yaml

~/.local/bin/                      # Binaries
├── obol                           # Obol CLI
├── kubectl                        # kubectl (or symlink)
├── helm                           # helm (or symlink)
├── k3d                            # k3d (or symlink)
├── helmfile                       # helmfile (or symlink)
├── k9s                            # k9s (or symlink)
└── obolup.sh                      # Bootstrap script copy

~/.local/share/obol/              # Persistent data
└── <cluster-id>/
    └── networks/
        ├── ethereum_knowing-wahoo/   # Blockchain data for first deployment
        ├── ethereum_prod/            # Blockchain data for second deployment
        ├── helios_united-bison/
        └── aztec_staging/
```

### Development Layout

```
.workspace/
├── bin/
│   ├── obol                       # Wrapper script (go run)
│   ├── kubectl
│   ├── helm
│   ├── k3d
│   ├── helmfile
│   └── k9s
├── config/
│   ├── k3d.yaml
│   ├── .cluster-id
│   ├── kubeconfig.yaml
│   ├── defaults/
│   │   ├── helmfile.yaml
│   │   ├── base/
│   │   └── values/
│   └── networks/
│       ├── ethereum/
│       │   └── nervous-otter/
│       ├── helios/
│       └── aztec/
└── data/                          # Persistent volumes
    └── networks/
        ├── ethereum_nervous-otter/
        └── helios_laughing-elephant/
```

## Stack Lifecycle

### Init (`obol stack init`)

**Purpose**: Initialize cluster configuration

**Operations** (`internal/stack/stack.go`):
1. Generate unique cluster ID (petname)
2. Get absolute paths for data and config directories
3. Read embedded k3d config template
4. Replace placeholders:
   - `{{CLUSTER_ID}}` → generated petname
   - `{{DATA_DIR}}` → absolute path to data directory
   - `{{CONFIG_DIR}}` → absolute path to config directory
5. Write resolved `k3d.yaml` to config directory
6. Copy embedded default applications to `defaults/` directory
7. Store cluster ID in `.cluster-id` file

**Template placeholders** (from `internal/embed/k3d-config.yaml`):
- Must use absolute paths (Docker volume mounts requirement)
- Resolved at init time, not runtime
- Ensures k3d can find volumes regardless of working directory

### Up (`obol stack up`)

**Purpose**: Start the Kubernetes cluster

**Operations**:
1. Read cluster ID from `.cluster-id`
2. Verify k3d.yaml exists
3. Run: `k3d cluster create --config k3d.yaml`
4. k3d creates cluster with:
   - 1 server + 3 agent nodes (fault tolerance)
   - Volume mounts configured (data, defaults)
   - Ports exposed: 8080:80, 8443:443
5. k3s auto-applies manifests from defaults directory
6. Export kubeconfig: `k3d kubeconfig write <cluster-name> > kubeconfig.yaml`

**k3d configuration highlights**:
- Image: `rancher/k3s:v1.31.4-k3s1`
- Container labels: `obol.cluster-id={{CLUSTER_ID}}`
- Feature gates: `KubeletInUserNamespace=true` (fixes /dev/kmsg issues)
- Ulimits: `nofile 26677` (prevents "too many open files")

### Down (`obol stack down`)

**Purpose**: Stop the cluster without deleting data

**Operations**:
1. Read cluster ID
2. Run: `k3d cluster delete <cluster-name>`
3. Preserves:
   - Config directory (k3d.yaml, kubeconfig, network configs)
   - Data directory (persistent volumes)

### Purge (`obol stack purge`)

**Purpose**: Complete removal of cluster and optionally data

**Operations**:
1. Run `stack down` to stop cluster
2. Remove config directory (k3d.yaml, kubeconfig, .cluster-id, networks/)
3. If `--force` flag: Remove data directory (persistent volumes)
4. Note: Always preserves binaries in `$OBOL_BIN_DIR`

**Important**: `-f` flag required to remove root-owned PVCs

## Default Stack Resources

### Defaults Namespace

**Location**: `~/.config/obol/defaults/`

**Purpose**: Base resources deployed automatically on `obol stack up`

**Components**:
- **Base resources**: Local path storage provisioner
- **ERPC**: Unified RPC load balancer (namespace: `erpc`, route: `/rpc`)
- **Obol Frontend**: Web management interface (namespace: `obol-frontend`, route: `/`)
- **Cloudflared**: Cloudflare Tunnel connector (namespace: `traefik`)
- **Monitoring**: Prometheus + kube-prometheus-stack (namespace: `monitoring`)
- **Reloader**: Watches ConfigMap/Secret changes and triggers pod restarts

**Deployment mechanism**:
- Defaults directory mounted to k3s: `/var/lib/rancher/k3s/server/manifests/defaults/`
- k3s auto-applies all YAML files on startup
- Uses k3s HelmChart CRD for Helm deployments

## Dynamic eRPC Upstream Management

When a local Ethereum node is deployed via `obol network install ethereum`, it is automatically registered as an upstream in the eRPC gateway. This enables local-first routing: read requests hit the local node first (lowest latency), while write methods (`eth_sendRawTransaction`, `eth_sendTransaction`) are blocked on local upstreams and routed to designated remote providers.

**Key functions** (`internal/network/erpc.go`):
- `RegisterERPCUpstream()` — called after `obol network sync`, adds local node to eRPC ConfigMap at position 0 (highest priority)
- `DeregisterERPCUpstream()` — called before `obol network delete`, removes the upstream
- `patchERPCUpstream()` — core logic: reads eRPC ConfigMap, adds/removes upstream, restarts eRPC deployment

**Chain ID mapping**: mainnet=1, hoodi=560048, sepolia=11155111

**Write protection**: Local upstreams include `ignoreMethods` for `eth_sendRawTransaction` and `eth_sendTransaction`. A `selectionPolicy` on the mainnet network routes writes exclusively to `obol-rpc-mainnet`.

**Data flow**:
```
obol network sync ethereum/my-node
  → helmfile sync (deploys execution + consensus clients)
  → RegisterERPCUpstream(cfg, "ethereum", "my-node")
    → patches erpc-config ConfigMap: adds local-ethereum-my-node upstream at position 0
    → restarts eRPC deployment
  → reads now route: local node (priority) → obol-rpc-mainnet (fallback)
  → writes route: obol-rpc-mainnet only (local node blocks write methods)
```

## LLM Configuration Architecture

The stack uses a two-tier architecture for LLM routing. A cluster-wide proxy (llmspy) handles actual provider communication, while each application instance (e.g., OpenClaw) sees a simplified single-provider view.

### Tier 1: Global llmspy Gateway (`llm` namespace)

**Purpose**: Shared OpenAI-compatible proxy that routes LLM traffic from all applications to actual providers (Ollama, Anthropic, OpenAI).

**Kubernetes resources** (defined in `internal/embed/infrastructure/base/templates/llm.yaml`):

| Resource | Type | Purpose |
|----------|------|---------|
| `llm` | Namespace | Dedicated namespace for LLM infrastructure |
| `llmspy-config` | ConfigMap | `llms.json` (provider enable/disable) + `providers.json` (provider definitions) |
| `llms-secrets` | Secret | Cloud API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) — empty by default |
| `llmspy` | Deployment | `ghcr.io/obolnetwork/llms:3.0.32-obol.1-rc.1`, port 8000 |
| `llmspy` | Service (ClusterIP) | `llmspy.llm.svc.cluster.local:8000` |
| `ollama` | Service (ExternalName) | Routes to host Ollama via `{{OLLAMA_HOST}}` placeholder |

**Configuration mechanism** (`internal/model/model.go` — `ConfigureLLMSpy()`):
1. Patches `llms-secrets` Secret with the API key
2. Reads `llmspy-config` ConfigMap, sets `providers.<name>.enabled = true` in `llms.json`
3. Restarts `llmspy` Deployment via rollout restart
4. Waits for rollout to complete (60s timeout)

**CLI surface** (`cmd/obol/model.go`):
- `obol model setup --provider=anthropic --api-key=sk-...`
- `obol model status` — show which providers are enabled in llmspy
- Interactive prompt if flags omitted (choice of Anthropic or OpenAI)

**Key design**: Ollama is enabled by default; cloud providers are disabled until configured via `obol model setup`. An init container copies the ConfigMap into a writable emptyDir so llmspy can write runtime state.

### Tier 2: Per-Instance Application Config (per-deployment namespace)

**Purpose**: Each application instance (e.g., OpenClaw) has its own model configuration, rendered by its Helm chart from values files.

**Values file hierarchy** (helmfile merges in order):
1. `values.yaml` — chart defaults (from embedded chart, e.g., `internal/openclaw/chart/values.yaml`)
2. `values-obol.yaml` — Obol Stack overlay (generated by `generateOverlayValues()`)

**How providers become application config** (OpenClaw example, `_helpers.tpl` lines 167-189):
- Iterates provider list from `.Values.models`
- Only emits providers where `enabled == true`
- For each enabled provider: `baseUrl`, `apiKey` (as `${ENV_VAR}` reference), `models` array
- `api` field is only emitted if non-empty (required for llmspy routing)

### The llmspy-Routed Overlay Pattern

When a cloud provider is selected during setup, two things happen simultaneously:

1. **Global tier**: `llm.ConfigureLLMSpy()` patches the cluster-wide llmspy gateway with the API key and enables the provider
2. **Instance tier**: `buildLLMSpyRoutedOverlay()` creates an overlay where a "llmspy" provider points at the llmspy gateway, the cloud model is listed under that provider with a `llmspy/` prefix, and `api` is set to `openai-completions`. The default "ollama" provider is disabled.

**Result**: The application never talks directly to cloud APIs. All traffic is routed through llmspy.

**Data flow**:
```
Application (openclaw.json)
  │ model: "llmspy/claude-sonnet-4-5-20250929"
  │ api: "openai-completions"
  │ baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
  │
  ▼
llmspy (llm namespace, port 8000)
  │ POST /v1/chat/completions
  │ → resolves "claude-sonnet-4-5-20250929" to anthropic provider
  │
  ▼
Anthropic API (or Ollama, OpenAI — depending on provider)
```

**Overlay example** (`values-obol.yaml` for cloud provider path):
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
  ollama:
    enabled: false
  anthropic:
    enabled: false
  openai:
    enabled: false
```

**Note**: The default Ollama path (no cloud provider) still uses the "ollama" provider name pointing at llmspy, since it genuinely routes Ollama model traffic.

### Summary Table

| Aspect | Tier 1 (llmspy) | Tier 2 (Application instance) |
|--------|-----------------|-------------------------------|
| **Scope** | Cluster-wide | Per-deployment |
| **Namespace** | `llm` | `<app>-<id>` (e.g., `openclaw-<id>`) |
| **Config storage** | ConfigMap `llmspy-config` | ConfigMap `<release>-config` |
| **Secrets** | Secret `llms-secrets` | Secret `<release>-secrets` |
| **Configure via** | `obol model setup` | `obol openclaw setup <id>` |
| **Providers** | Real (Ollama, Anthropic, OpenAI) | Cloud: "llmspy" virtual provider; Default: "ollama" pointing at llmspy |
| **API field** | N/A (provider-native) | Must be `openai-completions` for llmspy routing |

### Key Source Files

| File | Role |
|------|------|
| `internal/model/model.go` | `ConfigureLLMSpy()` — patches global Secret + ConfigMap + restart |
| `cmd/obol/model.go` | `obol model setup` CLI command |
| `internal/embed/infrastructure/base/templates/llm.yaml` | llmspy Kubernetes resource definitions |
| `internal/openclaw/openclaw.go` | `Setup()`, `interactiveSetup()`, `generateOverlayValues()`, `buildLLMSpyRoutedOverlay()` |
| `internal/openclaw/import.go` | `DetectExistingConfig()`, `TranslateToOverlayYAML()` |
| `internal/openclaw/chart/values.yaml` | Default per-instance model config |
| `internal/openclaw/chart/templates/_helpers.tpl` | Renders model providers into application JSON config |

## OpenClaw Skills System

### Overview

OpenClaw skills are SKILL.md files (with optional scripts and references) that give the AI agent domain-specific capabilities. The Obol Stack ships default skills embedded in the `obol` binary and supports runtime skill management via the CLI.

### Delivery Mechanism: Host-Path PVC Injection

Skills are delivered by writing directly to the host filesystem at `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/`, which maps to `/data/.openclaw/skills/` inside the OpenClaw container via k3d volume mounts and local-path-provisioner.

**Advantages over ConfigMap approach**: No 1MB size limit, works before pod readiness, survives pod restarts, supports binary files and scripts.

### Default Skills (21 skills)

The stack ships 20 embedded skills organized into categories. All are installed automatically on first deploy.

#### Infrastructure Skills

| Skill | Contents | Purpose |
|-------|----------|---------|
| `ethereum-networks` | `SKILL.md`, `scripts/rpc.sh`, `scripts/rpc.py`, `references/erc20-methods.md`, `references/common-contracts.md` | Read-only Ethereum queries via cast/eRPC — blocks, balances, contract reads, ERC-20 lookups, ENS resolution |
| `ethereum-local-wallet` | `SKILL.md`, `scripts/signer.py`, `references/remote-signer-api.md` | Sign and send Ethereum transactions via the per-agent remote-signer service |
| `obol-stack` | `SKILL.md`, `scripts/kube.py` | Kubernetes cluster diagnostics — pods, logs, events, deployments via ServiceAccount API |
| `distributed-validators` | `SKILL.md`, `references/api-examples.md` | Obol DVT cluster monitoring, operator audit, exit coordination via Obol API |

#### Ethereum Development Skills

| Skill | Contents | Purpose |
|-------|----------|---------|
| `addresses` | `SKILL.md` | Verified contract addresses — DeFi, tokens, bridges, ERC-8004 registries across all major chains |
| `building-blocks` | `SKILL.md` | OpenZeppelin patterns, DEX integration, oracle usage, access control |
| `concepts` | `SKILL.md` | Mental model — state machines, incentive design, gas mechanics, EOAs vs contracts |
| `gas` | `SKILL.md` | Gas optimization patterns, L2 fee structures, estimation |
| `indexing` | `SKILL.md` | The Graph, Dune, event indexing for onchain data |
| `l2s` | `SKILL.md` | L2 comparison — Base, Arbitrum, Optimism, zkSync with gas costs and use cases |
| `orchestration` | `SKILL.md` | End-to-end dApp build (Scaffold-ETH 2) + AI agent commerce cycle |
| `security` | `SKILL.md` | Smart contract vulnerability patterns, reentrancy, flash loans, MEV protection |
| `standards` | `SKILL.md` | ERC-8004, x402, EIP-3009, EIP-7702, ERC-4337 — spec details, integration patterns |
| `ship` | `SKILL.md` | Architecture planning — what goes onchain vs offchain, chain selection, agent patterns |
| `testing` | `SKILL.md` | Foundry testing — unit, fuzz, fork, invariant tests |
| `tools` | `SKILL.md` | Development tooling — Foundry, Hardhat, Scaffold-ETH 2, verification |
| `wallets` | `SKILL.md` | Wallet management — EOAs, Safe multisig, EIP-7702, key safety for AI agents |

#### Frontend & UX Skills

| Skill | Contents | Purpose |
|-------|----------|---------|
| `frontend-playbook` | `SKILL.md` | Frontend deployment — IPFS, Vercel, ENS subdomains |
| `frontend-ux` | `SKILL.md` | Web3 UX patterns — wallet connection, transaction flows, error handling |
| `qa` | `SKILL.md` | Quality assurance — testing strategy, coverage, CI/CD patterns |
| `why` | `SKILL.md` | Why build on Ethereum — the AI agent angle with ERC-8004 and x402 |

### Skill Delivery Flow

```
Onboard / Sync:
  1. stageDefaultSkills(deploymentDir)
     → copies embedded skills from internal/embed/skills/ to deploymentDir/skills/
     → skips if skills/ directory already exists (preserves user customizations)

  2. injectSkillsToVolume(cfg, id, deploymentDir)
     → copies skills/ from deployment dir to host PVC path:
       $DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/
     → this path is volume-mounted into the pod at /data/.openclaw/skills/

  3. doSync() → helmfile sync (creates namespace, chart, pod)
     → OpenClaw file watcher auto-discovers skills on startup
```

### Instance Resolution

All `obol openclaw` subcommands (except `onboard` and `list`) use `ResolveInstance()`:
- **0 instances**: error prompting `obol agent init`
- **1 instance**: auto-selected, no ID required
- **2+ instances**: first CLI arg must match an instance name

### CLI Commands

```bash
obol openclaw skills list                   # list installed skills (auto-resolves instance)
obol openclaw skills add <package>          # add via openclaw CLI in pod
obol openclaw skills remove <name>          # remove via openclaw CLI in pod
obol openclaw skills sync                   # re-inject embedded defaults to volume
obol openclaw skills sync --from <dir>      # push custom skills from local directory
```

### Ethereum Local Wallet (Remote-Signer)

Each OpenClaw instance is provisioned with an Ethereum signing wallet during `obol openclaw onboard`. The wallet is backed by a remote-signer service (Rust-based REST API) deployed in the same namespace.

**Architecture**:
```
Namespace: openclaw-<id>
  OpenClaw Pod ──HTTP:9000──> remote-signer Pod
  (signer.py skill)          /data/keystores/<uuid>.json (V3)
         │
         └── eth_sendRawTransaction ──> eRPC (:4000/rpc)
```

**Key generation**: secp256k1 via `crypto/rand` + `github.com/decred/dcrd/dcrec/secp256k1/v4`, encrypted to Web3 Secret Storage V3 format (scrypt + AES-128-CTR).

**Provisioning flow**:
1. `GenerateWallet()` creates key + V3 keystore + random password
2. Keystore written to host PVC path: `$DATA_DIR/openclaw-<id>/remote-signer-keystores/`
3. Password stored in `values-remote-signer.yaml` for the Helm chart
4. `generateHelmfile()` includes both `obol/openclaw` and `obol/remote-signer` releases
5. After helmfile sync, `applyWalletMetadataConfigMap()` creates a `wallet-metadata` ConfigMap for the frontend

**Remote-signer API** (ClusterIP, port 9000):
- `GET /api/v1/keys` — list signing addresses
- `POST /api/v1/sign/{address}/transaction` — sign EIP-1559 tx
- `POST /api/v1/sign/{address}/message` — sign EIP-191 message
- `POST /api/v1/sign/{address}/typed-data` — sign EIP-712 typed data
- `POST /api/v1/sign/{address}/hash` — sign raw hash

**Key source files**:

| File | Role |
|------|------|
| `internal/openclaw/wallet.go` | Key generation, V3 keystore encryption, provisioning, ConfigMap creation |
| `internal/openclaw/wallet_test.go` | Unit tests for key gen, encrypt/decrypt round-trip, address derivation |
| `internal/embed/skills/ethereum-local-wallet/` | Signing skill (SKILL.md, scripts/signer.py, references/) |

### Key Source Files (Skills)

| File | Role |
|------|------|
| `internal/embed/skills/` | Embedded default SKILL.md files + scripts + references |
| `internal/embed/embed.go` | `CopySkills()`, `GetEmbeddedSkillNames()` |
| `internal/embed/embed_skills_test.go` | Unit tests for skill embedding and copying |
| `internal/openclaw/resolve.go` | `ResolveInstance()`, `ListInstanceIDs()` |
| `internal/openclaw/openclaw.go` | `stageDefaultSkills()`, `injectSkillsToVolume()`, `skillsVolumePath()`, `SkillAdd/Remove/List/Sync()` |
| `internal/openclaw/skills_injection_test.go` | Unit tests for staging and volume injection |
| `cmd/obol/openclaw.go` | CLI wiring for `obol openclaw skills` subcommands |
| `tests/skills_smoke_test.py` | In-pod Python smoke tests for all 3 rich skills |

## Network Install Implementation Details

### Template Field Parser

**Location**: `internal/network/parser.go` - `ParseTemplateFields()`

**Annotations supported**:
- `@enum`: Comma-separated valid values
- `@default`: Default value if flag not provided
- `@description`: Help text for flag

**Parsing logic**:
1. Read embedded `values.yaml.gotmpl`
2. Parse Go template to extract field references (e.g., `{{.Network}}`, `{{.ExecutionClient}}`)
3. Parse annotations from comments above each field
4. Generate `TemplateField` struct with:
   - Name: Template field name (e.g., `Network`, `ExecutionClient`)
   - FlagName: CLI flag name (lowercase, dashed, e.g., `network`, `execution-client`)
   - DefaultValue: From `@default` annotation
   - EnumValues: From `@enum` annotation
   - Description: From `@description` annotation
   - Required: True if no `@default` annotation present

### CLI Flag Generation

**Location**: `cmd/obol/network.go` - `buildNetworkInstallCommands()`

**Process**:
1. For each embedded network:
   - Parse values template to extract template fields
   - Build `cli.Flag` for each template field
   - Add enum validation to flag usage
   - Set Required based on default presence
2. Create network-specific subcommand: `obol network install <network>`
3. Attach flags and validation action
4. Register subcommand dynamically

**Flag naming convention**:
- Template field: `ExecutionClient`
- Flag name: `--execution-client`
- Transformation: Insert hyphens before uppercase letters, lowercase

### Install Implementation

**Location**: `internal/network/network.go` - `Install()`

**Implementation** (two-stage templating):
1. Generate unique deployment ID (petname or user-specified via `--id`)
2. Check if deployment directory exists (fail unless `--force` flag provided)
3. Parse embedded values template to extract template fields
4. Build template data map from CLI flag overrides and defaults (NOT including `id`)
5. Display configuration to user (showing id from directory, overrides, and defaults)
6. Execute Go template on `values.yaml.gotmpl` with template data
7. Validate generated YAML syntax (catch malformed values early)
8. Write rendered `values.yaml` to: `$CONFIG_DIR/networks/<network>/<id>/values.yaml`
9. Copy network files (`helmfile.yaml.gotmpl`, `Chart.yaml`, `templates/`) to deployment directory
10. User runs `obol network sync <network>/<id>` to deploy
11. Sync command extracts `id` from directory path
12. Sync runs: `helmfile sync --state-values-file values.yaml --state-values-set id=<id>`
13. Helmfile reads values.yaml, receives `id` via CLI flag, templates Stage 2 (substitutes `{{.Values.*}}`), and applies to cluster

### Validation and Safety Features

**Deployment Overwrite Protection**:
- Install command checks if deployment directory already exists
- Fails with clear error if directory exists: `deployment already exists: ethereum/my-node`
- User must provide `--force` or `-f` flag to explicitly overwrite
- Shows warning when overwriting: `⚠️  WARNING: Overwriting existing deployment`

**YAML Syntax Validation**:
- After template execution, generated YAML is validated before writing to disk
- Uses `gopkg.in/yaml.v3` to parse and validate syntax
- Catches malformed values early (e.g., unquoted strings with colons)
- Error message shows the problematic content and specific syntax error
- Prevents invalid configuration from being saved or deployed

**Deterministic Field Ordering**:
- Template fields are parsed from `values.yaml.gotmpl` using Go template AST
- Fields are sorted by line number before processing
- Ensures consistent CLI flag ordering in `--help` output
- Predictable behavior across runs and environments

## Service Gateway (x402)

### Overview

The `obol service` subsystem is an OpenAI-compatible HTTP gateway that requires x402 micropayment headers before forwarding requests to a local LLM (Ollama). It is designed for trustless monetisation of inference: callers pay per request on-chain, the gateway verifies settlement with a facilitator, and then proxies the completion.

### Architecture

```
Client                     obol service gateway              Ollama / VM
  │                              │                              │
  ├─ POST /v1/chat/completions ──▶│                              │
  │  (no x402 header)            ├─ 402 Payment Required ───────▶│
  │◀─ 402 ───────────────────────│                              │
  │                              │                              │
  ├─ POST /v1/chat/completions ──▶│                              │
  │  (X-Payment header)          ├─ verify with facilitator      │
  │                              ├─ POST /v1/chat/completions ──▶│
  │◀─ 200 (completion) ──────────│◀─────────────────────────────│
```

### Key Components

| Component | Package | Role |
|-----------|---------|------|
| `Gateway` | `internal/inference/gateway.go` | HTTP server, x402 middleware, Ollama proxy |
| `ContainerManager` | `internal/inference/container.go` | Apple Containerization VM lifecycle (macOS) |
| `Store` | `internal/inference/store.go` | Deployment config persistence (~/.config/obol/inference/) |
| `Deployment` | `internal/inference/types.go` | Config struct (wallet, model, VM settings) |
| `seKey` | `internal/enclave/enclave_darwin.go` | Secure Enclave key (Sign-in with Apple entitlement) |

### Deployment Lifecycle

```
obol service create --wallet <addr> [--name <id>]
    → writes ~/.config/obol/inference/<id>/config.json (no container)

obol service deploy [--name <id>] [--vm] [--vm-image <img>] [--vm-cpus N] [--vm-memory M]
    → loads config, applies flag overrides (wallet validated before write)
    → if --vm: container pull <image>; container run --detach --publish 11434:11434
    → starts gateway on :8080 (or --listen), proxying to upstream Ollama

obol service list / info / delete / pubkey
    → manage saved deployments and SE key

obol service serve (stateless, from flags only)
    → gateway without a saved deployment record
```

### Secure Enclave Integration

The Secure Enclave key (`internal/enclave/`) is used to:
1. **Sign responses** — every completion response carries an SE signature proving it originated from this device
2. **Re-encrypt** (optional) — `enclave_middleware.go` can decrypt the client's request payload with its ephemeral key and re-encrypt the response

**`Key` interface** (`internal/enclave/enclave.go`):
```go
type Key interface {
    Tag() string
    PublicKey() *ecdsa.PublicKey
    Sign(digest []byte) ([]byte, error)
    Decrypt(ciphertext []byte) ([]byte, error)
    Delete() error
}
```

**macOS backend** (`enclave_darwin.go`): Uses `kSecAttrTokenIDSecureEnclave` via CGo/Security.framework. Falls back to ephemeral in-memory key when `errSecMissingEntitlement` (development without provisioning profile).

**Build guards**: `enclave_darwin.go` has `//go:build darwin && cgo`; `enclave_stub.go` has `//go:build !darwin || !cgo` — compiles on all platforms.

### VM Mode (Apple Containerization)

`--vm` flag on `obol service deploy` uses the `apple/container` CLI (v0.9.0+, installed by `obol agent init`) to run Ollama in a Linux VM:

```bash
container pull ollama/ollama:latest   # streams progress
container run --detach --name obol-inference-<id> \
    --publish 11434:11434 \
    ollama/ollama:latest
```

**First cold pull**: multi-GB download (arm64 Linux image); progress is now streamed to stdout.

**Platform guard**: `container.go` uses `runtime.GOOS == "darwin"` check; Linux implementation (`container_linux.go`) is a future stub using podman/containerd.

### CLI Flag Patterns

**Deployment name**: Two supported patterns (positional deprecated for flags after name):
```bash
obol service deploy --name test-vm --wallet 0xABC   # preferred
obol service deploy test-vm --wallet 0xABC           # also works
```

**VM flags**:
```bash
--vm               # enable Apple Containerization VM
--vm-image         # container image (default: ollama/ollama:latest)
--vm-cpus          # CPU count (default: 4)
--vm-memory        # memory MiB (default: 8192)
--vm-host-port     # host port for Ollama (default: 11434)
```

**Env var**: `X402_WALLET` sets the wallet address without a flag.

---



### Environment Variable Handling

**Consistent pattern**:
1. Check specific override: `OBOL_CONFIG_DIR`
2. Check XDG standard: `XDG_CONFIG_HOME`
3. Use default: `~/.config/obol`

**Development mode override**:
```bash
if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
    OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$WORKSPACE_DIR/config}"
else
    OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$XDG_CONFIG_HOME/obol}"
fi
```

### Binary Discovery

**Three-tier lookup**:
1. Global binary (outside OBOL_BIN_DIR)
2. Existing binary in OBOL_BIN_DIR
3. Download/install to OBOL_BIN_DIR

**Version comparison**:
- Uses semantic versioning: `version_ge()` function
- Symlinks to global binary if version sufficient
- Downloads pinned version otherwise

### Kubeconfig Management

**Automatic configuration**:
- All passthrough commands auto-set `KUBECONFIG`
- Path: `$OBOL_CONFIG_DIR/kubeconfig.yaml`
- Exported on cluster creation
- User never needs to manually configure

### Error Handling

**Graceful degradation**:
- Failed dependency installs continue with warnings
- Bootstrap script copy is non-critical
- helm-diff plugin failure doesn't block installation
- PATH configuration falls back to manual instructions

## Development Workflow

### Local Development Cycle

```bash
# One-time setup
OBOL_DEVELOPMENT=true ./obolup.sh

# Make code changes
vim cmd/obol/main.go
vim internal/network/network.go

# Run immediately (no compilation)
obol network list
obol network install ethereum

# All data in .workspace/
ls .workspace/config/networks/
ls .workspace/data/networks/
```

### Adding New Networks

**Steps**:
1. Create `internal/embed/networks/<network-name>/helmfile.yaml.gotmpl`
2. Add value annotations:
   ```yaml
   values:
     # @enum mainnet,testnet
     # @default mainnet
     # @description Network to deploy
     - network: {{.Network}}
   ```
3. Build binary (or use development mode)
4. CLI automatically generates `obol network install <network-name> --network=<value>`

**Annotations to CLI flags**:
- Parser runs at startup
- Flags generated dynamically
- Help text includes enum options and defaults
- Validation enforced automatically

### Testing Networks

```bash
# List available networks
obol network list

# Check generated flags
obol network install ethereum --help

# Install with specific config
obol network install ethereum --network=hoodi --execution-client=geth

# Verify deployment
obol kubectl get namespaces | grep ethereum
obol kubectl get all -n ethereum-<generated-name>

# Check logs
obol kubectl logs -n ethereum-<generated-name> <pod-name>

# Delete deployment
obol network delete ethereum-<generated-name> --force
```

## Important Notes for Development

### Critical Design Constraints

1. **Absolute paths required**: Docker volume mounts need absolute paths (use `filepath.Abs()`)
2. **Template resolution timing**: All k3d config values substituted during `init`, not at `up` time
3. **Unique namespaces**: Each deployment must have unique namespace to prevent resource collisions
4. **Two-stage templating**: Stage 1 (CLI flags) → Stage 2 (Helmfile) separation is critical
5. **Local source of truth**: Configuration saved to disk enables future updates and management

### Common Pitfalls

1. **Relative paths in k3d config**: Will fail with Docker volume mounts
2. **Missing absolute path resolution**: k3d.yaml must have absolute paths before cluster creation
3. **Namespace collisions**: Without unique namespaces, multiple deployments will conflict
4. **Root-owned PVCs**: Kubernetes creates PVCs as root, `-f` flag required to remove them
5. **Special characters in values**: Unquoted YAML special chars (`:`, `[`, `{`) break syntax - caught by validation

### Future Work

**ERPC integration**:
- Extract to separate helmfile
- Auto-discover network endpoints
- Dynamic registration/unregistration
- Provide unified RPC endpoints

**Network management enhancements**:
- `obol network list --installed` (show deployed instances)
- `obol network update <namespace>` (edit and re-sync)
- `obol network logs <namespace>` (convenient log access)
- Better namespace discovery and management

## References

### Key Files

**Bootstrap and installation**:
- `obolup.sh` - Bootstrap installer (1356 lines)
- `cmd/obol/main.go` - CLI entrypoint (379 lines)

**Core systems**:
- `internal/config/config.go` - Configuration management
- `internal/stack/stack.go` - Cluster lifecycle
- `internal/network/network.go` - Network deployment
- `internal/embed/embed.go` - Embedded asset management

**LLM and OpenClaw**:
- `internal/model/model.go` - llmspy gateway configuration (`ConfigureLLMSpy()`)
- `cmd/obol/model.go` - `obol model setup` CLI command
- `internal/embed/infrastructure/base/templates/llm.yaml` - llmspy K8s resources
- `internal/openclaw/openclaw.go` - OpenClaw setup, overlay generation, llmspy routing
- `internal/openclaw/import.go` - Existing config detection and translation
- `internal/openclaw/chart/` - OpenClaw Helm chart (values, templates, helpers)

**Embedded assets**:
- `internal/embed/k3d-config.yaml` - k3d configuration template
- `internal/embed/networks/` - Network definitions
  - `ethereum/helmfile.yaml.gotmpl`
  - `helios/helmfile.yaml.gotmpl`
  - `aztec/helmfile.yaml.gotmpl`
- `internal/embed/defaults/` - Default stack resources
- `internal/embed/infrastructure/` - Infrastructure resources (llmspy, Traefik)
- `internal/embed/skills/` - Default OpenClaw skills (21 skills) embedded in obol binary

**Skills system**:
- `internal/openclaw/resolve.go` - Smart instance resolution (0/1/2+ instances)
- `internal/embed/skills/ethereum-networks/` - Ethereum queries via cast/eRPC (SKILL.md + scripts/ + references/)
- `internal/embed/skills/obol-stack/` - Kubernetes cluster diagnostics (SKILL.md + scripts/kube.py)
- `internal/embed/skills/distributed-validators/` - DVT cluster monitoring via Obol API (SKILL.md + references/)
- `internal/embed/skills/addresses/` - Verified contract addresses across chains
- `internal/embed/skills/*/SKILL.md` - 17 additional domain-specific skills (see Default Skills section above)
- `internal/embed/embed_skills_test.go` - Unit tests for skill embedding
- `internal/openclaw/skills_injection_test.go` - Unit tests for skill staging and injection
- `tests/skills_smoke_test.py` - In-pod Python smoke tests for all rich skills

**Inference gateway and Secure Enclave**:
- `internal/enclave/enclave.go` - `Key` interface definition
- `internal/enclave/enclave_darwin.go` - macOS Secure Enclave backend (CGo/Security.framework)
- `internal/enclave/enclave_stub.go` - Stub for non-darwin/non-cgo builds
- `internal/inference/gateway.go` - x402 HTTP gateway, Ollama proxy
- `internal/inference/container.go` - Apple Containerization VM lifecycle (macOS)
- `internal/inference/store.go` - Deployment config persistence
- `internal/inference/types.go` - `Deployment` struct
- `internal/inference/enclave_middleware.go` - SE sign/decrypt/re-encrypt middleware
- `cmd/obol/service.go` - `obol service` CLI commands
- `internal/inference/sdk/` - Cross-platform Go client SDK

**Testing**:
- `internal/openclaw/integration_test.go` - Full-cluster integration tests (Ollama, Anthropic, OpenAI inference through llmspy)
- `internal/openclaw/overlay_test.go` - Unit tests for overlay generation
- `internal/openclaw/import_test.go` - Unit tests for config import/translation
- `internal/openclaw/resolve_test.go` - Unit tests for instance resolution
- `internal/stack/stack_test.go` - Stack lifecycle tests
- `internal/tunnel/tunnel_test.go` - Tunnel configuration tests
- `internal/dns/resolver_test.go` - DNS resolver tests

**Build and version**:
- `justfile` - Task runner (install, build, up, down commands)
- `VERSION` - Semver version file
- `internal/version/version.go` - Version injection

**CI/CD** (`.github/workflows/`):
- `release.yml` - Multi-platform binary builds on tags, creates GitHub releases
- `docker-publish-openclaw.yml` - OpenClaw Docker image build + Trivy security scan

**Documentation**:
- `README.md` - User-facing documentation
- `plan.md` - Network redesign plan
- `CONTRIBUTING.md` - Contribution guidelines

**Developer Skills**:
- `.agents/skills/obol-stack-dev/` - Dev/test/validate skill for LLM routing through llmspy

### External Dependencies

**Required**:
- Docker 20.10.0+ (daemon must be running)
- Go 1.25+ (for building from source)

**Installed by obolup.sh**:
- kubectl 1.35.0
- helm 3.19.4
- k3d 5.8.3
- helmfile 1.2.3
- k9s 0.50.18
- helm-diff plugin 3.14.1

**Go dependencies** (key packages):
- `github.com/urfave/cli/v3` - CLI framework (v3.6.2+; `cli.Command` replaces `cli.App`, `context.Context` added to Action signatures)
- `github.com/dustinkirkland/golang-petname` - Namespace generation
- Embed uses stdlib `embed` package

## Updating This File

This file should be updated when:
- Major architectural changes occur
- New systems or patterns are introduced
- Implementation details significantly change
- New workflows or development practices are established

Always confirm with the user before making updates to maintain accuracy and relevance.

## Related Codebases (External Resources)

| Resource | Path | Description |
|----------|------|-------------|
| obol-stack-front-end | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` | Next.js web dashboard |
| obol-stack-docs | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-docs` | MkDocs documentation site |
| OpenClaw | `/Users/bussyjd/Development/Obol_Workbench/openclaw` | OpenClaw AI assistant (upstream) |
| llmspy | `/Users/bussyjd/Development/R&D/llmspy` | LLM proxy/router (upstream) |
