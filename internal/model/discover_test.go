package model

import (
	"context"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/inference"
)

func TestShouldRegisterEndpoint(t *testing.T) {
	cases := []struct {
		name string
		ep   inference.EndpointInfo
		want bool
	}{
		{
			name: "vllm with one model",
			ep: inference.EndpointInfo{
				Host: "127.0.0.1", Port: 8000, ServerType: "vllm",
				Models: []inference.ModelInfo{{ID: "Qwen/Qwen2.5-7B-Instruct"}},
			},
			want: true,
		},
		{
			name: "llama-server with one model",
			ep: inference.EndpointInfo{
				Host: "127.0.0.1", Port: 8080, ServerType: "llama-server",
				Models: []inference.ModelInfo{{ID: "ggml-model"}},
			},
			want: true,
		},
		{
			name: "litellm port skipped",
			ep: inference.EndpointInfo{
				Host: "127.0.0.1", Port: 4000, ServerType: "litellm",
				Models: []inference.ModelInfo{{ID: "anthropic/claude"}},
			},
			want: false,
		},
		{
			name: "ollama skipped (handled by ListOllamaModels path)",
			ep: inference.EndpointInfo{
				Host: "127.0.0.1", Port: 11434, ServerType: "ollama",
				Models: []inference.ModelInfo{{ID: "qwen3.5:9b"}},
			},
			want: false,
		},
		{
			name: "no models loaded",
			ep: inference.EndpointInfo{
				Host: "127.0.0.1", Port: 8000, ServerType: "vllm",
				Models: nil,
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRegisterEndpoint(tc.ep)
			if got != tc.want {
				t.Errorf("shouldRegisterEndpoint(%+v) = %v, want %v", tc.ep, got, tc.want)
			}
		})
	}
}

func TestBuildDiscoveredProvider_TranslatesHost(t *testing.T) {
	ep := inference.EndpointInfo{
		Host: "127.0.0.1", Port: 8000, ServerType: "vllm",
		Models: []inference.ModelInfo{
			{ID: "meta-llama/Llama-3.1-8B-Instruct"},
			{ID: "Qwen/Qwen2.5-7B"},
		},
	}

	got := buildDiscoveredProvider(ep)

	if got.Label != "vllm@8000" {
		t.Errorf("Label = %q, want vllm@8000", got.Label)
	}
	if got.HostEndpoint != "http://127.0.0.1:8000" {
		t.Errorf("HostEndpoint = %q, want http://127.0.0.1:8000", got.HostEndpoint)
	}
	if got.ClusterEndpoint != "http://host.k3d.internal:8000" {
		t.Errorf("ClusterEndpoint = %q, want http://host.k3d.internal:8000", got.ClusterEndpoint)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(got.Entries))
	}

	// Round-trip contract: ModelName is the bare model ID, model uses
	// openai/<id>, api_base is the cluster URL. See AddCustomEndpoint
	// doc comment in model.go for why this is invariant.
	first := got.Entries[0]
	if first.ModelName != "meta-llama/Llama-3.1-8B-Instruct" {
		t.Errorf("ModelName = %q, want meta-llama/Llama-3.1-8B-Instruct", first.ModelName)
	}
	if first.LiteLLMParams.Model != "openai/meta-llama/Llama-3.1-8B-Instruct" {
		t.Errorf("LiteLLMParams.Model = %q, want openai/meta-llama/Llama-3.1-8B-Instruct", first.LiteLLMParams.Model)
	}
	// /v1 suffix required — LiteLLM's OpenAI provider does not append it.
	if first.LiteLLMParams.APIBase != "http://host.k3d.internal:8000/v1" {
		t.Errorf("APIBase = %q, want http://host.k3d.internal:8000/v1", first.LiteLLMParams.APIBase)
	}
	// buildCustomEndpointEntry sets api_key to "none" when no key is given.
	if first.LiteLLMParams.APIKey != "none" {
		t.Errorf("APIKey = %q, want \"none\"", first.LiteLLMParams.APIKey)
	}
}

func TestBuildDiscoveredProvider_SkipsEmptyModelIDs(t *testing.T) {
	ep := inference.EndpointInfo{
		Host: "127.0.0.1", Port: 8000, ServerType: "vllm",
		Models: []inference.ModelInfo{
			{ID: ""},
			{ID: "real-model"},
			{ID: ""},
		},
	}
	got := buildDiscoveredProvider(ep)
	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1 (empty IDs filtered)", len(got.Entries))
	}
	if got.Entries[0].ModelName != "real-model" {
		t.Errorf("ModelName = %q, want real-model", got.Entries[0].ModelName)
	}
}

func TestDiscoverLocalProviders_DisableEnv(t *testing.T) {
	t.Setenv(DiscoverDisabledEnv, "1")
	got, err := DiscoverLocalProviders(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %d providers, want nil (disabled)", len(got))
	}
}

func TestDiscoverLocalProviders_DisableEnvTrue(t *testing.T) {
	t.Setenv(DiscoverDisabledEnv, "true")
	got, err := DiscoverLocalProviders(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %d providers, want nil (disabled)", len(got))
	}
}
