## Project Overview

Obol Stack: AI agents running decentralised infra locally. k3d cluster + Hermes default agent + optional OpenClaw instances + blockchain networks + payment-gated inference (x402) + Cloudflare tunnels. CLI: `github.com/urfave/cli/v3`.

## Conventions

- **Commits**: Conventional commits — `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `security:` with optional scope.
- **Branches**: `feat/`, `fix/`, `research/`, `docs/`, `chore/` prefixes.
- **GitHub branch policy**: NEVER push `codex/`-prefixed branches to GitHub; rename to a non-codex prefix first.
- **Architecture reference**: `@.claude/skills/obol-stack-dev/SKILL.md` (invoke via `/obol-stack-dev`).
- **Review scope**: No broad/vague boundaries. State exact files, invariants, expected evidence. Prefer concrete checks ("controller cannot access signer/Secrets", "agent write RBAC is namespace-scoped", "flow uses real obol CLI path") over generic "review architecture".
- **Planning vs user docs**: `plans/` -> implementation plans, design notes, retrospectives. `docs/` -> durable user-facing docs. Don't mix.
- **Release descriptions**: Use `.github/release-template.md`. The release workflow creates a draft with generated notes; rewrite the narrative body from the template. Keep generated `What's Changed`, `New Contributors`, and `Full Changelog` sections at the bottom. NEVER include private keys, seed phrases, passwords, hostnames, personal paths, or raw bearer tokens.

## Build, Test, Run

```bash
just build                                    # build with version info
go build -o .workspace/bin/obol ./cmd/obol    # build to .workspace/bin/obol
go build ./...                                # check compilation
go test ./...                                 # all unit tests
go test -v -run 'TestName' ./internal/pkg/    # single test

# Integration tests (//go:build integration; skip when prerequisites missing)
# Requires running cluster + host Ollama
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR=$(pwd)/.workspace/config OBOL_BIN_DIR=$(pwd)/.workspace/bin OBOL_DATA_DIR=$(pwd)/.workspace/data
go build -o .workspace/bin/obol ./cmd/obol    # MUST rebuild after code changes
go test -tags integration -v -timeout 15m ./internal/openclaw/

# Paid-inference commerce loop — needs host Ollama qwen3.5:9b
# Does NOT replace release-gate flows 11/13/14 (those require OBOL_LLM_ENDPOINT → vLLM/llama.cpp)
# Reuse worktree cluster: export OBOL_CONFIG_DIR=<worktree>/.workspace/config
go test -tags integration -v -run TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance -timeout 30m ./internal/openclaw/

# Release-gate buyer/seller smoke — needs OBOL_LLM_ENDPOINT → OpenAI-compatible vLLM/llama.cpp
RELEASE_SMOKE_INCLUDE_OBOL=true RELEASE_SMOKE_INCLUDE_OBOL_FORK=true \
  OBOL_LLM_ENDPOINT=http://127.0.0.1:8000/v1 OBOL_LLM_MODEL=qwen36-deep \
  bash flows/release-smoke.sh

just up    # obol stack init → up
just down  # obol stack down → purge
just clean # rm -rf bin/ .workspace/bin/

OBOL_DEVELOPMENT=true ./obolup.sh  # one-time dev setup (uses .workspace/, go run wrapper)
```

Integration tests use `//go:build integration`; skip when prerequisites missing.

## Architecture

**Two parts**: `obolup.sh` (bootstrap installer, pinned deps) + `obol` CLI (Go binary, all management).

**Design**: Deployment-centric (unique namespaces via petnames), local-first (k3d), XDG-compliant, two-stage templating (CLI flags -> Go templates -> Helmfile -> K8s).

**Routing**: Traefik + Kubernetes Gateway API. GatewayClass `traefik`, Gateway `traefik-gateway` in `traefik` ns.

| Visibility | Hostnames | Route | Backend |
|---|---|---|---|
| Local only | `["obol.stack"]` | `/` | frontend (obol-frontend ns) |
| Local only | `["obol.stack"]` | `/rpc` | eRPC (erpc ns) |
| Public (tunnel) | none | `/services/<name>/*` | x402 ForwardAuth -> upstream |
| Public (tunnel) | none | `/.well-known/agent-registration.json` | ERC-8004 httpd |
| Public (tunnel) | none | `/skill.md` | service catalog |
| Public (tunnel) | none | `/api/services.json` | service catalog JSON feed (`displayName`, `tagline`, `logoUrl`, `services[]`) |
| Public (tunnel) | tunnel hostname only | `/` | storefront landing page (Next.js) |

**NEVER remove hostname restrictions from frontend or eRPC HTTPRoutes** — exposing the frontend/RPC to the public internet is a critical security flaw.

**Config**: `Config{ConfigDir, BinDir, DataDir, StateDir}` (`internal/config/config.go:9`). Precedence: `OBOL_CONFIG_DIR` > dev mode (`.workspace/config`) > `XDG_CONFIG_HOME/obol` (falls back to `~/.config/obol`). `OBOL_DEVELOPMENT=true` -> `.workspace/` dirs. All K8s tools auto-set `KUBECONFIG=$OBOL_CONFIG_DIR/kubeconfig.yaml`.


## CLI Commands

```
obol
├── stack           init, up, down, purge, export, import
├── agent           init, new, sync, setup, auth, list, delete, update
│   └── wallet      address, list, backup, restore
├── wallet          import
├── network         install, sync, delete, list, add, remove, status
├── sell            inference, http, agent, mcp, demo, list, status, test, stop, update, delete, pricing, register, identity, info, resume
├── buy             inference
├── hermes          passthrough (SkipFlagParsing) → native hermes CLI
│                   Token retrieval moved to `obol agent auth`
├── openclaw        onboard, sync, token, list, delete, setup, dashboard, cli
│   ├── wallet      backup, restore, address, list
│   └── skills      add, remove, list
├── model           setup (has sub: custom), status, token, sync, pull, list, prefer, discover, remove
├── app             install, sync, list, delete
├── tunnel          status, setup, restart, stop, logs (login hidden: browser-managed fallback)
├── domain          list, search, check, register
├── kubectl/helm/helmfile/k9s   Passthrough (auto KUBECONFIG)
├── update          Helm + CLI version check (--json)
├── upgrade         Apply chart upgrades (--defaults-only, --pinned, --major)
└── version
```

- `buy inference [<seller-url>]`: positional seller URL (default `https://inference.v1337.org/`). Walks `/api/services.json`, auto-resolves model + token from the catalog asset, prompts in a TTY (auto-top-up Y/N → count with cost preview → confirm), and pre-signs via the agent's remote signer. `--agent X` pays from X AND switches X's hermes-config to `paid/<model>` in-pod (no global model_list reorder); `--set-default` promotes globally + syncs every agent. `--cost-cap` bounds future top-ups against price hikes (reconcile loop re-probes seller pricing). Identity verification is opt-in via `--expected-agent-id`.
- `agent auth` (alias `token`): `--runtime [hermes|openclaw|all]`, `--regenerate`; positional `[instance-name]` defaults to stack-managed agent. Replaces legacy `hermes token`.
- `agent new` (alias `onboard`): CRD-declared sub-agent via `--model`, `--skills`, `--objective`, `--create-wallet`. Without positional name, falls back to legacy host-rendered Hermes/OpenClaw onboard.
- `network install` has dynamic subcommands (one per supported chain; `--help` to list). `network sync [<network>/<id>]` with `--all`.
- `sell info` is the buyer's-eye read of the storefront: `obol sell info` prints branding + every on-sale service (rendered from the published `/api/services.json` envelope, i.e. only operationally-ready offers); `obol sell info <name>` focuses one service + how-to-buy; `--verbose` adds health/detail. Operator health/conditions (incl. not-ready/draining offers) stay under `sell status`. Branding writes live under `sell info set` (no flags on a TTY → interactive; with flags → patch only those fields) and `sell info reset` (no flags → all defaults; with field flags → reset only those). Writes `x402/obol-storefront-profile`; controller merges into the catalog envelope. Example: `obol sell info set --display-name "Acme Labs" --tagline "Paid APIs." --logo-url "https://…"`. **Dev note**: branding changes need both a dev-built `obol` CLI (`go build -o .workspace/bin/obol ./cmd/obol`) and a current `serviceoffer-controller` image — if `sell info set` times out with `timed out waiting for controller to publish /api/services.json`, the profile ConfigMap was likely applied but the running controller is still publishing the legacy bare-array catalog. Rebuild + roll the controller before retrying: `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller OBOL_DEVELOPMENT=true obol stack up`, or `docker build -f Dockerfile.serviceoffer-controller -t ghcr.io/obolnetwork/serviceoffer-controller:latest . && k3d image import … -c <cluster> && kubectl rollout restart deploy/serviceoffer-controller -n x402`.
- `sell mcp [name]` runs a foreground x402-paid MCP server: forwards buyer JSON args to a backend HTTP service, injecting the seller's own API key (buyer never sees it). Payment rides MCP `_meta` (`internal/x402mcp`).
- `sell resume` replays every persisted sell offer (inference incl. detached host-gateway relaunch; http/agent/demo-agent via the manifest ledger at `$OBOL_CONFIG_DIR/sell-http/`) — run after a host reboot; `obol stack up` runs the same path. `--install-boot-unit` adds a systemd user unit (Linux). `sell mcp` is foreground-only, no offer, not resumed.
- `tunnel setup [<token>]`: the one permanent-URL command. Connector-token based (dashboard-managed) — no host binary, no account-wide API key. Accepts the bare connector token, the `--token` flag, a positional arg, or the whole `cloudflared tunnel run --token …` line (prefix stripped via `extractConnectorToken`). Reuses the remote runtime (`ProvisionWithToken` → `TUNNEL_TOKEN` secret, chart `management_mode=remote`); DNS/ingress are configured by the user in the Cloudflare dashboard (route Public Hostname → `http://traefik.traefik.svc.cluster.local:80`), not via API. The API-token provisioning path was removed (no more `tunnel provision`, no setup `--api-token/--account-id/--zone-id/--register-domain`). `--management local` (alias hidden `tunnel login`) is the browser fallback (needs `cloudflared`). `tunnel status` reads connector health from cloudflared's in-cluster `/ready`+`/metrics` (port 2000, no token) plus a public HTTP probe; concise by default, `--verbose` for replicas/pods, `--no-probe` to stay offline. Domain management lives under `obol domain` (`list`, `search`, `check`, `register`) — an optional CLI wrapper around Cloudflare Registrar; still uses a scoped Cloudflare **API token** (Account → Domain perm, via `--api-token`/`CLOUDFLARE_API_TOKEN`; on a TTY it walks you through token creation and prompts). `--api-token` deliberately has NO `-t` alias to avoid colliding with `tunnel setup -t` (connector token — a different credential). `register` is billable (needs a payment method on the CF account); on success it prints the `obol tunnel setup --hostname …` handoff.
- `hermes` is passthrough to native hermes CLI via `hermes.CLI()` (cmd/obol/hermes.go:27). No Go-level subcommands registered.
- `bootstrap` (cmd/obol/bootstrap.go) is a hidden command for installer use only — not user-facing.

## Infrastructure Stack

Deployed by `obol stack up` from `internal/embed/infrastructure/`. Key templates (`base/templates/`): `llm.yaml` (LiteLLM + Ollama), `x402.yaml` (x402-verifier + serviceoffer-controller), `obol-agent.yaml` (singleton ns), `serviceoffer-crd.yaml`, `registrationrequest-crd.yaml`, `obol-agent-monetize-rbac.yaml`, `local-path.yaml`. Plus `cloudflared/` Helm chart, `values/*.yaml.gotmpl` (eRPC, monitoring, frontend).

Namespaces: eRPC -> `erpc`, Frontend -> `obol-frontend`, Cloudflared -> `traefik`, Monitoring/Prometheus -> `monitoring`, LiteLLM -> `llm`, x402-verifier + serviceoffer-controller -> `x402`, default obol-agent (Hermes) -> `hermes-obol-agent`. ServiceOffer + RegistrationRequest CRDs (cluster-scoped).

## Monetize Subsystem

Payment-gated access to cluster services via x402 (HTTP 402 micropayments, Traefik ForwardAuth). Supports USDC (EIP-3009) and OBOL (Permit2) via `--token [USDC|OBOL]`; default USDC on Base Mainnet. Token registry: `internal/x402/tokens.go`.

**Sell flow**: `obol sell http` -> ServiceOffer CR -> controller reconciles: ModelReady -> UpstreamHealthy -> PaymentGateReady (x402 Middleware) -> RoutePublished (HTTPRoute) -> Registered (RegistrationRequest + optional ERC-8004) -> Ready. Traefik routes `/services/<name>/*` through ForwardAuth to upstream.

**Buy flow**: `buy.py probe` sees 402 pricing -> `buy.py buy` validates on-chain token contract -> pre-signs payment auths (ERC-3009 for USDC, Permit2 for OBOL) into `PurchaseRequest` CR in agent ns -> serviceoffer-controller writes buyer config/auth into `llm` ns, publishes `paid/<remote-model>` -> in-pod `x402-buyer` sidecar spends one auth per paid request. Agent-managed refill: `buy.py process --all`, NOT the controller.

**buy.py** at `${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py` (skill: `buy-x402`, not `buy`):
```
probe  <endpoint-url> [--model <id>]                 Probe x402 pricing (--type http|inference|agent)
pay    <url> [--data <json>] [--timeout <s>]         One-shot paid request (1 auth, X-PAYMENT; no sidecar)
pay-agent <url> --model <id> --message '<text>'      One-shot paid STREAMING agent call: POSTs
       [--data <json>] [--timeout <s>]               /v1/chat/completions stream:true, SSE → stdout (1h default timeout)
buy    <name> --endpoint <url> --model <id>          Pre-sign auths + create PurchaseRequest
       [--budget <micro-units>] [--count <N>]
       [--auto-refill] [--refill-threshold <N>] [--refill-count <N>]
process <name> | --all                                Reconcile auto-refill against live sidecar state
list                                                  List purchased providers
status <name>                                         Sidecar auth pool + spent count
balance [--chain <network>]                           Wallet address + USDC balance
maintain                                              Alias for process --all
```
`buy.py balance` prints `Wallet: 0x...` as first line. No `wallet` subcommand.

**Endpoint URL: host vs pod**: `obol.stack:8080` resolves only on Mac host (DNS resolver). From any pod always use Traefik cluster-internal address:
- Host:   `http://obol.stack:8080/services/<name>/...`
- In-pod: `http://traefik.traefik.svc.cluster.local/services/<name>/...`

**Direct HTTP buy (no LiteLLM / no x402-buyer)**: NOT a supported production path through Traefik ForwardAuth. Keep `verifyOnly: true` on `x402-verifier` — verifies payment, skips settlement. Use `obol sell inference` for raw `X-PAYMENT` buyers (gateway settles in-process after upstream success).

Port-forward to `x402-verifier` and calling `/verify` directly: MUST set `X-Forwarded-Uri` (and usually `X-Forwarded-Host`) as Traefik does; otherwise **403** `forbidden: missing forwarded URI`. Verifier endpoint is ForwardAuth integration/debugging only, not a full paid-request path.
`sell http --upstream ollama`: requests with `Host: obol.stack` may be rejected upstream with **403**. Prefer `x402-buyer` for paid production; prefer `obol sell inference` for raw `X-PAYMENT`.

**Standalone inference gateway** (`obol sell inference`): separate from LiteLLM+buyer path. With live cluster + kubeconfig: disables built-in x402 (`NoPaymentGate`), publishes `ServiceOffer` -> Traefik + x402-verifier gate traffic to host listener; run gateway on `0.0.0.0:<port>` so in-cluster Service+Endpoints reach it. Standalone host (no cluster): gateway uses own x402 middleware (`verifyOnly`/settle per config).

**Quick full-cycle smoke test** (sell + buy):
1. POST seller route without `X-PAYMENT`, expect `402` + accepts requirements
2. `buy.py buy <name> --endpoint <url> --model <id> --count N`, expect PurchaseRequest `Ready` + sidecar `/status` `remaining > 0`
3. Send LiteLLM request with model `paid/<remote-model>`, expect `200`
4. Sidecar `/status` moves `remaining -1`, `spent +1` after one paid call
5. Create purchase with `--auto-refill ...`, run `buy.py process --all`, confirm loop only signs when live `/status` at or below threshold

**PurchaseRequest status caveat**: `PurchaseRequest.status` (`conditions[].message`, `remaining`, `spent`) is controller's last reconciled snapshot, NOT live per-request counter. For real-time auth pool + refill decisions, always query `x402-buyer` `GET /status` in litellm pod.

**CLI**: `obol sell pricing --pay-to --chain`, `obol sell inference <name> --model --pay-to --price|--per-mtok [--token USDC|OBOL]`, `obol sell http <name> --pay-to --chain --price|--per-request|--per-mtok --upstream --port --namespace --health-path [--token USDC|OBOL]`, `obol sell list|status|stop|delete`, `obol sell register --chain [--name]`, `obol sell register x402scan [--origin]`.

**x402scan registration** (`obol sell register x402scan`, `internal/x402scan/`): submits the storefront origin to the x402scan.com discovery index via `POST /api/x402/registry/register-origin`. Auth is SIWX (EIP-4361/SIWE challenge in the 402 body, signed EIP-191 by the agent's remote-signer via `POST /api/v1/sign/{addr}/message`, retried with the payload base64-encoded in the `SIGN-IN-WITH-X` header). x402scan crawls the origin's `/openapi.json` (already published with `x-payment-info` per paid op) and live-probes each endpoint for a real 402. Rejects `obol.stack`/localhost and `*.trycloudflare.com` quick-tunnels locally — needs a permanent tunnel hostname. Idempotent; stale offers are deprecated server-side.

**`obol sell http` flag reference** (common mistakes: `--model`, `--network` do NOT exist; `--wallet` is a deprecated alias for `--pay-to`):
```
--pay-to      0x...          USDC recipient address (primary; --wallet deprecated alias)
--chain       base-sepolia   Payment chain
--token       USDC           Payment token (USDC, OBOL)            [default: USDC]
--per-request 0.001          Price per request     (or --price, --per-mtok, --per-hour)
--upstream    ollama         Upstream k8s service name
--port        11434          Upstream service port                 [default: 8080]
--namespace   llm            Controls TWO things with the same value [default: "default"]:
                               1. ServiceOffer CR namespace
                               2. upstream k8s service namespace
--health-path /api/tags      Upstream health check path            [default: /health]
```
**Critical**: `--namespace` sets BOTH the ServiceOffer namespace and the upstream service namespace to the same value. Always pass the same `-n <namespace>` to every follow-up command (`sell status`, `sell stop`, `sell delete`). The CLI itself prints the correct namespace after creation.

Example — expose Ollama (lives in `llm` ns) as a paid endpoint:
```bash
obol sell http ollama-gated \
  --upstream ollama --port 11434 --namespace llm --health-path /api/tags \
  --per-request "0.001" --chain "base-sepolia" --pay-to "0x<wallet>"
# CLI prints: "Check status: obol sell status ollama-gated -n llm"

obol sell status ollama-gated -n llm
obol sell stop   ollama-gated -n llm
obol sell delete ollama-gated -n llm
```

**ServiceOffer CRD** (`obol.org`): Source of truth for monetized service intent. Spec fields — `type` (inference|fine-tuning|http|agent), `model{name,runtime}`, `upstream{service,namespace,port,healthPath}`, `payment{scheme,network,payTo,asset,price{perRequest,perMTok,perHour,perEpoch},maxTimeoutSeconds}`, `path`, `registration{enabled,name,description,image,services,skills,domains,supportedTrust,metadata}`, `provenance`, `drainAt`, `drainGracePeriod`. Type `agent` resolves upstream from an Agent CR via `agent.ref`. Declared in `internal/monetizeapi/types.go`.

**x402-verifier** (`x402` ns): ForwardAuth middleware only. No match -> pass through. Match + no payment -> 402. Match + payment -> verify with facilitator. `verifyOnly: true` permanent (set in `internal/embed/infrastructure/base/templates/x402.yaml:35`). `x402-verifier` is NOT the final settlement point for the supported production flow; it gates requests before they reach settlement-aware components (`x402-buyer`). Static defaults from `x402-pricing` ConfigMap; live per-offer routes derived in-memory from published ServiceOffers.

**Agent-backed offers** (`obol sell agent <name>`): wrap an `Agent` CR (from `obol agent new`) in a `type=agent` ServiceOffer. Controller resolves `spec.agent.ref` -> agent's Hermes endpoint (port 8642), surfaces `agentModel`/`agentSkills`/`agentRuntime` in the 402 `extra`, gates `/services/<name>/*` via `x402-verifier.HandleProxy`. Hermes serves OpenAI-compatible `/v1/chat/completions` (streaming + non-streaming, same path).

**Streaming**: SSE chunks flush per-write end-to-end through `x402-verifier.HandleProxy` (seller gateway for `sell agent`/`sell http`). Prefer `stream: true` for any paid agent inference that may exceed Cloudflare quick-tunnel's ~100s idle window — non-streaming sends zero bytes until upstream is done, so the tunnel drops before the buffered body arrives. `statusRecorder.Flush` (`internal/x402/verifier.go`) MUST forward to the underlying `http.Flusher`; embedding the bare `http.ResponseWriter` hides the concrete Flusher and silently buffers the whole response. Regression: `internal/x402/verifier_test.go::TestVerifier_HandleProxy_StreamsSSEChunks`.

**serviceoffer-controller** (`internal/serviceoffercontroller/`, binary `cmd/serviceoffer-controller/`): Watches ServiceOffers + RegistrationRequests, adds finalizers, creates Middleware + HTTPRoute, publishes registration resources, drives tombstone cleanup on delete.

**ERC-8004**: Registration publication isolated behind `RegistrationRequest`. Controller serves `/.well-known/agent-registration.json` from dedicated child resources, watches the chain selected by the offer's `payment.network` (`mainnet`, `base`, `base-sepolia`) for the matching registration tx — submitted by the operator via `obol sell register`, never by the controller. Per-chain RPC routed through in-cluster eRPC at `http://erpc.erpc.svc.cluster.local/rpc/<alias>` (override via `ERC8004_RPC_BASE` env on controller; default `http://erpc.erpc.svc.cluster.local/rpc` in `internal/erc8004/abi.go:27`). `obol sell register --chain` defaults to `mainnet` (cmd/obol/sell.go:2752); `obol sell http --chain` defaults to `base` (cmd/obol/sell.go:538).

**RBAC** (`internal/embed/infrastructure/base/templates/obol-agent-monetize-rbac.yaml`): Controller owns child-resource + registration write access. Agent (Hermes SA in `hermes-obol-agent`, OpenClaw SA in `openclaw-obol-agent`) gets read on `serviceoffers` + `serviceoffers/status`, full CRUD on `serviceoffers` + `purchaserequests` for compatibility commands, and agent-factory writes on `namespaces`, `secrets`, `agents` (hermes-only).

## RPC Gateway

`obol network add|remove|status` manages remote RPCs via eRPC ConfigMap. Default: read-only (blocks `eth_sendRawTransaction`, `eth_sendTransaction`). `--allow-writes` flips readOnly → route `eth_sendRawTransaction` to the user-chosen upstream via eRPC per-method selection policy. `--endpoint` adds a custom RPC directly (skips ChainList). Key functions in `internal/network/rpc.go`: `AddPublicRPCs()` (ChainList), `AddCustomRPC()`, `ListRPCNetworks()`. Record-on-write: add/remove update `$CONFIG_DIR/rpc/recorded-upstreams.yaml` (`internal/network/record.go`); `obol stack up` replays via `ReconcileRecordedRPCs()` so remote RPCs survive cluster recreation. Local-node upstreams are NOT recorded (`network sync` re-registers them).

## Network Management

Two-stage templating: `values.yaml.gotmpl` annotated with `@enum`/`@default`/`@description` → CLI flags (dynamic `--id`, `--mode`, `--since`, etc. generated by `internal/network/parser.go`) → rendered `values.yaml` (Stage 1), then `helmfile sync --state-values-file values.yaml --state-values-set id=<id>` (Stage 2). Unique namespaces: `<network>-<id>` where ID is petname or `--id <name>`. Local Ethereum nodes auto-registered as priority upstream in eRPC via `RegisterERPCUpstream()` (writes blocked on local → routed to remote).

**Ethereum `--mode full|archive`** (default `full`): reth pruned full node (~500GB exec / ~200GB cons mainnet, ~100GB / ~50GB testnet) vs archive node retaining all historical state (~4.5TB exec / ~500GB cons mainnet, ~300GB / ~100GB testnet). Archive mode for state replay (block explorers, historical `eth_call`, indexers); full mode is the right default. Mode flows through to: (a) reth `--full` arg in `internal/embed/networks/ethereum/helmfile.yaml.gotmpl`, (b) PVC sizing in `templates/pvc.yaml` via `executionStorageSize`/`consensusStorageSize`, (c) helmfile `persistence.size`. `obol network install ethereum` runs disk-space preflight via `internal/network/preflight.go` — warns when `cfg.DataDir` free disk < expected, prompts, auto-continues in non-interactive mode (no TTY / JSON output). Other execution clients (geth, nethermind, besu, erigon) ignore the mode flag.

**Ethereum `--since` (partial archive)**: when `--mode=archive` would otherwise mean genesis-to-tip, `--since` bounds the archive at a known historical point → reth `--prune.{account-history,storage-history,receipts,bodies}.{before,distance}` flags. Accepted forms: EL hardfork names (`merge`, `shanghai`, `cancun`, `prague`, `osaka` — mainnet only, verified block numbers in `internal/network/hardforks.go`); durations (`365d`, `1y`, `6mo` — resolved against post-merge 12s slot rate → distance in blocks); raw block numbers (`22500000`); or `genesis`/`all` (full archive, no extra args). Resolution in `resolveEthereumArchiveScope` in `internal/network/picker.go`: `--since` wins outright; `--mode` unset + TTY → full vs archive picker; `--mode=archive` without `--since` + TTY → Archive scope picker (hardfork presets + custom block + 365 days + genesis). Non-TTY: mode unset → `full`; mode=archive → `since=genesis`. Resolved scope written to `values.yaml` as `pruneKind`/`pruneBlock`/`pruneDistance`, + storage profile `executionStorageSize`/`consensusStorageSize`/`diskRequirementGB`. Partial archive wired only for reth; geth/besu/erigon/nethermind emit warning, run with chart defaults. Hardfork-name presets rejected on testnets (mainnet block numbers don't apply).

## Stack Lifecycle

| Command | Action |
|---------|--------|
| `obol stack init` | Generate cluster ID (petname), resolve absolute paths, write k3d.yaml, copy infrastructure |
| `obol stack up` | `k3d cluster create`, export kubeconfig, k3s auto-applies manifests, auto-configures LiteLLM with Ollama models (preserves Ollama modified-time order; `:cloud` aliases demoted behind local chat models; embedding-only models last; warns + suggests `ollama pull qwen3.5:4b` when empty or all-`:cloud`), re-imposes recorded model config (`model.ReconcileRecorded` — operator's `obol model` choices win over auto-detect), deploys default Hermes agent, applies agent capabilities, starts persistent Cloudflare tunnel, then replays recorded state: RPC upstreams (`network.ReconcileRecordedRPCs`) → Agent CRs (`agentcrd.ResumeAll`) → sell offers (`resumeSellOffers`; agent-type offers ride the sell-http store). Guard: `cmd/obol/stackup_resume_guard_test.go` |
| `obol stack down` | `k3d cluster stop` (delete fallback; preserves config + data) |
| `obol stack purge [-f]` | Delete config; `-f` also deletes root-owned PVCs; `-f` offers a full `stack export` first (fallback: OpenClaw wallet prompt) |
| `obol stack export` | Full backup archive: config dir (minus kubeconfig/defaults), agent data dirs (brains + keystores, deployments quiesced for consistency), encrypted wallet backups, etcd-drift resources (Agent CRs, ServiceOffers, LiteLLM/eRPC CMs). `internal/stackbackup/` |
| `obol stack import <archive>` | Restore: host state first (then `stack up` mounts restored brains/keystores), `--cluster-only` re-applies CRs/CMs + re-syncs agents after up. PurchaseRequests/buyer auths intentionally not restored (auths expire) |

k3d: 1 server, ports `80:80` + `8080:80` + `443:443` + `8443:443`, image `rancher/k3s:v1.35.1-k3s1`.

**Local access**: macOS port 80 privileged — may not bind without root. Always use `http://obol.stack:8080/` (not `http://obol.stack/`). Port 8080 maps to same Traefik load balancer as port 80.

### Dev Registry Cache

`OBOL_DEVELOPMENT=true` -> `obol stack up` ensures pull-through k3d registry caches and a local push target; wires new clusters to use them:

- `docker.io` -> `k3d-obol-docker-io.localhost:54100` (pull-through)
- `ghcr.io` -> `k3d-obol-ghcr-io.localhost:54101` (pull-through)
- `quay.io` -> `k3d-obol-quay-io.localhost:54102` (pull-through)
- `localhost:54103` -> `k3d-obol-local.localhost:54103` (local push target, no upstream proxy)

Generated k3d registry config written to `$OBOL_CONFIG_DIR/registries.yaml`. Cache data under `~/.local/state/obol/registry-cache/` by default, or under `OBOL_REGISTRY_CACHE_DIR` when set.

Local push target: `just dev-frontend` builds `obol-stack-front-end`, pushes `localhost:54103/obol-stack-front-end:dev`, **imports into the active k3d cluster** (`k3d image import` — required because `imagePullPolicy: IfNotPresent` caches the `:dev` tag), and restarts the frontend pod. Use `just dev-frontend-rebuild` after code changes (forces `docker build --no-cache`). Reset: `just dev-frontend-reset`.

### Local frontend development

**Two ways to run the UI:**

| Mode | URL | When |
|------|-----|------|
| In-cluster (recommended) | `http://obol.stack:8080` | Stable Prometheus/disk metrics; no port-forwards |
| Host `pnpm run dev` | `http://obol.stack:3000` | Fast React HMR; needs kubeconfig + optional Prometheus/eRPC port-forwards (see frontend `.env.example`) |

**Single cluster when dev-building from this repo** — copy `.envrc.local.example` → `.envrc.local` and `source` it (or `direnv allow`). This sets `OBOL_CONFIG_DIR=$HOME/.config/obol` so `obol kubectl` / `just dev-frontend` hit your real stack instead of spawning a second `.workspace` cluster.

```bash
cd obol-stack && source .envrc.local
FRONTEND_DIR=../obol-stack-front-end just dev-frontend-rebuild   # after UI code changes
open http://obol.stack:8080
```

**k3d vs `obol stack up`:** `k3d cluster start` only powers Docker containers + API. `obol stack up` deploys Helm infra, agents, tunnel replay, etc. Use k3d directly only for loadbalancer/port emergencies; otherwise `obol stack up`.

**Stale frontend image symptoms:** build log shows all `CACHED` layers → use `dev-frontend-rebuild`; rollout succeeds but UI unchanged → `k3d image import` (now automatic in `just dev-frontend`).

Caveats:
- Pull-through caches don't help host `docker build` flows — `k3d image import` bypasses registries entirely. The local push target is what speeds up local-build redeploys.
- Registry --registry-use wiring only happens during cluster creation. Recreate the cluster (`obol stack down && obol stack up`) to pick up new registry entries in an existing cluster.

## LLM Routing

**Host reachability** — only Traefik-published routes accessible via `obol.stack:8080`. Everything else ClusterIP, needs `kubectl port-forward`:

| Service | How to reach from Mac host |
|---------|---------------------------|
| Traefik ingress (frontend, eRPC, x402 routes) | `http://obol.stack:8080/...` |
| LiteLLM (`llm` ns, port 4000) | `kubectl port-forward svc/litellm 14000:4000 -n llm` → `http://127.0.0.1:14000` |
| x402-buyer sidecar (port 8402, no Service — pod only) | `kubectl port-forward -n llm <litellm-pod> 18402:8402` → `http://127.0.0.1:18402` |
| OpenClaw instance | `kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789` |

**Never call `http://obol.stack:8080/v1/...`** — Traefik has no `/v1` route, returns frontend 404.

**x402-buyer sidecar is distroless** — no `wget`, `curl`, or shell. Use port-forward, not `kubectl exec`.

**LiteLLM gateway** (`llm` ns, port 4000): OpenAI-compatible proxy → Ollama/Anthropic/OpenAI. ConfigMap `litellm-config` (YAML config.yaml with model_list), Secret `litellm-secrets` (master key + API keys). Auto-configured with Ollama models during `obol stack up` (no manual `obol model setup`). `ConfigureLiteLLM()` patches config + Secret + restarts or hot-adds via LiteLLM model API. Paid remote inference: Obol LiteLLM fork + `x402-buyer` sidecar, with static `paid/*` → `openai/*` → `http://127.0.0.1:8402` route (wildcard catch-all, requires >=1 concrete `paid/<model>` entry to be useful). Hermes uses provider `"custom"` pointed at `http://litellm.llm.svc.cluster.local:4000/v1`; optional OpenClaw instances reuse the `"openai"` provider slot (ollama slot disabled). Agent configs use `dangerouslyDisableDeviceAuth` for Traefik-proxied access.

**Auto-configuration**: `obol stack up` → `autoConfigureLLM()` detects host Ollama models, patches LiteLLM config. `obolup.sh` → `check_agent_model_api_key()` reads `~/.openclaw/openclaw.json`, resolves API key from `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` (Anthropic) or `OPENAI_API_KEY` (OpenAI), exports for downstream.

**BYOK cloud providers** (easiest getting-started path) — provider knowledge is a single registry in `internal/model/model.go` (`knownProviders` / `ProviderInfo` with `Mode`/`BaseURL`/`Default`/`KeyURL`/`JoinURL`/`Free`); adding a provider is one row, no per-provider switch. `KeyURL` is the API-key dashboard (assumes account); optional `JoinURL` is a new-user landing page (may carry a referral tag) used in preference to `KeyURL` for browser-open and "new to X? Sign up" hints. Built-in: `anthropic`, `openai`, `ollama` (native/local) + OpenAI-compatible aggregators `venice`, `openrouter`, `nvidia`, `gmi`, `novita`, `huggingface` (`Mode=openai-compatible` → `model_list` entry `openai/<id>` + explicit `api_base` + key from the provider's env var; no wildcard). When `--model` is omitted, setup uses the registry `Default` or lists the live `GET <base>/v1/models` (TTY picker / non-TTY error naming real ids). `--free` seeds the curated free-tier model snapshot (currently OpenRouter only) intersected against the live `/v1/models` response to drop rotated-out ids; auto-applied for `openrouter` when no `--model` is passed.

Single front door: `obol model setup` (engine: `setupCloudProvider` in `cmd/obol/model.go`). Interactive picker defaults to OpenRouter — `obol model setup` with no flags walks a TTY user through provider pick → browser open at `JoinURL`/`KeyURL` → key prompt → free-roster seeding. Scriptable variant: `obol model setup --provider <id> --api-key <key>`. Unlisted endpoints: `obol model setup custom --endpoint … --model …`. `obol buy inference <provider>` is reserved for future credit top-ups against remote providers; today it errors with a redirect to `obol model setup`. `obol buy inference [<seller-url>]` (URL or no arg) is the **x402 crypto-paid seller** path — unchanged.

```bash
obol model setup                                       # interactive; default = openrouter + free roster
obol model setup --provider venice                     # opens https://venice.ai/chat?ref=ZynMuD (TTY) + prompts for key
obol model setup --provider venice --api-key $VENICE_API_KEY   # scriptable / CI
obol model setup --provider openrouter --model openrouter/auto # paid OpenRouter (skips free-roster seeding)
```

**External OpenAI-compatible LLM** (vLLM / sglang / mlx-lm / remote GPU) — canonical user flow, no ConfigMap surgery:

```bash
obol stack up       # cluster + base infra (auto-config picks up host Ollama if present)

# Hermes picks first chat-capable entry in LiteLLM's model_list as default.
# Order is source of truth — see internal/model/rank.go (Rank(): preserves
# model_list order, only demotes embedding-only entries past chat-capable).
# Auto-detect prepends host Ollama → they win unless (a) or (b).

# (a) Drop auto-detected Ollama entries:
obol model remove qwen3.5:9b
obol model remove qwen3.5:4b

obol model setup custom \
    --endpoint http://192.168.18.23:8000/v1 \
    --model qwen36-deep
# setup custom validates endpoint, patches LiteLLM, calls
# syncAgentModels -> hermes.Sync -> rewrites agent deployment files.
# No manual restart needed.

# (b) Keep Ollama, promote custom entry to head:
obol model prefer qwen36-deep
obol model sync

obol model list       # confirm head of model_list
obol model status     # show provider state
```

Flow scripts (`flows/lib.sh:route_llm_via_obol_cli`) wrap this behind `OBOL_LLM_ENDPOINT` / `OBOL_LLM_MODEL` / `OBOL_LLM_API_KEY` env vars for smoke-test GPU targeting.

**Per-instance overlay**: `buildLiteLLMRoutedOverlay()` uses `"openai"` provider slot (Name: "openai", ollama slot disabled) → `http://litellm.llm.svc.cluster.local:4000/v1`, `api: openai-completions`, `agentModel: "openai/<model>"`. App → litellm:4000 → routes by model_name → actual API.

## Standalone Inference Gateway

`obol sell inference` — standalone OpenAI-compatible HTTP gateway with x402 payment gating, for bare metal / Secure Enclave.
`--vm` runs Ollama in Apple Containerization Linux micro-VM (plus `--vm-image`, `--vm-cpus`, `--vm-memory`, `--vm-host-port`).
Key code: `internal/inference/{gateway,container,store}.go`, `internal/enclave/{enclave,enclave_darwin,enclave_stub}.go` (Secure Enclave signing via CGo/Security.framework on Darwin, stub fallback elsewhere).

## Agent Runtimes & Skills

**Hermes** — stack-managed default runtime.

| Item | Value |
|------|-------|
| State dir | `applications/hermes/obol-agent` |
| Namespace | `hermes-obol-agent` |
| Service / Deployment | `hermes` |
| ConfigMap | `hermes-config` |
| PVC host path | `$DATA_DIR/hermes-obol-agent/hermes-data/.hermes` |
| Skills path (pod) | `/data/.hermes/obol-skills` (`OBOL_SKILLS_DIR` env, `skills.external_dirs`) |

**OpenClaw** — optional manual runtime.

| Item | Value |
|------|-------|
| State dir | `applications/openclaw/<id>` |
| Namespace | `openclaw-<id>` |
| Service / Deployment | `openclaw` |
| ConfigMap | `openclaw-config` |
| Skills path (pod) | `/data/.openclaw/skills` (PVC injection) |

**Obol skills**: `SKILL.md` + optional scripts/references, embedded in `obol` binary (`internal/embed/skills/`).

**Monetize skill** (`internal/embed/skills/monetize/`): thin compatibility wrapper around ServiceOffer CRUD, controller waiting, `/skill.md` publication.

**Remote-signer wallet**: `GenerateWallet()` in `internal/openclaw/wallet.go`. secp256k1 → Web3 V3 keystore → remote-signer REST API at port 9000 in same ns.

## Buyer Sidecar

`x402-buyer` — lean Go sidecar for buy-side x402 payments using pre-signed ERC-3009 authorizations. Second container in `litellm` Deployment (no separate Service). Agent `buy.py` signs auths locally → `PurchaseRequest`; controller writes per-upstream buyer config/auth files into buyer ConfigMaps, keeps LiteLLM routes in sync. Endpoints: `/status`, `/healthz`, `/metrics`, `/admin/reload`; metrics scraped via `PodMonitor`. Zero signer access, bounded spending (max loss = N × price).

Settlement lifecycle (cluster-routed paid flow):
- Traefik/x402-verifier stays on the verify-only path (`verifyOnly: true`).
- `x402-buyer` retries with `X-PAYMENT`, waits for successful upstream response (`<400`), then calls facilitator `/settle`.
- Pre-signed auth is persisted as consumed after a successful paid upstream response. `X-PAYMENT-RESPONSE` is optional metadata from settlement-aware seller paths; the buyer sidecar still passes through the upstream response when that header is absent.

Supported paths:
- For cluster-routed paid traffic, use `x402-buyer`.
- For direct raw `X-PAYMENT` buyers, use `obol sell inference`.
- Do not treat raw direct `X-PAYMENT` through Traefik ForwardAuth as a supported production payment path.

Key code: `cmd/x402-buyer/`, `internal/x402/buyer/`, `internal/x402/forwardauth.go`.

## Development Constraints

1. **Absolute paths required** — Docker volume mounts need absolute paths (resolved at `obol stack init`)
2. **Two-stage templating** — Stage 1 (CLI flags) → Stage 2 (Helmfile); separation is critical
3. **Unique namespaces** — each deployment must have unique namespace
4. **`OBOL_DEVELOPMENT=true`** — required for `obol stack up` to auto-build local images: `x402-verifier`, `serviceoffer-controller`, `x402-buyer`, `demo-server`, `obol-stack-public-storefront` (`public-storefront` alias accepted). Build path reuses same-name local tag for warm-run speed. `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES` controls force-rebuilds: `true`/`all` = every image; comma-separated list (e.g. `x402-verifier,serviceoffer-controller`) = only those; `false`/`0`/unset = skip. "Local dev images ready" summary surfaces when nothing was rebuilt.
5. **Root-owned PVCs** — `-f` flag required to remove in `obol stack purge`
6. **Narrow review boundaries** — for controller/RBAC/payment changes, spell out exact security and user-journey invariants before editing or delegating; broad review prompts have previously missed test drift

### OpenClaw Version Management

Three places pin the version — all must agree:
1. `internal/openclaw/OPENCLAW_VERSION` — source of truth (Renovate watches, CI reads)
2. `internal/openclaw/openclaw.go` — `openclawImageTag` constant
3. `obolup.sh` — `OPENCLAW_VERSION` shell constant for standalone installs

`TestOpenClawVersionConsistency` in `internal/openclaw/version_test.go` catches drift.

### Pitfalls

**First diagnostic when release-smoke goes red**: confirm what is actually deployed before reading verifier code. A `503 Payment verification failed` from Traefik is almost never a real verifier bug — it is usually one of pitfalls 9–13 below.

```bash
kubectl get deploy -n x402 x402-verifier -o jsonpath='{.spec.template.spec.containers[*].image}'
kubectl get deploy -n x402 serviceoffer-controller -o jsonpath='{.spec.template.spec.containers[*].image}'
kubectl get deploy -n llm  litellm -o jsonpath='{.spec.template.spec.containers[*].image}'
```

A registry digest pin instead of `:latest` on the verifier means your dev rewrite was bypassed (pitfall 9).

1. **Kubeconfig port drift** — k3d API port can change between restarts. Fix: `k3d kubeconfig write <name> -o .workspace/config/kubeconfig.yaml --overwrite`
2. **Agent RBAC binding empty** — `obol-agent-monetize-rbac` (Hermes default; legacy `openclaw-monetize-binding` for OpenClaw runs) may have empty subjects if `obol agent init` races with k3s manifest apply. Re-run `obol agent init`.
3. **ConfigMap propagation** — ~60-120s for k3d file watcher; force restart for immediate effect
4. **ExternalName services** — do not work with Traefik Gateway API; use ClusterIP + Endpoints
5. **eRPC `eth_call` cache** — default TTL is 10s for unfinalized reads; `buy.py balance` can lag behind an already-settled paid request for a few seconds
6. **`/v1` required in `api_base` for `paid/*` route** — LiteLLM's OpenAI provider does NOT append `/v1` to a bare `api_base`. The buyer sidecar route must be `http://127.0.0.1:8402/v1`, not `http://127.0.0.1:8402`. Without `/v1`, LiteLLM calls `/chat/completions` on the buyer; the buyer's mux returns `404 page not found` (Go default), which LiteLLM surfaces as `OpenAIException - 404 page not found`.
7. **LiteLLM restart is fallback, not the default buy path** — the validated happy path is `buy.py buy`/`process --all`/same-name top-up without a manual LiteLLM restart. Controller hot-add/hot-delete + buyer reload is expected to make `paid/<model>` appear and disappear in place. If a paid alias still fails after controller reconciliation and buyer sidecar reports the upstream, restart LiteLLM as a fallback investigation step. Since rc14, Reloader auto-restarts LiteLLM on `litellm-config` changes (e.g. a buy that adds a NEW provider); buyer CM writes (top-ups/refills) hot-reload via `/admin/reload` with no restart — do not add the buyer CMs to the Reloader annotation.
8. **x402-verifier CA bundle missing → TLS failure** — The `x402-verifier` image is distroless (no CA store). The `ca-certificates` ConfigMap in `x402` namespace must be populated from the host CA bundle or the verifier cannot TLS-verify calls to the facilitator (`https://x402.gcp.obol.tech`), causing `x509: certificate signed by unknown authority` on every payment. **Fixed**: `obol stack up` calls `x402verifier.PopulateCABundle` after infrastructure deployment; `obol sell http` calls it before creating the ServiceOffer. If `Payment verification failed` errors still occur, check verifier logs for the x509 error and repopulate manually: `kubectl create configmap ca-certificates -n x402 --from-file=ca-certificates.crt=/etc/ssl/cert.pem --dry-run=client -o yaml | kubectl replace -f -`
9. **`EnsureVerifier` overwrites helmfile's image pin under `OBOL_DEVELOPMENT=true`** — `internal/x402/setup.go` reads embedded `x402.yaml` (hard-coded image pin) and `kubectl apply`s it. Without an in-memory rewrite this overwrites the helmfile-managed `:latest` deployment with the embedded pin → every source change to the verifier silently bypassed. Fix shipped in `5a10fb8` (rewrites pins in-memory before apply); structural regression test: `internal/x402/setup_structure_test.go` (`TestEnsureVerifier_NoInlineRegex`). **If you add a new component installed via `kubectl apply` of an embedded manifest**, give it the same dev-rewrite treatment.
10. **CAIP-2 vs legacy chain id form mismatch** — `RouteRule.Network` is normalized to CAIP-2 (`eip155:84532`) at one boundary, but `internal/x402/chains.go::ResolveChainInfo` must know both that form and the legacy alias (`base-sepolia`). If only one is registered, `matchPaidRouteFull` returns 404 silently on every paid request. When adding a new chain, register both the legacy alias and `CAIP2Network` in every `case` arm.
11. **anvil `--prune-history` is enable-pruning, not retention** — passing it to anvil drops historical state needed by the local x402-rs facilitator's `eth_getStorageAt`, surfacing as `state at block #N is pruned` and a misleading `503 Payment verification failed`. Never pass `--prune-history` to anvil. Also bind anvil with `--host 0.0.0.0` so in-cluster eRPC can reach it via `host.k3d.internal:8545` (loopback-only is the silent-503 case).
12. **Combo-form image-pin regex** — `internal/embed/infrastructure/...` may pin images as `<image>:<tag>@sha256:<digest>`. The dev-rewrite alternation in `internal/defaults/defaults.go` must list the longest form first (`:tag@sha256:digest` | `@sha256:digest` | `:tag`), or only the `:tag` portion gets rewritten, the `@sha256:` survives, and Docker honors the digest over the tag → local build silently bypassed. Test: `internal/defaults/defaults_test.go`.
13. **Free-tier Base Sepolia RPC throttling** — `drpc.org`, `sepolia.base.org`, and similar free-tier endpoints return HTTP 408 under release-smoke load (multiple anvil forks + balance reads + receipt scans). Flow-11 step 8 / flow-13 facilitator-reachability / flow-14 balance reads all start failing intermittently. Set `BASE_SEPOLIA_RPC=https://lb.drpc.live/base-sepolia/<paid-token>` or `ALCHEMY_BASE_SEPOLIA_API_KEY=<key>` in `.env`. `flows/release-smoke.sh` runs `warn_unpaid_base_sepolia_rpc` preflight; `cmd/obol/network.go::redactRPCURL` and `flows/lib.sh::scrub_secrets` collapse paid-RPC URLs to `[REDACTED].<domain>/[REDACTED]` so logs only ever surface the provider.
14. **First-request flake on freshly-deployed verifier** — the first request after `x402-verifier` becomes Ready can return an empty body / Bad Gateway from Traefik because the HTTPRoute is wired but the verifier's serviceoffer-source watcher has not loaded the route yet. `flows/flow-07-sell-verify.sh` and `flows/flow-08-buy.sh` wrap the 402-body fetch in a 12x5s retry loop. Do not extend retry loops elsewhere to mask intermittents — this one is a real first-request race.

15. **"0 spent" is not proof no money moved** — an error response (>= 400) carrying `X-PAYMENT-RESPONSE` with a tx hash means the facilitator settled on-chain and THEN failed; the buyer marks that auth consumed, buy.py prints a settled-on-chain warning. Chain is canonical; see `docs/observability.md` ("Verify settlement against the chain").
16. **Clusters created on <= v0.10.0-rc12 keep hostPath-typed PVs** — kubelet ignores `fsGroup` there, and v0.10.0's non-root pods (UID 1000, no chown inits) cannot read the legacy 10000-owned data. Symptom after ANY chart re-render (`agent sync`, model sync, tests that sync): `Init:CrashLoopBackOff` with `mkdir /data/.hermes: Permission denied`. Supported path: recreate the cluster (`obol stack export` -> recreate -> `import`); full steps in the v0.10.0 release notes (Breaking changes). Non-destructive workaround: `docker exec <k3d-node> chown -R 1000:1000 /data/<ns>/hermes-data` then delete the pod.
17. **EIP-7702-contaminated test accounts on a Base Sepolia fork** — standard anvil/hardhat accounts #1–#9 (the `test test ... junk` mnemonic) carry EIP-7702 delegation code (`0xef0100…`) from real-chain 7702 experiments on Base Sepolia. Base-Sepolia USDC is FiatTokenV2_2, which verifies EIP-3009 via `SignatureChecker.isValidSignatureNow` — any `from` with code routes to EIP-1271 `isValidSignature` and ignores a perfectly valid ECDSA signature, reverting `FiatTokenV2: invalid signature` (surfacing as facilitator 503 / `unexpected_error`). The buyer/`from` MUST be a freshly generated EOA (account #0 happens to be clean; payTo with code is fine — only the signer is checked). This is why flow-08 funds the agent's generated wallet, and why `flow-17-sell-mcp.sh` generates fresh buyer + seller keys and preflights `cast code "$BUYER_ADDR" == 0x`.
18. **x402 SDK signs `validAfter = now` with no past buffer** — the `x402-foundation/x402/go` client sets EIP-3009 `validAfter` to wall-clock now. An anvil fork's `block.timestamp` is pinned to the forked block and lags real time the longer the fork has been up, so verify/settle revert `FiatTokenV2: authorization is not yet valid`. In a normal release-smoke run flow-17 follows flow-10 immediately so the gap is tiny; flow-17 still defends with `cast rpc evm_setNextBlockTimestamp $((now+30)) && evm_mine` right before the paid call. (obol's own buy.py uses a past buffer and isn't affected.)
19. **`obol sell info set` times out but profile CM updated** — the dev CLI wrote `x402/obol-storefront-profile` but the in-cluster `serviceoffer-controller` is still on an image that publishes `/api/services.json` as a bare `services[]` array (no `displayName` envelope). Symptom: `configmap/obol-storefront-profile configured` then `timed out waiting for controller to publish /api/services.json`; `kubectl get cm -n x402 obol-skill-md -o jsonpath='{.data.services\.json}'` starts with `[` not `{`. Fix: rebuild + import + restart `serviceoffer-controller` (see `sell info` bullet above). `obol stack up` with a warm cache (`built == 0`) does not pick up controller source changes unless `OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES=serviceoffer-controller`.

For a fuller debug catalog with symptom->fix mapping, see `.agents/skills/obol-stack-dev/references/release-smoke-debugging.md`.

For observability architecture decisions (Prometheus retention vs. on-chain canonical record, counter-reset semantics, recording-rule naming, label conventions, CRD versioning stance, `clamp_min` epsilon), see `docs/observability.md` — read this before adding a new metric, recording rule, or proposing counter persistence.

### Security: Tunnel Exposure

The Cloudflare tunnel exposes the cluster to the public internet. Only x402-gated endpoints and discovery metadata should be reachable via the tunnel hostname. Internal services (frontend, eRPC, LiteLLM, monitoring) MUST have `hostnames: ["obol.stack"]` on their HTTPRoutes to restrict them to local access.

**NEVER**:
- Remove `hostnames` restrictions from frontend or eRPC HTTPRoutes
- Create HTTPRoutes without `hostnames` for internal services
- Expose the frontend UI, Prometheus/monitoring, or LiteLLM admin to the tunnel
- Run `obol stack down` or `obol stack purge` unless explicitly asked

**Public routes** (no hostname restriction, intentional):
- `/services/*` — x402 payment-gated, safe by design
- `/.well-known/agent-registration.json` — ERC-8004 discovery
- `/skill.md` — machine-readable service catalog
- `/api/services.json` — service catalog envelope (`displayName`, `tagline`, `logoUrl`, `services[]`)
- `/` on tunnel hostname — public storefront landing page (Next.js)

## Dependencies

| Package | Key Files | Role |
|---------|-----------|------|
| `cmd/obol` | `main.go`, `sell.go`, `network.go`, `openclaw.go`, `model.go` | CLI commands |
| `cmd/serviceoffer-controller` | `main.go` | ServiceOffer controller binary |
| `internal/config` | `config.go` | XDG Config struct |
| `internal/stack` | `stack.go` | Cluster lifecycle |
| `internal/network` | `network.go`, `erpc.go`, `rpc.go`, `parser.go` | Networks, eRPC, RPC gateway |
| `internal/monetizeapi` | `types.go` | Shared CRD types and GVR constants |
| `internal/serviceoffercontroller` | `controller.go`, `render.go` | ServiceOffer reconciliation controller |
| `internal/x402` | `config.go`, `setup.go`, `verifier.go`, `matcher.go`, `watcher.go`, `serviceoffer_source.go`, `source.go` | ForwardAuth verifier |
| `internal/x402/buyer` | `signer.go`, `proxy.go`, `config.go` | Buy-side sidecar |
| `internal/erc8004` | `client.go`, `types.go`, `abi.go` | ERC-8004 Identity Registry |
| `internal/agent` | `agent.go` | obol-agent singleton |
| `internal/model` | `model.go` | LiteLLM gateway configuration |
| `internal/openclaw` | `openclaw.go`, `wallet.go`, `resolve.go` | OpenClaw setup, wallet, instance resolution |
| `internal/inference` | `gateway.go`, `container.go`, `store.go` | Standalone x402 gateway |
| `internal/enclave` | `enclave.go`, `enclave_darwin.go`, `enclave_stub.go` | Secure Enclave keys |
| `internal/embed` | `embed.go` | Embedded assets (skills, infrastructure, networks) |

**Embedded assets**: `internal/embed/infrastructure/` (K8s templates), `internal/embed/networks/` (ethereum, aztec), `internal/embed/skills/` (21 skills).

**Tests**: `cmd/obol/sell_test.go` (CLI flags), `internal/x402/*_test.go` (verifier, config, matcher, E2E), `internal/erc8004/*_test.go` (ABI, client), `internal/embed/embed_crd_test.go` (CRD+RBAC validation), `internal/openclaw/integration_test.go` (full-cluster inference), `internal/openclaw/overlay_test.go`, `internal/inference/gateway_test.go`, `internal/serviceoffercontroller/*_test.go` (controller, render).

**Docs**: `docs/guides/monetize-inference.md` (E2E monetize walkthrough), `README.md`.

**Deps**: Docker 20.10.0+, Go 1.25+. Installed by obolup.sh: kubectl 1.36.1, helm 3.21.0, k3d 5.8.3, helmfile 1.5.2, k9s 0.50.18, helm-diff 3.15.7, ollama 0.24.0. Key Go: `urfave/cli/v3`, `dustinkirkland/golang-petname`, `coinbase/x402/go` (v2 SDK, v1 wire format).

## Related Codebases

These are sibling Git repositories that this stack integrates with. Set the env vars below to your local checkouts (or add a gitignored `CLAUDE.local.md` with absolute paths if you prefer).

| Resource | Repository | Env override |
|----------|------------|--------------|
| Frontend | `ObolNetwork/obol-stack-front-end` | `OBOL_FRONTEND_DIR` |
| Docs | `ObolNetwork/obol-stack-docs` | `OBOL_DOCS_DIR` |
| OpenClaw | `ObolNetwork/openclaw` | `OPENCLAW_DIR` |
| LiteLLM (Obol fork) | `ObolNetwork/litellm-fork` | `LITELLM_FORK_DIR` |
