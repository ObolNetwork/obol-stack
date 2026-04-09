package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestControllerWithSecret(ns, masterKey string) *Controller {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm-secrets",
			Namespace: ns,
		},
		Data: map[string][]byte{
			"LITELLM_MASTER_KEY": []byte(masterKey),
		},
	}
	kubeClient := fake.NewSimpleClientset(secret)
	return &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{},
	}
}

func TestGetLiteLLMMasterKey(t *testing.T) {
	c := newTestControllerWithSecret("llm", "sk-obol-test-key")

	key, err := c.getLiteLLMMasterKey(context.Background(), "llm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-obol-test-key" {
		t.Fatalf("key = %q, want %q", key, "sk-obol-test-key")
	}
}

func TestGetLiteLLMMasterKeyMissingSecret(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{kubeClient: kubeClient}

	_, err := c.getLiteLLMMasterKey(context.Background(), "llm")
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}
}

func TestGetLiteLLMMasterKeyMissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "litellm-secrets",
			Namespace: "llm",
		},
		Data: map[string][]byte{
			"OTHER_KEY": []byte("value"),
		},
	}
	kubeClient := fake.NewSimpleClientset(secret)
	c := &Controller{kubeClient: kubeClient}

	_, err := c.getLiteLLMMasterKey(context.Background(), "llm")
	if err == nil {
		t.Fatal("expected error for missing LITELLM_MASTER_KEY, got nil")
	}
}

func TestLiteLLMBaseURL(t *testing.T) {
	c := &Controller{}

	url := c.litellmBaseURL("llm")
	if url != "http://litellm.llm.svc.cluster.local:4000" {
		t.Fatalf("url = %q, want %q", url, "http://litellm.llm.svc.cluster.local:4000")
	}

	c.litellmURLOverride = "http://localhost:9999"
	url = c.litellmBaseURL("llm")
	if url != "http://localhost:9999" {
		t.Fatalf("url = %q, want %q", url, "http://localhost:9999")
	}
}

func TestAddLiteLLMModelEntryViaAPI(t *testing.T) {
	var (
		gotAuth      string
		gotBody      map[string]any
		callCount    atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model/new" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		callCount.Add(1)
		gotAuth = r.Header.Get("Authorization")

		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"model_id":   "test-uuid-123",
			"model_name": gotBody["model_name"],
		})
	}))
	defer server.Close()

	c := newTestControllerWithSecret("llm", "sk-obol-test-key")
	c.litellmURLOverride = server.URL

	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call to /model/new, got %d", callCount.Load())
	}

	if gotAuth != "Bearer sk-obol-test-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-obol-test-key")
	}

	if gotBody["model_name"] != "paid/qwen3.5:9b" {
		t.Fatalf("model_name = %v, want %q", gotBody["model_name"], "paid/qwen3.5:9b")
	}

	params, ok := gotBody["litellm_params"].(map[string]any)
	if !ok {
		t.Fatal("litellm_params missing or wrong type")
	}
	if params["model"] != "openai/paid/qwen3.5:9b" {
		t.Fatalf("litellm_params.model = %v, want %q", params["model"], "openai/paid/qwen3.5:9b")
	}
	if params["api_base"] != "http://127.0.0.1:8402" {
		t.Fatalf("litellm_params.api_base = %v, want %q", params["api_base"], "http://127.0.0.1:8402")
	}
	if params["api_key"] != "unused" {
		t.Fatalf("litellm_params.api_key = %v, want %q", params["api_key"], "unused")
	}
}

func TestAddLiteLLMModelEntryHandlesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	c := newTestControllerWithSecret("llm", "sk-obol-test-key")
	c.litellmURLOverride = server.URL

	// Should not panic; best-effort, logs the error.
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

func TestAddLiteLLMModelEntryHandlesMissingSecret(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	c := &Controller{
		kubeClient:         kubeClient,
		httpClient:         &http.Client{},
		litellmURLOverride: "http://localhost:1234",
	}

	// Should not panic; the function logs and returns on missing secret.
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

// ── removeLiteLLMModelEntry tests ──────────────────────────────────────────

func TestRemoveLiteLLMModelEntry(t *testing.T) {
	var (
		infoRequested   atomic.Bool
		deleteRequested atomic.Bool
		deletedID       string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model/info":
			infoRequested.Store(true)
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"model_name": "paid/qwen3.5:9b",
						"model_info": map[string]any{"id": "model-uuid-abc"},
					},
					{
						"model_name": "other-model",
						"model_info": map[string]any{"id": "model-uuid-xyz"},
					},
				},
			})
		case "/model/delete":
			deleteRequested.Store(true)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			deletedID = body["id"].(string)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newTestControllerWithSecret("llm", "sk-obol-test-key")
	c.litellmURLOverride = server.URL

	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	if !infoRequested.Load() {
		t.Fatal("expected GET /model/info to be called")
	}
	if !deleteRequested.Load() {
		t.Fatal("expected POST /model/delete to be called")
	}
	if deletedID != "model-uuid-abc" {
		t.Fatalf("deleted ID = %q, want model-uuid-abc", deletedID)
	}
}

func TestRemoveLiteLLMModelEntryNoMatch(t *testing.T) {
	var deleteRequested atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model/info":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"model_name": "other-model",
						"model_info": map[string]any{"id": "model-uuid-xyz"},
					},
				},
			})
		case "/model/delete":
			deleteRequested.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := newTestControllerWithSecret("llm", "sk-obol-test-key")
	c.litellmURLOverride = server.URL

	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/nonexistent")

	if deleteRequested.Load() {
		t.Fatal("expected /model/delete NOT to be called when model doesn't match")
	}
}

func TestRemoveLiteLLMModelEntryServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := newTestControllerWithSecret("llm", "sk-obol-test-key")
	c.litellmURLOverride = server.URL

	// Should not panic; best-effort, logs the error.
	c.removeLiteLLMModelEntry(context.Background(), "llm", "paid/test-model")
}

// ── triggerBuyerReload tests ───────────────────────────────────────────────

func TestTriggerBuyerReload(t *testing.T) {
	var reloadCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/reload" && r.Method == "POST" {
			reloadCount.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

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
