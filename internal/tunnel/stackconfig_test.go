package tunnel

import (
	"slices"
	"strings"
	"testing"
)

// The obolVersion-only apply MUST NOT share kubectl's default server-side-apply
// field manager with SyncTunnelConfigMap. Server-side apply prunes fields a
// manager previously owned but no longer sends, so a shared manager would make
// every SyncStackConfigVersion call delete tunnelURL from obol-stack-config.
func TestStackConfigVersionApplyArgs_UsesDedicatedFieldManager(t *testing.T) {
	args := stackConfigVersionApplyArgs("/tmp/kubeconfig.yaml")

	var fieldManager string
	for _, arg := range args {
		if after, ok := strings.CutPrefix(arg, "--field-manager="); ok {
			fieldManager = after
		}
	}

	if fieldManager == "" {
		t.Fatalf("no --field-manager in %v: the apply would inherit kubectl's default manager and prune tunnelURL", args)
	}

	if fieldManager == "kubectl" || fieldManager == "kubectl-client-side-apply" {
		t.Fatalf("--field-manager=%q collides with kubectl's default manager, which owns tunnelURL", fieldManager)
	}

	if fieldManager != stackConfigVersionFieldManager {
		t.Errorf("--field-manager: got %q, want %q", fieldManager, stackConfigVersionFieldManager)
	}
}

func TestStackConfigVersionApplyArgs_ServerSideApply(t *testing.T) {
	args := stackConfigVersionApplyArgs("/tmp/kubeconfig.yaml")

	for _, want := range []string{"apply", "--server-side", "--force-conflicts", "--kubeconfig", "/tmp/kubeconfig.yaml"} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %v", want, args)
		}
	}
}

func TestStackConfigVersionManifest(t *testing.T) {
	m := stackConfigVersionManifest("v1.2.3", "http://obol.stack:8080/")
	if !strings.Contains(m, `obolVersion: "v1.2.3"`) || !strings.Contains(m, `localURL: "http://obol.stack:8080"`) {
		t.Fatalf("manifest missing keys:\n%s", m)
	}
	if strings.Contains(m, "tunnelURL") {
		t.Fatalf("manifest must not carry tunnelURL (owned by another field manager):\n%s", m)
	}
	if strings.Contains(stackConfigVersionManifest("v1", ""), "localURL") {
		t.Fatal("empty localURL should be omitted")
	}
}
