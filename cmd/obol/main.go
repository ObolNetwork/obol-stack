package main

import (
	"fmt"
	"log"
	"os"

	"github.com/ObolNetwork/obol-stack/internal/config"
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
						Usage: "Initialize stack configuration",
						Action: func(c *cli.Context) error {
							fmt.Println("Stack init - not yet implemented")
							fmt.Printf("Config dir: %s\n", cfg.ConfigDir)
							fmt.Printf("Bin dir: %s\n", cfg.BinDir)
							fmt.Printf("State dir: %s\n", cfg.StateDir)
							return nil
						},
					},
					{
						Name:  "up",
						Usage: "Start the Obol Stack",
						Action: func(c *cli.Context) error {
							fmt.Println("Stack up - not yet implemented")
							return nil
						},
					},
					{
						Name:  "down",
						Usage: "Stop the Obol Stack",
						Action: func(c *cli.Context) error {
							fmt.Println("Stack down - not yet implemented")
							return nil
						},
					},
					{
						Name:  "purge",
						Usage: "Delete stack and all data",
						Action: func(c *cli.Context) error {
							fmt.Println("Stack purge - not yet implemented")
							return nil
						},
					},
					{
						Name:  "connect",
						Usage: "Connect to stack with k9s",
						Action: func(c *cli.Context) error {
							fmt.Println("Stack connect - not yet implemented")
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
							fmt.Printf("Stack backup %s - not yet implemented\n", c.Args().First())
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
