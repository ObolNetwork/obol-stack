package main

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/urfave/cli/v2"
)

func openclawCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "openclaw",
		Usage: "Manage OpenClaw AI agent instances",
		Subcommands: []*cli.Command{
			{
				Name:  "up",
				Usage: "Create and deploy an OpenClaw instance",
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
					return openclaw.Up(cfg, openclaw.UpOptions{
						ID:          c.String("id"),
						Force:       c.Bool("force"),
						Sync:        !c.Bool("no-sync"),
						Interactive: true,
					})
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update an OpenClaw instance",
				ArgsUsage: "<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw sync happy-otter)")
					}
					return openclaw.Sync(cfg, c.Args().First())
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve gateway token for an OpenClaw instance",
				ArgsUsage: "<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw token happy-otter)")
					}
					return openclaw.Token(cfg, c.Args().First())
				},
			},
			{
				Name:  "list",
				Usage: "List OpenClaw instances",
				Action: func(c *cli.Context) error {
					return openclaw.List(cfg)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove an OpenClaw instance and its cluster resources",
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
						return fmt.Errorf("instance ID required (e.g., obol openclaw delete happy-otter)")
					}
					return openclaw.Delete(cfg, c.Args().First(), c.Bool("force"))
				},
			},
			{
				Name:      "skills",
				Usage:     "Manage OpenClaw skills",
				Subcommands: []*cli.Command{
					{
						Name:      "sync",
						Usage:     "Package a local skills directory into a ConfigMap",
						ArgsUsage: "<id>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "from",
								Usage:    "Path to local skills directory",
								Required: true,
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("instance ID required (e.g., obol openclaw skills sync happy-otter --from ./skills)")
							}
							return openclaw.SkillsSync(cfg, c.Args().First(), c.String("from"))
						},
					},
				},
			},
		},
	}
}
