package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalletCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := walletCommand(cfg)

	if cmd.Name != "wallet" {
		t.Fatalf("command name = %q, want wallet", cmd.Name)
	}

	importCmd := findSubcommand(t, cmd, "import")
	flags := flagMap(importCmd)

	for _, name := range []string{"private-key-file", "instance", "force"} {
		if _, ok := flags[name]; !ok {
			t.Errorf("missing wallet import flag --%s", name)
		}
	}
}

func TestWalletClusterAvailable(t *testing.T) {
	cfg := newTestConfig(t)
	if walletClusterAvailable(cfg) {
		t.Fatal("cluster should not be available without kubeconfig")
	}

	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !walletClusterAvailable(cfg) {
		t.Fatal("cluster should be available with kubeconfig")
	}
}
