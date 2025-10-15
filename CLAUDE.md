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

2. **obol CLI** (cmd/obol/) - Go-based cluster management tool
   - Written in Go
   - Abstracts k3d cluster lifecycle management
   - Manages installation of applications via Helm/Helmfile

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
~/.config/obol/          # Configuration files

~/.local/bin/            # obol binary and dependencies

~/.local/share/obol/     # Persistent data (volumes)

~/.local/state/obol/     # Runtime state (logs)
```

Development layout:

```
.workspace/
  ├── bin/               # Local binaries
  ├── config/            # Local configs
  ├── data/              # Local persistent data
  └── state/             # Local runtime state
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

## Key Design Principles

1. **Local-first**: Designed to run entirely on the user's local machine using
   k3d
2. **Simplified UX**: Abstract Kubernetes complexity behind simple CLI commands
3. **XDG-compliant**: Follows Linux filesystem standards for configuration
4. **Application-centric**: Applications are tailored Helm charts/Helmfiles for
   the environment

## Legacy Structure

The following directories are part of the old architecture and should NOT be the
focus:

- `obolup/` - Old directory (functionality moved to `obolup.sh`)
- `values/` - Old directory structure for manifests

## Important Notes for Development

1. Always check if running in development mode via `OBOL_DEVELOPMENT`
   environment variable
2. The obol CLI is designed for non-developers; advanced users should use
   kubectl/helm directly
3. Applications are environment-specific Helm charts, not generic charts
4. All persistent data should use `DataDir` (XDG_DATA_HOME), logs should use
   `StateDir` (XDG_STATE_HOME)
5. The stack provides a local L1 RPC endpoint at
   `http://rpc.l1.cluster.svc.local/rpc/mainnet`

## Updating This File

This file should be updated when crucial architectural changes or important
information emerges. Always confirm with the user before making updates to
maintain accuracy and relevance.

## References

- Bootstrap script: `obolup.sh`
- CLI entrypoint: `cmd/obol/main.go`
- Config system: `internal/config/config.go`
