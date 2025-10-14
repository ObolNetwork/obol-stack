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

// CopyApplications copies embedded applications directory to destination
func CopyApplications(destDir string) error {
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

		// Build destination path
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking (don't return yet)
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			return nil // Continue walking into this directory
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
