package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var platforms = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}

func TestEmbeddedManifestValid(t *testing.T) {
	m, err := ParseManifest(manifestYAML)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"kubectl", "helm", "k3d", "helmfile", "k9s", "helm-diff"}
	if got := m.Names(); len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}

	for _, name := range want {
		tool, ok := m.Get(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}

		for _, p := range platforms {
			goos, goarch, _ := strings.Cut(p, "/")

			a, err := tool.AssetFor(goos, goarch)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}

			if regexp.MustCompile(`\{[a-z]+\}`).MatchString(a.URL) {
				t.Errorf("%s %s: unexpanded placeholder in %s", name, p, a.URL)
			}
		}
	}

	// helm must be installed before the helm-diff plugin.
	idx := map[string]int{}
	for i, n := range m.Names() {
		idx[n] = i
	}

	if idx["helm"] > idx["helm-diff"] {
		t.Error("helm must precede helm-diff in manifest order")
	}
}

func TestParseManifestRejectsBadEntries(t *testing.T) {
	bad := map[string]string{
		"short sha": `tools: [{name: x, version: "1.0.0", assets: {linux/amd64: {url: u, sha256: abc}}}]`,
		"no member": `tools: [{name: x, version: "1.0.0", format: tar.gz, assets: {linux/amd64: {url: u, sha256: ` + sha64 + `}}}]`,
		"dup":       `tools: [{name: x, version: "1"}, {name: x, version: "1"}]`,
		"format":    `tools: [{name: x, version: "1", format: rar}]`,
	}

	for name, y := range bad {
		if _, err := ParseManifest([]byte(y)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

const sha64 = "0000000000000000000000000000000000000000000000000000000000000000"

// TestManifestMatchesObolup keeps manifest.yaml and the obolup.sh pins in
// lockstep (same spirit as TestOpenClawVersionConsistency). If this fails,
// bump the manifest version AND all four sha256 values in the same commit.
func TestManifestMatchesObolup(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "obolup.sh"))
	if err != nil {
		t.Fatalf("read obolup.sh: %v", err)
	}

	for _, tool := range Default().Tools {
		if tool.ObolupVar == "" {
			t.Errorf("%s: obolupVar not set", tool.Name)
			continue
		}

		re := regexp.MustCompile(`(?m)^readonly ` + regexp.QuoteMeta(tool.ObolupVar) + `="v?([^"]+)"`)

		m := re.FindSubmatch(raw)
		if m == nil {
			t.Errorf("%s: %s not found in obolup.sh", tool.Name, tool.ObolupVar)
			continue
		}

		if got := string(m[1]); got != tool.Version {
			t.Errorf("%s: manifest.yaml version %s != obolup.sh %s=%s", tool.Name, tool.Version, tool.ObolupVar, got)
		}
	}
}
