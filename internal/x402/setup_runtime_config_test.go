package x402

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeRuntimeConfigMapData_LiteLLMPreservesUserModels(t *testing.T) {
	current := map[string]string{"config.yaml": `
model_list:
  - model_name: paid/*
    litellm_params:
      model: openai/*
      api_base: http://127.0.0.1:8402/v1
      api_key: unused
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
`}
	previous := map[string]string{"config.yaml": `
model_list:
  - model_name: paid/qwen36
    litellm_params:
      model: openai/qwen36-apex-i-compact
      api_base: http://silvermesh.v1337.lan:8081/v1
      api_key: unused
litellm_settings:
  drop_params: true
`}

	merged, err := mergeRuntimeConfigMapData("litellm-config", current, previous)
	if err != nil {
		t.Fatalf("mergeRuntimeConfigMapData: %v", err)
	}

	var parsed struct {
		ModelList []struct {
			ModelName string `yaml:"model_name"`
		} `yaml:"model_list"`
		GeneralSettings map[string]any `yaml:"general_settings"`
		LiteLLMSettings map[string]any `yaml:"litellm_settings"`
	}
	if err := yaml.Unmarshal([]byte(merged["config.yaml"]), &parsed); err != nil {
		t.Fatalf("parse merged yaml: %v\n%s", err, merged["config.yaml"])
	}

	got := map[string]bool{}
	for _, entry := range parsed.ModelList {
		got[entry.ModelName] = true
	}
	for _, want := range []string{"paid/*", "paid/qwen36"} {
		if !got[want] {
			t.Fatalf("merged config missing model %q:\n%s", want, merged["config.yaml"])
		}
	}
	if parsed.GeneralSettings["master_key"] == nil {
		t.Fatalf("current general_settings should be preserved:\n%s", merged["config.yaml"])
	}
	if parsed.LiteLLMSettings["drop_params"] == nil {
		t.Fatalf("previous litellm_settings should be restored when current is empty:\n%s", merged["config.yaml"])
	}
}

func TestMergeRuntimeConfigMapData_BuyerConfigPreservesRuntimeKeys(t *testing.T) {
	current := map[string]string{"new.json": `{"new":true}`}
	previous := map[string]string{
		"alice.json": `{"auths":["a"]}`,
		"new.json":   `{"old":true}`,
	}

	merged, err := mergeRuntimeConfigMapData("x402-buyer-auths", current, previous)
	if err != nil {
		t.Fatalf("mergeRuntimeConfigMapData: %v", err)
	}
	if merged["alice.json"] != previous["alice.json"] {
		t.Fatalf("runtime key was not preserved: %#v", merged)
	}
	if merged["new.json"] != current["new.json"] {
		t.Fatalf("current key should win on conflicts: %#v", merged)
	}
}

func TestConfigMapDataManifest_RendersConfigMap(t *testing.T) {
	manifest, err := configMapDataManifest("llm", "x402-buyer-config", map[string]string{
		"demo.json": `{"endpoint":"http://example"}`,
	})
	if err != nil {
		t.Fatalf("configMapDataManifest: %v", err)
	}

	var parsed struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   map[string]string `yaml:"metadata"`
		Data       map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(manifest, &parsed); err != nil {
		t.Fatalf("manifest is not yaml: %v\n%s", err, manifest)
	}
	if parsed.Kind != "ConfigMap" || parsed.Metadata["namespace"] != "llm" || parsed.Data["demo.json"] == "" {
		t.Fatalf("unexpected manifest: %#v\n%s", parsed, manifest)
	}
}
