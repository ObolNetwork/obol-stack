package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	"github.com/ObolNetwork/obol-stack/internal/tools"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v3"
)

func main() {
	// Load config with XDG defaults
	cfg := config.Load()
	cliApp := newRootCommand(cfg)

	if err := cliApp.Run(context.Background(), os.Args); err != nil {
		// Use the UI instance for colored error output if available.
		u, _ := cliApp.Metadata["ui"].(*ui.UI)
		if u == nil {
			u = ui.New(false)
		}

		// Contextual cluster-down message based on the command the user ran.
		if msg := kubectl.FormatClusterDownError(err, os.Args); msg != "" {
			u.Error(msg)
		} else {
			u.Error(err.Error())
		}
		os.Exit(1)
	}
}

// newRootCommand builds the full obol command tree. Help layout, completion
// and unknown-command suggestions are wired by configureCLI (help.go).
func newRootCommand(cfg *config.Config) *cli.Command {
	cliApp := &cli.Command{
		Name:    "obol",
		Usage:   "Obol Stack Management CLI",
		Version: version.Full(),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Show detailed output (subprocess logs, extra detail in list/status/info commands)",
				Sources: cli.EnvVars("OBOL_VERBOSE"),
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "Suppress all output except errors and warnings",
				Sources: cli.EnvVars("OBOL_QUIET"),
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output format: human or json",
				Value:   "human",
				Sources: cli.EnvVars("OBOL_OUTPUT"),
			},
			&cli.BoolFlag{
				// OBOL_NO_BROWSER is read by ui.PlanBrowserOpen directly
				// (any non-empty value except 0/false/no), not bound here.
				Name:  "no-open",
				Usage: "Never open a browser (or set OBOL_NO_BROWSER=1); links are still printed",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			outputMode, err := ui.ParseOutputMode(cmd.String("output"))
			if err != nil {
				// Backup/export commands used to take a local --output
				// <path>; it is now --file (global --output is the format).
				return ctx, fmt.Errorf("%w; to write an export or backup to a path, use --file", err)
			}
			u := ui.NewWithAllOptions(cmd.Bool("verbose"), cmd.Bool("quiet"), outputMode)
			u.SetNoBrowser(cmd.Bool("no-open"))
			cmd.Metadata = map[string]any{"ui": u}

			return ctx, nil
		},
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
				Commands: []*cli.Command{
					{
						Name:  "init",
						Usage: "Initialize stack configuration",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Force overwrite existing configuration",
							},
							&cli.StringFlag{
								Name:    "backend",
								Usage:   "Cluster backend: k3d (Docker-based) or k3s (bare-metal)",
								Sources: cli.EnvVars("OBOL_BACKEND"),
							},
							&cli.BoolFlag{
								Name:    "yes",
								Aliases: []string{"y"},
								Usage:   "Skip the live-services confirmation prompt when --force switches backends (required in non-interactive shells when offers are running)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return stack.Init(cfg, getUI(cmd), cmd.Bool("force"), cmd.String("backend"), cmd.Bool("yes"))
						},
					},
					{
						Name:  "up",
						Usage: "Start the Obol Stack",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "wildcard-dns",
								Usage: "Configure wildcard *.obol.stack DNS via NetworkManager/dnsmasq (Linux) or /etc/resolver (macOS)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							u := getUI(cmd)
							if err := stack.Up(cfg, u, cmd.Bool("wildcard-dns")); err != nil {
								return err
							}
							// Replay recorded remote RPC upstreams into the
							// (possibly fresh) eRPC ConfigMap. Best-effort.
							network.ReconcileRecordedRPCs(cfg, u)
							// Re-apply durable multi-upstream eRPC operator
							// overlays (baskets/scoring/rate-limits) AFTER
							// simple recorded remotes. Best-effort.
							// See ObolNetwork/obol-stack#763.
							network.ReconcileERPCOverlay(cfg, u)
							// Re-apply recorded Agent CRs BEFORE sell offers:
							// agent-backed ServiceOffers resolve agent.ref and
							// would dangle without their Agent. Best-effort.
							agentcrd.ResumeAll(cfg, u)
							// Replay recorded storefront branding into the
							// (fresh) x402/obol-storefront-profile ConfigMap
							// BEFORE offers republish, so the controller's first
							// catalog rebuild carries the operator's branding.
							// Best-effort. No-op when `obol sell info set` was
							// never used.
							storefront.ReconcileRecorded(cfg, u)
							// Re-sync installed app deployments BEFORE sell
							// offers: `obol sell http` offers can gate an
							// app's Service as their upstream, and the
							// controller's upstream health check needs it
							// present. App state is declarative on disk
							// (helmfile.yaml + values.yaml) but its cluster
							// resources live in etcd. Best-effort.
							app.ResumeAll(cfg, u)
							// Re-apply cluster-side state for locally-persisted
							// `obol sell *` offers. ServiceOffer CRs and the
							// Service/Endpoints that route to the host gateway
							// live in etcd, which is destroyed by `obol stack
							// down`, so a fresh `stack up` would otherwise come
							// back with the descriptors still on disk but no
							// matching cluster resources. Best-effort: a resume
							// failure does not block stack-up.
							if err := resumeSellOffers(ctx, cfg, u); err != nil {
								u.Warnf("Could not resume sell offers: %v", err)
							}
							printStackUpLinks(cfg, u)
							return nil
						},
					},
					{
						Name:  "down",
						Usage: "Stop the Obol Stack",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "yes",
								Aliases: []string{"y"},
								Usage:   "Skip the live-services confirmation prompt (required in non-interactive shells when offers are running)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return stack.Down(cfg, getUI(cmd), cmd.Bool("yes"))
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
							&cli.BoolFlag{
								Name:    "yes",
								Aliases: []string{"y"},
								Usage:   "Skip the live-services confirmation prompt (required in non-interactive shells when offers are running)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return stack.Purge(cfg, getUI(cmd), cmd.Bool("force"), cmd.Bool("yes"))
						},
					},
					stackExportCommand(cfg),
					stackImportCommand(cfg),
				},
			},
			// ============================================================
			// Obol Agent Commands
			// ============================================================
			agentCommand(cfg),
			walletCommand(cfg),
			// ============================================================
			// Tunnel & Domain Commands
			// ============================================================
			tunnelCommand(cfg),
			domainCommand(cfg),
			// ============================================================
			// Kubernetes Tool Passthroughs (with auto-configured KUBECONFIG)
			// ============================================================
			passthroughCommand(cfg, "kubectl", nil),
			passthroughCommand(cfg, "helm", nil),
			passthroughCommand(cfg, "helmfile", func(cfg *config.Config) []string {
				return []string{"HELMFILE_FILE_PATH=" + filepath.Join(cfg.ConfigDir, "helmfile.yaml")}
			}),
			passthroughCommand(cfg, "k9s", nil),
			// ============================================================
			// Utility Commands
			// ============================================================
			{
				Name:  "version",
				Usage: "Show detailed version information",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					if u.IsJSON() {
						result := struct {
							Version   string `json:"version"`
							GitCommit string `json:"git_commit"`
							BuildTime string `json:"build_time"`
							GitDirty  string `json:"git_dirty"`
							GoVersion string `json:"go_version,omitempty"`
						}{
							Version:   version.Version,
							GitCommit: version.GitCommit,
							BuildTime: version.BuildTime,
							GitDirty:  version.GitDirty,
						}
						if bi, ok := debugReadBuildInfo(); ok {
							result.GoVersion = bi
						}
						return u.JSON(result)
					}
					// Version output should always be unformatted for parseability.
					fmt.Print(version.BuildInfo())
					return nil
				},
			},
			updateCommand(cfg),
			upgradeCommand(cfg),
			networkCommand(cfg),
			hermesCommand(cfg),
			openclawCommand(cfg),
			sellCommand(cfg),
			buyCommand(cfg),
			modelCommand(cfg),
			{
				Name:  "app",
				Usage: "Manage applications",
				Commands: []*cli.Command{
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
							&cli.StringSliceFlag{
								Name:  "values",
								Usage: "Values file merged onto chart defaults (repeatable, merged in order)",
							},
							&cli.StringSliceFlag{
								Name:  "set",
								Usage: "Override a value, e.g. --set image.tag=1.2.3 (repeatable, applied after --values)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.NArg() == 0 {
								return errors.New("chart reference required\n\n" +
									"Examples:\n" +
									"  obol app install bitnami/redis\n" +
									"  obol app install bitnami/postgresql@15.0.0\n" +
									"  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz\n" +
									"  obol app install oci://registry-1.docker.io/bitnamicharts/redis\n\n" +
									"Find charts at https://artifacthub.io")
							}

							chartRef := cmd.Args().First()
							opts := app.InstallOptions{
								Name:        cmd.String("name"),
								Version:     cmd.String("version"),
								ID:          cmd.String("id"),
								Force:       cmd.Bool("force"),
								ValuesFiles: cmd.StringSlice("values"),
								Set:         cmd.StringSlice("set"),
							}

							return app.Install(cfg, getUI(cmd), chartRef, opts)
						},
					},
					{
						Name:      "sync",
						Usage:     "Deploy application to cluster",
						ArgsUsage: "[<app>/<id>]",
						Flags: []cli.Flag{
							&cli.StringSliceFlag{
								Name:  "values",
								Usage: "Values file merged into the deployment's values.yaml before syncing (repeatable)",
							},
							&cli.StringSliceFlag{
								Name:  "set",
								Usage: "Override a value before syncing, e.g. --set image.tag=1.2.3 (repeatable)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							identifier, _, err := app.ResolveInstance(cfg, cmd.Args().Slice())
							if err != nil {
								return err
							}

							u := getUI(cmd)
							// Overrides are persisted into the deployment's
							// values.yaml (not passed as ephemeral flags) so
							// resume-on-stack-up replays them.
							if err := app.ApplyOverrides(cfg, identifier, cmd.StringSlice("values"), cmd.StringSlice("set")); err != nil {
								return err
							}

							if err := app.Sync(cfg, u, identifier); err != nil {
								return err
							}

							app.PrintSellHint(cfg, u, identifier)

							return nil
						},
					},
					{
						Name:  "list",
						Usage: "List installed applications (global --verbose adds detail)",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							opts := app.ListOptions{
								Verbose: getUI(cmd).IsVerbose(),
							}

							return app.List(cfg, getUI(cmd), opts)
						},
					},
					{
						Name:      "delete",
						Usage:     "Remove application and cluster resources",
						ArgsUsage: "[<app>/<id>]",
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Skip confirmation prompt",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							identifier, _, err := app.ResolveInstance(cfg, cmd.Args().Slice())
							if err != nil {
								return err
							}

							return app.Delete(cfg, getUI(cmd), identifier, cmd.Bool("force"))
						},
					},
				},
			},
		},
	}

	configureCLI(cliApp)

	return cliApp
}

// getUI extracts the *ui.UI from the CLI command's root metadata.
func getUI(cmd *cli.Command) *ui.UI {
	root := cmd.Root()
	if root != nil && root.Metadata != nil {
		if u, ok := root.Metadata["ui"].(*ui.UI); ok {
			return u
		}
	}

	return ui.New(false)
}

// debugReadBuildInfo returns the Go version from runtime/debug.ReadBuildInfo.
func debugReadBuildInfo() (string, bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return bi.GoVersion, true
}

// passthroughCommand builds a CLI command that execs a bundled tool with
// KUBECONFIG pre-set. extraEnv, if non-nil, yields additional env vars at run time.
func passthroughCommand(cfg *config.Config, tool string, extraEnv func(*config.Config) []string) *cli.Command {
	return &cli.Command{
		Name:            tool,
		Usage:           "Run " + tool + " with stack kubeconfig (passthrough)",
		SkipFlagParsing: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				return errors.New("stack not running, use 'obol stack up' first")
			}
			toolPath := cfg.ToolPath(tool)
			if _, err := os.Stat(toolPath); os.IsNotExist(err) {
				return tools.MissingError(tool)
			}

			proc := exec.Command(toolPath, cmd.Args().Slice()...)
			env := append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
			if extraEnv != nil {
				env = append(env, extraEnv(cfg)...)
			}
			proc.Env = env
			proc.Stdin, proc.Stdout, proc.Stderr = os.Stdin, os.Stdout, os.Stderr

			if err := proc.Run(); err != nil {
				exitErr := &exec.ExitError{}
				if errors.As(err, &exitErr) {
					if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
						os.Exit(status.ExitStatus())
					}
				}
				return err
			}
			return nil
		},
	}
}
