package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/app"
	"github.com/ObolNetwork/obol-stack/internal/config"
	stackdefaults "github.com/ObolNetwork/obol-stack/internal/defaults"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

const (
	statusUpdateAvailable      = "Update available"
	statusMajorUpdateAvailable = "Major update available"
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
				switch s.Status {
				case statusUpdateAvailable:
					result.ChartUpdatesAvail = true
				case statusMajorUpdateAvailable:
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

// UpgradeOptions controls the behaviour of ApplyUpgrades.
type UpgradeOptions struct {
	DefaultsOnly bool   // Only upgrade default infrastructure, skip networks and apps
	Pinned       bool   // Deploy only the versions embedded in the binary
	Major        bool   // Allow upgrading across major version boundaries
	ChartFilter  string // If non-empty, only bump this chart (e.g. "obol/remote-signer")
}

// ApplyUpgrades runs helmfile sync on defaults and all installed deployments.
func ApplyUpgrades(cfg *config.Config, u *ui.UI, opts UpgradeOptions) error {
	defaultsOnly := opts.DefaultsOnly
	pinned := opts.Pinned
	major := opts.Major
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// 1. Helm repo update
	u.Info("Updating helm repositories...")

	if err := UpdateHelmRepos(cfg, false); err != nil {
		return fmt.Errorf("failed to update helm repos: %w", err)
	}

	u.Success("Helm repositories updated")

	// 2. Re-copy embedded defaults to pick up new chart versions from binary
	u.Blank()
	u.Info("Refreshing default infrastructure templates...")

	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	stackID := stackdefaults.StackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}

	if err := stackdefaults.CopyInfrastructure(cfg, stackdefaults.DetectedBackendName(cfg), stackID); err != nil {
		return fmt.Errorf("failed to refresh defaults: %w", err)
	}

	u.Success("Defaults updated from embedded assets")

	// 3. Bump chart version pins to latest (unless --pinned)
	if !pinned {
		u.Blank()

		if major {
			u.Info("Bumping chart versions to latest (including major versions)...")
		} else {
			u.Info("Bumping chart versions to latest (minor/patch only)...")
		}

		bumps, err := UpgradeHelmfileVersions(cfg, major, opts.ChartFilter)
		if err != nil {
			u.Warnf("Failed to bump versions: %v", err)
		} else if len(bumps) > 0 {
			for _, b := range bumps {
				u.Printf("  %s: %s → %s", b.Chart, b.From, b.To)
			}
		} else {
			u.Dim("  All chart versions already at latest.")
		}

		// Check if any major updates were skipped
		if !major {
			skipped := checkSkippedMajorUpdates(cfg)
			if len(skipped) > 0 {
				u.Blank()
				u.Warn("Major version updates available (skipped):")

				for _, s := range skipped {
					u.Printf("    %s: %s → %s", s.Chart, s.From, s.To)
				}

				u.Dim("  Use 'obol upgrade --major' to apply major version updates.")
			}
		}
	} else {
		u.Blank()
		u.Dim("Using pinned versions from embedded binary (--pinned).")
	}

	// 4. Helmfile sync on defaults
	u.Blank()

	helmfilePath := filepath.Join(defaultsDir, "helmfile.yaml")
	helmfileArgs := []string{
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
	}

	// When a chart filter is set, resolve it to release name(s) and use --selector
	// so helmfile only syncs the targeted release instead of everything.
	if opts.ChartFilter != "" {
		releaseNames := ResolveReleaseNames(cfg, opts.ChartFilter)
		if len(releaseNames) == 0 {
			return fmt.Errorf("no release found matching %q in defaults helmfile", opts.ChartFilter)
		}

		for _, name := range releaseNames {
			helmfileArgs = append(helmfileArgs, "--selector", "name="+name)
		}
	}

	helmfileArgs = append(helmfileArgs, "sync")
	helmfileArgs = append(helmfileArgs, helmcmd.SyncFlagsForVersion(filepath.Join(cfg.BinDir, "helm"))...)
	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		helmfileArgs...,
	)

	helmfileCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	label := "Upgrading default infrastructure"
	if opts.ChartFilter != "" {
		label = "Upgrading " + opts.ChartFilter
	}

	if err := u.Exec(ui.ExecConfig{Name: label, Cmd: helmfileCmd}); err != nil {
		return fmt.Errorf("failed to upgrade default infrastructure: %w", err)
	}

	// Skip networks/apps when targeting a specific chart
	if !defaultsOnly && opts.ChartFilter == "" {
		// 5. Re-sync installed networks
		u.Blank()
		u.Info("Upgrading installed networks...")

		if err := upgradeNetworks(cfg, u); err != nil {
			u.Warnf("%v", err)
		}

		// 6. Re-sync installed apps
		u.Blank()
		u.Info("Upgrading installed apps...")

		if err := upgradeApps(cfg, u); err != nil {
			u.Warnf("%v", err)
		}
	}

	u.Blank()
	u.Success("All helm chart upgrades applied.")

	// 7. Check CLI version and hint if newer available
	release, err := CheckLatestRelease()
	if err == nil && version.Short() != "dev" {
		if CompareVersions(version.Short(), release.Version) < 0 {
			u.Blank()
			u.Infof("A newer version of the obol CLI is available (v%s → %s).", version.Short(), release.TagName)
			u.Print("To update the CLI binary and dependencies, run:")
			u.Blank()
			u.Print("  bash <(curl -s https://stack.obol.org)")
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
		if s.Status == statusMajorUpdateAvailable {
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
func upgradeNetworks(cfg *config.Config, u *ui.UI) error {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	if _, err := os.Stat(networksDir); os.IsNotExist(err) {
		u.Dim("  No networks installed.")
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
			u.Printf("  Syncing %s...", identifier)

			if err := network.Sync(cfg, u, identifier); err != nil {
				u.Warnf("Failed to sync %s: %v", identifier, err)
			} else {
				u.Successf("%s upgraded", identifier)
			}

			found = true
		}
	}

	if !found {
		u.Dim("  No networks installed.")
	}

	return nil
}

// upgradeApps iterates over installed applications and syncs each.
func upgradeApps(cfg *config.Config, u *ui.UI) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")
	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		u.Dim("  No apps installed.")
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
			u.Printf("  Syncing %s...", identifier)

			if err := app.Sync(cfg, u, identifier); err != nil {
				u.Warnf("Failed to sync %s: %v", identifier, err)
			} else {
				u.Successf("%s upgraded", identifier)
			}

			found = true
		}
	}

	if !found {
		u.Dim("  No apps installed.")
	}

	return nil
}

// PrintUpdateTable prints a formatted table of chart statuses.
func PrintUpdateTable(u *ui.UI, statuses []ChartStatus) {
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
	u.Printf("  %-*s  %-*s  %-*s  %s", chartW, "Chart", pinnedW, "Pinned", latestW, "Latest", "Status")

	// Print rows
	for _, s := range statuses {
		u.Printf("  %-*s  %-*s  %-*s  %s", chartW, s.Chart, pinnedW, s.Pinned, latestW, s.Latest, s.Status)
	}
}

// PrintCLIStatus prints the CLI version status line.
func PrintCLIStatus(u *ui.UI, current string, release *LatestRelease, isDev bool) {
	if release == nil {
		return
	}

	if isDev {
		u.Printf("  Obol CLI  %-10s  %-10s  Development build (skipped)", "dev", release.TagName)
		return
	}

	currentDisplay := "v" + strings.TrimPrefix(current, "v")

	status := "Up to date"
	if CompareVersions(current, release.Version) < 0 {
		status = statusUpdateAvailable
	}

	u.Printf("  Obol CLI  %-10s  %-10s  %s", currentDisplay, release.TagName, status)
}

// PrintUpdateSummary prints the actionable summary at the end of `obol update`.
func PrintUpdateSummary(u *ui.UI, result *UpdateResult) {
	if !result.ChartUpdatesAvail && !result.ChartMajorUpdatesAvail && !result.CLIUpdateAvail {
		u.Blank()
		u.Success("Everything is up to date.")

		return
	}

	u.Blank()
	u.Info("Summary:")

	if result.ChartUpdatesAvail {
		count := 0

		for _, s := range result.ChartStatuses {
			if s.Status == statusUpdateAvailable {
				count++
			}
		}

		u.Printf("  %d chart update(s) available. Run 'obol upgrade' to apply.", count)
	}

	if result.ChartMajorUpdatesAvail {
		count := 0

		for _, s := range result.ChartStatuses {
			if s.Status == statusMajorUpdateAvailable {
				count++
			}
		}

		u.Printf("  %d major chart update(s) available. Run 'obol upgrade --major' to apply.", count)
	}

	if result.CLIUpdateAvail && result.CLIRelease != nil {
		u.Printf("  CLI update available (v%s → %s). Run:", version.Short(), result.CLIRelease.TagName)
		u.Print("    bash <(curl -s https://stack.obol.org)")
	}
}
