package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestResolveWalletAddress_NoInstances(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	_, err := ResolveWalletAddress(cfg)
	if err == nil {
		t.Fatal("expected error for no instances")
	}
}

func TestResolveWalletAddress_SingleInstance(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	// Create instance directory structure.
	instDir := filepath.Join(cfg.ConfigDir, "applications", appName, "test-instance")
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}

	wallet := &WalletInfo{
		Address:      "0xAbCd1234567890abcdef1234567890abcdef1234",
		KeystoreUUID: "uuid-123",
	}
	if err := WriteWalletMetadata(instDir, wallet); err != nil {
		t.Fatal(err)
	}

	addr, err := ResolveWalletAddress(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != wallet.Address {
		t.Errorf("got %q, want %q", addr, wallet.Address)
	}
}

func TestResolveWalletAddress_MultipleInstances(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	for _, id := range []string{"inst-a", "inst-b"} {
		instDir := filepath.Join(appsDir, id)
		if err := os.MkdirAll(instDir, 0755); err != nil {
			t.Fatal(err)
		}
		wallet := &WalletInfo{Address: "0x" + id}
		if err := WriteWalletMetadata(instDir, wallet); err != nil {
			t.Fatal(err)
		}
	}

	_, err := ResolveWalletAddress(cfg)
	if err == nil {
		t.Fatal("expected error for multiple instances")
	}
}

func TestResolveWalletAddress_CorruptedWallet(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	instDir := filepath.Join(cfg.ConfigDir, "applications", appName, "broken-instance")
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write invalid JSON as wallet metadata.
	if err := os.WriteFile(filepath.Join(instDir, "wallet.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveWalletAddress(cfg)
	if err == nil {
		t.Fatal("expected error for corrupted wallet.json")
	}
}

func TestResolveInstanceNamespace_NoInstances(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	_, err := ResolveInstanceNamespace(cfg)
	if err == nil {
		t.Fatal("expected error for no instances")
	}
}

func TestResolveInstanceNamespace_MultipleInstances(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)
	for _, id := range []string{"inst-x", "inst-y"} {
		if err := os.MkdirAll(filepath.Join(appsDir, id), 0755); err != nil {
			t.Fatal(err)
		}
	}

	_, err := ResolveInstanceNamespace(cfg)
	if err == nil {
		t.Fatal("expected error for multiple instances")
	}
}

func TestResolveInstanceNamespace_SingleInstance(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	instDir := filepath.Join(cfg.ConfigDir, "applications", appName, "my-agent")
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}

	ns, err := ResolveInstanceNamespace(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "openclaw-my-agent" {
		t.Errorf("got %q, want %q", ns, "openclaw-my-agent")
	}
}
