package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

func modelCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "model",
		Usage: "Manage LLM providers (LiteLLM gateway)",
		Commands: []*cli.Command{
			modelSetupCommand(cfg),
			modelStatusCommand(cfg),
			modelTokenCommand(cfg),
			modelSyncCommand(cfg),
			modelPullCommand(),
			modelListCommand(cfg),
			modelPreferCommand(cfg),
			modelDiscoverCommand(),
			modelRemoveCommand(cfg),
		},
	}
}

func modelTokenCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "token",
		Usage: "Print the LiteLLM master token for API access",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			token, err := model.GetMasterKey(cfg)
			if err != nil {
				return err
			}

			if u.IsJSON() {
				return u.JSON(map[string]string{"token": token})
			}

			u.Print(token)
			return nil
		},
	}
}

func modelSetupCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Configure an LLM provider in the LiteLLM gateway",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "provider",
				Usage: "Provider name: anthropic, openai, or ollama",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "API key for the provider",
				Sources: cli.EnvVars("LLM_API_KEY"),
			},
			&cli.StringSliceFlag{
				Name:  "model",
				Usage: "Model(s) to configure (e.g. claude-sonnet-4-5-20250929, gpt-4o)",
			},
		},
		Commands: []*cli.Command{
			modelSetupCustomCommand(cfg),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			provider := cmd.String("provider")
			apiKey := cmd.String("api-key")
			models := cmd.StringSlice("model")

			// Interactive mode if flags not provided
			if provider == "" {
				creds := detectCredentials()
				providers, _ := model.GetAvailableProviders(cfg)

				options := make([]string, len(providers))
				for i, p := range providers {
					label := fmt.Sprintf("%s (%s)", p.Name, p.ID)
					if det, ok := creds[p.ID]; ok {
						label += " — detected: " + det.source
					}

					options[i] = label
				}

				idx, err := u.Select("Select a provider:", options, 0)
				if err != nil {
					return err
				}

				provider = providers[idx].ID

				// If a credential was detected for the chosen provider, offer to use it
				if det, ok := creds[provider]; ok && det.key != "" && apiKey == "" {
					u.Infof("%s API key detected (%s)", providers[idx].Name, det.source)

					if u.Confirm("Use detected credential?", true) {
						apiKey = det.key
					}
				}
			}

			// Provider-specific flow
			switch provider {
			case "ollama":
				return setupOllama(cfg, u, models)
			case "anthropic", "openai":
				return setupCloudProvider(cfg, u, provider, apiKey, models)
			default:
				return fmt.Errorf("unknown provider %q — use anthropic, openai, or ollama", provider)
			}
		},
	}
}

func setupOllama(cfg *config.Config, u *ui.UI, models []string) error {
	if len(models) == 0 {
		// Diagnostic: check Ollama connectivity
		u.Info("Checking Ollama connectivity...")

		ollamaModels, err := model.ListOllamaModels()
		if err != nil {
			u.Errorf("Ollama not reachable")
			u.Print("")
			u.Print("  Hint: Is Ollama running? Try: ollama serve")
			u.Print("  Hint: Using a custom host? Set OLLAMA_HOST=http://your-host:port")
			u.Print("  Hint: Install from https://ollama.ai")

			return fmt.Errorf("ollama is not running: %w", err)
		}

		u.Success("Ollama is reachable")

		if len(ollamaModels) == 0 {
			u.Warn("No models pulled in Ollama")
			u.Print("")
			u.Print("  Hint: Pull a model with: ollama pull qwen3.5:4b  (or qwen3.6:27b on hosts with ≥32GB RAM)")
			u.Print("  Hint: Or run: obol model pull")

			return errors.New("ollama is running but has no models")
		}

		u.Successf("Found %d pulled model(s)", len(ollamaModels))

		for _, m := range ollamaModels {
			name := m.Name
			if before, ok := strings.CutSuffix(name, ":latest"); ok {
				name = before
			}

			models = append(models, name)
		}

		u.Infof("Models: %s", strings.Join(models, ", "))
	}

	if err := model.ConfigureLiteLLM(cfg, u, "ollama", "", models); err != nil {
		return err
	}

	u.Successf("Ollama configured. To change later, run: obol model setup (or obol model remove <name>)")

	return syncAgentModels(cfg, u)
}

func setupCloudProvider(cfg *config.Config, u *ui.UI, provider, apiKey string, models []string) error {
	if apiKey == "" {
		var err error

		info := providerInfo(provider)

		apiKey, err = u.SecretInput(fmt.Sprintf("%s API key (%s)", info.Name, info.EnvVar))
		if err != nil {
			return err
		}

		if apiKey == "" {
			return errors.New("API key is required")
		}
	}

	if len(models) == 0 {
		// Per-provider defaults — kept in sync with what the providers
		// document as their current chat-tuned flagship. Bumping these is a
		// small follow-up PR when frontier models drop, and it isolates the
		// "what's good today" maintenance to one place.
		var defaultModel string
		switch provider {
		case "anthropic":
			defaultModel = "claude-sonnet-4-6"
		case "openai":
			defaultModel = "gpt-5.5"
		}

		// Interactive: let the user override the default with a free-text
		// entry. Non-interactive (no TTY): silently use the default — the
		// caller can always pass --model to be explicit.
		chosen := defaultModel
		if defaultModel != "" && u.IsTTY() && !u.IsJSON() {
			input, err := u.Input(fmt.Sprintf("Model for %s", provider), defaultModel)
			if err != nil {
				return err
			}
			if strings.TrimSpace(input) != "" {
				chosen = strings.TrimSpace(input)
			}
		}
		if chosen != "" {
			models = []string{chosen}
		}
	}

	if err := model.ConfigureLiteLLM(cfg, u, provider, apiKey, models); err != nil {
		u.Print("")
		u.Print("  Hint: Configuration stored in: litellm-config ConfigMap (llm namespace)")

		return err
	}

	u.Print("")
	u.Successf("Model configured. To change later, run: obol model setup (or obol model remove <name>)")

	return syncAgentModels(cfg, u)
}

// syncAgentModels re-renders the stack-managed Hermes default agent from the
// current LiteLLM model inventory.
func syncAgentModels(cfg *config.Config, u *ui.UI) error {
	return hermes.SyncDefaultModels(cfg, u)
}

func modelSyncCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sync",
		Usage: "Sync LiteLLM model list to the stack-managed Hermes agent",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			u.Info("Reading model list from LiteLLM...")

			return syncAgentModels(cfg, u)
		},
	}
}

func modelSetupCustomCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "custom",
		Usage: "Add a custom OpenAI-compatible endpoint (validates before adding)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "endpoint", Usage: "Full base URL (e.g. http://host:8000/v1)", Required: true},
			&cli.StringFlag{Name: "model", Usage: "Model identifier at the endpoint — this is also the LiteLLM model_name the agent will call", Required: true},
			&cli.StringFlag{Name: "api-key", Usage: "API key (optional, some endpoints don't require it)"},
			&cli.BoolFlag{Name: "disable-thinking", Usage: "Forward chat_template_kwargs.enable_thinking=false to this endpoint on every request"},
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			endpoint := cmd.String("endpoint")
			modelName := cmd.String("model")
			apiKey := cmd.String("api-key")

			options := model.CustomEndpointOptions{
				DisableThinking: cmd.Bool("disable-thinking"),
			}
			if err := model.AddCustomEndpointWithOptions(cfg, u, endpoint, modelName, apiKey, options); err != nil {
				return err
			}

			if cmd.Bool("no-sync") {
				return nil
			}
			return syncAgentModels(cfg, u)
		},
	}
}

// modelStatusResult is the JSON-serialisable result for `model status`.
type modelStatusResult struct {
	Providers  []modelStatusProvider `json:"providers"`
	Discovered []discoverProvider    `json:"discovered,omitempty"`
}

type modelStatusProvider struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	APIKey  string   `json:"api_key"` // "set", "missing", or "n/a"
	Models  []string `json:"models"`
	EnvVar  string   `json:"env_var,omitempty"`
}

func modelStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show LiteLLM gateway provider status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			status, err := model.GetProviderStatus(cfg)
			if err != nil {
				return err
			}

			providers := make([]string, 0, len(status))
			for name := range status {
				providers = append(providers, name)
			}

			sort.Strings(providers)

			scanCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			discovered, _ := model.DiscoverLocalProviders(scanCtx)

			if u.IsJSON() {
				result := modelStatusResult{
					Discovered: discoveredProvidersToJSON(discovered),
				}
				for _, name := range providers {
					s := status[name]
					key := "n/a"
					if s.EnvVar != "" {
						if s.HasAPIKey {
							key = "set"
						} else {
							key = "missing"
						}
					}
					result.Providers = append(result.Providers, modelStatusProvider{
						Name:    name,
						Enabled: s.Enabled,
						APIKey:  key,
						Models:  s.Models,
						EnvVar:  s.EnvVar,
					})
				}
				return u.JSON(result)
			}

			u.Bold("LiteLLM gateway providers:")
			u.Blank()
			u.Printf("  %-20s %-8s %-10s %-10s %s", "PROVIDER", "ENABLED", "API KEY", "MODELS", "ENV VAR")

			for _, name := range providers {
				s := status[name]
				key := "n/a"

				if s.EnvVar != "" {
					if s.HasAPIKey {
						key = "set"
					} else {
						key = "missing"
					}
				}

				modelCount := strconv.Itoa(len(s.Models))
				if len(s.Models) == 0 {
					modelCount = "-"
				}

				u.Printf("  %-20s %-8t %-10s %-10s %s", name, s.Enabled, key, modelCount, s.EnvVar)
			}

			if len(discovered) > 0 {
				u.Blank()
				u.Bold(fmt.Sprintf("Discovered local inference servers (%d):", len(discovered)))
				for _, p := range discovered {
					noun := "models"
					if len(p.Entries) == 1 {
						noun = "model"
					}
					u.Printf("  %-20s %s  (%d %s)", p.Label, p.HostEndpoint, len(p.Entries), noun)
				}
				u.Dim("Run 'obol model discover' for the full model list.")
			}

			u.Blank()
			u.Dim("Run 'obol model setup' to configure a provider.")
			u.Dim("Run 'obol model setup custom' to add a custom endpoint.")

			return nil
		},
	}
}

func modelPullCommand() *cli.Command {
	return &cli.Command{
		Name:      "pull",
		Usage:     "Pull an Ollama model to the local machine",
		ArgsUsage: "[model]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			modelName := cmd.Args().First()

			if modelName == "" {
				var err error
				modelName, err = promptModelPull(u)
				if err != nil {
					return err
				}
			}

			u.Infof("Pulling model: %s", modelName)
			u.Blank()
			if err := model.PullOllamaModel(modelName); err != nil {
				return err
			}
			u.Blank()
			u.Successf("Model %s is ready.", modelName)
			return nil
		},
	}
}

// modelListResult is the JSON-serialisable result for `model list`.
type modelListResult struct {
	Local   []modelListLocal   `json:"local"`
	Gateway []modelListGateway `json:"gateway,omitempty"`
}

type modelListLocal struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type modelListGateway struct {
	Provider string   `json:"provider"`
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models"`
}

func modelListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List pulled Ollama models and cloud provider status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			if u.IsJSON() {
				result := modelListResult{}

				if models, err := model.ListOllamaModels(); err == nil {
					for _, m := range models {
						result.Local = append(result.Local, modelListLocal{
							Name: m.Name,
							Size: m.Size,
						})
					}
				}

				if providerStatus, err := model.GetProviderStatus(cfg); err == nil {
					providers := make([]string, 0, len(providerStatus))
					for name := range providerStatus {
						providers = append(providers, name)
					}
					sort.Strings(providers)
					for _, name := range providers {
						s := providerStatus[name]
						result.Gateway = append(result.Gateway, modelListGateway{
							Provider: name,
							Enabled:  s.Enabled,
							Models:   s.Models,
						})
					}
				}

				return u.JSON(result)
			}

			// List local Ollama models
			models, err := model.ListOllamaModels()
			if err != nil {
				u.Warnf("Local models (Ollama): not available (%s)", err)
			} else if len(models) == 0 {
				u.Info("Local models (Ollama): none pulled")
				u.Blank()
				u.Info("  Pull a model with: obol model pull")
			} else {
				u.Info("Local models (Ollama):")
				u.Blank()
				u.Printf("  %-35s %s", "NAME", "SIZE")
				for _, m := range models {
					u.Printf("  %-35s %s", m.Name, model.FormatBytes(m.Size))
				}
			}
			u.Blank()

			// Show LiteLLM configured models
			providerStatus, err := model.GetProviderStatus(cfg)
			if err != nil {
				u.Info("LiteLLM gateway: cluster not running")
				u.Blank()
				u.Info("  Run 'obol stack up' then 'obol model setup' to configure a provider.")
			} else {
				providers := make([]string, 0, len(providerStatus))
				for name := range providerStatus {
					providers = append(providers, name)
				}

				sort.Strings(providers)

				u.Info("LiteLLM gateway models:")
				u.Blank()
				u.Printf("  %-20s %-10s %s", "PROVIDER", "STATUS", "MODELS")
				for _, name := range providers {
					s := providerStatus[name]

					status := "disabled"
					if s.Enabled {
						status = "enabled"
					}

					modelList := strings.Join(s.Models, ", ")
					if modelList == "" {
						modelList = "-"
					}
					u.Printf("  %-20s %-10s %s", name, status, modelList)
				}
			}

			return nil
		},
	}
}

func modelPreferCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "prefer",
		Usage:     "Pull one or more models to the head of the LiteLLM model_list (the head becomes the agent's primary)",
		ArgsUsage: "<model-name> [<model-name> ...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			names := cmd.Args().Slice()
			if len(names) == 0 {
				return errors.New("at least one model name is required\n\nUsage: obol model prefer <model-name> [<model-name> ...]\n\nList configured models with: obol model list")
			}

			if err := model.PreferModels(cfg, u, names); err != nil {
				return err
			}

			if cmd.Bool("no-sync") {
				return nil
			}
			return syncAgentModels(cfg, u)
		},
	}
}

func modelRemoveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove a model from the LiteLLM gateway",
		ArgsUsage: "<model-name>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			modelName := cmd.Args().First()
			if modelName == "" {
				return errors.New("model name is required\n\nUsage: obol model remove <model-name>\n\nList configured models with: obol model list")
			}

			if err := model.RemoveModel(cfg, u, modelName); err != nil {
				return err
			}

			if cmd.Bool("no-sync") {
				return nil
			}
			return syncAgentModels(cfg, u)
		},
	}
}

func providerInfo(id string) model.ProviderInfo {
	providers, _ := model.GetAvailableProviders(nil)
	for _, p := range providers {
		if p.ID == id {
			return p
		}
	}

	return model.ProviderInfo{ID: id, Name: id}
}

// detectedCredential describes a credential found in the environment.
type detectedCredential struct {
	key    string // the actual API key value (empty for Ollama)
	source string // human-readable description of where it was found
}

// detectCredentials checks the environment for existing provider credentials.
// It returns a map of provider ID to detected credential info. Only providers
// with a detected credential appear in the map.
func detectCredentials() map[string]detectedCredential {
	creds := make(map[string]detectedCredential)

	// Anthropic: check ANTHROPIC_API_KEY, then CLAUDE_CODE_OAUTH_TOKEN
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		creds["anthropic"] = detectedCredential{key: key, source: "ANTHROPIC_API_KEY"}
	} else if key := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); key != "" {
		creds["anthropic"] = detectedCredential{key: key, source: "CLAUDE_CODE_OAUTH_TOKEN"}
	}

	// OpenAI: check OPENAI_API_KEY
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		creds["openai"] = detectedCredential{key: key, source: "OPENAI_API_KEY"}
	}

	// Ollama: check if reachable with models
	if ollamaModels, err := model.ListOllamaModels(); err == nil && len(ollamaModels) > 0 {
		creds["ollama"] = detectedCredential{
			source: fmt.Sprintf("%d model(s) available", len(ollamaModels)),
		}
	}

	return creds
}

// promptModelPull interactively asks the user which Ollama model to pull.
// When the UI is non-interactive (piped, CI, or JSON mode), it returns an
// error instructing the user to specify --model via flag.
func promptModelPull(u *ui.UI) (string, error) {
	if !u.IsTTY() || u.IsJSON() {
		return "", fmt.Errorf("model name required: use positional arg (obol model pull <model>)")
	}

	suggestions := []string{
		"qwen3.6:27b              (17 GB) — High-quality general-purpose (recommended, needs ≥32GB RAM)",
		"qwen3.6:27b-coding-mxfp8 (31 GB) — Code generation (Qwen3.6, MXFP8 quant)",
		"qwen3.5:9b               (6.6 GB) — Validated baseline; fits on most laptops",
		"qwen3.5:4b               (3.4 GB) — Smallest current Qwen, low-RAM laptops",
		"deepseek-r1:8b           (4.9 GB) — Reasoning",
		"gemma3:4b                (3.3 GB) — Lightweight, multilingual",
		"Other (enter name)",
	}
	modelNames := []string{
		"qwen3.6:27b",
		"qwen3.6:27b-coding-mxfp8",
		"qwen3.5:9b",
		"qwen3.5:4b",
		"deepseek-r1:8b",
		"gemma3:4b",
	}

	idx, err := u.Select("Select a model to pull:", suggestions, 0)
	if err != nil {
		return "", err
	}

	if idx < len(modelNames) {
		return modelNames[idx], nil
	}

	// Custom model name
	name, err := u.Input("Model name (e.g. mistral:7b)", "")
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("model name is required")
	}

	return name, nil
}

type discoverProvider struct {
	Label           string   `json:"label"`
	ServerType      string   `json:"server_type"`
	HostEndpoint    string   `json:"host_endpoint"`
	ClusterEndpoint string   `json:"cluster_endpoint"`
	Models          []string `json:"models"`
}

type discoverResult struct {
	Providers []discoverProvider `json:"providers"`
}

func discoveredProvidersToJSON(discovered []model.DiscoveredProvider) []discoverProvider {
	if len(discovered) == 0 {
		return nil
	}
	out := make([]discoverProvider, 0, len(discovered))
	for _, p := range discovered {
		names := make([]string, 0, len(p.Entries))
		for _, e := range p.Entries {
			names = append(names, e.ModelName)
		}
		out = append(out, discoverProvider{
			Label:           p.Label,
			ServerType:      p.ServerType,
			HostEndpoint:    p.HostEndpoint,
			ClusterEndpoint: p.ClusterEndpoint,
			Models:          names,
		})
	}
	return out
}

func modelDiscoverCommand() *cli.Command {
	return &cli.Command{
		Name:  "discover",
		Usage: "Detect other local inference servers (LM Studio, llama.cpp, vLLM, etc) and their models.",
		Description: "Read-only. Does not modify the existing cluster. This discovery runs every `obol stack up`.\n" +
			"Set OBOL_DISABLE_LOCAL_MODEL_DISCOVERY=true to skip local inference server auto-detection every startup.\n" +
			"Set OBOL_LOCAL_MODEL_DISCOVERY_PORTS=port[:label],... to manually add custom local inference servers.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			discovered, err := model.DiscoverLocalProviders(scanCtx)
			if err != nil {
				return fmt.Errorf("discovery scan failed: %w", err)
			}

			if u.IsJSON() {
				return u.JSON(discoverResult{Providers: discoveredProvidersToJSON(discovered)})
			}

			if len(discovered) == 0 {
				u.Info("No local OpenAI-compatible inference servers detected on well-known ports.")
				u.Blank()
				u.Dim("  Hint: start vLLM/sglang on :8000, llama.cpp on :8080, LM Studio on :1234,")
				u.Dim("  or add a custom port via OBOL_LOCAL_MODEL_DISCOVERY_PORTS=9000:vllm")
				return nil
			}

			u.Infof("Discovered %d local inference server(s):", len(discovered))
			for _, p := range discovered {
				u.Blank()
				u.Bold(fmt.Sprintf("  %s  (%s → %s)", p.Label, p.HostEndpoint, p.ClusterEndpoint))
				for _, e := range p.Entries {
					u.Print(fmt.Sprintf("    - %s", e.ModelName))
				}
			}
			u.Blank()
			u.Dim("  These are registered automatically on `obol stack up`. Disable with OBOL_DISABLE_LOCAL_MODEL_DISCOVERY=true.")
			return nil
		},
	}
}
