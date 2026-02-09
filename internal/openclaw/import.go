package openclaw

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/providers"
)

// ImportResult holds the parsed configuration from ~/.openclaw/openclaw.json.
// This is an OpenClaw-specific wrapper around providers.DetectedConfig.
type ImportResult struct {
	Providers  []ImportedProvider
	AgentModel string
	Channels   ImportedChannels
}

// ImportedProvider represents a model provider extracted from openclaw.json
type ImportedProvider struct {
	Name    string
	BaseURL string
	API     string
	APIKey  string // literal only; empty if env-var reference
	Models  []ImportedModel
}

// ImportedModel represents a model entry
type ImportedModel struct {
	ID   string
	Name string
}

// ImportedChannels holds detected channel configurations
type ImportedChannels struct {
	Telegram *ImportedTelegram
	Discord  *ImportedDiscord
	Slack    *ImportedSlack
}

// ImportedTelegram holds Telegram bot config
type ImportedTelegram struct {
	BotToken string
}

// ImportedDiscord holds Discord bot config
type ImportedDiscord struct {
	BotToken string
}

// ImportedSlack holds Slack bot config
type ImportedSlack struct {
	BotToken string
	AppToken string
}

// DetectExistingConfig checks for configuration from all known sources.
// Uses providers.DetectAll() to merge ~/.openclaw, ~/.nanobot, and env vars.
// Returns nil (not an error) if no config is found.
func DetectExistingConfig() (*ImportResult, error) {
	detected, err := providers.DetectAll()
	if err != nil {
		return nil, err
	}
	if detected == nil {
		return nil, nil
	}
	return fromDetectedConfig(detected), nil
}

// fromDetectedConfig converts providers.DetectedConfig to ImportResult.
func fromDetectedConfig(cfg *providers.DetectedConfig) *ImportResult {
	result := &ImportResult{
		AgentModel: cfg.AgentModel,
	}

	for _, p := range cfg.Providers {
		ip := ImportedProvider{
			Name:    p.Name,
			BaseURL: p.BaseURL,
			API:     p.API,
			APIKey:  p.APIKey,
		}
		for _, m := range p.Models {
			ip.Models = append(ip.Models, ImportedModel{ID: m.ID, Name: m.Name})
		}
		result.Providers = append(result.Providers, ip)
	}

	if cfg.Channels.Telegram != nil {
		result.Channels.Telegram = &ImportedTelegram{BotToken: cfg.Channels.Telegram.BotToken}
	}
	if cfg.Channels.Discord != nil {
		result.Channels.Discord = &ImportedDiscord{BotToken: cfg.Channels.Discord.BotToken}
	}
	if cfg.Channels.Slack != nil {
		result.Channels.Slack = &ImportedSlack{
			BotToken: cfg.Channels.Slack.BotToken,
			AppToken: cfg.Channels.Slack.AppToken,
		}
	}

	return result
}

// TranslateToOverlayYAML maps imported config fields to chart values YAML fragment.
// The returned string is appended to the base overlay.
func TranslateToOverlayYAML(result *ImportResult) string {
	if result == nil {
		return ""
	}

	var b strings.Builder

	if result.AgentModel != "" {
		b.WriteString(fmt.Sprintf("openclaw:\n  agentModel: %s\n\n", result.AgentModel))
	}

	if len(result.Providers) > 0 {
		b.WriteString("models:\n")
		for _, p := range result.Providers {
			b.WriteString(fmt.Sprintf("  %s:\n", p.Name))
			b.WriteString("    enabled: true\n")
			if p.BaseURL != "" {
				b.WriteString(fmt.Sprintf("    baseUrl: %s\n", p.BaseURL))
			}
			if p.API != "" {
				b.WriteString(fmt.Sprintf("    api: %s\n", p.API))
			}
			if p.APIKey != "" {
				b.WriteString(fmt.Sprintf("    apiKeyValue: %s\n", p.APIKey))
			}
			if len(p.Models) > 0 {
				b.WriteString("    models:\n")
				for _, m := range p.Models {
					b.WriteString(fmt.Sprintf("      - id: %s\n", m.ID))
					if m.Name != "" {
						b.WriteString(fmt.Sprintf("        name: %s\n", m.Name))
					}
				}
			}
		}
		b.WriteString("\n")
	}

	// Channels
	hasChannels := result.Channels.Telegram != nil || result.Channels.Discord != nil || result.Channels.Slack != nil
	if hasChannels {
		b.WriteString("channels:\n")
		if result.Channels.Telegram != nil {
			b.WriteString("  telegram:\n")
			b.WriteString("    enabled: true\n")
			b.WriteString(fmt.Sprintf("    botToken: %s\n", result.Channels.Telegram.BotToken))
		}
		if result.Channels.Discord != nil {
			b.WriteString("  discord:\n")
			b.WriteString("    enabled: true\n")
			b.WriteString(fmt.Sprintf("    botToken: %s\n", result.Channels.Discord.BotToken))
		}
		if result.Channels.Slack != nil {
			b.WriteString("  slack:\n")
			b.WriteString("    enabled: true\n")
			b.WriteString(fmt.Sprintf("    botToken: %s\n", result.Channels.Slack.BotToken))
			if result.Channels.Slack.AppToken != "" {
				b.WriteString(fmt.Sprintf("    appToken: %s\n", result.Channels.Slack.AppToken))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// PrintImportSummary prints a human-readable summary of detected config
func PrintImportSummary(result *ImportResult) {
	if result == nil {
		return
	}

	fmt.Println("Detected existing configuration:")
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
