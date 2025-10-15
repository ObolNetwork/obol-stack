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
   - Installs the `obol` CLI binary and dependencies (k3d, kubectl, helm,
     helmfile, k9s)
   - Supports two modes:
     - **Production mode**: Uses XDG Base Directory specification
       (`~/.config/obol`, `~/.local/share/obol`)
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
- `OBOL_BIN_DIR` - Binary directory (default: `$OBOL_CONFIG_DIR/bin` or
  `.workspace/bin`)
- `OBOL_STATE_DIR` - Persistent data (default: `~/.local/share/obol` or
  `.workspace/share`)
- `OBOL_DEVELOPMENT=true` - Enables local development mode using `.workspace/`

See: `internal/config/config.go`

### Directory Structure

Production layout:

```
~/.config/obol/
  ├── bin/                  # obol binary and dependencies
  ├── cluster/
  │   ├── k3d/             # k3d configurations
  │   └── kubeconfig/      # Kubernetes configs
  └── helmfile/            # Helmfile configurations
```

Development layout:

```
.workspace/
  ├── bin/                 # Local binaries
  ├── config/              # Local configs
  └── share/               # Local state
```

## Running Locally

### Development Mode

- `OBOL_DEVELOPMENT=TRUE` should always be the case and the local path should
  reference `.workspace/bin`
- Always run `./obolup.sh` to build the project to the `.workspace/bin`
- Do validate build correctness via `go build *` or other tooling

This mode is useful for:

- Testing changes without affecting system directories
- Developing new features
- CI/CD environments

### Production Mode

Standard installation for end users:

```bash
./obolup.sh            # Bootstrap (uses XDG directories)
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
4. All persistent data should respect the configured state directory
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
