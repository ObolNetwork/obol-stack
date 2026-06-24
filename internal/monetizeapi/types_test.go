package monetizeapi

import (
	"encoding/json"
	"testing"
)

// TestEffectivePayments covers the single→multi-payment fallback: an offer
// with only spec.payment yields a one-element slice synthesized from it,
// while spec.payments (when set) is returned verbatim and wins over the
// singular block. Downstream (the x402 verifier) relies on this so legacy
// single-payment CRs and new multi-payment offers share one code path.
func TestEffectivePayments(t *testing.T) {
	single := &ServiceOffer{Spec: ServiceOfferSpec{
		Payment: ServiceOfferPayment{Network: "base", PayTo: "0xaaa", Price: ServiceOfferPriceTable{PerRequest: "0.001"}},
	}}
	got := single.EffectivePayments()
	if len(got) != 1 || got[0].Network != "base" || got[0].PayTo != "0xaaa" {
		t.Fatalf("single-payment fallback = %+v, want one element mirroring spec.payment", got)
	}

	multi := &ServiceOffer{Spec: ServiceOfferSpec{
		Payment: ServiceOfferPayment{Network: "base", PayTo: "0xaaa", Price: ServiceOfferPriceTable{PerRequest: "1"}},
		Payments: []ServiceOfferPayment{
			{Network: "base", PayTo: "0xaaa", Price: ServiceOfferPriceTable{PerRequest: "1"}},
			{Network: "ethereum", PayTo: "0xbbb", Price: ServiceOfferPriceTable{PerRequest: "10"}},
		},
	}}
	got = multi.EffectivePayments()
	if len(got) != 2 || got[1].Network != "ethereum" || got[1].PayTo != "0xbbb" {
		t.Fatalf("multi-payment = %+v, want the verbatim 2-element payments slice", got)
	}
}

// TestPurchaseAutoRefill_JSONRoundTrip asserts every field on
// PurchaseAutoRefill marshals to JSON and unmarshals back without loss. The
// MaxTotal + MaxSpendPerDay fields were added to match the CRD spec; this test
// pins the wire format and `omitempty` semantics so silent drift between the
// Go struct and the CRD surfaces as a test failure.
func TestPurchaseAutoRefill_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		in       PurchaseAutoRefill
		wantJSON string
	}{
		{
			name: "all fields populated",
			in: PurchaseAutoRefill{
				Enabled:        true,
				Threshold:      5,
				Count:          10,
				MaxTotal:       100,
				MaxSpendPerDay: "1.50",
			},
			wantJSON: `{"enabled":true,"threshold":5,"count":10,"maxTotal":100,"maxSpendPerDay":"1.50"}`,
		},
		{
			name: "only enabled + new caps",
			in: PurchaseAutoRefill{
				Enabled:        true,
				MaxTotal:       42,
				MaxSpendPerDay: "0.05",
			},
			wantJSON: `{"enabled":true,"maxTotal":42,"maxSpendPerDay":"0.05"}`,
		},
		{
			name:     "zero values omit every field",
			in:       PurchaseAutoRefill{},
			wantJSON: `{}`,
		},
		{
			name: "MaxSpendPerDay alone",
			in: PurchaseAutoRefill{
				MaxSpendPerDay: "0.0001",
			},
			wantJSON: `{"maxSpendPerDay":"0.0001"}`,
		},
		{
			name: "MaxTotal alone",
			in: PurchaseAutoRefill{
				MaxTotal: 7,
			},
			wantJSON: `{"maxTotal":7}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotJSON, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(gotJSON) != tt.wantJSON {
				t.Fatalf("marshal:\n got: %s\nwant: %s", gotJSON, tt.wantJSON)
			}

			var roundTripped PurchaseAutoRefill
			if err := json.Unmarshal(gotJSON, &roundTripped); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if roundTripped != tt.in {
				t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", roundTripped, tt.in)
			}
		})
	}
}

// TestPurchaseAutoRefill_UnmarshalAcceptsCRDForm asserts that a JSON document
// shaped like the CRD spec deserialises into every Go field — this is the
// inverse of the marshal direction and catches accidental json-tag drift.
func TestPurchaseAutoRefill_UnmarshalAcceptsCRDForm(t *testing.T) {
	const crdJSON = `{
		"enabled": true,
		"threshold": 5,
		"count": 10,
		"maxTotal": 100,
		"maxSpendPerDay": "1.50"
	}`

	want := PurchaseAutoRefill{
		Enabled:        true,
		Threshold:      5,
		Count:          10,
		MaxTotal:       100,
		MaxSpendPerDay: "1.50",
	}

	var got PurchaseAutoRefill
	if err := json.Unmarshal([]byte(crdJSON), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Fatalf("unmarshal mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}
