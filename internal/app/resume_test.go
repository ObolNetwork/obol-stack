package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// TestResumeAll_NoApps ensures a config dir with no app deployments is a
// silent no-op — stack up must not print resume noise for users who never
// installed an app.
func TestResumeAll_NoApps(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	var stdout, stderr bytes.Buffer
	ResumeAll(cfg, ui.NewForTest(&stdout, &stderr))

	if out := stdout.String() + stderr.String(); out != "" {
		t.Errorf("expected no output with zero apps, got: %q", out)
	}
}

// TestResumeAll_SkipsNonAppStateDirs ensures resume only replays real app
// deployments (identified by values.yaml) and never touches sibling
// subsystem state dirs like applications/hermes/<id>, which use
// values-<component>.yaml naming and are NOT helmfile-syncable apps.
func TestResumeAll_SkipsNonAppStateDirs(t *testing.T) {
	configDir := t.TempDir()
	cfg := &config.Config{ConfigDir: configDir}

	appDir := filepath.Join(configDir, "applications", "redis", "eager-fox")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, appDir, "values.yaml", "a: 1\n")
	writeFile(t, appDir, "helmfile.yaml", "releases: []\n")

	hermesDir := filepath.Join(configDir, "applications", "hermes", "obol-agent")
	if err := os.MkdirAll(hermesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hermesDir, "values-hermes.yaml", "b: 2\n")
	writeFile(t, hermesDir, "helmfile.yaml", "releases: []\n")

	var stdout, stderr bytes.Buffer
	// No kubeconfig.yaml in configDir, so Sync fails fast with "cluster
	// not running" — the point is WHICH deployments resume attempts.
	ResumeAll(cfg, ui.NewForTest(&stdout, &stderr))

	out := stdout.String() + stderr.String()

	if !strings.Contains(out, "redis/eager-fox") {
		t.Errorf("expected resume attempt for redis/eager-fox, got: %q", out)
	}
	if strings.Contains(out, "hermes") {
		t.Errorf("resume must not touch the hermes state dir, got: %q", out)
	}
}

// stack up must not re-sync a deployed app (it would apply pending
// values.yaml edits to, and possibly restart, a live DV); a release that is
// missing (e.g. the cluster was recreated) is deployed again.
func TestResumeAll_OnlyMissingOrFailedReleases(t *testing.T) {
	cases := map[string]struct {
		exists   bool
		wantSync bool
	}{
		"deployed release is left alone": {exists: true, wantSync: false},
		"missing release is re-deployed": {exists: false, wantSync: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t, true)
			writeManaged(t, env, 3, "mode: launchpad\n")
			useFakeTools(t, (&releaseFake{env: env, exists: tc.exists, baseRev: 3}).handle)

			ResumeAll(env.cfg, env.u)

			if got := env.helmfileRuns() > 0; got != tc.wantSync {
				t.Fatalf("synced=%v, want %v\n%s", got, tc.wantSync, env.stdout.String())
			}
		})
	}
}
