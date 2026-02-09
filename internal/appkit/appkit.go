package appkit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	petname "github.com/dustinkirkland/golang-petname"
)

// DeploymentPaths holds resolved paths for an app deployment instance.
type DeploymentPaths struct {
	DeploymentDir string // ~/.config/obol/applications/<app>/<id>/
	ChartDir      string // <DeploymentDir>/chart/
	ValuesPath    string // <DeploymentDir>/values.yaml
	OverlayPath   string // <DeploymentDir>/values-obol.yaml
	HelmfilePath  string // <DeploymentDir>/helmfile.yaml
}

// ResolveDeployment returns the full set of paths for a given app/id.
func ResolveDeployment(cfg *config.Config, appName, id string) DeploymentPaths {
	dir := filepath.Join(cfg.ConfigDir, "applications", appName, id)
	return DeploymentPaths{
		DeploymentDir: dir,
		ChartDir:      filepath.Join(dir, "chart"),
		ValuesPath:    filepath.Join(dir, "values.yaml"),
		OverlayPath:   filepath.Join(dir, "values-obol.yaml"),
		HelmfilePath:  filepath.Join(dir, "helmfile.yaml"),
	}
}

// GenerateID returns the provided ID if non-empty, otherwise generates a petname.
func GenerateID(provided string) string {
	if provided != "" {
		return provided
	}
	return petname.Generate(2, "-")
}

// Hostname builds a hostname like <app>-<id>.<domain>.
func Hostname(appName, id, domain string) string {
	return fmt.Sprintf("%s-%s.%s", appName, id, domain)
}

// Namespace builds a namespace like <app>-<id>.
func Namespace(appName, id string) string {
	return fmt.Sprintf("%s-%s", appName, id)
}

// CopyEmbeddedChart extracts an embedded chart FS to destDir.
// The chartFS should embed a directory named by root (e.g. "chart").
// Files are written relative to destDir with the root prefix stripped.
func CopyEmbeddedChart(chartFS fs.FS, root, destDir string) error {
	return fs.WalkDir(chartFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}

		relPath := strings.TrimPrefix(path, root+"/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		data, err := fs.ReadFile(chartFS, path)
		if err != nil {
			return fmt.Errorf("failed to read embedded %s: %w", path, err)
		}
		return os.WriteFile(destPath, data, 0644)
	})
}

// WriteDefaultValues reads values.yaml from the embedded chart FS and writes it
// to dest (typically DeploymentPaths.ValuesPath).
func WriteDefaultValues(chartFS fs.FS, valuesPath, dest string) error {
	data, err := fs.ReadFile(chartFS, valuesPath)
	if err != nil {
		return fmt.Errorf("failed to read chart defaults: %w", err)
	}
	return os.WriteFile(dest, data, 0644)
}

// WriteFile is a convenience wrapper around os.WriteFile with 0644 perms.
func WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
