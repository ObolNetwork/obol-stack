package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
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
			modelPullCommand(),
			modelListCommand(cfg),
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
				providers, _ := model.GetAvailableProviders(cfg)
				options := make([]string, len(providers))
				for i, p := range providers {
					options[i] = fmt.Sprintf("%s (%s)", p.Name, p.ID)
				}

				idx, err := u.Select("Select a provider:", options, 0)
				if err != nil {
					return err
				}
				provider = providers[idx].ID
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
		// Discover from running Ollama
		ollamaModels, err := model.ListOllamaModels()
		if err != nil {
			return fmt.Errorf("Ollama is not running: %w\n\nInstall from https://ollama.ai and try again", err)
		}
		if len(ollamaModels) == 0 {
			return fmt.Errorf("Ollama is running but has no models. Pull one first:\n  ollama pull qwen3.5:35b")
		}
		for _, m := range ollamaModels {
			name := m.Name
			if strings.HasSuffix(name, ":latest") {
				name = strings.TrimSuffix(name, ":latest")
			}
			models = append(models, name)
		}
		u.Infof("Found %d Ollama model(s): %s", len(models), strings.Join(models, ", "))
	}

	if err := model.ConfigureLiteLLM(cfg, u, "ollama", "", models); err != nil {
		return err
	}
	return syncOpenClawModels(cfg, u)
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
			return fmt.Errorf("API key is required")
		}
	}

	if len(models) == 0 {
		// Sensible defaults
		switch provider {
		case "anthropic":
			models = []string{"claude-sonnet-4-5-20250929"}
		case "openai":
			models = []string{"gpt-4o"}
		}
	}

	if err := model.ConfigureLiteLLM(cfg, u, provider, apiKey, models); err != nil {
		return err
	}
	return syncOpenClawModels(cfg, u)
}

// syncOpenClawModels reads the full LiteLLM model list and updates all
// deployed OpenClaw instances so their "openai" provider (LiteLLM gateway)
// model list stays in sync. This prevents OpenClaw from trying to use
// native provider routing for models it discovers but doesn't recognise.
func syncOpenClawModels(cfg *config.Config, u *ui.UI) error {
	allModels, err := model.GetConfiguredModels(cfg)
	if err != nil {
		u.Warnf("Could not read LiteLLM model list: %v", err)
		return nil // non-fatal
	}
	return openclaw.SyncOverlayModels(cfg, allModels, u)
}

func modelSetupCustomCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "custom",
		Usage: "Add a custom OpenAI-compatible endpoint (validates before adding)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "name", Usage: "Short name for the endpoint (e.g. my-vllm)", Required: true},
			&cli.StringFlag{Name: "endpoint", Usage: "Full base URL (e.g. http://host:8000/v1)", Required: true},
			&cli.StringFlag{Name: "model", Usage: "Model name at the endpoint", Required: true},
			&cli.StringFlag{Name: "api-key", Usage: "API key (optional, some endpoints don't require it)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			name := cmd.String("name")
			endpoint := cmd.String("endpoint")
			modelName := cmd.String("model")
			apiKey := cmd.String("api-key")

			if err := model.AddCustomEndpoint(cfg, u, name, endpoint, modelName, apiKey); err != nil {
				return err
			}
			return syncOpenClawModels(cfg, u)
		},
	}
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
				modelCount := fmt.Sprintf("%d", len(s.Models))
				if len(s.Models) == 0 {
					modelCount = "-"
				}
				u.Printf("  %-20s %-8t %-10s %-10s %s", name, s.Enabled, key, modelCount, s.EnvVar)
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
			modelName := cmd.Args().First()

			if modelName == "" {
				var err error
				modelName, err = promptModelPull()
				if err != nil {
					return err
				}
			}

			fmt.Printf("Pulling model: %s\n\n", modelName)
			if err := model.PullOllamaModel(modelName); err != nil {
				return err
			}
			fmt.Printf("\nModel %s is ready.\n", modelName)
			return nil
		},
	}
}

func modelListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List pulled Ollama models and cloud provider status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// List local Ollama models
			models, err := model.ListOllamaModels()
			if err != nil {
				fmt.Printf("Local models (Ollama): not available (%s)\n", err)
			} else if len(models) == 0 {
				fmt.Println("Local models (Ollama): none pulled")
				fmt.Println()
				fmt.Println("  Pull a model with: obol model pull")
			} else {
				fmt.Println("Local models (Ollama):")
				fmt.Println()
				fmt.Printf("  %-35s %s\n", "NAME", "SIZE")
				for _, m := range models {
					fmt.Printf("  %-35s %s\n", m.Name, model.FormatBytes(m.Size))
				}
			}
			fmt.Println()

			// Show LiteLLM configured models
			providerStatus, err := model.GetProviderStatus(cfg)
			if err != nil {
				fmt.Println("LiteLLM gateway: cluster not running")
				fmt.Println()
				fmt.Println("  Run 'obol stack up' then 'obol model setup' to configure a provider.")
			} else {
				providers := make([]string, 0, len(providerStatus))
				for name := range providerStatus {
					providers = append(providers, name)
				}
				sort.Strings(providers)

				fmt.Println("LiteLLM gateway models:")
				fmt.Println()
				fmt.Printf("  %-20s %-10s %s\n", "PROVIDER", "STATUS", "MODELS")
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
					fmt.Printf("  %-20s %-10s %s\n", name, status, modelList)
				}
			}

			return nil
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

// promptModelPull interactively asks the user which Ollama model to pull.
func promptModelPull() (string, error) {
	type suggestion struct {
		name string
		size string
		desc string
	}
	suggestions := []suggestion{
		{"llama3.2:3b", "2.0 GB", "Fast, general-purpose"},
		{"qwen2.5-coder:7b", "4.7 GB", "Code generation"},
		{"deepseek-r1:8b", "4.9 GB", "Reasoning"},
		{"gemma3:4b", "3.3 GB", "Lightweight, multilingual"},
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Popular models:")
	fmt.Println()
	for i, s := range suggestions {
		fmt.Printf("  [%d] %-25s (%s) — %s\n", i+1, s.name, s.size, s.desc)
	}
	fmt.Printf("  [%d] Other (enter name)\n", len(suggestions)+1)
	fmt.Printf("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	idx := 0
	if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil || idx < 1 || idx > len(suggestions)+1 {
		return "", fmt.Errorf("invalid choice: %s", choice)
	}

	if idx <= len(suggestions) {
		return suggestions[idx-1].name, nil
	}

	// Custom model name
	fmt.Printf("Model name (e.g. mistral:7b): ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("model name is required")
	}
	return name, nil
}
