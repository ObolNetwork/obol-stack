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

// TestEffectiveRoutes covers the route-table fallback: an offer without
// spec.routes synthesizes the pre-route-table single paid catch-all, so the
// verifier and discovery surfaces can treat every offer as route-driven.
func TestEffectiveRoutes(t *testing.T) {
	legacy := &ServiceOffer{}
	got := legacy.EffectiveRoutes()
	if len(got) != 1 || got[0].Path != "/*" || got[0].EffectiveGate() != GatePaid {
		t.Fatalf("legacy fallback = %+v, want single paid catch-all /*", got)
	}

	declared := &ServiceOffer{Spec: ServiceOfferSpec{Routes: []ServiceOfferRoute{
		{Path: "/audit", Methods: []string{"POST"}, Gate: GatePaid, Price: ServiceOfferPriceTable{PerRequest: "0.5"}},
		{Path: "/healthz", Gate: GateFree},
	}}}
	got = declared.EffectiveRoutes()
	if len(got) != 2 || got[0].Path != "/audit" || got[1].EffectiveGate() != GateFree {
		t.Fatalf("declared routes = %+v, want the two spec entries verbatim", got)
	}
	if !got[0].HasPriceOverride() || got[1].HasPriceOverride() {
		t.Fatalf("price override detection wrong: %+v", got)
	}
}

// TestEffectiveGate_DefaultsPaid guards the fail-closed default: a
// zero-valued route (e.g. a hand-written CR that omitted gate, bypassing
// the CRD default) must never open a free path.
func TestEffectiveGate_DefaultsPaid(t *testing.T) {
	r := ServiceOfferRoute{Path: "/x"}
	if r.EffectiveGate() != GatePaid {
		t.Fatalf("EffectiveGate() = %q, want %q", r.EffectiveGate(), GatePaid)
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

// TestReservedPathConflict pins the platform-path denylist: offer paths may
// never claim or nest under shared-origin discovery/routing surfaces, and
// may not blanket "/" or the bare "/services" prefix. Regular
// /services/<name> paths (including nested sub-paths) stay allowed.
func TestReservedPathConflict(t *testing.T) {
	tests := []struct{ path, wantRoot string }{
		{"/services/my-offer", ""},
		{"/services/my-offer/", ""},
		{"/services/my-offer/v1", ""},
		{"/custom-prefix", ""},
		{"/", "/"},
		{"/services", "/"},
		{"/services/", "/"},
		{"/api", "/api"},
		{"/api/services.json", "/api"},
		{"/openapi.json", "/openapi.json"},
		{"/skill.md", "/skill.md"},
		{"/rpc", "/rpc"},
		{"/rpc/mainnet", "/rpc"},
		{"/.well-known", "/.well-known"},
		{"/.well-known/x402", "/.well-known"},
		{"/apiary", ""}, // prefix must respect segment boundaries
		{"/rpcx", ""},
	}
	for _, tt := range tests {
		if got := ReservedPathConflict(tt.path); got != tt.wantRoot {
			t.Errorf("ReservedPathConflict(%q) = %q, want %q", tt.path, got, tt.wantRoot)
		}
	}
}

// TestReservedRoutePathConflict pins the route-level denylist (F8): a
// spec.routes[].path may not land on "/auth" or "/auth/verify" (the
// verifier's SIWX sign-in endpoints for gate:auth offers) or nest under any
// of the shared reservedPathRoots. Unlike the offer-root check, "/" is a
// legitimate relative route path and must stay unreserved.
func TestReservedRoutePathConflict(t *testing.T) {
	tests := []struct{ path, wantRoot string }{
		{"/", ""},
		{"/v1/*", ""},
		{"/healthz", ""},
		{"/auth", "/auth"},
		{"/auth/", "/auth"},
		{"/auth/verify", "/auth"},
		{"/authorize", ""}, // prefix must respect segment boundaries
		{"/api", "/api"},
		{"/api/services.json", "/api"},
		{"/.well-known/x402", "/.well-known"},
	}
	for _, tt := range tests {
		if got := ReservedRoutePathConflict(tt.path); got != tt.wantRoot {
			t.Errorf("ReservedRoutePathConflict(%q) = %q, want %q", tt.path, got, tt.wantRoot)
		}
	}
}
