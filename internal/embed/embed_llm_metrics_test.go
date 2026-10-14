package embed

import (
	"encoding/json"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func findNamedDoc(t *testing.T, file, kind, name string) map[string]any {
	t.Helper()

	data, err := ReadInfrastructureFile(file)
	if err != nil {
		t.Fatal(err)
	}

	for _, doc := range multiDoc(data) {
		if doc["kind"] == kind && nested(doc, "metadata", "name") == name {
			return doc
		}
	}

	t.Fatalf("%s: %s %q not found", file, kind, name)

	return nil
}

// LiteLLM metrics are scraped only if the monitor carries release=monitoring
// (values/monitoring.yaml.gotmpl) and hits /metrics/ — /metrics is a 307.
func TestLiteLLMServiceMonitor(t *testing.T) {
	sm := findNamedDoc(t, "base/templates/llm.yaml", "ServiceMonitor", "litellm")
	if got := nested(sm, "metadata", "labels", "release"); got != "monitoring" {
		t.Fatalf("ServiceMonitor release label = %v, want monitoring", got)
	}

	if got := nested(sm, "spec", "selector", "matchLabels"); !reflect.DeepEqual(got, map[string]any{"app": "litellm"}) {
		t.Fatalf("selector = %v", got)
	}

	endpoints, _ := nested(sm, "spec", "endpoints").([]any)
	if len(endpoints) != 1 {
		t.Fatalf("endpoints = %v", endpoints)
	}

	ep := endpoints[0].(map[string]any)
	if ep["port"] != "http" || ep["path"] != "/metrics/" {
		t.Fatalf("endpoint = %v, want port http path /metrics/", ep)
	}

	svc := findNamedDoc(t, "base/templates/llm.yaml", "Service", "litellm")
	if got := nested(svc, "spec", "type"); got != "ClusterIP" {
		t.Fatalf("litellm Service type = %v: it serves unauthenticated /metrics and must stay ClusterIP", got)
	}

	if got := nested(svc, "metadata", "labels", "app"); got != "litellm" {
		t.Fatalf("litellm Service label app = %v, the ServiceMonitor selects app=litellm", got)
	}
}

func TestLiteLLMPrometheusRules(t *testing.T) {
	rule := findNamedDoc(t, "base/templates/llm-prometheus-rules.yaml", "PrometheusRule", "litellm")
	if got := nested(rule, "metadata", "labels", "release"); got != "monitoring" {
		t.Fatalf("PrometheusRule release label = %v, want monitoring", got)
	}

	var records []string

	groups, _ := nested(rule, "spec", "groups").([]any)
	for _, g := range groups {
		rules, _ := g.(map[string]any)["rules"].([]any)
		for _, r := range rules {
			if rec, ok := r.(map[string]any)["record"].(string); ok {
				records = append(records, rec)
			}
		}
	}
	// Documented in docs/observability.md and the obol-stack skill.
	for _, want := range []string{
		"obol:llm_ttft_seconds:p50_5m",
		"obol:llm_ttft_seconds:p95_5m",
		"obol:llm_output_tokens_per_second:mean_5m",
		"obol:llm_error_ratio:5m",
	} {
		if !slices.Contains(records, want) {
			t.Errorf("missing recording rule %s (have %v)", want, records)
		}
	}
}

// Dashboard uids are deep-link contracts; every Prometheus query must use the
// provisioned datasource uid.
func TestDashboards_ValidJSONAndStableUIDs(t *testing.T) {
	uids := map[string]string{}

	err := fs.WalkDir(infrastructureFS, "infrastructure/base/dashboards", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".json" {
			return err
		}

		data, err := infrastructureFS.ReadFile(p)
		if err != nil {
			return err
		}

		var dash map[string]any
		if err := json.Unmarshal(data, &dash); err != nil {
			t.Errorf("%s: invalid JSON: %v", p, err)
			return nil
		}

		uid, _ := dash["uid"].(string)

		uids[uid] = p
		if dash["editable"] != false {
			t.Errorf("%s: editable must be false (provisioned, read-only)", p)
		}

		checkDatasources(t, p, dash)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, uid := range []string{"obol-stack-overview", "obol-eth-node", "obol-charon", "obol-llm"} {
		if _, ok := uids[uid]; !ok {
			t.Errorf("no dashboard with uid %q (have %v)", uid, uids)
		}
	}

	overview, err := ReadInfrastructureFile("base/dashboards/obol-stack-overview.json")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(overview), `"url": "/grafana/d/obol-llm"`) {
		t.Error("overview dashboard does not link to /grafana/d/obol-llm")
	}
}

func checkDatasources(t *testing.T, file string, v any) {
	t.Helper()

	switch x := v.(type) {
	case map[string]any:
		if ds, ok := x["datasource"].(map[string]any); ok && ds["type"] == "prometheus" && ds["uid"] != "prometheus" {
			t.Errorf("%s: prometheus datasource uid = %v, want prometheus", file, ds["uid"])
		}

		for _, c := range x {
			checkDatasources(t, file, c)
		}
	case []any:
		for _, c := range x {
			checkDatasources(t, file, c)
		}
	}
}

// grafana chart >=13.x renders the init-sc-datasources container's METHOD
// from sidecar.datasources.watchMethod. WATCH never exits, so with
// initDatasources the Grafana pod sits in Init forever (v0.16.0-rc1).
func TestGrafanaInitDatasourcesUsesList(t *testing.T) {
	data, err := ReadInfrastructureFile("values/monitoring.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	docs := multiDoc(data)
	if len(docs) != 1 {
		t.Fatalf("monitoring values: got %d YAML docs, want 1", len(docs))
	}

	if got := nested(docs[0], "grafana", "sidecar", "datasources", "initDatasources"); got != true {
		t.Skipf("initDatasources = %v; watchMethod only matters for the init container", got)
	}

	if got := nested(docs[0], "grafana", "sidecar", "datasources", "watchMethod"); got != "LIST" {
		t.Fatalf("grafana.sidecar.datasources.watchMethod = %v, want LIST (WATCH blocks the init container forever)", got)
	}
}
