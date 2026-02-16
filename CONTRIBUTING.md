# Contributing to the Obol Stack

## What This Repo Is

Obol Stack is a Go CLI (`obol`) and bootstrap installer (`obolup.sh`) that manages a local Kubernetes cluster (k3d) for running blockchain networks. It is **not** a Helm chart repository — charts live in [ObolNetwork/helm-charts](https://github.com/ObolNetwork/helm-charts).

## Prerequisites

- Go 1.25+
- Docker (running)

That's it. The bootstrap installer handles everything else (kubectl, helm, k3d, helmfile, k9s).

## Development Setup

```bash
# Development mode — uses .workspace/, no compilation needed
OBOL_DEVELOPMENT=true ./obolup.sh

# Code changes take effect immediately via `go run`
obol network list
```

Or build a binary directly:

```bash
just build
```

## Project Layout

```
cmd/obol/          CLI entrypoint (urfave/cli/v2)
internal/
  config/          XDG-compliant configuration
  stack/           Cluster lifecycle (init, up, down, purge)
  network/         Network deployment (install, sync, delete)
  openclaw/        OpenClaw AI assistant integration
  llm/             LLM provider management (llmspy gateway)
  embed/           Embedded assets (k3d config, network definitions, infrastructure)
  version/         Build version injection
obolup.sh          Bootstrap installer
```

## Adding a New Network

1. Create `internal/embed/networks/<name>/values.yaml.gotmpl` with annotated fields
2. Create `internal/embed/networks/<name>/helmfile.yaml` with deployment logic
3. The CLI auto-generates flags from the template annotations — no CLI code changes needed

## Running Tests

```bash
go test ./...
```

## Pull Requests

1. Create a branch
2. Make changes, ensure `go test ./...` passes
3. Submit a PR against `main`
