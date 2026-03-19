# Autoresearch: Obol Stack Real User Flow Validation

## Objective
Validate that every documented user journey in Obol Stack works exactly as a
real human would experience it. Fix CLI bugs, error messages, timing issues,
and UX problems. Improve the flow scripts themselves when they're incomplete.

## Metric
steps_passed (count, higher is better) — each flow script emits STEP/PASS/FAIL.

## Source of Truth for User Flows
- `docs/getting-started.md` — Steps 1-6 (install → inference → agent → networks)
- `docs/guides/monetize-inference.md` — Parts 1-4 (sell → buy → facilitator → lifecycle)

Every numbered section in these docs MUST have a corresponding step in a flow script.
If a doc section has no flow coverage, that is a gap — add it.

## Self-Improving Research Rules
When a flow fails, determine WHY before fixing anything:

1. **Missing prerequisite?** (e.g., model not pulled, Anvil not running, Foundry
   not installed, USDC not funded) → Read the docs above, find the setup step,
   ADD it to the flow script, and re-run.

2. **Wrong command/flags?** (e.g., wrong --namespace, missing --port) → Run
   `obol <cmd> --help`, read the guide section, fix the flow script.

3. **CLI bug or bad error message?** (e.g., panic, misleading output, wrong exit
   code) → Fix the Go source code in cmd/obol/ or internal/, rebuild, re-run.

4. **Timing/propagation issue?** (e.g., 503 because verifier not ready yet) →
   Add polling with `obol sell status` or `obol kubectl wait`. If the wait is
   unreasonable (>5min), fix the underlying readiness logic in Go.

5. **Doc is wrong?** (e.g., doc says --per-request but CLI wants --price) →
   Fix the doc AND update the flow script. The CLI is the source of truth.

The flow scripts AND the obol-stack code are BOTH in scope for modification.

## Files in Scope
### Flow scripts (improve coverage, fix invocations)
- flows/*.sh

### CLI commands (fix bugs, improve UX)
- cmd/obol/sell.go, cmd/obol/openclaw.go, cmd/obol/main.go
- cmd/obol/network.go, cmd/obol/model.go, cmd/obol/stack.go

### Internal logic (fix timing, readiness, error handling)
- internal/stack/stack.go
- internal/openclaw/openclaw.go
- internal/agent/agent.go
- internal/x402/config.go, internal/x402/setup.go

### Documentation (fix if CLI disagrees)
- docs/getting-started.md
- docs/guides/monetize-inference.md

## Test Infrastructure — MUST REUSE existing Go helpers

The paid flows (flow-10, flow-08) MUST align with the existing integration test
infrastructure in `internal/testutil/`. Do NOT reinvent facilitator/Anvil setup.

Reference implementations (source of truth for test infra):
- `internal/testutil/anvil.go` — `StartAnvilFork()`: free port, `Accounts[]`, `MintUSDC()`, `ClearCode()`
- `internal/testutil/facilitator_real.go` — `StartRealFacilitator(anvil)`: discovers binary via
  `X402_FACILITATOR_BIN` or `X402_RS_DIR` or `~/Development/R&D/x402-rs`, points at Anvil RPC,
  uses `anvil.Accounts[0]` as signer, starts on free port, produces `ClusterURL` for k3d access
- `internal/testutil/verifier.go` — `PatchVerifierFacilitator()`: patches `x402-pricing` ConfigMap

Key patterns to follow:
- Use **free ports** (not hardcoded 8545/4040) to avoid conflicts
- The facilitator uses `anvil.Accounts[0].PrivateKey` as signer (not account #9)
- ClusterURL uses `host.docker.internal` (what k3d containers resolve), not `host.k3d.internal`
- Binary discovery: `X402_FACILITATOR_BIN` env → `~/Development/R&D/x402-rs/target/release/x402-facilitator`
- The flow scripts should mirror these patterns in shell

## Reference Codebases — ALWAYS check actual source code

When investigating behavior (heartbeat vs jobs, reconciliation logic, provider routing,
agent lifecycle), ALWAYS read the actual source code in these local repos. Never guess
or assume based on docs alone.

| Codebase | Local Path | Pinned Version | What to look up |
|----------|-----------|----------------|-----------------|
| **OpenClaw** | `/Users/bussyjd/Development/Obol_Workbench/openclaw` | `v2026.3.11` (`git checkout v2026.3.11`) | Heartbeat logic, job scheduling, model fallback, config parsing, gateway auth |
| **LiteLLM** | `/Users/bussyjd/Development/R&D/litellm` | (fork) | Model routing, provider config, master key auth |
| **x402-rs** | `/Users/bussyjd/Development/R&D/x402-rs` | (latest) | Facilitator binary, payment verification, settlement |
| **Frontend** | `/Users/bussyjd/Development/Obol_Workbench/obol-stack-front-end` | `v0.1.14` | UI components, API routes, ConfigMap reads |

**How to use**: Before debugging a flow failure related to agent behavior, `cd` into the
OpenClaw repo at the pinned tag and read the relevant source. For example:
- Heartbeat timing? → `openclaw/apps/openclaw/src/heartbeat/` or equivalent
- Model routing? → `openclaw/apps/openclaw/src/providers/` or config helpers
- Job vs heartbeat? → Look for task scheduling, cron, or interval logic in OpenClaw source

Do NOT modify these repos — they are read-only references. Only modify `obol-stack` code and flow scripts.

## Off Limits (do NOT modify)
- internal/embed/infrastructure/ (K8s templates — too risky)
- internal/x402/buyer/ (sidecar — separate domain)
- .workspace/ (runtime state)
- **Heartbeat interval / polling frequency**: The agent heartbeat runs every 5 minutes.
  Do NOT reduce this interval or try to make it faster. Local Ollama inference is slow
  and the heartbeat runs full reconciliation + tool calls. Faster polling will overload
  Ollama and cause cascading timeouts. The flow scripts must wait for the heartbeat
  (up to 8 minutes), not try to speed it up.

## Constraints
0. SKIP flow-05-network.sh entirely — do NOT deploy Ethereum clients (reth/lighthouse).
   They consume too much disk and network bandwidth. The user will add network coverage later.
1. STRICTLY FORBID: `go run`, direct `kubectl`, curl to pod IPs, `--force` flags
   a user wouldn't know, skipping propagation waits
2. All commands must use the built obol binary (`$OBOL_BIN_DIR/obol`)
3. All cluster HTTP access through `obol.stack:8080` or tunnel URL (not localhost)
   EXCEPT for documented port-forwards (LiteLLM §3c-3d, agent §5)
4. Must wait for real propagation (poll, don't sleep fixed durations)
5. `go build ./...` and `go test ./...` must pass after every change
6. NEVER run `obol stack down` or `obol stack purge`

## Branching Strategy
Each category of fix goes on its own branch off `main`. Create branches as needed:
- `fix/flow-scripts` — flow script improvements (wrong flags, missing steps, harness fixes)
- `fix/cli-ux` — CLI bugs, error messages, exit codes (Go code in `cmd/obol/`)
- `fix/timing` — readiness/polling/propagation fixes (Go code in `internal/`)
- `fix/docs` — documentation corrections (`docs/`)

Commit each fix individually with a descriptive message. Do NOT push — just commit locally.
Always create a NEW commit (never amend). The user will review branches on wakeup.

## Port-Forward vs Traefik Surfaces

| Surface | Access Method | Doc Reference |
|---------|--------------|---------------|
| LiteLLM direct | `obol kubectl port-forward -n llm svc/litellm 8001:4000` | getting-started §3c-3d |
| Agent inference | `obol kubectl port-forward -n openclaw-<id> svc/openclaw 18789:18789` | getting-started §5 |
| Frontend | `http://obol.stack:8080/` | getting-started §2 |
| eRPC | `http://obol.stack:8080/rpc` | monetize §1.6 |
| Monetized endpoints | `http://obol.stack:8080/services/<name>/*` | monetize §1.6 |
| Discovery | `<tunnel>/.well-known/*` | monetize §2.1 |

## Known Bugs in Current Flow Scripts (fix these first)
- `flow-10-anvil-facilitator.sh` uses `host.k3d.internal` but macOS needs `host.docker.internal`
  (see `internal/testutil/facilitator.go:34-39` — `clusterHostURL()` returns `host.docker.internal` on darwin)
- `flow-10` hardcodes ports 8545 and 4040 — should use free ports or at least check if already in use
- `flow-10` uses `FACILITATOR_PRIVATE_KEY` (Anvil account #9) but Go tests use `anvil.Accounts[0]`
  (derive with: `cast wallet private-key "test test ... junk" 0`)

## Initial State
- Cluster was wiped clean — no k3d cluster exists
- flow-02 will handle `obol stack init` + `obol stack up` automatically
- obol binary is pre-built at `.workspace/bin/obol`
- macOS DNS: use `$CURL_OBOL` (defined in lib.sh) for `obol.stack` URLs to bypass mDNS delays
- First run will be slow (~5 min for stack up) — subsequent iterations skip init/up

## What's Been Tried

### Session 2 (62 → 80/80, 26 experiments total)

**New doc coverage steps added:**
- flow-03: obol model status (§3), LiteLLM /v1/models endpoint (§3c)
- flow-04: obol openclaw skills list (§4), obol openclaw wallet list (§4 wallet), remote-signer health (§2 component table)
- flow-06: eRPC, Frontend, Reloader component checks (§1.1 / §2 component table)
- flow-07: x402-pricing active route check (§1.4/Pricing Config), tunnel logs (§1.5), ServiceOffer individual conditions (§1.4)
- flow-08: seller USDC balance increased after settlement (§2.4)
- flow-09: sell stop pricing route removal verification (§4 Pausing), sell list format check
- flow-02: obol network list (§6), obol network status, Prometheus readiness, frontend HTML content

**Root causes fixed in session 2:**
1. **Chokidar hot reload unreliable on k8s symlinks**: Pod starts with 30m default heartbeat because chokidar inotify doesn't detect ConfigMap symlink swap. Fixed by rollout restart after every ConfigMap patch → new pod starts WITH correct heartbeat at 5m.
2. **obol network add URL validation**: Invalid URLs (e.g. "not-a-url") were silently accepted. Added validateRPCEndpoint() to verify http/https/ws/wss scheme.
3. **obol sell http missing --upstream**: Empty upstream service name was silently accepted. Added explicit validation before kubectl apply.
4. **patchHeartbeatAfterSync missing in SyncAgentBaseURL**: tunnel/agent.go didn't call patchHeartbeatAfterSync (now it does + rollout restarts).

### Session 1 (baseline → 61/61)

**Baseline: 44/57** — 13 failures across all flows.

**Timing fixes (fix/timing → fix/flow-scripts):**
- `agent.Init()` / `ensureHeartbeatActive`: heartbeat was at 30m default (chart doesn't
  render `agents.defaults.heartbeat`). Added idempotent patch: reads ConfigMap, adds
  `every: 5m` if missing. OpenClaw hot-reloads — no pod restart needed.
- `patchHeartbeatConfig` (openclaw.go): removed incorrect pod restart (hot reload handles it).
- `SyncAgentBaseURL` (tunnel/agent.go): the root timing bug — every `obol sell http` call
  triggers `EnsureTunnelForSell` → `SyncAgentBaseURL` → helmfile sync, which renders the
  ConfigMap WITHOUT heartbeat. Added `patchHeartbeatAfterSync()` to re-patch heartbeat
  after each sync. Also added idempotency check (skip sync if URL unchanged).
- flow-06: added `kubectl rollout status` wait after `obol sell http` so the 480s heartbeat
  poll starts from a stable pod (not mid-restart).

**Flow script fixes (fix/flow-scripts):**
- flow-01: added eth_account + httpx prerequisite check
- flow-03: replaced `wget` with `python3 urllib` (not in litellm container), fixed
  health check to `/health/liveliness` (unauthenticated), added LITELLM_MASTER_KEY
  from secret, switched to `qwen3.5:9b` (only model in LiteLLM model_list)
- flow-06: added `kubectl rollout status` before poll
- flow-07: added x402 verifier pod readiness wait; fixed metrics check to iterate ALL
  pods (per-pod metrics, load-balanced by Traefik); moved BEFORE flow-10 (Reloader
  restarts x402-verifier on ConfigMap changes from flow-10)
- flow-08: replaced blockrun-llm (protocol mismatch — expects `"x402"` key not
  `"x402Version"`) with native EIP-712/ERC-3009 signing via eth_account. Changed
  discovery to `/skill.md` (always published, vs `/.well-known/` which requires on-chain
  ERC-8004 registration). Fixed `set -e` heredoc issue (`|| true`). Fixed balance
  check (`env -u CHAIN cast call` — CHAIN=base-sepolia conflicts with foundry uint64).
- flow-10: `host.k3d.internal` → `host.docker.internal` (matches testutil), correct
  facilitator signer (accounts[0]), binary discovery aligns with testutil order.
  Added verifier pod readiness wait after ConfigMap change.
- autoresearch.sh: reordered flow-07 before flow-10 to avoid Reloader pod restarts
  wiping metrics before flow-07 checks them.

**Root causes fixed:**
1. Heartbeat at 30m default instead of 5m (ConfigMap not rendered with heartbeat by chart)
2. `SyncAgentBaseURL` resetting heartbeat on every `obol sell` command
3. `wget` not in litellm container
4. LiteLLM requires Bearer token authentication
5. `qwen3:0.6b` not in LiteLLM model_list (only in Ollama)
6. blockrun-llm protocol mismatch with our x402 response format
7. `CHAIN=base-sepolia` env var conflicting with foundry cast uint64 parsing
8. `host.k3d.internal` not resolving on macOS (use `host.docker.internal`)
9. x402 verifier metrics empty (per-pod, must check all pods)
10. Kubernetes Reloader restarting verifier pods when x402-pricing ConfigMap changes
11. `/.well-known/agent-registration.json` requires ERC-8004 (use `/skill.md` instead)
12. `set -e` killing flow on Python heredoc failure
