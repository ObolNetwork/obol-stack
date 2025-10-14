package main

import (
	"fmt"
	"log"
	"os"

	"github.com/obol/obol-stack/internal/app"
	"github.com/obol/obol-stack/internal/cluster"
	"github.com/obol/obol-stack/internal/config"
	"github.com/obol/obol-stack/internal/embed"
	"github.com/obol/obol-stack/internal/logging"
	"github.com/urfave/cli/v2"
)

const version = "0.0.0"

func main() {
	// Load config with XDG defaults
	cfg := config.Load()

	// Note: Logger is not initialized here as it requires a cluster_id.
	// Individual commands (cluster, app) handle their own logging as needed.
	// Cluster commands create cluster-specific loggers with the cluster_id.
	// App commands can optionally log but don't require it.
	var logger *logging.Logger = nil

	app := &cli.App{
		Name:    "obol",
		Usage:   "Obol Stack Management CLI",
		Version: version,
		Commands: []*cli.Command{
			{
				Name:  "cluster",
				Usage: "Manage k3d cluster lifecycle",
				Subcommands: []*cli.Command{
					{
						Name:  "init",
						Usage: "Initialize cluster configuration",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Force overwrite existing configuration",
							},
						},
						Action: func(c *cli.Context) error {
							return cluster.Init(cfg, logger, c.Bool("force"))
						},
					},
					{
						Name:  "up",
						Usage: "Start the k3d cluster",
						Action: func(c *cli.Context) error {
							return cluster.Up(cfg, logger)
						},
					},
					{
						Name:  "down",
						Usage: "Stop the k3d cluster",
						Action: func(c *cli.Context) error {
							return cluster.Down(cfg, logger)
						},
					},
					{
						Name:  "purge",
						Usage: "Delete cluster and all data",
						Action: func(c *cli.Context) error {
							return cluster.Purge(cfg, logger)
						},
					},
					{
						Name:  "connect",
						Usage: "Connect to cluster with k9s",
						Action: func(c *cli.Context) error {
							return cluster.Connect(cfg)
						},
					},
					{
						Name:      "backup",
						Usage:     "Backup persistent volume",
						ArgsUsage: "<volume-name>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("volume name required")
							}
							fmt.Printf("Cluster backup %s - not yet implemented\n", c.Args().First())
							return nil
						},
					},
					// TODO: Implement doctor command for diagnostic reports
					// {
					//     Name:  "doctor",
					//     Usage: "Generate diagnostic report for debugging",
					//     ...
					// },
				},
			},
			{
				Name:  "app",
				Usage: "Manage applications",
				Subcommands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "List available applications",
						ArgsUsage: " ",
						Action: func(c *cli.Context) error {
							return app.List(cfg, logger, embed.GetApplicationsFS())
						},
					},
					{
						Name:      "install",
						Usage:     "Install an application",
						ArgsUsage: "<app-name>",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Force overwrite if application already exists",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application name required")
							}
							appName := c.Args().First()
							return app.Install(cfg, logger, embed.GetApplicationsFS(), appName, c.Bool("force"))
						},
					},
					{
						Name:      "edit",
						Usage:     "Edit application values.yaml",
						ArgsUsage: "<app-name>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application name required")
							}
							appName := c.Args().First()
							return app.Edit(cfg, logger, appName)
						},
					},
					{
						Name:      "sync",
						Usage:     "Sync application to cluster (apply changes)",
						ArgsUsage: "<app-name>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application name required")
							}
							appName := c.Args().First()
							return app.Sync(cfg, logger, appName)
						},
					},
					{
						Name:      "delete",
						Usage:     "Delete application and remove from cluster",
						ArgsUsage: "<app-name>",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Skip confirmation prompt",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application name required")
							}
							appName := c.Args().First()
							return app.Delete(cfg, logger, appName, c.Bool("force"))
						},
					},
				},
			},
			// TODO: Implement ai command
			// {
			//     Name:      "ai",
			//     Usage:     "AI-assisted cluster debugging",
			//     ArgsUsage: "<prompt>",
			//     Action: func(c *cli.Context) error {
			//         if c.NArg() == 0 {
			//             return fmt.Errorf("prompt required")
			//         }
			//         fmt.Printf("AI prompt: %s - not yet implemented\n", c.Args().First())
			//         return nil
			//     },
			// },
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config-dir",
				Usage:   "Configuration directory (overrides OBOL_CONFIG_DIR and XDG_CONFIG_HOME)",
				EnvVars: []string{"OBOL_CONFIG_DIR"},
			},
			&cli.StringFlag{
				Name:    "bin-dir",
				Usage:   "Binary directory (overrides OBOL_BIN_DIR)",
				EnvVars: []string{"OBOL_BIN_DIR"},
			},
			&cli.StringFlag{
				Name:    "data-dir",
				Usage:   "Persistent data directory (overrides OBOL_DATA_DIR and XDG_DATA_HOME)",
				EnvVars: []string{"OBOL_DATA_DIR"},
			},
			&cli.StringFlag{
				Name:    "state-dir",
				Usage:   "State directory for logs and history (overrides OBOL_STATE_DIR and XDG_STATE_HOME)",
				EnvVars: []string{"OBOL_STATE_DIR"},
			},
		},
		Before: func(c *cli.Context) error {
			// Override config with CLI flags if provided
			if c.String("config-dir") != "" {
				cfg.ConfigDir = c.String("config-dir")
			}
			if c.String("bin-dir") != "" {
				cfg.BinDir = c.String("bin-dir")
			}
			if c.String("data-dir") != "" {
				cfg.DataDir = c.String("data-dir")
			}
			if c.String("state-dir") != "" {
				cfg.StateDir = c.String("state-dir")
			}
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
