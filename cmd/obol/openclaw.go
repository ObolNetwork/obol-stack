package main

import (
	"context"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/urfave/cli/v3"
)

func openclawCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "openclaw",
		Usage: "Manage OpenClaw AI agent instances",
		Commands: []*cli.Command{
			{
				Name:  "onboard",
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return openclaw.Onboard(cfg, openclaw.OnboardOptions{
						ID:          cmd.String("id"),
						Force:       cmd.Bool("force"),
						Sync:        !cmd.Bool("no-sync"),
						Interactive: true,
					})
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update an OpenClaw instance",
				ArgsUsage: "<id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw sync happy-otter)")
					}
					return openclaw.Sync(cfg, cmd.Args().First())
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve gateway token for an OpenClaw instance",
				ArgsUsage: "<id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw token happy-otter)")
					}
					return openclaw.Token(cfg, cmd.Args().First())
				},
			},
			{
				Name:  "list",
				Usage: "List OpenClaw instances",
				Action: func(ctx context.Context, cmd *cli.Command) error {
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw delete happy-otter)")
					}
					return openclaw.Delete(cfg, cmd.Args().First(), cmd.Bool("force"))
				},
			},
			{
				Name:      "setup",
				Usage:     "Reconfigure model providers for a deployed instance",
				ArgsUsage: "<id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw setup default)")
					}
					return openclaw.Setup(cfg, cmd.Args().First(), openclaw.SetupOptions{})
				},
			},
			{
				Name:      "dashboard",
				Usage:     "Open the OpenClaw dashboard in a browser",
				ArgsUsage: "<id>",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "port",
						Usage: "Local port for port-forward (0 = auto)",
						Value: 0,
					},
					&cli.BoolFlag{
						Name:  "no-browser",
						Usage: "Print URL without opening browser",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw dashboard default)")
					}
					noBrowser := cmd.Bool("no-browser")
					return openclaw.Dashboard(cfg, cmd.Args().First(), openclaw.DashboardOptions{
						Port:      int(cmd.Int("port")),
						NoBrowser: noBrowser,
					}, func(url string) {
						if !noBrowser {
							openBrowser(url)
						}
					})
				},
			},
			{
				Name:  "skills",
				Usage: "Manage OpenClaw skills",
				Commands: []*cli.Command{
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
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.NArg() == 0 {
								return fmt.Errorf("instance ID required (e.g., obol openclaw skills sync happy-otter --from ./skills)")
							}
							return openclaw.SkillsSync(cfg, cmd.Args().First(), cmd.String("from"))
						},
					},
				},
			},
			{
				Name:            "cli",
				Usage:           "Run openclaw CLI commands against a deployed instance",
				ArgsUsage:       "<id> [-- <openclaw args...>]",
				SkipFlagParsing: true,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()
					if len(args) == 0 {
						return fmt.Errorf("instance ID required\n\nUsage:\n" +
							"  obol openclaw cli <id> -- <openclaw command>\n\n" +
							"Examples:\n" +
							"  obol openclaw cli default -- gateway health\n" +
							"  obol openclaw cli default -- gateway call config.get\n" +
							"  obol openclaw cli default -- doctor")
					}

					id := args[0]
					// Everything after "--" is the openclaw command
					var openclawArgs []string
					for i, arg := range args[1:] {
						if arg == "--" {
							openclawArgs = args[i+2:]
							break
						}
					}
					if len(openclawArgs) == 0 && len(args) > 1 {
						// No "--" separator found; treat remaining args as openclaw command
						openclawArgs = args[1:]
					}

					return openclaw.CLI(cfg, id, openclawArgs)
				},
			},
		},
	}
}
