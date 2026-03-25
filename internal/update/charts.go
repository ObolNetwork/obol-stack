package update

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

// ChartStatus represents the update status of a single helm chart
type ChartStatus struct {
	Chart  string // e.g., "traefik/traefik"
	Pinned string // Currently pinned version, e.g., "38.0.2"
	Latest string // Latest available in repo
	Status string // "Up to date", "Update available", "Local chart", "Unpinned"
}

// helmfileRelease is a subset of a helmfile release entry
type helmfileRelease struct {
	Name    string `yaml:"name"`
	Chart   string `yaml:"chart"`
	Version string `yaml:"version"`
}

// helmfileDoc is the top-level helmfile structure
type helmfileDoc struct {
	Releases []helmfileRelease `yaml:"releases"`
}

// helmSearchResult represents a single entry from `helm search repo --output json`
type helmSearchResult struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	AppVer  string `json:"app_version"`
}

// UpdateHelmRepos runs `helm repo update` to refresh all repo indexes.
// If quiet is true, stdout is suppressed (useful for JSON output mode).
func UpdateHelmRepos(cfg *config.Config, quiet bool) error {
	helmBinary := filepath.Join(cfg.BinDir, "helm")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(helmBinary, "repo", "update")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if !quiet {
		cmd.Stdout = os.Stdout
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm repo update failed: %w", err)
	}

	return nil
}

// CheckChartVersions parses all on-disk helmfiles (defaults + application instances)
// for pinned chart versions and compares each against the latest available via `helm search repo`.
func CheckChartVersions(cfg *config.Config) ([]ChartStatus, error) {
	var releases []helmfileRelease

	for _, hf := range collectHelmfiles(cfg) {
		rels, err := parseHelmfileReleases(hf)
		if err != nil {
			continue // best-effort: skip unreadable instance helmfiles
		}

		releases = append(releases, rels...)
	}

	helmBinary := filepath.Join(cfg.BinDir, "helm")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Deduplicate releases by chart name (e.g., bedag/raw appears multiple times)
	seen := make(map[string]bool)

	var uniqueReleases []helmfileRelease

	for _, rel := range releases {
		if seen[rel.Chart] {
			continue
		}

		seen[rel.Chart] = true
		uniqueReleases = append(uniqueReleases, rel)
	}

	var statuses []ChartStatus

	for _, rel := range uniqueReleases {
		// Skip local charts
		if strings.HasPrefix(rel.Chart, "./") || strings.HasPrefix(rel.Chart, "/") {
			statuses = append(statuses, ChartStatus{
				Chart:  rel.Chart,
				Pinned: "-",
				Latest: "-",
				Status: "Local chart",
			})

			continue
		}

		// Chart without a pinned version
		if rel.Version == "" {
			statuses = append(statuses, ChartStatus{
				Chart:  rel.Chart,
				Pinned: "-",
				Latest: "-",
				Status: "Unpinned",
			})

			continue
		}

		// Query helm for the latest version
		latest, err := helmSearchLatest(helmBinary, kubeconfigPath, rel.Chart)
		if err != nil {
			statuses = append(statuses, ChartStatus{
				Chart:  rel.Chart,
				Pinned: rel.Version,
				Latest: "?",
				Status: "Check failed",
			})

			continue
		}

		status := "Up to date"

		if CompareVersions(rel.Version, latest) < 0 {
			if MajorVersion(latest) != MajorVersion(rel.Version) {
				status = "Major update available"
			} else {
				status = "Update available"
			}
		}

		statuses = append(statuses, ChartStatus{
			Chart:  rel.Chart,
			Pinned: rel.Version,
			Latest: latest,
			Status: status,
		})
	}

	return statuses, nil
}

// VersionBump records a single chart version change made by UpgradeHelmfileVersions.
type VersionBump struct {
	Chart string
	From  string
	To    string
}

// UpgradeHelmfileVersions rewrites version pins in all on-disk helmfiles
// (defaults + application instances) to the latest available versions from
// helm repos. Uses yaml.Node to preserve comments and formatting. If major
// is false, only bumps within the same major version (like npm's ^ behavior).
// If chartFilter is non-empty, only the matching chart is bumped.
// Returns the list of charts that were bumped.
func UpgradeHelmfileVersions(cfg *config.Config, major bool, chartFilter string) ([]VersionBump, error) {
	helmBinary := filepath.Join(cfg.BinDir, "helm")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Track which charts we've already reported (dedup across helmfiles).
	reported := make(map[string]bool)

	var allBumps []VersionBump

	for _, helmfilePath := range collectHelmfiles(cfg) {
		bumps, err := upgradeOneHelmfile(helmfilePath, helmBinary, kubeconfigPath, major, chartFilter, reported)
		if err != nil {
			continue // best-effort: skip unreadable instance helmfiles
		}

		allBumps = append(allBumps, bumps...)
	}

	if len(allBumps) == 0 {
		return nil, nil
	}

	return allBumps, nil
}

// upgradeOneHelmfile bumps version pins in a single helmfile. Charts already
// present in reported are skipped (dedup across files). If chartFilter is
// non-empty, only that chart is considered (matched by chart name or release name).
func upgradeOneHelmfile(helmfilePath, helmBinary, kubeconfigPath string, major bool, chartFilter string, reported map[string]bool) ([]VersionBump, error) {
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	if len(doc.Content) == 0 {
		return nil, nil
	}

	root := doc.Content[0]

	var releasesNode *yaml.Node

	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "releases" {
			releasesNode = root.Content[i+1]
			break
		}
	}

	if releasesNode == nil {
		return nil, nil
	}

	var bumps []VersionBump

	changed := false

	for _, releaseNode := range releasesNode.Content {
		if releaseNode.Kind != yaml.MappingNode {
			continue
		}

		var (
			releaseName, chartValue string
			versionNode             *yaml.Node
		)

		for i := 0; i < len(releaseNode.Content)-1; i += 2 {
			switch releaseNode.Content[i].Value {
			case "name":
				releaseName = releaseNode.Content[i+1].Value
			case "chart":
				chartValue = releaseNode.Content[i+1].Value
			case "version":
				versionNode = releaseNode.Content[i+1]
			}
		}

		if chartValue == "" || strings.HasPrefix(chartValue, "./") || strings.HasPrefix(chartValue, "/") {
			continue
		}

		if chartFilter != "" && !matchesFilter(chartFilter, releaseName, chartValue) {
			continue
		}

		if versionNode == nil || reported[chartValue] {
			continue
		}

		latest, err := helmSearchLatest(helmBinary, kubeconfigPath, chartValue)
		if err != nil {
			continue
		}

		currentVersion := versionNode.Value
		if CompareVersions(currentVersion, latest) >= 0 {
			continue
		}

		if !major && MajorVersion(latest) != MajorVersion(currentVersion) {
			continue
		}

		// Update all releases in this file that use the same chart.
		for _, rn := range releasesNode.Content {
			if rn.Kind != yaml.MappingNode {
				continue
			}

			var (
				rnChart   string
				rnVersion *yaml.Node
			)

			for i := 0; i < len(rn.Content)-1; i += 2 {
				switch rn.Content[i].Value {
				case "chart":
					rnChart = rn.Content[i+1].Value
				case "version":
					rnVersion = rn.Content[i+1]
				}
			}

			if rnChart == chartValue && rnVersion != nil {
				rnVersion.Value = latest
			}
		}

		reported[chartValue] = true
		changed = true

		bumps = append(bumps, VersionBump{
			Chart: chartValue,
			From:  currentVersion,
			To:    latest,
		})
	}

	if !changed {
		return nil, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(helmfilePath, out, 0o644); err != nil {
		return nil, err
	}

	return bumps, nil
}

// collectHelmfiles returns all helmfile paths to check: the defaults helmfile
// plus any application instance helmfiles (e.g. openclaw instances that pin
// obol/remote-signer).
func collectHelmfiles(cfg *config.Config) []string {
	paths := []string{filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")}

	appsDir := filepath.Join(cfg.ConfigDir, "applications")

	appDirs, err := os.ReadDir(appsDir)
	if err != nil {
		return paths
	}

	for _, appDir := range appDirs {
		if !appDir.IsDir() {
			continue
		}

		instances, err := os.ReadDir(filepath.Join(appsDir, appDir.Name()))
		if err != nil {
			continue
		}

		for _, inst := range instances {
			if !inst.IsDir() {
				continue
			}

			hf := filepath.Join(appsDir, appDir.Name(), inst.Name(), "helmfile.yaml")
			if _, err := os.Stat(hf); err == nil {
				paths = append(paths, hf)
			}
		}
	}

	return paths
}

// parseHelmfileReleases extracts release entries from a helmfile YAML.
func parseHelmfileReleases(helmfilePath string) ([]helmfileRelease, error) {
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, err
	}

	var doc helmfileDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse helmfile YAML: %w", err)
	}

	return doc.Releases, nil
}

// ParseHelmfileReleasesFromBytes extracts release entries from helmfile YAML bytes.
// Exported for use by the hint system to parse embedded helmfile content.
func ParseHelmfileReleasesFromBytes(data []byte) ([]helmfileRelease, error) {
	var doc helmfileDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse helmfile YAML: %w", err)
	}

	return doc.Releases, nil
}

// matchesFilter returns true if the given filter matches either the release
// name or the chart name of a helmfile release. The filter can be:
//   - an exact release name (e.g. "reloader")
//   - an exact chart reference (e.g. "stakater/reloader")
func matchesFilter(filter, releaseName, chartName string) bool {
	return filter == releaseName || filter == chartName
}

// ResolveReleaseNames parses the defaults helmfile and returns the release
// names that match the given filter (by release name or chart name).
// Returns nil if no matches are found.
func ResolveReleaseNames(cfg *config.Config, filter string) []string {
	helmfilePath := filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")

	releases, err := parseHelmfileReleases(helmfilePath)
	if err != nil {
		return nil
	}

	var names []string

	for _, rel := range releases {
		if matchesFilter(filter, rel.Name, rel.Chart) {
			names = append(names, rel.Name)
		}
	}

	return names
}

// helmSearchLatest queries helm for the latest version of a chart.
func helmSearchLatest(helmBinary, kubeconfigPath, chart string) (string, error) {
	cmd := exec.Command(helmBinary, "search", "repo", chart, "--output", "json")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("helm search failed for %s: %w", chart, err)
	}

	var results []helmSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return "", fmt.Errorf("failed to parse helm search output: %w", err)
	}

	// Find exact match for the chart name
	for _, r := range results {
		if r.Name == chart {
			return r.Version, nil
		}
	}

	if len(results) > 0 {
		return results[0].Version, nil
	}

	return "", fmt.Errorf("no results found for chart %s", chart)
}
