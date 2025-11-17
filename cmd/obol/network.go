package main

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/urfave/cli/v2"
)

// networkCommand returns the network management command group
func networkCommand(cfg *config.Config) *cli.Command {
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
						return fmt.Errorf("network name required (e.g., ethereum, aztec, base)")
					}
					networkName := c.Args().First()
					return network.Add(cfg, networkName)
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy network configuration to cluster",
				ArgsUsage: "<network>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("network name required (e.g., ethereum, aztec, base)")
					}
					networkName := c.Args().First()
					return network.Sync(cfg, networkName)
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
						return fmt.Errorf("network name required (e.g., ethereum, aztec, base)")
					}
					networkName := c.Args().First()
					return network.Delete(cfg, networkName, c.Bool("force"))
				},
			},
		},
	}
}
