package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

// UpdateResult holds the complete results of an update check
type UpdateResult struct {
	HelmRepoUpdated        bool
	ChartStatuses          []ChartStatus
	CLIRelease             *LatestRelease
	CLIUpdateAvail         bool
	ChartUpdatesAvail      bool
	ChartMajorUpdatesAvail bool
	IsDev                  bool
	HelmError              string
	CLIError               string
}

// CheckForUpdates runs all update checks and returns a unified result.
// If clusterRunning is false, skips helm-related checks and only checks CLI version.
// If quiet is true, suppresses helm stdout (useful for JSON output mode).
func CheckForUpdates(cfg *config.Config, clusterRunning bool, quiet bool) (*UpdateResult, error) {
	result := &UpdateResult{}

	// Check if this is a development build
	if version.Short() == "dev" {
		result.IsDev = true
	}

	// Helm checks (require running cluster)
	if clusterRunning {
		if err := UpdateHelmRepos(cfg, quiet); err != nil {
			result.HelmError = err.Error()
		} else {
			result.HelmRepoUpdated = true
		}

		statuses, err := CheckChartVersions(cfg)
		if err != nil {
			if result.HelmError == "" {
				result.HelmError = err.Error()
			}
		} else {
			result.ChartStatuses = statuses
			for _, s := range statuses {
				if s.Status == "Update available" {
					result.ChartUpdatesAvail = true
				} else if s.Status == "Major update available" {
					result.ChartMajorUpdatesAvail = true
				}
			}
		}
	}

	// CLI version check (always runs)
	release, err := CheckLatestRelease()
	if err != nil {
		result.CLIError = err.Error()
	} else {
		result.CLIRelease = release
		if !result.IsDev {
			if CompareVersions(version.Short(), release.Version) < 0 {
				result.CLIUpdateAvail = true
			}
		}
	}

	return result, nil
}

// ApplyUpgrades runs helmfile sync on defaults and all installed deployments.
// If pinned is true, only deploys the versions embedded in the binary without bumping to latest.
// If major is true, allows bumping across major version boundaries.
func ApplyUpgrades(cfg *config.Config, defaultsOnly bool, pinned bool, major bool) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// 1. Helm repo update
	fmt.Println("Updating helm repositories...")
	if err := UpdateHelmRepos(cfg, false); err != nil {
		return fmt.Errorf("failed to update helm repos: %w", err)
	}
	fmt.Println("  ✓ Helm repositories updated")

	// 2. Re-copy embedded defaults to pick up new chart versions from binary
	fmt.Println("\nRefreshing default infrastructure templates...")
	ollamaHost := "host.k3d.internal"
	if runtime.GOOS == "darwin" {
		ollamaHost = "host.docker.internal"
	}
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if err := embed.CopyDefaults(defaultsDir, map[string]string{
		"{{OLLAMA_HOST}}": ollamaHost,
	}); err != nil {
		return fmt.Errorf("failed to refresh defaults: %w", err)
	}
	fmt.Println("  ✓ Defaults updated from embedded assets")

	// 3. Bump chart version pins to latest (unless --pinned)
	if !pinned {
		if major {
			fmt.Println("\nBumping chart versions to latest (including major versions)...")
		} else {
			fmt.Println("\nBumping chart versions to latest (minor/patch only)...")
		}
		bumps, err := UpgradeHelmfileVersions(cfg, major)
		if err != nil {
			fmt.Printf("  Warning: failed to bump versions: %v\n", err)
		} else if len(bumps) > 0 {
			for _, b := range bumps {
				fmt.Printf("  %s: %s → %s\n", b.Chart, b.From, b.To)
			}
		} else {
			fmt.Println("  All chart versions already at latest.")
		}

		// Check if any major updates were skipped
		if !major {
			skipped := checkSkippedMajorUpdates(cfg)
			if len(skipped) > 0 {
				fmt.Println("\n  Major version updates available (skipped):")
				for _, s := range skipped {
					fmt.Printf("    %s: %s → %s\n", s.Chart, s.From, s.To)
				}
				fmt.Println("  Use 'obol upgrade --major' to apply major version updates.")
			}
		}
	} else {
		fmt.Println("\nUsing pinned versions from embedded binary (--pinned).")
	}

	// 4. Helmfile sync on defaults
	fmt.Println("\nUpgrading default infrastructure...")
	helmfilePath := filepath.Join(defaultsDir, "helmfile.yaml")
	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"sync",
	)
	helmfileCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	helmfileCmd.Stdout = os.Stdout
	helmfileCmd.Stderr = os.Stderr

	if err := helmfileCmd.Run(); err != nil {
		return fmt.Errorf("failed to upgrade default infrastructure: %w", err)
	}
	fmt.Println("  ✓ Default infrastructure upgraded")

	if !defaultsOnly {
		// 5. Re-sync installed networks
		fmt.Println("\nUpgrading installed networks...")
		if err := upgradeNetworks(cfg); err != nil {
			fmt.Printf("  Warning: %v\n", err)
		}

		// 6. Re-sync installed apps
		fmt.Println("\nUpgrading installed apps...")
		if err := upgradeApps(cfg); err != nil {
			fmt.Printf("  Warning: %v\n", err)
		}
	}

	fmt.Println("\n✓ All helm chart upgrades applied.")

	// 7. Check CLI version and hint if newer available
	release, err := CheckLatestRelease()
	if err == nil && version.Short() != "dev" {
		if CompareVersions(version.Short(), release.Version) < 0 {
			fmt.Printf("\nNote: A newer version of the obol CLI is available (v%s → %s).\n", version.Short(), release.TagName)
			fmt.Println("To update the CLI binary and dependencies, run:")
			fmt.Println()
			fmt.Println("  bash <(curl -s https://stack.obol.org)")
		}
	}

	return nil
}

// checkSkippedMajorUpdates checks the on-disk helmfile for charts where a major
// version update is available but was not applied. Best-effort, returns nil on error.
func checkSkippedMajorUpdates(cfg *config.Config) []VersionBump {
	statuses, err := CheckChartVersions(cfg)
	if err != nil {
		return nil
	}
	var skipped []VersionBump
	for _, s := range statuses {
		if s.Status == "Major update available" {
			skipped = append(skipped, VersionBump{
				Chart: s.Chart,
				From:  s.Pinned,
				To:    s.Latest,
			})
		}
	}
	return skipped
}

// upgradeNetworks iterates over installed network deployments and syncs each.
func upgradeNetworks(cfg *config.Config) error {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	if _, err := os.Stat(networksDir); os.IsNotExist(err) {
		fmt.Println("  No networks installed.")
		return nil
	}

	networkDirs, err := os.ReadDir(networksDir)
	if err != nil {
		return fmt.Errorf("failed to read networks directory: %w", err)
	}

	found := false
	for _, netDir := range networkDirs {
		if !netDir.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(networksDir, netDir.Name()))
		if err != nil {
			continue
		}
		for _, dep := range deployments {
			if !dep.IsDir() {
				continue
			}
			identifier := fmt.Sprintf("%s/%s", netDir.Name(), dep.Name())
			fmt.Printf("  Syncing %s...\n", identifier)
			if err := network.Sync(cfg, identifier); err != nil {
				fmt.Printf("  Warning: failed to sync %s: %v\n", identifier, err)
			} else {
				fmt.Printf("  ✓ %s upgraded\n", identifier)
			}
			found = true
		}
	}

	if !found {
		fmt.Println("  No networks installed.")
	}
	return nil
}

// upgradeApps iterates over installed applications and syncs each.
func upgradeApps(cfg *config.Config) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")
	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		fmt.Println("  No apps installed.")
		return nil
	}

	appDirs, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read applications directory: %w", err)
	}

	found := false
	for _, appDir := range appDirs {
		if !appDir.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(appsDir, appDir.Name()))
		if err != nil {
			continue
		}
		for _, dep := range deployments {
			if !dep.IsDir() {
				continue
			}
			identifier := fmt.Sprintf("%s/%s", appDir.Name(), dep.Name())
			fmt.Printf("  Syncing %s...\n", identifier)
			if err := app.Sync(cfg, identifier); err != nil {
				fmt.Printf("  Warning: failed to sync %s: %v\n", identifier, err)
			} else {
				fmt.Printf("  ✓ %s upgraded\n", identifier)
			}
			found = true
		}
	}

	if !found {
		fmt.Println("  No apps installed.")
	}
	return nil
}

// PrintUpdateTable prints a formatted table of chart statuses.
func PrintUpdateTable(statuses []ChartStatus) {
	if len(statuses) == 0 {
		return
	}

	// Calculate column widths
	chartW, pinnedW, latestW := len("Chart"), len("Pinned"), len("Latest")
	for _, s := range statuses {
		if len(s.Chart) > chartW {
			chartW = len(s.Chart)
		}
		if len(s.Pinned) > pinnedW {
			pinnedW = len(s.Pinned)
		}
		if len(s.Latest) > latestW {
			latestW = len(s.Latest)
		}
	}

	// Print header
	fmt.Printf("  %-*s  %-*s  %-*s  %s\n", chartW, "Chart", pinnedW, "Pinned", latestW, "Latest", "Status")

	// Print rows
	for _, s := range statuses {
		fmt.Printf("  %-*s  %-*s  %-*s  %s\n", chartW, s.Chart, pinnedW, s.Pinned, latestW, s.Latest, s.Status)
	}
}

// PrintCLIStatus prints the CLI version status line.
func PrintCLIStatus(current string, release *LatestRelease, isDev bool) {
	if release == nil {
		return
	}
	if isDev {
		fmt.Printf("  Obol CLI  %-10s  %-10s  Development build (skipped)\n", "dev", release.TagName)
		return
	}
	currentDisplay := "v" + strings.TrimPrefix(current, "v")
	status := "Up to date"
	if CompareVersions(current, release.Version) < 0 {
		status = "Update available"
	}
	fmt.Printf("  Obol CLI  %-10s  %-10s  %s\n", currentDisplay, release.TagName, status)
}

// PrintUpdateSummary prints the actionable summary at the end of `obol update`.
func PrintUpdateSummary(result *UpdateResult) {
	if !result.ChartUpdatesAvail && !result.ChartMajorUpdatesAvail && !result.CLIUpdateAvail {
		fmt.Println("\nEverything is up to date.")
		return
	}

	fmt.Println("\nSummary:")
	if result.ChartUpdatesAvail {
		count := 0
		for _, s := range result.ChartStatuses {
			if s.Status == "Update available" {
				count++
			}
		}
		fmt.Printf("  %d chart update(s) available. Run 'obol upgrade' to apply.\n", count)
	}
	if result.ChartMajorUpdatesAvail {
		count := 0
		for _, s := range result.ChartStatuses {
			if s.Status == "Major update available" {
				count++
			}
		}
		fmt.Printf("  %d major chart update(s) available. Run 'obol upgrade --major' to apply.\n", count)
	}
	if result.CLIUpdateAvail && result.CLIRelease != nil {
		fmt.Printf("  CLI update available (v%s → %s). Run:\n", version.Short(), result.CLIRelease.TagName)
		fmt.Println("    bash <(curl -s https://stack.obol.org)")
	}
}
