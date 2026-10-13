package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// Sync must render the embedded network files, keeping only values.yaml from
// the deployment: stale and hand-edited copies are replaced, leftovers removed.
func TestRefreshDeploymentFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ethereum", "my-node")
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}

	values := []byte("network: hoodi\n")

	files := map[string][]byte{
		"values.yaml":                  values,
		"helmfile.yaml.gotmpl":         []byte("stale pin\n"),
		"templates/removed-later.yaml": []byte("gone\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	refreshed, err := refreshDeploymentFiles("ethereum", dir)
	if err != nil || !refreshed {
		t.Fatalf("refreshDeploymentFiles = (%v, %v); want (true, nil)", refreshed, err)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, "values.yaml")); string(got) != string(values) {
		t.Fatalf("values.yaml changed: %q", got)
	}

	want, _ := embed.ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")
	if got, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml.gotmpl")); string(got) != string(want) {
		t.Fatal("helmfile.yaml.gotmpl not refreshed from the embedded copy")
	}

	for _, gone := range []string{"templates/removed-later.yaml", "values.yaml.gotmpl"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should not exist after refresh", gone)
		}
	}

	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), ".my-node-refresh-*")); len(leftovers) > 0 {
		t.Fatalf("staging dir left behind: %v", leftovers)
	}
}

func TestRefreshDeploymentFiles_UnknownNetworkLeavesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helmfile.yaml.gotmpl"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	refreshed, err := refreshDeploymentFiles("no-such-network", dir)
	if err != nil || refreshed {
		t.Fatalf("refreshDeploymentFiles = (%v, %v); want (false, nil)", refreshed, err)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, "helmfile.yaml.gotmpl")); string(got) != "keep\n" {
		t.Fatal("files of an unknown network must be left alone")
	}
}
