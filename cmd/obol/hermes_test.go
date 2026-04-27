package main

import "testing"

func TestHermesCommand_IsNativePassthrough(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := hermesCommand(cfg)

	if cmd.Usage != "Run native Hermes CLI against a deployed Hermes instance" {
		t.Fatalf("unexpected usage: %q", cmd.Usage)
	}
	if !cmd.SkipFlagParsing {
		t.Fatal("Hermes command should pass native Hermes flags through")
	}
	if !cmd.HideHelp {
		t.Fatal("Hermes command should pass native --help through")
	}
	if len(cmd.Commands) != 0 {
		t.Fatalf("Hermes command should not define Obol-managed subcommands, got %d", len(cmd.Commands))
	}
}
