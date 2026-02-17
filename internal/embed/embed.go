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

//go:embed all:infrastructure
var infrastructureFS embed.FS

//go:embed all:networks
var networksFS embed.FS

// CopyDefaults recursively copies all embedded infrastructure manifests to the destination directory.
// The replacements map is applied to every file: each key (e.g. "{{OLLAMA_HOST}}") is replaced
// with its value. Pass nil for a verbatim copy.
func CopyDefaults(destDir string, replacements map[string]string) error {
	return fs.WalkDir(infrastructureFS, "infrastructure", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root infrastructure directory
		if path == "infrastructure" {
			return nil
		}

		// Get relative path within infrastructure/
		relPath := strings.TrimPrefix(path, "infrastructure/")
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
		data, err := infrastructureFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Apply placeholder replacements
		content := string(data)
		for placeholder, value := range replacements {
			content = strings.ReplaceAll(content, placeholder, value)
		}

		// Write to destination
		if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
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

// ReadEmbeddedNetworkFile reads a file from an embedded network
func ReadEmbeddedNetworkFile(networkName, filename string) ([]byte, error) {
	path := filepath.Join("networks", networkName, filename)
	content, err := networksFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s from network %s: %w", filename, networkName, err)
	}
	return content, nil
}

// ReadInfrastructureFile reads a file from the embedded infrastructure directory
func ReadInfrastructureFile(path string) ([]byte, error) {
	content, err := infrastructureFS.ReadFile(filepath.Join("infrastructure", path))
	if err != nil {
		return nil, fmt.Errorf("failed to read infrastructure file %s: %w", path, err)
	}
	return content, nil
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
