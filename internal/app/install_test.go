package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const dvPodExample = `# dv-pod in the Obol Stack (k3d, single node).
#
# Launchpad route (default here): ...

network: mainnet   # hoodi for a testnet run

mode: launchpad

charon:
  operatorAddress: "" # set with --set charon.operatorAddress=0x...
  nickname: "obol-stack"

statusReaders:
  - namespace: hermes-obol-agent
    serviceAccount: hermes

serviceMonitor:
  enabled: true
  labels:
    release: monitoring
`

// chartFake answers helm repo/show/pull calls for a dv-pod-like chart.
type chartFake struct {
	repos      string // helm repo list -o json
	version    string
	example    string // values-examples/obol-stack.yaml ("" = none)
	liveRev    int    // >0: a release exists in the cluster
	liveValues string
}

func (f chartFake) handle(tool, args string, argv []string, _ []byte) (string, error) {
	switch {
	case tool == "helm" && args == "repo list -o json":
		if f.repos == "" {
			return "", errors.New("Error: no repositories to show")
		}

		return f.repos, nil
	case tool == "helm" && strings.HasPrefix(args, "show chart"):
		if strings.Contains(args, "--version 9.9.9") {
			return "", errors.New("Error: chart \"dv-pod\" version \"9.9.9\" not found")
		}

		return "apiVersion: v2\nname: dv-pod\nversion: " + f.version + "\n", nil
	case tool == "helm" && strings.HasPrefix(args, "pull"):
		dir := ""

		for i, a := range argv {
			if a == "--untardir" && i+1 < len(argv) {
				dir = argv[i+1]
			}
		}

		chartDir := filepath.Join(dir, "dv-pod")
		_ = os.MkdirAll(filepath.Join(chartDir, "values-examples"), 0o755)
		_ = os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("name: dv-pod\nversion: "+f.version+"\n"), 0o600)
		_ = os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("image:\n  tag: v1.0.0\n"), 0o600)

		if f.example != "" {
			_ = os.WriteFile(filepath.Join(chartDir, "values-examples", "obol-stack.yaml"), []byte(f.example), 0o600)
		}

		return "", nil
	case tool == "helm" && strings.HasPrefix(args, "history"):
		if f.liveRev == 0 {
			return "", errors.New("Error: release: not found")
		}

		return `[{"revision":` + itoa(f.liveRev) + `,"status":"deployed","chart":"dv-pod-0.22.0"}]`, nil
	case tool == "helm" && strings.HasPrefix(args, "get values"):
		return f.liveValues, nil
	}

	return "", errors.New("unexpected call: " + tool + " " + args)
}

func itoa(n int) string {
	b, _ := yaml.Marshal(n)
	return strings.TrimSpace(string(b))
}

func noArtifactHub(t *testing.T) {
	t.Helper()

	orig := newArtifactHubResolver
	newArtifactHubResolver = func() func(string) (*ChartInfo, error) {
		return func(ref string) (*ChartInfo, error) {
			t.Errorf("ArtifactHub queried for %s", ref)
			return nil, errors.New("no network in tests")
		}
	}

	t.Cleanup(func() { newArtifactHubResolver = orig })
}

func TestInstall_ObolAliasDvPodStartsFromStackExample(t *testing.T) {
	env := newTestEnv(t, false)
	noArtifactHub(t)
	calls := useFakeTools(t, chartFake{version: "0.23.0", example: dvPodExample}.handle)

	err := Install(env.cfg, env.u, "obol/dv-pod@0.23.0", InstallOptions{
		Name: "dvtee",
		ID:   "a",
		Set:  []string{"charon.operatorAddress=" + testAddress, "network=hoodi"},
	})
	if err != nil {
		t.Fatalf("Install: %v\n%s", err, env.stdout)
	}

	if !hasCall(*calls, "helm", "show chart dv-pod --repo "+ObolChartRepoURL+" --version 0.23.0") {
		t.Errorf("obol alias not resolved to %s: %+v", ObolChartRepoURL, *calls)
	}

	dir := deploymentDirFor(env.cfg, "dvtee", "a")

	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))
	v := string(values)

	for _, want := range []string{
		"# Values for app dvtee/a (chart dv-pod 0.23.0).",
		"obol helm show values dv-pod --repo " + ObolChartRepoURL + " --version 0.23.0",
		"values-examples/obol-stack.yaml",
		"# dv-pod in the Obol Stack (k3d, single node).", // example's own header kept
		"serviceAccount: hermes",                         // statusReaders from the example
		"release: monitoring",                            // ServiceMonitor from the example
		`operatorAddress: "` + testAddress + `"`,         // override, quoted
		"network: hoodi # hoodi for a testnet run",       // comment kept on the replaced scalar
	} {
		if !strings.Contains(v, want) {
			t.Errorf("values.yaml missing %q:\n%s", want, v)
		}
	}

	if strings.Contains(v, "tag: v1.0.0") {
		t.Errorf("values.yaml must not copy chart defaults:\n%s", v)
	}

	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))
	for _, want := range []string{"name: dvtee-a\n", "namespace: dvtee-a\n", "chart: obol/dv-pod", `version: "0.23.0"`, "url: " + ObolChartRepoURL} {
		if !strings.Contains(string(helmfile), want) {
			t.Errorf("helmfile.yaml missing %q:\n%s", want, helmfile)
		}
	}

	meta, err := loadAppMeta(dir, "dvtee", "a")
	if err != nil {
		t.Fatal(err)
	}

	if meta.Legacy() || meta.Release != "dvtee-a" || meta.ValuesMode != ValuesModeOverrides || meta.ChartName != "dv-pod" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestInstall_LocalRepoAliasWinsAndReleaseFlag(t *testing.T) {
	env := newTestEnv(t, false)
	noArtifactHub(t)
	calls := useFakeTools(t, chartFake{
		repos:   `[{"name":"obol","url":"https://example.invalid/mirror"}]`,
		version: "0.23.1",
	}.handle)

	err := Install(env.cfg, env.u, "obol/dv-pod", InstallOptions{Name: "dv-pod", ID: "hoodi", Release: "dv-pod-hoodi"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !hasCall(*calls, "helm", "show chart dv-pod --repo https://example.invalid/mirror") {
		t.Errorf("local alias not preferred: %+v", *calls)
	}

	dir := deploymentDirFor(env.cfg, "dv-pod", "hoodi")
	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))

	if !strings.Contains(string(helmfile), "name: dv-pod-hoodi\n") || !strings.Contains(string(helmfile), `version: "0.23.1"`) {
		t.Errorf("helmfile.yaml:\n%s", helmfile)
	}

	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if strings.Contains(string(values), "tag: v1.0.0") || strings.Contains(string(values), "{}") {
		t.Errorf("values.yaml should only hold the header (no overrides):\n%s", values)
	}
}

func TestInstall_UnknownRepoFallsBackToArtifactHub(t *testing.T) {
	env := newTestEnv(t, false)
	useFakeTools(t, chartFake{version: "0.23.0"}.handle)

	var asked string

	orig := newArtifactHubResolver
	newArtifactHubResolver = func() func(string) (*ChartInfo, error) {
		return func(ref string) (*ChartInfo, error) {
			asked = ref
			return &ChartInfo{RepoName: "charon", RepoURL: ObolChartRepoURL, ChartName: "dv-pod", Version: "0.23.0"}, nil
		}
	}

	t.Cleanup(func() { newArtifactHubResolver = orig })

	if err := Install(env.cfg, env.u, "charon/dv-pod@0.23.0", InstallOptions{Name: "dv", ID: "x"}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if asked != "charon/dv-pod@0.23.0" {
		t.Errorf("ArtifactHub asked %q", asked)
	}
}

func TestInstall_MissingVersionInAliasRepo(t *testing.T) {
	env := newTestEnv(t, false)
	noArtifactHub(t)
	useFakeTools(t, chartFake{version: "0.23.0"}.handle)

	err := Install(env.cfg, env.u, "obol/dv-pod@9.9.9", InstallOptions{Name: "dv", ID: "x"})
	if err == nil || !strings.Contains(err.Error(), "version 9.9.9 not found in obol") {
		t.Fatalf("err = %v", err)
	}

	if _, statErr := os.Stat(deploymentDirFor(env.cfg, "dv", "x")); statErr == nil {
		t.Error("deployment dir created for a missing chart")
	}
}

func TestInstall_RefusesForeignReleaseUnlessAdopt(t *testing.T) {
	env := newTestEnv(t, true)
	noArtifactHub(t)

	fake := chartFake{
		version: "0.23.0", example: dvPodExample, liveRev: 4,
		liveValues: "charon:\n  operatorAddress: \"" + testAddress + "\"\nmode: definition\n",
	}
	useFakeTools(t, fake.handle)

	err := Install(env.cfg, env.u, "obol/dv-pod@0.23.0", InstallOptions{Name: "dv-pod", ID: "hoodi", Release: "dv-pod-hoodi"})
	if err == nil || !strings.Contains(err.Error(), "not managed by obol app") || !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("expected foreign-release refusal, got %v", err)
	}

	err = Install(env.cfg, env.u, "obol/dv-pod@0.23.0", InstallOptions{Name: "dv-pod", ID: "hoodi", Release: "dv-pod-hoodi", Adopt: true})
	if err != nil {
		t.Fatalf("Install --adopt: %v", err)
	}

	dir := deploymentDirFor(env.cfg, "dv-pod", "hoodi")
	values, _ := os.ReadFile(filepath.Join(dir, "values.yaml"))

	if !strings.Contains(string(values), "mode: definition") || !strings.Contains(string(values), `"`+testAddress+`"`) {
		t.Errorf("adopted values not seeded from the release:\n%s", values)
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.SyncedRevision != 4 {
		t.Errorf("SyncedRevision = %d, want 4 (adopted revision)", meta.SyncedRevision)
	}
}

func TestInstall_ForceKeepsExistingLegacyRelease(t *testing.T) {
	env := newTestEnv(t, false)
	noArtifactHub(t)
	useFakeTools(t, chartFake{version: "0.23.0"}.handle)

	// A pre-v0.19 deployment whose helmfile was hand-edited to release dvtee-a.
	env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "image:\n  tag: v1\n",
	})

	if err := Install(env.cfg, env.u, "obol/dv-pod@0.23.0", InstallOptions{Name: "dvtee", ID: "a", Force: true}); err != nil {
		t.Fatalf("Install --force: %v", err)
	}

	helmfile, _ := os.ReadFile(filepath.Join(deploymentDirFor(env.cfg, "dvtee", "a"), "helmfile.yaml"))
	if !strings.Contains(string(helmfile), "name: dvtee-a\n") {
		t.Errorf("--force must keep the existing release name:\n%s", helmfile)
	}
}

func TestLoadAppMeta_LegacyReadsHelmfile(t *testing.T) {
	env := newTestEnv(t, false)

	dir := env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "a: 1\n",
	})

	meta, err := loadAppMeta(dir, "dvtee", "a")
	if err != nil {
		t.Fatal(err)
	}

	if !meta.Legacy() || meta.Release != "dvtee-a" || meta.Namespace != "dvtee-a" ||
		meta.ChartName != "dv-pod" || meta.ValuesMode != ValuesModeFullDefaults || meta.ValuesChartVersion != "0.23.0" {
		t.Errorf("meta = %+v", meta)
	}

	// Unedited pre-v0.19 helmfile: release <name>.
	dir2 := env.writeDeployment(t, "redis", "x", map[string]string{"values.yaml": "a: 1\n"})

	meta2, _ := loadAppMeta(dir2, "redis", "x")
	if meta2.Release != "redis" || meta2.Namespace != "redis-x" {
		t.Errorf("meta2 = %+v", meta2)
	}
}

func TestChartNameFromRef(t *testing.T) {
	for in, want := range map[string]string{
		"charon/dv-pod":              "dv-pod",
		"obol/dv-pod@0.23.0":         "dv-pod",
		"https://x/redis-19.0.0.tgz": "redis",
		"oci://registry-1.docker.io/bitnamicharts/redis:19.0.0": "redis",
	} {
		if got := chartNameFromRef(in); got != want {
			t.Errorf("chartNameFromRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateReleaseName(t *testing.T) {
	for _, ok := range []string{"dvtee-a", "dv-pod-hoodi", "a"} {
		if err := ValidateReleaseName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}

	for _, bad := range []string{"", "Dv", "-a", "a_b", strings.Repeat("a", 54)} {
		if err := ValidateReleaseName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A dv-pod installed before v0.19 with 'obol helm upgrade --install
// my-dv-pod … -n dv-pod' can be adopted in place, and sync, the drift
// check and secrets then use its namespace.
func TestInstall_AdoptInCustomNamespace(t *testing.T) {
	env := newTestEnv(t, true)
	noArtifactHub(t)

	fake := chartFake{
		version: "0.24.0", example: dvPodExample, liveRev: 3,
		liveValues: "network: hoodi\nmode: enr\n",
	}
	calls := useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
		if strings.HasPrefix(args, "history") || strings.HasPrefix(args, "get values") {
			if !strings.Contains(args, "my-dv-pod -n dv-pod ") {
				t.Errorf("release/namespace not used: %s", args)
			}
			// One revision more per helmfile sync.
			fake.liveRev = 3 + env.helmfileRuns()
		}

		return fake.handle(tool, args, argv, stdin)
	})

	err := Install(env.cfg, env.u, "obol/dv-pod@0.24.0", InstallOptions{
		Name: "dv-pod", ID: "hoodi", Namespace: "dv-pod", Release: "my-dv-pod", Adopt: true,
	})
	if err != nil {
		t.Fatalf("Install --adopt --namespace: %v", err)
	}

	dir := deploymentDirFor(env.cfg, "dv-pod", "hoodi")

	helmfile, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml"))
	if !strings.Contains(string(helmfile), "name: my-dv-pod\n") || !strings.Contains(string(helmfile), "namespace: dv-pod\n") {
		t.Errorf("helmfile.yaml:\n%s", helmfile)
	}

	meta, _ := loadAppMeta(dir, "dv-pod", "hoodi")
	if meta.Namespace != "dv-pod" || meta.Release != "my-dv-pod" || meta.SyncedRevision != 3 {
		t.Errorf("meta = %+v", meta)
	}

	// Sync: adopted revision 3 is live → no drift.
	if err := Sync(env.cfg, env.u, "dv-pod/hoodi"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Someone runs helm upgrade directly: drift in the custom namespace.
	env.failHelmfile(t) // keeps the stub counting runs without success
	fake.liveRev = 9

	*calls = nil

	useFakeTools(t, func(tool, args string, argv []string, stdin []byte) (string, error) {
		if strings.HasPrefix(args, "history") {
			return `[{"revision":9,"status":"deployed","chart":"dv-pod-0.24.0"}]`, nil
		}

		return fake.handle(tool, args, argv, stdin)
	})

	err = Sync(env.cfg, env.u, "dv-pod/hoodi")
	if err == nil || !strings.Contains(err.Error(), "changed outside obol app") || !strings.Contains(err.Error(), "-n dv-pod") {
		t.Fatalf("expected drift refusal in namespace dv-pod, got %v", err)
	}

	ns, err := appNamespace(env.cfg, "dv-pod/hoodi")
	if err != nil || ns != "dv-pod" {
		t.Errorf("appNamespace = %q, %v", ns, err)
	}
}

func TestInstall_RejectsStackNamespaces(t *testing.T) {
	env := newTestEnv(t, false)
	noArtifactHub(t)
	useFakeTools(t, chartFake{version: "0.24.0"}.handle)

	for _, ns := range []string{"kube-system", "traefik", "x402", "llm", "erpc", "monitoring", "obol-frontend", "hermes-obol-agent", "ethereum-hoodi", "Bad_NS"} {
		err := Install(env.cfg, env.u, "obol/dv-pod@0.24.0", InstallOptions{Name: "dv", ID: "x", Namespace: ns})
		if err == nil {
			t.Errorf("namespace %q accepted", ns)
		}
	}

	// A default namespace that lands on a reserved prefix is refused too.
	if err := Install(env.cfg, env.u, "obol/dv-pod@0.24.0", InstallOptions{Name: "hermes", ID: "x"}); err == nil || !strings.Contains(err.Error(), "--namespace") {
		t.Errorf("hermes-x accepted or no hint: %v", err)
	}

	if _, err := os.Stat(deploymentDirFor(env.cfg, "dv", "x")); err == nil {
		t.Error("deployment dir created for a refused namespace")
	}
}
