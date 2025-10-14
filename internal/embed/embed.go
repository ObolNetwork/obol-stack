package embed

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Embedded file systems
// Note: embed paths are relative to this file's directory (internal/embed/)
//
//go:embed all:k3d
var k3dFS embed.FS

//go:embed all:applications
var applicationsFS embed.FS

// GetApplicationsFS returns the embedded applications filesystem for use by other packages
func GetApplicationsFS() embed.FS {
	return applicationsFS
}

// WriteK3dConfig writes the embedded k3d config to destination
func WriteK3dConfig(destPath string) error {
	data, err := k3dFS.ReadFile("k3d/config.yaml")
	if err != nil {
		return fmt.Errorf("failed to read embedded k3d config: %w", err)
	}

	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	return nil
}

// CopyDefaultApplications copies only default applications and README to destination
// This is used by cluster init to set up the base applications
// Non-default applications (like ethereum) must be installed via 'obol app install'
func CopyDefaultApplications(destDir string) error {
	// Walk through embedded filesystem starting at applications/
	return fs.WalkDir(applicationsFS, "applications", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the root "applications" directory itself
		if path == "applications" {
			return nil
		}

		// Remove the "applications/" prefix to get relative path
		relPath := strings.TrimPrefix(path, "applications/")

		// Split path to check if it's a top-level directory (not default)
		pathParts := strings.Split(relPath, string(filepath.Separator))
		if len(pathParts) > 0 {
			topLevelDir := pathParts[0]

			// Skip non-default application directories (like ethereum)
			// Only copy: default/ directory and README.md file
			if d.IsDir() && topLevelDir != "default" && !strings.HasPrefix(relPath, "default/") {
				return fs.SkipDir // Skip this entire directory tree
			}

			// Skip files in non-default app directories
			if !d.IsDir() && topLevelDir != "default" && topLevelDir != "README.md" {
				return nil // Skip this file
			}
		}

		// Build destination path
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			return nil
		}

		// Ensure parent directory exists before writing file
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := applicationsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Write to destination
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}
