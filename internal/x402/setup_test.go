package x402

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRouteRule_YAMLRoundTrip(t *testing.T) {
	original := RouteRule{
		Pattern:                "/inference-*/v1/*",
		Price:                  "0.001",
		Description:            "Inference gateway",
		PayTo:                  "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Network:                "base",
		UpstreamAuth:           "Bearer sk-obol",
		PriceModel:             "perMTok",
		PerMTok:                "0.50",
		ApproxTokensPerRequest: 1000,
		OfferNamespace:         "llm",
		OfferName:              "paid-qwen",
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
	if decoded.UpstreamAuth != original.UpstreamAuth {
		t.Errorf("UpstreamAuth = %q, want %q", decoded.UpstreamAuth, original.UpstreamAuth)
	}
	if decoded.PriceModel != original.PriceModel {
		t.Errorf("PriceModel = %q, want %q", decoded.PriceModel, original.PriceModel)
	}
	if decoded.PerMTok != original.PerMTok {
		t.Errorf("PerMTok = %q, want %q", decoded.PerMTok, original.PerMTok)
	}
	if decoded.ApproxTokensPerRequest != original.ApproxTokensPerRequest {
		t.Errorf("ApproxTokensPerRequest = %d, want %d", decoded.ApproxTokensPerRequest, original.ApproxTokensPerRequest)
	}
	if decoded.OfferNamespace != original.OfferNamespace {
		t.Errorf("OfferNamespace = %q, want %q", decoded.OfferNamespace, original.OfferNamespace)
	}
	if decoded.OfferName != original.OfferName {
		t.Errorf("OfferName = %q, want %q", decoded.OfferName, original.OfferName)
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
	if strings.Contains(out, "priceModel") {
		t.Errorf("YAML output should omit priceModel when empty, got:\n%s", out)
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
		FacilitatorURL: "https://x402.gcp.obol.tech",
		VerifyOnly:     true,
		Routes: []RouteRule{
			{
				Pattern:     "/rpc/*",
				Price:       "0.0001",
				Description: "RPC endpoint",
			},
			{
				Pattern:                "/inference-*/v1/*",
				Price:                  "0.001",
				Description:            "Inference gateway",
				PayTo:                  "0xROUTEROUTEROUTEROUTEROUTEROUTEROUTEROU",
				Network:                "base",
				PriceModel:             "perMTok",
				PerMTok:                "0.50",
				ApproxTokensPerRequest: 1000,
				OfferNamespace:         "default",
				OfferName:              "offer-a",
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
	if decoded.Routes[1].PriceModel != original.Routes[1].PriceModel {
		t.Errorf("Routes[1].PriceModel = %q, want %q", decoded.Routes[1].PriceModel, original.Routes[1].PriceModel)
	}
	if decoded.Routes[1].PerMTok != original.Routes[1].PerMTok {
		t.Errorf("Routes[1].PerMTok = %q, want %q", decoded.Routes[1].PerMTok, original.Routes[1].PerMTok)
	}
	if decoded.Routes[1].ApproxTokensPerRequest != original.Routes[1].ApproxTokensPerRequest {
		t.Errorf("Routes[1].ApproxTokensPerRequest = %d, want %d", decoded.Routes[1].ApproxTokensPerRequest, original.Routes[1].ApproxTokensPerRequest)
	}
	if decoded.Routes[1].OfferNamespace != original.Routes[1].OfferNamespace {
		t.Errorf("Routes[1].OfferNamespace = %q, want %q", decoded.Routes[1].OfferNamespace, original.Routes[1].OfferNamespace)
	}
	if decoded.Routes[1].OfferName != original.Routes[1].OfferName {
		t.Errorf("Routes[1].OfferName = %q, want %q", decoded.Routes[1].OfferName, original.Routes[1].OfferName)
	}
}

func TestPricingConfig_YAMLWithPerRouteOverrides(t *testing.T) {
	pcfg := PricingConfig{
		Wallet:         "0xGLOBALGLOBALGLOBALGLOBALGLOBALGLOBALGL",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://x402.gcp.obol.tech",
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

func TestX402Manifest_UsesServiceOfferControllerModel(t *testing.T) {
	manifest := string(x402Manifest)
	if strings.Contains(manifest, "paymentroutes.obol.org") {
		t.Fatalf("x402 manifest still references removed PaymentRoute CRD:\n%s", manifest)
	}
	if !strings.Contains(manifest, "name: serviceoffer-controller") {
		t.Fatalf("x402 manifest missing serviceoffer-controller deployment:\n%s", manifest)
	}
	if !strings.Contains(manifest, "--route-source=kube") {
		t.Fatalf("x402 verifier is not configured for kube-backed service offers:\n%s", manifest)
	}
	if !strings.Contains(manifest, "resources: [\"serviceoffers\"]") {
		t.Fatalf("x402 manifest missing serviceoffer watch RBAC:\n%s", manifest)
	}
	if strings.Contains(manifest, "kind: ServiceMonitor") {
		t.Fatalf("x402 manifest still includes legacy ServiceMonitor stanza:\n%s", manifest)
	}
}
