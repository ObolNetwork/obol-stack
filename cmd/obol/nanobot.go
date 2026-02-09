package main

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/nanobot"
	"github.com/urfave/cli/v2"
)

func nanobotCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "nanobot",
		Usage: "Manage Nanobot AI agent instances",
		Subcommands: []*cli.Command{
			{
				Name:  "up",
				Usage: "Create and deploy a Nanobot instance",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "id",
						Usage: "Instance ID (defaults to generated petname)",
					},
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Overwrite existing instance",
					},
					&cli.BoolFlag{
						Name:  "no-sync",
						Usage: "Only scaffold config, don't deploy to cluster",
					},
				},
				Action: func(c *cli.Context) error {
					return nanobot.Up(cfg, nanobot.UpOptions{
						ID:          c.String("id"),
						Force:       c.Bool("force"),
						Sync:        !c.Bool("no-sync"),
						Interactive: true,
					})
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update a Nanobot instance",
				ArgsUsage: "<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol nanobot sync happy-otter)")
					}
					return nanobot.Sync(cfg, c.Args().First())
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve gateway token for a Nanobot instance",
				ArgsUsage: "<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol nanobot token happy-otter)")
					}
					return nanobot.Token(cfg, c.Args().First())
				},
			},
			{
				Name:  "list",
				Usage: "List Nanobot instances",
				Action: func(c *cli.Context) error {
					return nanobot.List(cfg)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove a Nanobot instance and its cluster resources",
				ArgsUsage: "<id>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Skip confirmation prompt",
					},
				},
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol nanobot delete happy-otter)")
					}
					return nanobot.Delete(cfg, c.Args().First(), c.Bool("force"))
				},
			},
		},
	}
}
