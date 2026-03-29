package openclaw

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// testConfig creates a temp config dir with a .stack-id file for testing.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".stack-id"), []byte("test-cluster"), 0o644)

	return &config.Config{ConfigDir: dir, DataDir: dir, BinDir: dir}
}

func TestBuildLiteLLMRoutedOverlay_Anthropic(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  "sk-ant-test",
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}

	result := buildLiteLLMRoutedOverlay(testConfig(t), cloud)

	// Agent model uses openai/ prefix — routed through LiteLLM.
	if result.AgentModel != "openai/claude-sonnet-4-5-20250929" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "openai/claude-sonnet-4-5-20250929")
	}

	// Check 3 providers: openai (enabled, pointing at LiteLLM), anthropic (disabled), ollama (disabled)
	if len(result.Providers) != 3 {
		t.Fatalf("len(Providers) = %d, want 3", len(result.Providers))
	}

	openai := result.Providers[0]
	if openai.Name != "openai" || openai.Disabled {
		t.Errorf("openai: name=%q disabled=%v, want openai/false", openai.Name, openai.Disabled)
	}

	if openai.BaseURL != "http://litellm.llm.svc.cluster.local:4000/v1" {
		t.Errorf("openai.BaseURL = %q", openai.BaseURL)
	}

	if openai.APIKeyEnvVar != "OPENAI_API_KEY" {
		t.Errorf("openai.APIKeyEnvVar = %q, want OPENAI_API_KEY", openai.APIKeyEnvVar)
	}

	if openai.APIKey != "sk-obol-test-cluster" {
		t.Errorf("openai.APIKey = %q, want sk-obol-test-cluster", openai.APIKey)
	}

	if openai.API != "openai-completions" {
		t.Errorf("openai.API = %q, want openai-completions", openai.API)
	}

	if len(openai.Models) != 1 || openai.Models[0].ID != "claude-sonnet-4-5-20250929" {
		t.Errorf("openai.Models = %v", openai.Models)
	}

	// anthropic and ollama should be disabled
	for _, idx := range []int{1, 2} {
		if !result.Providers[idx].Disabled {
			t.Errorf("Providers[%d] (%s) should be disabled", idx, result.Providers[idx].Name)
		}
	}

	if result.Providers[1].Name != "anthropic" {
		t.Errorf("Providers[1].Name = %q, want anthropic", result.Providers[1].Name)
	}

	if result.Providers[2].Name != "ollama" {
		t.Errorf("Providers[2].Name = %q, want ollama", result.Providers[2].Name)
	}
}

func TestBuildLiteLLMRoutedOverlay_OpenAI(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "openai",
		APIKey:  "sk-open-test",
		ModelID: "gpt-5.2",
		Display: "GPT-5.2",
	}

	result := buildLiteLLMRoutedOverlay(testConfig(t), cloud)

	if result.AgentModel != "openai/gpt-5.2" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "openai/gpt-5.2")
	}

	ollama := result.Providers[0]
	if len(ollama.Models) != 1 || ollama.Models[0].ID != "gpt-5.2" {
		t.Errorf("ollama model = %v, want gpt-5.2", ollama.Models)
	}
}

func TestOverlayYAML_LiteLLMRouted(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  "sk-ant-test",
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}
	result := buildLiteLLMRoutedOverlay(testConfig(t), cloud)
	yaml := TranslateToOverlayYAML(result)

	// Agent model should have ollama/ prefix
	if !strings.Contains(yaml, "agentModel: openai/claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing agentModel, got:\n%s", yaml)
	}

	// openai should be enabled with LiteLLM baseUrl
	if !strings.Contains(yaml, "openai:\n    enabled: true") {
		t.Errorf("YAML missing enabled openai provider, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "baseUrl: http://litellm.llm.svc.cluster.local:4000/v1") {
		t.Errorf("YAML missing LiteLLM baseUrl, got:\n%s", yaml)
	}

	// apiKeyEnvVar should be OPENAI_API_KEY (LiteLLM master key injected via this env var)
	if !strings.Contains(yaml, "apiKeyEnvVar: OPENAI_API_KEY") {
		t.Errorf("YAML missing apiKeyEnvVar, got:\n%s", yaml)
	}

	// apiKeyValue should not be emitted; secrets are injected via env vars.
	if strings.Contains(yaml, "apiKeyValue:") {
		t.Errorf("YAML should not contain apiKeyValue literals, got:\n%s", yaml)
	}

	// api should be openai-completions (LiteLLM is OpenAI-compatible)
	if !strings.Contains(yaml, "api: openai-completions") {
		t.Errorf("YAML missing api: openai-completions, got:\n%s", yaml)
	}

	// Cloud model should appear in ollama's model list
	if !strings.Contains(yaml, "- id: claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing cloud model ID, got:\n%s", yaml)
	}

	// anthropic and ollama should be disabled
	if !strings.Contains(yaml, "anthropic:\n    enabled: false") {
		t.Errorf("YAML missing disabled anthropic, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "ollama:\n    enabled: false") {
		t.Errorf("YAML missing disabled ollama, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_OllamaDefaultWithModels(t *testing.T) {
	// When Ollama models are available, overlay should use them
	models := []string{"llama3.2:3b", "mistral:7b"}
	yaml := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", nil, false, models, "")

	if !strings.Contains(yaml, "agentModel: openai/llama3.2:3b") {
		t.Errorf("default overlay missing ollama agentModel, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "baseUrl: http://litellm.llm.svc.cluster.local:4000/v1") {
		t.Errorf("default overlay missing LiteLLM baseUrl, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "id: llama3.2:3b") {
		t.Errorf("default overlay missing first model, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "id: mistral:7b") {
		t.Errorf("default overlay missing second model, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_OllamaDefaultNoModels(t *testing.T) {
	// When no Ollama models are available, overlay should have empty model list
	yaml := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", nil, false, nil, "")

	if strings.Contains(yaml, "agentModel:") {
		t.Errorf("default overlay should not set agentModel when no models available, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "models: []") {
		t.Errorf("default overlay should have empty models list, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "baseUrl: http://litellm.llm.svc.cluster.local:4000/v1") {
		t.Errorf("default overlay missing LiteLLM baseUrl, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_ExternalSecrets(t *testing.T) {
	yaml := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", nil, true, nil, "")
	if !strings.Contains(yaml, "extraEnvFromSecrets") {
		t.Errorf("overlay missing extraEnvFromSecrets, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "openclaw-user-secrets") {
		t.Errorf("overlay missing external secret ref, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_AgentBaseURL(t *testing.T) {
	// When agentBaseURL is provided, it should appear in extraEnv.
	yaml := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", nil, false, nil, "https://mystack.example.com")

	if !strings.Contains(yaml, "AGENT_BASE_URL") {
		t.Errorf("overlay missing AGENT_BASE_URL, got:\n%s", yaml)
	}

	if !strings.Contains(yaml, "value: https://mystack.example.com") {
		t.Errorf("overlay missing AGENT_BASE_URL value, got:\n%s", yaml)
	}
	// REMOTE_SIGNER_URL should still be present.
	if !strings.Contains(yaml, "REMOTE_SIGNER_URL") {
		t.Errorf("overlay missing REMOTE_SIGNER_URL, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_NoAgentBaseURL(t *testing.T) {
	// When agentBaseURL is empty, AGENT_BASE_URL should NOT appear.
	yaml := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", nil, false, nil, "")

	if strings.Contains(yaml, "AGENT_BASE_URL") {
		t.Errorf("overlay should not contain AGENT_BASE_URL when empty, got:\n%s", yaml)
	}
}

func TestCollectSensitiveData_StripsLiterals(t *testing.T) {
	imported := &ImportResult{
		Providers: []ImportedProvider{
			{
				Name:         "openai",
				APIKey:       "sk-test",
				APIKeyEnvVar: "OPENAI_API_KEY",
			},
		},
		Channels: ImportedChannels{
			Telegram: &ImportedTelegram{BotToken: "tg-token"},
		},
	}

	data := collectSensitiveData(imported)
	if data["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("missing OPENAI_API_KEY in extracted data: %+v", data)
	}

	if data["TELEGRAM_BOT_TOKEN"] != "tg-token" {
		t.Fatalf("missing TELEGRAM_BOT_TOKEN in extracted data: %+v", data)
	}

	if imported.Providers[0].APIKey != "" {
		t.Fatalf("provider API key was not stripped from overlay data")
	}

	if imported.Channels.Telegram.BotToken != "" {
		t.Fatalf("telegram token was not stripped from overlay data")
	}
}

func TestBuildDirectProviderOverlay_OpenAI(t *testing.T) {
	result := buildDirectProviderOverlay(
		"openai",
		"https://api.openai.com/v1",
		"openai-completions",
		"OPENAI_API_KEY",
		"gpt-5.2",
		"GPT-5.2",
		"sk-open-test",
	)

	if result.AgentModel != "openai/gpt-5.2" {
		t.Fatalf("AgentModel = %q, want openai/gpt-5.2", result.AgentModel)
	}

	foundEnabled := false

	for _, p := range result.Providers {
		if p.Name == "openai" {
			foundEnabled = true

			if p.Disabled {
				t.Fatalf("openai provider should be enabled")
			}

			if p.APIKeyEnvVar != "OPENAI_API_KEY" {
				t.Fatalf("openai APIKeyEnvVar = %q", p.APIKeyEnvVar)
			}
		}
	}

	if !foundEnabled {
		t.Fatalf("openai provider not found in overlay")
	}
}

func TestBuildDirectProviderOverlay_Anthropic(t *testing.T) {
	result := buildDirectProviderOverlay(
		"anthropic",
		"https://api.anthropic.com",
		"anthropic-messages",
		"ANTHROPIC_API_KEY",
		"claude-sonnet-4-6",
		"Claude Sonnet 4.6",
		"sk-ant-test",
	)

	if result.AgentModel != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("AgentModel = %q, want anthropic/claude-sonnet-4-6", result.AgentModel)
	}

	foundEnabled := false

	for _, p := range result.Providers {
		if p.Name == "anthropic" {
			foundEnabled = true

			if p.Disabled {
				t.Fatalf("anthropic provider should be enabled")
			}

			if p.BaseURL != "https://api.anthropic.com" {
				t.Fatalf("anthropic BaseURL = %q, want https://api.anthropic.com (no /v1 suffix)", p.BaseURL)
			}

			if p.API != "anthropic-messages" {
				t.Fatalf("anthropic API = %q, want anthropic-messages", p.API)
			}

			if p.APIKeyEnvVar != "ANTHROPIC_API_KEY" {
				t.Fatalf("anthropic APIKeyEnvVar = %q", p.APIKeyEnvVar)
			}
		}

		if p.Name == "openai" && !p.Disabled {
			t.Fatalf("openai provider should be disabled for anthropic direct")
		}

		if p.Name == "ollama" && !p.Disabled {
			t.Fatalf("ollama provider should be disabled for anthropic direct")
		}
	}

	if !foundEnabled {
		t.Fatalf("anthropic provider not found in overlay")
	}
}

func TestPatchOverlayModelList(t *testing.T) {
	overlay := `# Default model provider
openclaw:
  agentModel: openai/llama3.2:3b
  gateway:
    controlUi:
      allowInsecureAuth: true

models:
  openai:
    enabled: true
    baseUrl: http://litellm.llm.svc.cluster.local:4000/v1
    api: openai-completions
    apiKeyEnvVar: OPENAI_API_KEY
    apiKeyValue: sk-obol-test
    models:
      - id: llama3.2:3b
        name: Llama 3.2 3B

# eRPC integration
erpc:
  url: http://erpc.erpc.svc.cluster.local/rpc
`

	t.Run("add models", func(t *testing.T) {
		updated, changed := patchOverlayModelList(overlay, []string{"llama3.2:3b", "claude-sonnet-4-5-20250929", "gpt-4o"})
		if !changed {
			t.Fatal("expected change")
		}

		if !strings.Contains(updated, "id: claude-sonnet-4-5-20250929") {
			t.Errorf("missing claude model in updated overlay:\n%s", updated)
		}

		if !strings.Contains(updated, "id: gpt-4o") {
			t.Errorf("missing gpt model in updated overlay:\n%s", updated)
		}
		// eRPC section should still be present
		if !strings.Contains(updated, "erpc:") {
			t.Errorf("eRPC section lost in updated overlay:\n%s", updated)
		}
	})

	t.Run("empty models", func(t *testing.T) {
		updated, changed := patchOverlayModelList(overlay, []string{})
		if !changed {
			t.Fatal("expected change")
		}

		if !strings.Contains(updated, "models: []") {
			t.Errorf("expected empty models list:\n%s", updated)
		}
	})

	t.Run("no litellm overlay", func(t *testing.T) {
		nonLiteLLM := `models:
  anthropic:
    enabled: true
    baseUrl: https://api.anthropic.com
`

		_, changed := patchOverlayModelList(nonLiteLLM, []string{"claude-sonnet-4-5-20250929"})
		if changed {
			t.Fatal("should not patch non-LiteLLM overlay")
		}
	})

	t.Run("empty initial models", func(t *testing.T) {
		emptyOverlay := strings.Replace(overlay,
			"    models:\n      - id: llama3.2:3b\n        name: Llama 3.2 3B",
			"    models: []", 1)

		updated, changed := patchOverlayModelList(emptyOverlay, []string{"llama3.2:3b"})
		if !changed {
			t.Fatal("expected change")
		}

		if !strings.Contains(updated, "id: llama3.2:3b") {
			t.Errorf("missing model in updated overlay:\n%s", updated)
		}
	})
}

func TestPatchAgentModelsJSON(t *testing.T) {
	t.Run("writes clean models.json", func(t *testing.T) {
		cfg := testConfig(t)
		id := "test"
		namespace := fmt.Sprintf("%s-%s", appName, id)
		agentDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "agents", "main", "agent")
		os.MkdirAll(agentDir, 0o755)

		models := []string{"claude-sonnet-4-6", "gpt-4o", "llama3.2:3b"}

		err := patchAgentModelsJSON(cfg, id, models, "sk-obol-test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
		if err != nil {
			t.Fatalf("failed to read models.json: %v", err)
		}

		content := string(data)
		if !strings.Contains(content, "claude-sonnet-4-6") {
			t.Errorf("missing claude model in models.json")
		}

		if !strings.Contains(content, "litellm.llm.svc.cluster.local:4000") {
			t.Errorf("missing LiteLLM URL in models.json")
		}

		if !strings.Contains(content, "sk-obol-test") {
			t.Errorf("missing master key in models.json")
		}
		// Should NOT contain stale providers
		if strings.Contains(content, "llmspy") {
			t.Errorf("models.json should not contain llmspy")
		}

		if strings.Contains(content, "ollama") {
			t.Errorf("models.json should not contain stale ollama provider")
		}
	})

	t.Run("skips when agent dir does not exist", func(t *testing.T) {
		cfg := testConfig(t)

		err := patchAgentModelsJSON(cfg, "nonexistent", []string{"model"}, "key")
		if err != nil {
			t.Fatalf("should skip gracefully, got error: %v", err)
		}
	})
}

func TestRemoteCapableCommands(t *testing.T) {
	// Commands that should go through port-forward
	remote := []string{"gateway", "acp", "browser", "logs"}
	for _, cmd := range remote {
		if !remoteCapableCommands[cmd] {
			t.Errorf("%q should be remote-capable", cmd)
		}
	}

	// Commands that should go through kubectl exec
	local := []string{"agent", "doctor", "config", "models", "message"}
	for _, cmd := range local {
		if remoteCapableCommands[cmd] {
			t.Errorf("%q should NOT be remote-capable", cmd)
		}
	}
}
