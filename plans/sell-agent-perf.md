# `obol sell` agent perf — Implementation Plan

**Status**: In progress

## Problem

Sell-agent responses (notably `obol sell demo quant`) routinely struggle:
high latency, long TTFT, and outright failure when total response time
exceeds the Cloudflare free-tunnel **100s hard timeout**. The agent is
otherwise uncapped — there is no `max_tokens` ceiling, `agent.max_turns`
defaults to 90, and `agent.reasoning_effort` defaults to medium — so a
chatty Hermes can spend the whole tunnel budget before producing an answer.

## Constraints / decisions

- LiteLLM is shared between the master agent and CRD-rendered sub-agents.
  Capping output tokens or reasoning at the model level is off the table —
  it would degrade the master too.
- Every CRD-rendered Agent is a sub-agent-for-sale (master is deployed via
  `obol agent init`, not through `ServiceOffer`). So all "sub-agent only"
  knobs can be gated purely on the controller render path — no extra flag.
- We are not modifying the Hermes container. Anything we change must be
  config Hermes already understands, or filesystem state in the agent's
  profile dir that Hermes already respects.
- Free Cloudflare tunnel stays. Enterprise tunnel is a separate, later
  conversation.
- Per-sale success/fail metric is **punted**. The existing verifier
  counters (`obol_x402_verifier_payment_{verified,failed}_total`) are the
  source of truth for now. Hermes has no Prometheus endpoint, only JSON
  `/usage` + `/insights` — not worth a sidecar today.

## Scope (this branch)

### 1. Tighten the sub-agent Hermes config — `internal/serviceoffercontroller/agent_render.go`

`renderHermesConfig()` currently emits only `model`, `terminal`, `skills`.
Extend to also emit:

```yaml
terminal:
  lifetime_seconds: 90        # was 300; live under the 100s tunnel cap
agent:
  max_turns: 30               # was 90 default
  reasoning_effort: low       # was "" (medium default)
  disabled_toolsets:
    - memory                  # no cross-session persistence in sub-agents
    - web                     # no web_search / web_extract
```

The master agent uses `internal/hermes/hermes.go` and is **not** touched.
Rendering happens once per ServiceOffer reconcile, so the new fields land
in the per-agent ConfigMap.

### 2. Trim SOUL.md and add a be-terse directive — `internal/agentruntime/soul.go`

Current `SoulTemplate` is ~1050 tokens. Cut the boilerplate sections (the
"how to handle requests", "confidentiality", and parts of "adversarial
inputs" can be tightened) and add a clear "be terse, no preamble, prefer
one short paragraph" instruction near the top. Target: ~50% length.

The objective interpolation contract (`{{ .OperatorObjective }}`) stays
unchanged. SOUL.md write-once semantics also stay unchanged.

### 3. Bundled-skills off + addresses skill split + pycache exclude

**3a.** `internal/agentcrd/agent.go` — `SeedHostFiles` writes a
`.no-bundled-skills` marker file into `HostHomePath` (the Hermes profile
dir). The marker is honored by Hermes' installer, `hermes update`, and
all skill syncs — it stops bundled-skill seeding without deleting
anything already on disk. CRD-rendered agents are always sub-agents, so
this is unconditional.

Idempotent: if the marker already exists, leave it.

**3b.** `internal/embed/skills/addresses/` — split the heavy SKILL.md
(~29KB, ~7k tokens) into a thin index + `references/` files. SKILL.md
keeps the frontmatter, the critical "never hallucinate" preamble, and a
table of contents pointing at `references/`. Address tables move into
files like `references/stablecoins.md`, `references/liquid-staking.md`,
`references/defi.md`, `references/l2-bridges.md`, etc. Hermes loads
SKILL.md eagerly and `references/` on demand, so the agent's prompt
shrinks dramatically while functionality is preserved.

**3c.** `internal/embed/embed.go` — `WriteSkillSubset` skips
`__pycache__/` directories and `*.pyc` files. Also add
`internal/embed/skills/.gitignore` to keep devs from accidentally
committing them after running scripts locally.

### 4. (Follow-up commit, same branch) RPC tool diet — `internal/embed/skills/ethereum-networks/scripts/rpc.py`

Vanilla-Python filter pass for `eth_getLogs` (and projection-only for
receipts/blocks). Four new opt-in flags:

- `--fields a,b,c` — keep only listed top-level fields per entry.
- `--where key=value,key=value` — exact-match AND filter. Supports
  top-level fields and `topics[N]` indices.
- `--limit N` / `--tail N` — bound result size.
- `--count` — return `{"count": N, "fromBlock": ..., "toBlock": ...}`
  instead of the array.

All flags opt-in; existing callers untouched. ~50 LOC, stdlib only.

## Out of scope (intentional)

- **Per-sale outcome metric.** Existing verifier counters first.
- **Streaming response.** Real win (tunnel measures inactivity, not
  total time) but needs an end-to-end SSE check across LiteLLM → Hermes
  → Traefik → cloudflared. Follow-up PR.
- **Enterprise tunnel.** Out of scope; "make free tunnel work".
- **Hermes `/usage` prom scraping sidecar.** Hermes upstream issues
  #6642 / #6741 are tracking native prom; revisit when they ship.

## Numbers (current best guesses, tune later)

| Knob | Old | New |
|------|-----|-----|
| `terminal.lifetime_seconds` | 300 | **90** |
| `terminal.timeout` | 180 | **80** (measured; see Post-review hardening #3) |
| `agent.max_turns` | 90 (default) | **30** |
| `agent.reasoning_effort` | medium (default) | **low** |
| `agent.disabled_toolsets` | — | `[memory, web]` |
| SOUL.md template | 2128 bytes | **1460 bytes** (measured; rendered ~1480 bytes, ~370 tok) |
| `addresses` SKILL.md | ~7k tok | **~1k tok** (rest in `references/`) |

The earlier "~1050 -> ~500 tok" SOUL estimate was optimistic. The
**measured** result is a template shrink from 2128 -> 1460 bytes; rendered
SOUL.md (after objective interpolation) is ~1480 bytes, ~370 tokens at
4 chars/token — a real reduction, just not as large as guessed.

The addresses skill split preserved **all 177 unique contract addresses
exactly** across 8 `references/` files (verified by diff against the
pre-split SKILL.md). No address was dropped, renamed, or mistyped in the
move — only the prompt-load footprint changed.

## Files touched

- `internal/serviceoffercontroller/agent_render.go` — extend `renderHermesConfig`
- `internal/serviceoffercontroller/agent_render_test.go` — assert new fields
- `internal/agentruntime/soul.go` — trim template
- `internal/agentruntime/soul_test.go` — adjust expected length
- `internal/agentcrd/agent.go` — write `.no-bundled-skills` marker
- `internal/agentcrd/agent_test.go` — assert marker exists post-seed
- `internal/embed/embed.go` — pycache skip in `WriteSkillSubset`
- `internal/embed/skills/.gitignore` — new
- `internal/embed/skills/addresses/SKILL.md` — slim down
- `internal/embed/skills/addresses/references/*.md` — new, by category
- `internal/embed/skills/ethereum-networks/scripts/rpc.py` — filter flags (separate commit)

## Verification

- Unit tests in each touched package.
- `obol stack up && obol sell demo quant ...` then issue a paid call and
  confirm the rendered ConfigMap has the new fields:
  `kubectl get cm hermes-config -n agent-quant -o yaml`
- Confirm marker file present in the agent PVC:
  `ls -la <DataDir>/agent-quant/hermes-data/.hermes/.no-bundled-skills`
- Eyeball Hermes pod logs for "skipping bundled skills" on startup.

## Post-review hardening (PR #582 follow-up)

Three follow-ups landed on this branch after review. None reopen the
decisions above; they tighten the contract and add coverage.

- **#2 — `SeedHostFiles` marker contract + regression test.**
  `SeedHostFiles` now carries an explicit "sub-agents only" doc contract:
  the `.no-bundled-skills` marker is written **only** by `SeedHostFiles`,
  never by the reusable seed primitives (`WriteSoul`,
  `embed.WriteSkillSubset`) that a master or objective-only path could
  route through. A regression test locks this in, so a future refactor that
  pushes the marker write down into a shared primitive — the way the master
  (which seeds via its own `internal/hermes` path) could accidentally
  inherit it — fails first.
- **#3 — `terminal.timeout` lowered 180 -> 80.** It now stays
  `<= terminal.lifetime_seconds` (90), so a single operation can no longer
  outlive the session. (Previously a 180s per-op timeout could exceed the
  90s session lifetime.) Reflected in the Numbers table above.
- **#1 — integration test for marker + config honoring.** A new
  `//go:build integration` test deploys a sub-agent and verifies the Hermes
  image actually honors the new state: the `.no-bundled-skills` marker on
  the PVC, the new `agent.*` / `lifetime_seconds` keys in the rendered
  ConfigMap, and a behavioral skip-bundled-skills signal from the running
  pod. It skips gracefully without a cluster (consistent with the rest of
  the integration suite).
