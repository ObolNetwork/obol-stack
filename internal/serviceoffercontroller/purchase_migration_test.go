package serviceoffercontroller

import (
	"context"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// These tests pin the upgrade path for the x402-buyer split (issue #321):
// clusters upgraded in place carry paid-route entries whose api_base still
// points at the removed litellm-pod sidecar (127.0.0.1:8402). Both the
// startup migration and the per-purchase reconcile must rewrite them to the
// x402-buyer Service and hot-sync the live router.

func newMigrationController(t *testing.T, configYAML string) (*Controller, *litellmFake) {
	t.Helper()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "litellm-config", Namespace: "llm"},
		Data:       map[string]string{"config.yaml": configYAML},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "litellm-secrets", Namespace: "llm"},
		Data:       map[string][]byte{"LITELLM_MASTER_KEY": []byte("sk-obol-test")},
	}
	fakeAPI := newLiteLLMFake()
	t.Cleanup(fakeAPI.close)

	return &Controller{
		kubeClient:         fake.NewSimpleClientset(cm, secret),
		httpClient:         fakeAPI.server.Client(),
		litellmURLOverride: fakeAPI.server.URL,
	}, fakeAPI
}

func configMapModelList(t *testing.T, c *Controller) []model.ModelEntry {
	t.Helper()

	cm, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm-config: %v", err)
	}
	var cfg model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatalf("parse config.yaml: %v", err)
	}
	return cfg.ModelList
}

func TestBuyerAPIBase(t *testing.T) {
	// The /v1 suffix is load-bearing: LiteLLM's OpenAI provider does not
	// append it, and without it the buyer's mux returns 404 (CLAUDE.md
	// pitfall 6).
	if got, want := buyerAPIBase("llm"), "http://x402-buyer.llm.svc.cluster.local:8402/v1"; got != want {
		t.Fatalf("buyerAPIBase(llm) = %q; want %q", got, want)
	}
}

func TestAddLiteLLMModelEntryMigratesLegacySidecarAPIBase(t *testing.T) {
	const legacyConfig = `model_list:
  - model_name: "paid/qwen36-deep"
    litellm_params:
      model: "openai/paid/qwen36-deep"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
`
	c, fakeAPI := newMigrationController(t, legacyConfig)
	fakeAPI.infoResp = []map[string]any{
		{"model_name": "paid/qwen36-deep", "model_info": map[string]any{"id": "id-1"}},
	}

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen36-deep")

	entries := configMapModelList(t, c)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after migration, got %d", len(entries))
	}
	if got, want := entries[0].LiteLLMParams.APIBase, buyerAPIBase("llm"); got != want {
		t.Errorf("api_base = %q; want migrated %q", got, want)
	}
	if got := fakeAPI.delCalls.Load(); got != 1 {
		t.Errorf("delCalls = %d; want 1 (stale legacy deployment removed from live router)", got)
	}
	if got := fakeAPI.addCalls.Load(); got != 1 {
		t.Errorf("addCalls = %d; want 1 (migrated entry hot-added)", got)
	}
}

func TestAddLiteLLMModelEntryLeavesNonLegacyEntryAlone(t *testing.T) {
	const currentConfig = `model_list:
  - model_name: "paid/qwen36-deep"
    litellm_params:
      model: "openai/paid/qwen36-deep"
      api_base: "http://x402-buyer.llm.svc.cluster.local:8402/v1"
      api_key: "unused"
`
	c, fakeAPI := newMigrationController(t, currentConfig)

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen36-deep")

	if got := fakeAPI.addCalls.Load() + fakeAPI.delCalls.Load(); got != 0 {
		t.Errorf("expected no hot API calls for an up-to-date entry, got %d", got)
	}
	entries := configMapModelList(t, c)
	if got, want := entries[0].LiteLLMParams.APIBase, buyerAPIBase("llm"); got != want {
		t.Errorf("api_base = %q; want unchanged %q", got, want)
	}
}

func TestMigrateLegacyBuyerAPIBases(t *testing.T) {
	// Wildcard and concrete legacy entries are both rewritten; entries
	// pointing elsewhere (cloud providers, custom endpoints) are untouched.
	const mixedConfig = `model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
  - model_name: "paid/qwen36-deep"
    litellm_params:
      model: "openai/paid/qwen36-deep"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
  - model_name: "qwen36-deep"
    litellm_params:
      model: "openai/qwen36-deep"
      api_base: "http://192.168.18.23:8000/v1"
      api_key: "os.environ/CUSTOM_KEY"
`
	c, fakeAPI := newMigrationController(t, mixedConfig)
	fakeAPI.infoResp = []map[string]any{
		{"model_name": "paid/*", "model_info": map[string]any{"id": "id-wild"}},
		{"model_name": "paid/qwen36-deep", "model_info": map[string]any{"id": "id-1"}},
	}

	c.migrateLegacyBuyerAPIBases(context.Background(), "llm")

	entries := configMapModelList(t, c)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	byName := make(map[string]model.ModelEntry, len(entries))
	for _, e := range entries {
		byName[e.ModelName] = e
	}
	for _, name := range []string{"paid/*", "paid/qwen36-deep"} {
		if got, want := byName[name].LiteLLMParams.APIBase, buyerAPIBase("llm"); got != want {
			t.Errorf("%s api_base = %q; want migrated %q", name, got, want)
		}
	}
	if got, want := byName["qwen36-deep"].LiteLLMParams.APIBase, "http://192.168.18.23:8000/v1"; got != want {
		t.Errorf("custom endpoint api_base = %q; want untouched %q", got, want)
	}

	if got := fakeAPI.delCalls.Load(); got != 2 {
		t.Errorf("delCalls = %d; want 2 (both legacy deployments removed live)", got)
	}
	if got := fakeAPI.addCalls.Load(); got != 2 {
		t.Errorf("addCalls = %d; want 2 (both migrated entries hot-added)", got)
	}
}

func TestMigrateLegacyBuyerAPIBasesNoopWhenClean(t *testing.T) {
	const cleanConfig = `model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://x402-buyer.llm.svc.cluster.local:8402/v1"
      api_key: "unused"
`
	c, fakeAPI := newMigrationController(t, cleanConfig)

	c.migrateLegacyBuyerAPIBases(context.Background(), "llm")

	if got := fakeAPI.addCalls.Load() + fakeAPI.delCalls.Load(); got != 0 {
		t.Errorf("expected no hot API calls on a clean config, got %d", got)
	}
}

func TestMigrateLegacyBuyerAPIBasesMissingConfigMap(t *testing.T) {
	fakeAPI := newLiteLLMFake()
	t.Cleanup(fakeAPI.close)
	c := &Controller{
		kubeClient:         fake.NewSimpleClientset(),
		httpClient:         fakeAPI.server.Client(),
		litellmURLOverride: fakeAPI.server.URL,
	}

	// Must not panic or call the API when litellm-config does not exist
	// (fresh cluster where the controller starts before the chart applies).
	c.migrateLegacyBuyerAPIBases(context.Background(), "llm")

	if got := fakeAPI.addCalls.Load() + fakeAPI.delCalls.Load(); got != 0 {
		t.Errorf("expected no hot API calls without a ConfigMap, got %d", got)
	}
}
