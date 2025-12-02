package main

import (
	"fmt"
	"slices"
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
				Name:      "sync",
				Usage:     "Deploy or update network configuration to cluster",
				ArgsUsage: "<network>/<id> or <network>-<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("deployment identifier required (e.g., ethereum/knowing-wahoo or ethereum-knowing-wahoo)")
					}
					deploymentIdentifier := c.Args().First()
					return network.Sync(cfg, deploymentIdentifier)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove network deployment and clean up cluster resources",
				ArgsUsage: "<network>/<id> or <network>-<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("deployment identifier required (e.g., ethereum/test-deploy or ethereum-test-deploy)")
					}
					deploymentIdentifier := c.Args().First()
					return network.Delete(cfg, deploymentIdentifier)
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
		// Parse the embedded values template to get fields
		fields, err := network.ParseTemplateFields(networkName)
		if err != nil {
			// Skip networks we can't parse
			continue
		}

		// Build flags from template fields
		flags := []cli.Flag{}
		for _, field := range fields {
			// Customize id flag description to include network-specific example
			if field.FlagName == "id" {
				flags = append(flags, &cli.StringFlag{
					Name:     "id",
					Usage:    fmt.Sprintf("Namespace suffix for this deployment (e.g., 'smart-kodiak' becomes '%s-smart-kodiak', defaults to generated petname)", networkName),
					Required: field.Required,
				})
				continue
			}
			// Build usage string
			usage := field.Description
			if usage == "" {
				usage = fmt.Sprintf("Override %s", field.Name)
			}

			// Mark as required if no default value
			if field.Required {
				usage = "[REQUIRED] " + usage
			}

			// Add enum options if available
			if len(field.EnumValues) > 0 {
				usage += fmt.Sprintf(" [options: %s]", strings.Join(field.EnumValues, ", "))
			}

			// Add default value
			if field.DefaultValue != "" {
				usage += fmt.Sprintf(" (default: %s)", field.DefaultValue)
			}

			flags = append(flags, &cli.StringFlag{
				Name:     field.FlagName,
				Usage:    usage,
				Required: field.Required,
			})
		}

		// Create the network-specific install command
		netName := networkName // Capture for closure
		netFields := fields    // Capture for validation
		commands = append(commands, &cli.Command{
			Name:  netName,
			Usage: fmt.Sprintf("Install %s network", netName),
			Flags: flags,
			Action: func(c *cli.Context) error {
				// Collect and validate flag values
				overrides := make(map[string]string)
				for _, field := range netFields {
					value := c.String(field.FlagName)
					if value != "" {
						// Validate enum constraint if defined
						if len(field.EnumValues) > 0 {
							valid := slices.Contains(field.EnumValues, value)
							if !valid {
								return fmt.Errorf("invalid value '%s' for --%s. Valid options: %s",
									value, field.FlagName, strings.Join(field.EnumValues, ", "))
							}
						}
						overrides[field.FlagName] = value
					}
				}

				return network.Install(cfg, netName, overrides)
			},
		})
	}

	return commands
}
