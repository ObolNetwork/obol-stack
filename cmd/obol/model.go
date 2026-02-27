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
	"github.com/urfave/cli/v3"
)

func modelCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "model",
		Usage: "Manage model providers (llmspy universal proxy)",
		Commands: []*cli.Command{
			{
				Name:  "setup",
				Usage: "Configure a cloud AI provider in the llmspy gateway",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "provider",
						Usage: "Provider name (e.g. anthropic, openai, zai, deepseek)",
					},
					&cli.StringFlag{
						Name:    "api-key",
						Usage:   "API key for the provider",
						Sources: cli.EnvVars("LLM_API_KEY"),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					provider := cmd.String("provider")
					apiKey := cmd.String("api-key")

					// Interactive mode if flags not provided
					if provider == "" || apiKey == "" {
						var err error
						provider, apiKey, err = promptModelConfig(cfg)
						if err != nil {
							return err
						}
					}

					return model.ConfigureLLMSpy(cfg, u, provider, apiKey)
				},
			},
			{
				Name:  "status",
				Usage: "Show global llmspy provider status",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					status, err := model.GetProviderStatus(cfg)
					if err != nil {
						return err
					}

					providers := make([]string, 0, len(status))
					for name := range status {
						providers = append(providers, name)
					}
					sort.Strings(providers)

					fmt.Println("Global llmspy providers:")
					fmt.Println()
					fmt.Printf("  %-20s %-8s %-10s %s\n", "PROVIDER", "ENABLED", "API KEY", "ENV VAR")
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
						fmt.Printf("  %-20s %-8t %-10s %s\n", name, s.Enabled, key, s.EnvVar)
					}

					// Show hint about available providers
					fmt.Println()
					fmt.Println("Run 'obol model setup' to configure a provider.")
					return nil
				},
			},
			{
				Name:      "pull",
				Usage:     "Pull an Ollama model to the local machine",
				ArgsUsage: "[model]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					modelName := cmd.Args().First()

					// Interactive mode if no model specified
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
			},
			{
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

					// Show cloud provider status if cluster is running
					providerStatus, err := model.GetProviderStatus(cfg)
					if err != nil {
						fmt.Println("Cloud providers: cluster not running")
						fmt.Println()
						fmt.Println("  Run 'obol stack up' to start the cluster,")
						fmt.Println("  then 'obol model setup' to configure a cloud provider.")
					} else {
						providers := make([]string, 0, len(providerStatus))
						for name := range providerStatus {
							providers = append(providers, name)
						}
						sort.Strings(providers)

						fmt.Println("Cloud providers:")
						fmt.Println()
						fmt.Printf("  %-20s %-10s %s\n", "PROVIDER", "STATUS", "API KEY")
						for _, name := range providers {
							if name == "ollama" {
								continue // Already shown above
							}
							s := providerStatus[name]
							status := "disabled"
							if s.Enabled {
								status = "enabled"
							}
							key := ""
							if s.EnvVar != "" {
								if s.HasAPIKey {
									key = "set"
								} else {
									key = "missing"
								}
							}
							fmt.Printf("  %-20s %-10s %s\n", name, status, key)
						}
					}

					return nil
				},
			},
		},
	}
}

// promptModelConfig interactively asks the user for provider and API key.
// It queries the running llmspy pod for available providers.
func promptModelConfig(cfg *config.Config) (string, string, error) {
	providers, err := model.GetAvailableProviders(cfg)
	if err != nil {
		return "", "", fmt.Errorf("failed to discover providers: %w", err)
	}
	if len(providers) == 0 {
		return "", "", fmt.Errorf("no cloud providers found in llmspy")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Available providers:")
	for i, p := range providers {
		fmt.Printf("  [%d] %s (%s)\n", i+1, p.Name, p.ID)
	}
	fmt.Printf("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	idx := 0
	if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil || idx < 1 || idx > len(providers) {
		return "", "", fmt.Errorf("invalid choice: %s", choice)
	}
	selected := providers[idx-1]

	fmt.Printf("\n%s API key (%s): ", selected.Name, selected.EnvVar)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("API key is required")
	}

	return selected.ID, apiKey, nil
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
