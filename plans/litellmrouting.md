# LiteLLM + OpenClaw Smart Routing

## Context

When `obol model setup anthropic` adds a cloud provider, OpenClaw can't use the new models because:
1. LiteLLM requires every model to be individually registered in `model_list`
2. OpenClaw's per-agent `models.json` persists stale config (old URLs, old model lists)
3. OpenClaw requires an explicit model allowlist — it does NOT auto-discover from `/v1/models`
4. The sync between LiteLLM config and OpenClaw config is fragile and multi-step

**Goal**: `obol model setup anthropic` → any Claude model immediately works in OpenClaw. Same for OpenAI. Ollama models work as soon as they're pulled. Direct-to-provider wiring preserved.

## Approach: Wildcards for Cloud + Explicit for Ollama + Host-Side Patching

### Why This Approach

| Feature | LiteLLM | OpenClaw |
|---------|---------|----------|
| `anthropic/*` wildcard | Works | N/A (LiteLLM-side) |
| `openai/*` wildcard | Works | N/A |
| `ollama_chat/*` wildcard | **Broken** | N/A |
| File watcher hot-reload | N/A | **Yes** — hot-applies model changes |

**Key insight**: LiteLLM wildcards handle cloud routing, but OpenClaw needs an explicit model allowlist. We solve this with: (a) wildcards in LiteLLM so any model routes, and (b) writing a clean `models.json` to OpenClaw's host-side PVC which its file watcher picks up.

### End-to-End Flows

**`obol model setup anthropic --api-key sk-ant-...`**:
1. LiteLLM gets `anthropic/*` wildcard + API key in Secret → restarts
2. `syncOpenClawModels()` queries running LiteLLM `/v1/models` for actual available models (falls back to baked-in well-known list if cluster unreachable)
3. Writes clean `models.json` to host PVC (replaces entire file)
4. OpenClaw file watcher hot-reloads — Claude models immediately available, no pod restart

**`obol model setup ollama`** (new models detected):
1. Explicit `ollama_chat/<model>` entries added to LiteLLM (no wildcards)
2. `syncOpenClawModels()` queries LiteLLM, updates `models.json`
3. OpenClaw hot-reloads

**Direct-to-provider** (`obol openclaw setup` → choose Anthropic direct):
- Unchanged — `buildDirectProviderOverlay()` is a separate code path, no LiteLLM involved

## Changes

### 1. LiteLLM: Wildcard entries for cloud providers

**File**: `internal/model/model.go` — `buildModelEntries()`

```
anthropic → wildcard: model_name: "anthropic/*", model: "anthropic/*"
            + explicit entries for requested models (better /v1/models)
openai    → wildcard: model_name: "openai/*", model: "openai/*"
            + explicit entries for requested models
ollama    → unchanged (explicit ollama_chat/<model> entries)
```

### 2. LiteLLM: Enable `drop_params: true`

**File**: `internal/embed/infrastructure/base/templates/llm.yaml` (line 71)

Cross-provider compatibility — LiteLLM drops unsupported params instead of erroring when routing across providers.

### 3. Model list: Live query + baked-in fallback

**File**: `internal/model/model.go` — `GetConfiguredModels()`

When syncing to OpenClaw:
1. **Try**: Query running LiteLLM pod's `/v1/models` endpoint (with `check_provider_endpoint: true` so wildcards expand to real models)
2. **Fallback**: Expand wildcards using baked-in `wellKnownModels` map if cluster unreachable

```go
var wellKnownModels = map[string][]string{
    "anthropic": {"claude-sonnet-4-6", "claude-opus-4", "claude-sonnet-4-5-20250929", "claude-haiku-3-5-20241022"},
    "openai":    {"gpt-4o", "gpt-4o-mini", "o3", "o3-mini"},
}
```

### 4. Host-side `models.json` patching (clean replacement)

**File**: `internal/openclaw/openclaw.go` — new `patchAgentModelsJSON()`

Writes a **clean** `models.json` to `$DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/agents/main/agent/models.json`. Replaces entire file — no backward-compatible merge needed (the stale llmspy config never shipped). Contains only the `openai` provider pointing at LiteLLM with the current model list.

### 5. Update `SyncOverlayModels()` — file watcher only, no helmfile re-sync

**File**: `internal/openclaw/openclaw.go`

After patching the overlay YAML, also call `patchAgentModelsJSON()` for each instance. **Skip helmfile re-sync** — OpenClaw's file watcher handles `models.json` changes in <1s. Only do helmfile sync when overlay YAML changes that affect the Helm release (e.g. new provider added, not just model list updates).

### 6. Add `obol model sync` CLI command

**File**: `cmd/obol/model.go`

Manual escape hatch: re-reads LiteLLM config (live query) and pushes to all OpenClaw instances. Useful when new models appear after binary was built.

### 7. Update `detectProvider()` for wildcards

**File**: `internal/model/model.go`

Handle wildcard model names (`anthropic/*`, `openai/*`) in provider detection logic.

### 8. Tests

- `model_test.go`: wildcard entry generation, wildcard expansion, provider detection for wildcards
- `overlay_test.go`: `models.json` clean write, end-to-end sync

## Files to Modify

| File | Changes |
|------|---------|
| `internal/model/model.go` | `buildModelEntries()` wildcards, `GetConfiguredModels()` live query + fallback, `detectProvider()` wildcards, `wellKnownModels` map |
| `internal/openclaw/openclaw.go` | New `patchAgentModelsJSON()`, update `SyncOverlayModels()` to patch models.json + skip helmfile sync |
| `internal/embed/infrastructure/base/templates/llm.yaml` | `drop_params: true` |
| `cmd/obol/model.go` | New `model sync` subcommand |
| `internal/model/model_test.go` | Tests for wildcards |
| `internal/openclaw/overlay_test.go` | Tests for models.json patching |

## Verification

1. `go build ./...` + `go test ./...`
2. `obol model setup anthropic --api-key sk-ant-...` → LiteLLM has `anthropic/*` → OpenClaw `models.json` has Claude models → inference works
3. `obol model setup ollama` → new models appear in OpenClaw
4. `obol model sync` → refreshes all instances from live LiteLLM
5. `obol openclaw setup` → direct Anthropic → still works (no LiteLLM)
