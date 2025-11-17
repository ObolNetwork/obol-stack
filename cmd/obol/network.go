package main

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v2"
)

// networkCommand returns the network management command group with dynamic subcommands
func networkCommand(cfg *config.Config) *cli.Command {
	// Build install subcommands dynamically from embedded networks
	installSubcommands := buildNetworkInstallCommands(cfg)

	return &cli.Command{
		Name:  "network",
		Usage: "Manage blockchain networks",
		Subcommands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List available networks",
				Action: func(c *cli.Context) error {
					return network.List(cfg)
				},
			},
			{
				Name:        "install",
				Usage:       "Install and deploy network to cluster",
				Subcommands: installSubcommands,
				Action: func(c *cli.Context) error {
					// Show help if no network specified
					return cli.ShowSubcommandHelp(c)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove network and clean up cluster resources",
				ArgsUsage: "<network>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Skip confirmation prompt",
					},
				},
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("network name required (e.g., ethereum, helios)")
					}
					networkName := c.Args().First()
					return network.Delete(cfg, networkName, c.Bool("force"))
				},
			},
		},
	}
}

// buildNetworkInstallCommands dynamically creates install subcommands for each embedded network
func buildNetworkInstallCommands(cfg *config.Config) []*cli.Command {
	// Get all embedded networks
	networks, err := embed.GetAvailableNetworks()
	if err != nil {
		return nil
	}

	var commands []*cli.Command
	for _, networkName := range networks {
		// Parse the embedded helmfile to get env vars
		envVars, err := network.ParseEmbeddedNetworkEnvVars(networkName)
		if err != nil {
			// Skip networks we can't parse
			continue
		}

		// Build flags from env vars
		flags := []cli.Flag{}
		for _, envVar := range envVars {
			// Build usage string
			usage := envVar.Description
			if usage == "" {
				usage = fmt.Sprintf("Override %s", envVar.Name)
			}

			// Add enum options if available
			if len(envVar.EnumValues) > 0 {
				usage += fmt.Sprintf(" [options: %s]", strings.Join(envVar.EnumValues, ", "))
			}

			// Add default value
			if envVar.DefaultValue != "" {
				usage += fmt.Sprintf(" (default: %s)", envVar.DefaultValue)
			}

			flags = append(flags, &cli.StringFlag{
				Name:  envVar.FlagName,
				Usage: usage,
			})
		}

		// Create the network-specific install command
		netName := networkName // Capture for closure
		netEnvVars := envVars  // Capture for validation
		commands = append(commands, &cli.Command{
			Name:  netName,
			Usage: fmt.Sprintf("Install %s network", netName),
			Flags: flags,
			Action: func(c *cli.Context) error {
				// Collect and validate flag values
				overrides := make(map[string]string)
				for _, envVar := range netEnvVars {
					value := c.String(envVar.FlagName)
					if value != "" {
						// Validate enum constraint if defined
						if len(envVar.EnumValues) > 0 {
							valid := false
							for _, enumVal := range envVar.EnumValues {
								if value == enumVal {
									valid = true
									break
								}
							}
							if !valid {
								return fmt.Errorf("invalid value '%s' for --%s. Valid options: %s",
									value, envVar.FlagName, strings.Join(envVar.EnumValues, ", "))
							}
						}
						overrides[envVar.FlagName] = value
					}
				}

				return network.Install(cfg, netName, overrides)
			},
		})
	}

	return commands
}
