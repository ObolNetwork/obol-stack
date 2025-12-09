package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// ChartReference holds parsed chart information
type ChartReference struct {
	Original  string // Original input string
	ChartName string // Chart name extracted from URL
	ChartURL  string // Full URL to chart
}

// ParseChartReference parses a chart URL
func ParseChartReference(ref string) (*ChartReference, error) {
	ref = strings.TrimSpace(ref)

	// Only support HTTPS/HTTP URLs
	if !strings.HasPrefix(ref, "https://") && !strings.HasPrefix(ref, "http://") {
		return nil, fmt.Errorf("invalid chart URL: %s\n"+
			"Please provide a direct HTTPS URL to a chart .tgz file\n"+
			"Example: https://charts.bitnami.com/bitnami/redis-19.0.0.tgz\n"+
			"Find chart URLs at https://artifacthub.io", ref)
	}

	return parseURLReference(ref)
}

func parseURLReference(ref string) (*ChartReference, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Extract chart name from URL path (e.g., redis-19.0.0.tgz -> redis)
	path := u.Path
	base := filepath.Base(path)

	// Remove .tgz extension
	chartName := strings.TrimSuffix(base, ".tgz")
	// Remove version suffix (e.g., redis-19.0.0 -> redis)
	re := regexp.MustCompile(`^(.+)-\d+\.\d+\.\d+.*$`)
	if matches := re.FindStringSubmatch(chartName); len(matches) > 1 {
		chartName = matches[1]
	}

	return &ChartReference{
		Original:  ref,
		ChartName: chartName,
		ChartURL:  ref,
	}, nil
}

// GetChartName returns the name to use for the app directory
func (c *ChartReference) GetChartName() string {
	return c.ChartName
}
