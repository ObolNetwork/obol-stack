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

	// Check agent model uses llmspy/ prefix for correct OpenClaw provider routing
	if result.AgentModel != "llmspy/claude-sonnet-4-5-20250929" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "llmspy/claude-sonnet-4-5-20250929")
	}

	// Check 4 providers: llmspy (enabled), ollama (disabled), anthropic (disabled), openai (disabled)
	if len(result.Providers) != 4 {
		t.Fatalf("len(Providers) = %d, want 4", len(result.Providers))
	}

	llmspy := result.Providers[0]
	if llmspy.Name != "llmspy" || llmspy.Disabled {
		t.Errorf("llmspy: name=%q disabled=%v, want llmspy/false", llmspy.Name, llmspy.Disabled)
	}
	if llmspy.BaseURL != "http://llmspy.llm.svc.cluster.local:8000/v1" {
		t.Errorf("llmspy.BaseURL = %q", llmspy.BaseURL)
	}
	if llmspy.APIKeyEnvVar != "LLMSPY_API_KEY" {
		t.Errorf("llmspy.APIKeyEnvVar = %q, want LLMSPY_API_KEY", llmspy.APIKeyEnvVar)
	}
	if llmspy.APIKey != "llmspy-default" {
		t.Errorf("llmspy.APIKey = %q, want llmspy-default", llmspy.APIKey)
	}
	if llmspy.API != "openai-completions" {
		t.Errorf("llmspy.API = %q, want openai-completions", llmspy.API)
	}
	if len(llmspy.Models) != 1 || llmspy.Models[0].ID != "claude-sonnet-4-5-20250929" {
		t.Errorf("llmspy.Models = %v", llmspy.Models)
	}

	// ollama, anthropic and openai should be disabled
	for _, idx := range []int{1, 2, 3} {
		if !result.Providers[idx].Disabled {
			t.Errorf("Providers[%d] (%s) should be disabled", idx, result.Providers[idx].Name)
		}
	}
	if result.Providers[1].Name != "ollama" {
		t.Errorf("Providers[1].Name = %q, want ollama", result.Providers[1].Name)
	}
	if result.Providers[2].Name != "anthropic" {
		t.Errorf("Providers[2].Name = %q, want anthropic", result.Providers[2].Name)
	}
	if result.Providers[3].Name != "openai" {
		t.Errorf("Providers[3].Name = %q, want openai", result.Providers[3].Name)
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

	if result.AgentModel != "llmspy/gpt-5.2" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "llmspy/gpt-5.2")
	}

	llmspy := result.Providers[0]
	if len(llmspy.Models) != 1 || llmspy.Models[0].ID != "gpt-5.2" {
		t.Errorf("llmspy model = %v, want gpt-5.2", llmspy.Models)
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

	// Agent model should have llmspy/ prefix
	if !strings.Contains(yaml, "agentModel: llmspy/claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing agentModel, got:\n%s", yaml)
	}

	// llmspy should be enabled with llmspy baseUrl
	if !strings.Contains(yaml, "llmspy:\n    enabled: true") {
		t.Errorf("YAML missing enabled llmspy provider, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1") {
		t.Errorf("YAML missing llmspy baseUrl, got:\n%s", yaml)
	}

	// apiKeyEnvVar should be LLMSPY_API_KEY
	if !strings.Contains(yaml, "apiKeyEnvVar: LLMSPY_API_KEY") {
		t.Errorf("YAML missing apiKeyEnvVar, got:\n%s", yaml)
	}

	// apiKeyValue should be llmspy-default
	if !strings.Contains(yaml, "apiKeyValue: llmspy-default") {
		t.Errorf("YAML missing apiKeyValue, got:\n%s", yaml)
	}

	// api should be openai-completions (llmspy is OpenAI-compatible)
	if !strings.Contains(yaml, "api: openai-completions") {
		t.Errorf("YAML missing api: openai-completions, got:\n%s", yaml)
	}

	// Cloud model should appear in llmspy's model list
	if !strings.Contains(yaml, "- id: claude-sonnet-4-5-20250929") {
		t.Errorf("YAML missing cloud model ID, got:\n%s", yaml)
	}

	// ollama, anthropic and openai should be disabled
	if !strings.Contains(yaml, "ollama:\n    enabled: false") {
		t.Errorf("YAML missing disabled ollama, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "anthropic:\n    enabled: false") {
		t.Errorf("YAML missing disabled anthropic, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "openai:\n    enabled: false") {
		t.Errorf("YAML missing disabled openai, got:\n%s", yaml)
	}
}

func TestGenerateOverlayValues_OllamaDefault(t *testing.T) {
	// When imported is nil, generateOverlayValues should use Ollama defaults
	yaml := generateOverlayValues("openclaw-default.obol.stack", nil)

	if !strings.Contains(yaml, "agentModel: ollama/glm-4.7-flash") {
		t.Errorf("default overlay missing ollama agentModel, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1") {
		t.Errorf("default overlay missing llmspy baseUrl, got:\n%s", yaml)
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
