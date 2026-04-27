package main

import "testing"

func TestAgentCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)

	expected := map[string]bool{
		"init":   false,
		"new":    false,
		"sync":   false,
		"setup":  false,
		"auth":   false,
		"list":   false,
		"delete": false,
		"wallet": false,
	}

	for _, sub := range cmd.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing agent subcommand %q", name)
		}
	}
}

func TestAgentNewCommand_DefaultsToHermes(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)
	newCmd := findSubcommand(t, cmd, "new")
	flags := flagMap(newCmd)

	assertStringDefault(t, flags, "runtime", "hermes")
	requireFlags(t, flags, "id", "force", "no-sync")
}

func TestAgentWalletCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)
	wallet := findSubcommand(t, cmd, "wallet")

	expected := map[string]bool{
		"address": false,
		"list":    false,
		"backup":  false,
		"restore": false,
	}

	for _, sub := range wallet.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing wallet subcommand %q", name)
		}
	}
}
