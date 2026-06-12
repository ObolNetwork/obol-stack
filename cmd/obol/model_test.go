package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/urfave/cli/v3"

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

// TestSetupPromoteList pins the contract that `obol model setup` auto-promotes
// explicitly named models to primary but never the auto-detected full Ollama
// inventory. Promoting on auto-detect would silently reshuffle the operator's
// model_list (the spark2 footgun); promoting on an explicit --model is what
// makes a freshly configured provider take effect without a manual prefer.
func TestSetupPromoteList(t *testing.T) {
	t.Run("auto-detect (no explicit models) promotes nothing", func(t *testing.T) {
		if got := setupPromoteList(nil); len(got) != 0 {
			t.Fatalf("setupPromoteList(nil) = %v, want empty (auto-detect must not reshuffle)", got)
		}
		if got := setupPromoteList([]string{}); len(got) != 0 {
			t.Fatalf("setupPromoteList([]) = %v, want empty", got)
		}
	})

	t.Run("explicit models are promoted in order", func(t *testing.T) {
		in := []string{"claude-sonnet-4-6"}
		got := setupPromoteList(in)
		if len(got) != 1 || got[0] != "claude-sonnet-4-6" {
			t.Fatalf("setupPromoteList(%v) = %v, want [claude-sonnet-4-6]", in, got)
		}
	})

	t.Run("returns a fresh slice, not an alias of the input", func(t *testing.T) {
		in := []string{"a", "b"}
		got := setupPromoteList(in)
		got[0] = "mutated"
		if in[0] != "a" {
			t.Fatalf("setupPromoteList aliased its input: mutating the result changed in[0] to %q", in[0])
		}
	})
}

// TestModelSetup_BYOKFlags pins the BYOK onboarding surface: the setup
// command exposes --provider/--api-key/--model/--free, and every BYOK
// aggregator in the registry dispatches through the generic cloud path
// (only Ollama is special-cased).
func TestModelSetup_BYOKFlags(t *testing.T) {
	cfg := &config.Config{}
	var setup *cli.Command
	for _, sub := range modelCommand(cfg).Commands {
		if sub.Name == "setup" {
			setup = sub
		}
	}
	if setup == nil {
		t.Fatal("model setup command missing")
	}

	want := map[string]bool{"provider": false, "api-key": false, "model": false, "free": false}
	for _, f := range setup.Flags {
		for _, n := range f.Names() {
			if _, ok := want[n]; ok {
				want[n] = true
			}
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("model setup missing --%s flag", n)
		}
	}

	// The registry must carry the BYOK providers this PR adds.
	for _, id := range []string{"venice", "openrouter", "nvidia", "gmi", "novita", "huggingface"} {
		p, ok := model.ProviderByID(id)
		if !ok {
			t.Errorf("provider %q missing from registry", id)
			continue
		}
		if p.BaseURL == "" {
			t.Errorf("provider %q has no BaseURL", id)
		}
	}
}
