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
//go:embed k3d-config.yaml
var K3dConfig string

//go:embed helmfile.yaml
var HelmfileTemplate string

//go:embed all:charts
var chartsFS embed.FS

//go:embed all:defaults
var defaultsFS embed.FS

// CopyCharts recursively copies all embedded charts to the destination directory
func CopyCharts(destDir string) error {
	return fs.WalkDir(chartsFS, "charts", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root charts directory
		if path == "charts" {
			return nil
		}

		// Get relative path within charts/
		relPath := strings.TrimPrefix(path, "charts/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			return nil
		}

		// Ensure parent directory exists
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := chartsFS.ReadFile(path)
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

// CopyDefaults recursively copies all embedded default manifests to the destination directory
func CopyDefaults(destDir string) error {
	return fs.WalkDir(defaultsFS, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root defaults directory
		if path == "defaults" {
			return nil
		}

		// Get relative path within defaults/
		relPath := strings.TrimPrefix(path, "defaults/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			return nil
		}

		// Ensure parent directory exists
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := defaultsFS.ReadFile(path)
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
