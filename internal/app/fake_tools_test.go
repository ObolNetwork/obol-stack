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

// fakeCall is one recorded helm/kubectl invocation.
type fakeCall struct {
	tool  string // base name: helm, kubectl
	args  string // space-joined argv
	stdin []byte
}

// fakeHandler answers a tool call: stdout and error.
type fakeHandler func(tool, args string, argv []string, stdin []byte) (string, error)

// useFakeTools routes every helm/kubectl call through h for the test and
// returns the recorded calls. Nothing reaches a real cluster.
func useFakeTools(t *testing.T, h fakeHandler) *[]fakeCall {
	t.Helper()

	calls := &[]fakeCall{}
	orig := runTool

	runTool = func(name string, args, env []string, stdin []byte) ([]byte, error) {
		tool := filepath.Base(name)
		joined := strings.Join(args, " ")
		*calls = append(*calls, fakeCall{tool: tool, args: joined, stdin: stdin})

		out, err := h(tool, joined, args, stdin)

		return []byte(out), err
	}

	t.Cleanup(func() { runTool = orig })

	return calls
}

// testEnv is a temp config dir with a stack kubeconfig and stub helm /
// helmfile binaries in the bin dir (so helmfile sync and helm version run
// the stubs, never real tools).
type testEnv struct {
	cfg         *config.Config
	helmfileLog string
	stdout      *bytes.Buffer
	u           *ui.UI
}

func newTestEnv(t *testing.T, withCluster bool) *testEnv {
	t.Helper()

	for _, tool := range []string{"HELM", "HELMFILE", "KUBECTL"} {
		t.Setenv("OBOL_"+tool, "")
	}

	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig-must-not-be-used")

	base := t.TempDir()
	cfg := &config.Config{ConfigDir: filepath.Join(base, "config"), BinDir: filepath.Join(base, "bin")}

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(cfg.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := &testEnv{cfg: cfg, helmfileLog: filepath.Join(base, "helmfile.log"), stdout: &bytes.Buffer{}}

	writeExec(t, filepath.Join(cfg.BinDir, "helm"), "#!/bin/sh\necho v3.21.0+gtest\n")
	writeExec(t, filepath.Join(cfg.BinDir, "kubectl"), "#!/bin/sh\nexit 1\n")
	writeExec(t, filepath.Join(cfg.BinDir, "helmfile"),
		"#!/bin/sh\necho \"$@\" >> '"+env.helmfileLog+"'\nif [ -f '"+env.helmfileLog+".fail' ]; then exit 1; fi\nexit 0\n")

	if withCluster {
		if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), []byte("# fake\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	env.u = ui.NewForTest(env.stdout, env.stdout)

	return env
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) failHelmfile(t *testing.T) {
	t.Helper()

	if err := os.WriteFile(e.helmfileLog+".fail", nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) helmfileRuns() int {
	data, err := os.ReadFile(e.helmfileLog)
	if err != nil {
		return 0
	}

	return strings.Count(string(data), "\n")
}

// writeDeployment creates an app deployment directory with the given
// files (name → content).
func (e *testEnv) writeDeployment(t *testing.T, appName, id string, files map[string]string) string {
	t.Helper()

	dir := deploymentDirFor(e.cfg, appName, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func hasCall(calls []fakeCall, tool, prefix string) bool {
	for _, c := range calls {
		if c.tool == tool && strings.HasPrefix(c.args, prefix) {
			return true
		}
	}

	return false
}

func legacyHelmfile(release, version string) string {
	return `# Installed from: charon/dv-pod@` + version + ` (resolved via ArtifactHub)

repositories:
  - name: charon
    url: https://obolnetwork.github.io/helm-charts

releases:
  - name: ` + release + `
    namespace: dvtee-a
    createNamespace: true
    chart: charon/dv-pod
    version: "` + version + `"
    values:
      - values.yaml
`
}
