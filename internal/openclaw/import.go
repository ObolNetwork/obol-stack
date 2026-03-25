package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImportResult holds the parsed configuration from ~/.openclaw/openclaw.json
type ImportResult struct {
	Providers    []ImportedProvider
	AgentModel   string
	Channels     ImportedChannels
	WorkspaceDir string // path to ~/.openclaw/workspace/ if it exists and contains marker files
}

// ImportedProvider represents a model provider extracted from openclaw.json
type ImportedProvider struct {
	Name         string
	BaseURL      string
	API          string
	APIKey       string // literal only; empty if env-var reference
	APIKeyEnvVar string // env var name for apiKey interpolation (e.g. OLLAMA_API_KEY)
	Models       []ImportedModel
	Disabled     bool // when true, emit only enabled: false (used to override chart defaults)
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

// openclawConfig mirrors the relevant parts of ~/.openclaw/openclaw.json
type openclawConfig struct {
	Models struct {
		Providers map[string]openclawProvider `json:"providers"`
	} `json:"models"`
	Agents struct {
		Defaults struct {
			Model struct {
				Primary string `json:"primary"`
			} `json:"model"`
			Workspace string `json:"workspace"`
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

// DetectExistingConfig checks for ~/.openclaw/openclaw.json and parses it.
// Returns nil (not an error) if the file does not exist.
func DetectExistingConfig() (*ImportResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil //nolint:nilerr,nilnil // home dir unavailable; treat as no existing config
	}

	return detectExistingConfigAt(home)
}

// detectExistingConfigAt reads and parses openclaw.json from the given home directory.
// Extracted from DetectExistingConfig for testability.
func detectExistingConfigAt(home string) (*ImportResult, error) {
	configPath := filepath.Join(home, ".openclaw", "openclaw.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // file absent means no prior config; not an error
		}

		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	var cfg openclawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	result := &ImportResult{
		AgentModel: cfg.Agents.Defaults.Model.Primary,
	}

	// Detect workspace directory
	result.WorkspaceDir = detectWorkspace(home, cfg.Agents.Defaults.Workspace)

	for name, p := range cfg.Models.Providers {
		sanitized := sanitizeModelAPI(p.API)
		if p.API != "" && sanitized == "" {
			fmt.Printf("  Note: unknown API type '%s' for provider '%s', will auto-detect\n", p.API, name)
		}

		ip := ImportedProvider{
			Name:         name,
			BaseURL:      p.BaseURL,
			API:          sanitized,
			APIKeyEnvVar: defaultProviderAPIKeyEnvVar(name),
		}
		// Import either a literal key (for secret extraction) or env-var reference.
		if p.APIKey != "" && !isEnvVarRef(p.APIKey) {
			ip.APIKey = p.APIKey
		} else if p.APIKey != "" {
			if envVar, ok := extractEnvVarName(p.APIKey); ok {
				ip.APIKeyEnvVar = envVar
			} else {
				fmt.Printf("  Note: provider '%s' uses an env-var reference for its API key (will need manual configuration)\n", name)
			}
		}

		for _, m := range p.Models {
			ip.Models = append(ip.Models, ImportedModel(m))
		}

		result.Providers = append(result.Providers, ip)
	}

	if cfg.Channels.Telegram != nil && cfg.Channels.Telegram.BotToken != "" {
		if !isEnvVarRef(cfg.Channels.Telegram.BotToken) {
			result.Channels.Telegram = &ImportedTelegram{BotToken: cfg.Channels.Telegram.BotToken}
		} else {
			fmt.Printf("  Note: Telegram bot token uses env-var reference (will need manual configuration)\n")
		}
	}

	if cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
		if !isEnvVarRef(cfg.Channels.Discord.BotToken) {
			result.Channels.Discord = &ImportedDiscord{BotToken: cfg.Channels.Discord.BotToken}
		} else {
			fmt.Printf("  Note: Discord bot token uses env-var reference (will need manual configuration)\n")
		}
	}

	if cfg.Channels.Slack != nil {
		botToken := cfg.Channels.Slack.BotToken

		appToken := cfg.Channels.Slack.AppToken
		if botToken != "" && !isEnvVarRef(botToken) {
			result.Channels.Slack = &ImportedSlack{
				BotToken: botToken,
			}
			if appToken != "" && !isEnvVarRef(appToken) {
				result.Channels.Slack.AppToken = appToken
			} else if appToken != "" {
				fmt.Printf("  Note: Slack app token uses env-var reference (will need manual configuration)\n")
			}
		} else if botToken != "" {
			fmt.Printf("  Note: Slack bot token uses env-var reference (will need manual configuration)\n")
		}
	}

	return result, nil
}

// TranslateToOverlayYAML maps imported config fields to chart values YAML fragment.
// The returned string is appended to the base overlay.
func TranslateToOverlayYAML(result *ImportResult) string {
	if result == nil {
		return ""
	}

	var b strings.Builder

	if result.AgentModel != "" {
		fmt.Fprintf(&b, "openclaw:\n  agentModel: %s\n\n", result.AgentModel)
	}

	if len(result.Providers) > 0 {
		b.WriteString("models:\n")

		for _, p := range result.Providers {
			fmt.Fprintf(&b, "  %s:\n", p.Name)

			if p.Disabled {
				b.WriteString("    enabled: false\n")
				continue
			}

			b.WriteString("    enabled: true\n")

			if p.BaseURL != "" {
				fmt.Fprintf(&b, "    baseUrl: %s\n", p.BaseURL)
			}
			// Always emit api to override any stale base chart value.
			// Empty string makes the Helm template omit it from JSON,
			// letting OpenClaw auto-detect the protocol.
			if p.API != "" {
				fmt.Fprintf(&b, "    api: %s\n", p.API)
			} else {
				b.WriteString("    api: \"\"\n")
			}

			if p.APIKeyEnvVar != "" {
				fmt.Fprintf(&b, "    apiKeyEnvVar: %s\n", p.APIKeyEnvVar)
			}

			if len(p.Models) > 0 {
				b.WriteString("    models:\n")

				for _, m := range p.Models {
					fmt.Fprintf(&b, "      - id: %s\n", m.ID)

					if m.Name != "" {
						fmt.Fprintf(&b, "        name: %s\n", m.Name)
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
		}

		if result.Channels.Discord != nil {
			b.WriteString("  discord:\n")
			b.WriteString("    enabled: true\n")
		}

		if result.Channels.Slack != nil {
			b.WriteString("  slack:\n")
			b.WriteString("    enabled: true\n")
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

	fmt.Println("Detected existing OpenClaw installation (~/.openclaw/):")

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

	if result.WorkspaceDir != "" {
		files := detectWorkspaceFiles(result.WorkspaceDir)
		fmt.Printf("  Workspace: %s (%s)\n", result.WorkspaceDir, strings.Join(files, ", "))
	}
}

// workspaceMarkers are files that indicate a valid OpenClaw workspace
var workspaceMarkers = []string{"SOUL.md", "AGENTS.md", "IDENTITY.md"}

// detectWorkspace checks for an OpenClaw workspace directory and returns
// its path if it exists and contains at least one marker file.
func detectWorkspace(home, configWorkspace string) string {
	// Use custom workspace path from config if set
	wsDir := configWorkspace
	if wsDir == "" {
		wsDir = filepath.Join(home, ".openclaw", "workspace")
	}

	info, err := os.Stat(wsDir)
	if err != nil || !info.IsDir() {
		return ""
	}

	// Verify at least one marker file exists
	for _, marker := range workspaceMarkers {
		if _, err := os.Stat(filepath.Join(wsDir, marker)); err == nil {
			return wsDir
		}
	}

	// Directory exists but has no marker files
	fmt.Printf("  Note: workspace at %s has no marker files (SOUL.md, AGENTS.md, IDENTITY.md)\n", wsDir)

	return ""
}

// detectWorkspaceFiles returns the names of workspace files that exist
func detectWorkspaceFiles(wsDir string) []string {
	candidates := []string{
		"SOUL.md", "AGENTS.md", "IDENTITY.md", "USER.md",
		"TOOLS.md", "MEMORY.md",
	}

	var found []string

	for _, name := range candidates {
		if _, err := os.Stat(filepath.Join(wsDir, name)); err == nil {
			found = append(found, name)
		}
	}
	// Check for memory/ directory
	if info, err := os.Stat(filepath.Join(wsDir, "memory")); err == nil && info.IsDir() {
		found = append(found, "memory/")
	}

	return found
}

// validModelAPIs is the set of values accepted by OpenClaw's ModelApiSchema (Zod enum).
// Any other value will be rejected at startup. When the api field is omitted,
// OpenClaw auto-detects the protocol from the provider name / baseUrl.
var validModelAPIs = map[string]bool{
	"openai-completions":      true,
	"openai-responses":        true,
	"anthropic-messages":      true,
	"google-generative-ai":    true,
	"github-copilot":          true,
	"bedrock-converse-stream": true,
}

// sanitizeModelAPI returns api unchanged if it is a valid OpenClaw ModelApi enum
// value, or "" (omit) if it is unrecognised. This prevents invalid values
// imported from ~/.openclaw/openclaw.json from crashing the gateway.
func sanitizeModelAPI(api string) string {
	if validModelAPIs[api] {
		return api
	}

	return ""
}

func defaultProviderAPIKeyEnvVar(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "ollama":
		return "OLLAMA_API_KEY"
	default:
		var out []rune

		for _, r := range strings.ToUpper(provider) {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				out = append(out, r)
			} else {
				out = append(out, '_')
			}
		}

		s := strings.Trim(string(out), "_")
		if s == "" {
			return "MODEL_API_KEY"
		}

		return s + "_API_KEY"
	}
}

func extractEnvVarName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return "", false
	}

	body := strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}")
	if body == "" {
		return "", false
	}

	if i := strings.Index(body, ":"); i > 0 {
		body = body[:i]
	}

	return body, body != ""
}

// isEnvVarRef returns true if the value looks like an environment variable reference (${...})
func isEnvVarRef(s string) bool {
	return strings.Contains(s, "${")
}
