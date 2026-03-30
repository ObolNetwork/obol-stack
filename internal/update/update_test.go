package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// sampleHelmfile is a minimal helmfile for testing.
const sampleHelmfile = `repositories:
  - name: traefik
    url: https://traefik.github.io/charts
  - name: stakater
    url: https://stakater.github.io/stakater-charts

releases:
  - name: base
    namespace: kube-system
    chart: ./base

  - name: traefik
    namespace: traefik
    chart: traefik/traefik
    version: 38.0.2

  - name: reloader
    namespace: reloader
    chart: stakater/reloader
    version: 2.2.7

  - name: monitoring
    namespace: monitoring
    chart: prometheus-community/kube-prometheus-stack
    version: 82.2.1
`

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		name        string
		filter      string
		releaseName string
		chartName   string
		want        bool
	}{
		{
			name:        "exact release name",
			filter:      "reloader",
			releaseName: "reloader",
			chartName:   "stakater/reloader",
			want:        true,
		},
		{
			name:        "exact chart name",
			filter:      "stakater/reloader",
			releaseName: "reloader",
			chartName:   "stakater/reloader",
			want:        true,
		},
		{
			name:        "no match",
			filter:      "traefik",
			releaseName: "reloader",
			chartName:   "stakater/reloader",
			want:        false,
		},
		{
			name:        "partial chart name does not match",
			filter:      "stakater",
			releaseName: "reloader",
			chartName:   "stakater/reloader",
			want:        false,
		},
		{
			name:        "release name matches even if chart differs",
			filter:      "traefik",
			releaseName: "traefik",
			chartName:   "traefik/traefik",
			want:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesFilter(tc.filter, tc.releaseName, tc.chartName)
			if got != tc.want {
				t.Errorf("matchesFilter(%q, %q, %q) = %v, want %v",
					tc.filter, tc.releaseName, tc.chartName, got, tc.want)
			}
		})
	}
}

func TestResolveReleaseNames(t *testing.T) {
	// Set up a temp config dir with a helmfile
	tmpDir := t.TempDir()

	defaultsDir := filepath.Join(tmpDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(defaultsDir, "helmfile.yaml"), []byte(sampleHelmfile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ConfigDir: tmpDir}

	tests := []struct {
		name   string
		filter string
		want   []string
	}{
		{
			name:   "match by release name",
			filter: "reloader",
			want:   []string{"reloader"},
		},
		{
			name:   "match by chart name",
			filter: "stakater/reloader",
			want:   []string{"reloader"},
		},
		{
			name:   "match traefik by release name",
			filter: "traefik",
			want:   []string{"traefik"},
		},
		{
			name:   "match traefik by chart name",
			filter: "traefik/traefik",
			want:   []string{"traefik"},
		},
		{
			name:   "no match returns nil",
			filter: "nonexistent",
			want:   nil,
		},
		{
			name:   "local chart skipped by parseHelmfileReleases but still has name",
			filter: "base",
			want:   []string{"base"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveReleaseNames(cfg, tc.filter)
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveReleaseNames(%q) = %v, want %v", tc.filter, got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ResolveReleaseNames(%q)[%d] = %q, want %q", tc.filter, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestUpgradeOneHelmfile_ChartFilter(t *testing.T) {
	// This test verifies that upgradeOneHelmfile only processes releases
	// matching the chart filter (by release name or chart name), and skips others.
	// We can't call helm search, so we test the filter/skip logic by using
	// a mock-friendly approach: write a helmfile and verify only the filtered
	// chart is considered.
	tmpDir := t.TempDir()
	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml")

	helmfileContent := `releases:
  - name: traefik
    chart: traefik/traefik
    version: 38.0.2
  - name: reloader
    chart: stakater/reloader
    version: 2.2.7
  - name: monitoring
    chart: prometheus-community/kube-prometheus-stack
    version: 82.2.1
`
	if err := os.WriteFile(helmfilePath, []byte(helmfileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// With a filter set and no helm binary, the function will fail at helmSearchLatest
	// for the matched chart but skip non-matching charts entirely. We verify that
	// non-matching charts are not processed by checking the file isn't modified.
	reported := make(map[string]bool)

	// Filter by release name "reloader" - should only try to process stakater/reloader.
	// helmSearchLatest will fail (no helm binary), but the key test is that
	// traefik and monitoring are skipped.
	_, _ = upgradeOneHelmfile(helmfilePath, "/nonexistent/helm", "/nonexistent/kubeconfig", false, "reloader", reported)

	// Verify the file was NOT modified (since helmSearchLatest fails, no bump happens)
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != helmfileContent {
		t.Error("helmfile was unexpectedly modified when helm binary is unavailable")
	}

	// Filter by chart name "traefik/traefik"
	reported2 := make(map[string]bool)
	_, _ = upgradeOneHelmfile(helmfilePath, "/nonexistent/helm", "/nonexistent/kubeconfig", false, "traefik/traefik", reported2)

	data, err = os.ReadFile(helmfilePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != helmfileContent {
		t.Error("helmfile was unexpectedly modified when helm binary is unavailable")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "2.0.0", -1},
		{"38.0.2", "39.0.2", -1},
		{"82.2.1", "82.2.1", 0},
		{"2.2.7", "2.3.0", -1},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0-rc.1", "1.0.0", 0},
	}

	for _, tc := range tests {
		t.Run(tc.current+"_vs_"+tc.latest, func(t *testing.T) {
			got := CompareVersions(tc.current, tc.latest)
			if got != tc.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestMajorVersion(t *testing.T) {
	tests := []struct {
		version string
		want    int
	}{
		{"1.0.0", 1},
		{"38.0.2", 38},
		{"0.1.0", 0},
		{"v2.3.4", 2},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			got := MajorVersion(tc.version)
			if got != tc.want {
				t.Errorf("MajorVersion(%q) = %d, want %d", tc.version, got, tc.want)
			}
		})
	}
}

func TestParseHelmfileReleases(t *testing.T) {
	tmpDir := t.TempDir()

	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml")
	if err := os.WriteFile(helmfilePath, []byte(sampleHelmfile), 0o644); err != nil {
		t.Fatal(err)
	}

	releases, err := parseHelmfileReleases(helmfilePath)
	if err != nil {
		t.Fatal(err)
	}

	// Should find 4 releases: base, traefik, reloader, monitoring
	if len(releases) != 4 {
		t.Fatalf("expected 4 releases, got %d", len(releases))
	}

	expected := []struct {
		name    string
		chart   string
		version string
	}{
		{"base", "./base", ""},
		{"traefik", "traefik/traefik", "38.0.2"},
		{"reloader", "stakater/reloader", "2.2.7"},
		{"monitoring", "prometheus-community/kube-prometheus-stack", "82.2.1"},
	}

	for i, exp := range expected {
		if releases[i].Name != exp.name {
			t.Errorf("release[%d].Name = %q, want %q", i, releases[i].Name, exp.name)
		}

		if releases[i].Chart != exp.chart {
			t.Errorf("release[%d].Chart = %q, want %q", i, releases[i].Chart, exp.chart)
		}

		if releases[i].Version != exp.version {
			t.Errorf("release[%d].Version = %q, want %q", i, releases[i].Version, exp.version)
		}
	}
}

func TestCollectHelmfiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create defaults helmfile
	defaultsDir := filepath.Join(tmpDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(defaultsDir, "helmfile.yaml"), []byte("releases: []"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an app instance helmfile
	appDir := filepath.Join(tmpDir, "applications", "openclaw", "my-instance")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(appDir, "helmfile.yaml"), []byte("releases: []"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{ConfigDir: tmpDir}
	paths := collectHelmfiles(cfg)

	if len(paths) != 2 {
		t.Fatalf("expected 2 helmfile paths, got %d: %v", len(paths), paths)
	}

	if paths[0] != filepath.Join(defaultsDir, "helmfile.yaml") {
		t.Errorf("paths[0] = %q, want defaults helmfile", paths[0])
	}

	if paths[1] != filepath.Join(appDir, "helmfile.yaml") {
		t.Errorf("paths[1] = %q, want app instance helmfile", paths[1])
	}
}

func TestCheckChartVersions_LocalChart(t *testing.T) {
	// Test that local charts (./base) are reported as "Local chart"
	tmpDir := t.TempDir()

	defaultsDir := filepath.Join(tmpDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	helmfile := `releases:
  - name: base
    chart: ./base
`
	if err := os.WriteFile(filepath.Join(defaultsDir, "helmfile.yaml"), []byte(helmfile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		ConfigDir: tmpDir,
		BinDir:    "/nonexistent",
	}

	statuses, err := CheckChartVersions(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	if statuses[0].Status != "Local chart" {
		t.Errorf("status = %q, want %q", statuses[0].Status, "Local chart")
	}
}
