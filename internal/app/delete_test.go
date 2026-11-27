package app

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// nsFake simulates a namespace with PVCs and Secrets (names, types, keys).
type nsFake struct {
	nsExists   bool
	nsErr      error
	pvcs       string // jsonpath output
	secrets    string // go-template output
	secretsErr error
	release    bool
	releases   string // helm list -q output
}

func (f nsFake) handle(tool, args string, _ []string, _ []byte) (string, error) {
	switch {
	case tool == "kubectl" && strings.HasPrefix(args, "get namespace"):
		if f.nsErr != nil {
			return "", f.nsErr
		}

		if !f.nsExists {
			return "", errors.New(`Error from server (NotFound): namespaces "x" not found`)
		}

		return "namespace/x", nil
	case tool == "kubectl" && strings.HasPrefix(args, "get pvc"):
		return f.pvcs, nil
	case tool == "kubectl" && strings.HasPrefix(args, "get secrets"):
		return f.secrets, f.secretsErr
	case tool == "kubectl" && strings.HasPrefix(args, "delete namespace"):
		return "", nil
	case tool == "helm" && strings.HasPrefix(args, "history"):
		if !f.release {
			return "", errors.New("Error: release: not found")
		}

		return `[{"revision":3,"status":"deployed","chart":"dv-pod-0.23.0"}]`, nil
	case tool == "helm" && strings.HasPrefix(args, "uninstall"):
		return "", nil
	case tool == "helm" && strings.HasPrefix(args, "list -n"):
		return f.releases, nil
	}

	return "", errors.New("unexpected call: " + tool + " " + args)
}

const dvPodSecrets = "dvtee-a-dv-pod-enr-key\tOpaque\tcharon-enr-private-key,enr,\n" +
	"obol-monitoring\tOpaque\ttoken,\n" +
	"sh.helm.release.v1.dvtee-a.v3\thelm.sh/release.v1\trelease,\n"

func TestDelete_KeepsDataByDefault(t *testing.T) {
	env := newTestEnv(t, true)
	dir := env.writeDeployment(t, "dvtee", "a", map[string]string{
		"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"),
		"values.yaml":   "a: 1\n",
	})
	calls := useFakeTools(t, nsFake{
		nsExists: true, pvcs: "charon-data-dvtee-a-0\nvalidator-data-dvtee-a-0\n",
		secrets: dvPodSecrets, release: true,
	}.handle)

	if err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{Force: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if hasCall(*calls, "kubectl", "delete namespace") {
		t.Fatal("namespace with key shares deleted without --delete-data")
	}

	if !hasCall(*calls, "helm", "uninstall dvtee-a -n dvtee-a") {
		t.Errorf("release (from the hand-edited helmfile) not uninstalled: %+v", *calls)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Error("config directory removed in keep-data mode")
	}

	out := env.stdout.String()
	for _, want := range []string{"charon-data-dvtee-a-0", "dvtee-a-dv-pod-enr-key", "--delete-data"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "obol-monitoring (looks like key material)") || strings.Contains(out, "sh.helm.release") {
		t.Errorf("non-key Secrets listed as key material:\n%s", out)
	}
}

func TestDelete_DeleteDataNeedsYesWithoutTTY(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"), "values.yaml": "a: 1\n"})
	calls := useFakeTools(t, nsFake{nsExists: true, pvcs: "charon-data-dvtee-a-0\n", secrets: dvPodSecrets, release: true}.handle)

	err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{DeleteData: true, Force: true})
	if err == nil || !strings.Contains(err.Error(), "--delete-data --yes") {
		t.Fatalf("expected --yes requirement, got %v", err)
	}

	if hasCall(*calls, "kubectl", "delete namespace") {
		t.Fatal("namespace deleted without confirmation")
	}

	if err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{DeleteData: true, Yes: true}); err != nil {
		t.Fatalf("Delete --delete-data --yes: %v", err)
	}

	if !hasCall(*calls, "kubectl", "delete namespace dvtee-a") {
		t.Error("namespace not deleted with --delete-data --yes")
	}

	if _, err := os.Stat(deploymentDirFor(env.cfg, "dvtee", "a")); err == nil {
		t.Error("config directory kept after --delete-data")
	}
}

func TestDelete_DeleteDataTTYRequiresTypedName(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{"helmfile.yaml": legacyHelmfile("dvtee-a", "0.23.0"), "values.yaml": "a: 1\n"})
	calls := useFakeTools(t, nsFake{nsExists: true, pvcs: "charon-data-dvtee-a-0\n", release: true}.handle)

	var out bytes.Buffer

	u := ui.NewForTestTTY(&out, &out)

	withStdin(t, "dvtee\n")

	err := Delete(env.cfg, u, "dvtee/a", DeleteOptions{DeleteData: true})
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("expected mismatch refusal, got %v", err)
	}

	if hasCall(*calls, "kubectl", "delete namespace") {
		t.Fatal("namespace deleted after a wrong confirmation")
	}

	withStdin(t, "dvtee/a\n")

	if err := Delete(env.cfg, u, "dvtee/a", DeleteOptions{DeleteData: true}); err != nil {
		t.Fatalf("Delete with typed name: %v", err)
	}

	if !hasCall(*calls, "kubectl", "delete namespace dvtee-a") {
		t.Error("namespace not deleted after typing the name")
	}
}

func TestDelete_NoDataDeletesNamespace(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "redis", "x", map[string]string{"values.yaml": "a: 1\n"})
	calls := useFakeTools(t, nsFake{nsExists: true, secrets: "sh.helm.release.v1.redis.v1\thelm.sh/release.v1\trelease,\n"}.handle)

	if err := Delete(env.cfg, env.u, "redis/x", DeleteOptions{Force: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !hasCall(*calls, "kubectl", "delete namespace redis-x") {
		t.Errorf("namespace not deleted: %+v", *calls)
	}
}

func TestDelete_FailsClosed(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{"values.yaml": "a: 1\n"})

	// Secrets can't be listed (RBAC): treated as data.
	calls := useFakeTools(t, nsFake{nsExists: true, secretsErr: errors.New("forbidden")}.handle)

	if err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{Force: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if hasCall(*calls, "kubectl", "delete namespace") {
		t.Fatal("namespace deleted although its Secrets could not be checked")
	}

	// Cluster unreachable: refuse instead of treating the namespace as gone.
	useFakeTools(t, nsFake{nsErr: errors.New("connection refused")}.handle)

	err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "cannot check namespace") {
		t.Fatalf("expected fail-closed error, got %v", err)
	}

	if _, statErr := os.Stat(deploymentDirFor(env.cfg, "dvtee", "a")); statErr != nil {
		t.Error("config dir removed while the cluster was unreachable")
	}
}

func TestDelete_FlagsExclusive(t *testing.T) {
	env := newTestEnv(t, false)
	if err := Delete(env.cfg, env.u, "a/b", DeleteOptions{DeleteData: true, KeepData: true}); err == nil {
		t.Error("--delete-data with --keep-data accepted")
	}
}

func TestLooksLikeKeyMaterial(t *testing.T) {
	yes := []secretMeta{
		{Name: "dvtee-a-dv-pod-enr-key", Type: "Opaque"},
		{Name: "dvtee-a-enr-provided-key", Type: "Opaque"},
		{Name: "charon-enr-private-key", Type: "Opaque"},
		{Name: "validator-keys", Type: "Opaque", Keys: []string{"keystore-0.json"}},
		{Name: "anything", Type: "Opaque", Keys: []string{"charon-enr-private-key"}},
		{Name: "remote-signer-keystore", Type: "Opaque"},
	}
	no := []secretMeta{
		{Name: "obol-monitoring", Type: "Opaque", Keys: []string{"token"}},
		{Name: "henry-config", Type: "Opaque", Keys: []string{"config"}},
		{Name: "sh.helm.release.v1.validator.v1", Type: "helm.sh/release.v1"},
	}

	for _, s := range yes {
		if !looksLikeKeyMaterial(s.Name, s.Type, s.Keys) {
			t.Errorf("%s not flagged", s.Name)
		}
	}

	for _, s := range no {
		if looksLikeKeyMaterial(s.Name, s.Type, s.Keys) {
			t.Errorf("%s flagged", s.Name)
		}
	}
}

// withStdin replaces os.Stdin with a pipe holding input for the test.
func withStdin(t *testing.T, input string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}

	w.Close()

	orig := os.Stdin
	os.Stdin = r

	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
	})
}

func TestDelete_KeepDataWithoutNamespaceKeepsConfig(t *testing.T) {
	env := newTestEnv(t, true)
	env.writeDeployment(t, "dvtee", "a", map[string]string{"values.yaml": "a: 1\n"})
	useFakeTools(t, nsFake{nsExists: false}.handle)

	if err := Delete(env.cfg, env.u, "dvtee/a", DeleteOptions{KeepData: true, Force: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(deploymentDirFor(env.cfg, "dvtee", "a")); err != nil {
		t.Error("--keep-data removed the config directory")
	}
}

func TestDelete_SharedNamespaceNeverDeleted(t *testing.T) {
	env := newTestEnv(t, true)
	dir := env.writeDeployment(t, "dv-pod", "legacy", map[string]string{"values.yaml": "a: 1\n"})

	if err := saveAppMeta(dir, &AppMeta{Release: "my-dv-pod", Namespace: "dv-pod", ValuesMode: ValuesModeOverrides, SyncedRevision: 2}); err != nil {
		t.Fatal(err)
	}

	calls := useFakeTools(t, nsFake{nsExists: true, release: true, releases: "my-dv-pod\nother-dv\n"}.handle)

	err := Delete(env.cfg, env.u, "dv-pod/legacy", DeleteOptions{DeleteData: true, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "other-dv") {
		t.Fatalf("expected refusal naming the other release, got %v", err)
	}

	if err := Delete(env.cfg, env.u, "dv-pod/legacy", DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}

	if hasCall(*calls, "kubectl", "delete namespace") {
		t.Fatal("shared namespace deleted")
	}

	if !hasCall(*calls, "helm", "uninstall my-dv-pod -n dv-pod") {
		t.Errorf("own release not uninstalled from the custom namespace: %+v", *calls)
	}
}
