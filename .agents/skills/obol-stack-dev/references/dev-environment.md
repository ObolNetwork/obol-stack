# Development Environment Setup

## Prerequisites

- **Docker** installed and running (Docker Desktop on macOS, Docker Engine on Linux)
- **Go 1.21+** for building from source
- API keys in `.env` file (gitignored) for cloud provider tests

## Bootstrap

```bash
# Development mode — uses .workspace/ instead of XDG dirs
OBOL_DEVELOPMENT=true ./obolup.sh
```

This creates:
```
.workspace/
├── bin/          # obol binary + dependencies (kubectl, helm, k3d, helmfile, k9s)
├── config/       # Cluster config, kubeconfig, deployment configs
│   ├── k3d.yaml
│   ├── kubeconfig.yaml
│   ├── .cluster-id
│   └── applications/openclaw/<id>/  # Per-instance configs
└── data/         # Persistent volumes (blockchain data)
```

## Building

```bash
# Build the obol binary
go build -o .workspace/bin/obol ./cmd/obol

# Verify
.workspace/bin/obol version
```

**Important**: Always rebuild after code changes. The `.workspace/bin/obol` is a compiled binary, not a `go run` wrapper.

## Environment Variables

When running tests or the binary outside the normal `obol` CLI flow, set these explicitly:

```bash
export OBOL_CONFIG_DIR=/path/to/obol-stack/.workspace/config
export OBOL_BIN_DIR=/path/to/obol-stack/.workspace/bin
export OBOL_DATA_DIR=/path/to/obol-stack/.workspace/data
```

**Why**: The `config.Load()` function reads these. Without them, dev mode computes paths relative to `os.Getwd()`, which during `go test` is the test package directory (e.g., `internal/openclaw/`), not the project root.

## .env File

Cloud provider integration tests read API keys from environment variables. Create a `.env` file at the project root (gitignored):

```bash
ANTHROPIC_API_KEY=sk-ant-api03-...
OPENAI_API_KEY=sk-proj-...
```

Load before running tests:
```bash
export $(grep -v '^#' .env | xargs)
```

## Starting the Cluster

```bash
obol stack init    # Generate k3d config + cluster ID
obol stack up      # Create k3d cluster, deploy defaults

# Verify
obol kubectl cluster-info
obol kubectl get namespaces
```

The cluster runs:
- k3d (Kubernetes in Docker) with 1 server + 3 agent nodes
- Traefik (ingress controller)
- llmspy (LLM gateway in `llm` namespace)
- ERPC (RPC load balancer)
- Obol Frontend (web dashboard)

## Config System

```go
// internal/config/config.go
type Config struct {
    ConfigDir string  // ~/.config/obol or .workspace/config
    DataDir   string  // ~/.local/share/obol or .workspace/data
    BinDir    string  // ~/.local/bin or .workspace/bin
}
```

Precedence:
1. `OBOL_CONFIG_DIR` (explicit override)
2. `XDG_CONFIG_HOME/obol` (XDG standard)
3. `~/.config/obol` (default)

`config.Load()` returns the resolved Config struct.

## Full Test Run Command

```bash
# Unit tests (no cluster needed)
go test ./...

# Integration tests (requires running cluster + Ollama + API keys)
export $(grep -v '^#' .env | xargs)
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go test -tags integration -v -timeout 15m ./internal/openclaw/
```
