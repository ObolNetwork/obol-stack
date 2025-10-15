# Obol Stack - Context for Claude Code

## Project Overview

The Obol Stack is a local Kubernetes-based framework for running decentralized
Ethereum applications (dApps). It provides a simplified CLI experience for
managing a k3d cluster and installing/managing Helm-packaged applications.

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
       (`~/.config/obol`, `~/.local/share/obol`, `~/.local/state/obol`, `~/.local/bin`)
     - **Development mode**: Uses local `.workspace/` directory (set via
       `OBOL_DEVELOPMENT=true`)
   - Supported platforms: Linux, Darwin
   - Supported architectures: amd64, arm64

2. **obol CLI** (cmd/obol/) - Go-based cluster management tool
   - Built with urfave/cli/v2 framework
   - Commands grouped: cluster lifecycle, passthrough k8s tools, utilities
   - Cluster lifecycle: `init`, `up`, `down`, `purge`
   - Passthrough tools: `kubectl`, `helm`, `helmfile`, `k9s` (auto-set
     KUBECONFIG)
   - Cluster package: `internal/cluster/cluster.go` handles k3d operations
   - Embedded config: k3d config template in `internal/embed/k3d-config.yaml`

### Configuration System

The application follows XDG Base Directory specification with override
capability:

- `OBOL_CONFIG_DIR` - Configuration files (default: `~/.config/obol` or
  `.workspace/config`)
- `OBOL_BIN_DIR` - Binary directory (default: `$XDG_BIN_HOME` → `~/.local/bin` or
  `.workspace/bin`)
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
  ├── cluster/           # Cluster-specific config
  │   ├── k3d.yaml       # Generated k3d config with unique cluster ID
  │   ├── .cluster-id    # Petname-generated cluster identifier
  │   └── kubeconfig.yaml # Exported cluster kubeconfig
  └── [other config files]

~/.local/bin/            # obol binary and dependencies

~/.local/share/obol/     # Persistent data (k3d volume mount: /data in nodes)

~/.local/state/obol/     # Runtime state (logs)
```

Development layout:

```
.workspace/
  ├── bin/               # Local binaries
  ├── config/
  │   └── cluster/       # Cluster-specific config
  │       ├── k3d.yaml       # Generated k3d config with unique cluster ID
  │       ├── .cluster-id    # Petname-generated cluster identifier
  │       └── kubeconfig.yaml # Exported cluster kubeconfig
  ├── data/              # Local persistent data (k3d volume mount: /data in nodes)
  └── state/             # Local runtime state (logs)
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
obol cluster init
obol cluster up
```

## Cluster Architecture

### k3d Configuration

- **Topology**: 1 server + 3 agent nodes (fault tolerance and pod distribution)
- **Image**: rancher/k3s:v1.31.4-k3s1
- **Unique naming**: Each cluster gets petname-generated ID (e.g.,
  `obol-stack-adorable-hippo`)
- **Volume mounts**: `$OBOL_DATA_DIR:/data` mounted on all nodes
- **Ports**: 8080:80, 8443:443 via load balancer
- **Feature gates**: KubeletInUserNamespace=true (fixes /dev/kmsg permission
  issues)
- **Ulimits**: nofile 26677 (prevents "too many open files")

### Cluster Lifecycle

1. **Init**: Generates k3d.yaml with unique cluster ID using petname library
2. **Up**: Creates k3d cluster, exports kubeconfig to `cluster/kubeconfig.yaml`
3. **Down**: Deletes k3d cluster (preserves config)
4. **Purge**: Removes cluster and all config files

See: `internal/cluster/cluster.go`, `internal/embed/k3d-config.yaml`

## Key Design Principles

1. **Local-first**: Runs entirely on local machine using k3d
2. **Simplified UX**: Abstracts Kubernetes complexity behind simple CLI commands
3. **XDG-compliant**: Follows Linux filesystem standards for configuration
4. **Unique clusters**: Petname-generated IDs prevent naming conflicts
5. **Passthrough pattern**: Wraps k8s tools with auto-configured KUBECONFIG

## Legacy Structure

The following directories are part of the old architecture and should NOT be the
focus:

- `obolup/` - Old directory (functionality moved to `obolup.sh`)
- `values/` - Old directory structure for manifests

## Important Notes for Development

1. Check `OBOL_DEVELOPMENT` environment variable for dev mode detection
2. Cluster ID stored in `.cluster-id` file, used for unique k3d cluster names
3. Kubeconfig path: `$OBOL_CONFIG_DIR/cluster/kubeconfig/kubeconfig.yaml`
   (legacy) or `$OBOL_CONFIG_DIR/cluster/kubeconfig.yaml` (current)
4. k3d config uses `{{CLUSTER_ID}}` placeholder, replaced during `cluster init`
5. Data directory must be absolute path for k3d volume mounts
6. Passthrough commands check kubeconfig exists before delegating to binaries
7. Version info injected at build time via ldflags (VERSION file + git metadata)

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
- Cluster management: `internal/cluster/cluster.go`
- Embedded assets: `internal/embed/embed.go`, `internal/embed/k3d-config.yaml`
- Build tasks: `justfile`
- Version tracking: `VERSION`, `internal/version/version.go`
- Example manifests: `examples/simple-persistence-test.yaml`
