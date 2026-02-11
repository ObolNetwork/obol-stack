package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/llm"
	"github.com/urfave/cli/v2"
)

func llmCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "llm",
		Usage: "Manage LLM providers (llmspy universal proxy)",
		Subcommands: []*cli.Command{
			{
				Name:  "configure",
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
						provider, apiKey, err = promptLLMConfig()
						if err != nil {
							return err
						}
					}

					return llm.ConfigureLLMSpy(cfg, provider, apiKey)
				},
			},
		},
	}
}

// promptLLMConfig interactively asks the user for provider and API key.
func promptLLMConfig() (string, string, error) {
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
