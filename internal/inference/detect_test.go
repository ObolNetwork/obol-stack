package inference

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeEndpoint_NotRunning(t *testing.T) {
	// Allocate a free port dynamically, then close it so nothing is listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	_, err = ProbeEndpoint("127.0.0.1", port)
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

// TestProbeEndpointContext_HappyPath uses httptest to serve /v1/models
// and verifies ProbeEndpointContext returns correct EndpointInfo.
func TestProbeEndpointContext_HappyPath(t *testing.T) {
	mockModels := modelsResponse{
		Data: []ModelInfo{
			{ID: "test-model-7b", OwnedBy: "test-org", Created: 1700000000},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mockModels)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Extract host and port from the test server.
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}
	var port int
	if _, err := net.LookupPort("tcp", portStr); err != nil {
		// portStr is numeric
		p, _ := net.LookupPort("tcp", portStr)
		port = p
	}
	// Parse port as int directly.
	for i, c := range portStr {
		_ = i
		_ = c
	}
	port = 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	ep, err := ProbeEndpointContext(context.Background(), host, port)
	if err != nil {
		t.Fatalf("ProbeEndpointContext returned error: %v", err)
	}
	if ep.ServerType != "openai-compat" {
		t.Errorf("ServerType = %q, want %q", ep.ServerType, "openai-compat")
	}
	if !ep.Healthy {
		t.Error("expected Healthy = true")
	}
	if len(ep.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(ep.Models))
	}
	if ep.Models[0].ID != "test-model-7b" {
		t.Errorf("model ID = %q, want %q", ep.Models[0].ID, "test-model-7b")
	}
}

// TestProbeEndpointContext_MalformedJSON tests that malformed JSON from /v1/models
// is handled gracefully.
func TestProbeEndpointContext_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{not valid json!!!`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	_, err := ProbeEndpointContext(context.Background(), host, port)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decoding models response") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestProbeEndpointContext_ContextCancelled verifies that a cancelled context
// causes ProbeEndpointContext to return promptly.
func TestProbeEndpointContext_ContextCancelled(t *testing.T) {
	// Use a server that delays responses so context cancellation takes effect.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	_, err := ProbeEndpointContext(ctx, host, port)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestExtraPortsFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []portProbe
	}{
		{name: "empty", env: "", want: nil},
		{name: "single port no label", env: "9000", want: []portProbe{{Port: 9000, ServerType: "openai-compat"}}},
		{name: "single port with label", env: "9000:vllm", want: []portProbe{{Port: 9000, ServerType: "vllm"}}},
		{
			name: "multiple",
			env:  "9000:vllm,5001:custom,7000",
			want: []portProbe{
				{Port: 9000, ServerType: "vllm"},
				{Port: 5001, ServerType: "custom"},
				{Port: 7000, ServerType: "openai-compat"},
			},
		},
		{name: "ignores garbage", env: "abc,9000:vllm,99999,-1,", want: []portProbe{{Port: 9000, ServerType: "vllm"}}},
		{name: "trims whitespace", env: " 9000 : vllm , 5001 ", want: []portProbe{
			{Port: 9000, ServerType: "vllm"},
			{Port: 5001, ServerType: "openai-compat"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LocalDiscoveryPortsEnv, tc.env)
			got := extraPortsFromEnv()
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries, want %d (%+v vs %+v)", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResolvedProbePorts_DedupsAndKeepsDefaultLabels(t *testing.T) {
	// Env entry whose port collides with a default (8080) must not
	// overwrite the default label — the default wins for predictable
	// detection priority.
	t.Setenv(LocalDiscoveryPortsEnv, "8080:overridden,9999:custom")

	got := resolvedProbePorts()

	// First N entries should match defaults exactly.
	for i, def := range commonPorts {
		if got[i] != def {
			t.Errorf("got[%d] = %+v, want default %+v", i, got[i], def)
		}
	}
	// Tail should include the unique extra.
	tail := got[len(commonPorts):]
	if len(tail) != 1 || tail[0] != (portProbe{Port: 9999, ServerType: "custom"}) {
		t.Errorf("tail = %+v, want [{9999 custom}]", tail)
	}
}
