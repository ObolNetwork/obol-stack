package stack

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDevRegistriesConfig(t *testing.T) {
	config := renderDevRegistriesConfig()

	for _, mirror := range devRegistryMirrors {
		if !strings.Contains(config, `"`+mirror.upstreamHost+`"`) {
			t.Fatalf("config missing mirror for %s", mirror.upstreamHost)
		}

		if !strings.Contains(config, registryEndpoint(mirror)) {
			t.Fatalf("config missing endpoint %s", registryEndpoint(mirror))
		}
	}
}

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
	setup := &devRegistrySetup{
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

	got := registryCacheDir(devRegistryMirrors[0])
	want := filepath.Join(tmpDir, devRegistryMirrors[0].upstreamHost)
	if got != want {
		t.Fatalf("registryCacheDir() = %q, want %q", got, want)
	}
}
