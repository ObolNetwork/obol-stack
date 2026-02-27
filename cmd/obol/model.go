package main

import (
	"fmt"
	"sort"

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
						Usage: "Provider name (e.g. anthropic, openai, zai, deepseek)",
					},
					&cli.StringFlag{
						Name:    "api-key",
						Usage:   "API key for the provider",
						EnvVars: []string{"LLM_API_KEY"},
					},
				},
				Action: func(c *cli.Context) error {
					u := getUI(c)
					provider := c.String("provider")
					apiKey := c.String("api-key")

					// Interactive mode if flags not provided
					if provider == "" || apiKey == "" {
						providers, err := model.GetAvailableProviders(cfg)
						if err != nil {
							return fmt.Errorf("failed to discover providers: %w", err)
						}
						if len(providers) == 0 {
							return fmt.Errorf("no cloud providers found in llmspy")
						}

						options := make([]string, len(providers))
						for i, p := range providers {
							options[i] = fmt.Sprintf("%s (%s)", p.Name, p.ID)
						}

						idx, err := u.Select("Select a provider:", options, 0)
						if err != nil {
							return err
						}
						provider = providers[idx].ID

						apiKey, err = u.SecretInput(fmt.Sprintf("%s API key (%s)", providers[idx].Name, providers[idx].EnvVar))
						if err != nil {
							return err
						}
						if apiKey == "" {
							return fmt.Errorf("API key is required")
						}
					}

					return model.ConfigureLLMSpy(cfg, u, provider, apiKey)
				},
			},
			{
				Name:  "status",
				Usage: "Show global llmspy provider status",
				Action: func(c *cli.Context) error {
					u := getUI(c)
					status, err := model.GetProviderStatus(cfg)
					if err != nil {
						return err
					}

					providers := make([]string, 0, len(status))
					for name := range status {
						providers = append(providers, name)
					}
					sort.Strings(providers)

					u.Bold("Global llmspy providers:")
					u.Blank()
					u.Printf("  %-20s %-8s %-10s %s", "PROVIDER", "ENABLED", "API KEY", "ENV VAR")
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
						u.Printf("  %-20s %-8t %-10s %s", name, s.Enabled, key, s.EnvVar)
					}

					u.Blank()
					u.Dim("Run 'obol model setup' to configure a provider.")
					return nil
				},
			},
		},
	}
}
