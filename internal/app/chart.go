package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// ChartFormat represents the type of chart reference
type ChartFormat int

const (
	FormatURL       ChartFormat = iota // https://.../*.tgz
	FormatRepoChart                    // repo/chart[@version]
	FormatOCI                          // oci://...
)

// ChartReference holds parsed chart information
type ChartReference struct {
	Original  string      // Original input string
	Format    ChartFormat // Detected format type
	ChartName string      // Chart name
	ChartURL  string      // Full URL (for URL/OCI formats)
	RepoName  string      // Repository name (for repo/chart format)
	RepoURL   string      // Repository URL (for repo/chart format, resolved)
	Version   string      // Chart version (may be empty)
}

// ParseChartReference parses a chart reference in any supported format
func ParseChartReference(ref string) (*ChartReference, error) {
	ref = strings.TrimSpace(ref)

	// Detect format
	switch {
	case strings.HasPrefix(ref, "oci://"):
		return parseOCIReference(ref)
	case strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://"):
		return parseURLReference(ref)
	case isRepoChartFormat(ref):
		return parseRepoChartReference(ref)
	default:
		return nil, fmt.Errorf("invalid chart reference: %s\n\n"+
			"Supported formats:\n"+
			"  URL:        https://charts.bitnami.com/bitnami/redis-19.0.0.tgz\n"+
			"  Repo/Chart: bitnami/redis or bitnami/redis@19.0.0\n"+
			"  OCI:        oci://registry-1.docker.io/bitnamicharts/redis\n\n"+
			"Find charts at https://artifacthub.io", ref)
	}
}

// isRepoChartFormat checks if the reference looks like repo/chart[@version]
func isRepoChartFormat(ref string) bool {
	// Must contain exactly one slash (not counting any in version part)
	base := ref
	if idx := strings.LastIndex(ref, "@"); idx != -1 {
		base = ref[:idx]
	}
	return strings.Count(base, "/") == 1 && !strings.Contains(ref, "://")
}

func parseURLReference(ref string) (*ChartReference, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Extract chart name and version from URL path
	path := u.Path
	base := filepath.Base(path)

	// Remove .tgz extension
	nameWithVersion := strings.TrimSuffix(base, ".tgz")

	// Extract chart name and version (e.g., redis-19.0.0 -> redis, 19.0.0)
	chartName := nameWithVersion
	version := ""
	re := regexp.MustCompile(`^(.+)-(\d+\.\d+\.\d+.*)$`)
	if matches := re.FindStringSubmatch(nameWithVersion); len(matches) > 2 {
		chartName = matches[1]
		version = matches[2]
	}

	return &ChartReference{
		Original:  ref,
		Format:    FormatURL,
		ChartName: chartName,
		ChartURL:  ref,
		Version:   version,
	}, nil
}

func parseOCIReference(ref string) (*ChartReference, error) {
	// oci://registry-1.docker.io/bitnamicharts/redis
	// oci://registry-1.docker.io/bitnamicharts/redis:19.0.0

	withoutScheme := strings.TrimPrefix(ref, "oci://")
	parts := strings.Split(withoutScheme, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid OCI reference: %s", ref)
	}

	// Last part is chart name (possibly with :version)
	last := parts[len(parts)-1]
	chartName := last
	version := ""
	if idx := strings.LastIndex(last, ":"); idx != -1 {
		chartName = last[:idx]
		version = last[idx+1:]
	}

	return &ChartReference{
		Original:  ref,
		Format:    FormatOCI,
		ChartName: chartName,
		ChartURL:  ref,
		Version:   version,
	}, nil
}

func parseRepoChartReference(ref string) (*ChartReference, error) {
	// Parse repo/chart[@version]
	version := ""
	base := ref
	if idx := strings.LastIndex(ref, "@"); idx != -1 {
		version = ref[idx+1:]
		base = ref[:idx]
	}

	parts := strings.SplitN(base, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid repo/chart reference: %s", ref)
	}

	return &ChartReference{
		Original:  ref,
		Format:    FormatRepoChart,
		ChartName: parts[1],
		RepoName:  parts[0],
		Version:   version,
		// RepoURL will be resolved via ArtifactHub
	}, nil
}

// GetChartName returns the name to use for the app directory
func (c *ChartReference) GetChartName() string {
	return c.ChartName
}

// NeedsResolution returns true if this reference needs ArtifactHub resolution
func (c *ChartReference) NeedsResolution() bool {
	return c.Format == FormatRepoChart
}
