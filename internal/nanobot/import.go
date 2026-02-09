package nanobot

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/providers"
)

// TranslateToOverlayYAML converts detected config to Nanobot chart values format.
func TranslateToOverlayYAML(result *providers.DetectedConfig) string {
	if result == nil {
		return ""
	}

	var b strings.Builder

	if result.AgentModel != "" {
		b.WriteString(fmt.Sprintf("nanobot:\n  agentModel: %s\n\n", result.AgentModel))
	}

	if len(result.Providers) > 0 {
		b.WriteString("providers:\n")
		for _, p := range result.Providers {
			b.WriteString(fmt.Sprintf("  %s:\n", p.Name))
			b.WriteString("    enabled: true\n")
			if p.BaseURL != "" {
				b.WriteString(fmt.Sprintf("    baseUrl: %s\n", p.BaseURL))
			}
			if p.APIKey != "" {
				b.WriteString(fmt.Sprintf("    apiKeyValue: %s\n", p.APIKey))
			}
		}
		b.WriteString("\n")
	}

	// Channels
	hasChannels := result.Channels.Telegram != nil || result.Channels.Discord != nil
	if hasChannels {
		b.WriteString("channels:\n")
		if result.Channels.Telegram != nil {
			b.WriteString("  telegram:\n")
			b.WriteString("    enabled: true\n")
			b.WriteString(fmt.Sprintf("    token: %s\n", result.Channels.Telegram.BotToken))
		}
		if result.Channels.Discord != nil {
			b.WriteString("  discord:\n")
			b.WriteString("    enabled: true\n")
			b.WriteString(fmt.Sprintf("    token: %s\n", result.Channels.Discord.BotToken))
		}
		b.WriteString("\n")
	}

	return b.String()
}
