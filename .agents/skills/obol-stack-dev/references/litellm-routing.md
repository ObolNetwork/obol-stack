# LLM Smart-Routing Through LiteLLM

## Architecture: 2-Tier Model Routing

### Tier 1: Global LiteLLM Gateway (`llm` namespace)

A cluster-wide OpenAI-compatible proxy that routes LLM traffic to actual providers.

**Kubernetes Resources**:

| Resource | Type | Purpose |
|----------|------|---------|
| `llm` | Namespace | Dedicated namespace for LLM infrastructure |
| `litellm-config` | ConfigMap | `config.yaml` with `model_list` (model definitions + routing) |
| `litellm-secrets` | Secret | `LITELLM_MASTER_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` |
| `litellm` | Deployment | `ghcr.io/berriai/litellm:main-v1.82.3`, port 4000 |
| `litellm` | Service | `litellm.llm.svc.cluster.local:4000` |
| `ollama` | Service (ExternalName) | Routes to host Ollama |

**Configuration** (`internal/model/model.go` -- `ConfigureLiteLLM()`):
1. Patches `litellm-secrets` Secret with the API key
2. Reads `litellm-config` ConfigMap, adds model to `model_list` in `config.yaml`
3. Restarts `litellm` Deployment via rollout restart
4. Waits for rollout completion (60s timeout)
5. Syncs OpenClaw overlays to reflect updated model availability

**CLI**:
```bash
obol model setup --provider anthropic --api-key sk-ant-...
obol model setup --provider openai --api-key sk-proj-...
obol model setup custom --name my-model --endpoint http://example.com --model model-id
obol model status  # Show which providers are enabled
```

### Tier 2: Per-Instance Config (per OpenClaw namespace)

Each OpenClaw instance has its own `values-obol.yaml` overlay that configures which models are available and how they route.

**All paths use the same pattern**:
```yaml
models:
  openai:
    enabled: true
    baseUrl: http://litellm.llm.svc.cluster.local:4000/v1
    api: openai-completions
    apiKeyEnvVar: OPENAI_API_KEY
    apiKeyValue: sk-obol-<cluster-id>
    models:
      - id: <model-id>
        name: <display-name>
```

The "openai" provider slot points at LiteLLM. The model ID is passed through to LiteLLM, which resolves the actual provider based on its `model_list` configuration.

## The 3 Provider Paths

### Path 1: Ollama (Default -- Local Inference)

```
OpenClaw                     LiteLLM                     Ollama
model: openai/glm-5:cloud   resolves glm-5:cloud        runs inference locally
api: openai-completions  --> to ollama provider       --> on host GPU
baseUrl: litellm:4000/v1    (default route)
```

**Setup**: No extra config needed. Ollama models are added to LiteLLM's `model_list` by default.

**Go function**: `generateOverlayValues(hostname, nil, false, ollamaModels)`

The function auto-detects available Ollama models via `listOllamaModels()` which queries `http://localhost:11434/api/tags`.

### Path 2: Anthropic (Cloud via LiteLLM)

```
OpenClaw                              LiteLLM                         Anthropic
model: openai/claude-sonnet-4-5...   resolves claude-* model         forwards to
api: openai-completions           --> via model_list config        --> api.anthropic.com
baseUrl: litellm:4000/v1             (enabled via obol model setup)
```

**Setup**:
```bash
# Step 1: Enable Anthropic in LiteLLM (Tier 1)
obol model setup --provider anthropic --api-key sk-ant-...

# Step 2: Deploy OpenClaw with cloud overlay (Tier 2)
# Uses buildLiteLLMRoutedOverlay() to generate values-obol.yaml
```

**Go function**: `buildLiteLLMRoutedOverlay(&CloudProviderInfo{Name: "anthropic", ModelID: "claude-sonnet-4-5-20250929", ...})`

### Path 3: OpenAI (Cloud via LiteLLM)

```
OpenClaw                         LiteLLM                     OpenAI
model: openai/gpt-4o-mini       resolves gpt-* model        forwards to
api: openai-completions      --> via model_list config   --> api.openai.com
baseUrl: litellm:4000/v1        (enabled via obol model setup)
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
   {"model": "openai/claude-sonnet-4-5-20250929", "messages": [...]}

2. OpenClaw reads its config, finds provider "openai" with:
   baseUrl: http://litellm.llm.svc.cluster.local:4000/v1
   api: openai-completions

3. OpenClaw forwards to LiteLLM:
   POST http://litellm.llm.svc.cluster.local:4000/v1/chat/completions
   {"model": "claude-sonnet-4-5-20250929", "messages": [...]}

4. LiteLLM checks its model_list in config.yaml:
   - Finds model "claude-sonnet-4-5-20250929" -> anthropic provider
   - Reads ANTHROPIC_API_KEY from litellm-secrets via os.environ

5. LiteLLM forwards to Anthropic API:
   POST https://api.anthropic.com/v1/messages
   Authorization: x-api-key <key>
   (converts from OpenAI format to Anthropic format)

6. Response flows back: Anthropic -> LiteLLM -> OpenClaw -> user
```

## Key Constants

| Constant | Value | Where |
|----------|-------|-------|
| LiteLLM service URL | `http://litellm.llm.svc.cluster.local:4000/v1` | In overlay YAML |
| OpenClaw port | `18789` | In Helm chart |
| Ollama host port | `11434` | Default Ollama |
| Provider names | `ollama`, `anthropic`, `openai` | In model_list + overlay |
| API format | `openai-completions` | Required for LiteLLM routing |
| Master key | `sk-obol-<cluster-id>` | LITELLM_MASTER_KEY in litellm-secrets |
| Config format | `config.yaml` with `model_list` | litellm-config ConfigMap |

## Overlay Generation Functions

| Function | Purpose | File |
|----------|---------|------|
| `generateOverlayValues()` | Generate values-obol.yaml for any path | `openclaw.go` |
| `buildLiteLLMRoutedOverlay()` | Build cloud provider overlay (Anthropic/OpenAI) | `openclaw.go` |
| `buildDirectProviderOverlay()` | Build direct provider overlay (no LiteLLM) | `openclaw.go` |
| `collectSensitiveData()` | Extract API keys from overlay for K8s Secrets | `openclaw.go` |
| `TranslateToOverlayYAML()` | Convert ImportResult to YAML string | `import.go` |
