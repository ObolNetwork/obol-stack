package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func TestBuildAcceptPayments_RejectsOverCap(t *testing.T) {
	accepts := make([]string, monetizeapi.MaxServiceOfferPayments+1)
	for i := range accepts {
		accepts[i] = "token=USDC,network=base,price=1"
	}
	_, err := buildAcceptPayments(accepts, "0x1111111111111111111111111111111111111111")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d payment options", monetizeapi.MaxServiceOfferPayments)) {
		t.Fatalf("err = %v, want payment-option cap error", err)
	}
}

func TestParseRouteFlags_Cap(t *testing.T) {
	vals := make([]string, 0, monetizeapi.MaxServiceOfferRoutes+1)
	for i := range monetizeapi.MaxServiceOfferRoutes {
		vals = append(vals, fmt.Sprintf("path=/r%d", i))
	}
	if _, _, err := parseRouteFlags(vals); err != nil {
		t.Fatalf("parseRouteFlags at cap: %v", err)
	}
	_, _, err := parseRouteFlags(append(vals, "path=/extra"))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("at most %d routes", monetizeapi.MaxServiceOfferRoutes)) {
		t.Fatalf("err = %v, want route cap error", err)
	}
}

func TestCheckServiceOfferListCaps(t *testing.T) {
	list := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{}
		}
		return out
	}
	tests := []struct {
		name    string
		spec    map[string]any
		wantErr string
	}{
		{"empty", map[string]any{}, ""},
		{"at caps", map[string]any{
			"payments": list(monetizeapi.MaxServiceOfferPayments),
			"routes":   list(monetizeapi.MaxServiceOfferRoutes),
		}, ""},
		{"payments over", map[string]any{"payments": list(monetizeapi.MaxServiceOfferPayments + 1)}, "spec.payments"},
		{"routes over", map[string]any{"routes": list(monetizeapi.MaxServiceOfferRoutes + 1)}, "spec.routes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkServiceOfferListCaps(tt.spec)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want mention of %s", err, tt.wantErr)
			}
		})
	}
}
