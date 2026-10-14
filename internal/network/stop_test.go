package network

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

const testValues = `# Configuration via CLI flags
network: mainnet
executionClient: reth
consensusClient: lighthouse
mode: full
`

func writeTestDeployment(t *testing.T, cfg *config.Config, typ, id, values string) string {
	t.Helper()

	dir := filepath.Join(cfg.ConfigDir, "networks", typ, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(path, []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// fakeStopCluster swaps every cluster call of Stop/Start for recorders.
type fakeStopCluster struct {
	kubectl      [][]string
	registered   []string
	deregistered []string
	synced       []string
	clusterDown  bool
	workloads    string
}

func installFakeStopCluster(t *testing.T) *fakeStopCluster {
	t.Helper()

	f := &fakeStopCluster{workloads: "statefulset.apps/ethereum-execution\nstatefulset.apps/ethereum-beacon\n"}

	origEnsure, origKubectl, origReg, origDereg, origSync, origDisk := stopEnsureClusterFn, stopKubectlFn, stopRegisterFn, stopDeregisterFn, stopSyncFn, stopDiskUsageFn
	t.Cleanup(func() {
		stopEnsureClusterFn, stopKubectlFn, stopRegisterFn, stopDeregisterFn, stopSyncFn, stopDiskUsageFn = origEnsure, origKubectl, origReg, origDereg, origSync, origDisk
	})

	stopEnsureClusterFn = func(*config.Config) error {
		if f.clusterDown {
			return errors.New("cluster down")
		}
		return nil
	}
	stopKubectlFn = func(_ *config.Config, args ...string) (string, error) {
		f.kubectl = append(f.kubectl, args)
		switch args[0] {
		case "get":
			return f.workloads, nil
		case "scale":
			if f.workloads == "" {
				return "", errors.New("exit status 1: error: no objects passed to scale")
			}
			var out strings.Builder
			for l := range strings.SplitSeq(strings.TrimSpace(f.workloads), "\n") {
				out.WriteString(l + " scaled\n")
			}
			return out.String(), nil
		}
		return "", fmt.Errorf("unexpected kubectl %v", args)
	}
	stopRegisterFn = func(_ *config.Config, typ, id string) error {
		f.registered = append(f.registered, typ+"/"+id)
		return nil
	}
	stopDeregisterFn = func(_ *config.Config, typ, id string) error {
		f.deregistered = append(f.deregistered, typ+"/"+id)
		return nil
	}
	stopSyncFn = func(_ *config.Config, _ *ui.UI, ident string) error {
		f.synced = append(f.synced, ident)
		return nil
	}
	stopDiskUsageFn = func(*config.Config, string) uint64 { return 450 << 30 }

	return f
}

func TestStopStart(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	path := writeTestDeployment(t, cfg, "ethereum", "mainnet", testValues)
	f := installFakeStopCluster(t)

	var out bytes.Buffer
	u := ui.NewForTest(&out, &out)

	if err := Stop(cfg, u, "ethereum/mainnet"); err != nil {
		t.Fatal(err)
	}

	wantScale := []string{"scale", "statefulset,deployment", "--all", "-n", "ethereum-mainnet", "--replicas=0"}
	if len(f.kubectl) != 1 || !reflect.DeepEqual(f.kubectl[0], wantScale) {
		t.Fatalf("kubectl = %v", f.kubectl)
	}

	if !reflect.DeepEqual(f.deregistered, []string{"ethereum/mainnet"}) {
		t.Fatalf("deregistered = %v", f.deregistered)
	}

	if d := readDeployment(cfg, "ethereum", "mainnet"); !d.Stopped || d.Network != "mainnet" {
		t.Fatalf("values.yaml after stop: %+v", d)
	}

	for _, want := range []string{"statefulset/ethereum-execution", "450 GiB", "obol network start ethereum/mainnet"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stop output missing %q:\n%s", want, out.String())
		}
	}

	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "# Configuration via CLI flags") {
		t.Fatalf("stop rewrote the file instead of appending:\n%s", raw)
	}

	// Stopping again is idempotent: one stopped line.
	if err := Stop(cfg, u, "ethereum/mainnet"); err != nil {
		t.Fatal(err)
	}

	raw, _ = os.ReadFile(path)
	if strings.Count(string(raw), "stopped:") != 1 {
		t.Fatalf("values.yaml:\n%s", raw)
	}

	f.kubectl = nil
	out.Reset()

	if err := Start(cfg, u, "ethereum/mainnet"); err != nil {
		t.Fatal(err)
	}

	if len(f.kubectl) != 2 || f.kubectl[1][len(f.kubectl[1])-1] != "--replicas=1" {
		t.Fatalf("start kubectl = %v", f.kubectl)
	}

	if !reflect.DeepEqual(f.registered, []string{"ethereum/mainnet"}) || len(f.synced) != 0 {
		t.Fatalf("registered = %v synced = %v", f.registered, f.synced)
	}

	if readDeployment(cfg, "ethereum", "mainnet").Stopped {
		t.Fatal("start must clear stopped")
	}
}

func TestStart_SyncsWhenWorkloadsAreGone(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	writeTestDeployment(t, cfg, "ethereum", "mainnet", testValues+"stopped: true\n")
	f := installFakeStopCluster(t)
	f.workloads = "" // cluster recreated: nothing in the namespace

	if err := Start(cfg, ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{}), "ethereum/mainnet"); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(f.synced, []string{"ethereum/mainnet"}) {
		t.Fatalf("synced = %v, want a full sync", f.synced)
	}
}

func TestStop_ClusterDownOnlyRecords(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	writeTestDeployment(t, cfg, "ethereum", "mainnet", testValues)
	f := installFakeStopCluster(t)
	f.clusterDown = true

	if err := Stop(cfg, ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{}), "ethereum/mainnet"); err != nil {
		t.Fatal(err)
	}

	if len(f.kubectl) != 0 || len(f.deregistered) != 0 || !readDeployment(cfg, "ethereum", "mainnet").Stopped {
		t.Fatalf("kubectl=%v deregistered=%v", f.kubectl, f.deregistered)
	}
}

func TestStop_RejectsUnsupported(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	writeTestDeployment(t, cfg, "aztec", "seq", "network: testnet\n")
	installFakeStopCluster(t)

	u := ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{})
	if err := Stop(cfg, u, "aztec/seq"); err == nil || !strings.Contains(err.Error(), "ethereum deployments only") {
		t.Fatalf("err = %v", err)
	}

	if err := Stop(cfg, u, "ethereum/nope"); err == nil {
		t.Fatal("missing deployment accepted")
	}
}

func TestResumeInstalled_LeavesStoppedNodesDown(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	writeTestDeployment(t, cfg, "ethereum", "mainnet", testValues+"stopped: true\n")
	writeTestDeployment(t, cfg, "ethereum", "hoodi", strings.Replace(testValues, "mainnet", "hoodi", 1))

	origSync, origHealth, origRegister := resumeSyncFn, resumeReleaseHealthFn, resumeRegisterERPCFn
	t.Cleanup(func() { resumeSyncFn, resumeReleaseHealthFn, resumeRegisterERPCFn = origSync, origHealth, origRegister })

	var checked, registered, synced []string
	resumeReleaseHealthFn = func(_ *config.Config, ns string) (helmcmd.ReleaseHealth, error) {
		checked = append(checked, ns)
		if ns == "ethereum-mainnet" {
			return helmcmd.ReleasesMissing, nil
		}
		return helmcmd.ReleasesDeployed, nil
	}
	resumeRegisterERPCFn = func(_ *config.Config, typ, id string) error {
		registered = append(registered, typ+"/"+id)
		return nil
	}
	resumeSyncFn = func(_ *config.Config, _ *ui.UI, ident string) error {
		synced = append(synced, ident)
		return nil
	}

	if err := ResumeInstalled(cfg, ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}

	if slices.Contains(checked, "ethereum-mainnet") || len(synced) != 0 || !reflect.DeepEqual(registered, []string{"ethereum/hoodi"}) {
		t.Fatalf("checked=%v synced=%v registered=%v; the stopped node must be neither synced nor registered", checked, synced, registered)
	}
}

func TestSetValuesStopped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte("network: hoodi"), 0o600); err != nil {
		t.Fatal(err)
	}

	if changed, err := setValuesStopped(path, false); err != nil || changed {
		t.Fatalf("clearing an absent key: changed=%v err=%v", changed, err)
	}

	if changed, err := setValuesStopped(path, true); err != nil || !changed {
		t.Fatalf("set: changed=%v err=%v", changed, err)
	}

	if changed, _ := setValuesStopped(path, false); !changed {
		t.Fatal("clear: not changed")
	}

	raw, _ := os.ReadFile(path)

	var v map[string]any
	if err := yaml.Unmarshal(raw, &v); err != nil || v["stopped"] != false || v["network"] != "hoodi" {
		t.Fatalf("values = %v (%v):\n%s", v, err, raw)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

// renderEthereumHelmfile renders the embedded helmfile with the subset of
// helmfile's sprig functions it uses, so its values logic is testable
// without helmfile.
func renderEthereumHelmfile(t *testing.T, values map[string]any) map[string]any {
	t.Helper()

	src, err := embed.ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	empty := func(v any) bool {
		switch x := v.(type) {
		case nil:
			return true
		case string:
			return x == ""
		case bool:
			return !x
		case int:
			return x == 0
		}
		return false
	}

	funcs := template.FuncMap{
		"default": func(def any, v ...any) any {
			if len(v) == 0 || empty(v[0]) {
				return def
			}
			return v[0]
		},
		"has": func(needle any, list []any) bool { return slices.Contains(list, needle) },
		"list": func(v ...any) []any {
			return v
		},
		"fail":  func(msg string) (string, error) { return "", errors.New(msg) },
		"quote": func(v any) string { return fmt.Sprintf("%q", fmt.Sprint(v)) },
	}

	tmpl, err := template.New("helmfile").Funcs(funcs).Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}

	values["id"] = "x"

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Values": values}); err != nil {
		t.Fatal(err)
	}

	var state map[string]any
	if err := yaml.Unmarshal(buf.Bytes(), &state); err != nil {
		t.Fatalf("rendered helmfile is not YAML: %v\n%s", err, buf.String())
	}

	return state
}

// clientValues returns the values block for a client of the "ethereum" release.
func clientValues(t *testing.T, state map[string]any, client string) map[string]any {
	t.Helper()

	for _, r := range state["releases"].([]any) {
		rel := r.(map[string]any)
		if rel["name"] != "ethereum" {
			continue
		}

		for _, v := range rel["values"].([]any) {
			if m, ok := v.(map[string]any)[client].(map[string]any); ok {
				return m
			}
		}
	}

	t.Fatalf("no values for %s", client)

	return nil
}

func TestEthereumHelmfile_ReplicasAndRequests(t *testing.T) {
	cases := []struct {
		name               string
		values             map[string]any
		replicas           int
		elMemory, clMemory string
		elCPU              string
	}{
		{"mainnet full", map[string]any{"network": "mainnet", "executionClient": "reth", "consensusClient": "lighthouse", "mode": "full"}, 1, "8Gi", "6Gi", "2"},
		{"mainnet archive stopped", map[string]any{"network": "mainnet", "executionClient": "reth", "consensusClient": "lighthouse", "mode": "archive", "stopped": true}, 0, "12Gi", "6Gi", "2"},
		{"hoodi nimbus", map[string]any{"network": "hoodi", "executionClient": "geth", "consensusClient": "nimbus", "mode": "full"}, 1, "2Gi", "2Gi", "500m"},
		{"sepolia archive", map[string]any{"network": "sepolia", "executionClient": "reth", "consensusClient": "teku", "mode": "archive"}, 1, "4Gi", "2Gi", "500m"},
		{"overrides, empty since", map[string]any{
			"network": "mainnet", "executionClient": "reth", "consensusClient": "lighthouse", "mode": "full",
			"since": nil, "executionMemory": "16Gi", "consensusMemory": "10Gi", "stopped": false,
		}, 1, "16Gi", "10Gi", "2"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := renderEthereumHelmfile(t, c.values)

			el := clientValues(t, state, c.values["executionClient"].(string))
			cl := clientValues(t, state, c.values["consensusClient"].(string))

			for side, m := range map[string]map[string]any{"execution": el, "consensus": cl} {
				if m["replicas"] != c.replicas {
					t.Errorf("%s replicas = %v, want %d", side, m["replicas"], c.replicas)
				}

				res := m["resources"].(map[string]any)
				if _, ok := res["limits"]; ok {
					t.Errorf("%s: requests only, no limits", side)
				}
			}

			elReq := el["resources"].(map[string]any)["requests"].(map[string]any)
			clReq := cl["resources"].(map[string]any)["requests"].(map[string]any)

			if elReq["memory"] != c.elMemory || clReq["memory"] != c.clMemory || elReq["cpu"] != c.elCPU {
				t.Errorf("requests: el %v, cl %v", elReq, clReq)
			}
		})
	}
}
