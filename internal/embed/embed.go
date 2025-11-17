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

//go:embed all:defaults
var defaultsFS embed.FS

//go:embed all:networks
var networksFS embed.FS

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

// GetAvailableNetworks returns a list of all embedded network names
func GetAvailableNetworks() ([]string, error) {
	entries, err := fs.ReadDir(networksFS, "networks")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded networks directory: %w", err)
	}

	var networks []string
	for _, entry := range entries {
		if entry.IsDir() {
			networks = append(networks, entry.Name())
		}
	}

	return networks, nil
}

// CopyNetwork recursively copies an embedded network to the destination directory
func CopyNetwork(networkName, destDir string) error {
	networkPath := filepath.Join("networks", networkName)

	// Check if network exists in embedded FS
	_, err := fs.Stat(networksFS, networkPath)
	if err != nil {
		return fmt.Errorf("network %s not found in embedded filesystem: %w", networkName, err)
	}

	return fs.WalkDir(networksFS, networkPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root network directory
		if path == networkPath {
			return nil
		}

		// Get relative path within networks/<network>/
		relPath := strings.TrimPrefix(path, networkPath+"/")
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
		data, err := networksFS.ReadFile(path)
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
