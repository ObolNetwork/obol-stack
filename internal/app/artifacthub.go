package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ArtifactHubClient handles chart resolution via the ArtifactHub API
type ArtifactHubClient struct {
	httpClient *http.Client
	baseURL    string
}

// ArtifactHubPackage represents the API response for a package
type ArtifactHubPackage struct {
	Name       string                   `json:"name"`
	Version    string                   `json:"version"`
	Repository ArtifactHubRepository    `json:"repository"`
	ContentURL string                   `json:"content_url"`
	AvailableVersions []ArtifactHubVersion `json:"available_versions"`
}

// ArtifactHubRepository represents repository info in the API response
type ArtifactHubRepository struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ArtifactHubVersion represents a version in the available_versions list
type ArtifactHubVersion struct {
	Version string `json:"version"`
}

// ChartInfo holds resolved chart information from ArtifactHub
type ChartInfo struct {
	RepoName  string // Repository name (e.g., "bitnami")
	RepoURL   string // Repository URL (e.g., "https://charts.bitnami.com/bitnami")
	ChartName string // Chart name (e.g., "redis")
	Version   string // Resolved version (e.g., "19.6.4")
}

// NewArtifactHubClient creates a new ArtifactHub API client
func NewArtifactHubClient() *ArtifactHubClient {
	return &ArtifactHubClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://artifacthub.io/api/v1",
	}
}

// ResolveChart resolves a repo/chart[@version] reference to full chart info
// Input formats:
//   - "bitnami/redis" -> resolves to latest version
//   - "bitnami/redis@19.0.0" -> resolves to specific version
func (c *ArtifactHubClient) ResolveChart(ref string) (*ChartInfo, error) {
	// Parse the reference
	repoName, chartName, version, err := parseRepoChartRef(ref)
	if err != nil {
		return nil, err
	}

	// Build API URL
	var apiURL string
	if version != "" {
		// Specific version endpoint
		apiURL = fmt.Sprintf("%s/packages/helm/%s/%s/%s", c.baseURL, repoName, chartName, version)
	} else {
		// Latest version endpoint
		apiURL = fmt.Sprintf("%s/packages/helm/%s/%s", c.baseURL, repoName, chartName)
	}

	// Make request
	resp, err := c.httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query ArtifactHub: %w\n"+
			"Please check your network connection or provide a direct chart URL instead", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if version != "" {
			return nil, fmt.Errorf("chart not found: %s/%s version %s\n"+
				"Verify the repository, chart name, and version are correct on https://artifacthub.io", repoName, chartName, version)
		}
		return nil, fmt.Errorf("chart not found: %s/%s\n"+
			"Verify the repository and chart name are correct on https://artifacthub.io", repoName, chartName)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ArtifactHub API error (status %d)\n"+
			"Please try again later or provide a direct chart URL instead", resp.StatusCode)
	}

	// Parse response
	var pkg ArtifactHubPackage
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return nil, fmt.Errorf("failed to parse ArtifactHub response: %w", err)
	}

	// Validate we got the required fields
	if pkg.Repository.URL == "" {
		return nil, fmt.Errorf("ArtifactHub returned incomplete data: missing repository URL")
	}

	return &ChartInfo{
		RepoName:  repoName,
		RepoURL:   pkg.Repository.URL,
		ChartName: chartName,
		Version:   pkg.Version,
	}, nil
}

// parseRepoChartRef parses "repo/chart[@version]" format
func parseRepoChartRef(ref string) (repoName, chartName, version string, err error) {
	// Check for version suffix
	if idx := strings.LastIndex(ref, "@"); idx != -1 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}

	// Split repo/chart
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid chart reference: %s\n"+
			"Expected format: repo/chart or repo/chart@version\n"+
			"Example: bitnami/redis or bitnami/redis@19.0.0", ref)
	}

	return parts[0], parts[1], version, nil
}
