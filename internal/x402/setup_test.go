package x402

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRouteOption_WithPayTo(t *testing.T) {
	r := RouteRule{Pattern: "/rpc/*", Price: "0.001", Description: "RPC"}
	opt := WithPayTo("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	opt(&r)

	if r.PayTo != "0xABCDEF1234567890ABCDEF1234567890ABCDEF12" {
		t.Errorf("PayTo = %q, want 0xABCDEF...", r.PayTo)
	}
	// Other fields unchanged.
	if r.Pattern != "/rpc/*" {
		t.Errorf("Pattern mutated: %q", r.Pattern)
	}
	if r.Network != "" {
		t.Errorf("Network should remain empty, got %q", r.Network)
	}
}

func TestRouteOption_WithNetwork(t *testing.T) {
	r := RouteRule{Pattern: "/inference/*", Price: "0.01", Description: "Inference"}
	opt := WithNetwork("base")
	opt(&r)

	if r.Network != "base" {
		t.Errorf("Network = %q, want base", r.Network)
	}
	if r.PayTo != "" {
		t.Errorf("PayTo should remain empty, got %q", r.PayTo)
	}
}

func TestRouteOption_Multiple(t *testing.T) {
	r := RouteRule{Pattern: "/api/*", Price: "0.005", Description: "API"}
	opts := []RouteOption{
		WithPayTo("0x1111111111111111111111111111111111111111"),
		WithNetwork("base-sepolia"),
	}
	for _, opt := range opts {
		opt(&r)
	}

	if r.PayTo != "0x1111111111111111111111111111111111111111" {
		t.Errorf("PayTo = %q, want 0x1111...", r.PayTo)
	}
	if r.Network != "base-sepolia" {
		t.Errorf("Network = %q, want base-sepolia", r.Network)
	}
}

func TestRouteOption_NoOptions(t *testing.T) {
	r := RouteRule{Pattern: "/health", Price: "0", Description: "Health check"}

	// No options applied — PayTo and Network should remain zero-value.
	if r.PayTo != "" {
		t.Errorf("PayTo should be empty, got %q", r.PayTo)
	}
	if r.Network != "" {
		t.Errorf("Network should be empty, got %q", r.Network)
	}
}

func TestRouteRule_YAMLRoundTrip(t *testing.T) {
	original := RouteRule{
		Pattern:     "/inference-*/v1/*",
		Price:       "0.001",
		Description: "Inference gateway",
		PayTo:       "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Network:     "base",
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded RouteRule
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Pattern != original.Pattern {
		t.Errorf("Pattern = %q, want %q", decoded.Pattern, original.Pattern)
	}
	if decoded.Price != original.Price {
		t.Errorf("Price = %q, want %q", decoded.Price, original.Price)
	}
	if decoded.Description != original.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, original.Description)
	}
	if decoded.PayTo != original.PayTo {
		t.Errorf("PayTo = %q, want %q", decoded.PayTo, original.PayTo)
	}
	if decoded.Network != original.Network {
		t.Errorf("Network = %q, want %q", decoded.Network, original.Network)
	}
}

func TestRouteRule_OmitEmpty(t *testing.T) {
	// RouteRule without PayTo or Network — those fields should be omitted from YAML.
	r := RouteRule{
		Pattern:     "/rpc/*",
		Price:       "0.0001",
		Description: "RPC endpoint",
	}

	data, err := yaml.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "payTo") {
		t.Errorf("YAML output should omit payTo when empty, got:\n%s", out)
	}
	if strings.Contains(out, "network") {
		t.Errorf("YAML output should omit network when empty, got:\n%s", out)
	}
	// Verify required fields are present.
	if !strings.Contains(out, "pattern:") {
		t.Errorf("YAML output should contain pattern, got:\n%s", out)
	}
	if !strings.Contains(out, "price:") {
		t.Errorf("YAML output should contain price, got:\n%s", out)
	}
}

func TestPricingConfig_YAMLRoundTrip(t *testing.T) {
	original := PricingConfig{
		Wallet:         "0xGLOBALGLOBALGLOBALGLOBALGLOBALGLOBALGL",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		VerifyOnly:     true,
		Routes: []RouteRule{
			{
				Pattern:     "/rpc/*",
				Price:       "0.0001",
				Description: "RPC endpoint",
			},
			{
				Pattern:     "/inference-*/v1/*",
				Price:       "0.001",
				Description: "Inference gateway",
				PayTo:       "0xROUTEROUTEROUTEROUTEROUTEROUTEROUTEROU",
				Network:     "base",
			},
		},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded PricingConfig
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Wallet != original.Wallet {
		t.Errorf("Wallet = %q, want %q", decoded.Wallet, original.Wallet)
	}
	if decoded.Chain != original.Chain {
		t.Errorf("Chain = %q, want %q", decoded.Chain, original.Chain)
	}
	if decoded.FacilitatorURL != original.FacilitatorURL {
		t.Errorf("FacilitatorURL = %q, want %q", decoded.FacilitatorURL, original.FacilitatorURL)
	}
	if decoded.VerifyOnly != original.VerifyOnly {
		t.Errorf("VerifyOnly = %v, want %v", decoded.VerifyOnly, original.VerifyOnly)
	}
	if len(decoded.Routes) != len(original.Routes) {
		t.Fatalf("Routes count = %d, want %d", len(decoded.Routes), len(original.Routes))
	}

	// Route 0: no per-route overrides.
	if decoded.Routes[0].PayTo != "" {
		t.Errorf("Routes[0].PayTo = %q, want empty", decoded.Routes[0].PayTo)
	}
	if decoded.Routes[0].Network != "" {
		t.Errorf("Routes[0].Network = %q, want empty", decoded.Routes[0].Network)
	}

	// Route 1: per-route overrides.
	if decoded.Routes[1].PayTo != original.Routes[1].PayTo {
		t.Errorf("Routes[1].PayTo = %q, want %q", decoded.Routes[1].PayTo, original.Routes[1].PayTo)
	}
	if decoded.Routes[1].Network != original.Routes[1].Network {
		t.Errorf("Routes[1].Network = %q, want %q", decoded.Routes[1].Network, original.Routes[1].Network)
	}
}

func TestPricingConfig_YAMLWithPerRouteOverrides(t *testing.T) {
	pcfg := PricingConfig{
		Wallet:         "0xGLOBALGLOBALGLOBALGLOBALGLOBALGLOBALGL",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes: []RouteRule{
			{
				Pattern:     "/inference-llama/v1/*",
				Price:       "0.001",
				Description: "Llama inference",
				PayTo:       "0xROUTE_SPECIFIC_WALLET_ADDRESS_HERE_1234",
				Network:     "base",
			},
			{
				Pattern:     "/rpc/*",
				Price:       "0.0001",
				Description: "RPC endpoint",
				// No per-route overrides.
			},
		},
	}

	data, err := yaml.Marshal(pcfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	out := string(data)

	// The global wallet should appear at the top level.
	if !strings.Contains(out, "wallet: 0xGLOBALGLOBALGLOBALGLOBALGLOBALGLOBALGL") {
		t.Errorf("expected global wallet in output:\n%s", out)
	}

	// The first route should have a per-route payTo override.
	if !strings.Contains(out, "payTo: 0xROUTE_SPECIFIC_WALLET_ADDRESS_HERE_1234") {
		t.Errorf("expected per-route payTo in output:\n%s", out)
	}

	// The first route should have a per-route network override.
	if !strings.Contains(out, "network: base") {
		t.Errorf("expected per-route network in output:\n%s", out)
	}

	// The second route should NOT have payTo or network (omitempty).
	// Split output by route patterns to isolate sections.
	sections := strings.Split(out, "pattern:")
	if len(sections) < 3 {
		t.Fatalf("expected at least 2 route sections, got %d patterns", len(sections)-1)
	}
	// sections[2] is the RPC route section (after the second "pattern:" occurrence).
	rpcSection := sections[2]
	if strings.Contains(rpcSection, "payTo") {
		t.Errorf("RPC route section should not contain payTo:\n%s", rpcSection)
	}
	if strings.Contains(rpcSection, "network") {
		t.Errorf("RPC route section should not contain network:\n%s", rpcSection)
	}
}
