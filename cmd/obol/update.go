package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/update"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/urfave/cli/v2"
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
		Action: func(c *cli.Context) error {
			kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
			clusterRunning := true
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				clusterRunning = false
			}

			jsonMode := c.Bool("json")

			if !jsonMode && clusterRunning {
				fmt.Println("Updating helm repositories...")
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
					fmt.Printf("  Warning: %s\n", result.HelmError)
				} else if result.HelmRepoUpdated {
					fmt.Println("  ✓ Helm repositories updated")
				}

				if len(result.ChartStatuses) > 0 {
					fmt.Println("\nChecking chart versions...")
					update.PrintUpdateTable(result.ChartStatuses)
				}
			} else {
				fmt.Println("Helm check skipped (cluster not running)")
			}

			// Print CLI status
			fmt.Println("\nChecking CLI version...")
			if result.CLIError != "" {
				fmt.Printf("  Warning: %s\n", result.CLIError)
			} else {
				update.PrintCLIStatus(version.Short(), result.CLIRelease, result.IsDev)
			}

			// Print summary
			update.PrintUpdateSummary(result)

			return nil
		},
	}
}

func upgradeCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "upgrade",
		Usage: "Apply available helm chart upgrades to the running stack",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "defaults-only",
				Usage: "Only upgrade default infrastructure, skip networks and apps",
			},
		},
		Action: func(c *cli.Context) error {
			kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
			if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
				return fmt.Errorf("stack not running, use 'obol stack up' first")
			}

			return update.ApplyUpgrades(cfg, c.Bool("defaults-only"))
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
