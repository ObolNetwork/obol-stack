package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestApplyChartCRDs(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	applied := filepath.Join(dir, "applied.yaml")

	t.Setenv("OBOL_HELMFILE", writeScript(t, dir, "helmfile", `echo "helmfile $*" >> `+log+`
cat <<'EOF'
[{"name":"traefik","enabled":true,"chart":"traefik/traefik","version":"39.0.9"},
 {"name":"reloader","enabled":true,"chart":"stakater/reloader","version":"2.2.18"},
 {"name":"base","enabled":true,"chart":"./base","version":""},
 {"name":"cloudflared","enabled":false,"chart":"./cloudflared","version":""}]
EOF
`))
	t.Setenv("OBOL_HELM", writeScript(t, dir, "helm", `echo "helm $*" >> `+log+`
case "$3" in
  traefik/traefik) printf 'apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n' ;;
esac
`))
	t.Setenv("OBOL_KUBECTL", writeScript(t, dir, "kubectl", `echo "kubectl $* KUBECONFIG=$KUBECONFIG" >> `+log+`
cat >> `+applied+`
`))

	cfg := &config.Config{ConfigDir: dir, BinDir: filepath.Join(dir, "bin")}
	u := ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{})
	helmfile := filepath.Join(dir, "defaults", "helmfile.yaml")

	if err := applyChartCRDs(cfg, u, helmfile, "/kc.yaml", []string{"name=traefik"}); err != nil {
		t.Fatal(err)
	}

	calls, _ := os.ReadFile(log)
	got := string(calls)

	for _, want := range []string{
		"helmfile --file " + helmfile + " --kubeconfig /kc.yaml --selector name=traefik list --output json",
		"helm show crds traefik/traefik --version 39.0.9",
		"helm show crds stakater/reloader --version 2.2.18",
		"helm show crds " + filepath.Join(dir, "defaults", "base"),
		"kubectl apply --server-side --force-conflicts --field-manager=obol-upgrade -f - KUBECONFIG=/kc.yaml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in:\n%s", want, got)
		}
	}

	if strings.Contains(got, "cloudflared") {
		t.Errorf("disabled release was processed:\n%s", got)
	}

	// Only traefik ships CRDs, so kubectl runs exactly once.
	if n := strings.Count(got, "kubectl apply"); n != 1 {
		t.Errorf("kubectl apply ran %d times, want 1", n)
	}

	if b, _ := os.ReadFile(applied); !strings.Contains(string(b), "kind: CustomResourceDefinition") {
		t.Errorf("CRDs not piped to kubectl, got %q", b)
	}
}

func TestApplyChartCRDsHelmFailureStopsUpgrade(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("OBOL_HELMFILE", writeScript(t, dir, "helmfile",
		`echo '[{"name":"traefik","enabled":true,"chart":"traefik/traefik","version":"39.0.9"}]'`))
	t.Setenv("OBOL_HELM", writeScript(t, dir, "helm", `echo boom >&2; exit 1`))
	t.Setenv("OBOL_KUBECTL", writeScript(t, dir, "kubectl", `exit 0`))

	cfg := &config.Config{ConfigDir: dir, BinDir: filepath.Join(dir, "bin")}
	u := ui.NewForTest(&bytes.Buffer{}, &bytes.Buffer{})

	err := applyChartCRDs(cfg, u, filepath.Join(dir, "helmfile.yaml"), "/kc.yaml", nil)
	if err == nil || !strings.Contains(err.Error(), "helm show crds traefik/traefik") {
		t.Fatalf("want helm show crds error, got %v", err)
	}
}
