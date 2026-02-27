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
				Action: func(c *cli.Context) error {
					return openclaw.Onboard(cfg, openclaw.OnboardOptions{
						ID:          c.String("id"),
						Force:       c.Bool("force"),
						Sync:        !c.Bool("no-sync"),
						Interactive: true,
					}, getUI(c))
				},
			},
			{
				Name:      "sync",
				Usage:     "Deploy or update an OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Sync(cfg, id, getUI(c))
				},
			},
			{
				Name:      "token",
				Usage:     "Retrieve gateway token for an OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Token(cfg, id, getUI(c))
				},
			},
			{
				Name:  "list",
				Usage: "List OpenClaw instances",
				Action: func(c *cli.Context) error {
					return openclaw.List(cfg, getUI(c))
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
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Delete(cfg, id, c.Bool("force"), getUI(c))
				},
			},
			{
				Name:      "setup",
				Usage:     "Reconfigure model providers for a deployed instance",
				ArgsUsage: "[instance-name]",
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.Setup(cfg, id, openclaw.SetupOptions{}, getUI(c))
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
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					noBrowser := c.Bool("no-browser")
					return openclaw.Dashboard(cfg, id, openclaw.DashboardOptions{
						Port:      c.Int("port"),
						NoBrowser: noBrowser,
					}, func(url string) {
						if !noBrowser {
							openBrowser(url)
						}
					}, getUI(c))
				},
			},
			openclawSkillsCommand(cfg),
			{
				Name:            "cli",
				Usage:           "Run openclaw CLI commands against a deployed instance",
				ArgsUsage:       "[instance-name] [-- <openclaw args...>]",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					args := c.Args().Slice()

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

					return openclaw.CLI(cfg, id, openclawArgs, getUI(c))
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
		Subcommands: []*cli.Command{
			{
				Name:            "add",
				Usage:           "Add a skill package to the OpenClaw instance",
				ArgsUsage:       "[instance-name] <package-or-path>",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					args := c.Args().Slice()
					id, remaining, err := openclaw.ResolveInstance(cfg, args)
					if err != nil {
						return err
					}
					if len(remaining) == 0 {
						return fmt.Errorf("skill package or path required\n\nUsage: obol openclaw skill add <package-or-path>")
					}
					return openclaw.SkillAdd(cfg, id, remaining, getUI(c))
				},
			},
			{
				Name:            "remove",
				Usage:           "Remove a skill from the OpenClaw instance",
				ArgsUsage:       "[instance-name] <skill-name>",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					args := c.Args().Slice()
					id, remaining, err := openclaw.ResolveInstance(cfg, args)
					if err != nil {
						return err
					}
					if len(remaining) == 0 {
						return fmt.Errorf("skill name required\n\nUsage: obol openclaw skill remove <skill-name>")
					}
					return openclaw.SkillRemove(cfg, id, remaining, getUI(c))
				},
			},
			{
				Name:      "list",
				Usage:     "List installed skills on the OpenClaw instance",
				ArgsUsage: "[instance-name]",
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.SkillList(cfg, id, getUI(c))
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
				Action: func(c *cli.Context) error {
					id, _, err := openclaw.ResolveInstance(cfg, c.Args().Slice())
					if err != nil {
						return err
					}
					return openclaw.SkillsSync(cfg, id, c.String("from"), getUI(c))
				},
			},
		},
	}
}
