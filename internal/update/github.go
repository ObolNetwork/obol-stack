package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	githubReleasesURL = "https://api.github.com/repos/ObolNetwork/obol-stack/releases/latest"
)

// LatestRelease holds info about the latest GitHub release
type LatestRelease struct {
	TagName string // e.g., "v0.2.0"
	Version string // e.g., "0.2.0" (tag stripped of leading "v")
	URL     string // HTML URL to the release page
}

// githubRelease is the subset of the GitHub API response we need
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// CheckLatestRelease queries GitHub API for the latest obol-stack release.
func CheckLatestRelease() (*LatestRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	resp, err := client.Get(githubReleasesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API rate limit reached, try again later")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub response: %w", err)
	}

	version := strings.TrimPrefix(release.TagName, "v")

	return &LatestRelease{
		TagName: release.TagName,
		Version: version,
		URL:     release.HTMLURL,
	}, nil
}

// CompareVersions compares two semver strings (without "v" prefix).
// Returns -1 if current < latest, 0 if equal, +1 if current > latest.
func CompareVersions(current, latest string) int {
	currentParts := parseSemver(current)
	latestParts := parseSemver(latest)

	for i := 0; i < 3; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}
	return 0
}

// parseSemver extracts [major, minor, patch] from a version string.
// Handles formats like "1.2.3", "1.2.3-rc.1", "1.2".
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")

	// Strip any pre-release suffix (e.g., "-rc.1")
	if idx := strings.Index(v, "-"); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	var result [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		result[i] = n
	}
	return result
}
