package main

import (
	"fmt"
	"log"
	"os"

	"github.com/obol/obol-stack/internal/config"
	"github.com/urfave/cli/v2"
)

const version = "0.1.0"

func main() {
	// Load config with XDG defaults
	cfg := config.Load()

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
						Action: func(c *cli.Context) error {
							fmt.Println("Cluster init - not yet implemented")
							fmt.Printf("Config dir: %s\n", cfg.ConfigDir)
							fmt.Printf("Bin dir: %s\n", cfg.BinDir)
							fmt.Printf("State dir: %s\n", cfg.StateDir)
							return nil
						},
					},
					{
						Name:  "up",
						Usage: "Start the k3d cluster",
						Action: func(c *cli.Context) error {
							fmt.Println("Cluster up - not yet implemented")
							return nil
						},
					},
					{
						Name:  "down",
						Usage: "Stop the k3d cluster",
						Action: func(c *cli.Context) error {
							fmt.Println("Cluster down - not yet implemented")
							return nil
						},
					},
					{
						Name:  "purge",
						Usage: "Delete cluster and all data",
						Action: func(c *cli.Context) error {
							fmt.Println("Cluster purge - not yet implemented")
							return nil
						},
					},
					{
						Name:  "connect",
						Usage: "Connect to cluster with k9s",
						Action: func(c *cli.Context) error {
							fmt.Println("Cluster connect - not yet implemented")
							return nil
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
				},
			},
			// TODO: Implement app command
			// {
			//     Name:  "app",
			//     Usage: "Manage applications",
			//     Subcommands: []*cli.Command{
			//         {Name: "install", Usage: "Install an application"},
			//         {Name: "edit", Usage: "Edit application values"},
			//         {Name: "sync", Usage: "Sync application changes to cluster"},
			//         {Name: "update", Usage: "Update application template"},
			//         {Name: "delete", Usage: "Delete an application"},
			//     },
			// },
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
				Name:    "state-dir",
				Usage:   "Persistent data directory (overrides OBOL_STATE_DIR and XDG_DATA_HOME)",
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
