package stack

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writePluginsFixture materializes a fake ~/.claude/plugins dir. Shapes
// mirror real Claude Code files (installed_plugins.json v2 maps each
// "<plugin>@<marketplace>" key to an ARRAY of per-scope install records).
func writePluginsFixture(t *testing.T, marketplaces, installed string) string {
	t.Helper()
	dir := t.TempDir()
	if marketplaces != "" {
		if err := os.WriteFile(filepath.Join(dir, "known_marketplaces.json"), []byte(marketplaces), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if installed != "" {
		if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(installed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const marketplacesFixture = `{
  "claude-plugins-official": {"source": {"source": "github", "repo": "anthropics/claude-plugins-official"}},
  "obol": {"source": {"source": "github", "repo": "ObolNetwork/skills"}}
}`

func TestObolMarketplaceName(t *testing.T) {
	dir := writePluginsFixture(t, marketplacesFixture, "")
	name, ok := obolMarketplaceName(dir)
	if !ok || name != "obol" {
		t.Fatalf("obolMarketplaceName = %q, %v; want obol, true", name, ok)
	}

	// Renamed marketplace still resolves by repo.
	dir = writePluginsFixture(t, `{"my-skills": {"source": {"source": "github", "repo": "obolnetwork/SKILLS"}}}`, "")
	name, ok = obolMarketplaceName(dir)
	if !ok || name != "my-skills" {
		t.Fatalf("case-insensitive repo match = %q, %v", name, ok)
	}

	// Missing file / no matching repo → not registered.
	if _, ok := obolMarketplaceName(t.TempDir()); ok {
		t.Fatal("empty dir must report unregistered")
	}
	dir = writePluginsFixture(t, `{"other": {"source": {"source": "github", "repo": "someone/else"}}}`, "")
	if _, ok := obolMarketplaceName(dir); ok {
		t.Fatal("unrelated marketplace must report unregistered")
	}
}

func TestObolInstalledPlugin(t *testing.T) {
	// v2 shape: array of install records, version present.
	dir := writePluginsFixture(t, "", `{
  "version": 2,
  "plugins": {
    "ralph-loop@claude-plugins-official": [{"scope": "project", "version": "1.0.0"}],
    "obol@obol": [{"scope": "user", "version": "1.3.0"}, {"scope": "project", "version": "1.4.0"}]
  }
}`)
	key, version, ok := obolInstalledPlugin(dir, "obol")
	if !ok || key != "obol@obol" || version != "1.4.0" {
		t.Fatalf("obolInstalledPlugin = %q, %q, %v; want obol@obol, 1.4.0, true", key, version, ok)
	}

	// "unknown" version → installed but version unknowable (no update nag).
	dir = writePluginsFixture(t, "", `{"plugins": {"obol@obol": [{"version": "unknown"}]}}`)
	key, version, ok = obolInstalledPlugin(dir, "obol")
	if !ok || key != "obol@obol" || version != "" {
		t.Fatalf("unknown version: got %q, %q, %v; want obol@obol, \"\", true", key, version, ok)
	}

	// Pre-v2 single-object shape tolerated.
	dir = writePluginsFixture(t, "", `{"plugins": {"obol@obol": {"version": "1.1.0"}}}`)
	if _, version, ok = obolInstalledPlugin(dir, "obol"); !ok || version != "1.1.0" {
		t.Fatalf("legacy shape: version = %q, ok = %v", version, ok)
	}

	// Not installed / different marketplace.
	dir = writePluginsFixture(t, "", `{"plugins": {"obol@other-mp": [{"version": "9.9.9"}]}}`)
	if _, _, ok = obolInstalledPlugin(dir, "obol"); ok {
		t.Fatal("plugin from another marketplace must not count as installed")
	}
	if _, _, ok = obolInstalledPlugin(t.TempDir(), "obol"); ok {
		t.Fatal("missing installed_plugins.json must report not installed")
	}
}

func TestLatestObolPluginVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "obol", "version": "1.5.2"}`))
	}))
	defer srv.Close()
	orig := obolPluginManifestURL
	obolPluginManifestURL = srv.URL
	t.Cleanup(func() { obolPluginManifestURL = orig })

	if got := latestObolPluginVersion(); got != "1.5.2" {
		t.Fatalf("latestObolPluginVersion = %q, want 1.5.2", got)
	}

	// Failure modes stay silent (empty), never error.
	obolPluginManifestURL = srv.URL + "/missing"
	srv404 := httptest.NewServer(http.NotFoundHandler())
	defer srv404.Close()
	obolPluginManifestURL = srv404.URL
	if got := latestObolPluginVersion(); got != "" {
		t.Fatalf("404 must yield empty version, got %q", got)
	}
}

func TestVersionLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.3.0", "1.4.0", true},
		{"1.4.0", "1.3.0", false},
		{"1.4.0", "1.4.0", false},
		{"1.4", "1.4.1", true},
		{"v1.3.0", "1.10.0", true}, // numeric, not lexicographic
		{"1.9.9", "2.0.0", true},
		{"2.0.0", "1.9.9", false},
		{"", "1.0.0", true}, // "" splits to [""] → non-numeric fallback
	}
	for _, tc := range tests {
		if got := versionLess(tc.a, tc.b); got != tc.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
