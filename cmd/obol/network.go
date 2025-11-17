package main

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v2"
)

// networkCommand returns the network management command group with dynamic subcommands
func networkCommand(cfg *config.Config) *cli.Command {
	// Build sync subcommands dynamically from embedded networks
	syncSubcommands := buildNetworkSyncCommands(cfg)

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
				Name:      "add",
				Usage:     "Add a network to the stack",
				ArgsUsage: "<network>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("network name required (e.g., ethereum, helios)")
					}
					networkName := c.Args().First()
					return network.Add(cfg, networkName)
				},
			},
			{
				Name:        "sync",
				Usage:       "Deploy network configuration to cluster",
				Subcommands: syncSubcommands,
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

// buildNetworkSyncCommands dynamically creates sync subcommands for each embedded network
func buildNetworkSyncCommands(cfg *config.Config) []*cli.Command {
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
			flags = append(flags, &cli.StringFlag{
				Name:  envVar.FlagName,
				Usage: fmt.Sprintf("Override %s (default: %s)", envVar.Name, envVar.DefaultValue),
			})
		}

		// Create the network-specific sync command
		netName := networkName // Capture for closure
		commands = append(commands, &cli.Command{
			Name:  netName,
			Usage: fmt.Sprintf("Deploy %s network", netName),
			Flags: flags,
			Action: func(c *cli.Context) error {
				// Collect flag values into overrides map
				overrides := make(map[string]string)
				for _, flag := range flags {
					if stringFlag, ok := flag.(*cli.StringFlag); ok {
						if value := c.String(stringFlag.Name); value != "" {
							overrides[stringFlag.Name] = value
						}
					}
				}

				return network.Sync(cfg, netName, overrides)
			},
		})
	}

	return commands
}
