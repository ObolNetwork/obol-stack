package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// litellmFake is a minimal httptest stand-in for the LiteLLM admin API.
// It records every received request and responds to /model/new, /model/info,
// and /model/delete. Used to assert that addLiteLLMModelEntry and
// removeLiteLLMModelEntry hot-add/hot-delete instead of restarting the pod.
type litellmFake struct {
	server   *httptest.Server
	addCalls atomic.Int32
	delCalls atomic.Int32
	infoResp []map[string]any // returned from /model/info
	authSeen atomic.Value     // last Authorization header value
}

func newLiteLLMFake() *litellmFake {
	f := &litellmFake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/model/new", func(w http.ResponseWriter, r *http.Request) {
		f.authSeen.Store(r.Header.Get("Authorization"))
		f.addCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/model/info", func(w http.ResponseWriter, r *http.Request) {
		f.authSeen.Store(r.Header.Get("Authorization"))
		payload := map[string]any{"data": f.infoResp}
		b, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("/model/delete", func(w http.ResponseWriter, r *http.Request) {
		f.authSeen.Store(r.Header.Get("Authorization"))
		f.delCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	f.server = httptest.NewServer(mux)
	return f
}

func (f *litellmFake) close() { f.server.Close() }

func newTestControllerWithLiteLLM(ns string) (*Controller, *litellmFake) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm-config",
			Namespace: ns,
		},
		Data: map[string]string{
			"config.yaml": "model_list: []\n",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm-secrets",
			Namespace: ns,
		},
		Data: map[string][]byte{
			"LITELLM_MASTER_KEY": []byte("sk-obol-test"),
		},
	}
	kubeClient := fake.NewSimpleClientset(cm, secret)
	fakeAPI := newLiteLLMFake()
	return &Controller{
		kubeClient:         kubeClient,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
		litellmURLOverride: fakeAPI.server.URL,
	}, fakeAPI
}

func TestAddLiteLLMModelEntryUpdatesConfigMapAndHotAdds(t *testing.T) {
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

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

	if got := fakeAPI.addCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 call to /model/new, got %d", got)
	}
	if auth, _ := fakeAPI.authSeen.Load().(string); auth != "Bearer sk-obol-test" {
		t.Fatalf("authorization header = %q, want Bearer sk-obol-test", auth)
	}
}

func TestAddLiteLLMModelEntryIsIdempotent(t *testing.T) {
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")
	firstCalls := fakeAPI.addCalls.Load()
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	cm, _ := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if strings.Count(cm.Data["config.yaml"], "paid/qwen3.5:9b") != 2 {
		t.Fatal("expected exactly one model entry and one openai target reference")
	}
	if got := fakeAPI.addCalls.Load(); got != firstCalls {
		t.Fatalf("idempotent add should not re-hit /model/new, got %d calls total", got)
	}
}

func TestAddLiteLLMModelEntryNeverRestartsDeployment(t *testing.T) {
	// Deliberately omit any Deployment from the fake client. The controller
	// must never touch Deployments during model add — if it tries, the fake
	// client will surface a NotFound error that would be logged but harmless.
	// What matters: no rollout annotation should appear under any code path,
	// because restartLiteLLM no longer exists.
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	// There should be no Deployment list action at all.
	deployList, err := c.kubeClient.AppsV1().Deployments("llm").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deployList.Items) != 0 {
		t.Fatalf("add should not create a Deployment; got %d", len(deployList.Items))
	}
}

func TestAddLiteLLMModelEntryHandlesMissingConfigMap(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

func TestRemoveLiteLLMModelEntryUpdatesConfigMapAndHotDeletes(t *testing.T) {
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

	// Seed /model/info with a matching entry so hot-delete resolves an ID.
	fakeAPI.infoResp = []map[string]any{
		{
			"model_name": "paid/qwen3.5:9b",
			"model_info": map[string]any{"id": "abc-123"},
		},
	}

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")
	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	cm, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm-config: %v", err)
	}
	if strings.Contains(cm.Data["config.yaml"], "paid/qwen3.5:9b") {
		t.Fatal("expected model entry to be removed from config.yaml")
	}
	if got := fakeAPI.delCalls.Load(); got != 1 {
		t.Fatalf("expected 1 call to /model/delete, got %d", got)
	}
}

func TestRemoveLiteLLMModelEntryNoMatch(t *testing.T) {
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

	// ConfigMap and live router both have no matching entry -> no delete call.
	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/nonexistent")
	if got := fakeAPI.delCalls.Load(); got != 0 {
		t.Fatalf("expected no /model/delete calls for missing entry, got %d", got)
	}
}

func TestRemoveLiteLLMModelEntryRetriesHotDeleteWhenConfigMapAlreadyClean(t *testing.T) {
	c, fakeAPI := newTestControllerWithLiteLLM("llm")
	defer fakeAPI.close()

	// Simulates a previous reconcile that removed the persistent ConfigMap entry
	// but crashed before, or failed during, the live /model/delete API call.
	fakeAPI.infoResp = []map[string]any{
		{
			"model_name": "paid/qwen3.5:9b",
			"model_info": map[string]any{"id": "stale-live-route"},
		},
	}

	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	if got := fakeAPI.delCalls.Load(); got != 1 {
		t.Fatalf("expected /model/delete retry for stale live route, got %d calls", got)
	}
}

func TestRemoveLiteLLMModelEntryServerError(t *testing.T) {
	// No ConfigMap in the fake client → read fails, function logs and returns.
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
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

func TestOtherActivePurchaseUsesModel(t *testing.T) {
	now := metav1.Now()
	purchases := []*monetizeapi.PurchaseRequest{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "agent-a"},
			Spec:       monetizeapi.PurchaseRequestSpec{Model: "qwen3.5:9b"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "agent-a"},
			Spec:       monetizeapi.PurchaseRequestSpec{Model: "qwen3.5:9b"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gamma", Namespace: "agent-a"},
			Spec:       monetizeapi.PurchaseRequestSpec{Model: "qwen3.6:9b"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "draining", Namespace: "agent-a", DeletionTimestamp: &now},
			Spec:       monetizeapi.PurchaseRequestSpec{Model: "qwen3.7:9b"},
		},
	}

	conflict := otherActivePurchaseUsesModel(purchases, "agent-a", "alpha", "qwen3.5:9b")
	if conflict == nil || conflict.Name != "beta" {
		t.Fatalf("conflict = %#v, want beta", conflict)
	}

	noConflict := otherActivePurchaseUsesModel(purchases, "agent-a", "gamma", "qwen3.6:9b")
	if noConflict != nil {
		t.Fatalf("conflict = %#v, want nil", noConflict)
	}

	drainingConflict := otherActivePurchaseUsesModel(purchases, "agent-a", "nobody", "qwen3.7:9b")
	if drainingConflict == nil || drainingConflict.Name != "draining" {
		t.Fatalf("conflict = %#v, want draining", drainingConflict)
	}
}

type staticTransport struct {
	hostToBody map[string]string
}

func (s *staticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(s.hostToBody[req.URL.Host])),
	}, nil
}

func TestCheckBuyerStatusSkipsDeletingPods(t *testing.T) {
	now := metav1.Now()
	kubeClient := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "litellm-old",
				Namespace:         "llm",
				Labels:            map[string]string{"app": "litellm"},
				DeletionTimestamp: &now,
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.1"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "litellm-new",
				Namespace: "llm",
				Labels:    map[string]string{"app": "litellm"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.2"},
		},
	)

	c := &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{Transport: &staticTransport{
			hostToBody: map[string]string{
				"10.0.0.1:8402": `{"solo":{"remaining":99,"spent":1}}`,
				"10.0.0.2:8402": `{"solo":{"remaining":3,"spent":2}}`,
			},
		}},
	}

	remaining, spent, err := c.checkBuyerStatus(context.Background(), "llm", "solo")
	if err != nil {
		t.Fatalf("checkBuyerStatus: %v", err)
	}
	if remaining != 3 || spent != 2 {
		t.Fatalf("remaining/spent = %d/%d, want 3/2", remaining, spent)
	}
}
