package buy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
)

func TestWellKnownURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"base URL", "https://demo.example", "https://demo.example/.well-known/agent-registration.json"},
		{"service path stripped", "https://demo.example/services/foo", "https://demo.example/.well-known/agent-registration.json"},
		{"trailing slash", "https://demo.example/", "https://demo.example/.well-known/agent-registration.json"},
		{"query dropped", "https://demo.example/services/foo?x=1", "https://demo.example/.well-known/agent-registration.json"},
		{"trim spaces", "  https://demo.example  ", "https://demo.example/.well-known/agent-registration.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := wellKnownURL(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("wellKnownURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWellKnownURL_Invalid(t *testing.T) {
	tests := []string{"", "not-a-url", "/relative/only"}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if _, err := wellKnownURL(in); err == nil {
				t.Fatalf("wellKnownURL(%q) returned nil err, want error", in)
			}
		})
	}
}

func TestPricingURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"service root", "https://demo.example/services/foo", "https://demo.example/services/foo/v1/chat/completions"},
		{"already full path", "https://demo.example/services/foo/v1/chat/completions", "https://demo.example/services/foo/v1/chat/completions"},
		{"chat path normalized to v1", "https://demo.example/services/foo/chat/completions", "https://demo.example/services/foo/v1/chat/completions"},
		{"host root", "https://demo.example", "https://demo.example/v1/chat/completions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := pricingURL(tc.in)
			if err != nil {
				t.Fatalf("pricingURL(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("pricingURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPricingURL_Invalid(t *testing.T) {
	if _, err := pricingURL("not-a-url"); err == nil {
		t.Fatal("pricingURL(invalid) returned nil err, want error")
	}
}

func TestVerifyAgentID(t *testing.T) {
	multi := &erc8004.AgentRegistration{
		Registrations: []erc8004.OnChainReg{
			{AgentID: 41, AgentRegistry: "eip155:1:0xabc"},
			{AgentID: 42, AgentRegistry: "eip155:84532:0xdef"},
		},
	}

	tests := []struct {
		name        string
		reg         *erc8004.AgentRegistration
		expected    int64
		wantErrSubs string
	}{
		{name: "match first", reg: multi, expected: 41},
		{name: "match second", reg: multi, expected: 42},
		{name: "mismatch", reg: multi, expected: 99, wantErrSubs: "expected 99"},
		{name: "empty registrations", reg: &erc8004.AgentRegistration{}, expected: 42, wantErrSubs: "no on-chain registrations"},
		{name: "expected zero", reg: multi, expected: 0, wantErrSubs: "expected agentId is 0"},
		{name: "nil registration", reg: nil, expected: 42, wantErrSubs: "nil registration"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := VerifyAgentID(tc.reg, tc.expected)
			if tc.wantErrSubs == "" {
				if err != nil {
					t.Fatalf("VerifyAgentID() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Fatalf("VerifyAgentID() err = %v, want substring %q", err, tc.wantErrSubs)
			}
		})
	}
}

func TestVerifyAgentIDOnRegistry(t *testing.T) {
	reg := &erc8004.AgentRegistration{
		Registrations: []erc8004.OnChainReg{
			{AgentID: 42, AgentRegistry: erc8004.Base.CAIP10Registry()},
			{AgentID: 42, AgentRegistry: erc8004.BaseSepolia.CAIP10Registry()},
		},
	}

	if err := VerifyAgentIDOnRegistry(reg, 42, erc8004.BaseSepolia.CAIP10Registry()); err != nil {
		t.Fatalf("VerifyAgentIDOnRegistry(match) = %v, want nil", err)
	}
	if err := VerifyAgentIDOnRegistry(reg, 42, erc8004.Ethereum.CAIP10Registry()); err == nil || !strings.Contains(err.Error(), erc8004.Ethereum.CAIP10Registry()) {
		t.Fatalf("VerifyAgentIDOnRegistry(mismatch) err = %v, want registry mismatch", err)
	}
}

func TestVerifyAgentIDForPricing(t *testing.T) {
	reg := &erc8004.AgentRegistration{
		Registrations: []erc8004.OnChainReg{{AgentID: 42, AgentRegistry: erc8004.BaseSepolia.CAIP10Registry()}},
	}
	pricing := &PricingResponse{Accepts: []PaymentOption{{Network: "base-sepolia", Amount: "1000"}}}
	if err := VerifyAgentIDForPricing(reg, 42, pricing); err != nil {
		t.Fatalf("VerifyAgentIDForPricing(match) = %v, want nil", err)
	}
	pricing.Accepts[0].Network = "base"
	if err := VerifyAgentIDForPricing(reg, 42, pricing); err == nil || !strings.Contains(err.Error(), erc8004.Base.CAIP10Registry()) {
		t.Fatalf("VerifyAgentIDForPricing(mismatch) err = %v, want base registry mismatch", err)
	}
}

func TestVerifySellerEndpoint(t *testing.T) {
	reg := &erc8004.AgentRegistration{
		Services: []erc8004.ServiceDef{
			{Name: "inference", Endpoint: "https://seller.example/services/alice-inference"},
			{Name: "OASF"},
		},
	}
	if err := VerifySellerEndpoint(reg, "https://seller.example/services/alice-inference/v1/chat/completions"); err != nil {
		t.Fatalf("VerifySellerEndpoint(match) = %v, want nil", err)
	}
	if err := VerifySellerEndpoint(reg, "https://seller.example/services/other/v1/chat/completions"); err == nil || !strings.Contains(err.Error(), "seller endpoint mismatch") {
		t.Fatalf("VerifySellerEndpoint(mismatch) err = %v, want endpoint mismatch", err)
	}
}

func TestValidateBudgetAgainstPricing(t *testing.T) {
	pricing := &PricingResponse{Accepts: []PaymentOption{{Network: "base-sepolia", Amount: "1000"}}}
	if err := ValidateBudgetAgainstPricing("1000", pricing); err != nil {
		t.Fatalf("ValidateBudgetAgainstPricing(equal) = %v, want nil", err)
	}
	if err := ValidateBudgetAgainstPricing("2500", pricing); err != nil {
		t.Fatalf("ValidateBudgetAgainstPricing(greater) = %v, want nil", err)
	}
	if err := ValidateBudgetAgainstPricing("999", pricing); err == nil || !strings.Contains(err.Error(), "smaller than one request price 1000") {
		t.Fatalf("ValidateBudgetAgainstPricing(too small) err = %v, want floor error", err)
	}
}

func TestValidateBudgetAgainstPricing_NonUSDCPricingRejected(t *testing.T) {
	pricing := &PricingResponse{Accepts: []PaymentOption{{
		Network: "base-sepolia",
		Asset:   "0x0a09371a8b011d5110656ceBCc70603e53FD2c78",
		Amount:  "1000000000000000000",
	}}}
	if err := ValidateBudgetAgainstPricing("2000000", pricing); err == nil || !strings.Contains(err.Error(), "currently supports only USDC-priced sellers") {
		t.Fatalf("ValidateBudgetAgainstPricing(non-usdc) err = %v, want explicit non-USDC rejection", err)
	}
}

func TestFetchSellerRegistration_Success(t *testing.T) {
	body := `{
		"type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
		"name": "demo",
		"description": "demo seller",
		"image": "https://demo.example/logo.png",
		"services": [{"name": "OASF"}],
		"x402Support": true,
		"active": true,
		"registrations": [{"agentId": 42, "agentRegistry": "eip155:84532:0xabc"}]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wellKnownPath {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	reg, err := FetchSellerRegistration(context.Background(), srv.URL+"/services/foo")
	if err != nil {
		t.Fatalf("FetchSellerRegistration: %v", err)
	}
	if got := reg.Registrations[0].AgentID; got != 42 {
		t.Fatalf("agentId = %d, want 42", got)
	}
}

func TestFetchSellerRegistration_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := FetchSellerRegistration(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("FetchSellerRegistration(404) err = %v, want 404 error", err)
	}
}

func TestFetchSellerRegistration_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(srv.Close)

	_, err := FetchSellerRegistration(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "parse registration JSON") {
		t.Fatalf("FetchSellerRegistration(bad json) err = %v, want parse error", err)
	}
}

func TestFetchSellerRegistration_InvalidURL(t *testing.T) {
	_, err := FetchSellerRegistration(context.Background(), "not-a-url")
	if err == nil || !strings.Contains(err.Error(), "scheme and host") {
		t.Fatalf("FetchSellerRegistration(invalid url) err = %v, want scheme/host error", err)
	}
}

func TestFetchSellerPricing_Success(t *testing.T) {
	const wantPath = "/services/demo/v1/chat/completions"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"x402Version":2,"accepts":[{"payTo":"0xabc","network":"base-sepolia","amount":"1000"}]}`))
	}))
	defer srv.Close()

	pricing, err := FetchSellerPricing(context.Background(), srv.URL+"/services/demo", "gemma4-fast")
	if err != nil {
		t.Fatalf("FetchSellerPricing: %v", err)
	}
	if got := pricing.Accepts[0].Network; got != "base-sepolia" {
		t.Fatalf("network = %q, want base-sepolia", got)
	}
}

func TestFetchSellerPricing_Non402(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	_, err := FetchSellerPricing(context.Background(), srv.URL+"/services/demo", "gemma4-fast")
	if err == nil || !strings.Contains(err.Error(), "expected HTTP 402") {
		t.Fatalf("FetchSellerPricing(non402) err = %v, want 402 error", err)
	}
}

func TestFetchSellerPricing_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{oops`))
	}))
	defer srv.Close()

	_, err := FetchSellerPricing(context.Background(), srv.URL+"/services/demo", "gemma4-fast")
	if err == nil || !strings.Contains(err.Error(), "parse pricing JSON") {
		t.Fatalf("FetchSellerPricing(bad json) err = %v, want parse error", err)
	}
}
