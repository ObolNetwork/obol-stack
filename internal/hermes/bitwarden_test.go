package hermes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitwardenConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultBitwardenConfig()
	cfg.Enabled = true
	cfg.ProjectID = "project-123"
	cfg.ServerURL = "https://vault.bitwarden.com"

	if err := saveBitwardenConfig(dir, cfg); err != nil {
		t.Fatalf("saveBitwardenConfig: %v", err)
	}
	got, ok, err := LoadBitwardenConfig(dir)
	if err != nil {
		t.Fatalf("LoadBitwardenConfig: %v", err)
	}
	if !ok {
		t.Fatal("LoadBitwardenConfig ok=false, want true")
	}
	if got.ProjectID != cfg.ProjectID || got.ServerURL != cfg.ServerURL || !got.Enabled {
		t.Fatalf("loaded config = %#v", got)
	}
	if got.AccessTokenEnv != "BWS_ACCESS_TOKEN" {
		t.Fatalf("AccessTokenEnv = %q", got.AccessTokenEnv)
	}
}

func TestFetchBitwardenSecretsUsesBWSCLI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "bws")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "secret" ] || [ "$2" != "list" ] || [ "$3" != "project-123" ]; then
  echo "unexpected args: $*" >&2
  exit 2
fi
if [ "$BWS_ACCESS_TOKEN" != "token-123" ]; then
  echo "missing token" >&2
  exit 3
fi
printf '[{"key":"OPENAI_API_KEY","value":"sk-test"}]'
`), 0o755); err != nil {
		t.Fatalf("write fake bws: %v", err)
	}
	t.Setenv("OBOL_BWS_BIN", script)

	cfg := DefaultBitwardenConfig()
	cfg.Enabled = true
	cfg.ProjectID = "project-123"
	secrets, err := FetchBitwardenSecrets(context.Background(), cfg, "token-123")
	if err != nil {
		t.Fatalf("FetchBitwardenSecrets: %v", err)
	}
	if got := secrets["OPENAI_API_KEY"]; got != "sk-test" {
		t.Fatalf("OPENAI_API_KEY = %q", got)
	}
}

func TestFetchBitwardenSecretsRedactsTokenOnError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "bws")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo "bad token token-123" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write fake bws: %v", err)
	}
	t.Setenv("OBOL_BWS_BIN", script)

	cfg := DefaultBitwardenConfig()
	cfg.ProjectID = "project-123"
	_, err := FetchBitwardenSecrets(context.Background(), cfg, "token-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "token-123") {
		t.Fatalf("error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error missing redaction marker: %v", err)
	}
}
