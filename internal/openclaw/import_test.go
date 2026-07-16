package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIsEnvVarRef(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"${ANTHROPIC_API_KEY}", true},
		{"${VAR:default}", true},
		{"prefix${VAR}suffix", true},
		{"sk-ant-literal-key", false},
		{"", false},
		{"$VAR", false},
		{"plain-string", false},
	}
	for _, tt := range tests {
		if got := isEnvVarRef(tt.in); got != tt.want {
			t.Errorf("isEnvVarRef(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestExtractEnvVarName(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"${OPENAI_API_KEY}", "OPENAI_API_KEY", true},
		{"${OPENAI_API_KEY:default}", "OPENAI_API_KEY", true},
		{"OPENAI_API_KEY", "", false},
		{"${}", "", false},
	}

	for _, tt := range tests {
		got, ok := extractEnvVarName(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("extractEnvVarName(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestDefaultProviderAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"ollama", "OLLAMA_API_KEY"},
		{"my-provider", "MY_PROVIDER_API_KEY"},
	}

	for _, tt := range tests {
		if got := defaultProviderAPIKeyEnvVar(tt.provider); got != tt.want {
			t.Errorf("defaultProviderAPIKeyEnvVar(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestSanitizeModelAPI(t *testing.T) {
	// All valid values should pass through unchanged
	valid := []string{
		"openai-completions",
		"openai-responses",
		"anthropic-messages",
		"google-generative-ai",
		"github-copilot",
		"bedrock-converse-stream",
	}
	for _, api := range valid {
		if got := sanitizeModelAPI(api); got != api {
			t.Errorf("sanitizeModelAPI(%q) = %q, want %q", api, got, api)
		}
	}

	// Invalid values should return ""
	invalid := []string{
		"custom-api",
		"openai",
		"",
		"OpenAI-Completions",
		"mistral-api",
	}
	for _, api := range invalid {
		if got := sanitizeModelAPI(api); got != "" {
			t.Errorf("sanitizeModelAPI(%q) = %q, want empty", api, got)
		}
	}
}

func TestDetectWorkspace(t *testing.T) {
	t.Run("dir with SOUL.md marker", func(t *testing.T) {
		home := t.TempDir()
		wsDir := filepath.Join(home, ".openclaw", "workspace")
		os.MkdirAll(wsDir, 0o755)
		os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("test"), 0o644)

		got := detectWorkspace(home, "")
		if got != wsDir {
			t.Errorf("detectWorkspace() = %q, want %q", got, wsDir)
		}
	})

	t.Run("dir with AGENTS.md marker only", func(t *testing.T) {
		home := t.TempDir()
		wsDir := filepath.Join(home, ".openclaw", "workspace")
		os.MkdirAll(wsDir, 0o755)
		os.WriteFile(filepath.Join(wsDir, "AGENTS.md"), []byte("test"), 0o644)

		got := detectWorkspace(home, "")
		if got != wsDir {
			t.Errorf("detectWorkspace() = %q, want %q", got, wsDir)
		}
	})

	t.Run("dir with IDENTITY.md marker only", func(t *testing.T) {
		home := t.TempDir()
		wsDir := filepath.Join(home, ".openclaw", "workspace")
		os.MkdirAll(wsDir, 0o755)
		os.WriteFile(filepath.Join(wsDir, "IDENTITY.md"), []byte("test"), 0o644)

		got := detectWorkspace(home, "")
		if got != wsDir {
			t.Errorf("detectWorkspace() = %q, want %q", got, wsDir)
		}
	})

	t.Run("dir exists but no marker files", func(t *testing.T) {
		home := t.TempDir()
		wsDir := filepath.Join(home, ".openclaw", "workspace")
		os.MkdirAll(wsDir, 0o755)
		os.WriteFile(filepath.Join(wsDir, "readme.txt"), []byte("test"), 0o644)

		got := detectWorkspace(home, "")
		if got != "" {
			t.Errorf("detectWorkspace() = %q, want empty", got)
		}
	})

	t.Run("dir does not exist", func(t *testing.T) {
		home := t.TempDir()

		got := detectWorkspace(home, "")
		if got != "" {
			t.Errorf("detectWorkspace() = %q, want empty", got)
		}
	})

	t.Run("custom workspace path from config", func(t *testing.T) {
		home := t.TempDir()
		customWs := filepath.Join(t.TempDir(), "my-workspace")
		os.MkdirAll(customWs, 0o755)
		os.WriteFile(filepath.Join(customWs, "SOUL.md"), []byte("test"), 0o644)

		got := detectWorkspace(home, customWs)
		if got != customWs {
			t.Errorf("detectWorkspace() = %q, want %q", got, customWs)
		}
	})
}

func TestDetectWorkspaceFiles(t *testing.T) {
	t.Run("all files present", func(t *testing.T) {
		wsDir := t.TempDir()
		for _, f := range []string{"SOUL.md", "AGENTS.md", "IDENTITY.md", "USER.md", "TOOLS.md", "MEMORY.md"} {
			os.WriteFile(filepath.Join(wsDir, f), []byte("test"), 0o644)
		}

		os.Mkdir(filepath.Join(wsDir, "memory"), 0o755)

		got := detectWorkspaceFiles(wsDir)
		if len(got) != 7 {
			t.Errorf("detectWorkspaceFiles() returned %d items, want 7: %v", len(got), got)
		}
	})

	t.Run("only SOUL.md", func(t *testing.T) {
		wsDir := t.TempDir()
		os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("test"), 0o644)

		got := detectWorkspaceFiles(wsDir)
		if len(got) != 1 || got[0] != "SOUL.md" {
			t.Errorf("detectWorkspaceFiles() = %v, want [SOUL.md]", got)
		}
	})

	t.Run("memory dir included", func(t *testing.T) {
		wsDir := t.TempDir()
		os.Mkdir(filepath.Join(wsDir, "memory"), 0o755)

		got := detectWorkspaceFiles(wsDir)
		if len(got) != 1 || got[0] != "memory/" {
			t.Errorf("detectWorkspaceFiles() = %v, want [memory/]", got)
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		wsDir := t.TempDir()

		got := detectWorkspaceFiles(wsDir)
		if len(got) != 0 {
			t.Errorf("detectWorkspaceFiles() = %v, want empty", got)
		}
	})
}

func TestTranslateToOverlayYAML_Nil(t *testing.T) {
	got := TranslateToOverlayYAML(nil)
	if got != "" {
		t.Errorf("TranslateToOverlayYAML(nil) = %q, want empty", got)
	}
}

func TestTranslateToOverlayYAML_AgentModelOnly(t *testing.T) {
	result := &ImportResult{
		AgentModel: "claude-sonnet-4-6",
	}

	got := TranslateToOverlayYAML(result)
	if !strings.Contains(got, `agentModel: "claude-sonnet-4-6"`) {
		t.Errorf("YAML missing agentModel, got:\n%s", got)
	}

	if strings.Contains(got, "models:") {
		t.Errorf("YAML should not contain models section, got:\n%s", got)
	}
}

func TestTranslateToOverlayYAML_ProviderWithModels(t *testing.T) {
	result := &ImportResult{
		Providers: []ImportedProvider{
			{
				Name:    "anthropic",
				BaseURL: "https://api.anthropic.com",
				API:     "anthropic-messages",
				APIKey:  "sk-ant-test",
				Models: []ImportedModel{
					{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
				},
			},
		},
	}
	got := TranslateToOverlayYAML(result)

	checks := []string{
		"\"anthropic\":\n    enabled: true",
		`baseUrl: "https://api.anthropic.com"`,
		"api: anthropic-messages",
		`- id: "claude-sonnet-4-6"`,
		`name: "Claude Sonnet 4.6"`,
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("YAML missing %q, got:\n%s", check, got)
		}
	}
}

func TestTranslateToOverlayYAML_DisabledProvider(t *testing.T) {
	result := &ImportResult{
		Providers: []ImportedProvider{
			{Name: "openai", Disabled: true},
		},
	}
	got := TranslateToOverlayYAML(result)

	if !strings.Contains(got, "\"openai\":\n    enabled: false") {
		t.Errorf("YAML missing disabled openai, got:\n%s", got)
	}

	if strings.Contains(got, "enabled: true") {
		t.Errorf("YAML should not contain enabled: true for disabled provider, got:\n%s", got)
	}
}

func TestTranslateToOverlayYAML_EmptyAPI(t *testing.T) {
	result := &ImportResult{
		Providers: []ImportedProvider{
			{
				Name:    "custom",
				BaseURL: "https://custom.api/v1",
				API:     "",
			},
		},
	}
	got := TranslateToOverlayYAML(result)

	if !strings.Contains(got, `api: ""`) {
		t.Errorf("YAML missing empty api field, got:\n%s", got)
	}
}

func TestTranslateToOverlayYAML_Channels(t *testing.T) {
	result := &ImportResult{
		Channels: ImportedChannels{
			Telegram: &ImportedTelegram{BotToken: "123456:ABC"},
			Discord:  &ImportedDiscord{BotToken: "MTIz..."},
			Slack:    &ImportedSlack{BotToken: "xoxb-test", AppToken: "xapp-test"},
		},
	}
	got := TranslateToOverlayYAML(result)

	checks := []string{
		"telegram:\n    enabled: true",
		"discord:\n    enabled: true",
		"slack:\n    enabled: true",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("YAML missing %q, got:\n%s", check, got)
		}
	}

	for _, unexpected := range []string{"botToken:", "appToken:"} {
		if strings.Contains(got, unexpected) {
			t.Errorf("YAML should not contain %q, got:\n%s", unexpected, got)
		}
	}
}

func TestTranslateToOverlayYAML_FullConfig(t *testing.T) {
	result := &ImportResult{
		AgentModel: "claude-sonnet-4-6",
		Providers: []ImportedProvider{
			{
				Name:    "anthropic",
				BaseURL: "https://api.anthropic.com",
				API:     "anthropic-messages",
				APIKey:  "sk-ant-test",
				Models:  []ImportedModel{{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"}},
			},
			{Name: "openai", Disabled: true},
		},
		Channels: ImportedChannels{
			Telegram: &ImportedTelegram{BotToken: "123:ABC"},
		},
	}
	got := TranslateToOverlayYAML(result)

	if !strings.Contains(got, `agentModel: "claude-sonnet-4-6"`) {
		t.Errorf("YAML missing agentModel, got:\n%s", got)
	}

	if !strings.Contains(got, "\"anthropic\":\n    enabled: true") {
		t.Errorf("YAML missing enabled anthropic, got:\n%s", got)
	}

	if !strings.Contains(got, "\"openai\":\n    enabled: false") {
		t.Errorf("YAML missing disabled openai, got:\n%s", got)
	}

	if !strings.Contains(got, "telegram:\n    enabled: true") {
		t.Errorf("YAML missing telegram channel, got:\n%s", got)
	}
}

// writeTestOpenclawConfig creates a test openclaw.json at the expected path
func writeTestOpenclawConfig(t *testing.T, home string, cfg *openclawConfig) {
	t.Helper()

	dir := filepath.Join(home, ".openclaw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "openclaw.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectExistingConfigAt_FileNotFound(t *testing.T) {
	home := t.TempDir()

	result, err := detectExistingConfigAt(home)
	if result != nil || err != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", result, err)
	}
}

func TestDetectExistingConfigAt_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".openclaw")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "openclaw.json"), []byte("{invalid json"), 0o644)

	result, err := detectExistingConfigAt(home)
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}

	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("error should mention parsing, got: %v", err)
	}
}

func TestDetectExistingConfigAt_ValidConfig(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	cfg.Models.Providers = map[string]openclawProvider{
		"anthropic": {
			BaseURL: "https://api.anthropic.com",
			API:     "anthropic-messages",
			APIKey:  "sk-ant-test-key",
			Models:  []openclawModel{{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"}},
		},
	}
	cfg.Agents.Defaults.Model.Primary = "claude-sonnet-4-6"
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.AgentModel != "claude-sonnet-4-6" {
		t.Errorf("AgentModel = %q, want %q", result.AgentModel, "claude-sonnet-4-6")
	}

	if len(result.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(result.Providers))
	}

	p := result.Providers[0]
	if p.Name != "anthropic" {
		t.Errorf("Provider.Name = %q, want %q", p.Name, "anthropic")
	}

	if p.APIKey != "sk-ant-test-key" {
		t.Errorf("Provider.APIKey = %q, want %q", p.APIKey, "sk-ant-test-key")
	}

	if p.API != "anthropic-messages" {
		t.Errorf("Provider.API = %q, want %q", p.API, "anthropic-messages")
	}

	if p.APIKeyEnvVar != "ANTHROPIC_API_KEY" {
		t.Errorf("Provider.APIKeyEnvVar = %q, want %q", p.APIKeyEnvVar, "ANTHROPIC_API_KEY")
	}

	if len(p.Models) != 1 || p.Models[0].ID != "claude-sonnet-4-6" {
		t.Errorf("Provider.Models = %v", p.Models)
	}
}

func TestDetectExistingConfigAt_EnvVarKeySkipped(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	cfg.Models.Providers = map[string]openclawProvider{
		"openai": {
			BaseURL: "https://api.openai.com/v1",
			API:     "openai-completions",
			APIKey:  "${OPENAI_API_KEY}",
			Models:  []openclawModel{{ID: "gpt-5.2", Name: "GPT-5.2"}},
		},
	}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if len(result.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(result.Providers))
	}

	if result.Providers[0].APIKey != "" {
		t.Errorf("Provider.APIKey = %q, want empty (env-var should be skipped)", result.Providers[0].APIKey)
	}

	if result.Providers[0].APIKeyEnvVar != "OPENAI_API_KEY" {
		t.Errorf("Provider.APIKeyEnvVar = %q, want OPENAI_API_KEY", result.Providers[0].APIKeyEnvVar)
	}
}

func TestDetectExistingConfigAt_ChannelImport(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	cfg.Channels.Telegram = &struct {
		BotToken string `json:"botToken"`
	}{BotToken: "123456:ABCDEF"}
	cfg.Channels.Discord = &struct {
		BotToken string `json:"botToken"`
	}{BotToken: "MTIzNDU2"}
	cfg.Channels.Slack = &struct {
		BotToken string `json:"botToken"`
		AppToken string `json:"appToken"`
	}{BotToken: "xoxb-test", AppToken: "xapp-test"}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Channels.Telegram == nil || result.Channels.Telegram.BotToken != "123456:ABCDEF" {
		t.Errorf("Telegram = %v", result.Channels.Telegram)
	}

	if result.Channels.Discord == nil || result.Channels.Discord.BotToken != "MTIzNDU2" {
		t.Errorf("Discord = %v", result.Channels.Discord)
	}

	if result.Channels.Slack == nil || result.Channels.Slack.BotToken != "xoxb-test" || result.Channels.Slack.AppToken != "xapp-test" {
		t.Errorf("Slack = %v", result.Channels.Slack)
	}
}

func TestDetectExistingConfigAt_ChannelEnvVarSkipped(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	cfg.Channels.Telegram = &struct {
		BotToken string `json:"botToken"`
	}{BotToken: "${TELEGRAM_TOKEN}"}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Channels.Telegram != nil {
		t.Errorf("Telegram should be nil when token is env-var, got %v", result.Channels.Telegram)
	}
}

func TestDetectExistingConfigAt_WorkspaceDetection(t *testing.T) {
	home := t.TempDir()
	wsDir := filepath.Join(home, ".openclaw", "workspace")
	os.MkdirAll(wsDir, 0o755)
	os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("I am an agent"), 0o644)

	cfg := &openclawConfig{}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.WorkspaceDir != wsDir {
		t.Errorf("WorkspaceDir = %q, want %q", result.WorkspaceDir, wsDir)
	}
}

func TestDetectExistingConfigAt_UnknownAPISanitized(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	cfg.Models.Providers = map[string]openclawProvider{
		"custom": {
			BaseURL: "https://custom.api/v1",
			API:     "custom-protocol",
			APIKey:  "key123",
		},
	}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(result.Providers))
	}

	if result.Providers[0].API != "" {
		t.Errorf("Provider.API = %q, want empty (unknown API should be sanitized)", result.Providers[0].API)
	}
}

func TestDetectExistingConfigAt_EmptyConfig(t *testing.T) {
	home := t.TempDir()
	cfg := &openclawConfig{}
	writeTestOpenclawConfig(t, home, cfg)

	result, err := detectExistingConfigAt(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result for valid but empty config")
	}

	if len(result.Providers) != 0 {
		t.Errorf("len(Providers) = %d, want 0", len(result.Providers))
	}

	if result.AgentModel != "" {
		t.Errorf("AgentModel = %q, want empty", result.AgentModel)
	}
}

// TestYAMLScalar_EscapesInjection is a Canary402 full-surface audit
// regression test: values imported from ~/.openclaw/openclaw.json must never
// be able to inject additional YAML keys (e.g. overriding `image:` to
// achieve RCE via `helmfile sync`) when hand-formatted into the overlay
// values file.
func TestYAMLScalar_EscapesInjection(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"plain", "openai/claude-sonnet-4-6"},
		{"newline_key_injection", "x\nimage:\n  repository: evil"},
		{"colon", "has: colon"},
		{"quotes_and_backslash", `back\slash"quote`},
		{"empty", ""},
		{"trailing_newline_injection", "gpt-5.2\nrbac:\n  create: false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := "key: " + yamlScalar(tt.in) + "\n"

			var doc map[string]any
			if err := yaml.Unmarshal([]byte(line), &doc); err != nil {
				t.Fatalf("yamlScalar(%q) produced invalid YAML %q: %v", tt.in, line, err)
			}

			if got, ok := doc["key"].(string); !ok || got != tt.in {
				t.Errorf("round-trip = %#v, want single scalar %q (no injected keys); rendered: %q", doc["key"], tt.in, line)
			}

			if len(doc) != 1 {
				t.Errorf("doc has %d top-level keys, want 1 (injection leaked extra keys): %#v", len(doc), doc)
			}
		})
	}
}

// TestGenerateOverlayValues_RejectsYAMLInjection feeds generateOverlayValues
// a malicious imported agentModel and provider name (newline + "image:")
// and asserts the rendered values-obol.yaml parses to the SAME single
// scalar values, with no injected top-level "image" key — i.e. the fix for
// the confirmed Canary402 full-surface audit finding holds end-to-end.
func TestGenerateOverlayValues_RejectsYAMLInjection(t *testing.T) {
	maliciousModel := "x\nimage:\n  repository: pwned-by-canary402\nrbac:\n  create: false"
	maliciousProvider := "ollama\nimage:\n  repository: pwned-by-canary402"

	imported := &ImportResult{
		AgentModel: maliciousModel,
		Providers: []ImportedProvider{
			{Name: maliciousProvider, BaseURL: "http://localhost:11434"},
		},
	}

	rendered := generateOverlayValues(testConfig(t), "openclaw-default.obol.stack", imported, false, nil, "")

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered overlay is not valid YAML: %v\n%s", err, rendered)
	}

	// The chart-default image override is legitimate and expected to stay a
	// map with only "tag" — injection would add a "repository" key to it.
	if img, ok := doc["image"].(map[string]any); ok {
		if _, hasRepo := img["repository"]; hasRepo {
			t.Errorf("attacker injected an 'image.repository' key: %#v\nrendered:\n%s", img, rendered)
		}
	}

	openclawSection, _ := doc["openclaw"].(map[string]any)
	if openclawSection == nil || openclawSection["agentModel"] != "openai/"+maliciousModel {
		t.Errorf("agentModel = %#v, want single scalar %q (rewritten with openai/ prefix); rendered:\n%s",
			openclawSection["agentModel"], "openai/"+maliciousModel, rendered)
	}
}
