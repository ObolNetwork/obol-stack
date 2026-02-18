package openclaw

import (
	"strings"
	"testing"
)

func TestBuildLLMSpyRoutedOverlay_Anthropic(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  "sk-ant-test",
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}

	result := buildLLMSpyRoutedOverlay(cloud)

	// Agent model uses ollama/ prefix — the "ollama" provider slot is repurposed
	// to point at llmspy, so the model reference must match the provider name.
	if result.AgentModel != "ollama/claude-sonnet-4-5-20250929" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "ollama/claude-sonnet-4-5-20250929")
	}

	// Check 3 providers: ollama (enabled, pointing at llmspy), anthropic (disabled), openai (disabled)
	if len(result.Providers) != 3 {
		t.Fatalf("len(Providers) = %d, want 3", len(result.Providers))
	}

	ollama := result.Providers[0]
	if ollama.Name != "ollama" || ollama.Disabled {
		t.Errorf("ollama: name=%q disabled=%v, want ollama/false", ollama.Name, ollama.Disabled)
	}
	if ollama.BaseURL != "http://llmspy.llm.svc.cluster.local:8000/v1" {
		t.Errorf("ollama.BaseURL = %q", ollama.BaseURL)
	}
	if ollama.APIKeyEnvVar != "OLLAMA_API_KEY" {
		t.Errorf("ollama.APIKeyEnvVar = %q, want OLLAMA_API_KEY", ollama.APIKeyEnvVar)
	}
	if ollama.APIKey != "ollama-local" {
		t.Errorf("ollama.APIKey = %q, want ollama-local", ollama.APIKey)
	}
	if ollama.API != "openai-completions" {
		t.Errorf("ollama.API = %q, want openai-completions", ollama.API)
	}
	if len(ollama.Models) != 1 || ollama.Models[0].ID != "claude-sonnet-4-5-20250929" {
		t.Errorf("ollama.Models = %v", ollama.Models)
	}

	// anthropic and openai should be disabled
	for _, idx := range []int{1, 2} {
		if !result.Providers[idx].Disabled {
			t.Errorf("Providers[%d] (%s) should be disabled", idx, result.Providers[idx].Name)
		}
	}
	if result.Providers[1].Name != "anthropic" {
		t.Errorf("Providers[1].Name = %q, want anthropic", result.Providers[1].Name)
	}
	if result.Providers[2].Name != "openai" {
		t.Errorf("Providers[2].Name = %q, want openai", result.Providers[2].Name)
	}
}

func TestBuildLLMSpyRoutedOverlay_OpenAI(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "openai",
		APIKey:  "sk-open-test",
		ModelID: "gpt-5.2",
		Display: "GPT-5.2",
	}

	result := buildLLMSpyRoutedOverlay(cloud)

	if result.AgentModel != "ollama/gpt-5.2" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "ollama/gpt-5.2")
	}

	ollama := result.Providers[0]
	if len(ollama.Models) != 1 || ollama.Models[0].ID != "gpt-5.2" {
		t.Errorf("ollama model = %v, want gpt-5.2", ollama.Models)
	}
}

func TestOverlayYAML_LLMSpyRouted(t *testing.T) {
	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  "sk-ant-test",
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}
	result := buildLLMSpyRoutedOverlay(cloud)
	yaml := TranslateToOverlayYAML(result)

	// Agent model should have ollama/ prefix
	if !strings.Contains(yaml, "agentModel: ollama/claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing agentModel, got:\n%s", yaml)
	}

	// ollama should be enabled with llmspy baseUrl
	if !strings.Contains(yaml, "ollama:\n    enabled: true") {
		t.Errorf("YAML missing enabled ollama provider, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1") {
		t.Errorf("YAML missing llmspy baseUrl, got:\n%s", yaml)
	}

	// apiKeyEnvVar should be OLLAMA_API_KEY
	if !strings.Contains(yaml, "apiKeyEnvVar: OLLAMA_API_KEY") {
		t.Errorf("YAML missing apiKeyEnvVar, got:\n%s", yaml)
	}

	// apiKeyValue should not be emitted; secrets are injected via env vars.
	if strings.Contains(yaml, "apiKeyValue:") {
		t.Errorf("YAML should not contain apiKeyValue literals, got:\n%s", yaml)
	}

	// api should be openai-completions (llmspy is OpenAI-compatible)
	if !strings.Contains(yaml, "api: openai-completions") {
		t.Errorf("YAML missing api: openai-completions, got:\n%s", yaml)
	}

	// Cloud model should appear in ollama's model list
	if !strings.Contains(yaml, "- id: claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing cloud model ID, got:\n%s", yaml)
	}

	// anthropic and openai should be disabled
	if !strings.Contains(yaml, "anthropic:\n    enabled: false") {
		t.Errorf("YAML missing disabled anthropic, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "openai:\n    enabled: false") {
		t.Errorf("YAML missing disabled openai, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_OllamaDefault(t *testing.T) {
	// When imported is nil, generateOverlayValues should use Ollama defaults
	yaml := generateOverlayValues("openclaw-default.obol.stack", nil, false)

	if !strings.Contains(yaml, "agentModel: ollama/gpt-oss:120b-cloud") {
		t.Errorf("default overlay missing ollama agentModel, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1") {
		t.Errorf("default overlay missing llmspy baseUrl, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_ExternalSecrets(t *testing.T) {
	yaml := generateOverlayValues("openclaw-default.obol.stack", nil, true)
	if !strings.Contains(yaml, "extraEnvFromSecrets") {
		t.Errorf("overlay missing extraEnvFromSecrets, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "openclaw-user-secrets") {
		t.Errorf("overlay missing external secret ref, got:\n%s", yaml)
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
