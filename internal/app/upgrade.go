package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// UpgradeOptions controls Upgrade.
type UpgradeOptions struct {
	Version string // target chart version (required)
	Force   bool   // passed to sync: overwrite changes made outside obol app
}

// Upgrade moves an app to another chart version: it sets version: in
// helmfile.yaml (yaml node edit, comments kept), warns when values.yaml
// is a pre-v0.19 full copy of the old defaults, and syncs with the normal
// drift check. If the sync is refused for drift, helmfile.yaml is
// restored.
func Upgrade(cfg *config.Config, u *ui.UI, identifier string, opts UpgradeOptions) error {
	if opts.Version == "" {
		return errors.New("--version is required")
	}

	if !clusterConfigured(cfg) {
		return errNoCluster
	}

	appName, id, err := parseDeploymentIdentifier(identifier)
	if err != nil {
		return err
	}

	dir := deploymentDirFor(cfg, appName, id)
	helmfilePath := filepath.Join(dir, "helmfile.yaml")

	original, err := os.ReadFile(helmfilePath)
	if err != nil {
		return fmt.Errorf("deployment not found: %s (%w)", identifier, err)
	}

	meta, err := loadAppMeta(dir, appName, id)
	if err != nil {
		return err
	}

	updated, previous, err := setHelmfileVersion(original, opts.Version)
	if err != nil {
		return err
	}

	if previous == opts.Version {
		u.Infof("%s is already on chart version %s; syncing", identifier, opts.Version)
	} else {
		if err := checkRepoChartVersion(cfg, original, opts.Version); err != nil {
			return err
		}

		u.Infof("Upgrading %s: chart %s %s → %s", identifier, meta.ChartName, previous, opts.Version)
	}

	if meta.ValuesMode == ValuesModeFullDefaults {
		u.Warnf("values.yaml is a full copy of chart %s's defaults (installed before v0.19): its image tags and other defaults stay pinned "+
			"and override chart %s's. Slim it first: obol app install <chart>@%s --name %s --id %s --force --values <your-overrides.yaml>",
			previous, opts.Version, opts.Version, appName, id)
	}

	if err := os.WriteFile(helmfilePath, updated, 0o600); err != nil {
		return err
	}

	err = SyncWithOptions(cfg, u, identifier, SyncOptions{Force: opts.Force})
	if errors.Is(err, ErrDrift) {
		_ = os.WriteFile(helmfilePath, original, 0o600) //nolint:gosec // G703: path in obol's own config dir
		u.Printf("helmfile.yaml left at chart version %s", previous)
	}

	if err != nil {
		return err
	}

	u.Printf("values.yaml was not changed. New chart settings: %s", "obol helm show values <chart> --version "+opts.Version+
		" (and values-examples/obol-stack.yaml in the chart, if it ships one)")

	return nil
}

// setHelmfileVersion sets releases[0].version on a yaml node tree and
// returns the new bytes and the previous version.
func setHelmfileVersion(data []byte, version string) ([]byte, string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("parse helmfile.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, "", errors.New("helmfile.yaml is empty")
	}

	releases := mappingValue(doc.Content[0], "releases")
	if releases == nil || releases.Kind != yaml.SequenceNode || len(releases.Content) == 0 {
		return nil, "", errors.New("helmfile.yaml has no releases")
	}

	rel := releases.Content[0]

	chart := mappingValue(rel, "chart")
	if chart != nil && chartVersionSuffixRE.MatchString(chartNameFromURLBase(chart.Value)) {
		return nil, "", fmt.Errorf("chart %s is a versioned archive URL; reinstall from the new URL with obol app install --force", chart.Value)
	}

	previous := ""
	if v := mappingValue(rel, "version"); v != nil {
		previous = v.Value
	}

	setMappingValue(rel, scalarKey("version"), &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: version, Style: yaml.DoubleQuotedStyle})

	out, err := encodeValuesNode(&doc)
	if err != nil {
		return nil, "", err
	}

	return out, previous, nil
}

// chartNameFromURLBase returns the last path element of a .tgz chart URL
// without the extension, or "" for other chart references.
func chartNameFromURLBase(chart string) string {
	if filepath.Ext(chart) != ".tgz" {
		return ""
	}

	base := filepath.Base(chart)

	return base[:len(base)-len(".tgz")]
}

// checkRepoChartVersion verifies a repo chart has the target version
// before helmfile.yaml is touched. Other chart kinds are checked by sync.
func checkRepoChartVersion(cfg *config.Config, helmfile []byte, version string) error {
	var hf struct {
		Repositories []struct {
			Name string `yaml:"name"`
			URL  string `yaml:"url"`
		} `yaml:"repositories"`
		Releases []struct {
			Chart string `yaml:"chart"`
		} `yaml:"releases"`
	}
	if err := yaml.Unmarshal(helmfile, &hf); err != nil || len(hf.Repositories) == 0 || len(hf.Releases) == 0 {
		return nil //nolint:nilerr // not a repo chart: sync reports problems
	}

	ref := &ChartReference{
		RepoName:  hf.Repositories[0].Name,
		RepoURL:   hf.Repositories[0].URL,
		ChartName: chartNameFromRef(hf.Releases[0].Chart),
		Version:   version,
	}

	_, err := chartVersionFromRepo(cfg, ref)

	return err
}
