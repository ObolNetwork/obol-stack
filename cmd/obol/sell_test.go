package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

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

func flagMap(cmd *cli.Command) map[string]cli.Flag {
	m := map[string]cli.Flag{}

	for _, f := range cmd.Flags {
		for _, name := range f.Names() {
			m[name] = f
		}
	}

	return m
}

func requireFlags(t *testing.T, flags map[string]cli.Flag, names ...string) {
	t.Helper()

	for _, name := range names {
		if _, ok := flags[name]; !ok {
			t.Errorf("missing flag: --%s", name)
		}
	}
}

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

func assertFlagHasAlias(t *testing.T, flags map[string]cli.Flag, primary, alias string) {
	t.Helper()

	if _, ok := flags[alias]; !ok {
		t.Errorf("flag --%s missing alias %q", primary, alias)
	}
}

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()

	return &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
		BinDir:    t.TempDir(),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestSellCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)

	if cmd.Name != "sell" {
		t.Fatalf("command name = %q, want sell", cmd.Name)
	}

	expected := map[string]bool{
		"inference": false,
		"http":      false,
		"list":      false,
		"status":    false,
		"stop":      false,
		"delete":    false,
		"pricing":   false,
		"register":  false,
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

func TestSellInference_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	inf := findSubcommand(t, cmd, "inference")
	flags := flagMap(inf)

	requireFlags(t, flags,
		"model", "wallet", "price", "per-request", "per-mtok", "chain", "facilitator",
		"listen", "upstream", "enclave-tag",
		"vm", "vm-image", "vm-cpus", "vm-memory", "vm-host-port",
		"tee", "model-hash",
	)

	assertStringDefault(t, flags, "price", "0.001")
	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "listen", ":8402")
	assertStringDefault(t, flags, "upstream", "http://localhost:11434")
	assertStringDefault(t, flags, "facilitator", "https://x402.gcp.obol.tech")
	assertStringDefault(t, flags, "vm-image", "ollama/ollama:latest")
	assertIntDefault(t, flags, "vm-cpus", 4)
	assertIntDefault(t, flags, "vm-memory", 8192)
	assertIntDefault(t, flags, "vm-host-port", 11435)
}

func TestSellHTTP_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	http := findSubcommand(t, cmd, "http")
	flags := flagMap(http)

	requireFlags(t, flags,
		"wallet", "chain", "price", "per-request", "per-mtok", "per-hour",
		"namespace", "upstream", "port", "health-path", "path",
		"max-timeout",
		"register", "no-register", "register-name", "register-description", "register-image", "private-key-file",
	)

	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "namespace", "default")
	assertStringDefault(t, flags, "health-path", "/health")
	assertIntDefault(t, flags, "port", 8080)
	assertIntDefault(t, flags, "max-timeout", 300)
}

func TestBuildSellHTTPRegistrationConfig_DefaultEnabled(t *testing.T) {
	reg, enabled, err := buildSellHTTPRegistrationConfig("demo", sellHTTPRegistrationInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled {
		t.Fatal("registration should be enabled by default")
	}
	if reg["enabled"] != true {
		t.Fatalf("registration.enabled = %v, want true", reg["enabled"])
	}
	if reg["name"] != "demo" {
		t.Fatalf("registration.name = %v, want demo", reg["name"])
	}
}

func TestBuildSellHTTPRegistrationConfig_NoRegisterConflicts(t *testing.T) {
	_, _, err := buildSellHTTPRegistrationConfig("demo", sellHTTPRegistrationInput{
		NoRegister: true,
		Name:       "custom",
	})
	if err == nil {
		t.Fatal("expected error for --no-register with registration-specific flags")
	}
}

func TestReadPrivateKeyMaterial_RawKey(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	raw := "0x" + fmt.Sprintf("%x", crypto.FromECDSA(key))
	gotKey, gotAddr, err := readPrivateKeyMaterial(raw)
	if err != nil {
		t.Fatalf("readPrivateKeyMaterial: %v", err)
	}
	if gotKey != raw {
		t.Fatalf("got key = %q, want %q", gotKey, raw)
	}
	if gotAddr != crypto.PubkeyToAddress(key.PublicKey).Hex() {
		t.Fatalf("got addr = %q, want %q", gotAddr, crypto.PubkeyToAddress(key.PublicKey).Hex())
	}
}

func TestReadPrivateKeyMaterial_File(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	raw := "0x" + fmt.Sprintf("%x", crypto.FromECDSA(key))
	path := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	gotKey, gotAddr, err := readPrivateKeyMaterial(path)
	if err != nil {
		t.Fatalf("readPrivateKeyMaterial: %v", err)
	}
	if gotKey != raw {
		t.Fatalf("got key = %q, want %q", gotKey, raw)
	}
	if gotAddr != crypto.PubkeyToAddress(key.PublicKey).Hex() {
		t.Fatalf("got addr = %q, want %q", gotAddr, crypto.PubkeyToAddress(key.PublicKey).Hex())
	}
}

func TestReadPrivateKeyMaterial_Invalid(t *testing.T) {
	if _, _, err := readPrivateKeyMaterial("0xdeadbeef"); err == nil {
		t.Fatal("expected error for invalid private key")
	}
}

func TestServiceOfferStatusLines(t *testing.T) {
	offer := monetizeapi.ServiceOffer{
		Status: monetizeapi.ServiceOfferStatus{
			Endpoint:           "/services/demo",
			AgentID:            "5008",
			RegistrationTxHash: "0xabc",
			Conditions: []monetizeapi.Condition{
				{Type: "Registered", Status: "True", Reason: "Registered", Message: "Published registration document and recorded agent 5008"},
			},
		},
	}
	lines := serviceOfferStatusLines("llm", "demo", offer)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"ServiceOffer:    llm/demo",
		"Agent ID:        5008",
		"Registration Tx: 0xabc",
		"type: Registered",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("status lines missing %q\n%s", want, joined)
		}
	}
}

func TestSellStop_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	stop := findSubcommand(t, cmd, "stop")
	flags := flagMap(stop)

	requireFlags(t, flags, "namespace")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
}

func TestSellDelete_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	del := findSubcommand(t, cmd, "delete")
	flags := flagMap(del)

	requireFlags(t, flags, "namespace", "force")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
	assertFlagHasAlias(t, flags, "force", "f")
}

func TestSellRegister_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	reg := findSubcommand(t, cmd, "register")
	flags := flagMap(reg)

	requireFlags(t, flags,
		"chain", "sponsored", "private-key-file",
		"endpoint", "name", "description", "image",
	)

	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "name", "Obol Agent")
	assertStringDefault(t, flags, "description", "Obol Stack AI agent with x402 payment-gated services")
}

func TestSellPricing_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	pricing := findSubcommand(t, cmd, "pricing")
	flags := flagMap(pricing)

	requireFlags(t, flags, "wallet", "chain")
	assertStringDefault(t, flags, "chain", "base")
}

func TestSellList_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	list := findSubcommand(t, cmd, "list")
	flags := flagMap(list)

	requireFlags(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
}

func TestMustMarshal_ValidJSON(t *testing.T) {
	doc := map[string]any{"active": false, "name": "test"}

	got := mustMarshal(doc)
	if got == "{}" {
		t.Fatal("mustMarshal returned empty object for valid input")
	}

	for _, want := range []string{`"active":false`, `"name":"test"`} {
		if !strings.Contains(got, want) {
			t.Errorf("mustMarshal output missing %s, got: %s", want, got)
		}
	}
}

func TestMustMarshal_InvalidInput(t *testing.T) {
	got := mustMarshal(make(chan int))
	if got != "{}" {
		t.Errorf("mustMarshal should return {} on error, got: %s", got)
	}
}

func TestResolveX402Chain(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"base", false},
		{"base-mainnet", false},
		{"base-sepolia", false},
		{"ethereum", false},
		{"mainnet", false},
		{"ethereum-mainnet", false},
		{"unknown-chain", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := x402verifier.ResolveChainInfo(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveChainInfo(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}
