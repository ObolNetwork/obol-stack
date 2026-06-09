# Dev Environment & CLI

## Operating Mode

Use the product surface first: `obol ...` commands for lifecycle and mutations,
`obol kubectl ...` for Kubernetes evidence, and `curl` only for endpoint probes.
Do not write custom `.sh` helpers for stack checks. Existing `flows/*.sh` are
release-gate artifacts; reach for them only when the user asks for full
release-smoke or a named flow regression.

## Bootstrap

Dev mode uses `.workspace/` instead of XDG dirs. Without `OBOL_DEVELOPMENT=true`, `obolup.sh` downloads the released binary and your branch changes never run.

```bash
OBOL_DEVELOPMENT=true ./obolup.sh
```

Layout:

```
.workspace/
├── bin/      # obol + kubectl, helm, k3d, helmfile, k9s
├── config/   # k3d.yaml, kubeconfig.yaml, .cluster-id, applications/
└── data/     # PVC content (root-owned for some namespaces)
```

## Build

```bash
go build -o .workspace/bin/obol ./cmd/obol
.workspace/bin/obol version
```

**Always replace the wrapper before long QA.** `obolup.sh` with `OBOL_DEVELOPMENT=true` installs a `go run -a` wrapper at `.workspace/bin/obol`. It recompiles on every invocation and can make repeated CLI calls or port-forwards look flaky.

```bash
mv .workspace/bin/obol .workspace/bin/obol.wrapper
go build -o .workspace/bin/obol ./cmd/obol
```

## Foundry

`obolup.sh` does not manage Foundry. Install **nightly**, not stable. Stable lags far enough that Base Sepolia archive-lookup support drifts and `flow-08`/`flow-11` payment verification dies with `state at block #N is pruned` from the facilitator.

```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup --branch nightly
```

## Env Resolution (`internal/config/config.go`)

1. `OBOL_CONFIG_DIR` (explicit)
2. `XDG_CONFIG_HOME/obol`
3. `~/.config/obol`

`OBOL_DEVELOPMENT=true` flips all three to `.workspace/...`. All k8s passthrough commands (`obol kubectl`, `obol helm`, `obol helmfile`, `obol k9s`) auto-set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml`.

## Required Test Env

```bash
export $(grep -v '^#' .env | xargs)
export OBOL_DEVELOPMENT=true
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol      # rebuild after every code change
```

## CLI Surface

| Group | Subcommands |
|---|---|
| `stack` | `init` `up` `down` `purge -f` |
| `agent` | `init` (deploys obol-agent singleton); `auth --runtime <runtime> obol-agent` (auth token) |
| `model` | `setup [--provider \| custom]` `prefer` `sync` `status` `list` `remove` |
| `network` | `list` `install` `add` `remove` `status` `sync` `delete` |
| `sell` | `inference` `http` `list` `status` `stop` `delete` `pricing` `register` |
| `hermes` | `onboard` `setup` `sync` `list` `delete` `wallet` `skills` (plus legacy `token` — **do not use**) |
| `openclaw` | `onboard` `setup` `sync` `list` `delete` `dashboard` `cli` `token` `skills` |
| `app` | `install` `sync` `list` `delete` |
| `tunnel` | `status` `login` `provision` `restart` `logs` |
| passthrough | `kubectl` `helm` `helmfile` `k9s` |
| meta | `update` `upgrade` `version` |

Use passthrough tools through `obol` (`obol kubectl ...`, `obol helm ...`) so
the active stack kubeconfig and dev paths are selected consistently.

## `obol sell http` — easy-to-misuse flag set

Common mistakes: `--model`, `--pay-to`, and `--network` do **not** exist on `sell http`.

```
--wallet      0x...          USDC recipient (NOT --pay-to)
--chain       base-sepolia   Payment chain  (NOT --network)
--per-request 0.001          Or --price / --per-mtok / --per-hour
--upstream    ollama         Upstream k8s service name
--port        11434
--namespace   llm            Sets BOTH the ServiceOffer ns AND the upstream service ns
--health-path /api/tags
```

Always pass the same `-n <ns>` to follow-ups (`sell status`, `sell stop`, `sell delete`). The CLI prints the correct ns on creation; copy that.

## Dev Registry Cache

When `OBOL_DEVELOPMENT=true`, `obol stack up` creates k3d pull-through caches and a local push target at cluster-create time:

| Upstream | Cache | Purpose |
|---|---|---|
| `docker.io` | `k3d-obol-docker-io.localhost:54100` | pull-through |
| `ghcr.io` | `k3d-obol-ghcr-io.localhost:54101` | pull-through |
| `quay.io` | `k3d-obol-quay-io.localhost:54102` | pull-through |
| n/a | `k3d-obol-local.localhost:54103` | local push target (`localhost:54103/...:dev`) |

Caveats:

- Pull-through caches do **not** speed up local-build flows (`docker build` runs on the host daemon, `k3d image import` bypasses registries). Use the local push target (`just dev-frontend` does this).
- Registry config is only set up at cluster create. If `obol stack up` is starting an existing cluster, registry setup is skipped — recreate (`obol stack down && obol stack up`) once to pick up new entries.
