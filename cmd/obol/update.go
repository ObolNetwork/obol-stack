package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/update"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v3"
)

func updateCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "Check for available updates to helm charts and the obol CLI",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Output results as JSON",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

			clusterRunning := true
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				clusterRunning = false
			}

			jsonMode := cmd.Bool("json")

			if !jsonMode && clusterRunning {
				u.Info("Updating helm repositories...")
			}

			result, err := update.CheckForUpdates(cfg, clusterRunning, jsonMode)
			if err != nil {
				return err
			}

			if jsonMode {
				return printUpdateJSON(result)
			}

			// Print helm results
			if clusterRunning {
				if result.HelmError != "" {
					u.Warnf("%s", result.HelmError)
				} else if result.HelmRepoUpdated {
					u.Success("Helm repositories updated")
				}

				if len(result.ChartStatuses) > 0 {
					u.Blank()
					u.Info("Checking chart versions...")
					update.PrintUpdateTable(u, result.ChartStatuses)
				}
			} else {
				u.Dim("Helm check skipped (cluster not running)")
			}

			// Print CLI status
			u.Blank()
			u.Info("Checking CLI version...")

			if result.CLIError != "" {
				u.Warnf("%s", result.CLIError)
			} else {
				update.PrintCLIStatus(u, version.Short(), result.CLIRelease, result.IsDev)
			}

			// Print summary
			update.PrintUpdateSummary(u, result)

			return nil
		},
	}
}

func upgradeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "upgrade",
		Usage:     "Apply available helm chart upgrades to the running stack",
		ArgsUsage: "[chart-name]",
		Description: `Upgrade all charts, or a single chart by name.

Examples:
  obol upgrade                       Upgrade everything
  obol upgrade obol/remote-signer    Upgrade only obol/remote-signer
  obol upgrade traefik/traefik       Upgrade only traefik`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "defaults-only",
				Usage: "Only upgrade default infrastructure, skip networks and apps",
			},
			&cli.BoolFlag{
				Name:  "pinned",
				Usage: "Deploy only the versions embedded in the binary, without bumping to latest",
			},
			&cli.BoolFlag{
				Name:  "major",
				Usage: "Allow upgrading across major version boundaries (may include breaking changes)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				return errors.New("stack not running, use 'obol stack up' first")
			}

			chartFilter := ""
			if cmd.NArg() > 0 {
				chartFilter = cmd.Args().First()
			}

			return update.ApplyUpgrades(cfg, getUI(cmd), update.UpgradeOptions{
				DefaultsOnly: cmd.Bool("defaults-only"),
				Pinned:       cmd.Bool("pinned"),
				Major:        cmd.Bool("major"),
				ChartFilter:  chartFilter,
			})
		},
	}
}

// jsonOutput is the structured JSON output for `obol update --json`
type jsonOutput struct {
	Charts []jsonChart `json:"charts,omitempty"`
	CLI    *jsonCLI    `json:"cli,omitempty"`
}

type jsonChart struct {
	Chart  string `json:"chart"`
	Pinned string `json:"pinned"`
	Latest string `json:"latest"`
	Status string `json:"status"`
}

type jsonCLI struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Status  string `json:"status"`
}

func printUpdateJSON(result *update.UpdateResult) error {
	out := jsonOutput{}

	for _, s := range result.ChartStatuses {
		out.Charts = append(out.Charts, jsonChart{
			Chart:  s.Chart,
			Pinned: s.Pinned,
			Latest: s.Latest,
			Status: s.Status,
		})
	}

	if result.CLIRelease != nil {
		status := "up_to_date"
		if result.IsDev {
			status = "development"
		} else if result.CLIUpdateAvail {
			status = "update_available"
		}

		out.CLI = &jsonCLI{
			Current: version.Short(),
			Latest:  result.CLIRelease.Version,
			Status:  status,
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	return enc.Encode(out)
}
