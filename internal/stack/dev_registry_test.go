package stack

import (
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// renderRegistriesConfig / renderDevRegistriesConfig
// ---------------------------------------------------------------------------

// TestRenderRegistriesConfig_PullThroughOnly verifies that the default
// (non-dev) config contains all three pull-through mirrors and does NOT
// contain the local push target.
func TestRenderRegistriesConfig_PullThroughOnly(t *testing.T) {
	config := renderRegistriesConfig(pullThroughMirrors)

	for _, mirror := range pullThroughMirrors {
		if !strings.Contains(config, `"`+mirror.upstreamHost+`"`) {
			t.Fatalf("pull-through config missing mirror for %s", mirror.upstreamHost)
		}
		if !strings.Contains(config, registryEndpoint(mirror)) {
			t.Fatalf("pull-through config missing endpoint %s", registryEndpoint(mirror))
		}
	}

	// Local push target must NOT be present in the non-dev config.
	if strings.Contains(config, `"`+localPushMirror.upstreamHost+`"`) {
		t.Fatalf("non-dev config must not include local push target %s", localPushMirror.upstreamHost)
	}
	if strings.Contains(config, registryEndpoint(localPushMirror)) {
		t.Fatalf("non-dev config must not include local push endpoint %s", registryEndpoint(localPushMirror))
	}
}

// TestRenderRegistriesConfig_DevMode verifies that the dev-mode config
// contains all three pull-through mirrors AND the local push target.
func TestRenderRegistriesConfig_DevMode(t *testing.T) {
	config := renderRegistriesConfig(allDevRegistryMirrors())

	for _, mirror := range pullThroughMirrors {
		if !strings.Contains(config, `"`+mirror.upstreamHost+`"`) {
			t.Fatalf("dev config missing pull-through mirror for %s", mirror.upstreamHost)
		}
	}

	// Local push target must be present in dev mode.
	if !strings.Contains(config, `"`+localPushMirror.upstreamHost+`"`) {
		t.Fatalf("dev config missing local push target %s", localPushMirror.upstreamHost)
	}
	if !strings.Contains(config, registryEndpoint(localPushMirror)) {
		t.Fatalf("dev config missing local push endpoint %s", registryEndpoint(localPushMirror))
	}
}

// TestRenderDevRegistriesConfig is the legacy wrapper — it must still
// produce output that includes all four entries (3 pull-through + local push).
func TestRenderDevRegistriesConfig(t *testing.T) {
	config := renderDevRegistriesConfig()

	for _, mirror := range allDevRegistryMirrors() {
		if !strings.Contains(config, `"`+mirror.upstreamHost+`"`) {
			t.Fatalf("config missing mirror for %s", mirror.upstreamHost)
		}
		if !strings.Contains(config, registryEndpoint(mirror)) {
			t.Fatalf("config missing endpoint %s", registryEndpoint(mirror))
		}
	}
}

// ---------------------------------------------------------------------------
// Pull-through mirror set invariants
// ---------------------------------------------------------------------------

// TestPullThroughMirrorsCount guards against accidentally adding or removing
// one of the three canonical pull-through caches.
func TestPullThroughMirrorsCount(t *testing.T) {
	if got := len(pullThroughMirrors); got != 3 {
		t.Fatalf("expected 3 pull-through mirrors (docker.io, ghcr.io, quay.io), got %d", got)
	}
}

// TestPullThroughMirrorsHaveRemoteURL ensures every pull-through mirror has a
// proxy URL configured (the local push target intentionally does not).
func TestPullThroughMirrorsHaveRemoteURL(t *testing.T) {
	for _, m := range pullThroughMirrors {
		if m.remoteURL == "" {
			t.Errorf("pull-through mirror %q has empty remoteURL", m.upstreamHost)
		}
	}
}

// TestLocalPushMirrorHasNoRemoteURL ensures the local push target does NOT
// have a proxy URL (it is a pure local registry, not a pull-through cache).
func TestLocalPushMirrorHasNoRemoteURL(t *testing.T) {
	if localPushMirror.remoteURL != "" {
		t.Fatalf("local push mirror must not have a remoteURL, got %q", localPushMirror.remoteURL)
	}
}

// ---------------------------------------------------------------------------
// k3dCreateArgs
// ---------------------------------------------------------------------------

func TestK3dCreateArgsWithoutRegistrySetup(t *testing.T) {
	args := k3dCreateArgs("obol-stack-test", "/tmp/k3d.yaml", nil)
	want := []string{
		"cluster", "create", "obol-stack-test",
		"--config", "/tmp/k3d.yaml",
		"--kubeconfig-update-default=false",
	}

	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected args:\n got: %v\nwant: %v", args, want)
	}
}

func TestK3dCreateArgsWithRegistrySetup(t *testing.T) {
	setup := &registrySetup{
		configPath: "/tmp/registries.yaml",
		useRefs: []string{
			"k3d-obol-docker-io.localhost:54100",
			"k3d-obol-ghcr-io.localhost:54101",
		},
	}

	args := k3dCreateArgs("obol-stack-test", "/tmp/k3d.yaml", setup)
	got := strings.Join(args, " ")

	if !strings.Contains(got, "--registry-config /tmp/registries.yaml") {
		t.Fatalf("missing --registry-config: %v", args)
	}

	for _, ref := range setup.useRefs {
		if !strings.Contains(got, "--registry-use "+ref) {
			t.Fatalf("missing --registry-use %s: %v", ref, args)
		}
	}
}

// ---------------------------------------------------------------------------
// disableRegistryCacheEnvVar
// ---------------------------------------------------------------------------

// TestEnsureRegistryCaches_DisabledByEnv verifies that setting
// OBOL_DISABLE_REGISTRY_CACHE=true causes ensureRegistryCaches to return
// nil, nil (no setup, no error).
func TestEnsureRegistryCaches_DisabledByEnv(t *testing.T) {
	t.Setenv(disableRegistryCacheEnvVar, "true")

	// We pass a nil config and ui — if the function tries to do real work it
	// will panic, which would surface as a test failure. A clean nil,nil
	// return means it bailed out before touching anything.
	setup, err := ensureRegistryCaches(nil, nil, false)
	if err != nil {
		t.Fatalf("expected no error when registry cache is disabled, got: %v", err)
	}
	if setup != nil {
		t.Fatalf("expected nil setup when registry cache is disabled, got: %+v", setup)
	}
}

// TestEnsureRegistryCaches_DisabledBy1 verifies that "1" is also accepted as
// the disable sentinel (mirrors standard boolean env-var conventions).
func TestEnsureRegistryCaches_DisabledBy1(t *testing.T) {
	t.Setenv(disableRegistryCacheEnvVar, "1")

	setup, err := ensureRegistryCaches(nil, nil, true)
	if err != nil {
		t.Fatalf("expected no error when registry cache is disabled via '1', got: %v", err)
	}
	if setup != nil {
		t.Fatalf("expected nil setup when registry cache is disabled via '1', got: %+v", setup)
	}
}

// ---------------------------------------------------------------------------
// Cache root / dir helpers
// ---------------------------------------------------------------------------

func TestDevRegistryCacheRootEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(devRegistryCacheEnvVar, tmpDir)

	if got := devRegistryCacheRoot(); got != tmpDir {
		t.Fatalf("devRegistryCacheRoot() = %q, want %q", got, tmpDir)
	}
}

func TestRegistryCacheDirUsesSharedRoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(devRegistryCacheEnvVar, tmpDir)

	got := registryCacheDir(pullThroughMirrors[0])
	want := filepath.Join(tmpDir, pullThroughMirrors[0].upstreamHost)
	if got != want {
		t.Fatalf("registryCacheDir() = %q, want %q", got, want)
	}
}

// TestRegistryCacheDir_LocalPushUsesUnderscore ensures colons in the
// localhost:54103 host are replaced with underscores in the on-disk path.
func TestRegistryCacheDir_LocalPushUsesUnderscore(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(devRegistryCacheEnvVar, tmpDir)

	got := registryCacheDir(localPushMirror)
	want := filepath.Join(tmpDir, "localhost_54103")
	if got != want {
		t.Fatalf("registryCacheDir(localPushMirror) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Golden snapshot: registries.yaml content by mode
// ---------------------------------------------------------------------------

// TestRegistriesConfigSnapshot_PullThrough is a lightweight golden test that
// locks the exact YAML produced for the default (non-dev) mode so regressions
// in port numbers, mirror names, or formatting are caught immediately.
func TestRegistriesConfigSnapshot_PullThrough(t *testing.T) {
	got := renderRegistriesConfig(pullThroughMirrors)

	want := `mirrors:
  "docker.io":
    endpoint:
      - http://k3d-obol-docker-io.localhost:5000
  "ghcr.io":
    endpoint:
      - http://k3d-obol-ghcr-io.localhost:5000
  "quay.io":
    endpoint:
      - http://k3d-obol-quay-io.localhost:5000
`
	if got != want {
		t.Fatalf("registries.yaml (pull-through mode) mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRegistriesConfigSnapshot_DevMode locks the YAML produced for
// OBOL_DEVELOPMENT=true (3 pull-through + local push target).
func TestRegistriesConfigSnapshot_DevMode(t *testing.T) {
	got := renderRegistriesConfig(allDevRegistryMirrors())

	want := `mirrors:
  "docker.io":
    endpoint:
      - http://k3d-obol-docker-io.localhost:5000
  "ghcr.io":
    endpoint:
      - http://k3d-obol-ghcr-io.localhost:5000
  "quay.io":
    endpoint:
      - http://k3d-obol-quay-io.localhost:5000
  "localhost:54103":
    endpoint:
      - http://k3d-obol-local.localhost:5000
`
	if got != want {
		t.Fatalf("registries.yaml (dev mode) mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
