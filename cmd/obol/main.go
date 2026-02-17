package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/agent"
	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
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
   Obol Agent:
     agent init      Initialize the Obol Agent
   Network Management:
     network list    List available networks
     network install Install and deploy network to cluster
     network delete  Remove network and clean up cluster resources

   OpenClaw (AI Agent):
     openclaw onboard   Create and deploy an OpenClaw instance
     openclaw setup     Reconfigure model providers for a deployed instance
     openclaw dashboard Open the dashboard in a browser
     openclaw cli       Run openclaw CLI against a deployed instance
     openclaw sync      Deploy or update an instance
     openclaw token     Retrieve gateway token
     openclaw list      List instances
     openclaw delete    Remove instance and cluster resources
     openclaw skills    Manage skills (sync from local dir)

   Model Providers:
     model setup        Configure cloud AI provider in llmspy gateway
     model status       Show global llmspy provider status

   Inference (x402 Pay-Per-Request):
     inference serve  Start the x402 inference gateway

   App Management:
     app install     Install a Helm chart as an application
     app list        List installed applications
     app sync        Deploy application to cluster
     app delete      Remove application and cluster resources

   Tunnel Management:
     tunnel status    Show tunnel status and public URL
     tunnel login     Authenticate and create persistent tunnel (browser)
     tunnel provision Provision persistent tunnel (API token)
     tunnel restart   Restart tunnel connector (quick tunnels get new URL)
     tunnel logs      View cloudflared logs

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
						Usage: "Delete stack config (data preserved by default)",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Also delete persistent data",
							},
						},
						Action: func(c *cli.Context) error {
							return stack.Purge(cfg, c.Bool("force"))
						},
					},
				},
			},
			// ============================================================
			// Obol Agent Commands
			// ============================================================
			{
				Name:  "agent",
				Usage: "Manage Obol Agent",
				Subcommands: []*cli.Command{
					{
						Name:  "init",
						Usage: "Initialize the Obol Agent",
						Action: func(c *cli.Context) error {
							return agent.Init(cfg)
						},
					},
				},
			},
			// ============================================================
			// Tunnel Management Commands
			// ============================================================
			{
				Name:  "tunnel",
				Usage: "Manage Cloudflare tunnel for public access",
				Subcommands: []*cli.Command{
					{
						Name:  "status",
						Usage: "Show tunnel status and public URL",
						Action: func(c *cli.Context) error {
							return tunnel.Status(cfg)
						},
					},
					{
						Name:  "login",
						Usage: "Authenticate via browser and create a locally-managed tunnel (no API token)",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "hostname",
								Aliases:  []string{"H"},
								Usage:    "Public hostname to route (e.g. stack.example.com)",
								Required: true,
							},
						},
						Action: func(c *cli.Context) error {
							return tunnel.Login(cfg, tunnel.LoginOptions{
								Hostname: c.String("hostname"),
							})
						},
					},
					{
						Name:  "provision",
						Usage: "Provision a persistent (DNS-routed) Cloudflare Tunnel",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "hostname",
								Aliases:  []string{"H"},
								Usage:    "Public hostname to route (e.g. stack.example.com)",
								Required: true,
							},
							&cli.StringFlag{
								Name:    "account-id",
								Aliases: []string{"a"},
								Usage:   "Cloudflare account ID (or set CLOUDFLARE_ACCOUNT_ID)",
								EnvVars: []string{"CLOUDFLARE_ACCOUNT_ID"},
							},
							&cli.StringFlag{
								Name:    "zone-id",
								Aliases: []string{"z"},
								Usage:   "Cloudflare zone ID for the hostname (or set CLOUDFLARE_ZONE_ID)",
								EnvVars: []string{"CLOUDFLARE_ZONE_ID"},
							},
							&cli.StringFlag{
								Name:    "api-token",
								Aliases: []string{"t"},
								Usage:   "Cloudflare API token (or set CLOUDFLARE_API_TOKEN)",
								EnvVars: []string{"CLOUDFLARE_API_TOKEN"},
							},
						},
						Action: func(c *cli.Context) error {
							return tunnel.Provision(cfg, tunnel.ProvisionOptions{
								Hostname:  c.String("hostname"),
								AccountID: c.String("account-id"),
								ZoneID:    c.String("zone-id"),
								APIToken:  c.String("api-token"),
							})
						},
					},
					{
						Name:  "restart",
						Usage: "Restart the tunnel connector (quick tunnels get a new URL)",
						Action: func(c *cli.Context) error {
							return tunnel.Restart(cfg)
						},
					},
					{
						Name:  "logs",
						Usage: "View cloudflared logs",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "follow",
								Aliases: []string{"f"},
								Usage:   "Follow log output",
							},
						},
						Action: func(c *cli.Context) error {
							return tunnel.Logs(cfg, c.Bool("follow"))
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
			openclawCommand(cfg),
			inferenceCommand(cfg),
			modelCommand(cfg),
			{
				Name:  "app",
				Usage: "Manage applications",
				Subcommands: []*cli.Command{
					{
						Name:      "install",
						Usage:     "Install a Helm chart as an application",
						ArgsUsage: "<chart-reference>",
						Description: `Install a Helm chart as a managed application.

Supported chart reference formats:
  repo/chart          Resolved via ArtifactHub (e.g., bitnami/redis)
  repo/chart@version  Specific version (e.g., bitnami/redis@19.0.0)
  https://.../*.tgz   Direct URL to chart archive
  oci://...           OCI registry reference

Examples:
  obol app install bitnami/redis
  obol app install bitnami/postgresql@15.0.0
  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz
  obol app install oci://registry-1.docker.io/bitnamicharts/redis --name mydb --id production

Find charts at https://artifacthub.io`,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "name",
								Usage: "Application name (defaults to chart name)",
							},
							&cli.StringFlag{
								Name:  "version",
								Usage: "Chart version (defaults to latest)",
							},
							&cli.StringFlag{
								Name:  "id",
								Usage: "Deployment ID (defaults to generated petname)",
							},
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Overwrite existing deployment",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("chart reference required\n\n" +
									"Examples:\n" +
									"  obol app install bitnami/redis\n" +
									"  obol app install bitnami/postgresql@15.0.0\n" +
									"  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz\n" +
									"  obol app install oci://registry-1.docker.io/bitnamicharts/redis\n\n" +
									"Find charts at https://artifacthub.io")
							}
							chartRef := c.Args().First()
							opts := app.InstallOptions{
								Name:    c.String("name"),
								Version: c.String("version"),
								ID:      c.String("id"),
								Force:   c.Bool("force"),
							}
							return app.Install(cfg, chartRef, opts)
						},
					},
					{
						Name:      "sync",
						Usage:     "Deploy application to cluster",
						ArgsUsage: "<app>/<id>",
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("deployment identifier required (e.g., postgresql/eager-fox)")
							}
							return app.Sync(cfg, c.Args().First())
						},
					},
					{
						Name:  "list",
						Usage: "List installed applications",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "verbose",
								Aliases: []string{"v"},
								Usage:   "Show detailed information",
							},
						},
						Action: func(c *cli.Context) error {
							opts := app.ListOptions{
								Verbose: c.Bool("verbose"),
							}
							return app.List(cfg, opts)
						},
					},
					{
						Name:      "delete",
						Usage:     "Remove application and cluster resources",
						ArgsUsage: "<app>/<id>",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Skip confirmation prompt",
							},
						},
						Action: func(c *cli.Context) error {
							if c.NArg() == 0 {
								return fmt.Errorf("deployment identifier required (e.g., postgresql/eager-fox)")
							}
							return app.Delete(cfg, c.Args().First(), c.Bool("force"))
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
