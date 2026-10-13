package model

import "slices"

// LiteLLM Prometheus metrics (docs/observability.md "LLM performance").
//
// The settings below turn on LiteLLM's built-in `prometheus` callback, serve
// /metrics without the master key (Prometheus scrapes it in-cluster; LiteLLM
// stays ClusterIP-only) and keep label cardinality bounded.
//
// prometheus_metrics_config does two things: only the listed metrics are
// registered (every other LiteLLM metric becomes a no-op), and include_labels
// trims each metric's label set. LiteLLM rejects the whole config (the
// callback fails to start) if include_labels names a label the metric does not
// define, so every group below uses labels that exist on that metric in both
// the pinned image (upstream v1.87) and v1.105. Per-key / per-user labels
// (hashed_api_key, api_key_alias, team, team_alias, end_user, user,
// user_email, client_ip, user_agent) are dropped everywhere.
//
// The TTFT / latency / token metrics carry no api_base label; dashboards join
// them to litellm_deployment_state on model_id to get the upstream.

const prometheusCallback = "prometheus"

// liteLLMLatencyBuckets replaces LiteLLM's default latency buckets
// (…0.5, 1, 2, 5, 10, 30…) with finer ones in the 0.1–10s range where TTFT
// and short completions live, so p50/p95 interpolation is meaningful.
var liteLLMLatencyBuckets = []float64{
	0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3, 4, 5, 7.5, 10,
	15, 20, 30, 45, 60, 90, 120, 180, 300, 600,
}

type liteLLMMetricGroup struct {
	group   string
	metrics []string
	labels  []string // nil: keep LiteLLM's default labels
}

var liteLLMMetricGroups = []liteLLMMetricGroup{
	{
		group: "obol-latency",
		metrics: []string{
			"litellm_llm_api_time_to_first_token_metric",
			"litellm_llm_api_latency_metric",
			"litellm_request_total_latency_metric",
		},
		labels: []string{"model", "requested_model", "model_id"},
	},
	{
		group: "obol-tokens",
		metrics: []string{
			"litellm_input_tokens_metric",
			"litellm_output_tokens_metric",
		},
		labels: []string{"model", "requested_model", "model_id"},
	},
	{
		group: "obol-deployment-requests",
		metrics: []string{
			"litellm_deployment_total_requests",
			"litellm_deployment_success_responses",
		},
		labels: []string{"requested_model", "litellm_model_name", "model_id", "api_base", "api_provider"},
	},
	{
		group:   "obol-deployment-failures",
		metrics: []string{"litellm_deployment_failure_responses"},
		labels:  []string{"requested_model", "litellm_model_name", "model_id", "api_base", "api_provider", "exception_status", "exception_class"},
	},
	{
		// model_id → api_base / api_provider lookup table for the joins.
		// Its default labels are exactly these four.
		group:   "obol-deployment-state",
		metrics: []string{"litellm_deployment_state"},
	},
	{
		group:   "obol-llm-failures",
		metrics: []string{"litellm_llm_api_failed_requests_metric"},
		labels:  []string{"model", "model_id"},
	},
	{
		group:   "obol-proxy-failures",
		metrics: []string{"litellm_proxy_failed_requests_metric"},
		labels:  []string{"requested_model", "exception_status", "exception_class", "route"},
	},
}

// LiteLLMObservabilitySettings returns the litellm_settings keys the stack
// sets for Prometheus. Each call returns fresh values.
func LiteLLMObservabilitySettings() map[string]any {
	groups := make([]any, 0, len(liteLLMMetricGroups))
	for _, g := range liteLLMMetricGroups {
		entry := map[string]any{"group": g.group, "metrics": toAnySlice(g.metrics)}
		if g.labels != nil {
			entry["include_labels"] = toAnySlice(g.labels)
		}

		groups = append(groups, entry)
	}

	buckets := make([]any, len(liteLLMLatencyBuckets))
	for i, b := range liteLLMLatencyBuckets {
		buckets[i] = b
	}

	return map[string]any{
		"callbacks":                         []any{prometheusCallback},
		"require_auth_for_metrics_endpoint": false,
		"prometheus_latency_buckets":        buckets,
		"prometheus_metrics_config":         groups,
	}
}

// EnsureLiteLLMObservability adds the Prometheus settings to cfg's
// litellm_settings without overriding anything the operator set: the
// prometheus callback is appended to an existing callbacks list, and the
// other keys are only filled in when absent. Returns true if cfg changed.
//
// Existing stacks keep their live litellm-config ConfigMap across upgrades
// (llm.yaml renders it via lookup), so this is what turns metrics on for
// them: `obol stack up` (restoreLiteLLMConfig) and every CLI model write
// call it.
func EnsureLiteLLMObservability(cfg *LiteLLMConfig) bool {
	if cfg.LiteLLMSettings == nil {
		cfg.LiteLLMSettings = map[string]any{}
	}

	s := cfg.LiteLLMSettings
	changed := false

	if !hasPrometheusCallback(s["callbacks"]) && !hasPrometheusCallback(s["success_callback"]) {
		switch v := s["callbacks"].(type) {
		case nil:
			s["callbacks"] = []any{prometheusCallback}
		case []any:
			s["callbacks"] = append(v, prometheusCallback)
		case string:
			s["callbacks"] = []any{v, prometheusCallback}
		default:
			// Unknown shape (e.g. a custom-callback object): leave it alone
			// rather than corrupt the operator's config.
			return changed
		}

		changed = true
	}

	for key, value := range LiteLLMObservabilitySettings() {
		if key == "callbacks" {
			continue
		}

		if _, ok := s[key]; !ok {
			s[key] = value
			changed = true
		}
	}

	return changed
}

func hasPrometheusCallback(v any) bool {
	switch cb := v.(type) {
	case string:
		return cb == prometheusCallback
	case []any:
		return slices.ContainsFunc(cb, func(e any) bool { return e == prometheusCallback })
	case []string:
		return slices.Contains(cb, prometheusCallback)
	}

	return false
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}

	return out
}
