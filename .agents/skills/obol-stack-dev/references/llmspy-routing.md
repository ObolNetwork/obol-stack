# LLM Smart-Routing Through llmspy

## Architecture: 2-Tier Model Routing

### Tier 1: Global llmspy Gateway (`llm` namespace)

A cluster-wide OpenAI-compatible proxy that routes LLM traffic to actual providers.

**Kubernetes Resources**:

| Resource | Type | Purpose |
|----------|------|---------|
| `llm` | Namespace | Dedicated namespace for LLM infrastructure |
| `llmspy-config` | ConfigMap | `llms.json` (enable/disable) + `providers.json` (definitions) |
| `llms-secrets` | Secret | Cloud API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) |
| `llmspy` | Deployment | `ghcr.io/obolnetwork/llms`, port 8000 |
| `llmspy` | Service | `llmspy.llm.svc.cluster.local:8000` |
| `ollama` | Service (ExternalName) | Routes to host Ollama |

**Configuration** (`internal/model/model.go` — `ConfigureLLMSpy()`):
1. Patches `llms-secrets` Secret with the API key
2. Reads `llmspy-config` ConfigMap, sets `providers.<name>.enabled = true`
3. Restarts `llmspy` Deployment via rollout restart
4. Waits for rollout completion (60s timeout)

**CLI**:
```bash
obol model setup --provider anthropic --api-key sk-ant-...
obol model setup --provider openai --api-key sk-proj-...
obol model status  # Show which providers are enabled
```

### Tier 2: Per-Instance Config (per OpenClaw namespace)

Each OpenClaw instance has its own `values-obol.yaml` overlay that configures which models are available and how they route.

**All 3 paths use the same pattern**:
```yaml
models:
  ollama:
    enabled: true
    baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
    api: openai-completions
    apiKeyEnvVar: OLLAMA_API_KEY
    apiKeyValue: ollama-local
    models:
      - id: <model-id>
        name: <display-name>
```

The key trick: the "ollama" provider slot is repurposed to point at llmspy. The model ID is passed through to llmspy, which resolves the actual provider.

## The 3 Provider Paths

### Path 1: Ollama (Default — Local Inference)

```
OpenClaw                     llmspy                      Ollama
model: ollama/glm-5:cloud    resolves glm-5:cloud        runs inference locally
api: openai-completions  --> to ollama provider       --> on host GPU
baseUrl: llmspy:8000/v1      (default route)
```

**Setup**: No extra config needed. Ollama is enabled by default in llmspy.

**Go function**: `generateOverlayValues(hostname, nil, false, ollamaModels)`

The function auto-detects available Ollama models via `listOllamaModels()` which queries `http://localhost:11434/api/tags`.

### Path 2: Anthropic (Cloud via llmspy)

```
OpenClaw                              llmspy                          Anthropic
model: ollama/claude-sonnet-4-5...    resolves claude-* prefix        forwards to
api: openai-completions           --> to anthropic provider        --> api.anthropic.com
baseUrl: llmspy:8000/v1               (enabled via obol model setup)
```

**Setup**:
```bash
# Step 1: Enable Anthropic in llmspy (Tier 1)
obol model setup --provider anthropic --api-key sk-ant-...

# Step 2: Deploy OpenClaw with cloud overlay (Tier 2)
# Uses buildLLMSpyRoutedOverlay() to generate values-obol.yaml
```

**Go function**: `buildLLMSpyRoutedOverlay(&CloudProviderInfo{Name: "anthropic", ModelID: "claude-sonnet-4-5-20250929", ...})`

### Path 3: OpenAI (Cloud via llmspy)

```
OpenClaw                         llmspy                      OpenAI
model: ollama/gpt-4o-mini        resolves gpt-* prefix       forwards to
api: openai-completions      --> to openai provider      --> api.openai.com
baseUrl: llmspy:8000/v1          (enabled via obol model setup)
```

**Setup**: Same as Anthropic but with `--provider openai`.

## Model Detection

The function `detectOllama()` checks if Ollama is running on the host:
```go
func detectOllama() bool {
    // Tries http://localhost:11434/api/tags
    // Returns true if reachable
}
```

The function `listOllamaModels()` returns available model names:
```go
func listOllamaModels() []string {
    // Queries http://localhost:11434/api/tags
    // Returns slice like ["glm-5:cloud", "llama3.2:3b", ...]
}
```

## Data Flow (Detailed)

```
1. User sends POST /v1/chat/completions to OpenClaw (port 18789)
   {"model": "ollama/claude-sonnet-4-5-20250929", "messages": [...]}

2. OpenClaw reads its config, finds provider "ollama" with:
   baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
   api: openai-completions

3. OpenClaw forwards to llmspy:
   POST http://llmspy.llm.svc.cluster.local:8000/v1/chat/completions
   {"model": "claude-sonnet-4-5-20250929", "messages": [...]}

4. llmspy checks providers.json:
   - Model starts with "claude" → route to anthropic provider
   - Reads ANTHROPIC_API_KEY from llms-secrets

5. llmspy forwards to Anthropic API:
   POST https://api.anthropic.com/v1/messages
   Authorization: x-api-key <key>
   (converts from OpenAI format to Anthropic format)

6. Response flows back: Anthropic → llmspy → OpenClaw → user
```

## Key Constants

| Constant | Value | Where |
|----------|-------|-------|
| llmspy service URL | `http://llmspy.llm.svc.cluster.local:8000/v1` | In overlay YAML |
| OpenClaw port | `18789` | In Helm chart |
| Ollama host port | `11434` | Default Ollama |
| Provider names | `ollama`, `anthropic`, `openai` | In overlay + llmspy config |
| API format | `openai-completions` | Required for llmspy routing |
| Dummy API key | `ollama-local` | For the repurposed ollama provider |

## Overlay Generation Functions

| Function | Purpose | File |
|----------|---------|------|
| `generateOverlayValues()` | Generate values-obol.yaml for any path | `openclaw.go` |
| `buildLLMSpyRoutedOverlay()` | Build cloud provider overlay (Anthropic/OpenAI) | `openclaw.go` |
| `buildDirectProviderOverlay()` | Build direct provider overlay (no llmspy) | `openclaw.go` |
| `collectSensitiveData()` | Extract API keys from overlay for K8s Secrets | `openclaw.go` |
| `TranslateToOverlayYAML()` | Convert ImportResult to YAML string | `import.go` |
