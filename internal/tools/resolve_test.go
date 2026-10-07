package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeTool writes an executable shell script named name into dir that
// prints out for any arguments.
func writeTool(t *testing.T, dir, name, out string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(dir, name)
	// Builtins only: tests point $PATH at temp dirs.
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\n", out)

	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func kubectlOut(v string) string { return `{"clientVersion": {"gitVersion": "v` + v + `"}}` }

func helmOut(v string) string { return "v" + v + "+gabc123" }

func pin(t *testing.T, name string) string {
	t.Helper()

	tool, ok := Default().Get(name)
	if !ok {
		t.Fatalf("%s not in manifest", name)
	}

	return tool.Version
}

// bump returns the pin with its minor shifted by d (patch reset to 0).
func bump(t *testing.T, v string, d int) string {
	t.Helper()

	sv, ok := semver(v)
	if !ok {
		t.Fatalf("bad version %q", v)
	}

	return fmt.Sprintf("%d.%d.0", sv[0], sv[1]+d)
}

func TestResolveOrder(t *testing.T) {
	binDir := t.TempDir()
	pathDir := t.TempDir()
	envDir := t.TempDir()

	t.Setenv("PATH", pathDir)
	t.Setenv("OBOL_KUBECTL", "")

	// Nothing anywhere → missing, path points at the managed location.
	r := Resolve(binDir, "kubectl")
	if r.Source != SourceMissing || r.Path != filepath.Join(binDir, "kubectl") {
		t.Fatalf("empty: got %+v", r)
	}

	// Compatible PATH copy is used.
	pathKubectl := writeTool(t, pathDir, "kubectl", kubectlOut(pin(t, "kubectl")))
	if r = Resolve(binDir, "kubectl"); r.Source != SourcePath || r.Path != pathKubectl {
		t.Fatalf("path: got %+v", r)
	}

	// Managed copy beats PATH.
	managed := writeTool(t, binDir, "kubectl", kubectlOut("0.0.1"))
	if r = Resolve(binDir, "kubectl"); r.Source != SourceManaged || r.Path != managed {
		t.Fatalf("managed: got %+v", r)
	}

	// Env override beats everything, no version check.
	envKubectl := writeTool(t, envDir, "my-kubectl", kubectlOut("0.0.1"))
	t.Setenv("OBOL_KUBECTL", envKubectl)

	if r = Resolve(binDir, "kubectl"); r.Source != SourceEnv || r.Path != envKubectl {
		t.Fatalf("env: got %+v", r)
	}

	// Config-style shim returns the same thing.
	if got := Path(binDir, "kubectl"); got != envKubectl {
		t.Fatalf("Path() = %q, want %q", got, envKubectl)
	}
}

func TestResolveEnvBareNameUsesLookPath(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)

	want := writeTool(t, pathDir, "helm4", helmOut("4.0.0"))
	t.Setenv("OBOL_HELM", "helm4")

	if r := Resolve(t.TempDir(), "helm"); r.Source != SourceEnv || r.Path != want {
		t.Fatalf("got %+v, want env %s", r, want)
	}
}

func TestResolveRejectsIncompatiblePath(t *testing.T) {
	binDir := t.TempDir()
	oldDir := t.TempDir()
	goodDir := t.TempDir()

	t.Setenv("OBOL_HELM", "")
	old := writeTool(t, oldDir, "helm", helmOut(bump(t, pin(t, "helm"), -1)))

	t.Setenv("PATH", oldDir)

	r := Resolve(binDir, "helm")
	if r.Found() || r.Rejected != old {
		t.Fatalf("old helm should be rejected: %+v", r)
	}

	// A later compatible PATH entry is picked over the earlier incompatible one.
	good := writeTool(t, goodDir, "helm", helmOut(bump(t, pin(t, "helm"), 2)))
	t.Setenv("PATH", oldDir+string(os.PathListSeparator)+goodDir)

	if r = Resolve(binDir, "helm"); r.Source != SourcePath || r.Path != good {
		t.Fatalf("want later compatible helm, got %+v", r)
	}
}

func TestResolveSkipsBinDirOnPath(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	t.Setenv("OBOL_K9S", "")

	// Non-executable file in bin dir is not "managed" and must not be
	// picked up again through PATH.
	if err := os.WriteFile(filepath.Join(binDir, "k9s"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := Resolve(binDir, "k9s"); r.Found() {
		t.Fatalf("non-executable bin dir file resolved: %+v", r)
	}
}

func TestResolveAnyCompatSkipsProbe(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	t.Setenv("OBOL_K9S", "")

	// Output has no version at all: compat=any must still accept it.
	p := writeTool(t, pathDir, "k9s", "garbage")
	if r := Resolve(t.TempDir(), "k9s"); r.Source != SourcePath || r.Path != p {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveDanglingManagedSymlinkIsMissing(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OBOL_K3D", "")

	if err := os.Symlink(filepath.Join(binDir, "nope"), filepath.Join(binDir, "k3d")); err != nil {
		t.Fatal(err)
	}

	if r := Resolve(binDir, "k3d"); r.Found() {
		t.Fatalf("dangling symlink resolved: %+v", r)
	}
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		policy, want, have string
		ok                 bool
	}{
		{CompatSkew1, "1.36.3", "1.36.0", true},
		{CompatSkew1, "1.36.3", "1.35.9", true},
		{CompatSkew1, "1.36.3", "1.37.1", true},
		{CompatSkew1, "1.36.3", "1.34.0", false},
		{CompatSkew1, "1.36.3", "1.38.0", false},
		{CompatSkew1, "1.36.3", "2.36.0", false},
		{CompatMinor, "3.21.3", "3.21.0", true},
		{CompatMinor, "3.21.3", "3.22.0", true},
		{CompatMinor, "3.21.3", "3.20.9", false},
		{CompatMinor, "3.21.3", "4.0.0", false},
		{CompatMinor, "5.9.0", "v5.9.1", true},
		{CompatMinor, "5.9.0", "garbage", false},
		{CompatAny, "0.51.0", "0.1.0", true},
		{"bogus", "1.0.0", "1.0.0", false},
	}

	for _, c := range cases {
		if got := Compatible(c.policy, c.want, c.have); got != c.ok {
			t.Errorf("Compatible(%s, %s, %s) = %v, want %v", c.policy, c.want, c.have, got, c.ok)
		}
	}
}

func TestParseVersionFromRealOutputs(t *testing.T) {
	m := Default()
	cases := map[string]string{
		"kubectl":  "{\n  \"clientVersion\": {\n    \"major\": \"1\",\n    \"gitVersion\": \"v1.36.1\",\n  },\n  \"kustomizeVersion\": \"v5.7.1\"\n}",
		"helm":     "v3.21.0+ge0878d4\n",
		"k3d":      "k3d version v5.9.0\nk3s version v1.33.6-k3s1 (default)\n",
		"helmfile": "helmfile version 1.5.3\n",
		"k9s":      "Version              v0.51.0\nCommit               558caaf\n",
	}
	want := map[string]string{"kubectl": "1.36.1", "helm": "3.21.0", "k3d": "5.9.0", "helmfile": "1.5.3", "k9s": "0.51.0"}

	for name, out := range cases {
		tool, _ := m.Get(name)

		got, err := ParseVersion(tool.VersionRegex, out)
		if err != nil || got != want[name] {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, want[name])
		}
	}
}

// A Helm 3 on $PATH must not satisfy the Helm 4 pin: obol then installs its
// managed Helm 4 and the status note says why the PATH copy was skipped.
func TestCompatible_Helm3RejectedForHelm4Pin(t *testing.T) {
	if Compatible(CompatMinor, "4.3.0", "3.22.0") {
		t.Fatal("helm 3.22.0 accepted for a 4.3.0 pin")
	}
	if !Compatible(CompatMinor, "4.3.0", "4.4.1") {
		t.Fatal("helm 4.4.1 rejected for a 4.3.0 pin")
	}
	if got, want := CompatRange(CompatMinor, "4.3.0"), "v4.x >= 4.3"; got != want {
		t.Fatalf("CompatRange(minor) = %q, want %q", got, want)
	}
	if got := CompatRange(CompatAny, "4.3.0"); got != "" {
		t.Fatalf("CompatRange(any) = %q, want empty", got)
	}
}
