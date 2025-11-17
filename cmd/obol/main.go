package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
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
     stack purge     Delete stack config (use --force to also delete data)

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
			// Hidden Bootstrap Command (for installer)
			// ============================================================
			bootstrapCommand(cfg),
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
						Usage: "Delete stack config (data preserved by default)",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Also delete persistent data",
							},
						},
						Action: func(c *cli.Context) error {
							if err := stack.Purge(cfg, c.Bool("force")); err != nil {
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
						return fmt.Errorf("stack not running, use 'obol stack up' first")
					}

					kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

					// Check if kubectl exists
					if _, err := os.Stat(kubectlPath); os.IsNotExist(err) {
						return fmt.Errorf("kubectl not found at %s", cfg.BinDir)
					}

					// Execute kubectl directly with KUBECONFIG set
					cmd := exec.Command(kubectlPath, c.Args().Slice()...)
					cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					if err := cmd.Run(); err != nil {
						// Preserve the exit code from kubectl
						if exitErr, ok := err.(*exec.ExitError); ok {
							if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
								os.Exit(status.ExitStatus())
							}
						}
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
						return fmt.Errorf("stack not running, use 'obol stack up' first")
					}

					helmPath := filepath.Join(cfg.BinDir, "helm")

					// Check if helm exists
					if _, err := os.Stat(helmPath); os.IsNotExist(err) {
						return fmt.Errorf("helm not found at %s", cfg.BinDir)
					}

					// Execute helm directly with KUBECONFIG set
					cmd := exec.Command(helmPath, c.Args().Slice()...)
					cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					if err := cmd.Run(); err != nil {
						// Preserve the exit code from helm
						if exitErr, ok := err.(*exec.ExitError); ok {
							if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
								os.Exit(status.ExitStatus())
							}
						}
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
						return fmt.Errorf("stack not running, use 'obol stack up' first")
					}

					helmfilePath := filepath.Join(cfg.BinDir, "helmfile")

					// Check if helmfile exists
					if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
						return fmt.Errorf("helmfile not found at %s", cfg.BinDir)
					}

					// Execute helmfile directly with KUBECONFIG and HELMFILE_FILE_PATH set
					helmfileConfigPath := filepath.Join(cfg.ConfigDir, "helmfile.yaml")
					cmd := exec.Command(helmfilePath, c.Args().Slice()...)
					cmd.Env = append(os.Environ(),
						fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
						fmt.Sprintf("HELMFILE_FILE_PATH=%s", helmfileConfigPath),
					)
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					if err := cmd.Run(); err != nil {
						// Preserve the exit code from helmfile
						if exitErr, ok := err.(*exec.ExitError); ok {
							if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
								os.Exit(status.ExitStatus())
							}
						}
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
						return fmt.Errorf("stack not running, use 'obol stack up' first")
					}

					k9sPath := filepath.Join(cfg.BinDir, "k9s")

					// Check if k9s exists
					if _, err := os.Stat(k9sPath); os.IsNotExist(err) {
						return fmt.Errorf("k9s not found at %s", cfg.BinDir)
					}

					// Execute k9s directly with KUBECONFIG set
					cmd := exec.Command(k9sPath, c.Args().Slice()...)
					cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
					cmd.Stdin = os.Stdin
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr

					if err := cmd.Run(); err != nil {
						// Preserve the exit code from k9s
						if exitErr, ok := err.(*exec.ExitError); ok {
							if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
								os.Exit(status.ExitStatus())
							}
						}
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
			networkCommand(cfg),
			{
				Name:  "app",
				Usage: "Manage applications",
				Subcommands: []*cli.Command{
					{
						Name:      "install",
						Usage:     "Install a Helm chart as an application",
						ArgsUsage: "<chart-url> [--values <override.yaml>]",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:    "values",
								Aliases: []string{"v"},
								Usage:   "Path to values override file",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("chart URL required (e.g., obol/ethereum or ethereum-helm-charts/ethereum-node)")
							}
							chartURL := c.Args().First()
							valuesOverride := c.String("values")
							// Parse chart URL: repo/chart -> repo and chart
							parts := strings.SplitN(chartURL, "/", 2)
							if len(parts) != 2 {
								return fmt.Errorf("invalid chart URL format, use: <repo>/<chart>")
							}
							return app.Install(cfg, parts[1], parts[0], valuesOverride)
						},
					},
					{
						Name:      "edit",
						Usage:     "Edit application helmfile or values",
						ArgsUsage: "<app-path>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application path required (e.g., obol/ethereum)")
							}
							appPath := c.Args().First()
							return app.Edit(cfg, appPath)
						},
					},
					{
						Name:      "sync",
						Usage:     "Deploy application to cluster via helmfile",
						ArgsUsage: "<app-path>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application path required (e.g., obol/ethereum)")
							}
							appPath := c.Args().First()
							return app.Sync(cfg, appPath)
						},
					},
					{
						Name:      "delete",
						Usage:     "Remove application and clean up cluster resources",
						ArgsUsage: "<app-path>",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Skip confirmation prompt",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("application path required (e.g., obol/ethereum)")
							}
							appPath := c.Args().First()
							return app.Delete(cfg, appPath, c.Bool("force"))
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
