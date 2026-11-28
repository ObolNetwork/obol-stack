package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgrade_BumpsVersionAndSyncs(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 3, "mode: enr\n")
	rel := &releaseFake{env: env, exists: true, baseRev: 3}
	chart := chartFake{version: "0.24.0"}
	calls := useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
		if strings.HasPrefix(args, "show chart") {
			return chart.handle(tool, args, argv, stdin)
		}

		return rel.handle(tool, args, argv, stdin)
	})

	if err := Upgrade(env.cfg, env.u, "dv-pod/hoodi", UpgradeOptions{Version: "0.24.0"}); err != nil {
		t.Fatalf("Upgrade: %v\n%s", err, env.stdout)
	}

	if !hasCall(*calls, "helm", "show chart dv-pod --repo https://obolnetwork.github.io/helm-charts --version 0.24.0") {
		t.Errorf("target version not checked: %+v", *calls)
	}

	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))
	h := string(helmfile)

	for _, want := range []string{`version: "0.24.0"`, "# Installed from: obol/dv-pod@0.23.0", "name: dv-pod-hoodi", "namespace: dv-pod-hoodi"} {
		if !strings.Contains(h, want) {
			t.Errorf("helmfile.yaml missing %q:\n%s", want, h)
		}
	}

	if info, err := ParseHelmfile(dir); err != nil || info.Version != "0.24.0" || info.Release != "dv-pod-hoodi" {
		t.Errorf("ParseHelmfile = %+v, %v", info, err)
	}

	if env.helmfileRuns() != 1 {
		t.Errorf("helmfile runs = %d, want 1", env.helmfileRuns())
	}
}

func TestUpgrade_DriftRestoresHelmfile(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 3, "mode: enr\n")
	useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
		if strings.HasPrefix(args, "show chart") {
			return chartFake{version: "0.24.0"}.handle(tool, args, argv, stdin)
		}

		return (&releaseFake{env: env, exists: true, baseRev: 5}).handle(tool, args, argv, stdin)
	})

	err := Upgrade(env.cfg, env.u, "dv-pod/hoodi", UpgradeOptions{Version: "0.24.0"})
	if !errors.Is(err, ErrDrift) {
		t.Fatalf("expected drift refusal, got %v", err)
	}

	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))
	if string(helmfile) != overridesHelmfile {
		t.Errorf("helmfile.yaml not restored:\n%s", helmfile)
	}
}

func TestUpgrade_WarnsOnFullDefaultsAndMissingVersion(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "image:\n  tag: v1.11.3\n",
	})
	useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
		if strings.HasPrefix(args, "show chart") {
			return chartFake{version: "0.24.0"}.handle(tool, args, argv, stdin)
		}

		return (&releaseFake{env: env, exists: true, baseRev: 3}).handle(tool, args, argv, stdin)
	})

	if err := Upgrade(env.cfg, env.u, "dvtee/a", UpgradeOptions{Version: "9.9.9"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing version accepted: %v", err)
	}

	if env.helmfileRuns() != 0 {
		t.Fatal("synced to a missing version")
	}

	if err := Upgrade(env.cfg, env.u, "dvtee/a", UpgradeOptions{Version: "0.24.0"}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	if !strings.Contains(env.stdout.String(), "full copy of chart 0.23.0's defaults") {
		t.Errorf("no full-defaults warning:\n%s", env.stdout)
	}
}

func TestSetHelmfileVersion_RefusesVersionedURL(t *testing.T) {
	hf := "releases:\n  - name: r\n    chart: https://x/redis-19.0.0.tgz\n"
	if _, _, err := setHelmfileVersion([]byte(hf), "20.0.0"); err == nil {
		t.Error("versioned archive URL accepted")
	}
}
