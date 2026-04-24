package main

import "testing"

func TestHermesCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := hermesCommand(cfg)

	expected := map[string]bool{
		"onboard":   false,
		"sync":      false,
		"token":     false,
		"list":      false,
		"delete":    false,
		"setup":     false,
		"dashboard": false,
		"wallet":    false,
		"skills":    false,
	}

	for _, sub := range cmd.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing Hermes subcommand %q", name)
		}
	}
}

func TestHermesSkillsCommand_UsesRawFlagParsing(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := hermesCommand(cfg)
	skills := findSubcommand(t, cmd, "skills")

	if !skills.SkipFlagParsing {
		t.Fatal("Hermes skills command should pass native Hermes flags through")
	}
}
