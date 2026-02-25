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

					return model.ConfigureLLMSpy(cfg, provider, apiKey)
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
