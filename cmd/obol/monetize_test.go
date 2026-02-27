package main

import (
	"strings"
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

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// findSubcommand looks up a subcommand by name within a parent command.
func findSubcommand(t *testing.T, parent *cli.Command, name string) *cli.Command {
	t.Helper()
	for _, sub := range parent.Commands {
		if sub.Name == name {
			return sub
		}
	}
	t.Fatalf("subcommand %q not found in %q", name, parent.Name)
	return nil
}

// flagMap builds a map of flag name -> cli.Flag from a command's flags,
// including all aliases.
func flagMap(cmd *cli.Command) map[string]cli.Flag {
	m := map[string]cli.Flag{}
	for _, f := range cmd.Flags {
		for _, name := range f.Names() {
			m[name] = f
		}
	}
	return m
}

// requireFlags asserts that all named flags exist in the command.
func requireFlags(t *testing.T, flags map[string]cli.Flag, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := flags[name]; !ok {
			t.Errorf("missing flag: --%s", name)
		}
	}
}

// assertStringDefault checks that a flag is a *cli.StringFlag with the expected default.
func assertStringDefault(t *testing.T, flags map[string]cli.Flag, name, want string) {
	t.Helper()
	f, ok := flags[name]
	if !ok {
		t.Errorf("missing flag: --%s", name)
		return
	}
	sf, ok := f.(*cli.StringFlag)
	if !ok {
		t.Errorf("flag --%s is %T, want *cli.StringFlag", name, f)
		return
	}
	if sf.Value != want {
		t.Errorf("flag --%s default = %q, want %q", name, sf.Value, want)
	}
}

// assertIntDefault checks that a flag is a *cli.IntFlag with the expected default.
func assertIntDefault(t *testing.T, flags map[string]cli.Flag, name string, want int) {
	t.Helper()
	f, ok := flags[name]
	if !ok {
		t.Errorf("missing flag: --%s", name)
		return
	}
	sf, ok := f.(*cli.IntFlag)
	if !ok {
		t.Errorf("flag --%s is %T, want *cli.IntFlag", name, f)
		return
	}
	if sf.Value != want {
		t.Errorf("flag --%s default = %d, want %d", name, sf.Value, want)
	}
}

// assertFlagRequired checks that a flag has Required == true.
func assertFlagRequired(t *testing.T, flags map[string]cli.Flag, name string) {
	t.Helper()
	f, ok := flags[name]
	if !ok {
		t.Errorf("missing flag: --%s", name)
		return
	}
	switch sf := f.(type) {
	case *cli.StringFlag:
		if !sf.Required {
			t.Errorf("flag --%s should be required", name)
		}
	case *cli.IntFlag:
		if !sf.Required {
			t.Errorf("flag --%s should be required", name)
		}
	case *cli.BoolFlag:
		if !sf.Required {
			t.Errorf("flag --%s should be required", name)
		}
	default:
		t.Errorf("flag --%s has unexpected type %T", name, f)
	}
}

// assertFlagHasAlias checks that a flag exposes a given alias.
func assertFlagHasAlias(t *testing.T, flags map[string]cli.Flag, primary, alias string) {
	t.Helper()
	if _, ok := flags[alias]; !ok {
		t.Errorf("flag --%s missing alias %q", primary, alias)
	}
}

// newTestConfig returns a throwaway config suitable for unit tests.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// New tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMonetizeOffer_AllFlags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	offer := findSubcommand(t, cmd, "offer")
	flags := flagMap(offer)

	// Every expected flag must be present.
	requireFlags(t, flags,
		"type", "model", "runtime",
		"per-request", "per-mtok", "per-hour",
		"network", "pay-to",
		"max-timeout", "namespace", "upstream", "port", "path",
		"register", "register-name", "register-description", "register-image",
	)

	// Verify default values for flags that have them.
	assertStringDefault(t, flags, "type", "inference")
	assertStringDefault(t, flags, "runtime", "ollama")
	assertStringDefault(t, flags, "namespace", "llm")
	assertStringDefault(t, flags, "upstream", "ollama")
	assertIntDefault(t, flags, "max-timeout", 300)
	assertIntDefault(t, flags, "port", 11434)

	// Flags without defaults should have zero-value.
	assertStringDefault(t, flags, "model", "")
	assertStringDefault(t, flags, "per-request", "")
	assertStringDefault(t, flags, "per-mtok", "")
	assertStringDefault(t, flags, "per-hour", "")
	assertStringDefault(t, flags, "path", "")
	assertStringDefault(t, flags, "register-name", "")
	assertStringDefault(t, flags, "register-description", "")
	assertStringDefault(t, flags, "register-image", "")

	// "register" is a BoolFlag, default false.
	if bf, ok := flags["register"].(*cli.BoolFlag); ok {
		if bf.Value {
			t.Error("flag --register default should be false")
		}
	} else {
		t.Errorf("flag --register is %T, want *cli.BoolFlag", flags["register"])
	}
}

func TestMonetizeStop_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	stop := findSubcommand(t, cmd, "stop")
	flags := flagMap(stop)

	requireFlags(t, flags, "namespace")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
}

func TestMonetizeDelete_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	del := findSubcommand(t, cmd, "delete")
	flags := flagMap(del)

	requireFlags(t, flags, "namespace", "force")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
	assertFlagHasAlias(t, flags, "force", "f")
}

func TestMonetizeRegister_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	reg := findSubcommand(t, cmd, "register")
	flags := flagMap(reg)

	requireFlags(t, flags,
		"private-key", "private-key-file", "rpc-url",
		"endpoint", "name", "description",
	)
}

func TestMonetizePricing_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	pricing := findSubcommand(t, cmd, "pricing")
	flags := flagMap(pricing)

	requireFlags(t, flags, "wallet", "chain")
	assertFlagRequired(t, flags, "wallet")
	assertStringDefault(t, flags, "chain", "base-sepolia")
}

func TestMonetizeList_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	list := findSubcommand(t, cmd, "list")
	flags := flagMap(list)

	requireFlags(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
}

func TestMonetizeOfferStatus_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := monetizeCommand(cfg)
	offerStatus := findSubcommand(t, cmd, "offer-status")
	flags := flagMap(offerStatus)

	requireFlags(t, flags, "namespace")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
}

func TestMustMarshal_ValidJSON(t *testing.T) {
	doc := map[string]interface{}{"active": false, "name": "test"}
	got := mustMarshal(doc)
	if got == "{}" {
		t.Fatal("mustMarshal returned empty object for valid input")
	}
	// Should contain the expected fields.
	for _, want := range []string{`"active":false`, `"name":"test"`} {
		if !strings.Contains(got, want) {
			t.Errorf("mustMarshal output missing %s, got: %s", want, got)
		}
	}
}

func TestMustMarshal_InvalidInput(t *testing.T) {
	// Channels can't be JSON-marshaled.
	got := mustMarshal(make(chan int))
	if got != "{}" {
		t.Errorf("mustMarshal should return {} on error, got: %s", got)
	}
}
