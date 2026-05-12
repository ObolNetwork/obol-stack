# Development Environment Setup

## Prerequisites

- **Docker** installed and running (Docker Desktop on macOS, Docker Engine on Linux)
- **Go 1.21+** for building from source
- API keys in `.env` file (gitignored) for cloud provider tests

## Bootstrap

```bash
# Development mode -- uses .workspace/ instead of XDG dirs
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

`obolup.sh` with `OBOL_DEVELOPMENT=true` installs a `go run -a` *wrapper* at `.workspace/bin/obol` for rapid iteration. That wrapper recompiles on every invocation, and any flow step that backgrounds a port-forward and polls within ~5–8 seconds (e.g. `flow-06` step 15) will false-FAIL because the port-forward isn't listening yet. Replace the wrapper with a real binary before running flows:

```bash
mv .workspace/bin/obol .workspace/bin/obol.wrapper
go build -o .workspace/bin/obol ./cmd/obol
```

## Foundry

`obolup.sh` does not manage Foundry. Install separately and **use nightly, not stable** — stable lags far enough behind that Base Sepolia archive-lookup support drifts, and `flow-08` / `flow-11` payment verification then dies with `state at block #N is pruned` from the facilitator. Install:

```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup --install nightly
```

`flows/lib.sh` sets `FOUNDRY_DISABLE_NIGHTLY_WARNING=1` so nightly's per-invocation stderr warning doesn't bleed into `cast` output and break pattern-matching assertions. Keep that export.

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

### Forcing image rebuilds

`obol stack up` (with `OBOL_DEVELOPMENT=true`) reuses any locally-tagged
`ghcr.io/obolnetwork/<name>:latest` image to keep warm runs fast. Use
`OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES` to override that for specific images:

```bash
# Rebuild everything
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=true obol stack up

# Rebuild only the image you changed — avoids the full 10-minute rebuild
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=x402-verifier obol stack up
OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller,x402-buyer obol stack up
```

| Value | Effect |
|-------|--------|
| unset / `false` / `0` | Reuse all cached images (default) |
| `true` / `all` | Rebuild every local image |
| `img1,img2` | Rebuild only the named images |

The short name is the image base without registry prefix or tag
(`x402-verifier` from `ghcr.io/obolnetwork/x402-verifier:latest`).

Available image names: `x402-verifier`, `serviceoffer-controller`,
`x402-buyer`, `demo-server`, `obol-stack-public-storefront`
(`public-storefront` alias accepted).

When nothing was rebuilt the "Local dev images ready" summary line prints
the selective-rebuild hint so you know the option is available.

The cluster runs:
- k3d (Kubernetes in Docker) with 1 server + 3 agent nodes
- Traefik (ingress controller)
- LiteLLM (LLM gateway in `llm` namespace, port 4000)
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
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go test -tags integration -v -timeout 15m ./internal/openclaw/
```
