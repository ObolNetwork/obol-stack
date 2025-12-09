package app

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Metadata stores information about an installed application
type Metadata struct {
	ChartURL    string    `yaml:"chartUrl"`             // Chart download URL
	ChartName   string    `yaml:"chartName"`            // Extracted chart name
	Version     string    `yaml:"version"`              // Chart version
	InstalledAt time.Time `yaml:"installedAt"`          // Installation timestamp
	UpdatedAt   time.Time `yaml:"updatedAt,omitempty"`  // Last update timestamp
}

// SaveMetadata writes metadata to the deployment directory
func SaveMetadata(dir string, meta *Metadata) error {
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "metadata.yaml"), data, 0644)
}

// LoadMetadata reads metadata from a deployment directory
func LoadMetadata(dir string) (*Metadata, error) {
	data, err := os.ReadFile(filepath.Join(dir, "metadata.yaml"))
	if err != nil {
		return nil, err
	}

	var meta Metadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
