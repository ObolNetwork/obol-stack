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
				Name:      "setup",
				Usage:     "Reconfigure model providers for a deployed instance",
				ArgsUsage: "<id>",
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw setup default)")
					}
					return openclaw.Setup(cfg, c.Args().First(), openclaw.SetupOptions{})
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
				Action: func(c *cli.Context) error {
					if c.NArg() == 0 {
						return fmt.Errorf("instance ID required (e.g., obol openclaw dashboard default)")
					}
					noBrowser := c.Bool("no-browser")
					return openclaw.Dashboard(cfg, c.Args().First(), openclaw.DashboardOptions{
						Port:      c.Int("port"),
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
				Subcommands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "List skills loaded in an OpenClaw instance",
						ArgsUsage: "<id>",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "json", Usage: "Output as JSON"},
							&cli.BoolFlag{Name: "eligible", Usage: "Show only eligible (ready-to-use) skills"},
							&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Show details including requirements"},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("instance ID required (e.g., obol openclaw skills list default)")
							}
							var args []string
							if c.Bool("json") {
								args = append(args, "--json")
							}
							if c.Bool("eligible") {
								args = append(args, "--eligible")
							}
							if c.Bool("verbose") {
								args = append(args, "-v")
							}
							return openclaw.SkillsCLI(cfg, c.Args().First(), append([]string{"list"}, args...))
						},
					},
					{
						Name:      "info",
						Usage:     "Show detailed information about a skill",
						ArgsUsage: "<id> <skill-name>",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "json", Usage: "Output as JSON"},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() < 2 {
								return fmt.Errorf("instance ID and skill name required (e.g., obol openclaw skills info default github)")
							}
							args := []string{"info", c.Args().Get(1)}
							if c.Bool("json") {
								args = append(args, "--json")
							}
							return openclaw.SkillsCLI(cfg, c.Args().First(), args)
						},
					},
					{
						Name:      "check",
						Usage:     "Check which skills are ready vs missing requirements",
						ArgsUsage: "<id>",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "json", Usage: "Output as JSON"},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("instance ID required (e.g., obol openclaw skills check default)")
							}
							args := []string{"check"}
							if c.Bool("json") {
								args = append(args, "--json")
							}
							return openclaw.SkillsCLI(cfg, c.Args().First(), args)
						},
					},
					{
						Name:      "add",
						Usage:     "Install a skill from the clawhub registry and sync to pod",
						ArgsUsage: "<id> <slug>",
						Action: func(c *cli.Context) error {
							if c.NArg() < 2 {
								return fmt.Errorf("instance ID and skill slug required (e.g., obol openclaw skills add default austintgriffith/ethereum-wingman)")
							}
							return openclaw.SkillsAdd(cfg, c.Args().First(), c.Args().Get(1))
						},
					},
					{
						Name:      "remove",
						Usage:     "Remove a skill from the managed directory and re-sync",
						ArgsUsage: "<id> <skill-name>",
						Action: func(c *cli.Context) error {
							if c.NArg() < 2 {
								return fmt.Errorf("instance ID and skill name required (e.g., obol openclaw skills remove default ethereum-wingman)")
							}
							return openclaw.SkillsRemove(cfg, c.Args().First(), c.Args().Get(1))
						},
					},
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
			{
				Name:            "cli",
				Usage:           "Run openclaw CLI commands against a deployed instance",
				ArgsUsage:       "<id> [-- <openclaw args...>]",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					args := c.Args().Slice()
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
