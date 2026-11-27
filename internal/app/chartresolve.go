package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

// ObolChartRepoURL is Obol's published Helm chart repository (dv-pod,
// charon, remote-signer, ...). ArtifactHub lists it under the name
// "charon", while docs and skills use the alias "obol".
const ObolChartRepoURL = "https://obolnetwork.github.io/helm-charts"

// builtinRepoAliases are repo names obol resolves without ArtifactHub.
// A local `helm repo add` with the same name takes precedence.
var builtinRepoAliases = map[string]string{
	"obol": ObolChartRepoURL,
}

// stackExampleValuesPath is the values file a chart can ship for the Obol
// Stack (dv-pod does: ServiceMonitor + Hermes status readers). When the
// chart package contains it, install starts values.yaml from it.
const stackExampleValuesPath = "values-examples/obol-stack.yaml"

// localHelmRepos returns the repositories configured in the obol-resolved
// helm (`helm repo list`), name → URL. Errors (no repos configured, helm
// missing) yield an empty map.
func localHelmRepos(cfg *config.Config) map[string]string {
	out, err := helmLocal(cfg, "repo", "list", "-o", "json")
	if err != nil {
		return map[string]string{}
	}

	var repos []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(out, &repos); err != nil {
		return map[string]string{}
	}

	m := make(map[string]string, len(repos))
	for _, r := range repos {
		if r.Name != "" && r.URL != "" {
			m[r.Name] = r.URL
		}
	}

	return m
}

// resolveRepoChart fills RepoURL (and Version, when empty) for a
// repo/chart reference. Order: a local helm repo alias, then a built-in
// alias (obol), then ArtifactHub. It returns a short description of where
// the repo came from.
func resolveRepoChart(cfg *config.Config, chart *ChartReference, artifactHub func(ref string) (*ChartInfo, error)) (string, error) {
	source := ""

	if url, ok := localHelmRepos(cfg)[chart.RepoName]; ok {
		chart.RepoURL = url
		source = "local helm repo " + chart.RepoName
	} else if url, ok := builtinRepoAliases[chart.RepoName]; ok {
		chart.RepoURL = url
		source = "built-in alias " + chart.RepoName
	}

	if source != "" {
		version, err := chartVersionFromRepo(cfg, chart)
		if err != nil {
			return "", err
		}

		chart.Version = version

		return source, nil
	}

	ref := chart.RepoName + "/" + chart.ChartName
	if chart.Version != "" {
		ref += "@" + chart.Version
	}

	info, err := artifactHub(ref)
	if err != nil {
		return "", err
	}

	chart.RepoURL = info.RepoURL
	chart.RepoName = info.RepoName

	if chart.Version == "" {
		chart.Version = info.Version
	}

	return "ArtifactHub", nil
}

// chartVersionFromRepo asks helm for the chart's metadata in a repo URL,
// which both checks that the chart (and version, when pinned) exists and
// resolves the latest version when none was given.
func chartVersionFromRepo(cfg *config.Config, chart *ChartReference) (string, error) {
	args := []string{"show", "chart", chart.ChartName, "--repo", chart.RepoURL}
	if chart.Version != "" {
		args = append(args, "--version", chart.Version)
	}

	out, err := helmLocal(cfg, args...)
	if err != nil {
		what := chart.ChartName
		if chart.Version != "" {
			what += " version " + chart.Version
		}

		return "", fmt.Errorf("chart %s not found in %s (%s): %w", what, chart.RepoName, chart.RepoURL, err)
	}

	var meta struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(out, &meta); err != nil || meta.Version == "" {
		return "", fmt.Errorf("could not read the version of %s from %s", chart.ChartName, chart.RepoURL)
	}

	return meta.Version, nil
}

// showValuesCommand is the command that prints a chart's defaults, for the
// values.yaml header and hints.
func showValuesCommand(chart *ChartReference) string {
	switch chart.Format {
	case FormatRepoChart:
		cmd := fmt.Sprintf("obol helm show values %s --repo %s", chart.ChartName, chart.RepoURL)
		if chart.Version != "" {
			cmd += " --version " + chart.Version
		}

		return cmd
	case FormatOCI:
		cmd := "obol helm show values " + chart.ChartURL
		if chart.Version != "" {
			cmd += " --version " + chart.Version
		}

		return cmd
	default:
		return "obol helm show values " + chart.ChartURL
	}
}

// pulledChart is a chart package unpacked into a temp directory.
type pulledChart struct {
	dir     string // chart root (contains Chart.yaml)
	cleanup func()
}

// pullChart downloads and unpacks the chart package so install can read
// files shipped with it (the Obol Stack example values).
func pullChart(cfg *config.Config, chart *ChartReference) (*pulledChart, error) {
	tmp, err := os.MkdirTemp("", "obol-app-chart-*")
	if err != nil {
		return nil, err
	}

	cleanup := func() { _ = os.RemoveAll(tmp) }

	var args []string

	switch chart.Format {
	case FormatURL, FormatOCI:
		args = []string{"pull", chart.ChartURL}
	case FormatRepoChart:
		args = []string{"pull", chart.ChartName, "--repo", chart.RepoURL}
	}

	if chart.Version != "" && chart.Format != FormatURL {
		args = append(args, "--version", chart.Version)
	}

	args = append(args, "--untar", "--untardir", tmp)

	if _, err := helmLocal(cfg, args...); err != nil {
		cleanup()
		return nil, fmt.Errorf("helm pull failed: %w", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		cleanup()
		return nil, err
	}

	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(tmp, e.Name(), "Chart.yaml")); err == nil {
				return &pulledChart{dir: filepath.Join(tmp, e.Name()), cleanup: cleanup}, nil
			}
		}
	}

	cleanup()

	return nil, errors.New("helm pull produced no chart directory")
}

// stackExampleValues returns the chart's values-examples/obol-stack.yaml,
// or nil when the chart doesn't ship one.
func (p *pulledChart) stackExampleValues() []byte {
	data, err := os.ReadFile(filepath.Join(p.dir, filepath.FromSlash(stackExampleValuesPath)))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	return data
}

// chartVersion reads the version from the pulled Chart.yaml.
func (p *pulledChart) chartVersion() string {
	data, err := os.ReadFile(filepath.Join(p.dir, "Chart.yaml"))
	if err != nil {
		return ""
	}

	var meta struct {
		Version string `yaml:"version"`
	}

	_ = yaml.Unmarshal(data, &meta)

	return meta.Version
}
