package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKubectl serves config as the ConfigMap's config.yaml and records
// every patch call's arguments in the returned file.
func fakeKubectl(t *testing.T, config string) (bin, patches string) {
	t.Helper()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	patches = filepath.Join(dir, "patches")
	bin = filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"get) cat '" + cfgFile + "' ;;\n" +
		"patch) echo \"$@\" >> '" + patches + "' ;;\n" +
		"*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return bin, patches
}

func TestEnsureObservabilityInConfigMap_OldStackGainsSettings(t *testing.T) {
	// A v0.15 config: no callbacks, no metrics settings.
	bin, patches := fakeKubectl(t, `model_list:
  - model_name: qwen3:8b
    litellm_params:
      model: ollama_chat/qwen3:8b
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
litellm_settings:
  cache: false
  drop_params: true
`)

	changed, err := ensureObservabilityInConfigMap(bin, "kubeconfig")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want a write", changed, err)
	}

	data, err := os.ReadFile(patches)
	if err != nil {
		t.Fatalf("no patch written: %v", err)
	}

	for _, want := range []string{"require_auth_for_metrics_endpoint: false", "- prometheus", "qwen3:8b", "--field-manager=helm"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("patch missing %q:\n%s", want, data)
		}
	}
}

func TestEnsureObservabilityInConfigMap_NoOpWhenPresent(t *testing.T) {
	bin, patches := fakeKubectl(t, "litellm_settings:\n  callbacks: [prometheus]\n  require_auth_for_metrics_endpoint: false\n"+
		"  prometheus_latency_buckets: [1]\n  prometheus_metrics_config: []\n")

	changed, err := ensureObservabilityInConfigMap(bin, "kubeconfig")
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v, want no write", changed, err)
	}

	if _, err := os.Stat(patches); !os.IsNotExist(err) {
		t.Fatal("patched a config that already had the settings")
	}
}
