package inference

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestIsInferenceConfigured_Empty(t *testing.T) {
	// Fresh config with a temp dir — no inference.json exists.
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}
	if IsInferenceConfigured(cfg) {
		t.Error("expected false for fresh config, got true")
	}
}

func TestIsInferenceConfigured_NilConfig(t *testing.T) {
	if IsInferenceConfigured(nil) {
		t.Error("expected false for nil config, got true")
	}
}

func TestIsInferenceConfigured_Set(t *testing.T) {
	dir := t.TempDir()
	// Write a non-empty inference.json.
	path := filepath.Join(dir, inferenceConfigFile)
	if err := os.WriteFile(path, []byte(`{"endpoint":"http://127.0.0.1:11434"}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg := &config.Config{ConfigDir: dir}
	if !IsInferenceConfigured(cfg) {
		t.Error("expected true when inference.json exists, got false")
	}
}

func TestIsInferenceConfigured_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Write an empty inference.json — should be treated as not configured.
	path := filepath.Join(dir, inferenceConfigFile)
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg := &config.Config{ConfigDir: dir}
	if IsInferenceConfigured(cfg) {
		t.Error("expected false for empty inference.json, got true")
	}
}

func TestFormatEndpointDisplay(t *testing.T) {
	endpoints := []EndpointInfo{
		{
			Host:       "127.0.0.1",
			Port:       11434,
			ServerType: "ollama",
			Models: []ModelInfo{
				{ID: "llama-3.2-3b", OwnedBy: "meta"},
				{ID: "qwen-2.5-coder", OwnedBy: "alibaba"},
			},
			Healthy: true,
		},
		{
			Host:       "127.0.0.1",
			Port:       8080,
			ServerType: "llama-server",
			Models:     []ModelInfo{{ID: "phi-3-mini", OwnedBy: ""}},
			Healthy:    true,
		},
	}

	output := FormatEndpointDisplay(endpoints)

	// Check key elements are present.
	checks := []string{
		"Discovered inference endpoints:",
		"http://127.0.0.1:11434",
		"ollama",
		"llama-3.2-3b",
		"qwen-2.5-coder",
		"http://127.0.0.1:8080",
		"llama-server",
		"phi-3-mini",
		"by unknown", // empty OwnedBy falls back to "unknown"
		"healthy",
	}
	for _, s := range checks {
		if !strings.Contains(output, s) {
			t.Errorf("output missing %q:\n%s", s, output)
		}
	}
}

func TestFormatEndpointDisplay_NoModels(t *testing.T) {
	endpoints := []EndpointInfo{
		{
			Host:       "127.0.0.1",
			Port:       8000,
			ServerType: "vllm",
			Models:     nil,
			Healthy:    false,
		},
	}
	output := FormatEndpointDisplay(endpoints)
	if !strings.Contains(output, "no models loaded") {
		t.Errorf("expected 'no models loaded' in output:\n%s", output)
	}
	if !strings.Contains(output, "unhealthy") {
		t.Errorf("expected 'unhealthy' in output:\n%s", output)
	}
}
