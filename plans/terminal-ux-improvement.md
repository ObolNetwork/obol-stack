# Plan: Obol Stack CLI Terminal UX Improvement

## Context

The obol CLI (`cmd/obol`) and the bootstrap installer (`obolup.sh`) had inconsistent terminal output styles. obolup.sh had a clean visual language (colored `==>`, `✓`, `!`, `✗` prefixes, suppressed subprocess output), while the Go CLI used raw `fmt.Println` with no colors, no spinners, and direct subprocess passthrough that flooded the terminal with helmfile/k3d/kubectl output. Invalid commands produced poor error messages with no suggestions.

**Goal**: Unify the visual language across both tools, capture subprocess output behind spinners, and add `--verbose`/`--quiet` flags for different user needs.

**Decision**: User chose "Capture + spinner" for subprocess handling and Charmbracelet lipgloss as the styling library.

## What Was Built

### New Package: `internal/ui/` (7 files)

| File | Exports | Purpose |
|------|---------|---------|
| `ui.go` | `UI` struct, `New(verbose)`, `NewWithOptions(verbose, quiet)` | Core type with TTY detection, verbose/quiet flags |
| `output.go` | `Info`, `Success`, `Warn`, `Error`, `Print`, `Printf`, `Detail`, `Dim`, `Bold`, `Blank` | Colored message functions matching obolup.sh's `log_*` style. Quiet mode suppresses all except Error/Warn. |
| `exec.go` | `Exec(ExecConfig)`, `ExecOutput(ExecConfig)` | Subprocess capture: spinner by default, streams with `--verbose`, dumps captured output on error |
| `spinner.go` | `RunWithSpinner(msg, fn)` | Braille spinner (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) — minimal goroutine impl, no bubbletea |
| `prompt.go` | `Confirm`, `Select`, `Input`, `SecretInput` | Thin wrappers around `bufio.Reader` with lipgloss formatting |
| `errors.go` | `FormatError`, `FormatActionableError` | Structured error display with hints and next-step commands |
| `suggest.go` | `SuggestCommand`, `findSimilarCommands` | Levenshtein distance for "did you mean?" on unknown commands |

### Output Style (unified across both tools)

```
==> Starting cluster...        (blue, top-level header — no indent)
  ✓ Cluster created            (green, subordinate result — 2-space indent)
  ! DNS config skipped         (yellow, warning — 2-space indent)
✗ Helmfile sync failed         (red, error — no indent)
```

### Subprocess Capture Pattern

- **Default** (TTY, not verbose): Spinner + buffer. Success → `  ✓ msg (Xs)`. Failure → `✗ msg` + dump captured output.
- **`--verbose`**: Stream subprocess output live, each line prefixed with dim `  │ `.
- **Non-TTY** (pipe/CI): Plain text, no spinner, live stream.
- **Exception**: Passthrough commands (`obol kubectl`, `obol helm`, `obol k9s`, `obol openclaw cli`) keep direct stdin/stdout piping.

### Global Flags

| Flag | Env Var | Effect |
|------|---------|--------|
| `--verbose` | `OBOL_VERBOSE=1` | Stream subprocess output live with `│` prefix |
| `--quiet` / `-q` | `OBOL_QUIET=1` | Suppress all output except errors and warnings |

### CLI Improvements

- **Colored errors**: `log.Fatal(err)` replaced with `✗ error message` (red)
- **"Did you mean?"**: Levenshtein-based command suggestions on typos (`obol netwerk` → "Did you mean? obol network")
- **Interactive prompts**: `obol model setup` uses styled select menu + hidden API key input via `ui.SecretInput`

## Phased Rollout (as executed)

### Phase 1: Foundation
Created `internal/ui/` package (7 files), added lipgloss dependency, wired `--verbose` flag, `Before` hook, `CommandNotFound` handler, replaced `log.Fatal` with colored error output.

**Files created**: `internal/ui/*.go`
**Files modified**: `go.mod`, `cmd/obol/main.go`

### Phase 2: Stack Lifecycle (highest impact)
Migrated `stack init/up/down/purge` — the noisiest commands. Added `*ui.UI` to `Backend` interface. Converted ~8 subprocess passthrough sites to `u.Exec()`. `waitForAPIServer` and polling loops wrapped in spinners.

**Files modified**: `internal/stack/stack.go`, `internal/stack/backend.go`, `internal/stack/backend_k3d.go`, `internal/stack/backend_k3s.go`, `internal/stack/backend_test.go`, `internal/stack/stack_test.go`, `cmd/obol/bootstrap.go`, `cmd/obol/main.go`

### Phase 3: Network + OpenClaw + App + Agent
Migrated network install/sync/delete, openclaw onboard/sync/setup/delete/skills, app install/sync/delete, and agent init. Cascaded `*ui.UI` through all call chains. Converted confirmation prompts to `u.Confirm()`.

**Files modified**: `internal/network/network.go`, `internal/openclaw/openclaw.go`, `internal/openclaw/skills_injection_test.go`, `internal/app/app.go`, `internal/agent/agent.go`, `cmd/obol/network.go`, `cmd/obol/openclaw.go`, `cmd/obol/main.go`

### Phase 4: Update, Tunnel, Model
Migrated remaining internal packages. `update.ApplyUpgrades` helmfile sync captured. All tunnel operations use `u.Exec()` (except interactive `cloudflared login` and `logs -f`). `model.ConfigureLLMSpy` status messages styled.

**Files modified**: `internal/update/update.go`, `internal/tunnel/tunnel.go`, `internal/tunnel/login.go`, `internal/tunnel/provision.go`, `internal/model/model.go`, `cmd/obol/update.go`, `cmd/obol/model.go`, `cmd/obol/main.go`

### Phase 5: Polish
Added `--quiet` / `-q` global flag with `OBOL_QUIET` env var. Quiet mode suppresses all output except errors/warnings. Migrated `obol model setup` interactive prompt to use `ui.Select()` + `ui.SecretInput()`. Fixed `cmd/obol/update.go` to use `getUI(c)` instead of `ui.New(false)`.

**Files modified**: `internal/ui/ui.go`, `internal/ui/output.go`, `cmd/obol/main.go`, `cmd/obol/update.go`, `cmd/obol/model.go`

### Phase 6: obolup.sh Alignment
Aligned the bash installer's output to match the Go CLI's visual hierarchy:
- `log_success`/`log_warn` gained 2-space indent (subordinate to `log_info`)
- Banner replaced from Unicode box (`╔═══╗`) to ASCII art logo (matches `obol --help`)
- Added `log_dim()` function and `DIM`/`BOLD` ANSI codes
- Instruction blocks indented consistently (2-space for text, 4-space for commands)

**Files modified**: `obolup.sh`

## Dependencies Added

```
github.com/charmbracelet/lipgloss  — styles, colors, NO_COLOR support, TTY degradation
```

Transitive: `muesli/termenv`, `lucasb-eyer/go-colorful`, `mattn/go-runewidth`, `rivo/uniseg`, `xo/terminfo`. `mattn/go-isatty` was already an indirect dep.

## Files Inventory

**New files (7)**:
- `internal/ui/ui.go`
- `internal/ui/output.go`
- `internal/ui/exec.go`
- `internal/ui/spinner.go`
- `internal/ui/prompt.go`
- `internal/ui/errors.go`
- `internal/ui/suggest.go`

**Modified Go files (~25)**:
- `go.mod`, `go.sum`
- `cmd/obol/main.go`, `cmd/obol/bootstrap.go`, `cmd/obol/network.go`, `cmd/obol/openclaw.go`, `cmd/obol/model.go`, `cmd/obol/update.go`
- `internal/stack/stack.go`, `internal/stack/backend.go`, `internal/stack/backend_k3d.go`, `internal/stack/backend_k3s.go`
- `internal/network/network.go`
- `internal/openclaw/openclaw.go`
- `internal/app/app.go`
- `internal/agent/agent.go`
- `internal/update/update.go`
- `internal/tunnel/tunnel.go`, `internal/tunnel/login.go`, `internal/tunnel/provision.go`
- `internal/model/model.go`
- `internal/stack/backend_test.go`, `internal/stack/stack_test.go`, `internal/openclaw/skills_injection_test.go`

**Modified shell (1)**:
- `obolup.sh`

## Verification

1. `go build ./...` — compiles clean
2. `go vet ./...` — no issues
3. `go test ./...` — all 7 test packages pass
4. `bash -n obolup.sh` — syntax valid
5. `obol netwerk` — shows "Did you mean? obol network"
6. `obol --quiet network list` — suppresses output
7. `obol network list` — shows colored output with bold headers
8. `obol app install` — shows colored `✗` error with examples
