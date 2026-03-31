package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeEndpoint_NotRunning(t *testing.T) {
	// Probe a port where nothing is listening — should fail gracefully.
	_, err := ProbeEndpoint("127.0.0.1", 19999)
	if err == nil {
		t.Fatal("expected error probing non-running port, got nil")
	}
	if !strings.Contains(err.Error(), "no inference server detected") {
		t.Fatalf("unexpected error message: %s", err)
	}
}

func TestScanLocalEndpoints_NoneFound(t *testing.T) {
	// When nothing is running on any common port, should return empty list.
	// This test is safe because CI/test environments rarely run inference servers.
	endpoints, err := ScanLocalEndpoints()
	if err != nil {
		t.Fatalf("ScanLocalEndpoints returned error: %v", err)
	}
	// We can't guarantee nothing is running, so just check it doesn't crash.
	_ = endpoints
}

func TestDetectServerType(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[string]int // path -> status code
		want     string
	}{
		{
			name:     "ollama",
			handlers: map[string]int{"/api/tags": 200, "/v1/models": 200},
			want:     "ollama",
		},
		{
			name:     "llama-server",
			handlers: map[string]int{"/health": 200, "/v1/models": 200},
			want:     "llama-server",
		},
		{
			name:     "openai-compat",
			handlers: map[string]int{"/v1/models": 200},
			want:     "openai-compat",
		},
		{
			name:     "nothing",
			handlers: map[string]int{},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code, ok := tt.handlers[r.URL.Path]; ok {
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()

			got := DetectServerType(context.Background(), srv.URL)
			if got != tt.want {
				t.Errorf("DetectServerType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseModelsResponse(t *testing.T) {
	payload := modelsResponse{
		Data: []ModelInfo{
			{ID: "llama-3.2-3b", OwnedBy: "meta", Created: 1700000000},
			{ID: "qwen-2.5-coder", OwnedBy: "alibaba", Created: 1700000001},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}

	models, err := ParseModelsResponse(raw)
	if err != nil {
		t.Fatalf("ParseModelsResponse returned error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "llama-3.2-3b" {
		t.Errorf("models[0].ID = %q, want %q", models[0].ID, "llama-3.2-3b")
	}
	if models[1].OwnedBy != "alibaba" {
		t.Errorf("models[1].OwnedBy = %q, want %q", models[1].OwnedBy, "alibaba")
	}
}

func TestParseModelsResponse_Invalid(t *testing.T) {
	_, err := ParseModelsResponse([]byte(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseModelsResponse_Empty(t *testing.T) {
	models, err := ParseModelsResponse([]byte(`{"data":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected 0 models, got %d", len(models))
	}
}
