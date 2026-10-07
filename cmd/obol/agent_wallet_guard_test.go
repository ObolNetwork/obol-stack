package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestWalletDeleteGuard(t *testing.T) {
	if err := walletDeleteGuard("a", "", "backup", false); err != nil {
		t.Fatalf("no wallet must not block: %v", err)
	}
	if err := walletDeleteGuard("a", "0xabc", "backup", true); err != nil {
		t.Fatalf("--delete-wallet must allow: %v", err)
	}
	err := walletDeleteGuard("a", "0xabc", "obol agent wallet backup x", false)
	if err == nil || !strings.Contains(err.Error(), "0xabc") || !strings.Contains(err.Error(), "--delete-wallet") ||
		!strings.Contains(err.Error(), "obol agent wallet backup x") {
		t.Fatalf("guard error should name address, backup and flag: %v", err)
	}
}

func TestLegacyAgentWalletAddress(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	if got := legacyAgentWalletAddress(cfg, agentruntime.Hermes, "obol-agent"); got != "" {
		t.Fatalf("no keystore dir → %q, want empty", got)
	}

	dir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, "obol-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Address-only fixture: the guard must never need key material.
	if err := os.WriteFile(filepath.Join(dir, "k.json"), []byte(`{"address":"d0391eedc3268f3deef1f05fff5d7aef82f64ccf"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := legacyAgentWalletAddress(cfg, agentruntime.Hermes, "obol-agent"); got != "0xd0391eedc3268f3deef1f05fff5d7aef82f64ccf" {
		t.Fatalf("address = %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "k.json"), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := legacyAgentWalletAddress(cfg, agentruntime.Hermes, "obol-agent"); got == "" {
		t.Fatal("unreadable keystore must still count as a wallet")
	}
}
