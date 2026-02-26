package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/urfave/cli/v3"
)

func TestMonetizeCommand_Structure(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}

	cmd := monetizeCommand(cfg)
	if cmd.Name != "monetize" {
		t.Fatalf("command name = %q, want monetize", cmd.Name)
	}

	expected := map[string]bool{
		"offer":        false,
		"list":         false,
		"offer-status": false,
		"stop":         false,
		"delete":       false,
		"register":     false,
		"pricing":      false,
		"status":       false,
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

func TestMonetizeOffer_RequiredFlags(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}

	cmd := monetizeCommand(cfg)

	for _, sub := range cmd.Commands {
		if sub.Name != "offer" {
			continue
		}

		requiredFlags := map[string]bool{
			"network": false,
			"pay-to":  false,
		}

		for _, f := range sub.Flags {
			for _, name := range f.Names() {
				if _, ok := requiredFlags[name]; !ok {
					continue
				}
				// Check Required field via type assertion to concrete flag types.
				switch sf := f.(type) {
				case *cli.StringFlag:
					requiredFlags[name] = sf.Required
				case *cli.IntFlag:
					requiredFlags[name] = sf.Required
				case *cli.BoolFlag:
					requiredFlags[name] = sf.Required
				}
			}
		}

		for name, isReq := range requiredFlags {
			if !isReq {
				t.Errorf("flag --%s should be required", name)
			}
		}
		return
	}
	t.Fatal("offer subcommand not found")
}
