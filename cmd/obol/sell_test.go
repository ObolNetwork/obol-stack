package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
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

func TestParseUpstreamHeaders(t *testing.T) {
	t.Run("parses Key: Value pairs and trims", func(t *testing.T) {
		got, err := parseUpstreamHeaders([]string{"X-Api-Key: abc", "X-Region:eu"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["X-Api-Key"] != "abc" || got["X-Region"] != "eu" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("nil for no flags", func(t *testing.T) {
		if got, err := parseUpstreamHeaders(nil); err != nil || got != nil {
			t.Errorf("got %v, %v; want nil, nil", got, err)
		}
	})
	t.Run("rejects malformed pair", func(t *testing.T) {
		if _, err := parseUpstreamHeaders([]string{"no-colon"}); err == nil {
			t.Error("expected error for header without a colon")
		}
	})
}

func TestSellCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)

	if cmd.Name != "sell" {
		t.Fatalf("command name = %q, want sell", cmd.Name)
	}

	expected := map[string]bool{
		"inference": false,
		"http":      false,
		"demo":      false,
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
		"model", "wallet", "price", "per-request", "per-mtok", "chain", "token", "facilitator",
		"listen", "upstream", "enclave-tag",
		"vm", "vm-image", "vm-cpus", "vm-memory", "vm-host-port",
		"tee", "model-hash",
		// Registration parity with `obol sell http`. Their absence on the
		// inference subcommand was the regression that left
		// /.well-known/agent-registration.json unrouted (see
		// TestBuildInferenceServiceOfferSpec_RegistrationEnabledByDefault).
		"no-register",
		"register-name", "register-description", "register-image",
		"register-skills", "register-domains", "register-metadata",
	)

	// --price intentionally has no default after #470 — the resolvePriceTable
	// fallthrough requires an explicit price flag instead of letting "0.001"
	// shadow --per-mtok / --per-request. Empty default is the pinned contract.
	assertStringDefault(t, flags, "price", "")
	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "token", "USDC")
	assertStringDefault(t, flags, "listen", ":8402")
	assertStringDefault(t, flags, "upstream", "http://localhost:11434")
	assertStringDefault(t, flags, "facilitator", "https://x402.gcp.obol.tech")
	assertStringDefault(t, flags, "vm-image", "ollama/ollama:latest")
	assertIntDefault(t, flags, "vm-cpus", 4)
	assertIntDefault(t, flags, "vm-memory", 8192)
	assertIntDefault(t, flags, "vm-host-port", 11435)
}

// TestSell_PayToFlagAliases locks in the --pay-to rollout: every sell
// command that takes a recipient address must use --pay-to as the primary
// flag with --wallet/--recipient/-w as deprecated aliases. The shared
// payToFlag() helper produces this exact shape; the test guards against
// someone re-introducing a bespoke --wallet flag.
func TestSell_PayToFlagAliases(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)

	cases := []string{"inference", "http", "demo", "update", "pricing"}
	for _, sub := range cases {
		t.Run(sub, func(t *testing.T) {
			subCmd := findSubcommand(t, cmd, sub)
			flags := flagMap(subCmd)

			payTo, ok := flags["pay-to"]
			if !ok {
				t.Fatalf("--pay-to missing on `obol sell %s`", sub)
			}
			names := payTo.Names()
			for _, expected := range []string{"pay-to", "wallet", "recipient", "w"} {
				found := false
				for _, n := range names {
					if n == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("`obol sell %s` --pay-to missing alias %q (got %v)", sub, expected, names)
				}
			}
		})
	}
}

func TestSellHTTP_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	http := findSubcommand(t, cmd, "http")
	flags := flagMap(http)

	requireFlags(t, flags,
		"wallet", "chain", "token", "price", "per-request", "per-mtok", "per-hour",
		"namespace", "upstream", "port", "health-path", "path",
		"max-timeout",
		"register", "no-register", "register-name", "register-description", "register-image",
	)

	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "token", "USDC")
	assertStringDefault(t, flags, "namespace", "default")
	assertStringDefault(t, flags, "health-path", "/health")
	assertIntDefault(t, flags, "port", 8080)
	assertIntDefault(t, flags, "max-timeout", 300)
}

func TestBuildSellRegistrationConfig_DefaultEnabled(t *testing.T) {
	reg, enabled, err := buildSellRegistrationConfig("demo", sellRegistrationInput{})
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

func TestBuildSellRegistrationConfig_NoRegisterConflicts(t *testing.T) {
	_, _, err := buildSellRegistrationConfig("demo", sellRegistrationInput{
		NoRegister: true,
		Name:       "custom",
	})
	if err == nil {
		t.Fatal("expected error for --no-register with registration-specific flags")
	}
}

func TestServiceOfferStatusLines(t *testing.T) {
	offer := monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0xd0391eedc3268f3deef1f05fff5d7aef82f64ccf",
				Asset: monetizeapi.ServiceOfferAsset{
					Symbol:  "USDC",
					Address: "0x036C...",
				},
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Endpoint:           "/services/demo",
			AgentID:            "5008",
			RegistrationTxHash: "0xabc",
			Conditions: []monetizeapi.Condition{
				{Type: "Registered", Status: "True", Reason: "Registered", Message: "Published registration document and recorded agent 5008"},
			},
		},
	}
	lines := serviceOfferStatusLines("llm", "demo", offer, "")
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"ServiceOffer:    llm/demo",
		"Network:         base-sepolia",
		"Asset:           USDC (0x036C...)",
		"Price:           0.001 USDC per request",
		"Pay To:          0xd0391eedc3268f3deef1f05fff5d7aef82f64ccf",
		"Agent ID:        5008 (https://sepolia.basescan.org/nft/0x8004A818BFB912233c491871b3d84c89A494BD9e/5008)",
		"Registration Tx: https://sepolia.basescan.org/tx/0xabc",
		"✓ Registered",
		"Published registration document and recorded agent 5008",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("status lines missing %q\n%s", want, joined)
		}
	}
}

func TestServiceOfferStatusLines_RawTxFallback(t *testing.T) {
	// Unknown network: fall back to raw hash (no explorer link).
	offer := monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{
			Payment: monetizeapi.ServiceOfferPayment{Network: "polygon"},
		},
		Status: monetizeapi.ServiceOfferStatus{RegistrationTxHash: "0xdeadbeef"},
	}
	lines := serviceOfferStatusLines("llm", "demo", offer, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Registration Tx: 0xdeadbeef") {
		t.Fatalf("expected raw tx fallback, got:\n%s", joined)
	}
	if strings.Contains(joined, "https://") {
		t.Fatalf("unexpected URL in tx line for unknown network:\n%s", joined)
	}
}

func TestFormatOfferAsset(t *testing.T) {
	tests := []struct {
		name  string
		asset monetizeapi.ServiceOfferAsset
		want  string
	}{
		{"both", monetizeapi.ServiceOfferAsset{Symbol: "USDC", Address: "0x036C"}, "USDC (0x036C)"},
		{"symbol only", monetizeapi.ServiceOfferAsset{Symbol: "OBOL"}, "OBOL"},
		{"address only", monetizeapi.ServiceOfferAsset{Address: "0xabc"}, "0xabc"},
		{"empty", monetizeapi.ServiceOfferAsset{}, "(not set)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatOfferAsset(tt.asset); got != tt.want {
				t.Errorf("formatOfferAsset = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatOfferPrice(t *testing.T) {
	tests := []struct {
		name string
		p    monetizeapi.ServiceOfferPayment
		want string
	}{
		{
			"per request with symbol",
			monetizeapi.ServiceOfferPayment{
				Asset: monetizeapi.ServiceOfferAsset{Symbol: "USDC"},
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
			"0.001 USDC per request",
		},
		{
			"per request no symbol",
			monetizeapi.ServiceOfferPayment{
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
			"0.001 per request",
		},
		{
			"per MTok",
			monetizeapi.ServiceOfferPayment{
				Asset: monetizeapi.ServiceOfferAsset{Symbol: "USDC"},
				Price: monetizeapi.ServiceOfferPriceTable{PerMTok: "5"},
			},
			"5 USDC per MTok",
		},
		{
			"per hour",
			monetizeapi.ServiceOfferPayment{
				Asset: monetizeapi.ServiceOfferAsset{Symbol: "USDC"},
				Price: monetizeapi.ServiceOfferPriceTable{PerHour: "1"},
			},
			"1 USDC per hour",
		},
		{
			"empty",
			monetizeapi.ServiceOfferPayment{},
			"(not set)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatOfferPrice(tt.p); got != tt.want {
				t.Errorf("formatOfferPrice = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExplorerTxURL(t *testing.T) {
	tests := []struct {
		name    string
		network string
		hash    string
		want    string
	}{
		{"ethereum", "ethereum", "0xabc", "https://etherscan.io/tx/0xabc"},
		{"base", "base", "0xabc", "https://basescan.org/tx/0xabc"},
		{"base-sepolia", "base-sepolia", "0xabc", "https://sepolia.basescan.org/tx/0xabc"},
		{"unknown network", "polygon", "0xabc", ""},
		{"empty hash", "ethereum", "", ""},
		{"whitespace hash", "ethereum", "  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := explorerTxURL(tt.network, tt.hash); got != tt.want {
				t.Errorf("explorerTxURL(%q, %q) = %q, want %q", tt.network, tt.hash, got, tt.want)
			}
		})
	}
}

func TestConditionIcon(t *testing.T) {
	tests := []struct {
		name string
		cond monetizeapi.Condition
		want string
	}{
		{"true succeeded", monetizeapi.Condition{Status: "True", Reason: "Reconciled"}, "✓"},
		{"true skipped", monetizeapi.Condition{Status: "True", Reason: "Skipped"}, "ℹ"},
		{"true disabled", monetizeapi.Condition{Status: "True", Reason: "Disabled"}, "ℹ"},
		{"false failed", monetizeapi.Condition{Status: "False", Reason: "Unhealthy"}, "⚠"},
		{"unknown pending", monetizeapi.Condition{Status: "Unknown"}, "⏳"},
		{"empty pending", monetizeapi.Condition{}, "⏳"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conditionIcon(tt.cond); got != tt.want {
				t.Errorf("conditionIcon = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentRegistryNFTURL(t *testing.T) {
	const (
		mainnetReg = "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
		sepoliaReg = "0x8004A818BFB912233c491871b3d84c89A494BD9e"
	)
	tests := []struct {
		name    string
		network string
		agentID string
		want    string
	}{
		{"ethereum", "ethereum", "32117", "https://etherscan.io/nft/" + mainnetReg + "/32117"},
		{"mainnet alias", "mainnet", "32117", "https://etherscan.io/nft/" + mainnetReg + "/32117"},
		{"base shares mainnet registry", "base", "42", "https://basescan.org/nft/" + mainnetReg + "/42"},
		{"base-sepolia distinct registry", "base-sepolia", "1", "https://sepolia.basescan.org/nft/" + sepoliaReg + "/1"},
		{"unknown network", "polygon", "1", ""},
		{"empty network", "", "1", ""},
		{"empty agent id", "ethereum", "", ""},
		{"whitespace agent id", "ethereum", "  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentRegistryNFTURL(tt.network, tt.agentID); got != tt.want {
				t.Errorf("agentRegistryNFTURL(%q, %q) = %q, want %q", tt.network, tt.agentID, got, tt.want)
			}
		})
	}
}

func TestIsConditionTrue(t *testing.T) {
	conds := []monetizeapi.Condition{
		{Type: "Ready", Status: "True"},
		{Type: "Registered", Status: "False"},
		{Type: "Pending", Status: "Unknown"},
	}
	if !isConditionTrue(conds, "Ready") {
		t.Error("Ready should be true")
	}
	if isConditionTrue(conds, "Registered") {
		t.Error("Registered should be false")
	}
	if isConditionTrue(conds, "Pending") {
		t.Error("Pending should be false")
	}
	if isConditionTrue(conds, "Missing") {
		t.Error("Missing should be false")
	}
}

// TestServiceOfferStatusLines_FullURL verifies that when the tunnel URL is
// passed as baseURL, the Endpoint line shows the full https://… URL buyers
// would actually hit (not just the path). Trailing slashes on the base URL
// must not produce double-slashes.
func TestServiceOfferStatusLines_FullURL(t *testing.T) {
	offer := monetizeapi.ServiceOffer{
		Status: monetizeapi.ServiceOfferStatus{
			Endpoint: "/services/demo-hello",
		},
	}
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "with tunnel URL",
			baseURL: "https://records-vast-insert-gear.trycloudflare.com",
			want:    "Endpoint:        https://records-vast-insert-gear.trycloudflare.com/services/demo-hello",
		},
		{
			name:    "trailing slash trimmed",
			baseURL: "https://records-vast-insert-gear.trycloudflare.com/",
			want:    "Endpoint:        https://records-vast-insert-gear.trycloudflare.com/services/demo-hello",
		},
		{
			name:    "no tunnel URL falls back to path",
			baseURL: "",
			want:    "Endpoint:        /services/demo-hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			joined := strings.Join(serviceOfferStatusLines("demo", "demo-hello", offer, tt.baseURL), "\n")
			if !strings.Contains(joined, tt.want) {
				t.Errorf("expected %q in:\n%s", tt.want, joined)
			}
		})
	}
}

func TestSellDemo_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	demo := findSubcommand(t, cmd, "demo")
	flags := flagMap(demo)

	requireFlags(t, flags, "wallet", "chain", "token", "price", "name", "no-register")
	// chain and token deliberately have no flag-level defaults so the action
	// can apply per-type defaults from demoTypes (e.g. hello → OBOL/ethereum,
	// quant → USDC/base-sepolia).
	assertStringDefault(t, flags, "chain", "")
	assertStringDefault(t, flags, "token", "")
}

// TestDemoTypes_PerTypeDefaults locks in the canonical defaults for each demo
// type. These are the prices/chains/tokens users see when running `obol sell
// demo` with no flags — changing them changes onboarding behavior.
func TestDemoTypes_PerTypeDefaults(t *testing.T) {
	tests := []struct {
		demo  string
		chain string
		token string
		price string
	}{
		{"hello", "ethereum", "OBOL", "1"},
		{"blocks", "base-sepolia", "USDC", "0.0001"},
		// Quant moved to agent-backed: 10 OBOL on ethereum mainnet, served
		// by the new Agent CRD path. The legacy 0.01 USDC / base-sepolia
		// pricing is gone with the pure-Go quant handler.
		{"quant", "ethereum", "OBOL", "10"},
	}
	for _, tt := range tests {
		t.Run(tt.demo, func(t *testing.T) {
			spec, ok := demoTypes[tt.demo]
			if !ok {
				t.Fatalf("demoTypes[%q] missing", tt.demo)
			}
			if spec.DefaultChain != tt.chain {
				t.Errorf("DefaultChain = %q, want %q", spec.DefaultChain, tt.chain)
			}
			if spec.DefaultToken != tt.token {
				t.Errorf("DefaultToken = %q, want %q", spec.DefaultToken, tt.token)
			}
			if spec.Price != tt.price {
				t.Errorf("Price = %q, want %q", spec.Price, tt.price)
			}
		})
	}

	// The bare `obol sell demo` (no args) defaults to hello — onboarding's
	// canonical "earn 1 OBOL on mainnet" experience.
	if defaultDemoType != "hello" {
		t.Errorf("defaultDemoType = %q, want hello", defaultDemoType)
	}
}

func TestSellStop_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	stop := findSubcommand(t, cmd, "stop")
	flags := flagMap(stop)

	requireFlags(t, flags, "namespace", "grace", "force")
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "namespace", "n")
	// --now is the documented alias for --force; if it disappears,
	// scripted operators that rely on it break silently.
	assertFlagHasAlias(t, flags, "force", "now")

	graceFlag, ok := flags["grace"].(*cli.DurationFlag)
	if !ok {
		t.Fatalf("--grace should be *cli.DurationFlag, got %T", flags["grace"])
	}
	if graceFlag.Value != monetizeapi.DefaultDrainGracePeriod {
		t.Errorf("--grace default = %v, want %v", graceFlag.Value, monetizeapi.DefaultDrainGracePeriod)
	}
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
		"chain",
		"endpoint", "name", "description", "image",
		// --sponsored is intentionally retained as a deprecated flag that
		// errors with a clear message; users with old muscle memory or stale
		// docs need a louder signal than "unknown flag".
		"sponsored",
	)

	assertStringDefault(t, flags, "chain", "mainnet")
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

func TestIsTransientRegistrationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "rpc 500", err: errors.New("erc8004: register tx: 500 Internal Server Error"), want: true},
		{name: "timeout", err: errors.New("context deadline exceeded while waiting for headers"), want: true},
		{name: "revert", err: errors.New("erc8004: register tx: execution reverted"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientRegistrationError(tt.err); got != tt.want {
				t.Fatalf("isTransientRegistrationError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDemoRPCNetwork(t *testing.T) {
	tests := []struct {
		paymentChain string
		want         string
	}{
		{"base", "base"},
		{"base-mainnet", "base"},
		{"base-sepolia", "base-sepolia"},
		{"ethereum", "mainnet"},
		{"mainnet", "mainnet"},
	}

	for _, tt := range tests {
		t.Run(tt.paymentChain, func(t *testing.T) {
			if got := demoRPCNetwork(tt.paymentChain); got != tt.want {
				t.Fatalf("demoRPCNetwork(%q) = %q, want %q", tt.paymentChain, got, tt.want)
			}
		})
	}
}

// TestBuildInferenceServiceOfferSpec_RegistrationEnabledByDefault pins the
// fix for the missing-registration regression: `obol sell inference` used to
// build a ServiceOffer with empty `spec.registration`, so the controller
// emitted "Registration disabled" and never published the
// /.well-known/agent-registration.json HTTPRoute. The default contract is
// "registration enabled, name derived from offer name" — same as `sell http`.
func TestBuildInferenceServiceOfferSpec_RegistrationEnabledByDefault(t *testing.T) {
	d := &inference.Deployment{
		Name:            "aeon",
		UpstreamURL:     "http://127.0.0.1:8000",
		WalletAddress:   "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
		Chain:           "base-sepolia",
		PricePerRequest: "0.001",
	}
	reg, enabled, err := buildSellRegistrationConfig("aeon", sellRegistrationInput{})
	if err != nil {
		t.Fatalf("buildSellRegistrationConfig: %v", err)
	}
	if !enabled {
		t.Fatal("registration should default to enabled")
	}
	spec, err := buildInferenceServiceOfferSpec(d, schemas.PriceTable{PerRequest: "0.001"}, "llm", "8402", schemas.AssetTerms{}, "aeon-ultimate", reg)
	if err != nil {
		t.Fatalf("buildInferenceServiceOfferSpec: %v", err)
	}
	got, ok := spec["registration"].(map[string]any)
	if !ok {
		t.Fatalf("spec.registration missing or wrong type: %#v", spec["registration"])
	}
	if got["enabled"] != true {
		t.Errorf("spec.registration.enabled = %v, want true", got["enabled"])
	}
	if got["name"] != "aeon" {
		t.Errorf("spec.registration.name = %v, want %q", got["name"], "aeon")
	}
}

// TestBuildInferenceServiceOfferSpec_NoRegisterOmitsRegistration confirms the
// opt-out path: --no-register produces an empty registration map and the spec
// builder must leave spec.registration unset (so the controller emits the
// well-known route only when explicitly requested).
func TestBuildInferenceServiceOfferSpec_NoRegisterOmitsRegistration(t *testing.T) {
	d := &inference.Deployment{
		Name: "aeon", UpstreamURL: "http://127.0.0.1:8000",
		WalletAddress: "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
		Chain:         "base-sepolia", PricePerRequest: "0.001",
	}
	reg, enabled, err := buildSellRegistrationConfig("aeon", sellRegistrationInput{NoRegister: true})
	if err != nil {
		t.Fatalf("buildSellRegistrationConfig: %v", err)
	}
	if enabled {
		t.Fatal("--no-register should disable registration")
	}
	spec, err := buildInferenceServiceOfferSpec(d, schemas.PriceTable{PerRequest: "0.001"}, "llm", "8402", schemas.AssetTerms{}, "aeon-ultimate", reg)
	if err != nil {
		t.Fatalf("buildInferenceServiceOfferSpec: %v", err)
	}
	if _, present := spec["registration"]; present {
		t.Errorf("spec.registration should be absent when --no-register; got %#v", spec["registration"])
	}
}

// TestBuildInferenceServiceOfferSpec_OperatorOverridesWin pins the fix for
// the controller-side description-clobber regression in
// internal/serviceoffercontroller/render.go: the operator's explicit
// registration fields (name, description, image, skills, domains) must
// survive into the published spec verbatim, not be silently replaced by
// controller-side defaults. This test covers the *spec input*; the
// controller-side guarantee is covered by the companion test in
// serviceoffercontroller/render_test.go.
func TestBuildInferenceServiceOfferSpec_OperatorOverridesWin(t *testing.T) {
	d := &inference.Deployment{
		Name: "aeon", UpstreamURL: "http://127.0.0.1:8000",
		WalletAddress: "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
		Chain:         "base-sepolia", PricePerRequest: "0.001",
	}
	reg, _, err := buildSellRegistrationConfig("aeon", sellRegistrationInput{
		Name:        "Qwen36 AEON Ultimate",
		Description: "Uncensored Qwen3.6-27B abliteration on DGX Spark",
		Image:       "https://example.com/aeon.png",
		Skills:      []string{"llm/inference", "llm/uncensored"},
		Domains:     []string{"inference.v1337.org"},
	})
	if err != nil {
		t.Fatalf("buildSellRegistrationConfig: %v", err)
	}
	spec, err := buildInferenceServiceOfferSpec(d, schemas.PriceTable{PerRequest: "0.001"}, "llm", "8402", schemas.AssetTerms{}, "aeon-ultimate", reg)
	if err != nil {
		t.Fatalf("buildInferenceServiceOfferSpec: %v", err)
	}
	r := spec["registration"].(map[string]any)
	for k, want := range map[string]any{
		"name":        "Qwen36 AEON Ultimate",
		"description": "Uncensored Qwen3.6-27B abliteration on DGX Spark",
		"image":       "https://example.com/aeon.png",
	} {
		if r[k] != want {
			t.Errorf("spec.registration.%s = %#v, want %#v", k, r[k], want)
		}
	}
	skills, _ := r["skills"].([]string)
	if len(skills) != 2 || skills[0] != "llm/inference" {
		t.Errorf("spec.registration.skills = %#v, want [llm/inference llm/uncensored]", r["skills"])
	}
	domains, _ := r["domains"].([]string)
	if len(domains) != 1 || domains[0] != "inference.v1337.org" {
		t.Errorf("spec.registration.domains = %#v, want [inference.v1337.org]", r["domains"])
	}
}

// TestBuildInferenceServiceOfferSpec_ModelNameNotHardcoded pins the fix for
// the "spec.model.name = ollama regardless of --model" regression. The model
// id the operator passed must surface in spec.model.name so the controller's
// per-model registration description (and any downstream tooling that keys
// off model name) reads the truth, not the historical hardcoded literal.
func TestBuildInferenceServiceOfferSpec_ModelNameNotHardcoded(t *testing.T) {
	d := &inference.Deployment{
		Name: "aeon", UpstreamURL: "http://127.0.0.1:8000",
		WalletAddress: "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
		Chain:         "base-sepolia", PricePerRequest: "0.001",
	}
	spec, err := buildInferenceServiceOfferSpec(d, schemas.PriceTable{PerRequest: "0.001"}, "llm", "8402", schemas.AssetTerms{}, "aeon-ultimate", nil)
	if err != nil {
		t.Fatalf("buildInferenceServiceOfferSpec: %v", err)
	}
	model, ok := spec["model"].(map[string]any)
	if !ok {
		t.Fatalf("spec.model missing or wrong type: %#v", spec["model"])
	}
	if model["name"] != "aeon-ultimate" {
		t.Errorf("spec.model.name = %v, want %q (must reflect --model, not the legacy hardcoded \"ollama\")", model["name"], "aeon-ultimate")
	}
}

// TestShouldAutoRegisterSell pins the decision logic shared by the http and
// inference action handlers. The bug it guards: leaving a fresh inference
// offer in `Registered=False AwaitingExternalRegistration` so that the
// controller's services.json filter (which requires Ready=True, which in
// turn requires Registered=True) silently excluded the offer from the
// operator's own storefront. Both call sites must auto-register exactly
// when the spec says enabled AND the tunnel is up — anything else is a
// regression.
func TestShouldAutoRegisterSell(t *testing.T) {
	tests := []struct {
		name      string
		spec      map[string]any
		tunnelURL string
		want      bool
	}{
		{
			name:      "registration enabled + tunnel up → register",
			spec:      map[string]any{"registration": map[string]any{"enabled": true}},
			tunnelURL: "https://inference.example.com",
			want:      true,
		},
		{
			name:      "registration explicitly disabled → skip",
			spec:      map[string]any{"registration": map[string]any{"enabled": false}},
			tunnelURL: "https://inference.example.com",
			want:      false,
		},
		{
			name:      "no registration block (sell http --no-register / pre-#485 sell inference) → skip",
			spec:      map[string]any{},
			tunnelURL: "https://inference.example.com",
			want:      false,
		},
		{
			name:      "registration enabled but tunnel down → skip (no endpoint to advertise)",
			spec:      map[string]any{"registration": map[string]any{"enabled": true}},
			tunnelURL: "",
			want:      false,
		},
		{
			name:      "registration block is not a map (defensive) → skip",
			spec:      map[string]any{"registration": "garbage"},
			tunnelURL: "https://inference.example.com",
			want:      false,
		},
		{
			name:      "registration.enabled is not a bool (defensive) → skip",
			spec:      map[string]any{"registration": map[string]any{"enabled": "yes"}},
			tunnelURL: "https://inference.example.com",
			want:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAutoRegisterSell(tc.spec, tc.tunnelURL)
			if got != tc.want {
				t.Errorf("shouldAutoRegisterSell = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSellInferenceAction_InvokesAutoRegister is a source-level guard
// against the specific regression the user just hit on spark2: the
// inference Action committed the ServiceOffer, ensured the tunnel, then
// jumped straight to runInferenceGateway without ever calling
// autoRegisterServiceOffer. Result: the offer stayed in
// AwaitingExternalRegistration and never reached the storefront feed.
// Without this guard, an innocent refactor of the post-create code path
// could silently remove the call again — and the only downstream signal
// would be "operator's storefront mysteriously empty", which is hard to
// attribute.
func TestSellInferenceAction_InvokesAutoRegister(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellInferenceCommand(")
	if start < 0 {
		t.Fatal("sellInferenceCommand not found in sell.go")
	}
	next := strings.Index(body[start+1:], "\nfunc ")
	if next < 0 {
		t.Fatal("could not delimit sellInferenceCommand body")
	}
	scope := body[start : start+1+next]
	for _, needle := range []string{
		"shouldAutoRegisterSell(",
		"autoRegisterServiceOffer(",
	} {
		if !strings.Contains(scope, needle) {
			t.Errorf("sellInferenceCommand body must contain %q — auto-register path missing, "+
				"offers will stay in AwaitingExternalRegistration and be excluded from /api/services.json", needle)
		}
	}
}

// TestSignerPayeeDelegationNote pins the contract that obol now treats the
// "registration signer != offer payTo" case as legitimate ERC-8004 ownership
// delegation rather than an error. The helper returns "" when the two match
// (or either is empty) and a single-line informational note otherwise.
//
// Regression context: autoRegisterServiceOffer used to fail with
//
//	registration signer 0xA... does not match the payment wallet 0xB...
//
// which read like an ERC-8004 spec constraint but was purely an obol-CLI
// policy. ERC-8004 explicitly supports the split (msg.sender at register
// time owns the agent; setAgentWallet re-points the wallet post-mint), and
// x402 settlement honors the offer's payTo regardless of what the registry
// reports.
func TestSignerPayeeDelegationNote(t *testing.T) {
	tests := []struct {
		name        string
		signer      string
		payTo       string
		wantNoteHas []string
		wantEmpty   bool
	}{
		{
			name:      "exact match → no note",
			signer:    "0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
			payTo:     "0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
			wantEmpty: true,
		},
		{
			name:      "case-insensitive match → no note",
			signer:    "0xa5d4af96e3e740383a36c3123a54724dacb3df57",
			payTo:     "0xA5D4AF96E3E740383A36C3123A54724DACB3DF57",
			wantEmpty: true,
		},
		{
			name:      "whitespace tolerance → no note",
			signer:    "  0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57  ",
			payTo:     "0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
			wantEmpty: true,
		},
		{
			name:      "empty payTo → no note (caller didn't request the check)",
			signer:    "0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
			payTo:     "",
			wantEmpty: true,
		},
		{
			name:      "empty signer → no note (impossible in practice; defensive)",
			signer:    "",
			payTo:     "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
			wantEmpty: true,
		},
		{
			name:   "true mismatch → note names both addrs and suggests sell update",
			signer: "0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
			payTo:  "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
			wantNoteHas: []string{
				"0xA5d4aF96E3e740383A36c3123A54724dAcB3Df57",
				"0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
				"obol sell update",
				"--pay-to",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := signerPayeeDelegationNote(tc.signer, tc.payTo)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("note = %q, want empty", got)
				}
				return
			}
			for _, sub := range tc.wantNoteHas {
				if !strings.Contains(got, sub) {
					t.Errorf("note missing %q: %s", sub, got)
				}
			}
		})
	}
}

// TestAutoRegister_AllowsSignerPayeeMismatch is the source-level guard against
// re-introducing the early-return error that used to reject ERC-8004
// registrations whenever signer != payTo. The error wording was specific
// ("registration signer ... does not match the payment wallet ..."); we grep
// the function body for it. Anyone who reintroduces that check — even with a
// different error message — must also delete this test, which is a deliberate
// nudge to read the rationale comment first.
func TestAutoRegister_AllowsSignerPayeeMismatch(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func autoRegisterServiceOffer(")
	if start < 0 {
		t.Fatal("autoRegisterServiceOffer not found in sell.go")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit autoRegisterServiceOffer body")
	}
	scope := body[start : start+1+end]
	for _, banned := range []string{
		`"registration signer %s does not match the payment wallet`,
		`does not match the payment wallet`,
	} {
		if strings.Contains(scope, banned) {
			t.Errorf("autoRegisterServiceOffer must NOT reject signer != payee — ERC-8004 allows "+
				"the split via setAgentWallet and x402 settlement honors payTo directly. "+
				"Banned snippet still present: %q", banned)
		}
	}
	// Positive guard: the soft-notice path must still be present, otherwise
	// the operator gets no signal that the addresses diverge.
	if !strings.Contains(scope, "signerPayeeDelegationNote(") {
		t.Error("autoRegisterServiceOffer must call signerPayeeDelegationNote(...) so operators see when signer ≠ payTo")
	}
}

// TestBuildSellUpdatePatch_PayToOnly pins the `obol sell update <name> --pay-to 0x...`
// shape. The user-facing promise is "change the payee in place" — the patch
// must touch only spec.payment.payTo, not the rest of the offer.
func TestBuildSellUpdatePatch_PayToOnly(t *testing.T) {
	const newPayee = "0xB00B00000000000000000000000000000000B00B"
	patch, err := buildSellUpdatePatch(newPayee, "", schemas.PriceTable{})
	if err != nil {
		t.Fatalf("buildSellUpdatePatch: %v", err)
	}
	spec, ok := patch["spec"].(map[string]any)
	if !ok {
		t.Fatalf("patch.spec missing or wrong type: %#v", patch)
	}
	payment, ok := spec["payment"].(map[string]any)
	if !ok {
		t.Fatalf("patch.spec.payment missing or wrong type: %#v", spec)
	}
	if payment["payTo"] != newPayee {
		t.Errorf("patch.spec.payment.payTo = %v, want %q", payment["payTo"], newPayee)
	}
	if _, present := payment["network"]; present {
		t.Errorf("--pay-to only patch should NOT touch payment.network; got %#v", payment["network"])
	}
	if _, present := payment["price"]; present {
		t.Errorf("--pay-to only patch should NOT touch payment.price; got %#v", payment["price"])
	}
}

// TestBuildSellUpdatePatch_PriceSwitchNullsOldKeys pins the switching contract:
// changing the price model (e.g. perRequest → perMTok) must explicitly null
// the unused keys so merge-patch semantics don't leave a stranded perRequest
// fighting the new perMTok.
func TestBuildSellUpdatePatch_PriceSwitchNullsOldKeys(t *testing.T) {
	tests := []struct {
		name   string
		price  schemas.PriceTable
		setKey string
	}{
		{"per-request set", schemas.PriceTable{PerRequest: "0.002"}, "perRequest"},
		{"per-mtok set", schemas.PriceTable{PerMTok: "5.0"}, "perMTok"},
		{"per-hour set", schemas.PriceTable{PerHour: "1.0"}, "perHour"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patch, err := buildSellUpdatePatch("", "", tc.price)
			if err != nil {
				t.Fatalf("buildSellUpdatePatch: %v", err)
			}
			price := patch["spec"].(map[string]any)["payment"].(map[string]any)["price"].(map[string]any)
			for _, key := range []string{"perRequest", "perMTok", "perHour"} {
				v := price[key]
				if key == tc.setKey {
					if v == nil {
						t.Errorf("price.%s = nil, want a value", key)
					}
				} else {
					if v != nil {
						t.Errorf("price.%s = %#v, want nil (so old key doesn't survive the merge)", key, v)
					}
				}
			}
		})
	}
}

// TestBuildSellUpdatePatch_NoFieldsErrors pins the no-op-protection contract:
// `obol sell update <name>` with no field flags must error rather than fire
// a no-op kubectl patch. The error message names the flags so the operator
// learns the surface from the failure.
func TestBuildSellUpdatePatch_NoFieldsErrors(t *testing.T) {
	_, err := buildSellUpdatePatch("", "", schemas.PriceTable{})
	if err == nil {
		t.Fatal("expected error when no update flags are set")
	}
	for _, sub := range []string{"--per-request", "--per-mtok", "--per-hour", "--pay-to", "--chain"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error must name flag %q so the operator learns the surface; got: %v", sub, err)
		}
	}
}

// TestSellUpdate_PayToFlagSurface pins the user-facing `obol sell update
// <name> --pay-to 0x...` flag wiring: the same payToFlag() shape used by
// inference + http (so a buyer can muscle-memory `-w` / `--wallet`), plus
// the namespace requirement (the resource is per-namespace, ambiguous
// otherwise).
func TestSellUpdate_PayToFlagSurface(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := sellCommand(cfg)
	update := findSubcommand(t, cmd, "update")
	flags := flagMap(update)

	requireFlags(t, flags,
		"namespace", "pay-to", "wallet", "recipient", "w",
		"chain", "price", "per-request", "per-mtok", "per-hour",
	)
	assertFlagRequired(t, flags, "namespace")
	assertFlagHasAlias(t, flags, "pay-to", "wallet")
	assertFlagHasAlias(t, flags, "pay-to", "recipient")
	assertFlagHasAlias(t, flags, "pay-to", "w")
}

// TestResumeSellOffers_EmptyStoreNoOp pins the "nothing to resume" path:
// stack-up against a workspace with no persisted sell-inference deployments
// must return nil without erroring. The same path also has to handle the
// no-cluster-yet case (no kubeconfig.yaml) gracefully — both are exercised
// by an empty ConfigDir.
func TestResumeSellOffers_EmptyStoreNoOp(t *testing.T) {
	cfg := newTestConfig(t)
	u := ui.New(false)
	if err := resumeSellOffers(context.Background(), cfg, u); err != nil {
		t.Fatalf("empty resume must succeed, got: %v", err)
	}
}

// TestResumeSellOffers_DescriptorPresentButNoCluster pins the
// "descriptors-on-disk-but-cluster-not-up-yet" path. Real-world example:
// `obol stack down`, then re-running `obol sell inference` somewhere that
// happens to fail before reaching cluster apply — the descriptor lands on
// disk, the cluster never comes back, and a subsequent `obol stack up`
// against a missing kubeconfig must NOT panic or hard-error. It should be
// a quiet skip until the cluster is available.
func TestResumeSellOffers_DescriptorPresentButNoCluster(t *testing.T) {
	cfg := newTestConfig(t)
	store := inference.NewStore(cfg.ConfigDir)
	if err := store.Create(&inference.Deployment{
		Name:             "aeon",
		ModelName:        "aeon-ultimate",
		ServiceNamespace: "llm",
		ListenAddr:       "0.0.0.0:8402",
		WalletAddress:    "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
		PricePerRequest:  "0.023",
		AssetSymbol:      "OBOL",
		Chain:            "base-sepolia",
	}, true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// No kubeconfig.yaml in cfg.ConfigDir — resume must not attempt
	// cluster operations.
	u := ui.New(false)
	if err := resumeSellOffers(context.Background(), cfg, u); err != nil {
		t.Fatalf("resume with descriptor + no cluster must skip cleanly, got: %v", err)
	}
}

// TestResumeOneInferenceOffer_RequiresModelName pins the per-offer error
// for legacy descriptors that were written before ModelName became a
// persisted field. The resume path cannot fabricate a model name out of
// thin air, and silently writing a ServiceOffer with no spec.model.name
// would surface as a controller "ModelReady=False" loop with no actionable
// signal. Operators need a clear "recreate the offer" message instead.
func TestResumeOneInferenceOffer_RequiresModelName(t *testing.T) {
	cfg := newTestConfig(t)
	u := ui.New(false)
	err := resumeOneInferenceOffer(cfg, u, &inference.Deployment{
		Name:             "legacy",
		ServiceNamespace: "llm",
		ListenAddr:       ":8402",
		// No ModelName.
	})
	if err == nil {
		t.Fatal("expected error for legacy descriptor with no ModelName")
	}
	for _, sub := range []string{"model_name", "obol sell inference"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error must name the missing field and the recovery command; missing %q: %v", sub, err)
		}
	}
}

// TestResumeOneInferenceOffer_NilDescriptor pins the defensive guard for
// the never-supposed-to-happen case of a nil or empty descriptor reaching
// the resume loop. A bug elsewhere that produces such an entry shouldn't
// panic the entire resume pass; one descriptor failing should not block
// the rest.
func TestResumeOneInferenceOffer_NilDescriptor(t *testing.T) {
	cfg := newTestConfig(t)
	u := ui.New(false)
	if err := resumeOneInferenceOffer(cfg, u, nil); err == nil {
		t.Fatal("expected error for nil descriptor")
	}
	if err := resumeOneInferenceOffer(cfg, u, &inference.Deployment{}); err == nil {
		t.Fatal("expected error for empty descriptor (no Name)")
	}
}

// TestStackUpAction_CallsResumeSellOffers is a source-level guard against
// silently regressing the stack-up → sell-resume wiring. The whole feature
// hinges on the `stack up` action handler calling resumeSellOffers after
// stack.Up succeeds; without that call, persisted sell-inference offers
// stay on disk forever and never reach the freshly-recreated cluster.
// A future refactor that splits the handler or moves the call must update
// this test, which forces a moment of "why is this here" attention.
func TestStackUpAction_CallsResumeSellOffers(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "resumeSellOffers(") {
		t.Fatal("cmd/obol/main.go must call resumeSellOffers — without it persisted sell-inference offers never reach a freshly-stacked cluster")
	}
	// Belt-and-suspenders: assert the call lives after stack.Up so the
	// kubeconfig + infrastructure are ready when resume runs.
	upIdx := strings.Index(body, "stack.Up(cfg")
	resumeIdx := strings.Index(body, "resumeSellOffers(")
	if upIdx < 0 || resumeIdx < 0 {
		t.Fatalf("expected both stack.Up and resumeSellOffers in main.go; upIdx=%d resumeIdx=%d", upIdx, resumeIdx)
	}
	if resumeIdx < upIdx {
		t.Error("resumeSellOffers must be invoked AFTER stack.Up — running it before the cluster is up will see no kubeconfig and skip every offer")
	}
}

// TestBuildResumeGatewayArgs pins the round-trip between an
// inference.Deployment on disk and the `obol sell inference` invocation
// the resume path uses to relaunch the host gateway. If a flag is dropped
// here, an offer that came back from disk would lose part of the
// operator's original intent — most painfully, registration metadata
// that fell out of the relaunch would leave the gateway running with
// stripped-down /.well-known/ content.
func TestBuildResumeGatewayArgs(t *testing.T) {
	tests := []struct {
		name      string
		d         *inference.Deployment
		wantSubs  []string
		wantNoSub []string
	}{
		{
			name: "full descriptor (the spark2 aeon offer)",
			d: &inference.Deployment{
				Name:            "aeon",
				ModelName:       "aeon-ultimate",
				UpstreamURL:     "http://127.0.0.1:8000",
				WalletAddress:   "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
				ListenAddr:      "0.0.0.0:8402",
				Chain:           "base-sepolia",
				AssetSymbol:     "OBOL",
				PricePerMTok:    "23",
				PricePerRequest: "0.023",
				FacilitatorURL:  "https://x402.gcp.obol.tech",
				Registration: map[string]any{
					"enabled":     true,
					"name":        "Qwen3.6-27B AEON Ultimate",
					"description": "Uncensored Qwen3.6-27B abliteration",
					"skills":      []any{"llm/inference", "llm/uncensored"},
					"domains":     []any{"inference.v1337.org"},
				},
			},
			wantSubs: []string{
				"sell", "inference", "aeon",
				"--model", "aeon-ultimate",
				"--upstream", "http://127.0.0.1:8000",
				"--pay-to", "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
				"--listen", "0.0.0.0:8402",
				"--chain", "base-sepolia",
				"--token", "OBOL",
				"--per-mtok", "23",
				"--facilitator", "https://x402.gcp.obol.tech",
				"--register-name", "Qwen3.6-27B AEON Ultimate",
				"--register-description", "Uncensored Qwen3.6-27B abliteration",
				"--register-skills", "llm/inference",
				"--register-skills", "llm/uncensored",
				"--register-domains", "inference.v1337.org",
			},
			wantNoSub: []string{
				"--price", // perMTok set, perRequest must not also be passed
				"--no-register",
				// the rc12+ spelling — replay must emit the spelling
				// every released CLI parses (--register-description)
				"--description",
			},
		},
		{
			name: "no-register descriptor",
			d: &inference.Deployment{
				Name:         "no-reg",
				ModelName:    "qwen3:0.6b",
				ListenAddr:   ":8402",
				Registration: map[string]any{"enabled": false},
			},
			wantSubs: []string{
				"--no-register",
			},
			wantNoSub: []string{
				"--register-name",
				"--description",
			},
		},
		{
			name: "legacy descriptor (no registration map at all)",
			d: &inference.Deployment{
				Name:            "legacy",
				ModelName:       "qwen3.5:9b",
				ListenAddr:      ":8402",
				PricePerRequest: "0.001",
			},
			wantSubs: []string{"--price", "0.001"},
			wantNoSub: []string{
				"--no-register",
				"--register-name",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildResumeGatewayArgs(tc.d)
			joined := strings.Join(got, " ")
			for _, want := range tc.wantSubs {
				found := false
				for _, a := range got {
					if a == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("buildResumeGatewayArgs missing %q in %v", want, got)
				}
			}
			for _, banned := range tc.wantNoSub {
				for _, a := range got {
					if a == banned {
						t.Errorf("buildResumeGatewayArgs unexpectedly contains %q in %v", banned, got)
					}
				}
			}
			// Order sanity: positional `<name>` comes before any flag.
			nameIdx := -1
			firstFlagIdx := -1
			for i, a := range got {
				if a == tc.d.Name && nameIdx < 0 {
					nameIdx = i
				}
				if strings.HasPrefix(a, "--") && firstFlagIdx < 0 {
					firstFlagIdx = i
				}
			}
			if nameIdx < 0 || firstFlagIdx < 0 || nameIdx > firstFlagIdx {
				t.Errorf("name positional must come before flags; got %v", joined)
			}
		})
	}
}

// TestResumeGatewayBinaryPrefersRunningExecutable pins the binary-skew
// fix from the spark2 reboot test: buildResumeGatewayArgs encodes the
// running version's flag surface, so the relaunch must spawn the running
// executable — even when an installed (possibly older) obol exists at
// BinDir. Spawning the BinDir copy is how the live relaunch died with
// "flag provided but not defined: -description" against rc11.
func TestResumeGatewayBinaryPrefersRunningExecutable(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "obol"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake installed obol: %v", err)
	}
	cfg := &config.Config{BinDir: binDir}

	got, err := resumeGatewayBinary(cfg)
	if err != nil {
		t.Fatalf("resumeGatewayBinary: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if got != exe {
		t.Errorf("resumeGatewayBinary = %q; want running executable %q (BinDir copy must not win)", got, exe)
	}
}

// TestResumeGatewayEnviron pins the replay marker on the spawned gateway
// environment. Without it, validation added after a descriptor was
// persisted (the slash-in-model rule) rejects the replayed flags and the
// offer can never resume — the second failure mode from the spark2
// reboot test.
func TestResumeGatewayEnviron(t *testing.T) {
	want := resumeReplayEnv + "=1"
	for _, kv := range resumeGatewayEnviron() {
		if kv == want {
			return
		}
	}
	t.Errorf("resumeGatewayEnviron() missing %q", want)
}

// TestValidateSellInferenceModelName pins the two-mode behavior of the
// slash-in-model rule: hard error for new offers, warning-only for
// resume replays of descriptors that predate the rule.
func TestValidateSellInferenceModelName(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		replay     bool
		wantErr    bool
		wantWarn   bool
		wantInText string
	}{
		{"clean name, new offer", "aeon-ultimate", false, false, false, ""},
		{"clean name, replay", "aeon-ultimate", true, false, false, ""},
		{"slash, new offer rejected", "AEON-7/Qwen3.6", false, true, false, "AEON-7--Qwen3.6"},
		{"slash, replay tolerated with warning", "AEON-7/Qwen3.6", true, false, true, "resume replay"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := validateSellInferenceModelName(tc.model, tc.replay)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v; wantErr = %v", err, tc.wantErr)
			}
			if (warn != "") != tc.wantWarn {
				t.Fatalf("warn = %q; wantWarn = %v", warn, tc.wantWarn)
			}
			if tc.wantInText != "" {
				combined := warn
				if err != nil {
					combined += err.Error()
				}
				if !strings.Contains(combined, tc.wantInText) {
					t.Errorf("output %q missing %q", combined, tc.wantInText)
				}
			}
		})
	}
}

// TestWaitForClusterAPI pins the two fast paths of the boot-race wait:
// no kubeconfig means no cluster and returns nil immediately; an
// unreachable cluster surfaces an error once the deadline passes
// instead of blocking resume forever.
func TestWaitForClusterAPI(t *testing.T) {
	u := ui.New(false)

	t.Run("no kubeconfig returns nil immediately", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir(), BinDir: t.TempDir()}
		if err := waitForClusterAPI(context.Background(), cfg, u, 0); err != nil {
			t.Fatalf("expected nil without kubeconfig, got %v", err)
		}
	})

	t.Run("unreachable cluster errors after deadline", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "kubeconfig.yaml"), []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}
		cfg := &config.Config{ConfigDir: dir, BinDir: t.TempDir()}
		if err := waitForClusterAPI(context.Background(), cfg, u, 0); err == nil {
			t.Fatal("expected error for unreachable cluster with zero timeout")
		}
	})
}

// TestReadGatewayPID pins the on-disk PID file format. The file must be a
// single decimal integer in ASCII (no JSON, no trailing newlines that
// confuse trivial readers) so other tools — `tail -f .../gateway.log` and
// `kill $(cat .../gateway.pid)` — work without parsing helpers.
func TestReadGatewayPID(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		wantPID int
		wantOK  bool
	}{
		{"clean integer", "12345", 12345, true},
		{"trailing newline tolerated", "12345\n", 12345, true},
		{"surrounding whitespace tolerated", "  12345  \n", 12345, true},
		{"zero rejected", "0", 0, false},
		{"negative rejected", "-1", 0, false},
		{"non-numeric rejected", "abc", 0, false},
		{"empty rejected", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".pid")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			pid, ok := readGatewayPID(path)
			if pid != tc.wantPID || ok != tc.wantOK {
				t.Errorf("readGatewayPID(%q) = (%d, %v); want (%d, %v)", tc.content, pid, ok, tc.wantPID, tc.wantOK)
			}
		})
	}

	t.Run("missing file → (0, false)", func(t *testing.T) {
		pid, ok := readGatewayPID(filepath.Join(dir, "does-not-exist"))
		if pid != 0 || ok {
			t.Errorf("readGatewayPID(missing) = (%d, %v); want (0, false)", pid, ok)
		}
	})
}

// TestProcessAlive_SelfAndBogus pins the liveness probe used to decide
// whether the previous gateway is still running. The current process is
// trivially alive; a far-out-of-range PID is not. We don't test
// "definitely-dead-but-recently-existed" cases because those depend on
// the kernel's PID-reuse window and would be flaky.
func TestProcessAlive_SelfAndBogus(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("processAlive(self) must be true")
	}
	if processAlive(99_999_999) {
		t.Error("processAlive(absurd pid) must be false")
	}
}

// TestPersistSellHTTPOffer_RoundTrip pins the on-disk manifest contract.
// Anything the persistence layer writes must come back identically from
// the resume path (which reads it via yaml.Unmarshal + kubectl-apply).
// The path layout — <ConfigDir>/sell-http/<namespace>__<name>.yaml — is
// the second half of the contract: two offers with the same name in
// different namespaces must never collide on disk.
func TestPersistSellHTTPOffer_RoundTrip(t *testing.T) {
	cfg := newTestConfig(t)
	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata": map[string]any{
			"name":      "my-api",
			"namespace": "default",
		},
		"spec": map[string]any{
			"type": "http",
			"upstream": map[string]any{
				"service": "my-svc", "namespace": "default", "port": int64(8080), "healthPath": "/health",
			},
			"payment": map[string]any{
				"scheme":  "exact",
				"network": "base-sepolia",
				"payTo":   "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
				"price":   map[string]any{"perRequest": "0.001"},
			},
		},
	}
	if err := persistServiceOffer(cfg, "default", "my-api", manifest); err != nil {
		t.Fatalf("persistServiceOffer: %v", err)
	}

	expected := filepath.Join(cfg.ConfigDir, "sell-http", "default__my-api.yaml")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("expected manifest at %s: %v", expected, err)
	}
	if len(data) == 0 {
		t.Fatal("manifest file is empty")
	}
	// Round-trip: parse, verify metadata.name + spec.payment.payTo survived.
	var round map[string]any
	if err := yaml.Unmarshal(data, &round); err != nil {
		t.Fatalf("YAML unmarshal: %v\n%s", err, data)
	}
	ns, name := manifestNSName(round)
	if ns != "default" || name != "my-api" {
		t.Errorf("round-tripped metadata = %s/%s, want default/my-api", ns, name)
	}
	payTo := round["spec"].(map[string]any)["payment"].(map[string]any)["payTo"]
	if payTo != "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47" {
		t.Errorf("round-tripped payTo = %v, want operator-supplied", payTo)
	}
}

// TestPersistSellHTTPOffer_NamespaceIsolation pins that two offers with
// the same name in different namespaces produce distinct files. A
// careless filename scheme would have the second `obol sell http`
// silently overwrite the first.
func TestPersistSellHTTPOffer_NamespaceIsolation(t *testing.T) {
	cfg := newTestConfig(t)
	stub := func(ns string) map[string]any {
		return map[string]any{
			"apiVersion": "obol.org/v1alpha1",
			"kind":       "ServiceOffer",
			"metadata":   map[string]any{"name": "shared", "namespace": ns},
			"spec":       map[string]any{"type": "http"},
		}
	}
	if err := persistServiceOffer(cfg, "team-a", "shared", stub("team-a")); err != nil {
		t.Fatalf("persist team-a: %v", err)
	}
	if err := persistServiceOffer(cfg, "team-b", "shared", stub("team-b")); err != nil {
		t.Fatalf("persist team-b: %v", err)
	}
	for _, ns := range []string{"team-a", "team-b"} {
		path := filepath.Join(cfg.ConfigDir, "sell-http", ns+"__shared.yaml")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
}

// TestRemoveSellHTTPOffer_DropsPersistedManifest pins the symmetric
// teardown: `obol sell delete` must remove the on-disk manifest so the
// next stack-up doesn't re-create an offer the operator intentionally
// deleted. The function must also be a quiet no-op for missing files —
// running it twice (or against an offer that was never persisted) must
// not error.
func TestRemoveSellHTTPOffer_DropsPersistedManifest(t *testing.T) {
	cfg := newTestConfig(t)
	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
		"metadata": map[string]any{"name": "doomed", "namespace": "llm"},
	}
	if err := persistServiceOffer(cfg, "llm", "doomed", manifest); err != nil {
		t.Fatalf("persist: %v", err)
	}
	path := filepath.Join(cfg.ConfigDir, "sell-http", "llm__doomed.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest must exist before remove: %v", err)
	}
	if err := removePersistedServiceOffer(cfg, "llm", "doomed"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("manifest still exists after remove: %v", err)
	}
	// Idempotent: removing the missing file must not error.
	if err := removePersistedServiceOffer(cfg, "llm", "doomed"); err != nil {
		t.Errorf("second remove must be no-op, got: %v", err)
	}
	// Defensive: empty inputs are silent no-ops, not panics.
	if err := removePersistedServiceOffer(cfg, "", "foo"); err != nil {
		t.Errorf("empty namespace: %v", err)
	}
	if err := removePersistedServiceOffer(cfg, "llm", ""); err != nil {
		t.Errorf("empty name: %v", err)
	}
}

// TestResumeSellHTTPOffers_EmptyStoreNoOp pins the "no http offers yet"
// path: stack-up against a workspace that's never persisted an http
// offer returns nil without erroring. Same shape as the inference
// equivalent — important because resumeSellOffers calls both even when
// only one store has entries.
func TestResumeSellHTTPOffers_EmptyStoreNoOp(t *testing.T) {
	cfg := newTestConfig(t)
	// Need a kubeconfig.yaml present, otherwise the function bails early
	// (correctly — no cluster). For the "store empty but cluster up"
	// case we want to verify it doesn't error.
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("seed kubeconfig: %v", err)
	}
	if err := resumePersistedServiceOffers(cfg, ui.New(false)); err != nil {
		t.Errorf("empty-store resume must succeed: %v", err)
	}
}

// TestSellDeleteAction_CallsRemoveSellHTTPOffer is a source-level guard
// against the obvious post-delete leak: forget to call
// removePersistedServiceOffer in the sell delete handler and the next
// `obol stack up` resurrects the offer the operator just killed. The
// only signal would be "deleted offers spookily come back" which is
// hard to attribute, hence the test.
func TestSellDeleteAction_CallsRemoveSellHTTPOffer(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellDeleteCommand(")
	if start < 0 {
		t.Fatal("sellDeleteCommand not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit sellDeleteCommand body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, "removePersistedServiceOffer(") {
		t.Fatal("sellDeleteCommand must call removePersistedServiceOffer — otherwise the on-disk manifest survives the kubectl delete and `obol stack up` resurrects the offer")
	}
}

// TestResumeSellOffers_HTTPOnlyStore pins that http offers are still
// resumed when the inference store is completely empty. The original
// resumeSellOffers short-circuited on `len(deployments) == 0` before
// reaching the http branch; this test catches a regression that
// reintroduces that early return.
func TestResumeSellOffers_HTTPOnlyStore(t *testing.T) {
	cfg := newTestConfig(t)
	// Seed a kubeconfig so resume gets past the no-cluster guard.
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("seed kubeconfig: %v", err)
	}
	// No inference offers. One http offer present. Function must walk
	// the http branch without erroring on the missing inference store.
	manifest := map[string]any{
		"apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
		"metadata": map[string]any{"name": "only-http", "namespace": "llm"},
	}
	if err := persistServiceOffer(cfg, "llm", "only-http", manifest); err != nil {
		t.Fatalf("persist: %v", err)
	}
	// kubectl apply will fail against the fake kubeconfig — that's
	// expected and reported as a warn, not an error. We just need
	// resumeSellOffers itself to return nil.
	if err := resumeSellOffers(context.Background(), cfg, ui.New(false)); err != nil {
		t.Errorf("http-only store must not error from resumeSellOffers: %v", err)
	}
}

func TestBuildDemoServiceOffer_RegisterFlagDrivesEnabled(t *testing.T) {
	for _, register := range []bool{true, false} {
		manifest := buildDemoServiceOffer(
			"demo-hello", "demo", "base-sepolia",
			"0x1111111111111111111111111111111111111111",
			"0.00001",
			register,
			demoSpec{Type: "hello", Description: "echo"},
			schemas.AssetTerms{},
		)
		registration := manifest["spec"].(map[string]any)["registration"].(map[string]any)
		if registration["enabled"] != register {
			t.Errorf("register=%v: registration.enabled = %v, want %v", register, registration["enabled"], register)
		}
	}
}

func TestBuildDemoServiceOffer_USDCOmitsAssetBlock(t *testing.T) {
	// USDC is the chain default; AssetTerms is zero, so the manifest must NOT
	// include a payment.asset block (the verifier falls back to chain default).
	manifest := buildDemoServiceOffer(
		"demo-hello", "demo", "base-sepolia",
		"0x1111111111111111111111111111111111111111",
		"0.00001",
		true,
		demoSpec{Type: "hello", Description: "echo"},
		schemas.AssetTerms{},
	)
	payment := manifest["spec"].(map[string]any)["payment"].(map[string]any)
	if _, ok := payment["asset"]; ok {
		t.Fatalf("expected no payment.asset block when asset is zero, got: %v", payment["asset"])
	}
	if payment["network"] != "base-sepolia" {
		t.Errorf("network = %v, want base-sepolia", payment["network"])
	}
}

func TestBuildDemoServiceOffer_OBOLIncludesAssetBlock(t *testing.T) {
	// Selling for OBOL on Ethereum mainnet must populate the full asset block
	// so the verifier and storefront know which token to enforce.
	asset := schemas.AssetTerms{
		Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
		Symbol:         "OBOL",
		Decimals:       18,
		TransferMethod: schemas.AssetTransferMethodPermit2,
		EIP712Name:     "Obol Network",
		EIP712Version:  "1",
	}
	manifest := buildDemoServiceOffer(
		"demo-quant", "demo", "ethereum",
		"0x2222222222222222222222222222222222222222",
		"0.001",
		true,
		demoSpec{Type: "quant", Description: "agent driven analysis"},
		asset,
	)
	payment := manifest["spec"].(map[string]any)["payment"].(map[string]any)
	got, ok := payment["asset"].(schemas.AssetTerms)
	if !ok {
		t.Fatalf("payment.asset missing or wrong type: %T %v", payment["asset"], payment["asset"])
	}
	if got.Symbol != "OBOL" {
		t.Errorf("asset.Symbol = %q, want OBOL", got.Symbol)
	}
	if got.TransferMethod != schemas.AssetTransferMethodPermit2 {
		t.Errorf("asset.TransferMethod = %q, want %q", got.TransferMethod, schemas.AssetTransferMethodPermit2)
	}
	if payment["network"] != "ethereum" {
		t.Errorf("network = %v, want ethereum", payment["network"])
	}
}

func TestBuildDemoResources_UsesImportedImageAndERPCPath(t *testing.T) {
	resources := buildDemoResources("demo-blocks", demoSpec{Type: "blocks", NeedsERPC: true}, "base-sepolia")
	deploy := resources[1]
	spec := deploy["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	podSpec := template["spec"].(map[string]any)
	container := podSpec["containers"].([]map[string]any)[0]

	if got := container["imagePullPolicy"]; got != "IfNotPresent" {
		t.Fatalf("imagePullPolicy = %v, want IfNotPresent", got)
	}

	env := container["env"].([]map[string]string)
	for _, kv := range env {
		if kv["name"] == "ERPC_URL" {
			if kv["value"] != "http://erpc.erpc.svc.cluster.local/rpc/base-sepolia" {
				t.Fatalf("ERPC_URL = %q", kv["value"])
			}
			return
		}
	}

	t.Fatal("ERPC_URL not set for chain-backed demo")
}

// TestSellResumeCommand_Registered pins `obol sell resume` as a
// first-class subcommand. The resume path used to be reachable only via
// `obol stack up`, which never runs after a host reboot (Docker's
// restart policy resurrects the k3d cluster by itself) — so persisted
// sell-inference offers sat at UpstreamHealthy=False with an empty
// public catalog until the operator manually re-ran stack-up. Dropping
// this registration silently re-opens that gap.
func TestSellResumeCommand_Registered(t *testing.T) {
	cfg := newTestConfig(t)
	for _, sub := range sellCommand(cfg).Commands {
		if sub.Name != "resume" {
			continue
		}
		for _, f := range sub.Flags {
			for _, n := range f.Names() {
				if n == "install-boot-unit" {
					return
				}
			}
		}
		t.Fatal("sell resume must expose --install-boot-unit for reboot persistence")
	}
	t.Fatal("`obol sell resume` is not registered under `obol sell`")
}

// TestRenderResumeBootUnit pins the systemd unit emitted by
// `obol sell resume --install-boot-unit`. Each assertion guards a
// production behavior: ExecStart is the actual resume entrypoint,
// OBOL_CONFIG_DIR pins the unit to the stack that installed it,
// default.target makes it run at (lingering) login/boot, the
// pre-start sleep gives the Docker-restarted k3d API server time to
// accept the resume path's kubectl applies, and RemainAfterExit keeps
// the unit's cgroup alive — without it systemd kills the detached
// gateway the moment the oneshot finishes (live reboot-test failure).
func TestRenderResumeBootUnit(t *testing.T) {
	unit := renderResumeBootUnit("/home/op/.local/bin/obol", "/home/op/.config/obol")
	for _, want := range []string{
		"ExecStart=/home/op/.local/bin/obol sell resume",
		"Environment=OBOL_CONFIG_DIR=/home/op/.config/obol",
		"WantedBy=default.target",
		"ExecStartPre=/bin/sleep",
		"After=network-online.target docker.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("boot unit missing %q:\n%s", want, unit)
		}
	}
}

// TestInstallResumeBootUnit_PlatformBehavior pins both GOOS branches:
// non-Linux must refuse with actionable guidance (launchd is not wired
// up), Linux must write the unit file under $HOME even when systemctl
// is unavailable — the on-disk unit is the durable artifact and the
// systemctl enable steps are best-effort warnings.
func TestInstallResumeBootUnit_PlatformBehavior(t *testing.T) {
	cfg := newTestConfig(t)
	u := ui.New(false)

	if runtime.GOOS != "linux" {
		if err := installResumeBootUnit(cfg, u); err == nil {
			t.Fatal("non-Linux install must return an error, got nil")
		}
		return
	}

	t.Setenv("HOME", t.TempDir())
	if err := installResumeBootUnit(cfg, u); err != nil {
		t.Fatalf("linux install must succeed even without systemctl: %v", err)
	}
	home, _ := os.UserHomeDir()
	unitPath := filepath.Join(home, ".config", "systemd", "user", resumeBootUnitName)
	body, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("unit file not written: %v", err)
	}
	if !strings.Contains(string(body), "sell resume") {
		t.Errorf("unit file does not invoke sell resume:\n%s", body)
	}
}

// TestLoadPersistedServiceOffers_MixedTypes pins the ledger walk that
// `obol sell resume` relies on: every *.yaml ServiceOffer manifest is
// returned regardless of spec.type (http, agent, legacy files persisted
// before the type was recorded), while corrupt YAML, non-YAML files,
// and subdirectories are skipped without failing the walk.
func TestLoadPersistedServiceOffers_MixedTypes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("llm__api.yaml", "apiVersion: obol.org/v1alpha1\nkind: ServiceOffer\nmetadata:\n  name: api\n  namespace: llm\nspec:\n  type: http\n")
	write("agent-quant__quant.yaml", "apiVersion: obol.org/v1alpha1\nkind: ServiceOffer\nmetadata:\n  name: quant\n  namespace: agent-quant\nspec:\n  type: agent\n")
	write("llm__legacy.yaml", "apiVersion: obol.org/v1alpha1\nkind: ServiceOffer\nmetadata:\n  name: legacy\n  namespace: llm\n")
	write("corrupt.yaml", "::: not yaml {{{")
	write("notes.txt", "not a manifest")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	offers, err := loadPersistedServiceOffers(dir, ui.New(false))
	if err != nil {
		t.Fatalf("loadPersistedServiceOffers: %v", err)
	}

	got := map[string]string{} // "ns/name" -> label
	for _, o := range offers {
		got[o.Namespace+"/"+o.Name] = o.label()
	}
	want := map[string]string{
		"agent-quant/quant": "sell-agent",
		"llm/api":           "sell-http",
		"llm/legacy":        "sell",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d offers (%v), want %d", len(got), got, len(want))
	}
	for k, label := range want {
		if got[k] != label {
			t.Errorf("offer %s label = %q, want %q", k, got[k], label)
		}
	}

	// Missing dir: no offers, no error — first run on a fresh host.
	none, err := loadPersistedServiceOffers(filepath.Join(dir, "missing"), ui.New(false))
	if err != nil || len(none) != 0 {
		t.Fatalf("missing dir: offers=%v err=%v, want empty/nil", none, err)
	}
}

// TestSellAgentPaths_PersistOffers is the source-level guard that BOTH
// agent-offer creation sites — `obol sell agent <name>` and the
// agent-backed demo flow — persist the rendered manifest into the
// ledger. Without it, agent offers silently drop out of `obol sell
// resume` coverage while http and inference offers come back, which is
// exactly the inconsistency this ledger exists to prevent.
func TestSellAgentPaths_PersistOffers(t *testing.T) {
	src, err := os.ReadFile("sell_agent.go")
	if err != nil {
		t.Fatalf("read sell_agent.go: %v", err)
	}
	body := string(src)
	for _, fn := range []string{"func sellAgentCommand(", "func runAgentBackedDemo("} {
		start := strings.Index(body, fn)
		if start < 0 {
			t.Fatalf("%s not found", fn)
		}
		end := strings.Index(body[start+1:], "\nfunc ")
		scope := body[start:]
		if end >= 0 {
			scope = body[start : start+1+end]
		}
		if !strings.Contains(scope, "persistServiceOffer(") {
			t.Errorf("%s must call persistServiceOffer so agent offers are covered by `obol sell resume`", fn)
		}
	}
}

// TestRemovePersistedServiceOffersInNamespace pins the `obol agent
// delete` cleanup: removing one agent's namespace drops exactly that
// namespace's ledger entries and leaves every other offer alone.
func TestRemovePersistedServiceOffersInNamespace(t *testing.T) {
	cfg := newTestConfig(t)
	manifest := func(ns, name string) map[string]any {
		return map[string]any{
			"apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
			"metadata": map[string]any{"name": name, "namespace": ns},
			"spec":     map[string]any{"type": "agent"},
		}
	}
	if err := persistServiceOffer(cfg, "agent-quant", "quant", manifest("agent-quant", "quant")); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := persistServiceOffer(cfg, "llm", "api", manifest("llm", "api")); err != nil {
		t.Fatalf("persist: %v", err)
	}

	removed, err := removePersistedServiceOffersInNamespace(cfg, "agent-quant")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(sellOfferStorePath(cfg, "agent-quant", "quant")); !os.IsNotExist(err) {
		t.Error("agent-quant manifest must be gone")
	}
	if _, err := os.Stat(sellOfferStorePath(cfg, "llm", "api")); err != nil {
		t.Errorf("llm manifest must survive: %v", err)
	}

	// Empty namespace and nil cfg are no-ops, not panics.
	if n, err := removePersistedServiceOffersInNamespace(cfg, ""); n != 0 || err != nil {
		t.Errorf("empty ns: n=%d err=%v", n, err)
	}
	if n, err := removePersistedServiceOffersInNamespace(nil, "agent-quant"); n != 0 || err != nil {
		t.Errorf("nil cfg: n=%d err=%v", n, err)
	}
}

// TestDeleteCRDAgent_CleansPersistedOffers is the source-level guard
// that `obol agent delete` (a) deletes the agent's ServiceOffer CRs
// in-cluster — nothing else does: the agent finalizer leaves the
// namespace and offers, and a surviving offer would reconcile back to
// Ready (paying the DELETED agent's wallet) if the name is ever reused
// — and (b) withdraws their ledger entries so resume doesn't replay
// ghosts. The ledger sweep must live in the cluster-reachable branch:
// when the cluster is unreachable the CRs survive and the ledger must
// keep covering them.
func TestDeleteCRDAgent_CleansPersistedOffers(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func deleteCRDAgent(")
	if start < 0 {
		t.Fatal("deleteCRDAgent not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit deleteCRDAgent body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, `"delete", "serviceoffers.obol.org", "--all", "-n", ns`) {
		t.Fatal("deleteCRDAgent must delete the namespace's ServiceOffer CRs — the agent finalizer doesn't, and survivors resurrect against a recreated agent")
	}
	if !strings.Contains(scope, "removePersistedServiceOffersInNamespace(") {
		t.Fatal("deleteCRDAgent must call removePersistedServiceOffersInNamespace — otherwise resume replays offers for deleted agents forever")
	}
	unreachable := strings.Index(scope, "Cluster unreachable")
	sweep := strings.Index(scope, "removePersistedServiceOffersInNamespace(")
	if unreachable >= 0 && sweep > unreachable {
		t.Fatal("ledger sweep must run in the cluster-reachable branch only — when the cluster is unreachable the CRs survive and the ledger must keep covering them")
	}
}

// TestLoadPersistedServiceOffers_DemoListBundle pins the demo-bundle
// shape: the legacy demo persists a v1 List (namespace + backend +
// ServiceOffer). The loader must key it from the List metadata and
// report the inner offer's spec.type.
func TestLoadPersistedServiceOffers_DemoListBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := `apiVersion: v1
kind: List
metadata:
  name: hello
  namespace: demo
items:
  - apiVersion: v1
    kind: Namespace
    metadata:
      name: demo
  - apiVersion: obol.org/v1alpha1
    kind: ServiceOffer
    metadata:
      name: hello
      namespace: demo
    spec:
      type: http
`
	if err := os.WriteFile(filepath.Join(dir, "demo__hello.yaml"), []byte(bundle), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	offers, err := loadPersistedServiceOffers(dir, ui.New(false))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("got %d offers, want 1", len(offers))
	}
	o := offers[0]
	if o.Namespace != "demo" || o.Name != "hello" || o.label() != "sell-http" {
		t.Errorf("got %s/%s label=%s, want demo/hello label=sell-http", o.Namespace, o.Name, o.label())
	}
}

// TestSellDemoCommand_PersistsBundle is the source-level guard that the
// legacy demo path persists its bundle for resume coverage.
func TestSellDemoCommand_PersistsBundle(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellDemoCommand(")
	if start < 0 {
		t.Fatal("sellDemoCommand not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit sellDemoCommand body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, "persistServiceOffer(") {
		t.Fatal("sellDemoCommand must persist its demo bundle so `obol sell resume` restores a working demo")
	}
}

// TestSellUpdateCommand_RefreshesLedger is the source-level guard for
// the staleness bug class: `sell update` patches the live CR, and
// without a ledger refresh the next resume kubectl-applies the OLD
// payment terms back — silently reverting an intentional payTo or
// price change.
func TestSellUpdateCommand_RefreshesLedger(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellUpdateCommand(")
	if start < 0 {
		t.Fatal("sellUpdateCommand not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit sellUpdateCommand body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, "refreshPersistedServiceOffer(") {
		t.Fatal("sellUpdateCommand must call refreshPersistedServiceOffer after the patch — else resume reverts updated payment terms")
	}
}

// TestSellStopCommand_RefreshesLedger mirrors the sell-update guard for
// the drain path: without a ledger refresh, an etcd-wiping
// stack-down/up replays the pre-drain manifest and a deliberately
// stopped offer comes back fully live.
func TestSellStopCommand_RefreshesLedger(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellStopCommand(")
	if start < 0 {
		t.Fatal("sellStopCommand not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit sellStopCommand body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, "refreshPersistedServiceOffer(") {
		t.Fatal("sellStopCommand must refresh the ledger after the drain patch — else resume resurrects a stopped offer fully live")
	}
}

// TestSellDeleteCommand_TombstonesInference is the source-level guard
// for the inference half of delete⇒no-resume: the descriptor survives
// for list/status history, so without a tombstone the resume loop
// rebuilds the offer and relaunches the host gateway the operator just
// deleted.
func TestSellDeleteCommand_TombstonesInference(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func sellDeleteCommand(")
	if start < 0 {
		t.Fatal("sellDeleteCommand not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit sellDeleteCommand body")
	}
	scope := body[start : start+1+end]
	if !strings.Contains(scope, "DeletedAt") {
		t.Fatal("sellDeleteCommand must tombstone the inference descriptor (DeletedAt) — else resume undoes the delete")
	}
}

// TestActiveInferenceDeployments pins the resume-side tombstone filter.
func TestActiveInferenceDeployments(t *testing.T) {
	live := &inference.Deployment{Name: "live"}
	dead := &inference.Deployment{Name: "dead", DeletedAt: "2026-06-10T00:00:00Z"}
	got := activeInferenceDeployments([]*inference.Deployment{live, dead, nil})
	if len(got) != 1 || got[0].Name != "live" {
		t.Fatalf("activeInferenceDeployments = %v, want [live]", got)
	}
}

// TestAgentOfferBundle_RoundTrip pins the agent persist shape: a v1
// List carrying the agent NAMESPACE (so replay after stack recreation
// can land at all) plus the offer — and explicitly NOT an Agent CR
// (replaying one would mint a fresh wallet). The ledger loader must key
// it by List metadata and label it from the inner offer's type.
func TestAgentOfferBundle_RoundTrip(t *testing.T) {
	offer := map[string]any{
		"apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
		"metadata": map[string]any{"name": "quant", "namespace": "agent-quant"},
		"spec":     map[string]any{"type": "agent"},
	}
	bundle := agentOfferBundle("agent-quant", "quant", offer)

	items, _ := bundle["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("bundle has %d items, want 2 (Namespace + ServiceOffer)", len(items))
	}
	nsItem := items[0].(map[string]any)
	nsMeta, _ := nsItem["metadata"].(map[string]any)
	if nsItem["kind"] != "Namespace" || nsMeta["name"] != "agent-quant" {
		t.Errorf("first item must be the agent namespace, got %v", nsItem)
	}
	labels, _ := nsMeta["labels"].(map[string]any)
	if labels["obol.org/agent-namespace"] != "true" {
		t.Error("namespace must carry the obol.org/agent-namespace label")
	}
	for _, it := range items {
		if it.(map[string]any)["kind"] == "Agent" {
			t.Fatal("bundle must NOT contain an Agent CR — replaying one mints a fresh wallet")
		}
	}

	cfg := newTestConfig(t)
	if err := persistServiceOffer(cfg, "agent-quant", "quant", bundle); err != nil {
		t.Fatalf("persist: %v", err)
	}
	offers, err := loadPersistedServiceOffers(sellOfferStoreDir(cfg), ui.New(false))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("got %d offers, want 1", len(offers))
	}
	o := offers[0]
	if o.Namespace != "agent-quant" || o.Name != "quant" || o.label() != "sell-agent" {
		t.Errorf("got %s/%s label=%s, want agent-quant/quant label=sell-agent", o.Namespace, o.Name, o.label())
	}
}
