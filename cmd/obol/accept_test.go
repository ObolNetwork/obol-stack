package main

import (
	"context"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

const testPayTo = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParseAcceptOption_RegistryShorthand(t *testing.T) {
	// USDC resolves to the chain default — no explicit asset block.
	opt, _, err := parseAcceptOption("token=USDC,network=base,price=1,pay-to="+testPayTo, "")
	if err != nil {
		t.Fatalf("USDC: unexpected error: %v", err)
	}
	if opt.Network != "base" || opt.PriceKey != "perRequest" || opt.PriceVal != "1" {
		t.Fatalf("USDC: got %+v", opt)
	}
	if !opt.Asset.IsZero() {
		t.Errorf("USDC should use chain default (zero asset), got %+v", opt.Asset)
	}

	// OBOL resolves to a full asset block from the registry.
	obol, _, err := parseAcceptOption("token=OBOL,network=ethereum,price=10,pay-to="+testPayTo, "")
	if err != nil {
		t.Fatalf("OBOL: unexpected error: %v", err)
	}
	if obol.Asset.Symbol != "OBOL" || obol.Asset.TransferMethod != schemas.AssetTransferMethodPermit2 || obol.Asset.Decimals != 18 {
		t.Fatalf("OBOL asset not resolved from registry: %+v", obol.Asset)
	}
}

func TestParseAcceptOption_DefaultPayToFallback(t *testing.T) {
	opt, _, err := parseAcceptOption("token=USDC,network=base,price=1", testPayTo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opt.PayTo != testPayTo {
		t.Errorf("pay-to fallback = %q, want command default", opt.PayTo)
	}
}

func TestParseAcceptOption_RawAssetEscapeHatch(t *testing.T) {
	raw := "asset=0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb,decimals=18,transfer=permit2," +
		"eip712-name=Foo Token,eip712-version=1,symbol=FOO,network=base,price=0.5,pay-to=" + testPayTo
	opt, _, err := parseAcceptOption(raw, "")
	if err != nil {
		t.Fatalf("raw asset: unexpected error: %v", err)
	}
	if opt.Asset.Address != "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		opt.Asset.Symbol != "FOO" || opt.Asset.Decimals != 18 ||
		opt.Asset.TransferMethod != "permit2" || opt.Asset.EIP712Name != "Foo Token" || opt.Asset.EIP712Version != "1" {
		t.Fatalf("raw asset block wrong: %+v", opt.Asset)
	}
}

func TestParseAcceptOption_Errors(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"unknown key", "token=USDC,network=base,price=1,bogus=x,pay-to=" + testPayTo, "unknown --accept key"},
		{"no network", "token=USDC,price=1,pay-to=" + testPayTo, "network is required"},
		{"unsupported chain", "token=USDC,network=eip155:9999,price=1,pay-to=" + testPayTo, "unsupported chain"},
		{"no price", "token=USDC,network=base,pay-to=" + testPayTo, "price is required"},
		{"two prices", "token=USDC,network=base,price=1,per-mtok=2,pay-to=" + testPayTo, "only one of"},
		{"bad pay-to", "token=USDC,network=base,price=1,pay-to=nope", "pay-to must be"},
		{"token and asset", "token=USDC,asset=0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb,network=base,price=1,pay-to=" + testPayTo, "not both"},
		{"bad decimals", "asset=0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb,decimals=999,network=base,price=1,pay-to=" + testPayTo, "decimals must be"},
		{"unregistered token", "token=WETH,network=base,price=1,pay-to=" + testPayTo, "not in the registry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseAcceptOption(tc.raw, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseAcceptOption_RawAssetDefersToAutofill(t *testing.T) {
	// A raw asset with just the address parses (transfer defaults to permit2);
	// decimals/eip712 are left for autofill rather than erroring at parse.
	opt, _, err := parseAcceptOption("asset=0x"+strings.Repeat("b", 40)+",network=base,price=1,pay-to="+testPayTo, "")
	if err != nil {
		t.Fatalf("partial raw asset should parse, got: %v", err)
	}
	if opt.Asset.Address != "0x"+strings.Repeat("b", 40) || opt.Asset.TransferMethod != schemas.AssetTransferMethodPermit2 {
		t.Fatalf("partial raw asset = %+v, want address set + permit2 default", opt.Asset)
	}
	if opt.Asset.Decimals != 0 || opt.Asset.EIP712Name != "" {
		t.Errorf("decimals/eip712 should be empty pending autofill, got %+v", opt.Asset)
	}
}

func TestAutofillAcceptPayments(t *testing.T) {
	ctx := context.Background()
	full := tokenMeta{Decimals: 18, Symbol: "FOO", EIP712Name: "Foo Token", EIP712Version: "1"}

	// (1) Partial raw asset → filled from chain.
	pays, _ := buildAcceptPayments([]string{"asset=0x" + strings.Repeat("b", 40) + ",network=base,price=1,pay-to=" + testPayTo}, "")
	calls := 0
	err := autofillAcceptPayments(ctx, pays, func(_ context.Context, _, _ string) (tokenMeta, error) {
		calls++
		return full, nil
	})
	if err != nil {
		t.Fatalf("autofill: %v", err)
	}
	a := pays[0]["asset"].(schemas.AssetTerms)
	if a.Decimals != 18 || a.EIP712Name != "Foo Token" || a.EIP712Version != "1" || a.Symbol != "FOO" {
		t.Fatalf("autofill did not fill from chain: %+v", a)
	}

	// (2) Registry (OBOL) + USDC options are already complete → no RPC call.
	pays, _ = buildAcceptPayments([]string{
		"token=USDC,network=base,price=1,pay-to=" + testPayTo,
		"token=OBOL,network=ethereum,price=10,pay-to=" + testPayTo,
	}, "")
	calls = 0
	if err := autofillAcceptPayments(ctx, pays, func(_ context.Context, _, _ string) (tokenMeta, error) {
		calls++
		return tokenMeta{}, nil
	}); err != nil {
		t.Fatalf("autofill complete options: %v", err)
	}
	if calls != 0 {
		t.Errorf("registry/USDC options should not hit the chain, got %d calls", calls)
	}

	// (3) Chain can't resolve the signature-critical fields → error to specify.
	pays, _ = buildAcceptPayments([]string{"asset=0x" + strings.Repeat("c", 40) + ",network=base,price=1,pay-to=" + testPayTo}, "")
	err = autofillAcceptPayments(ctx, pays, func(_ context.Context, _, _ string) (tokenMeta, error) {
		return tokenMeta{Symbol: "X"}, nil // no decimals / eip712 (token lacks EIP-5267)
	})
	if err == nil || !strings.Contains(err.Error(), "could not read") {
		t.Fatalf("expected unresolved-fields error, got %v", err)
	}
}

func TestBuildAcceptPayments(t *testing.T) {
	// No --accept → nil so callers fall back to singular flags.
	got, err := buildAcceptPayments(nil, testPayTo)
	if err != nil || got != nil {
		t.Fatalf("empty accepts: got %v, %v; want nil,nil", got, err)
	}

	// Two distinct options → spec.payments[] with two entries.
	payments, err := buildAcceptPayments([]string{
		"token=USDC,network=base,price=1",
		"token=OBOL,network=ethereum,price=10",
	}, testPayTo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payments) != 2 {
		t.Fatalf("payments len = %d, want 2", len(payments))
	}
	if payments[0]["network"] != "base" || payments[1]["network"] != "ethereum" {
		t.Fatalf("payments order/networks wrong: %+v", payments)
	}

	// Duplicate (chain, token) is rejected.
	_, err = buildAcceptPayments([]string{
		"token=USDC,network=base,price=1",
		"token=USDC,network=base,price=2",
	}, testPayTo)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}
