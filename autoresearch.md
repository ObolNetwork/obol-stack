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

## Off Limits (do NOT modify)
- internal/embed/infrastructure/ (K8s templates — too risky)
- internal/x402/buyer/ (sidecar — separate domain)
- .workspace/ (runtime state)

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

## Initial State
- Cluster was wiped clean — no k3d cluster exists
- flow-02 will handle `obol stack init` + `obol stack up` automatically
- obol binary is pre-built at `.workspace/bin/obol`
- macOS DNS: use `$CURL_OBOL` (defined in lib.sh) for `obol.stack` URLs to bypass mDNS delays
- First run will be slow (~5 min for stack up) — subsequent iterations skip init/up

## What's Been Tried
(Agent updates this section as experiments accumulate)
