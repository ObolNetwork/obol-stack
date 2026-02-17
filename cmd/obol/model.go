package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/urfave/cli/v2"
)

func modelCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "model",
		Usage: "Manage model providers (llmspy universal proxy)",
		Subcommands: []*cli.Command{
			{
				Name:  "setup",
				Usage: "Configure a cloud AI provider in the llmspy gateway",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "provider",
						Usage: "Provider name (anthropic, openai)",
					},
					&cli.StringFlag{
						Name:    "api-key",
						Usage:   "API key for the provider",
						EnvVars: []string{"LLM_API_KEY"},
					},
				},
				Action: func(c *cli.Context) error {
					provider := c.String("provider")
					apiKey := c.String("api-key")

					// Interactive mode if flags not provided
					if provider == "" || apiKey == "" {
						var err error
						provider, apiKey, err = promptModelConfig()
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
				Action: func(c *cli.Context) error {
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
					fmt.Printf("  %-12s %-8s %-10s %s\n", "PROVIDER", "ENABLED", "API KEY", "ENV VAR")
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
						fmt.Printf("  %-12s %-8t %-10s %s\n", name, s.Enabled, key, s.EnvVar)
					}
					return nil
				},
			},
		},
	}
}

// promptModelConfig interactively asks the user for provider and API key.
func promptModelConfig() (string, string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Select a provider:")
	fmt.Println("  [1] Anthropic")
	fmt.Println("  [2] OpenAI")
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	var provider, display string
	switch choice {
	case "1":
		provider = "anthropic"
		display = "Anthropic"
	case "2":
		provider = "openai"
		display = "OpenAI"
	default:
		return "", "", fmt.Errorf("unknown choice: %s", choice)
	}

	fmt.Printf("\n%s API key: ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("API key is required")
	}

	return provider, apiKey, nil
}
