package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseFake simulates one helm release whose revision goes up by one per
// helmfile sync the stub records.
type releaseFake struct {
	env        *testEnv
	exists     bool
	baseRev    int
	chart      string // e.g. dv-pod-0.23.0
	liveValues string // helm get values -o json
}

func (f *releaseFake) handle(tool, args string, _ []string, _ []byte) (string, error) {
	switch {
	case tool == "helm" && strings.HasPrefix(args, "history"):
		runs := f.env.helmfileRuns()
		if !f.exists && runs == 0 {
			return "", errors.New("Error: release: not found")
		}

		chart := f.chart
		if chart == "" {
			chart = "dv-pod-0.23.0"
		}

		return fmt.Sprintf(`[{"revision":%d,"status":"deployed","chart":%q}]`, f.baseRev+runs, chart), nil
	case tool == "helm" && strings.HasPrefix(args, "get values"):
		if f.liveValues == "" {
			return "null", nil
		}

		return f.liveValues, nil
	}

	return "", errors.New("unexpected call: " + tool + " " + args)
}

const overridesHelmfile = `# Installed from: obol/dv-pod@0.23.0

repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts

releases:
  - name: dv-pod-hoodi
    namespace: dv-pod-hoodi
    createNamespace: true
    chart: obol/dv-pod
    version: "0.23.0"
    values:
      - values.yaml
`

func writeManaged(t *testing.T, env *testEnv, syncedRev int, values string) string {
	t.Helper()

	dir := env.writeDeployment(t, "dv-pod", "hoodi", map[string]string{
		"helmfile.yaml": overridesHelmfile,
		"values.yaml":   values,
	})

	if err := saveAppMeta(dir, &AppMeta{
		Release: "dv-pod-hoodi", Namespace: "dv-pod-hoodi", ChartName: "dv-pod",
		ValuesMode: ValuesModeOverrides, ValuesChartVersion: "0.23.0", SyncedRevision: syncedRev,
	}); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestSync_RefusesReleaseChangedOutsideObolApp(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 3, "mode: launchpad\n")
	useFakeTools(t, (&releaseFake{
		env: env, exists: true, baseRev: 5,
		liveValues: `{"mode":"definition","dkg":{"definition":{"configHash":"0xabc"}}}`,
	}).handle)

	err := SyncWithOptions(env.cfg, env.u, "dv-pod/hoodi", SyncOptions{Set: []string{"x=1"}})
	if err == nil {
		t.Fatal("expected drift refusal")
	}

	for _, want := range []string{"changed outside obol app", "revision 5", "last synced revision 3", "dkg.definition.configHash", "mode", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	if strings.Contains(err.Error(), "0xabc") || strings.Contains(err.Error(), "definition\"") {
		t.Errorf("drift message must name keys, not values: %v", err)
	}

	if env.helmfileRuns() != 0 {
		t.Error("helmfile ran despite drift")
	}

	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if strings.Contains(string(values), "x:") {
		t.Error("--set applied although the sync was refused")
	}

	// --force: syncs and records the new revision.
	if err := SyncWithOptions(env.cfg, env.u, "dv-pod/hoodi", SyncOptions{Force: true}); err != nil {
		t.Fatalf("Sync --force: %v", err)
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.SyncedRevision != 6 {
		t.Errorf("SyncedRevision = %d, want 6", meta.SyncedRevision)
	}

	if !strings.Contains(env.stdout.String(), "--force: overwriting") {
		t.Errorf("missing --force warning:\n%s", env.stdout)
	}
}

func TestSync_RecordedRevisionMatches(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 3, "mode: launchpad\n")
	useFakeTools(t, (&releaseFake{env: env, exists: true, baseRev: 3}).handle)

	if err := SyncWithOptions(env.cfg, env.u, "dv-pod/hoodi", SyncOptions{Set: []string{"charon.operatorAddress=" + testAddress}}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.SyncedRevision != 4 {
		t.Errorf("SyncedRevision = %d, want 4", meta.SyncedRevision)
	}

	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if !strings.Contains(string(values), `"`+testAddress+`"`) {
		t.Errorf("--set not applied (quoted):\n%s", values)
	}
}

func TestSync_ForeignReleaseNeverSynced(t *testing.T) {
	env := newTestEnv(t, true)
	writeManaged(t, env, 0, "mode: launchpad\n")
	useFakeTools(t, (&releaseFake{env: env, exists: true, baseRev: 1}).handle)

	err := SyncWithOptions(env.cfg, env.u, "dv-pod/hoodi", SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "installed outside obol app") {
		t.Fatalf("expected foreign-release refusal, got %v", err)
	}
}

func TestSync_FreshClusterIsNotDrift(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 7, "mode: launchpad\n")
	useFakeTools(t, (&releaseFake{env: env, exists: false}).handle)

	if err := Sync(env.cfg, env.u, "dv-pod/hoodi"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.SyncedRevision != 1 {
		t.Errorf("SyncedRevision = %d, want 1", meta.SyncedRevision)
	}
}

func TestSync_LegacyWarnsAndAdopts(t *testing.T) {
	env := newTestEnv(t, true)

	// Pre-v0.19 deployment: full defaults, bare hex, helmfile on chart 0.24.0
	// while the live release runs 0.23.0.
	dir := env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.24.0"),
		"values.yaml":   "image:\n  tag: v1.11.3\ntests:\n  dkgSidecar:\n    operator: " + testAddress + "\n",
	})
	useFakeTools(t, (&releaseFake{
		env: env, exists: true, baseRev: 3,
		liveValues: `{"image":{"tag":"v1.11.3"},"tests":{"dkgSidecar":{"operator":3.49e+47}},"extra":true}`,
	}).handle)

	if err := Sync(env.cfg, env.u, "dvtee/a"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	out := env.stdout.String()

	for _, want := range []string{
		"before obol app kept a sync record", "extra", // legacy values drift: warn only
		"0.23.0 → 0.24.0", "stay pinned", // stale full defaults
		"unquoted hex", "tests.dkgSidecar.operator", // bare hex
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "dkgSidecar.operator,") || strings.Contains(out, "differ between the live release and values.yaml: extra, tests") {
		t.Errorf("bare hex vs float must not count as drift:\n%s", out)
	}

	meta, _ := loadAppMeta(dir, "dvtee", "a")
	if meta.Legacy() || meta.SyncedRevision != 4 || meta.Release != "dvtee-a" || meta.ValuesMode != ValuesModeFullDefaults {
		t.Errorf("meta after legacy sync = %+v", meta)
	}

	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))
	if string(helmfile) != legacyHelmfile("dvtee-a", "0.24.0") {
		t.Error("sync must never rewrite helmfile.yaml")
	}
}

func TestSync_FailedSyncStillRecordsRevision(t *testing.T) {
	env := newTestEnv(t, true)
	dir := writeManaged(t, env, 3, "mode: launchpad\n")
	useFakeTools(t, (&releaseFake{env: env, exists: true, baseRev: 3}).handle)
	env.failHelmfile(t)

	if err := Sync(env.cfg, env.u, "dv-pod/hoodi"); err == nil {
		t.Fatal("expected helmfile failure")
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.SyncedRevision != 4 {
		t.Errorf("SyncedRevision = %d, want 4 (a failed upgrade still adds our revision)", meta.SyncedRevision)
	}
}

func TestValuesDiff(t *testing.T) {
	file := map[string]any{"a": 1, "b": map[string]any{"c": "x", "d": []any{}}, "hex": testAddress}
	live := map[string]any{"a": float64(1), "b": map[string]any{"c": "y"}, "hex": 3.49e47, "e": true}

	got := valuesDiff(file, live)
	if strings.Join(got, ",") != "b.c,e" {
		t.Errorf("valuesDiff = %v", got)
	}
}

// Reinstalling with --force (the slimming path the stale-defaults warning
// suggests) keeps the sync record: the next sync is not refused as
// foreign, and real drift is still caught.
func TestInstallForceThenSync(t *testing.T) {
	for _, tc := range []struct {
		name      string
		legacy    bool
		synced    int
		liveRev   int
		wantError bool
	}{
		{name: "legacy app adopts live revision", legacy: true, liveRev: 3},
		{name: "managed app in sync", synced: 3, liveRev: 3},
		{name: "managed app with drift", synced: 3, liveRev: 5, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, true)
			noArtifactHub(t)

			if tc.legacy {
				env.writeDeployment(t, "dvtee", "a", map[string]string{
					"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
					"values.yaml":   "image:\n  tag: v1\n",
				})
			} else {
				dir := env.writeDeployment(t, "dvtee", "a", map[string]string{
					"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
					"values.yaml":   "mode: launchpad\n",
				})
				if err := saveAppMeta(dir, &AppMeta{Release: "dvtee-a", Namespace: "dvtee-a", ValuesMode: ValuesModeOverrides, SyncedRevision: tc.synced}); err != nil {
					t.Fatal(err)
				}
			}

			chart := chartFake{version: "0.23.0", example: dvPodExample}
			rel := &releaseFake{env: env, exists: true, baseRev: tc.liveRev}
			useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
				if strings.HasPrefix(args, "history") || strings.HasPrefix(args, "get values") {
					return rel.handle(tool, args, argv, stdin)
				}

				return chart.handle(tool, args, argv, stdin)
			})

			if err := Install(env.cfg, env.u, "obol/dv-pod@0.23.0", InstallOptions{Name: "dvtee", ID: "a", Force: true}); err != nil {
				t.Fatalf("Install --force: %v", err)
			}

			err := Sync(env.cfg, env.u, "dvtee/a")
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "changed outside obol app") {
					t.Fatalf("expected drift refusal, got %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Sync after install --force: %v", err)
			}
		})
	}
}
