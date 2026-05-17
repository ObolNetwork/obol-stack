package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/buy"
)

func TestBudgetToBaseUnits(t *testing.T) {
	tests := []struct {
		amount  string
		token   string
		want    string
		wantErr string
	}{
		// USDC — 6 decimals
		{amount: "0.5", token: "USDC", want: "500000"},
		{amount: "1", token: "USDC", want: "1000000"},
		{amount: "10", token: "USDC", want: "10000000"},
		{amount: "0.001", token: "USDC", want: "1000"},
		{amount: "0.000001", token: "USDC", want: "1"},
		{amount: "  0.5  ", token: "USDC", want: "500000"},
		// OBOL — 18 decimals
		{amount: "0.023", token: "OBOL", want: "23000000000000000"},
		{amount: "1", token: "OBOL", want: "1000000000000000000"},
		{amount: "0.000000000000000001", token: "OBOL", want: "1"},
		// lower-case token name normalised
		{amount: "0.023", token: "obol", want: "23000000000000000"},
		// Error cases
		{amount: "", token: "USDC", wantErr: "empty"},
		{amount: "abc", token: "USDC", wantErr: "valid number"},
		{amount: "0", token: "USDC", wantErr: "positive"},
		{amount: "-1", token: "USDC", wantErr: "positive"},
		{amount: "0.0000001", token: "USDC", wantErr: "more precision"},
		{amount: "0.0000000000000000001", token: "OBOL", wantErr: "more precision"},
		{amount: "1", token: "SHITCOIN", wantErr: "not a known payment token"},
	}
	for _, tc := range tests {
		name := tc.token + "/" + tc.amount
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := budgetToBaseUnits(tc.amount, tc.token)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("budgetToBaseUnits(%q, %q) err = %v, want substring %q", tc.amount, tc.token, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("budgetToBaseUnits(%q, %q) unexpected err: %v", tc.amount, tc.token, err)
			}
			if got != tc.want {
				t.Fatalf("budgetToBaseUnits(%q, %q) = %q, want %q", tc.amount, tc.token, got, tc.want)
			}
		})
	}
}

func TestBuildBuyPyArgv(t *testing.T) {
	tests := []struct {
		name string
		opts buyPyOptions
		want []string
	}{
		{
			name: "minimal",
			opts: buyPyOptions{
				Name:        "default-paid",
				Seller:      "https://demo.example/services/x",
				BudgetMicro: "10000000",
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "default-paid",
				"--endpoint", "https://demo.example/services/x",
				"--budget", "10000000",
			},
		},
		{
			name: "all optional flags",
			opts: buyPyOptions{
				Name:            "demo",
				Seller:          "https://s.example",
				Model:           "qwen3.5:9b",
				BudgetMicro:     "5000000",
				AutoRefill:      true,
				RefillThreshold: 3,
				RefillCount:     7,
				Force:           true,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "5000000",
				"--model", "qwen3.5:9b",
				"--auto-refill",
				"--refill-threshold", "3",
				"--refill-count", "7",
				"--force",
			},
		},
		{
			name: "auto-refill without explicit counts",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				BudgetMicro: "1000000",
				AutoRefill:  true,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
				"--auto-refill",
			},
		},
		{
			name: "auto-refill off does not emit refill flags",
			opts: buyPyOptions{
				Name:            "demo",
				Seller:          "https://s.example",
				BudgetMicro:     "1000000",
				AutoRefill:      false,
				RefillThreshold: 3,
				RefillCount:     7,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
			},
		},
		{
			name: "model whitespace trimmed",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				Model:       "  qwen3.5:9b  ",
				BudgetMicro: "1000000",
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
				"--model", "qwen3.5:9b",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildBuyPyArgv(tc.opts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildBuyPyArgv() = %#v\n want                  %#v", got, tc.want)
			}
		})
	}
}

// TestValidateTokenAgainstPricing exercises the token-mismatch check using
// synthetic PricingResponse values without any network calls.
func TestValidateTokenAgainstPricing(t *testing.T) {
	const (
		usdcAddrBaseSepolia = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
		obolAddrBaseSepolia = "0x0a09371a8b011d5110656ceBCc70603e53FD2c78"
		networkBaseSepolia  = "eip155:84532"
	)

	mkPricing := func(network, asset string) *buy.PricingResponse {
		return &buy.PricingResponse{
			X402Version: 1,
			Accepts: []buy.PaymentOption{{
				Network: network,
				Asset:   asset,
				Amount:  "23000000000000000",
			}},
		}
	}

	tests := []struct {
		name    string
		token   string
		pricing *buy.PricingResponse
		wantErr string
	}{
		{
			name:    "USDC matches USDC seller",
			token:   "USDC",
			pricing: mkPricing(networkBaseSepolia, usdcAddrBaseSepolia),
		},
		{
			name:    "OBOL matches OBOL seller",
			token:   "OBOL",
			pricing: mkPricing(networkBaseSepolia, obolAddrBaseSepolia),
		},
		{
			name:    "USDC vs OBOL seller — names both sides",
			token:   "USDC",
			pricing: mkPricing(networkBaseSepolia, obolAddrBaseSepolia),
			wantErr: "--token USDC selected, but seller requires asset",
		},
		{
			name:    "USDC vs OBOL seller — suggests --token OBOL",
			token:   "USDC",
			pricing: mkPricing(networkBaseSepolia, obolAddrBaseSepolia),
			wantErr: "retry with --token OBOL",
		},
		{
			name:    "OBOL vs USDC seller — suggests --token USDC",
			token:   "OBOL",
			pricing: mkPricing(networkBaseSepolia, usdcAddrBaseSepolia),
			wantErr: "retry with --token USDC",
		},
		{
			name:    "unknown token name",
			token:   "SHITCOIN",
			pricing: mkPricing(networkBaseSepolia, obolAddrBaseSepolia),
			wantErr: "not available on network",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buy.ValidateTokenAgainstPricing(tc.token, tc.pricing)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateTokenAgainstPricing(%q) unexpected err: %v", tc.token, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateTokenAgainstPricing(%q) err = %v, want substring %q", tc.token, err, tc.wantErr)
			}
		})
	}
}
