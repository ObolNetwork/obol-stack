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
					}, getUI(cmd))
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update an OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Sync(cfg, id, getUI(cmd))
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve or regenerate gateway token for an OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "regenerate",
						Usage: "Delete and regenerate the gateway token (restarts the instance)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					u := getUI(cmd)
					if cmd.Bool("regenerate") {
						newToken, err := openclaw.RegenerateToken(cfg, id, u)
						if err != nil {
							return err
						}
						u.Print(newToken)
						return nil
					}
					return openclaw.Token(cfg, id, u)
				},
			},
			{
				Name:  "list",
				Usage: "List OpenClaw instances",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return openclaw.List(cfg, getUI(cmd))
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove an OpenClaw instance and its cluster resources",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "force",
						Aliases: []string{"f"},
						Usage:   "Skip confirmation prompt",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Delete(cfg, id, cmd.Bool("force"), getUI(cmd))
				},
			},
			{
				Name:      "setup",
				Usage:     "Reconfigure model providers for a deployed instance",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Setup(cfg, id, openclaw.SetupOptions{}, getUI(cmd))
				},
			},
			{
				Name:      "dashboard",
				Usage:     "Open the OpenClaw dashboard in a browser",
				ArgsUsage: "[instance-name]",
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
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					noBrowser := cmd.Bool("no-browser")
					return openclaw.Dashboard(cfg, id, openclaw.DashboardOptions{
						Port:      int(cmd.Int("port")),
						NoBrowser: noBrowser,
					}, func(url string) {
						if !noBrowser {
							openBrowser(url)
						}
					}, getUI(cmd))
				},
			},
			openclawSkillsCommand(cfg),
			{
				Name:            "cli",
				Usage:           "Run openclaw CLI commands against a deployed instance",
				ArgsUsage:       "[instance-name] [-- <openclaw args...>]",
				SkipFlagParsing: true,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()

					id, remaining, err := openclaw.ResolveInstance(cfg, args)
					if err != nil {
						return fmt.Errorf("%w\n\nUsage:\n"+
							"  obol openclaw cli [instance-name] -- <openclaw command>\n\n"+
							"Examples:\n"+
							"  obol openclaw cli -- gateway health\n"+
							"  obol openclaw cli default -- doctor", err)
					}

					// Strip the "--" separator if present
					var openclawArgs []string
					for i, arg := range remaining {
						if arg == "--" {
							openclawArgs = remaining[i+1:]
							break
						}
					}
					if len(openclawArgs) == 0 && len(remaining) > 0 {
						openclawArgs = remaining
					}

					return openclaw.CLI(cfg, id, openclawArgs, getUI(cmd))
				},
			},
		},
	}
}

// openclawSkillsCommand builds the "obol openclaw skills" subcommand group.
func openclawSkillsCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "skills",
		Usage: "Manage OpenClaw skills",
		Commands: []*cli.Command{
			{
				Name:            "add",
				Usage:           "Add a skill package to the OpenClaw instance",
				ArgsUsage:       "[instance-name] <package-or-path>",
				SkipFlagParsing: true,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()
					id, remaining, err := openclaw.ResolveInstance(cfg, args)
					if err != nil {
						return err
					}
					if len(remaining) == 0 {
						return fmt.Errorf("skill package or path required\n\nUsage: obol openclaw skill add <package-or-path>")
					}
					return openclaw.SkillAdd(cfg, id, remaining, getUI(cmd))
				},
			},
			{
				Name:            "remove",
				Usage:           "Remove a skill from the OpenClaw instance",
				ArgsUsage:       "[instance-name] <skill-name>",
				SkipFlagParsing: true,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args().Slice()
					id, remaining, err := openclaw.ResolveInstance(cfg, args)
					if err != nil {
						return err
					}
					if len(remaining) == 0 {
						return fmt.Errorf("skill name required\n\nUsage: obol openclaw skill remove <skill-name>")
					}
					return openclaw.SkillRemove(cfg, id, remaining, getUI(cmd))
				},
			},
			{
				Name:      "list",
				Usage:     "List installed skills on the OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.SkillList(cfg, id, getUI(cmd))
				},
			},
			{
				Name:      "sync",
				Usage:     "Copy a local skills directory to the OpenClaw volume",
				ArgsUsage: "[instance-name]",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "from",
						Usage:    "Path to local skills directory",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, _, err := openclaw.ResolveInstance(cfg, cmd.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.SkillsSync(cfg, id, cmd.String("from"), getUI(cmd))
				},
			},
		},
	}
}
