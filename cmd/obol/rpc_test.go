package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestRPCCommand_Structure(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}

	cmd := rpcCommand(cfg)
	if cmd.Name != "rpc" {
		t.Fatalf("expected command name 'rpc', got %q", cmd.Name)
	}

	// Verify all expected subcommands exist.
	expected := map[string]bool{
		"list":   false,
		"add":    false,
		"remove": false,
		"status": false,
	}

	for _, sub := range cmd.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		} else {
			t.Errorf("unexpected subcommand: %q", sub.Name)
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing expected subcommand: %q", name)
		}
	}
}

func TestRPCCommand_AddRequiresArg(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}

	cmd := rpcCommand(cfg)
	addCmd := findSubcommand(t, cmd, "add")
	if addCmd == nil {
		t.Fatal("add subcommand not found")
	}

	if addCmd.ArgsUsage == "" {
		t.Error("add command should have ArgsUsage set")
	}
}

func TestRPCCommand_RemoveRequiresArg(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}

	cmd := rpcCommand(cfg)
	removeCmd := findSubcommand(t, cmd, "remove")
	if removeCmd == nil {
		t.Fatal("remove subcommand not found")
	}

	if removeCmd.ArgsUsage == "" {
		t.Error("remove command should have ArgsUsage set")
	}
}
