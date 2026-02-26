package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProviderEnvKey(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		output   string
		want     string
		wantErr  bool
	}{
		{
			name:     "anthropic",
			provider: "anthropic",
			output:   "ANTHROPIC_API_KEY\n",
			want:     "ANTHROPIC_API_KEY",
		},
		{
			name:     "zai with trailing whitespace",
			provider: "zai",
			output:   "  ZHIPU_API_KEY  \n",
			want:     "ZHIPU_API_KEY",
		},
		{
			name:     "empty output means unknown provider",
			provider: "nosuchprovider",
			output:   "",
			wantErr:  true,
		},
		{
			name:     "whitespace-only output means unknown provider",
			provider: "nosuchprovider",
			output:   "  \n  ",
			wantErr:  true,
		},
		{
			name:     "openai",
			provider: "openai",
			output:   "OPENAI_API_KEY",
			want:     "OPENAI_API_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProviderEnvKey(tt.provider, tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseAvailableProviders(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []ProviderInfo
	}{
		{
			name:   "empty output",
			output: "",
			want:   nil,
		},
		{
			name:   "whitespace only",
			output: "  \n  ",
			want:   nil,
		},
		{
			name:   "single provider",
			output: "anthropic\tAnthropic\tANTHROPIC_API_KEY\n",
			want: []ProviderInfo{
				{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
			},
		},
		{
			name: "multiple providers sorted",
			output: "anthropic\tAnthropic\tANTHROPIC_API_KEY\n" +
				"openai\tOpenAI\tOPENAI_API_KEY\n" +
				"zai\tZ.AI\tZHIPU_API_KEY\n",
			want: []ProviderInfo{
				{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
				{ID: "openai", Name: "OpenAI", EnvVar: "OPENAI_API_KEY"},
				{ID: "zai", Name: "Z.AI", EnvVar: "ZHIPU_API_KEY"},
			},
		},
		{
			name:   "malformed line skipped",
			output: "badline\n" + "anthropic\tAnthropic\tANTHROPIC_API_KEY\n",
			want: []ProviderInfo{
				{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
			},
		},
		{
			name:   "tab in name preserved",
			output: "deepseek\tDeepSeek\tDEEPSEEK_API_KEY\n",
			want: []ProviderInfo{
				{ID: "deepseek", Name: "DeepSeek", EnvVar: "DEEPSEEK_API_KEY"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAvailableProviders(tt.output)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d providers, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("provider[%d]: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildProviderStatus(t *testing.T) {
	t.Run("basic status with cloud provider key set", func(t *testing.T) {
		available := []ProviderInfo{
			{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
			{ID: "openai", Name: "OpenAI", EnvVar: "OPENAI_API_KEY"},
			{ID: "zai", Name: "Z.AI", EnvVar: "ZHIPU_API_KEY"},
		}

		llmsJSON := []byte(`{
			"providers": {
				"ollama": {"enabled": true},
				"anthropic": {"enabled": true},
				"openai": {"enabled": false},
				"zai": {"enabled": true}
			}
		}`)

		// Secret .data values are base64 in real k8s, but our code just checks
		// if the key exists and the value is non-empty (the cross-reference uses
		// the raw string from the JSON — k8s returns base64 in .data).
		secretJSON := []byte(`{
			"data": {
				"ANTHROPIC_API_KEY": "c2stYW50LXh4eA==",
				"OPENAI_API_KEY": "",
				"ZHIPU_API_KEY": "ZWU1NjM5Nzk="
			}
		}`)

		status, err := buildProviderStatus(available, llmsJSON, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Ollama: enabled, always has key
		if s := status["ollama"]; !s.Enabled || !s.HasAPIKey {
			t.Errorf("ollama: got enabled=%t hasKey=%t, want enabled=true hasKey=true", s.Enabled, s.HasAPIKey)
		}

		// Anthropic: enabled, key set
		if s := status["anthropic"]; !s.Enabled || !s.HasAPIKey || s.EnvVar != "ANTHROPIC_API_KEY" {
			t.Errorf("anthropic: got %+v, want enabled=true hasKey=true envVar=ANTHROPIC_API_KEY", s)
		}

		// OpenAI: disabled, key empty
		if s := status["openai"]; s.Enabled || s.HasAPIKey || s.EnvVar != "OPENAI_API_KEY" {
			t.Errorf("openai: got %+v, want enabled=false hasKey=false envVar=OPENAI_API_KEY", s)
		}

		// Z.AI: enabled, key set
		if s := status["zai"]; !s.Enabled || !s.HasAPIKey || s.EnvVar != "ZHIPU_API_KEY" {
			t.Errorf("zai: got %+v, want enabled=true hasKey=true envVar=ZHIPU_API_KEY", s)
		}
	})

	t.Run("ollama injected when missing from configmap", func(t *testing.T) {
		available := []ProviderInfo{
			{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
		}
		llmsJSON := []byte(`{"providers":{"anthropic":{"enabled":false}}}`)
		secretJSON := []byte(`{"data":{}}`)

		status, err := buildProviderStatus(available, llmsJSON, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if s, ok := status["ollama"]; !ok || !s.Enabled || !s.HasAPIKey {
			t.Errorf("ollama should be injected as enabled with key; got %+v, ok=%t", s, ok)
		}
	})

	t.Run("provider in configmap but not in available list gets no env var", func(t *testing.T) {
		available := []ProviderInfo{} // no providers discovered
		llmsJSON := []byte(`{"providers":{"mystery":{"enabled":true}}}`)
		secretJSON := []byte(`{"data":{}}`)

		status, err := buildProviderStatus(available, llmsJSON, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if s := status["mystery"]; !s.Enabled || s.EnvVar != "" {
			t.Errorf("mystery: got %+v, want enabled=true envVar=''", s)
		}
	})

	t.Run("empty providers section", func(t *testing.T) {
		llmsJSON := []byte(`{}`)
		secretJSON := []byte(`{"data":{}}`)

		status, err := buildProviderStatus(nil, llmsJSON, secretJSON)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Only ollama (injected)
		if len(status) != 1 {
			t.Errorf("expected 1 provider (ollama), got %d", len(status))
		}
	})

	t.Run("invalid llms json", func(t *testing.T) {
		_, err := buildProviderStatus(nil, []byte(`not json`), []byte(`{"data":{}}`))
		if err == nil {
			t.Fatal("expected error for invalid llms.json")
		}
	})

	t.Run("invalid secret json", func(t *testing.T) {
		_, err := buildProviderStatus(nil, []byte(`{}`), []byte(`not json`))
		if err == nil {
			t.Fatal("expected error for invalid secret JSON")
		}
	})
}

func TestPatchLLMsJSON(t *testing.T) {
	t.Run("enable existing disabled provider", func(t *testing.T) {
		input := []byte(`{"providers":{"anthropic":{"enabled":false},"ollama":{"enabled":true}}}`)

		got, err := patchLLMsJSON(input, "anthropic")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}

		providers := result["providers"].(map[string]interface{})
		anthropic := providers["anthropic"].(map[string]interface{})
		if anthropic["enabled"] != true {
			t.Errorf("anthropic.enabled = %v, want true", anthropic["enabled"])
		}

		// Ollama should be untouched
		ollama := providers["ollama"].(map[string]interface{})
		if ollama["enabled"] != true {
			t.Errorf("ollama.enabled = %v, want true (untouched)", ollama["enabled"])
		}
	})

	t.Run("enable new provider not in config", func(t *testing.T) {
		input := []byte(`{"providers":{"ollama":{"enabled":true}}}`)

		got, err := patchLLMsJSON(input, "zai")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}

		providers := result["providers"].(map[string]interface{})
		zai := providers["zai"].(map[string]interface{})
		if zai["enabled"] != true {
			t.Errorf("zai.enabled = %v, want true", zai["enabled"])
		}
	})

	t.Run("create providers section if missing", func(t *testing.T) {
		input := []byte(`{"version":"1.0"}`)

		got, err := patchLLMsJSON(input, "deepseek")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}

		// version preserved
		if result["version"] != "1.0" {
			t.Errorf("version lost: got %v", result["version"])
		}

		providers := result["providers"].(map[string]interface{})
		ds := providers["deepseek"].(map[string]interface{})
		if ds["enabled"] != true {
			t.Errorf("deepseek.enabled = %v, want true", ds["enabled"])
		}
	})

	t.Run("preserves other provider fields", func(t *testing.T) {
		input := []byte(`{"providers":{"anthropic":{"enabled":false,"customField":"keep"}}}`)

		got, err := patchLLMsJSON(input, "anthropic")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}

		providers := result["providers"].(map[string]interface{})
		anthropic := providers["anthropic"].(map[string]interface{})
		if anthropic["enabled"] != true {
			t.Errorf("enabled = %v, want true", anthropic["enabled"])
		}
		if anthropic["customField"] != "keep" {
			t.Errorf("customField = %v, want 'keep'", anthropic["customField"])
		}
	})

	t.Run("invalid json input", func(t *testing.T) {
		_, err := patchLLMsJSON([]byte(`{bad`), "anthropic")
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("idempotent enable", func(t *testing.T) {
		input := []byte(`{"providers":{"anthropic":{"enabled":true}}}`)

		got, err := patchLLMsJSON(input, "anthropic")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}

		providers := result["providers"].(map[string]interface{})
		anthropic := providers["anthropic"].(map[string]interface{})
		if anthropic["enabled"] != true {
			t.Errorf("enabled = %v, want true", anthropic["enabled"])
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

func TestPullOllamaModel_MockServer(t *testing.T) {
	t.Run("successful pull", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/pull" && r.Method == "POST" {
				var req struct {
					Name   string `json:"name"`
					Stream bool   `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if req.Name != "llama3.2:3b" {
					http.Error(w, "unexpected model", 400)
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
				w.WriteHeader(200)
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
			w.WriteHeader(200)
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
