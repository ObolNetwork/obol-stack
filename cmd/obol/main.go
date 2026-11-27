package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/images"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/passthrough"
	"github.com/ObolNetwork/obol-stack/internal/replay"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/stackversion"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v3"
)

func main() {
	// Load config with XDG defaults
	cfg := config.Load()
	cliApp := newRootCommand(cfg)

	if err := cliApp.Run(context.Background(), os.Args); err != nil {
		// A passthrough tool's exit status (normally already applied by
		// urfave's ExitCoder handling) is propagated without extra output.
		var exitErr *passthrough.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}

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

// notifiesVersionSkew reports whether the top-level command should say the
// stack has not been brought up by this CLI version yet. Skipped where the
// notice is noise or the command is the fix itself (stack up), reports it
// already (doctor), or must keep its output clean (passthroughs).
func notifiesVersionSkew(command string) bool {
	return command != "" && !slices.Contains(versionSkewQuietCommands, command)
}

var versionSkewQuietCommands = strings.Fields(
	"help h version completion stack upgrade update doctor bootstrap kubectl helm helmfile k9s env")

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

			if !u.IsJSON() && !cmd.Bool("quiet") && notifiesVersionSkew(cmd.Args().First()) {
				if stackVersion, skewed := stackversion.Skew(cfg); skewed {
					u.Warn(stackversion.SkewMessage(stackVersion))
				}
			}

			return ctx, checkGlobalFlagsBeforePassthrough(cmd)
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
								Usage:   "Confirm destroying the old cluster when --force switches backends (required in non-interactive shells)",
							},
							&cli.StringFlag{
								Name:    "node-memory",
								Usage:   "Memory cap for the k3d node, without swap: a size such as 22g, auto (Docker's memory minus 4-8 GiB) or off. Applies when the cluster is created",
								Sources: cli.EnvVars(stack.NodeMemoryEnv),
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return stack.Init(cfg, getUI(cmd), cmd.Bool("force"), cmd.String("backend"), cmd.Bool("yes"), cmd.String("node-memory"))
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
							&cli.BoolFlag{
								Name:  "reset-defaults",
								Usage: "Overwrite hand-edited files in the defaults tree with the shipped defaults (edited files are backed up first)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							u := getUI(cmd)
							if err := runStackUp(ctx, cfg, u, cmd.Bool("wildcard-dns"), cmd.Bool("reset-defaults")); err != nil {
								return err
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
								Usage:   "Skip the confirmation prompt (required in non-interactive shells when offers are running or the cluster cannot be checked)",
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
			// ============================================================
			// Kubernetes Tool Passthroughs (with auto-configured KUBECONFIG)
			// ============================================================
			passthroughCommand(cfg, "kubectl", nil),
			passthroughCommand(cfg, "helm", nil),
			passthroughCommand(cfg, "helmfile", helmfilePrepare),
			passthroughCommand(cfg, "k9s", nil),
			envCommand(cfg),
			doctorCommand(cfg),
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
							ImageTag  string `json:"image_tag"`
							GoVersion string `json:"go_version,omitempty"`
						}{
							Version:   version.Version,
							GitCommit: version.GitCommit,
							BuildTime: version.BuildTime,
							GitDirty:  version.GitDirty,
							ImageTag:  images.PinTag(),
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
  repo/chart          A local helm repo alias (obol helm repo list), the
                      built-in alias "obol" (Obol's chart repo), else ArtifactHub
  repo/chart@version  Specific version (e.g., obol/dv-pod@0.24.0)
  https://.../*.tgz   Direct URL to chart archive
  oci://...           OCI registry reference

The deployment lives in namespace <name>-<id> and the helm release defaults to
<name>-<id> too (--namespace and --release override them). values.yaml holds only your
overrides (--values/--set); charts that ship values-examples/obol-stack.yaml
(dv-pod) start from it. An existing helm release of the same name that obol
app doesn't manage is refused unless --adopt; to take over a dv-pod installed
with helm: --namespace dv-pod --release my-dv-pod --adopt.

Examples:
  obol app install obol/dv-pod@0.24.0 --name dv-pod --id hoodi --set network=hoodi --values my-dv.yaml
  obol app install bitnami/redis
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
							&cli.StringFlag{
								Name:  appNamespaceFlag,
								Usage: "Kubernetes namespace (default <name>-<id>; stack namespaces are refused)",
							},
							&cli.StringFlag{
								Name:  "release",
								Usage: "Helm release name (default <name>-<id>; with --force, the existing release)",
							},
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Overwrite existing deployment",
							},
							&cli.BoolFlag{
								Name:  "adopt",
								Usage: "Take over an existing helm release of the same name, seeding values.yaml from its values",
							},
							&cli.StringSliceFlag{
								Name:  "values",
								Usage: "Values file merged into values.yaml (repeatable, merged in order)",
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
								Release:     cmd.String("release"),
								Namespace:   cmd.String(appNamespaceFlag),
								Force:       cmd.Bool("force"),
								Adopt:       cmd.Bool("adopt"),
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
						Description: `Deploys values.yaml with helmfile. Refuses when the live helm release was
changed outside obol app (helm upgrade/rollback since obol's last sync),
naming the keys that differ; fold them into values.yaml or pass --force.`,
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "force",
								Usage: "Sync even if the release was changed outside obol app (reverts those changes)",
							},
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
							// resume-on-stack-up replays them. Sync merges
							// them after its drift check.
							syncOpts := app.SyncOptions{
								Force:       cmd.Bool("force"),
								ValuesFiles: cmd.StringSlice("values"),
								Set:         cmd.StringSlice("set"),
							}
							if err := app.SyncWithOptions(cfg, u, identifier, syncOpts); err != nil {
								return err
							}

							app.PrintSellHint(cfg, u, identifier)

							return nil
						},
					},
					{
						Name:      appUpgradeCmd,
						Usage:     "Move an app to another chart version and sync it",
						ArgsUsage: "[<app>/<id>]",
						Description: `Sets version: in the app's helmfile.yaml and runs 'obol app sync' (with its
drift check; a refused sync restores the old version). values.yaml is not
changed. Warns when values.yaml is a pre-v0.19 full copy of the old chart's
defaults, whose image tags would stay pinned.

Example:
  obol app upgrade dv-pod/hoodi --version 0.24.0`,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:     "version",
								Usage:    "Target chart version",
								Required: true,
							},
							&cli.BoolFlag{
								Name:  "force",
								Usage: "Sync even if the release was changed outside obol app (reverts those changes)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							identifier, _, err := app.ResolveInstance(cfg, cmd.Args().Slice())
							if err != nil {
								return err
							}

							u := getUI(cmd)
							upgradeOpts := app.UpgradeOptions{Version: cmd.String("version"), Force: cmd.Bool("force")}
							if err := app.Upgrade(cfg, u, identifier, upgradeOpts); err != nil {
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
						Description: `Deletes the app's namespace and config directory.

If the namespace holds PersistentVolumeClaims or Secrets that look like key
material (dv-pod key shares and ENR keys, validator keystores), only the helm
release is removed: the namespace, its PVCs and Secrets and the config
directory stay, and 'obol app sync' redeploys onto them. --delete-data
deletes everything; on a terminal you type the app name to confirm, without
one pass --delete-data --yes.`,
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:    "force",
								Aliases: []string{"f"},
								Usage:   "Skip the confirmation prompt (never deletes data on its own)",
							},
							&cli.BoolFlag{
								Name:  "delete-data",
								Usage: "Also delete PVCs and key-material Secrets (key shares are lost)",
							},
							&cli.BoolFlag{
								Name:  "keep-data",
								Usage: "Remove only the helm release; keep the namespace, PVCs, Secrets and config",
							},
							&cli.BoolFlag{
								Name:  "yes",
								Usage: "Confirm --delete-data without typing the app name",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							identifier, _, err := app.ResolveInstance(cfg, cmd.Args().Slice())
							if err != nil {
								return err
							}

							return app.Delete(cfg, getUI(cmd), identifier, app.DeleteOptions{
								Force:      cmd.Bool("force"),
								DeleteData: cmd.Bool("delete-data"),
								KeepData:   cmd.Bool("keep-data"),
								Yes:        cmd.Bool("yes"),
							})
						},
					},
					appSecretCommand(cfg),
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

// runStackUp is the full `obol stack up`: cluster + infra, then replay of every
// recorded piece of operator state. Shared with `obol stack import`, which
// brings a restored stack up through exactly this path.
func runStackUp(ctx context.Context, cfg *config.Config, u *ui.UI, wildcardDNS, resetDefaults bool) error {
	if err := stack.Up(cfg, u, wildcardDNS, resetDefaults); err != nil {
		return err
	}
	// Replay every recorded piece of operator state (models, networks, RPC
	// upstreams + eRPC overlay, x402 pricing, AgentIdentity, agent
	// instances, Agent CRs, storefront, apps, then sell offers) in the one
	// dependency order shared with `obol sell resume`. The ordering rules
	// (overlay after recorded RPCs #763; Agent CRs, storefront and apps
	// before offers) are documented and test-pinned on internal/replay.Order.
	// Best-effort per step: a replay failure never fails stack-up.
	replay.ReplayRecorded(ctx, cfg, u, replayOptions())
	stack.PrintReady(cfg, u)
	return nil
}
