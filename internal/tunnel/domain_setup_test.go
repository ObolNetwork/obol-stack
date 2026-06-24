package tunnel

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestSetupRequiresHostnameNonInteractive(t *testing.T) {
	cfg := testConfig(t)
	_, err := Setup(cfg, ui.New(false), SetupOptions{})
	if err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("expected hostname-required error, got %v", err)
	}
}

func TestResolveConnectorTokenNonInteractiveRequiresToken(t *testing.T) {
	_, err := resolveConnectorToken(ui.New(false), "stack.example.com", "")
	if err == nil || !strings.Contains(err.Error(), "connector token") {
		t.Fatalf("expected connector-token-required error, got %v", err)
	}
}

func TestResolveConnectorTokenAcceptsSuppliedFullLine(t *testing.T) {
	tok := makeConnectorToken(t)
	got, err := resolveConnectorToken(ui.New(false), "stack.example.com", "cloudflared tunnel run --token "+tok)
	if err != nil {
		t.Fatalf("resolveConnectorToken: %v", err)
	}
	if got != tok {
		t.Fatalf("token = %q, want extracted token", got)
	}
}

func TestResolveConnectorTokenRejectsGarbage(t *testing.T) {
	_, err := resolveConnectorToken(ui.New(false), "stack.example.com", "not-a-real-token")
	if err == nil {
		t.Fatal("expected error for invalid connector token")
	}
}
