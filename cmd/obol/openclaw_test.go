package main

import "testing"

func TestOpenClawWalletCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := openclawCommand(cfg)
	wallet := findSubcommand(t, cmd, "wallet")

	expected := map[string]bool{
		"backup":             false,
		"restore":            false,
		"import-private-key": false,
		"address":            false,
		"list":               false,
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
