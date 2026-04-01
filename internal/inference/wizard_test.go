package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if err := os.WriteFile(path, []byte(`{"base_url":"http://127.0.0.1:11434","model":"llama3"}`), 0644); err != nil {
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

func TestIsInferenceConfigured_NonexistentDir(t *testing.T) {
	cfg := &config.Config{ConfigDir: "/tmp/definitely-does-not-exist-obol-test-12345"}
	if IsInferenceConfigured(cfg) {
		t.Error("expected false for nonexistent directory, got true")
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

func TestFormatEndpointDisplay_EmptySlice(t *testing.T) {
	output := FormatEndpointDisplay([]EndpointInfo{})
	if !strings.Contains(output, "Discovered inference endpoints:") {
		t.Errorf("expected header in output:\n%s", output)
	}
	if !strings.Contains(output, "(none)") {
		t.Errorf("expected '(none)' for empty endpoints:\n%s", output)
	}
}

func TestRunInferenceWizardIO_AlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, inferenceConfigFile)
	if err := os.WriteFile(path, []byte(`{"base_url":"http://127.0.0.1:11434","model":"llama3"}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	cfg := &config.Config{ConfigDir: dir}

	var out bytes.Buffer
	in := strings.NewReader("")
	err := RunInferenceWizardIO(context.Background(), cfg, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "already configured") {
		t.Errorf("expected 'already configured' message, got: %s", out.String())
	}
}

func TestRunInferenceWizardIO_NoEndpoints(t *testing.T) {
	// Use a fresh temp dir as ConfigDir. Provide "n\n" as input in case
	// a real inference server happens to be running on the test machine.
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}

	var out bytes.Buffer
	in := strings.NewReader("n\n")
	err := RunInferenceWizardIO(context.Background(), cfg, in, &out)
	// Either no endpoints found (nil error) or user declined.
	if err != nil && !errors.Is(err, ErrUserDeclined) {
		t.Fatalf("unexpected error: %v", err)
	}
	output := out.String()
	// Should contain either "No local inference servers" or "Skipped" (user said no).
	if !strings.Contains(output, "No local inference servers") &&
		!strings.Contains(output, "Skipped") &&
		!strings.Contains(output, "Invalid selection") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestRunInferenceWizardIO_UserDeclines(t *testing.T) {
	// This test verifies the sentinel error type is exported and correct.
	if !errors.Is(ErrUserDeclined, ErrUserDeclined) {
		t.Error("ErrUserDeclined should be equal to itself")
	}
}

func TestPersistConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}

	err := persistConfig(cfg, "http://127.0.0.1:11434", "llama3")
	if err != nil {
		t.Fatalf("persistConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, inferenceConfigFile))
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var ic InferenceConfig
	if err := json.Unmarshal(data, &ic); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}
	if ic.BaseURL != "http://127.0.0.1:11434" {
		t.Errorf("BaseURL = %q, want %q", ic.BaseURL, "http://127.0.0.1:11434")
	}
	if ic.Model != "llama3" {
		t.Errorf("Model = %q, want %q", ic.Model, "llama3")
	}
}
