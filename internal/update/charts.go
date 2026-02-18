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
	Chart   string // e.g., "traefik/traefik"
	Pinned  string // Currently pinned version, e.g., "38.0.2"
	Latest  string // Latest available in repo
	Status  string // "Up to date", "Update available", "Local chart", "Unpinned"
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
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	if !quiet {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm repo update failed: %w", err)
	}
	return nil
}

// CheckChartVersions parses the on-disk defaults helmfile for pinned chart versions
// and compares each against the latest available via `helm search repo`.
func CheckChartVersions(cfg *config.Config) ([]ChartStatus, error) {
	helmfilePath := filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")
	releases, err := parseHelmfileReleases(helmfilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse defaults helmfile: %w", err)
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

// UpgradeHelmfileVersions rewrites version pins in the on-disk defaults helmfile
// to the latest available versions from helm repos. Uses yaml.Node to preserve
// comments and formatting. If major is false, only bumps within the same major
// version (like npm's ^ behavior). Returns the list of charts that were bumped.
func UpgradeHelmfileVersions(cfg *config.Config, major bool) ([]VersionBump, error) {
	helmfilePath := filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")
	helmBinary := filepath.Join(cfg.BinDir, "helm")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read helmfile: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse helmfile: %w", err)
	}

	// doc.Content[0] is the root mapping node
	if doc.Content == nil || len(doc.Content) == 0 {
		return nil, fmt.Errorf("empty helmfile document")
	}
	root := doc.Content[0]

	// Find the "releases" key in the root mapping
	var releasesNode *yaml.Node
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "releases" {
			releasesNode = root.Content[i+1]
			break
		}
	}
	if releasesNode == nil {
		return nil, fmt.Errorf("no releases found in helmfile")
	}

	// Track which charts we've already bumped (dedup bedag/raw etc.)
	bumped := make(map[string]bool)
	var bumps []VersionBump

	// Walk each release (sequence of mapping nodes)
	for _, releaseNode := range releasesNode.Content {
		if releaseNode.Kind != yaml.MappingNode {
			continue
		}

		var chartValue string
		var versionNode *yaml.Node

		// Extract chart and version from the mapping
		for i := 0; i < len(releaseNode.Content)-1; i += 2 {
			key := releaseNode.Content[i].Value
			val := releaseNode.Content[i+1]
			switch key {
			case "chart":
				chartValue = val.Value
			case "version":
				versionNode = val
			}
		}

		// Skip local charts and charts without version pins
		if chartValue == "" || strings.HasPrefix(chartValue, "./") || strings.HasPrefix(chartValue, "/") {
			continue
		}
		if versionNode == nil {
			continue
		}

		// Skip if we already bumped this chart (dedup)
		if bumped[chartValue] {
			continue
		}

		// Query latest version
		latest, err := helmSearchLatest(helmBinary, kubeconfigPath, chartValue)
		if err != nil {
			continue // best-effort, skip on failure
		}

		currentVersion := versionNode.Value
		if CompareVersions(currentVersion, latest) >= 0 {
			continue // already up to date
		}

		// Skip major version jumps unless explicitly opted in
		if !major && MajorVersion(latest) != MajorVersion(currentVersion) {
			continue
		}

		// Update all releases with this chart to the new version
		for _, rn := range releasesNode.Content {
			if rn.Kind != yaml.MappingNode {
				continue
			}
			var rnChart string
			var rnVersion *yaml.Node
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

		bumped[chartValue] = true
		bumps = append(bumps, VersionBump{
			Chart: chartValue,
			From:  currentVersion,
			To:    latest,
		})
	}

	if len(bumps) == 0 {
		return nil, nil
	}

	// Marshal back to YAML and write
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal updated helmfile: %w", err)
	}

	if err := os.WriteFile(helmfilePath, out, 0644); err != nil {
		return nil, fmt.Errorf("failed to write updated helmfile: %w", err)
	}

	return bumps, nil
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

// helmSearchLatest queries helm for the latest version of a chart.
func helmSearchLatest(helmBinary, kubeconfigPath, chart string) (string, error) {
	cmd := exec.Command(helmBinary, "search", "repo", chart, "--output", "json")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

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
