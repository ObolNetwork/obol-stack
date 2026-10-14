package x402

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// TestVerifier_DuplicatePaymentOptionsAdvertisedOnce: an offer that lists the
// same payment option more than once (same scheme, network, asset, amount and
// payTo — also when spelled once by CAIP-2 and once by legacy alias, or with
// different address case) advertises it once in accepts[] (plus its single
// legacy-alias copy), never as several identical choices.
func TestVerifier_DuplicatePaymentOptionsAdvertisedOnce(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	const payTo = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	route := RouteRule{
		Pattern:        "/services/dup/*",
		OfferNamespace: "demo",
		OfferName:      "dup",
		Payments: []RoutePayment{
			{Price: "0.001", PayTo: payTo, Network: "base-sepolia", AssetSymbol: "USDC"},
			{Price: "0.001", PayTo: payTo, Network: "eip155:84532", AssetSymbol: "USDC"},
			{Price: "0.001", PayTo: "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Network: "base-sepolia", AssetSymbol: "USDC"},
		},
	}
	v := newTestVerifier(t, fac.URL, []RouteRule{route})
	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/dup/run")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}
	var got x402types.PaymentRequired
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse 402 body: %v (body=%s)", err, w.Body.String())
	}
	if len(got.Accepts) != 2 {
		t.Fatalf("accepts length = %d, want 2 (one option: CAIP-2 + legacy alias): %+v", len(got.Accepts), got.Accepts)
	}
	if got.Accepts[0].Network != "eip155:84532" || got.Accepts[1].Network != "base-sepolia" {
		t.Fatalf("accepts networks = %q, %q; want eip155:84532 then base-sepolia", got.Accepts[0].Network, got.Accepts[1].Network)
	}
}

func TestLegacyCompatRequirements_Dedupes(t *testing.T) {
	base := x402types.PaymentRequirements{
		Scheme:  "exact",
		Network: "eip155:84532",
		Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Amount:  "1000",
		PayTo:   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	alias := base
	alias.Network = "base-sepolia"
	other := base
	other.Amount = "2000"

	out := legacyCompatRequirements([]x402types.PaymentRequirements{base, base, alias, other})
	want := []struct{ network, amount string }{
		{"eip155:84532", "1000"},
		{"base-sepolia", "1000"},
		{"eip155:84532", "2000"},
		{"base-sepolia", "2000"},
	}
	if len(out) != len(want) {
		t.Fatalf("len = %d, want %d (2 distinct options × CAIP-2+alias): %+v", len(out), len(want), out)
	}
	for i, w := range want {
		if out[i].Network != w.network || out[i].Amount != w.amount {
			t.Errorf("out[%d] = %s/%s, want %s/%s", i, out[i].Network, out[i].Amount, w.network, w.amount)
		}
	}
}
