# CLI Agent-Readiness Optimizations

## Status

**Implemented (this branch)**:
- Phase 1: Global `--output json` / `-o json` / `OBOL_OUTPUT=json` flag
- Phase 1: `OutputMode` + `IsJSON()` + `JSON()` on `internal/ui/UI`
- Phase 1: 11 commands refactored with typed JSON results (sell list/status/info, network list, model status/list, version, update, openclaw list, tunnel status)
- Phase 1: Human output redirected to stderr in JSON mode (stdout is clean JSON)
- Phase 2: `internal/validate/` package (Name, Namespace, WalletAddress, ChainName, Price, URL, Path, NoControlChars)
- Phase 2: Headless prompt paths — `Confirm`, `Select`, `Input`, `SecretInput` auto-resolve defaults in non-TTY/JSON mode
- Phase 2: `sell delete` migrated from raw `fmt.Scanln` to `u.Confirm()`
- Phase 6: `CONTEXT.md` — agent-facing context document

- Phase 1D: `--from-json` on sell http, sell pricing, network add (`cmd/obol/input.go` helper)
- Phase 2B: `validate.Name()` wired into sell inference/http/stop/delete, `validate.URL()` in network add
- Phase 2C: model.go `promptModelPull()` migrated from bufio to `u.Select()`/`u.Input()`, openclaw onboard headless via `u.IsTTY() && !u.IsJSON()`

**Deferred to follow-up**:
- Phase 3: `obol describe` schema introspection
- Phase 4: `--fields` field filtering
- Phase 5: `--dry-run` for mutating commands
- Phase 7: MCP surface (`obol mcp`)

## Context

The obol CLI is increasingly consumed by AI agents — Claude Code during development, OpenClaw agents in-cluster, and soon MCP clients. Today the CLI is human-optimized: colored output, spinners, interactive prompts, and hand-formatted tables. Agents need structured output, non-interactive paths, input hardening, and runtime introspection. This plan makes the CLI agent-ready while preserving human DX.

**Strengths**: `internal/ui/` abstraction with TTY detection, `OutputMode` (human/json), `--verbose`/`--quiet`/`--output` global flags, `internal/schemas/` with JSON-tagged Go types, `internal/validate/` for input validation, `--force` pattern for non-interactive destructive ops, 23 SKILL.md files shipped in `internal/embed/skills/`, `CONTEXT.md` for agent consumption.

**Remaining gaps**: `--from-json` for structured input, some `fmt.Printf` calls still bypass UI layer, `model.go` interactive prompts not fully migrated, `openclaw onboard` still hardwired `Interactive: true`, no schema introspection, no `--dry-run`, no field filtering, no MCP surface.

---

## Phase 1: Global `--output json` + Raw JSON Input

Structured output is table stakes. Raw JSON input (`--from-json`) is first-class — agents shouldn't have to translate nested structures into 15+ flags.

### 1A. Extend UI struct with output mode

**`internal/ui/ui.go`** — Add `OutputMode` type (`human`|`json`) and field to `UI` struct. Add `NewWithAllOptions(verbose, quiet bool, output OutputMode)`. Add `IsJSON() bool`.

**`internal/ui/output.go`** — Add `JSON(v any) error` method that writes to stdout via `json.NewEncoder`. When `IsJSON()` is true, redirect `Info`/`Success`/`Detail`/`Print`/`Printf`/`Dim`/`Bold`/`Blank` to stderr (so agents get clean JSON on stdout, diagnostics on stderr). Suppress spinners in JSON mode.

### 1B. Add global `--output` flag

**`cmd/obol/main.go`** (lines 110-127) — Add `--output` / `-o` flag (`human`|`json`, env `OBOL_OUTPUT`, default `human`). Wire in `Before` hook to pass to `ui.NewWithAllOptions`.

### 1C. Refactor commands to return typed results

Don't just bolt JSON onto existing `fmt.Printf` calls. Refactor high-value commands to return typed data first, then format for human or JSON. This pays off twice: clean JSON output now, and reusable typed results for MCP later.

**Audit note**: Raw `fmt.Printf` output is spread across `main.go:460` (version), `model.go:286` (tables), `network.go:188` (tables), and throughout `sell.go`. Each needs a return-data-then-format refactor.

| Command | Strategy | Effort |
|---------|----------|--------|
| `sell list` | Switch kubectl arg from `-o wide` to `-o json` | Trivial |
| `sell status <name>` | Switch kubectl arg from `-o yaml` to `-o json` | Trivial |
| `sell status` (global) | Marshal `PricingConfig` + `store.List()` — currently raw `fmt.Printf` at `sell.go:463-498` | Medium |
| `sell info` | Already has `--json` (`sell.go:841`) — wire to global flag, deprecate local | Trivial |
| `network list` | `ListRPCNetworks()` returns `[]RPCNetworkInfo` — marshal it, but local node output also uses `fmt.Printf` at `network.go:188` | Medium |
| `model status` | Return provider status map as JSON — currently `fmt.Printf` tables at `model.go:286` | Medium |
| `model list` | `ListOllamaModels()` returns structured data | Low |
| `version` | `BuildInfo()` returns a string today — refactor to struct with fields (version, commit, date, go version) | Medium |
| `update` | Already has `--json` (`update.go:20`); wire to global flag, deprecate local | Trivial |
| `openclaw list` | Refactor to return data before formatting | Medium |
| `tunnel status` | Refactor to return data before formatting | Medium |

### 1D. Raw JSON input (`--from-json`)

Add `--from-json` flag to all commands that create resources. Accepts file path or `-` for stdin. Unmarshals into existing `internal/schemas/` types, validates, creates manifest. This is first-class, not an afterthought.

| Command | Schema Type | Flags Bypassed |
|---------|-------------|----------------|
| `sell http` | `schemas.ServiceOfferSpec` | 15+ flags (wallet, chain, price, upstream, port, namespace, health-path, etc.) |
| `sell inference` | `schemas.ServiceOfferSpec` | 10+ flags |
| `sell pricing` | `schemas.PaymentTerms` | wallet, chain, facilitator |
| `network add` | New `RPCConfig` type | endpoint, chain-id, allow-writes |

### Testing
- `internal/ui/ui_test.go`: OutputMode switching, JSON writes valid JSON to stdout, human methods go to stderr in JSON mode
- `cmd/obol/output_test.go`: `--output json` on each migrated command produces parseable JSON
- `cmd/obol/json_input_test.go`: `--from-json` with valid/invalid specs

---

## Phase 2: Input Validation + Headless Paths

Agents hallucinate inputs and can't answer interactive prompts. Fix both together.

### 2A. New validation package

**`internal/validate/validate.go`** (new)

```
Name(s)           — k8s-safe: [a-z0-9][a-z0-9.-]*, no path traversal
Namespace(s)      — same rules as Name
WalletAddress(s)  — reuse x402verifier.ValidateWallet() pattern
ChainName(s)      — from known set (base, base-sepolia, etc.)
Path(s)           — no .., no %2e%2e, no control chars
Price(s)          — valid decimal, positive
URL(s)            — parseable, no control chars
NoControlChars(s) — reject \x00-\x1f except \n\t
```

### 2B. Wire into commands

Add validation at the top of every action handler for positional args and key flags:
- **`cmd/obol/sell.go`**: name, wallet, chain, path, price, namespace, upstream URL
- **`cmd/obol/network.go`**: network name, custom RPC URL, chain ID
- **`cmd/obol/model.go`**: provider name, endpoint URL
- **`cmd/obol/openclaw.go`**: instance ID

### 2C. Headless paths for interactive flows

**`internal/ui/prompt.go`** — When `IsJSON() || !IsTTY()`:
- `Confirm` → return default value (no stdin read)
- `Select` → return error: "interactive selection unavailable; use --provider flag"
- `Input` → return default or error if no default
- `SecretInput` → return error: "use --api-key flag"

**`cmd/obol/openclaw.go`** (line 36) — `openclaw onboard` is hardwired `Interactive: true`. Add a non-interactive path when all required flags are provided (`--id`, plus any other required inputs). Only fall through to interactive mode when flags are missing AND stdin is a TTY.

**`cmd/obol/model.go`** (lines 62-84) — `model setup` enters interactive selection when `--provider` is omitted. In non-TTY/JSON mode, error with required flags instead.

**`cmd/obol/model.go`** (lines 387-419) — `model pull` uses `bufio.NewReader(os.Stdin)` for interactive model selection. Same treatment.

**`cmd/obol/sell.go`** (line 576-588) — `sell delete` confirmation uses raw `fmt.Scanln`. Migrate to `u.Confirm()` so the headless path is automatic.

### Testing
- `internal/validate/validate_test.go`: Table-driven tests for path traversal variants, control char injection, valid inputs
- Test that `--output json` + missing required flags → clear error (not a hung prompt)
- Test that `openclaw onboard --id test -o json` works without interactive mode

---

## Phase 3: Schema Introspection (`obol describe`)

Let agents discover what the CLI accepts at runtime without parsing `--help` text.

### 3A. Add `obol describe` command

**`cmd/obol/describe.go`** (new)

```
obol describe                    # list all commands + flags as JSON
obol describe sell http          # flags + ServiceOffer schema for that command
obol describe --schemas          # dump resource schemas only
```

Walk urfave/cli's `*cli.Command` tree. For each command, emit: name, usage, flags (name, type, required, default, env vars, aliases), ArgsUsage. Output always JSON.

### 3B. Schema registry

**`internal/schemas/registry.go`** (new) — Map of schema names to JSON Schema generated from Go struct tags via `reflect`. Schemas: `ServiceOfferSpec`, `PaymentTerms`, `PriceTable`, `RegistrationSpec`.

### 3C. Command metadata annotations

Add `Metadata: map[string]any{"schema": "ServiceOfferSpec", "mutating": true}` to commands that create resources (sell http, sell inference, sell pricing). `obol describe` reads this and includes the schema in output.

### Testing
- `cmd/obol/describe_test.go`: Valid JSON output, every command appears, schemas resolve, flag metadata matches actual flags

---

## Phase 4: `--fields` Support

Let agents limit response size to protect their context window.

### 4A. Field mask implementation

**`internal/ui/fields.go`** (new) — `FilterFields(data any, fields []string) any` using reflect on JSON tags.

### 4B. Global `--fields` flag

**`cmd/obol/main.go`** — Global `--fields` flag (comma-separated, requires `--output json`). Applied in `u.JSON()` before encoding.

### Testing
- `--fields name,status` on `sell list -o json` returns only those fields
- `--fields` without `--output json` returns error

---

## Phase 5: `--dry-run` for Mutating Commands

Let agents validate before mutating. Safety rail.

### 5A. Global `--dry-run` flag

**`cmd/obol/main.go`** — Add `--dry-run` bool flag.

### 5B. Priority commands

| Command | Implementation |
|---------|---------------|
| `sell http` | Already builds manifest before `kubectlApply()` — return manifest instead of applying |
| `sell pricing` | Validate wallet/chain, show what would be written to ConfigMap |
| `network add` | Validate chain, show which RPCs would be added to eRPC config |
| `sell delete` | Validate name exists, show what would be deleted |

Pattern: after validation, before execution, check `cmd.Root().Bool("dry-run")` and return a `DryRunResult{Command, Valid, WouldCreate, Manifest}` as JSON.

### Testing
- `cmd/obol/dryrun_test.go`: `--dry-run sell http` returns manifest without kubectl apply, validation still runs in dry-run

---

## Phase 6: Agent Context & Skills

The 23 SKILL.md files are a strength, but there's no top-level `CONTEXT.md` encoding invariants agents can't intuit from `--help`.

### 6A. Ship `CONTEXT.md`

**`CONTEXT.md`** (repo root, also embedded in binary) — Agent-facing context file encoding:
- Always use `--output json` when parsing output programmatically
- Always use `--force` for non-interactive destructive operations
- Always use `--fields` on list commands to limit context window usage
- Always use `--dry-run` before mutating operations
- Use `obol describe <command>` to introspect flags and schemas
- Cluster commands require `OBOL_CONFIG_DIR` or a running stack (`obol stack up`)
- Payment wallet addresses must be 0x-prefixed, 42 chars
- Chain names: `base`, `base-sepolia` (not CAIP-2 format)

### 6B. Update existing skills

Review and update the 23 SKILL.md files to reference the new agent-friendly flags where relevant (e.g., the `sell` skill should mention `--from-json` and `--dry-run`).

---

## Phase 7: MCP Surface (`obol mcp`)

Expose the CLI as typed JSON-RPC tools over stdio. Depends on all previous phases.

### 7A. New package `internal/mcp/`

- `server.go` — MCP server over stdio using `github.com/mark3labs/mcp-go`
- `tools.go` — Tool definitions from the typed result functions built in Phase 1C (not by shelling out with `--output json`)
- `handlers.go` — Tool handlers that call the refactored return-typed-data functions directly

### 7B. `obol mcp` command

**`cmd/obol/mcp.go`** (new) — Starts MCP server. Exposes high-value tools only:
- sell: `sell_http`, `sell_list`, `sell_status`, `sell_pricing`, `sell_delete`
- network: `network_list`, `network_add`, `network_remove`, `network_status`
- model: `model_status`, `model_list`, `model_setup`
- openclaw: `openclaw_list`, `openclaw_onboard`
- utility: `version`, `update`, `tunnel_status`

Excludes: kubectl/helm/k9s passthroughs, interactive-only commands, dangerous ops (stack purge/down).

### Testing
- `internal/mcp/mcp_test.go`: Tool registration produces valid MCP definitions, stdin/stdout JSON-RPC round-trip

---

## Key Files Summary

| File | Changes |
|------|---------|
| `internal/ui/ui.go` | Add OutputMode, IsJSON(), NewWithAllOptions() |
| `internal/ui/output.go` | Add JSON() method, stderr redirect in JSON mode |
| `internal/ui/prompt.go` | Non-interactive behavior when JSON/non-TTY |
| `internal/ui/fields.go` | New — field mask filtering |
| `cmd/obol/main.go` | `--output`, `--dry-run`, `--fields` global flags + Before hook |
| `cmd/obol/sell.go` | JSON output, typed results, input validation, dry-run, --from-json, migrate Scanln to u.Confirm |
| `cmd/obol/network.go` | JSON output, typed results, input validation |
| `cmd/obol/model.go` | JSON output, typed results, input validation, headless paths |
| `cmd/obol/openclaw.go` | JSON output, typed results, input validation, headless onboard path |
| `cmd/obol/update.go` | Wire to global --output flag, deprecate local --json |
| `cmd/obol/describe.go` | New — schema introspection command |
| `cmd/obol/mcp.go` | New — `obol mcp` command |
| `internal/validate/validate.go` | New — input validation functions |
| `internal/schemas/registry.go` | New — JSON Schema generation from Go types |
| `internal/mcp/` | New package — MCP server, tools, handlers |
| `CONTEXT.md` | New — agent-facing context file |

## Verification

```bash
# Phase 1: JSON output + JSON input
obol sell list -o json | jq .
obol sell status -o json | jq .
obol version -o json | jq .
obol network list -o json | jq .
echo '{"upstream":{"service":"ollama","namespace":"llm","port":11434},...}' | obol sell http test --from-json -

# Phase 2: Input validation + headless
obol sell http '../etc/passwd' --wallet 0x... --chain base-sepolia  # should error
obol sell http 'valid-name' --wallet 'not-a-wallet' --chain base-sepolia  # should error
echo '' | obol model setup -o json  # should error with "use --provider flag", not hang

# Phase 3: Schema introspection
obol describe | jq '.commands | length'
obol describe sell http | jq '.schema'

# Phase 4: Fields
obol sell list -o json --fields name,namespace,status | jq .

# Phase 5: Dry-run
obol sell http test-svc --wallet 0x... --chain base-sepolia --dry-run -o json | jq .

# Phase 7: MCP
echo '{"jsonrpc":"2.0","method":"tools/list","id":1}' | obol mcp

# Unit tests
go test ./internal/ui/ ./internal/validate/ ./internal/schemas/ ./internal/mcp/ ./cmd/obol/
```
