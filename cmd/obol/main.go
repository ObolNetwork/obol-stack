package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/executor"
	"github.com/ObolNetwork/obol-stack/internal/logging"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v2"
)

func main() {
	// Load config with XDG defaults
	cfg := config.Load()

	// Custom help template with command sections
	cli.AppHelpTemplate = `
   ██████╗ ██████╗  ██████╗ ██╗         ███████╗████████╗ █████╗  ██████╗██╗  ██╗
  ██╔═══██╗██╔══██╗██╔═══██╗██║         ██╔════╝╚══██╔══╝██╔══██╗██╔════╝██║ ██╔╝
  ██║   ██║██████╔╝██║   ██║██║         ███████╗   ██║   ███████║██║     █████╔╝
  ██║   ██║██╔══██╗██║   ██║██║         ╚════██║   ██║   ██╔══██║██║     ██╔═██╗
  ╚██████╔╝██████╔╝╚██████╔╝███████╗    ███████║   ██║   ██║  ██║╚██████╗██║  ██╗
   ╚═════╝ ╚═════╝  ╚═════╝ ╚══════╝    ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝

NAME:
   {{.Name}}{{if .Usage}} - {{.Usage}}{{end}}

USAGE:
   {{if .UsageText}}{{.UsageText}}{{else}}{{.HelpName}} {{if .VisibleFlags}}[global options]{{end}}{{if .Commands}} command [command options]{{end}}{{end}}

VERSION:
   {{.Version}}

COMMANDS:
   Stack Lifecycle:
     stack init      Initialize stack configuration
     stack up        Start the Obol Stack
     stack down      Stop the Obol Stack
     stack purge     Delete stack and all data

   Kubernetes Tools (with auto-configured KUBECONFIG):
     kubectl         Run kubectl with stack kubeconfig (passthrough)
     helm            Run helm with stack kubeconfig (passthrough)
     helmfile        Run helmfile with stack kubeconfig (passthrough)
     k9s             Run k9s with stack kubeconfig (passthrough)

   Other:
     version         Show detailed version information
     help, h         Shows a list of commands or help for one command
{{if .VisibleFlags}}
GLOBAL OPTIONS:
   {{range $index, $option := .VisibleFlags}}{{if $index}}
   {{end}}{{$option}}{{end}}{{end}}
`
	app := &cli.App{
		Name:    "obol",
		Usage:   "Obol Stack Management CLI",
		Version: version.Full(),
		Commands: []*cli.Command{
			// ============================================================
			// Obol Stack Lifecycle Commands
			// ============================================================
			{
				Name:  "stack",
				Usage: "Manage Obol Stack lifecycle",
				Subcommands: []*cli.Command{
					{
						Name:  "init",
						Usage: "Initialize stack configuration",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Force overwrite existing configuration",
							},
						},
						Action: func(c *cli.Context) error {
							if err := stack.Init(cfg, c.Bool("force")); err != nil {
								l, _ := logging.NewSlogLogger(logging.LoggerConfig{
									StateDir: cfg.StateDir,
									StackID:  "",
								})
								l.Error("Failed to initialize stack", "error", err.Error())
								return err
							}
							return nil
						},
					},
					{
						Name:  "up",
						Usage: "Start the Obol Stack",
						Action: func(c *cli.Context) error {
							if err := stack.Up(cfg); err != nil {
								stackID := stack.GetStackID(cfg)
								l, _ := logging.NewSlogLogger(logging.LoggerConfig{
									StateDir: cfg.StateDir,
									StackID:  stackID,
								})
								l.Error("Failed to start stack", "error", err.Error())
								return err
							}
							return nil
						},
					},
					{
						Name:  "down",
						Usage: "Stop the Obol Stack",
						Action: func(c *cli.Context) error {
							if err := stack.Down(cfg); err != nil {
								stackID := stack.GetStackID(cfg)
								l, _ := logging.NewSlogLogger(logging.LoggerConfig{
									StateDir: cfg.StateDir,
									StackID:  stackID,
								})
								l.Error("Failed to stop stack", "error", err.Error())
								return err
							}
							return nil
						},
					},
					{
						Name:  "purge",
						Usage: "Delete stack and all data",
						Action: func(c *cli.Context) error {
							if err := stack.Purge(cfg); err != nil {
								stackID := stack.GetStackID(cfg)
								l, _ := logging.NewSlogLogger(logging.LoggerConfig{
									StateDir: cfg.StateDir,
									StackID:  stackID,
								})
								l.Error("Failed to purge stack", "error", err.Error())
								return err
							}
							return nil
						},
					},
				},
			},
			// ============================================================
			// Kubernetes Tool Passthroughs (with auto-configured KUBECONFIG)
			// ============================================================
			{
				Name:            "kubectl",
				Usage:           "Run kubectl with stack kubeconfig (passthrough)",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

					// Check if kubeconfig exists
					if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("stack not running, use 'obol stack up' first")
						return err
					}

					kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

					// Check if kubectl exists
					if _, err := os.Stat(kubectlPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("kubectl not found", "path", cfg.BinDir)
						return err
					}

					stackID := stack.GetStackID(cfg)

					// Create logger and executor
					l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
						StateDir: cfg.StateDir,
						StackID:  stackID,
					})
					defer cleanup()

					exec := executor.New(l.Logger)
					cmd := exec.CommandWithOutput(kubectlPath, c.Args().Slice()...)
					cmd.SetEnv(append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath)))
					cmd.SetStdin(os.Stdin)

					if err := cmd.Run(); err != nil {
						l.Error("kubectl command failed", "error", err.Error())
						return err
					}
					return nil
				},
			},
			{
				Name:            "helm",
				Usage:           "Run helm with stack kubeconfig (passthrough)",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

					// Check if kubeconfig exists
					if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("stack not running, use 'obol stack up' first")
						return err
					}

					helmPath := filepath.Join(cfg.BinDir, "helm")

					// Check if helm exists
					if _, err := os.Stat(helmPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("helm not found", "path", cfg.BinDir)
						return err
					}

					stackID := stack.GetStackID(cfg)

					// Create logger and executor
					l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
						StateDir: cfg.StateDir,
						StackID:  stackID,
					})
					defer cleanup()

					exec := executor.New(l.Logger)
					cmd := exec.CommandWithOutput(helmPath, c.Args().Slice()...)
					cmd.SetEnv(append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath)))
					cmd.SetStdin(os.Stdin)

					if err := cmd.Run(); err != nil {
						l.Error("helm command failed", "error", err.Error())
						return err
					}
					return nil
				},
			},
			{
				Name:            "helmfile",
				Usage:           "Run helmfile with stack kubeconfig (passthrough)",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

					// Check if kubeconfig exists
					if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("stack not running, use 'obol stack up' first")
						return err
					}

					helmfilePath := filepath.Join(cfg.BinDir, "helmfile")

					// Check if helmfile exists
					if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("helmfile not found", "path", cfg.BinDir)
						return err
					}

					stackID := stack.GetStackID(cfg)

					// Create logger and executor
					l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
						StateDir: cfg.StateDir,
						StackID:  stackID,
					})
					defer cleanup()

					exec := executor.New(l.Logger)
					cmd := exec.CommandWithOutput(helmfilePath, c.Args().Slice()...)
					cmd.SetEnv(append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath)))
					cmd.SetStdin(os.Stdin)

					if err := cmd.Run(); err != nil {
						l.Error("helmfile command failed", "error", err.Error())
						return err
					}
					return nil
				},
			},
			{
				Name:            "k9s",
				Usage:           "Run k9s with stack kubeconfig (passthrough)",
				SkipFlagParsing: true,
				Action: func(c *cli.Context) error {
					kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

					// Check if kubeconfig exists
					if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("stack not running, use 'obol stack up' first")
						return err
					}

					k9sPath := filepath.Join(cfg.BinDir, "k9s")

					// Check if k9s exists
					if _, err := os.Stat(k9sPath); os.IsNotExist(err) {
						stackID := stack.GetStackID(cfg)
						l, _ := logging.NewSlogLogger(logging.LoggerConfig{
							StateDir: cfg.StateDir,
							StackID:  stackID,
						})
						l.Error("k9s not found", "path", cfg.BinDir)
						return err
					}

					stackID := stack.GetStackID(cfg)

					// Create logger and executor
					l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
						StateDir: cfg.StateDir,
						StackID:  stackID,
					})
					defer cleanup()

					exec := executor.New(l.Logger)
					cmd := exec.CommandWithOutput(k9sPath, c.Args().Slice()...)
					cmd.SetEnv(append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath)))
					cmd.SetStdin(os.Stdin)

					if err := cmd.Run(); err != nil {
						l.Error("k9s command failed", "error", err.Error())
						return err
					}
					return nil
				},
			},
			// ============================================================
			// Utility Commands
			// ============================================================
			{
				Name:  "version",
				Usage: "Show detailed version information",
				Action: func(c *cli.Context) error {
					fmt.Print(version.BuildInfo())
					return nil
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
			{
				Name:  "app",
				Usage: "Manage applications",
				Subcommands: []*cli.Command{
					{
						Name:      "list",
						Usage:     "List available applications",
						ArgsUsage: " ",
						Action: func(c *cli.Context) error {
							return app.List(cfg, embed.GetApplicationsFS())
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
							return app.Install(cfg, embed.GetApplicationsFS(), appName, c.Bool("force"))
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
							return app.Edit(cfg, appName)
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
							return app.Sync(cfg, appName)
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
							return app.Delete(cfg, appName, c.Bool("force"))
						},
					},
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
