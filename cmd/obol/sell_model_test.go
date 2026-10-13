package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func TestCheckSellHTTPModel(t *testing.T) {
	configured := []string{"qwen3.5:9b", "paid/aeon", "paid/*"}
	listErr := errors.New("cluster not running")

	cases := []struct {
		name, upstream, model string
		configured            []string
		listErr               error
		want, wantErr         string
	}{
		{name: "litellm requires model", upstream: "litellm", wantErr: "requires --model"},
		{name: "litellm blank model", upstream: "LiteLLM", model: "  ", wantErr: "requires --model"},
		{name: "litellm configured", upstream: "litellm", model: "qwen3.5:9b", configured: configured, want: "qwen3.5:9b"},
		{name: "litellm purchased model", upstream: "litellm", model: "paid/aeon", configured: configured, want: "paid/aeon"},
		{name: "litellm unknown model", upstream: "litellm", model: "gpt-5", configured: configured, wantErr: "not configured in LiteLLM"},
		{name: "litellm wildcard", upstream: "litellm", model: "paid/*", configured: configured, wantErr: "not a wildcard"},
		{name: "litellm comma list", upstream: "litellm", model: "a,b", wantErr: "not a wildcard or list"},
		{name: "litellm list unreadable", upstream: "litellm", model: "anything", listErr: listErr, want: "anything"},
		{name: "other upstream rejects model", upstream: "ollama", model: "qwen3.5:9b", wantErr: "only applies to --upstream litellm"},
		{name: "other upstream no model", upstream: "httpbin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkSellHTTPModel(tc.upstream, tc.model, tc.configured, tc.listErr)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSellHTTP_ModelFlag(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	requireFlags(t, flagMap(findSubcommand(t, cmd, "http")), "model")
	requireFlags(t, flagMap(findSubcommand(t, cmd, "update")), "model")
}

func TestCheckFromJSONLiteLLMModel(t *testing.T) {
	spec := map[string]any{
		"type":     "http",
		"upstream": map[string]any{"service": "litellm", "namespace": "llm", "port": 4000},
	}
	if err := checkFromJSONLiteLLMModel(spec); err == nil {
		t.Fatal("http offer on litellm without spec.model.name must be rejected")
	}
	spec["model"] = map[string]any{"name": "qwen3.5:9b", "runtime": "litellm"}
	if err := checkFromJSONLiteLLMModel(spec); err != nil {
		t.Fatalf("pinned model rejected: %v", err)
	}
	// Inference offers carry their own model requirement; other upstreams none.
	if err := checkFromJSONLiteLLMModel(map[string]any{"type": "inference", "upstream": map[string]any{"service": "litellm"}}); err != nil {
		t.Fatalf("inference spec: %v", err)
	}
	if err := checkFromJSONLiteLLMModel(map[string]any{"type": "http", "upstream": map[string]any{"service": "httpbin"}}); err != nil {
		t.Fatalf("non-litellm spec: %v", err)
	}
}

func TestSellUpdateModelPatch(t *testing.T) {
	llm := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type: "http", Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm"},
	}}
	patch, err := sellUpdateModelPatch(llm, "qwen3.5:27b", []string{"qwen3.5:9b", "qwen3.5:27b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if patch["name"] != "qwen3.5:27b" || patch["runtime"] != "litellm" {
		t.Fatalf("patch = %v", patch)
	}
	if _, err := sellUpdateModelPatch(llm, "gpt-5", []string{"qwen3.5:9b"}, nil); err == nil {
		t.Fatal("unknown model must be rejected")
	}
	if _, err := sellUpdateModelPatch(llm, " ", nil, nil); err == nil {
		t.Fatal("empty model must be rejected")
	}

	for _, offer := range []*monetizeapi.ServiceOffer{
		{Spec: monetizeapi.ServiceOfferSpec{Type: "http", Upstream: monetizeapi.ServiceOfferUpstream{Service: "ollama"}}},
		{Spec: monetizeapi.ServiceOfferSpec{Type: "agent"}},
		{Spec: monetizeapi.ServiceOfferSpec{Type: "inference", Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm"}}},
	} {
		if _, err := sellUpdateModelPatch(offer, "qwen3.5:9b", nil, nil); err == nil {
			t.Fatalf("--model must be refused for %s/%s offers", offer.Spec.Type, offer.Spec.Upstream.Service)
		}
	}
}
