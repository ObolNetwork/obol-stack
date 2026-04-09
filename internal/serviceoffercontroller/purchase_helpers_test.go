package serviceoffercontroller

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"gopkg.in/yaml.v3"
)

func newTestControllerWithLiteLLM(ns string) *Controller {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm-config",
			Namespace: ns,
		},
		Data: map[string]string{
			"config.yaml": "model_list: []\n",
		},
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm",
			Namespace: ns,
		},
	}
	kubeClient := fake.NewSimpleClientset(cm, deploy)
	return &Controller{
		kubeClient: kubeClient,
	}
}

func TestAddLiteLLMModelEntryUpdatesConfigMapAndRestarts(t *testing.T) {
	c := newTestControllerWithLiteLLM("llm")

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	cm, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm-config: %v", err)
	}

	var cfg model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatalf("parse config.yaml: %v", err)
	}
	if len(cfg.ModelList) != 1 {
		t.Fatalf("expected 1 model entry, got %d", len(cfg.ModelList))
	}
	entry := cfg.ModelList[0]
	if entry.ModelName != "paid/qwen3.5:9b" {
		t.Fatalf("model_name = %q, want %q", entry.ModelName, "paid/qwen3.5:9b")
	}
	if entry.LiteLLMParams.Model != "openai/paid/qwen3.5:9b" {
		t.Fatalf("litellm_params.model = %q", entry.LiteLLMParams.Model)
	}

	deploy, err := c.kubeClient.AppsV1().Deployments("llm").Get(context.Background(), "litellm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm deployment: %v", err)
	}
	if deploy.Spec.Template.Annotations["obol.org/restartedAt"] == "" {
		t.Fatal("expected rollout restart annotation to be set")
	}
}

func TestAddLiteLLMModelEntryIsIdempotent(t *testing.T) {
	c := newTestControllerWithLiteLLM("llm")

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")
	deploy1, _ := c.kubeClient.AppsV1().Deployments("llm").Get(context.Background(), "litellm", metav1.GetOptions{})
	restartedAt := deploy1.Spec.Template.Annotations["obol.org/restartedAt"]
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	cm, _ := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if strings.Count(cm.Data["config.yaml"], "paid/qwen3.5:9b") != 2 {
		t.Fatal("expected exactly one model entry and one openai target reference")
	}
	deploy2, _ := c.kubeClient.AppsV1().Deployments("llm").Get(context.Background(), "litellm", metav1.GetOptions{})
	if deploy2.Spec.Template.Annotations["obol.org/restartedAt"] != restartedAt {
		t.Fatal("idempotent add should not trigger a second restart")
	}
}

func TestAddLiteLLMModelEntryHandlesMissingConfigMap(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{kubeClient: kubeClient}
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

func TestRemoveLiteLLMModelEntryUpdatesConfigMapAndRestarts(t *testing.T) {
	c := newTestControllerWithLiteLLM("llm")
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	cm, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm-config: %v", err)
	}
	if strings.Contains(cm.Data["config.yaml"], "paid/qwen3.5:9b") {
		t.Fatal("expected model entry to be removed from config.yaml")
	}
}

func TestRemoveLiteLLMModelEntryNoMatch(t *testing.T) {
	c := newTestControllerWithLiteLLM("llm")
	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/nonexistent")
}

func TestRemoveLiteLLMModelEntryServerError(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{kubeClient: kubeClient}
	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

// ── triggerBuyerReload tests ───────────────────────────────────────────────

func TestTriggerBuyerReload(t *testing.T) {
	// The triggerBuyerReload hits pod IPs directly, not the LiteLLM service.
	// We can't easily test the pod discovery with fake client, but we can
	// verify the function doesn't panic with no pods.
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{},
	}

	// Should not panic with no pods.
	c.triggerBuyerReload(context.Background(), "llm")
}
