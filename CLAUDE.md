# Obol Stack - Context for Claude Code

## Project Overview

The Obol Stack is a local Kubernetes-based framework for running decentralized
Ethereum applications (dApps). It provides a simplified CLI experience for
managing a k3d cluster with embedded default applications and a planned
installable application system.

## Architecture

### Core Components

1. **obolup.sh** - Bootstrap installer script
   - Validates prerequisites (Docker daemon)
   - Creates XDG-compliant directory structure
   - Installs the `obol` CLI binary and dependencies with pinned versions:
     - kubectl, helm, k3d, helmfile, k9s (binaries)
     - helm-diff plugin (pinned for helm compatibility)
   - Version constants defined at script top - update to upgrade all
     installations
   - Supports two modes:
     - **Production mode**: Uses XDG Base Directory specification
       (`~/.config/obol`, `~/.local/share/obol`, `~/.local/state/obol`,
       `~/.local/bin`)
     - **Development mode**: Uses local `.workspace/` directory (set via
       `OBOL_DEVELOPMENT=true`)
   - Supported platforms: Linux, Darwin
   - Supported architectures: amd64, arm64

2. **obol CLI** (cmd/obol/) - Go-based stack management tool
   - Built with urfave/cli/v2 framework
   - Commands grouped: stack lifecycle, passthrough k8s tools, utilities
   - Stack lifecycle: `init`, `up`, `down`, `purge`
   - Passthrough tools: `kubectl`, `helm`, `helmfile`, `k9s` (auto-set
     KUBECONFIG)
   - Stack package: `internal/stack/stack.go` handles k3d operations
   - Embedded assets: k3d config template and applications in `internal/embed/`

3. **Logging & Execution Framework** (internal/logging/, internal/executor/)
   - **Console output**: Structured logging with colored symbols and clean
     formatting
     - `[→]` Info (blue), `[✓]` Success (green), `[!]` Warn (yellow), `[✗]`
       Error (red)
     - `[⚙]` Subprocess execution (magenta) with indented output
   - **File logging**: Date-based JSON logs at
     `$OBOL_STATE_DIR/{cluster-id}/{date}.log`
     - One log file per day, all sessions append to same file
     - Full structured logs with source info, cluster ID tagging
   - **Executor**: Wraps subprocess calls (k3d, kubectl, etc.) with automatic
     logging
     - Captures and logs all subprocess output via slog
     - Displays subprocess commands and output with visual hierarchy
   - See: `internal/logging/handler.go`, `internal/executor/executor.go`

4. **Embedded Applications System** (internal/embed/)
   - **Dual embed pattern**:
     - `K3dConfig` string: k3d configuration template with `{{PLACEHOLDERS}}`
     - `applicationsFS embed.FS`: Entire applications directory tree
   - **CopyDefaultApplications()**: Extracts only default apps during cluster init
   - **GetApplicationsFS()**: Exposes embed.FS for app management operations
   - **Default applications**: Auto-deployed monitoring stack (Prometheus/Grafana)
   - **Installable applications**: Copied to config on-demand via `obol app install`
   - Default apps mounted to k3s manifest directory for automatic deployment
   - See: `internal/embed/embed.go`, `internal/embed/applications/`

3. **Logging & Execution Framework** (internal/logging/, internal/executor/)
   - **Console output**: Structured logging with colored symbols and clean
     formatting
     - `[→]` Info (blue), `[✓]` Success (green), `[!]` Warn (yellow), `[✗]`
       Error (red)
     - `[⚙]` Subprocess execution (magenta) with indented output
   - **File logging**: Date-based JSON logs at
     `$OBOL_STATE_DIR/{cluster-id}/{date}.log`
     - One log file per day, all sessions append to same file
     - Full structured logs with source info, cluster ID tagging
   - **Executor**: Wraps subprocess calls (k3d, kubectl, etc.) with automatic
     logging
     - Captures and logs all subprocess output via slog
     - Displays subprocess commands and output with visual hierarchy
   - See: `internal/logging/handler.go`, `internal/executor/executor.go`

4. **Embedded Applications System** (internal/embed/)
   - **Dual embed pattern**:
     - `K3dConfig` string: k3d configuration template with `{{PLACEHOLDERS}}`
     - `applicationsFS embed.FS`: Entire applications directory tree
   - **CopyApplications()**: Recursively extracts embedded apps to disk
   - **Default applications**: Auto-deployed monitoring stack
     (Prometheus/Grafana)
   - Applications mounted to k3s manifest directory for automatic application
   - See: `internal/embed/embed.go`, `internal/embed/applications/`

### Configuration System

The application follows XDG Base Directory specification with override
capability:

- `OBOL_CONFIG_DIR` - Configuration files (default: `~/.config/obol` or
  `.workspace/config`)
- `OBOL_BIN_DIR` - Binary directory (default: `$XDG_BIN_HOME` → `~/.local/bin`
  or `.workspace/bin`)
- `XDG_BIN_HOME` - XDG standard for user binaries (default: `~/.local/bin`)
- `OBOL_DATA_DIR` - Persistent volumes and data (default: `~/.local/share/obol`
  or `.workspace/data`)
- `OBOL_STATE_DIR` - Logs and runtime state (default: `~/.local/state/obol` or
  `.workspace/state`)
- `OBOL_DEVELOPMENT=true` - Enables local development mode using `.workspace/`

See: `internal/config/config.go`

### Directory Structure

Production layout:

```
~/.config/obol/
  ├── k3d.yaml                       # Generated k3d config (absolute paths substituted)
  ├── .cluster-id                    # Petname-generated cluster identifier
  ├── kubeconfig.yaml                # Exported cluster kubeconfig
  └── applications/                  # Application configurations
      ├── default/                   # Auto-deployed (copied during init)
      │   └── monitoring/            # Prometheus/Grafana stack
      │       ├── monitoring-stack.yaml
      │       ├── monitoring-ingress.yaml
      │       └── README.md
      └── ethereum/                  # Installable (copied via obol app install)
          ├── helmfile.yaml
          ├── values.yaml
          └── README.md

~/.local/bin/                        # obol binary and dependencies

~/.local/share/obol/                 # Persistent data (k3d volume mount: /data in nodes)

~/.local/state/obol/                 # Runtime state
  └── {stack-id}/
      └── 2025-10-15.log             # Date-based log files (JSON)
```

Development layout:

```
.workspace/
  ├── bin/                           # Local binaries
  ├── config/
  │   ├── k3d.yaml                   # Generated k3d config (absolute paths substituted)
  │   ├── .stack-id                  # Petname-generated stack identifier
  │   ├── kubeconfig.yaml            # Exported stack kubeconfig
  │   └── applications/              # Copied from embedded FS
  │       └── default/               # Auto-deployed applications
  │           └── monitoring/        # Prometheus/Grafana stack
  ├── data/                          # Local persistent data (k3d volume mount: /data)
  └── state/                         # Local runtime state
      └── {stack-id}/
          └── 2025-10-15.log         # Date-based log files (JSON)
```

## Running Locally

### Development Mode

When `OBOL_DEVELOPMENT=true`, `./obolup.sh` installs a wrapper script at
`.workspace/bin/obol` that uses `go run ./cmd/obol`. No compilation needed -
code changes are immediately reflected.

**Development cycle:**

```bash
# One-time setup (or when switching to dev mode)
./obolup.sh

# Make code changes, then run directly
obol <command>  # Uses go run under the hood
```

### Production Mode

Standard installation for end users:

```bash
# Install from latest release or build from source
OBOL_RELEASE=latest ./obolup.sh

# Or install specific version
OBOL_RELEASE=v0.1.0 ./obolup.sh

# Run commands
obol stack init
obol stack up
```

## Stack Architecture

### k3d Configuration

- **Topology**: 1 server + 3 agent nodes (fault tolerance and pod distribution)
- **Image**: rancher/k3s:v1.31.4-k3s1
- **Unique naming**: Each stack gets petname-generated ID (e.g.,
  `obol-stack-adorable-hippo`)
- **Container labels**: `obol.cluster-id={{CLUSTER_ID}}` label applied to all
  containers
- **Volume mounts**:
  - Data: `{{DATA_DIR}}:/data` (persistent storage, all nodes)
  - Applications:
    `{{CONFIG_DIR}}/applications/default:/var/lib/rancher/k3s/server/manifests/default`
    (server only)
- **Ports**: 8080:80, 8443:443 via load balancer
- **Feature gates**: KubeletInUserNamespace=true (fixes /dev/kmsg permission
  issues)
- **Ulimits**: nofile 26677 (prevents "too many open files")
- **Template placeholders**: `{{CLUSTER_ID}}`, `{{DATA_DIR}}`, `{{CONFIG_DIR}}`
  replaced during init with absolute paths

### Stack Lifecycle

1. **Init**:
   - Generates unique stack ID (petname)
   - Gets absolute paths for data and config directories
   - Replaces all template placeholders in k3d config
   - Writes resolved k3d.yaml with absolute paths
   - Copies embedded applications to `applications/` directory
   - Stores stack ID in `.stack-id` file

2. **Up**:
   - Creates k3d cluster using pre-resolved config
   - k3d mounts applications directory to k3s manifests path
   - k3s auto-applies all manifests (monitoring stack deployed)
   - Exports kubeconfig

3. **Down**:
   - Deletes k3d cluster
   - Preserves config directory and logs

4. **Purge**:
   - Stops cluster (via Down)
   - Removes entire config directory (k3d.yaml, kubeconfig, .stack-id,
     applications/)
   - Removes data directory (persistent volumes)
   - Preserves state directory (logs remain for debugging)

All lifecycle commands use structured logging with stack ID context.

See: `internal/stack/stack.go`, `internal/embed/k3d-config.yaml`

## Embedded Applications

### Default Applications (`internal/embed/applications/default/`)

**Auto-deployed with every cluster:**

- **Monitoring stack** (Prometheus + Grafana via kube-prometheus-stack)
  - Grafana: `http://grafana.localhost:8080` (anonymous admin access)
  - Prometheus: `http://prometheus.localhost:8080`
  - Pre-configured Kubernetes dashboards
  - Persistent storage: Prometheus (10Gi), Grafana (5Gi)
  - Resource limits configured for local development

**Deployment mechanism:**

- Applications embedded in binary as `embed.FS`
- Extracted to disk during `obol stack init`
- Mounted into k3s at `/var/lib/rancher/k3s/server/manifests/default/`
- k3s automatically applies manifests on startup
- Uses k3s HelmChart CRD for Helm chart deployment

**Structure:**

```
internal/embed/applications/
├── README.md                    # Architecture and future plans
└── default/
    └── monitoring/
        ├── monitoring-stack.yaml      # HelmChart CRD (kube-prometheus-stack)
        └── monitoring-ingress.yaml    # Traefik IngressRoutes
```

### Future: Installable Applications (Planned)

**Not yet implemented:**

- `obol app install <app-name>` - Download application from registry
- `obol app apply <app-name>` - Deploy using Helmfile + applyset tracking
- Applyset pattern for atomic updates and automatic pruning
- Example applications: ethereum (client stacks), charon (DVT)

See: `internal/embed/applications/README.md` for detailed architecture

=======

### Cluster Lifecycle

1. **Init**:
   - Generates unique cluster ID (petname)
   - Gets absolute paths for data and config directories
   - Replaces all template placeholders in k3d config
   - Writes resolved k3d.yaml with absolute paths
   - Copies only default applications to `applications/default/` directory
   - Stores cluster ID in `.cluster-id` file

2. **Up**:
   - Creates k3d cluster using pre-resolved config
   - k3d mounts applications directory to k3s manifests path
   - k3s auto-applies all manifests (monitoring stack deployed)
   - Exports kubeconfig

3. **Down**:
   - Deletes k3d cluster
   - Preserves config directory and logs

4. **Purge**:
   - Stops cluster (via Down)
   - Removes entire config directory (k3d.yaml, kubeconfig, .cluster-id,
     applications/)
   - Removes data directory (persistent volumes)
   - Preserves state directory (logs remain for debugging)

=======
All lifecycle commands use structured logging with cluster ID context.

=======
See: `internal/cluster/cluster.go`, `internal/embed/k3d-config.yaml`

## Embedded Applications

### Default Applications (`internal/embed/applications/default/`)

**Auto-deployed with every cluster:**

- **Monitoring stack** (Prometheus + Grafana via kube-prometheus-stack)
  - Grafana: `http://grafana.localhost:8080` (anonymous admin access)
  - Prometheus: `http://prometheus.localhost:8080`
  - Pre-configured Kubernetes dashboards
  - Persistent storage: Prometheus (10Gi), Grafana (5Gi)
  - Resource limits configured for local development
  - **Generic discovery architecture**:
    - ServiceMonitors: Cross-namespace discovery via `release: monitoring` label
    - Dashboards: Grafana sidecar discovers ConfigMaps with `grafana_dashboard: "1"` label
    - Applications self-register, monitoring stack remains application-agnostic

**Deployment mechanism:**

- Default applications embedded in binary as `embed.FS`
- Extracted to disk during `obol cluster init`
- Mounted into k3s at `/var/lib/rancher/k3s/server/manifests/default/`
- k3s automatically applies manifests on startup
- Uses k3s HelmChart CRD for Helm chart deployment

**Structure:**

```
internal/embed/applications/
├── README.md                    # Architecture and future plans
├── default/
│   └── monitoring/
│       ├── monitoring-stack.yaml      # HelmChart CRD (kube-prometheus-stack)
│       ├── monitoring-ingress.yaml    # Traefik IngressRoutes
│       └── README.md                  # Generic discovery architecture
└── ethereum/                          # Installable application
    ├── helmfile.yaml                  # Helm chart deployment config
    ├── values.yaml                    # Production-ready defaults
    └── README.md                      # Configuration guide
```

### Installable Applications

**Application lifecycle commands:**

```bash
obol app list                    # List available apps from embedded FS
obol app install <app-name>      # Copy app to config directory
obol app edit <app-name>         # Open values.yaml in $EDITOR
obol app sync <app-name>         # Deploy/update app using applyset
obol app delete <app-name>       # Remove from cluster and config
```

<<<<<<< HEAD
See: `internal/embed/applications/README.md` for detailed architecture
>>>>>>> d06eb3f (updated CLAUDE.md with context)
=======
**Deployment pattern (helmfile template + kubectl applyset):**

1. Render manifests: `helmfile template --output-dir /tmp`
2. Create namespace: `kubectl create namespace <app-name>`
3. Apply with tracking: `kubectl apply --prune --applyset <app-name> -n <app-name>`
4. Automatic pruning of resources removed from manifests (enables clean client switching)

**Available applications:**

- **ethereum** - Full Ethereum node with configurable execution/consensus clients
  - Networks: mainnet, holesky, sepolia
  - Execution clients: Besu, Erigon, EthereumJS, Geth, Nethermind, Reth (default)
  - Consensus clients: Grandine, Lighthouse (default), Lodestar, Nimbus, Prysm, Teku
  - Checkpoint sync enabled (fast initial sync)
  - Full Prometheus/Grafana integration via discovery labels
  - Storage: 1TB execution, 200GB consensus (configurable per network)

**Application integration pattern:**

All installable applications follow standard structure:
- `helmfile.yaml` - Defines Helm chart deployment
- `values.yaml` - Application configuration (user-editable)
- `README.md` - Configuration guide and documentation
- Optional: Dashboard ConfigMaps with `grafana_dashboard: "1"` label
- Optional: ServiceMonitors with `release: monitoring` label

See: `internal/embed/applications/README.md`, `internal/app/app.go`
>>>>>>> 150adca (updated CLAUDE.md with context)

## Key Design Principles

1. **Local-first**: Runs entirely on local machine using k3d
2. **Simplified UX**: Abstracts Kubernetes complexity behind simple CLI commands
3. **XDG-compliant**: Follows Linux filesystem standards for configuration
4. **Unique stacks**: Petname-generated IDs prevent naming conflicts
5. **Passthrough pattern**: Wraps k8s tools with auto-configured KUBECONFIG
6. **Embedded defaults**: Core services bundled in binary, auto-deployed
7. **Template resolution**: Config values substituted at init time, not runtime

## Important Notes for Development

1. **Embed pattern**: Use string embed for templates (`K3dConfig`), FS embed for
   directories (`applicationsFS`)
2. **Template placeholders**: `{{STACK_ID}}`, `{{DATA_DIR}}`, `{{CONFIG_DIR}}`
   replaced during init
3. **Absolute paths required**: Docker volume mounts need absolute paths (use
   `filepath.Abs()`)
4. **Config resolution timing**: All template values substituted during `init`,
   not at `up` time
5. **k3s auto-apply**: Manifests in `/var/lib/rancher/k3s/server/manifests/`
   automatically applied
6. **Stack ID context**: All logging requires stack ID via
   `logging.NewSlogLogger()`
7. **Subprocess execution**: Use `executor.Executor` for consistent logging and
   output capture
8. **Log persistence**: Date-based JSON logs at
   `$OBOL_STATE_DIR/{stack-id}/{date}.log`
9. **Purge behavior**: Removes config and data directories, preserves state
   (logs)
10. **Development mode**: Set `OBOL_DEVELOPMENT=true` for local `.workspace/`
    usage
<<<<<<< HEAD
11. **Logger wrapper**: Use `logging.Logger` type with embedded `*slog.Logger`
    to get `Success()` method for success-level logging with green check symbol
12. **Error logging**: All errors propagated through CLI commands should log via
    `l.Error()` before returning
=======
11. **Application separation**: Default apps auto-deployed during init,
    installable apps require explicit `obol app install`
12. **Applyset pattern**: Use `kubectl apply --prune --applyset` with
    `KUBECTL_APPLYSET=true` for atomic updates and automatic pruning
13. **Monitoring integration**: Applications self-register via labels:
    - ServiceMonitors: `release: monitoring`
    - Grafana dashboards: `grafana_dashboard: "1"`
14. **Application structure**: All installable apps need `helmfile.yaml`,
    `values.yaml`, and `README.md`
>>>>>>> 150adca (updated CLAUDE.md with context)

### Build System

- **justfile**: Task runner with `install`, `build`, `up`, `down` commands
- **VERSION**: Semver file (0.0.0) used by `just build` for ldflags
- **Go build**: Injects version, commit, build time, dirty flag into binary

## Updating This File

This file should be updated when crucial architectural changes or important
information emerges. Always confirm with the user before making updates to
maintain accuracy and relevance.

## References

- Bootstrap script: `obolup.sh`
- CLI entrypoint: `cmd/obol/main.go`
- Config system: `internal/config/config.go`
<<<<<<< HEAD
- Stack management: `internal/stack/stack.go`
=======
- Cluster management: `internal/cluster/cluster.go`
- Application management: `internal/app/app.go`
>>>>>>> 150adca (updated CLAUDE.md with context)
- Logging framework: `internal/logging/handler.go`
- Subprocess execution: `internal/executor/executor.go`
- Embedded assets: `internal/embed/embed.go`
- k3d configuration template: `internal/embed/k3d-config.yaml`
- Default applications: `internal/embed/applications/default/`
- Installable applications: `internal/embed/applications/ethereum/`
- Application architecture: `internal/embed/applications/README.md`
- Monitoring architecture: `internal/embed/applications/default/monitoring/README.md`
- Ethereum application: `internal/embed/applications/ethereum/README.md`
- Build tasks: `justfile`
- Version tracking: `VERSION`, `internal/version/version.go`
- Example manifests: `examples/simple-persistence-test.yaml`
