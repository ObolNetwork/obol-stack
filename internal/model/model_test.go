package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildModelEntries(t *testing.T) {
	t.Run("ollama models get ollama_chat/ prefix and api_base", func(t *testing.T) {
		entries := buildModelEntries("ollama", []string{"qwen3.5:35b", "llama3.2:3b"})
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}

		if entries[0].LiteLLMParams.Model != "ollama_chat/qwen3.5:35b" {
			t.Errorf("model = %q, want ollama_chat/qwen3.5:35b", entries[0].LiteLLMParams.Model)
		}

		if entries[0].LiteLLMParams.APIBase != "http://ollama.llm.svc.cluster.local:11434" {
			t.Errorf("api_base = %q", entries[0].LiteLLMParams.APIBase)
		}

		if entries[0].LiteLLMParams.APIKey != "" {
			t.Errorf("ollama should not have api_key, got %q", entries[0].LiteLLMParams.APIKey)
		}
	})

	t.Run("anthropic puts explicit before the wildcard", func(t *testing.T) {
		entries := buildModelEntries("anthropic", []string{"claude-sonnet-4-5-20250929"})
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2 (explicit + wildcard)", len(entries))
		}
		// Explicit first so model.Rank picks it as primary; Hermes can't
		// send `model: anthropic/*` literally.
		if entries[0].ModelName != "claude-sonnet-4-5-20250929" {
			t.Errorf("entries[0].ModelName = %q, want explicit model", entries[0].ModelName)
		}
		if entries[0].LiteLLMParams.Model != "claude-sonnet-4-5-20250929" {
			t.Errorf("entries[0].Model = %q", entries[0].LiteLLMParams.Model)
		}
		if entries[0].LiteLLMParams.APIKey != "os.environ/ANTHROPIC_API_KEY" {
			t.Errorf("api_key = %q, want os.environ/ANTHROPIC_API_KEY", entries[0].LiteLLMParams.APIKey)
		}
		// Wildcard appended last as catch-all.
		if entries[1].ModelName != "anthropic/*" {
			t.Errorf("entries[1].ModelName = %q, want anthropic/*", entries[1].ModelName)
		}
	})

	t.Run("openai puts explicit before the wildcard", func(t *testing.T) {
		entries := buildModelEntries("openai", []string{"gpt-4o"})
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2 (explicit + wildcard)", len(entries))
		}

		if entries[0].ModelName != "gpt-4o" {
			t.Errorf("entries[0].ModelName = %q, want gpt-4o", entries[0].ModelName)
		}
		if entries[0].LiteLLMParams.Model != "openai/gpt-4o" {
			t.Errorf("entries[0].Model = %q, want openai/gpt-4o", entries[0].LiteLLMParams.Model)
		}
		if entries[1].ModelName != "openai/*" {
			t.Errorf("entries[1].ModelName = %q, want openai/*", entries[1].ModelName)
		}
	})

	t.Run("empty models still gets wildcard for cloud providers", func(t *testing.T) {
		entries := buildModelEntries("anthropic", nil)
		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1 (wildcard only)", len(entries))
		}

		if entries[0].ModelName != "anthropic/*" {
			t.Errorf("ModelName = %q, want anthropic/*", entries[0].ModelName)
		}
	})

	t.Run("empty ollama returns nil", func(t *testing.T) {
		entries := buildModelEntries("ollama", nil)
		if entries != nil {
			t.Errorf("expected nil for empty ollama, got %v", entries)
		}
	})

	t.Run("anthropic entries inject system-message cache breakpoint", func(t *testing.T) {
		entries := buildModelEntries("anthropic", []string{"claude-sonnet-4-5-20250929", "claude-opus-4-7"})
		if len(entries) != 3 {
			t.Fatalf("got %d entries, want 3 (wildcard + 2 explicit)", len(entries))
		}

		for i, e := range entries {
			points := e.LiteLLMParams.CacheControlInjectionPoints
			if len(points) != 1 {
				t.Fatalf("entries[%d] (%s): got %d cache points, want 1", i, e.ModelName, len(points))
			}
			if points[0].Location != "message" {
				t.Errorf("entries[%d] (%s): Location = %q, want \"message\"", i, e.ModelName, points[0].Location)
			}
			if points[0].Role != "system" {
				t.Errorf("entries[%d] (%s): Role = %q, want \"system\"", i, e.ModelName, points[0].Role)
			}
		}
	})

	t.Run("non-anthropic entries do not inject cache control", func(t *testing.T) {
		openaiEntries := buildModelEntries("openai", []string{"gpt-4o"})
		for i, e := range openaiEntries {
			if len(e.LiteLLMParams.CacheControlInjectionPoints) != 0 {
				t.Errorf("openai entries[%d] (%s): unexpected cache points %+v", i, e.ModelName, e.LiteLLMParams.CacheControlInjectionPoints)
			}
		}

		ollamaEntries := buildModelEntries("ollama", []string{"qwen3.5:9b"})
		for i, e := range ollamaEntries {
			if len(e.LiteLLMParams.CacheControlInjectionPoints) != 0 {
				t.Errorf("ollama entries[%d] (%s): unexpected cache points %+v", i, e.ModelName, e.LiteLLMParams.CacheControlInjectionPoints)
			}
		}
	})
}

// TestBuildCustomEndpointEntry encodes the LiteLLM `model_name` contract for
// custom endpoints added via `obol model setup custom`:
//
//   - `model_name` is the BARE model identifier (no `custom/<name>/` namespace
//     and no `openai/` provider prefix). The agent reads this string and
//     passes it back as the `model` field on chat-completion calls; any
//     namespacing here breaks the round-trip — the flow-14 / ca820c9 bug.
//   - `litellm_params.model` carries the `openai/` provider hint so LiteLLM
//     routes through its OpenAI-compatible adapter to the user's upstream.
//   - The `name` (label) flag does NOT participate in the route key.
func TestBuildCustomEndpointEntry(t *testing.T) {
	t.Run("bare model_name with openai-compat routing", func(t *testing.T) {
		entry := buildCustomEndpointEntry("qwen36-fast", "http://host.k3d.internal:8000/v1", "secret-key")
		if entry.ModelName != "qwen36-fast" {
			t.Errorf("ModelName = %q, want bare %q (contract: bare LiteLLM model_name)", entry.ModelName, "qwen36-fast")
		}
		if strings.HasPrefix(entry.ModelName, "custom/") {
			t.Errorf("ModelName = %q must NOT carry custom/<name>/ namespace", entry.ModelName)
		}
		if entry.LiteLLMParams.Model != "openai/qwen36-fast" {
			t.Errorf("litellm_params.model = %q, want openai/qwen36-fast", entry.LiteLLMParams.Model)
		}
		if entry.LiteLLMParams.APIBase != "http://host.k3d.internal:8000/v1" {
			t.Errorf("api_base = %q", entry.LiteLLMParams.APIBase)
		}
		if entry.LiteLLMParams.APIKey != "secret-key" {
			t.Errorf("api_key = %q, want secret-key", entry.LiteLLMParams.APIKey)
		}
	})

	t.Run("empty api_key falls back to none", func(t *testing.T) {
		// Some self-hosted OpenAI-compatible servers (vLLM, llama.cpp, mlx-lm)
		// don't require auth. LiteLLM still wants a non-empty api_key field
		// or the openai client errors before even hitting the endpoint.
		entry := buildCustomEndpointEntry("any-model", "http://host:8000/v1", "")
		if entry.LiteLLMParams.APIKey != "none" {
			t.Errorf("api_key = %q, want %q (LiteLLM placeholder)", entry.LiteLLMParams.APIKey, "none")
		}
	})

	t.Run("model with colon tag survives intact", func(t *testing.T) {
		// A custom endpoint can serve a tagged model id (e.g. mlx-lm publishing
		// `qwen3:9b-mlx`). The colon must NOT be stripped or interpreted as a
		// provider separator.
		entry := buildCustomEndpointEntry("qwen3:9b-mlx", "http://host:8000/v1", "")
		if entry.ModelName != "qwen3:9b-mlx" {
			t.Errorf("ModelName = %q, want qwen3:9b-mlx unchanged", entry.ModelName)
		}
	})
}

func TestExpandWildcard(t *testing.T) {
	t.Run("uses live models when available", func(t *testing.T) {
		live := []string{"claude-sonnet-4-6", "claude-opus-4", "gpt-4o"}

		got := expandWildcard("anthropic", live)
		if len(got) != 2 {
			t.Fatalf("got %d models, want 2 (claude only)", len(got))
		}
	})

	t.Run("falls back to well-known when no live models", func(t *testing.T) {
		got := expandWildcard("anthropic", nil)
		if len(got) != len(WellKnownModels["anthropic"]) {
			t.Fatalf("got %d models, want %d", len(got), len(WellKnownModels["anthropic"]))
		}
	})

	t.Run("returns nil for unknown provider", func(t *testing.T) {
		got := expandWildcard("unknown-provider", nil)
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		model    string
		name     string
		wantProv string
	}{
		{"anthropic/*", "anthropic/*", "anthropic"},
		{"openai/*", "openai/*", "openai"},
		{"ollama/llama3.2:3b", "llama3.2:3b", "ollama"},
		{"ollama_chat/qwen3.5:35b", "qwen3.5:35b", "ollama"},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250929", "anthropic"},
		{"gpt-4o", "gpt-4o", "openai"},
		{"o1-mini", "o1-mini", "openai"},
		{"openai/gpt-4", "gpt-4", "openai"},
		{"mistral-large", "custom/my-vllm/mistral-large", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			entry := ModelEntry{
				ModelName:     tt.name,
				LiteLLMParams: LiteLLMParams{Model: tt.model},
			}

			got := detectProvider(entry)
			if got != tt.wantProv {
				t.Errorf("detectProvider(%q) = %q, want %q", tt.model, got, tt.wantProv)
			}
		})
	}
}

func TestBuildProviderStatus(t *testing.T) {
	t.Run("status from config with models", func(t *testing.T) {
		configYAML := []byte(`
model_list:
  - model_name: claude-sonnet-4-5-20250929
    litellm_params:
      model: claude-sonnet-4-5-20250929
      api_key: os.environ/ANTHROPIC_API_KEY
  - model_name: qwen3.5:35b
    litellm_params:
      model: ollama/qwen3.5:35b
      api_base: http://ollama.llm.svc:11434
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
`)
		secretJSON := []byte(`{"data":{"ANTHROPIC_API_KEY":"c2stYW50LXh4eA==","OPENAI_API_KEY":"","LITELLM_MASTER_KEY":"c2stb2JvbA=="}}`)

		status, err := buildProviderStatus(configYAML, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Anthropic: enabled with 1 model, has API key
		if s := status["anthropic"]; !s.Enabled || len(s.Models) != 1 || !s.HasAPIKey {
			t.Errorf("anthropic: got %+v", s)
		}

		// Ollama: enabled with 1 model, always has key
		if s := status["ollama"]; !s.Enabled || len(s.Models) != 1 || !s.HasAPIKey {
			t.Errorf("ollama: got %+v", s)
		}

		// OpenAI: not in config, not enabled
		if s := status["openai"]; s.Enabled {
			t.Errorf("openai should not be enabled, got %+v", s)
		}
	})

	t.Run("empty model_list", func(t *testing.T) {
		configYAML := []byte(`model_list: []`)
		secretJSON := []byte(`{"data":{}}`)

		status, err := buildProviderStatus(configYAML, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// All known providers should appear (from knownProviders)
		if _, ok := status["ollama"]; !ok {
			t.Error("ollama should be present")
		}
	})

	t.Run("invalid config yaml", func(t *testing.T) {
		_, err := buildProviderStatus([]byte(`{bad yaml`), []byte(`{"data":{}}`))
		if err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})

	t.Run("invalid secret json", func(t *testing.T) {
		_, err := buildProviderStatus([]byte(`model_list: []`), []byte(`not json`))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestResolveAPIKey(t *testing.T) {
	t.Run("primary env var found", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-primary")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		key, envVar := ResolveAPIKey("anthropic")
		if key != "sk-ant-primary" {
			t.Errorf("key = %q, want sk-ant-primary", key)
		}

		if envVar != "ANTHROPIC_API_KEY" {
			t.Errorf("envVar = %q, want ANTHROPIC_API_KEY", envVar)
		}
	})

	t.Run("fallback env var found", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-token-123")

		key, envVar := ResolveAPIKey("anthropic")
		if key != "oauth-token-123" {
			t.Errorf("key = %q, want oauth-token-123", key)
		}

		if envVar != "CLAUDE_CODE_OAUTH_TOKEN" {
			t.Errorf("envVar = %q, want CLAUDE_CODE_OAUTH_TOKEN", envVar)
		}
	})

	t.Run("primary takes precedence over fallback", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-primary")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-token-123")

		key, envVar := ResolveAPIKey("anthropic")
		if key != "sk-ant-primary" {
			t.Errorf("key = %q, want sk-ant-primary (primary should win)", key)
		}

		if envVar != "ANTHROPIC_API_KEY" {
			t.Errorf("envVar = %q, want ANTHROPIC_API_KEY", envVar)
		}
	})

	t.Run("neither found", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		key, envVar := ResolveAPIKey("anthropic")
		if key != "" {
			t.Errorf("key = %q, want empty", key)
		}

		if envVar != "" {
			t.Errorf("envVar = %q, want empty", envVar)
		}
	})

	t.Run("provider with no alt env vars", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-openai-123")

		key, envVar := ResolveAPIKey("openai")
		if key != "sk-openai-123" {
			t.Errorf("key = %q, want sk-openai-123", key)
		}

		if envVar != "OPENAI_API_KEY" {
			t.Errorf("envVar = %q, want OPENAI_API_KEY", envVar)
		}
	})

	t.Run("ollama returns empty", func(t *testing.T) {
		key, envVar := ResolveAPIKey("ollama")
		if key != "" || envVar != "" {
			t.Errorf("ollama should return empty, got key=%q envVar=%q", key, envVar)
		}
	})

	t.Run("unknown provider returns empty", func(t *testing.T) {
		key, envVar := ResolveAPIKey("unknown-provider")
		if key != "" || envVar != "" {
			t.Errorf("unknown provider should return empty, got key=%q envVar=%q", key, envVar)
		}
	})
}

func TestProviderEnvVar(t *testing.T) {
	if got := ProviderEnvVar("anthropic"); got != "ANTHROPIC_API_KEY" {
		t.Errorf("got %q, want ANTHROPIC_API_KEY", got)
	}

	if got := ProviderEnvVar("openai"); got != "OPENAI_API_KEY" {
		t.Errorf("got %q, want OPENAI_API_KEY", got)
	}

	if got := ProviderEnvVar("ollama"); got != "" {
		t.Errorf("got %q, want empty string for ollama", got)
	}

	if got := ProviderEnvVar("custom_thing"); got != "CUSTOM_THING_API_KEY" {
		t.Errorf("got %q, want CUSTOM_THING_API_KEY", got)
	}
}

func TestProviderFromModelName(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{"anthropic claude", "claude-sonnet-4-6", "anthropic"},
		{"anthropic full", "claude-opus-4", "anthropic"},
		{"openai gpt", "gpt-4o", "openai"},
		{"openai o3", "o3-mini", "openai"},
		{"ollama model", "qwen3.5:9b", ""},
		{"unknown", "llama-3.2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderFromModelName(tt.model); got != tt.expected {
				t.Errorf("ProviderFromModelName(%q) = %q, want %q", tt.model, got, tt.expected)
			}
		})
	}
}

func TestLoadDotEnv(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		os.WriteFile(path, []byte("FOO=bar\nBAZ=qux\n"), 0o644)

		m := LoadDotEnv(path)
		if m["FOO"] != "bar" {
			t.Errorf("FOO = %q, want bar", m["FOO"])
		}

		if m["BAZ"] != "qux" {
			t.Errorf("BAZ = %q, want qux", m["BAZ"])
		}
	})

	t.Run("missing file", func(t *testing.T) {
		m := LoadDotEnv("/nonexistent/.env")
		if len(m) != 0 {
			t.Errorf("expected empty map, got %v", m)
		}
	})

	t.Run("comments and blanks", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		os.WriteFile(path, []byte("# comment\n\nKEY=val\n"), 0o644)

		m := LoadDotEnv(path)
		if len(m) != 1 || m["KEY"] != "val" {
			t.Errorf("expected {KEY:val}, got %v", m)
		}
	})

	t.Run("quoted values", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		os.WriteFile(path, []byte(`KEY="hello world"`+"\n"+`KEY2='single'`+"\n"), 0o644)

		m := LoadDotEnv(path)
		if m["KEY"] != "hello world" {
			t.Errorf("KEY = %q, want 'hello world'", m["KEY"])
		}

		if m["KEY2"] != "single" {
			t.Errorf("KEY2 = %q, want 'single'", m["KEY2"])
		}
	})
}

func TestValidateCustomEndpoint(t *testing.T) {
	t.Run("full validation success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"test-model"},{"id":"other-model"}]}`)
			case "/v1/chat/completions":
				fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		err := ValidateCustomEndpoint(srv.URL+"/v1", "test-model", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("inference probe returns empty choices", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch {
			case strings.HasSuffix(r.URL.Path, "/models"):
				fmt.Fprint(w, `{"data":[{"id":"other-model"}]}`)
			case strings.HasSuffix(r.URL.Path, "/chat/completions"):
				fmt.Fprint(w, `{"choices":[]}`)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer srv.Close()

		err := ValidateCustomEndpoint(srv.URL+"/v1", "nonexistent", "")
		if err == nil {
			t.Fatal("expected error for empty choices")
		}

		if !strings.Contains(err.Error(), "empty choices") {
			t.Errorf("error should mention 'empty choices', got: %v", err)
		}
	})

	t.Run("endpoint unreachable", func(t *testing.T) {
		err := ValidateCustomEndpoint("http://localhost:19999/v1", "test", "")
		if err == nil {
			t.Fatal("expected error for unreachable endpoint")
		}
	})

	t.Run("inference probe fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			case "/v1/chat/completions":
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"internal server error"}`)
			}
		}))
		defer srv.Close()

		err := ValidateCustomEndpoint(srv.URL+"/v1", "test-model", "")
		if err == nil {
			t.Fatal("expected error for failed inference")
		}
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
		{1073741824, "1.0 GB"},
		{4831838208, "4.5 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatBytes(tt.input)
			if got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestOllamaEndpoint(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "")

		got := ollamaEndpoint()
		if got != "http://localhost:11434" {
			t.Errorf("got %q, want http://localhost:11434", got)
		}
	})

	t.Run("custom host:port", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "myhost:9999")

		got := ollamaEndpoint()
		if got != "http://myhost:9999" {
			t.Errorf("got %q, want http://myhost:9999", got)
		}
	})

	t.Run("full URL", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "https://ollama.example.com/")

		got := ollamaEndpoint()
		if got != "https://ollama.example.com" {
			t.Errorf("got %q, want https://ollama.example.com", got)
		}
	})
}

func TestListOllamaModels_MockServer(t *testing.T) {
	t.Run("success with models", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/tags" {
				http.NotFound(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[
				{"name":"llama3.2:3b","size":2000000000,"modified_at":"2025-01-01T00:00:00Z"},
				{"name":"qwen2.5-coder:7b","size":4700000000,"modified_at":"2025-01-02T00:00:00Z"}
			]}`)
		}))
		defer srv.Close()

		t.Setenv("OLLAMA_HOST", strings.TrimPrefix(srv.URL, "http://"))

		models, err := ListOllamaModels()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(models) != 2 {
			t.Fatalf("got %d models, want 2", len(models))
		}

		if models[0].Name != "llama3.2:3b" {
			t.Errorf("models[0].Name = %q, want llama3.2:3b", models[0].Name)
		}

		if models[1].Size != 4700000000 {
			t.Errorf("models[1].Size = %d, want 4700000000", models[1].Size)
		}
	})

	t.Run("success with no models", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[]}`)
		}))
		defer srv.Close()

		t.Setenv("OLLAMA_HOST", strings.TrimPrefix(srv.URL, "http://"))

		models, err := ListOllamaModels()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(models) != 0 {
			t.Fatalf("got %d models, want 0", len(models))
		}
	})

	t.Run("server not running", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "localhost:19999")

		_, err := ListOllamaModels()
		if err == nil {
			t.Fatal("expected error when server is not running")
		}

		if !strings.Contains(err.Error(), "not running") {
			t.Errorf("error should mention 'not running', got: %v", err)
		}
	})
}

func TestAutoConfigOllamaModelNames(t *testing.T) {
	t.Run("demotes credential-requiring cloud aliases behind local chat models", func(t *testing.T) {
		models := []OllamaModel{
			{Name: "deepseek-v4-pro:cloud", Size: 9_000_000_000},
			{Name: "llama3.2:1b", Size: 1_300_000_000},
			{Name: "nomic-embed-text:latest", Size: 274_000_000},
			{Name: "qwen3.5:9b", Size: 5_400_000_000},
			{Name: "", Size: 1},
		}

		got := AutoConfigOllamaModelNames(models)
		want := []string{"llama3.2:1b", "qwen3.5:9b", "deepseek-v4-pro:cloud", "nomic-embed-text"}
		if len(got) != len(want) {
			t.Fatalf("len(got) = %d, want %d (got %v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("preserves cloud-only order when no local chat models exist", func(t *testing.T) {
		models := []OllamaModel{
			{Name: "deepseek-v4-pro:cloud", Size: 9_000_000_000},
			{Name: "qwen3-coder:cloud", Size: 7_000_000_000},
		}

		got := AutoConfigOllamaModelNames(models)
		want := []string{"deepseek-v4-pro:cloud", "qwen3-coder:cloud"}
		if len(got) != len(want) {
			t.Fatalf("len(got) = %d, want %d (got %v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("trims whitespace and only removes a terminal latest tag", func(t *testing.T) {
		models := []OllamaModel{
			{Name: "   custom/qa-vllm/qwen36-fast:latest   ", Size: 1},
			{Name: "   ", Size: 2},
		}

		got := AutoConfigOllamaModelNames(models)
		want := []string{"custom/qa-vllm/qwen36-fast"}
		if len(got) != len(want) {
			t.Fatalf("len(got) = %d, want %d (got %v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
			}
		}
	})
}

func TestPullOllamaModel_MockServer(t *testing.T) {
	t.Run("successful pull", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/pull" && r.Method == http.MethodPost {
				var req struct {
					Name   string `json:"name"`
					Stream bool   `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				if req.Name != "llama3.2:3b" {
					http.Error(w, "unexpected model", http.StatusBadRequest)
					return
				}

				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintln(w, `{"status":"pulling manifest"}`)
				fmt.Fprintln(w, `{"status":"pulling abc123","total":1000,"completed":500}`)
				fmt.Fprintln(w, `{"status":"pulling abc123","total":1000,"completed":1000}`)
				fmt.Fprintln(w, `{"status":"success"}`)

				return
			}
			// Health check endpoint
			if r.URL.Path == "/" {
				w.WriteHeader(http.StatusOK)
				return
			}

			http.NotFound(w, r)
		}))
		defer srv.Close()

		t.Setenv("OLLAMA_HOST", strings.TrimPrefix(srv.URL, "http://"))

		err := PullOllamaModel("llama3.2:3b")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pull error from server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/pull" {
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintln(w, `{"status":"pulling manifest"}`)
				fmt.Fprintln(w, `{"error":"model not found"}`)

				return
			}

			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		t.Setenv("OLLAMA_HOST", strings.TrimPrefix(srv.URL, "http://"))

		err := PullOllamaModel("nonexistent:latest")
		if err == nil {
			t.Fatal("expected error for nonexistent model")
		}

		if !strings.Contains(err.Error(), "model not found") {
			t.Errorf("error should contain 'model not found', got: %v", err)
		}
	})

	t.Run("server not running", func(t *testing.T) {
		t.Setenv("OLLAMA_HOST", "localhost:19999")

		err := PullOllamaModel("llama3.2:3b")
		if err == nil {
			t.Fatal("expected error when server is not running")
		}
	})
}

func TestReorderModelList(t *testing.T) {
	entries := []ModelEntry{
		{ModelName: "anthropic/*"},
		{ModelName: "claude-sonnet-4-6"},
		{ModelName: "claude-opus-4-7"},
		{ModelName: "openai/*"},
		{ModelName: "gpt-5.5"},
	}

	t.Run("pulls a single model to the head", func(t *testing.T) {
		got, already, err := reorderModelList(entries, []string{"claude-opus-4-7"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		if already {
			t.Fatalf("expected alreadyAtHead = false")
		}
		want := []string{"claude-opus-4-7", "anthropic/*", "claude-sonnet-4-6", "openai/*", "gpt-5.5"}
		assertOrder(t, got, want)
	})

	t.Run("pulls multiple models to the head in the given order", func(t *testing.T) {
		got, _, err := reorderModelList(entries, []string{"gpt-5.5", "claude-opus-4-7"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		want := []string{"gpt-5.5", "claude-opus-4-7", "anthropic/*", "claude-sonnet-4-6", "openai/*"}
		assertOrder(t, got, want)
	})

	t.Run("preserves relative order of unselected entries", func(t *testing.T) {
		got, _, err := reorderModelList(entries, []string{"claude-opus-4-7"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		// anthropic/*, claude-sonnet-4-6, openai/*, gpt-5.5 should stay in
		// that relative order behind the promoted entry.
		want := []string{"claude-opus-4-7", "anthropic/*", "claude-sonnet-4-6", "openai/*", "gpt-5.5"}
		assertOrder(t, got, want)
	})

	t.Run("flags already-at-head as no-op", func(t *testing.T) {
		_, already, err := reorderModelList(entries, []string{"anthropic/*"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		if !already {
			t.Fatalf("expected alreadyAtHead = true when first entry is requested")
		}
	})

	t.Run("flags multi-arg already-at-head as no-op", func(t *testing.T) {
		_, already, err := reorderModelList(entries, []string{"anthropic/*", "claude-sonnet-4-6"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		if !already {
			t.Fatalf("expected alreadyAtHead = true when first two requested entries already lead")
		}
	})

	t.Run("errors on missing names with full list", func(t *testing.T) {
		_, _, err := reorderModelList(entries, []string{"claude-opus-4-7", "typo-1", "typo-2"})
		if err == nil {
			t.Fatal("expected error for missing names")
		}
		if !strings.Contains(err.Error(), "typo-1") || !strings.Contains(err.Error(), "typo-2") {
			t.Fatalf("error should list every missing name, got: %v", err)
		}
	})

	t.Run("errors on duplicate args", func(t *testing.T) {
		_, _, err := reorderModelList(entries, []string{"claude-opus-4-7", "claude-opus-4-7"})
		if err == nil {
			t.Fatal("expected error for duplicate names")
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("error should mention duplicate, got: %v", err)
		}
	})

	t.Run("preserves ModelEntry contents not just names", func(t *testing.T) {
		entriesWithParams := []ModelEntry{
			{ModelName: "a", LiteLLMParams: LiteLLMParams{Model: "openai/a", APIKey: "key-a"}},
			{ModelName: "b", LiteLLMParams: LiteLLMParams{Model: "openai/b", APIKey: "key-b"}},
		}
		got, _, err := reorderModelList(entriesWithParams, []string{"b"})
		if err != nil {
			t.Fatalf("reorderModelList: %v", err)
		}
		if got[0].LiteLLMParams.APIKey != "key-b" {
			t.Fatalf("promoted entry lost its params: got %+v", got[0])
		}
		if got[1].LiteLLMParams.APIKey != "key-a" {
			t.Fatalf("trailing entry lost its params: got %+v", got[1])
		}
	})
}

func assertOrder(t *testing.T, got []ModelEntry, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), modelNames(got))
	}
	for i, w := range want {
		if got[i].ModelName != w {
			t.Fatalf("entry[%d] = %q, want %q (full order: %v)", i, got[i].ModelName, w, modelNames(got))
		}
	}
}

func modelNames(entries []ModelEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ModelName
	}
	return out
}
