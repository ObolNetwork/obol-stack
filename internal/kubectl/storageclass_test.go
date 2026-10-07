package kubectl

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// stubKubectl installs a shell-script kubectl via OBOL_KUBECTL that logs its
// argv + KUBECONFIG, prints getOut for `get`, and exits getExit for `get`.
func stubKubectl(t *testing.T, getOut string, getExit int) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	out := filepath.Join(dir, "get.out")
	if err := os.WriteFile(out, []byte(getOut), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		`echo "$* KUBECONFIG=$KUBECONFIG" >> '` + log + "'\n" +
		`if [ "$1" = get ]; then cat '` + out + "'; " +
		`if [ ` + strconv.Itoa(getExit) + ` -ne 0 ]; then echo 'Error from server (NotFound): storageclasses.storage.k8s.io "local-path" not found' >&2; fi; ` +
		`exit ` + strconv.Itoa(getExit) + "; fi\n"
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBOL_KUBECTL", bin)
	return &config.Config{ConfigDir: dir, BinDir: filepath.Join(dir, "bin")}, log
}

func readCalls(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateLocalPathStorageClass(t *testing.T) {
	const stale = `{"kind":"StorageClass","metadata":{"name":"local-path"},"parameters":{"pathPattern":"{{ .PVC.Namespace }}/{{ .PVC.Name }}"}}`
	const current = `{"kind":"StorageClass","metadata":{"name":"local-path"},"parameters":{"pathPattern":"x","allowUnsafePathPattern":"true"}}`

	tests := []struct {
		name        string
		getOut      string
		getExit     int
		wantDeleted bool
		wantErr     bool
	}{
		{name: "stale class is deleted", getOut: stale, wantDeleted: true},
		{name: "class without parameters is deleted", getOut: `{"kind":"StorageClass"}`, wantDeleted: true},
		{name: "current class is kept", getOut: current},
		{name: "missing class (ignore-not-found) is a no-op", getOut: ""},
		{name: "NotFound error is a no-op", getExit: 1},
		{name: "unparseable output errors", getOut: "not json", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, log := stubKubectl(t, tc.getOut, tc.getExit)

			deleted, err := MigrateLocalPathStorageClass(cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if deleted != tc.wantDeleted {
				t.Fatalf("deleted = %v, want %v", deleted, tc.wantDeleted)
			}

			calls := readCalls(t, log)
			kc := "KUBECONFIG=" + filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
			if !strings.Contains(calls, "get storageclass local-path -o json --ignore-not-found "+kc) {
				t.Fatalf("missing get call with stack kubeconfig:\n%s", calls)
			}
			gotDelete := strings.Contains(calls, "delete storageclass local-path --ignore-not-found "+kc)
			if gotDelete != tc.wantDeleted {
				t.Fatalf("delete called = %v, want %v:\n%s", gotDelete, tc.wantDeleted, calls)
			}
		})
	}
}

func TestMigrateLocalPathStorageClass_GetFailureIsError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Unable to connect to the server' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBOL_KUBECTL", bin)
	cfg := &config.Config{ConfigDir: dir, BinDir: filepath.Join(dir, "bin")}

	deleted, err := MigrateLocalPathStorageClass(cfg)
	if err == nil || deleted {
		t.Fatalf("want error and no delete, got deleted=%v err=%v", deleted, err)
	}
}
