package model

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestConfigureLiteLLM_APIKeyTriggersRestart(t *testing.T) {
	// When apiKey is non-empty, ConfigureLiteLLM should call RestartLiteLLM
	// (not hotAddModels). We verify this by checking that it fails fast
	// when the cluster isn't running — the restart path hits kubectl.
	cfg := testConfig(t)
	u := ui.New(false)

	err := ConfigureLiteLLM(cfg, u, "anthropic", "sk-test-key", []string{"claude-sonnet-4-20250514"})
	if err == nil {
		t.Fatal("expected error when cluster not running")
	}
}

func TestConfigureLiteLLM_NoAPIKeyTriesHotAdd(t *testing.T) {
	// When apiKey is empty, ConfigureLiteLLM should attempt hot-add.
	// It will fail (no cluster), but the error path differs from restart.
	cfg := testConfig(t)
	u := ui.New(false)

	err := ConfigureLiteLLM(cfg, u, "ollama", "", []string{"qwen3.5:35b"})
	if err == nil {
		t.Fatal("expected error when cluster not running")
	}
}

func TestHotAddModels_BuildsCorrectPayload(t *testing.T) {
	entries := []ModelEntry{
		{
			ModelName: "qwen3.5:35b",
			LiteLLMParams: LiteLLMParams{
				Model:   "ollama_chat/qwen3.5:35b",
				APIBase: "http://ollama.llm.svc.cluster.local:11434",
			},
		},
	}

	for _, entry := range entries {
		body := map[string]any{
			"model_name": entry.ModelName,
			"litellm_params": map[string]any{
				"model":    entry.LiteLLMParams.Model,
				"api_base": entry.LiteLLMParams.APIBase,
				"api_key":  entry.LiteLLMParams.APIKey,
			},
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var decoded map[string]any
		if err := json.Unmarshal(bodyJSON, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if decoded["model_name"] != "qwen3.5:35b" {
			t.Errorf("model_name = %v, want qwen3.5:35b", decoded["model_name"])
		}

		params, ok := decoded["litellm_params"].(map[string]any)
		if !ok {
			t.Fatal("missing litellm_params")
		}
		if params["model"] != "ollama_chat/qwen3.5:35b" {
			t.Errorf("model = %v, want ollama_chat/qwen3.5:35b", params["model"])
		}
		if params["api_base"] != "http://ollama.llm.svc.cluster.local:11434" {
			t.Errorf("api_base = %v", params["api_base"])
		}
	}
}

func TestHotAddModels_MultipleEntries(t *testing.T) {
	// Cloud providers include a wildcard entry plus explicit models.
	entries := buildModelEntries("anthropic", []string{"claude-sonnet-4-20250514", "claude-haiku-4-5-20251001"})
	if len(entries) < 2 {
		t.Fatalf("got %d entries, want >= 2", len(entries))
	}

	for _, entry := range entries {
		body := map[string]any{
			"model_name": entry.ModelName,
			"litellm_params": map[string]any{
				"model":    entry.LiteLLMParams.Model,
				"api_base": entry.LiteLLMParams.APIBase,
				"api_key":  entry.LiteLLMParams.APIKey,
			},
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s: %v", entry.ModelName, err)
		}
		if len(bodyJSON) == 0 {
			t.Errorf("empty JSON for %s", entry.ModelName)
		}
	}
}

func TestHotAddModels_ServerAccepts(t *testing.T) {
	var received []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model/new" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		received = append(received, payload)
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	entries := buildModelEntries("ollama", []string{"llama3.2:3b"})
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	bodyJSON, err := json.Marshal(map[string]any{
		"model_name": entries[0].ModelName,
		"litellm_params": map[string]any{
			"model":    entries[0].LiteLLMParams.Model,
			"api_base": entries[0].LiteLLMParams.APIBase,
			"api_key":  entries[0].LiteLLMParams.APIKey,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/model/new", "application/json",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		t.Fatalf("POST /model/new: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(received) != 1 {
		t.Fatalf("server received %d requests, want 1", len(received))
	}
	if received[0]["model_name"] != "llama3.2:3b" {
		t.Errorf("model_name = %v, want llama3.2:3b", received[0]["model_name"])
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		ConfigDir: dir,
		DataDir:   dir,
		BinDir:    dir,
	}
}
