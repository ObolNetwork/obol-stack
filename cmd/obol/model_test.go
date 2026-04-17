package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestModelCommand_Structure(t *testing.T) {
	cfg := &config.Config{}
	cmd := modelCommand(cfg)

	if cmd.Name != "model" {
		t.Fatalf("command name = %q, want model", cmd.Name)
	}

	expected := map[string]bool{
		"setup":  false,
		"status": false,
		"token":  false,
		"sync":   false,
		"pull":   false,
		"list":   false,
		"remove": false,
	}

	for _, sub := range cmd.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}
