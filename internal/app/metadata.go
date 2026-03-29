package app

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HelmfileInfo holds parsed information from a helmfile.yaml
type HelmfileInfo struct {
	ChartRef string // Original chart reference (from comment)
	Chart    string // Chart field value
	Version  string // Chart version
}

// ParseHelmfile extracts chart information from a helmfile.yaml
func ParseHelmfile(dir string) (*HelmfileInfo, error) {
	helmfilePath := filepath.Join(dir, "helmfile.yaml")

	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, err
	}

	info := &HelmfileInfo{}

	// Extract original reference from first comment line
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "# Installed from: "); ok {
			info.ChartRef = after
			// Remove any trailing annotation like "(resolved via ArtifactHub)"
			if idx := strings.Index(info.ChartRef, " ("); idx != -1 {
				info.ChartRef = info.ChartRef[:idx]
			}

			break
		}
	}

	// Parse YAML structure to get chart and version
	var helmfile struct {
		Releases []struct {
			Chart   string `yaml:"chart"`
			Version string `yaml:"version"`
		} `yaml:"releases"`
	}
	if err := yaml.Unmarshal(data, &helmfile); err != nil {
		return info, nil //nolint:nilerr // return partial info (name from dir) if YAML parsing fails
	}

	if len(helmfile.Releases) > 0 {
		info.Chart = helmfile.Releases[0].Chart
		info.Version = helmfile.Releases[0].Version
	}

	return info, nil
}

// GetHelmfileModTime returns the modification time of helmfile.yaml
func GetHelmfileModTime(dir string) (modTime string, err error) {
	helmfilePath := filepath.Join(dir, "helmfile.yaml")

	stat, err := os.Stat(helmfilePath)
	if err != nil {
		return "", err
	}

	return stat.ModTime().Format("2006-01-02 15:04:05"), nil
}
