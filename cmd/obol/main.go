package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v2"
)

func main() {
	// Load config with XDG defaults
	cfg := config.Load()

	app := &cli.App{
		Name:    "obol",
		Usage:   "Obol Stack Management CLI",
		Version: version.Full(),
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Show detailed version information",
				Action: func(c *cli.Context) error {
					fmt.Print(version.BuildInfo())
					return nil
				},
			},
			{
				Name:  "stack",
				Usage: "Manage Obol Stack lifecycle",
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
							return stack.Init(cfg, c.Bool("force"))
						},
					},
					{
						Name:  "up",
						Usage: "Start the Obol Stack",
						Action: func(c *cli.Context) error {
							return stack.Up(cfg)
						},
					},
					{
						Name:  "down",
						Usage: "Stop the Obol Stack",
						Action: func(c *cli.Context) error {
							return stack.Down(cfg)
						},
					},
					{
						Name:  "purge",
						Usage: "Delete stack and all data",
						Action: func(c *cli.Context) error {
							return stack.Purge(cfg)
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
							fmt.Printf("Stack backup %s - not yet implemented\n", c.Args().First())
							return nil
						},
					},
				},
			},
			{
				Name:            "k9s",
				Usage:           "Launch k9s with cluster kubeconfig (passthrough)",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", "kubeconfig.yaml")

					// Check if kubeconfig exists
					if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
						return fmt.Errorf("cluster not running, use 'obol cluster up' first")
					}

					k9sPath := filepath.Join(cfg.BinDir, "k9s")

					// Check if k9s exists
					if _, err := os.Stat(k9sPath); os.IsNotExist(err) {
						return fmt.Errorf("k9s not found in %s", cfg.BinDir)
					}

					// Pass all arguments to k9s
					cmd := exec.Command(k9sPath, c.Args().Slice()...)
					cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					return cmd.Run()
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
			//     Name:      "agent",
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
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
