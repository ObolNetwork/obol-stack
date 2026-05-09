package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
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
	)

	assertStringDefault(t, flags, "price", "0.001")
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
