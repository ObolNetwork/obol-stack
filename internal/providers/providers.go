package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DetectedConfig holds the merged configuration detected from all sources.
type DetectedConfig struct {
	Providers  []ProviderConfig
	Channels   ChannelConfig
	AgentModel string
}

// ProviderConfig represents a model provider.
type ProviderConfig struct {
	Name    string
	BaseURL string
	API     string
	APIKey  string
	Models  []ModelConfig
}

// ModelConfig represents a model entry.
type ModelConfig struct {
	ID   string
	Name string
}

// ChannelConfig holds detected channel configurations.
type ChannelConfig struct {
	Telegram *TelegramConfig
	Discord  *DiscordConfig
	Slack    *SlackConfig
}

// TelegramConfig holds Telegram bot config.
type TelegramConfig struct {
	BotToken string
}

// DiscordConfig holds Discord bot config.
type DiscordConfig struct {
	BotToken string
}

// SlackConfig holds Slack bot config.
type SlackConfig struct {
	BotToken string
	AppToken string
}

// DetectAll merges config from all known sources.
// Order: OpenClaw, Nanobot, environment variables. First-found wins per provider.
func DetectAll() (*DetectedConfig, error) {
	sources := []func() (*DetectedConfig, error){
		DetectFromOpenClaw,
		DetectFromNanobot,
		DetectFromEnv,
	}

	var merged DetectedConfig
	seenProviders := make(map[string]bool)

	for _, detect := range sources {
		cfg, err := detect()
		if err != nil || cfg == nil {
			continue
		}

		// Merge agent model (first-found wins)
		if merged.AgentModel == "" && cfg.AgentModel != "" {
			merged.AgentModel = cfg.AgentModel
		}

		// Merge providers (first-found per name wins)
		for _, p := range cfg.Providers {
			if !seenProviders[p.Name] {
				merged.Providers = append(merged.Providers, p)
				seenProviders[p.Name] = true
			}
		}

		// Merge channels (first-found per channel wins)
		if merged.Channels.Telegram == nil && cfg.Channels.Telegram != nil {
			merged.Channels.Telegram = cfg.Channels.Telegram
		}
		if merged.Channels.Discord == nil && cfg.Channels.Discord != nil {
			merged.Channels.Discord = cfg.Channels.Discord
		}
		if merged.Channels.Slack == nil && cfg.Channels.Slack != nil {
			merged.Channels.Slack = cfg.Channels.Slack
		}
	}

	if len(merged.Providers) == 0 && merged.AgentModel == "" &&
		merged.Channels.Telegram == nil && merged.Channels.Discord == nil && merged.Channels.Slack == nil {
		return nil, nil
	}

	return &merged, nil
}

// DetectFromOpenClaw reads ~/.openclaw/openclaw.json.
func DetectFromOpenClaw() (*DetectedConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}

	configPath := filepath.Join(home, ".openclaw", "openclaw.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	var cfg openclawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	result := &DetectedConfig{
		AgentModel: cfg.Agents.Defaults.Model.Primary,
	}

	for name, p := range cfg.Models.Providers {
		pc := ProviderConfig{
			Name:    name,
			BaseURL: p.BaseURL,
			API:     p.API,
		}
		if p.APIKey != "" && !isEnvVarRef(p.APIKey) {
			pc.APIKey = p.APIKey
		}
		for _, m := range p.Models {
			pc.Models = append(pc.Models, ModelConfig{ID: m.ID, Name: m.Name})
		}
		result.Providers = append(result.Providers, pc)
	}

	if cfg.Channels.Telegram != nil && cfg.Channels.Telegram.BotToken != "" && !isEnvVarRef(cfg.Channels.Telegram.BotToken) {
		result.Channels.Telegram = &TelegramConfig{BotToken: cfg.Channels.Telegram.BotToken}
	}
	if cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" && !isEnvVarRef(cfg.Channels.Discord.BotToken) {
		result.Channels.Discord = &DiscordConfig{BotToken: cfg.Channels.Discord.BotToken}
	}
	if cfg.Channels.Slack != nil {
		botToken := cfg.Channels.Slack.BotToken
		appToken := cfg.Channels.Slack.AppToken
		if botToken != "" && !isEnvVarRef(botToken) {
			sc := &SlackConfig{BotToken: botToken}
			if appToken != "" && !isEnvVarRef(appToken) {
				sc.AppToken = appToken
			}
			result.Channels.Slack = sc
		}
	}

	return result, nil
}

// DetectFromNanobot reads ~/.nanobot/config.json.
func DetectFromNanobot() (*DetectedConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}

	configPath := filepath.Join(home, ".nanobot", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	var cfg nanobotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	result := &DetectedConfig{
		AgentModel: cfg.AgentModel,
	}

	for name, p := range cfg.Providers {
		pc := ProviderConfig{
			Name:    name,
			BaseURL: p.BaseURL,
		}
		if p.APIKey != "" && !isEnvVarRef(p.APIKey) {
			pc.APIKey = p.APIKey
		}
		result.Providers = append(result.Providers, pc)
	}

	if cfg.Channels.Telegram != nil && cfg.Channels.Telegram.Token != "" && !isEnvVarRef(cfg.Channels.Telegram.Token) {
		result.Channels.Telegram = &TelegramConfig{BotToken: cfg.Channels.Telegram.Token}
	}
	if cfg.Channels.Discord != nil && cfg.Channels.Discord.Token != "" && !isEnvVarRef(cfg.Channels.Discord.Token) {
		result.Channels.Discord = &DiscordConfig{BotToken: cfg.Channels.Discord.Token}
	}

	return result, nil
}

// DetectFromEnv reads well-known environment variables for API keys.
func DetectFromEnv() (*DetectedConfig, error) {
	result := &DetectedConfig{}

	envProviders := []struct {
		envVar  string
		name    string
		baseURL string
		api     string
	}{
		{"OPENAI_API_KEY", "openai", "https://api.openai.com/v1", ""},
		{"ANTHROPIC_API_KEY", "anthropic", "https://api.anthropic.com/v1", "anthropic"},
		{"OPENROUTER_API_KEY", "openrouter", "https://openrouter.ai/api/v1", ""},
	}

	for _, ep := range envProviders {
		if key := os.Getenv(ep.envVar); key != "" {
			result.Providers = append(result.Providers, ProviderConfig{
				Name:    ep.name,
				BaseURL: ep.baseURL,
				API:     ep.api,
				APIKey:  key,
			})
		}
	}

	if len(result.Providers) == 0 {
		return nil, nil
	}

	return result, nil
}

// PrintSummary prints a human-readable summary of detected config.
func PrintSummary(source string, result *DetectedConfig) {
	if result == nil {
		return
	}

	fmt.Printf("Detected configuration (%s):\n", source)
	if len(result.Providers) > 0 {
		fmt.Printf("  Providers: ")
		names := make([]string, 0, len(result.Providers))
		for _, p := range result.Providers {
			names = append(names, p.Name)
		}
		fmt.Println(strings.Join(names, ", "))
	}
	if result.AgentModel != "" {
		fmt.Printf("  Agent model: %s\n", result.AgentModel)
	}
	if result.Channels.Telegram != nil {
		fmt.Println("  Telegram: configured")
	}
	if result.Channels.Discord != nil {
		fmt.Println("  Discord: configured")
	}
	if result.Channels.Slack != nil {
		fmt.Println("  Slack: configured")
	}
}

// --- Internal types for JSON parsing ---

// openclawConfig mirrors relevant parts of ~/.openclaw/openclaw.json
type openclawConfig struct {
	Models struct {
		Providers map[string]openclawProvider `json:"providers"`
	} `json:"models"`
	Agents struct {
		Defaults struct {
			Model struct {
				Primary string `json:"primary"`
			} `json:"model"`
		} `json:"defaults"`
	} `json:"agents"`
	Channels struct {
		Telegram *struct {
			BotToken string `json:"botToken"`
		} `json:"telegram"`
		Discord *struct {
			BotToken string `json:"botToken"`
		} `json:"discord"`
		Slack *struct {
			BotToken string `json:"botToken"`
			AppToken string `json:"appToken"`
		} `json:"slack"`
	} `json:"channels"`
}

type openclawProvider struct {
	BaseURL string          `json:"baseUrl"`
	API     string          `json:"api"`
	APIKey  string          `json:"apiKey"`
	Models  []openclawModel `json:"models"`
}

type openclawModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// nanobotConfig mirrors relevant parts of ~/.nanobot/config.json
type nanobotConfig struct {
	AgentModel string                       `json:"agentModel"`
	Providers  map[string]nanobotProvider   `json:"providers"`
	Channels   struct {
		Telegram *struct {
			Token string `json:"token"`
		} `json:"telegram"`
		Discord *struct {
			Token string `json:"token"`
		} `json:"discord"`
	} `json:"channels"`
}

type nanobotProvider struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

func isEnvVarRef(s string) bool {
	return strings.Contains(s, "${")
}
