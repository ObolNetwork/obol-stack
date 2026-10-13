package model_test

import (
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"gopkg.in/yaml.v3"
)

// roundTrip marshals and re-parses cfg the way every ConfigMap writer does
// (CLI model commands, stack-up restore, serviceoffer-controller).
func roundTrip(t *testing.T, cfg model.LiteLLMConfig) model.LiteLLMConfig {
	t.Helper()

	raw, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out model.LiteLLMConfig
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}

	return out
}

// normalized returns the Go settings in their parsed-YAML shape (ints vs
// floats, []any) so they compare with settings read from a config file.
func normalized(t *testing.T) map[string]any {
	t.Helper()
	return roundTrip(t, model.LiteLLMConfig{LiteLLMSettings: model.LiteLLMObservabilitySettings()}).LiteLLMSettings
}

func TestEnsureLiteLLMObservability_AddsSettingsToOldConfig(t *testing.T) {
	cfg := model.LiteLLMConfig{LiteLLMSettings: map[string]any{"cache": false, "drop_params": true}}

	if !model.EnsureLiteLLMObservability(&cfg) {
		t.Fatal("expected a change on a config without Prometheus settings")
	}

	if model.EnsureLiteLLMObservability(&cfg) {
		t.Fatal("second call must be a no-op")
	}

	got := roundTrip(t, cfg).LiteLLMSettings
	if got["cache"] != false || got["drop_params"] != true {
		t.Fatalf("existing settings lost: %#v", got)
	}

	for key, want := range normalized(t) {
		if !reflect.DeepEqual(got[key], want) {
			t.Fatalf("%s = %#v, want %#v", key, got[key], want)
		}
	}
}

func TestEnsureLiteLLMObservability_NilSettings(t *testing.T) {
	var cfg model.LiteLLMConfig
	if !model.EnsureLiteLLMObservability(&cfg) {
		t.Fatal("expected a change")
	}

	if !reflect.DeepEqual(roundTrip(t, cfg).LiteLLMSettings, normalized(t)) {
		t.Fatalf("got %#v", cfg.LiteLLMSettings)
	}
}

func TestEnsureLiteLLMObservability_KeepsOperatorValues(t *testing.T) {
	custom := []any{map[string]any{"group": "mine", "metrics": []any{"litellm_spend_metric"}}}
	cfg := model.LiteLLMConfig{LiteLLMSettings: map[string]any{
		"callbacks":                         []any{"langfuse"},
		"require_auth_for_metrics_endpoint": true,
		"prometheus_metrics_config":         custom,
	}}

	model.EnsureLiteLLMObservability(&cfg)
	s := cfg.LiteLLMSettings

	if !reflect.DeepEqual(s["callbacks"], []any{"langfuse", "prometheus"}) {
		t.Fatalf("callbacks = %#v, want prometheus appended", s["callbacks"])
	}

	if s["require_auth_for_metrics_endpoint"] != true {
		t.Fatal("operator's require_auth_for_metrics_endpoint overridden")
	}

	if !reflect.DeepEqual(s["prometheus_metrics_config"], custom) {
		t.Fatal("operator's prometheus_metrics_config overridden")
	}

	if _, ok := s["prometheus_latency_buckets"]; !ok {
		t.Fatal("missing keys must still be filled in")
	}
}

// A second prometheus callback registers the same metric names twice and
// fails, so an existing one (in any accepted shape) must not be duplicated.
func TestEnsureLiteLLMObservability_NoDuplicateCallback(t *testing.T) {
	cfg := model.LiteLLMConfig{LiteLLMSettings: map[string]any{"callbacks": "prometheus"}}
	model.EnsureLiteLLMObservability(&cfg)

	if cfg.LiteLLMSettings["callbacks"] != "prometheus" {
		t.Fatalf("callbacks rewritten: %#v", cfg.LiteLLMSettings["callbacks"])
	}

	cfg = model.LiteLLMConfig{LiteLLMSettings: map[string]any{"success_callback": []any{"prometheus"}}}
	model.EnsureLiteLLMObservability(&cfg)

	if cb, ok := cfg.LiteLLMSettings["callbacks"]; ok {
		t.Fatalf("prometheus added to callbacks although success_callback has it: %#v", cb)
	}
}

// The settings must survive every writer's parse → marshal cycle, which is
// all the CLI, stack-up restore and the controller do to litellm_settings.
func TestLiteLLMObservabilitySettingsSurviveRoundTrip(t *testing.T) {
	cfg := model.LiteLLMConfig{
		ModelList: []model.ModelEntry{{ModelName: "m", LiteLLMParams: model.LiteLLMParams{Model: "openai/m"}}},
	}
	model.EnsureLiteLLMObservability(&cfg)
	first := roundTrip(t, cfg)

	second := roundTrip(t, first)
	if !reflect.DeepEqual(first.LiteLLMSettings, second.LiteLLMSettings) {
		t.Fatalf("settings changed across round trips:\n%#v\n%#v", first.LiteLLMSettings, second.LiteLLMSettings)
	}

	if model.EnsureLiteLLMObservability(&second) {
		t.Fatal("round-tripped settings no longer recognised as complete")
	}
}

// Fresh installs get the settings from llm.yaml; upgrades get them from
// EnsureLiteLLMObservability. Both must agree.
func TestLLMTemplateObservabilitySettingsMatchGo(t *testing.T) {
	data, err := embed.ReadInfrastructureFile("base/templates/llm.yaml")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?s)  config.yaml: \|\n(.*?)\{\{- end \}\}`).FindSubmatch(data)
	if m == nil {
		t.Fatal("default config.yaml block not found in llm.yaml")
	}

	var cfg model.LiteLLMConfig
	if err := yaml.Unmarshal(m[1], &cfg); err != nil {
		t.Fatalf("parse default config: %v\n%s", err, m[1])
	}

	for key, want := range normalized(t) {
		if !reflect.DeepEqual(cfg.LiteLLMSettings[key], want) {
			t.Fatalf("llm.yaml litellm_settings.%s = %#v\nGo LiteLLMObservabilitySettings: %#v", key, cfg.LiteLLMSettings[key], want)
		}
	}

	if model.EnsureLiteLLMObservability(&cfg) {
		t.Fatal("EnsureLiteLLMObservability changes the chart default config")
	}
}

// Per-key / per-user labels are unbounded (or identify callers) and must
// never be re-enabled through include_labels.
func TestLiteLLMObservabilityDropsCallerLabels(t *testing.T) {
	forbidden := []string{"hashed_api_key", "api_key_alias", "team", "team_alias", "end_user", "user", "user_email", "client_ip", "user_agent"}

	groups, _ := model.LiteLLMObservabilitySettings()["prometheus_metrics_config"].([]any)
	if len(groups) == 0 {
		t.Fatal("no prometheus_metrics_config groups")
	}

	for _, g := range groups {
		group := g.(map[string]any)

		labels, ok := group["include_labels"].([]any)
		if !ok {
			// Only litellm_deployment_state may keep its defaults (litellm_model_name, model_id, api_base, api_provider).
			if !reflect.DeepEqual(group["metrics"], []any{"litellm_deployment_state"}) {
				t.Fatalf("group %v has no include_labels: it would export LiteLLM's default per-key/user labels", group["group"])
			}

			continue
		}

		for _, l := range labels {
			if slices.Contains(forbidden, l.(string)) {
				t.Fatalf("group %v includes caller label %q", group["group"], l)
			}
		}
	}
}
