package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// HintIfStale compares embedded helmfile chart versions against the on-disk
// deployed versions. If any embedded version is newer than on-disk, prints a
// one-line hint suggesting `obol upgrade`. This is best-effort and never errors.
func HintIfStale(cfg *config.Config) {
	// Read embedded helmfile
	embeddedData, err := embed.ReadInfrastructureFile("helmfile.yaml")
	if err != nil {
		return
	}

	// Read on-disk helmfile
	onDiskPath := filepath.Join(cfg.ConfigDir, "defaults", "helmfile.yaml")

	onDiskData, err := os.ReadFile(onDiskPath)
	if err != nil {
		return
	}

	// Parse both
	embeddedReleases, err := ParseHelmfileReleasesFromBytes(embeddedData)
	if err != nil {
		return
	}

	onDiskReleases, err := ParseHelmfileReleasesFromBytes(onDiskData)
	if err != nil {
		return
	}

	// Build map of on-disk chart versions
	onDiskVersions := make(map[string]string)

	for _, rel := range onDiskReleases {
		if rel.Version != "" && !strings.HasPrefix(rel.Chart, "./") {
			onDiskVersions[rel.Chart] = rel.Version
		}
	}

	// Check if any embedded version is newer
	for _, rel := range embeddedReleases {
		if rel.Version == "" || strings.HasPrefix(rel.Chart, "./") {
			continue
		}

		onDiskVer, ok := onDiskVersions[rel.Chart]
		if !ok {
			// New chart in embedded that doesn't exist on disk
			fmt.Println("\nHint: Some stack components have updates available. Run 'obol upgrade' to apply.")
			return
		}

		if CompareVersions(onDiskVer, rel.Version) < 0 {
			fmt.Println("\nHint: Some stack components have updates available. Run 'obol upgrade' to apply.")
			return
		}
	}
}
