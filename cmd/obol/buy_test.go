package main

import (
	"math/big"
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
				CostCap:         big.NewInt(150000),
				SetDefault:      true,
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
				"--cost-cap", "150000",
				"--set-default",
				"--force",
			},
		},
		{
			name: "cost cap without auto-refill is still emitted (host owns intent)",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				BudgetMicro: "1000000",
				CostCap:     big.NewInt(42),
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
				"--cost-cap", "42",
			},
		},
		{
			name: "cost cap of zero is suppressed",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				BudgetMicro: "1000000",
				CostCap:     big.NewInt(0),
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
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

// canonicalOfferURL splices the catalog endpoint onto a storefront base when
// the user passed no /services/<name> segment. When they did pass one, we
// trust it verbatim and don't re-derive from the catalog (so a deliberately
// mismatched user URL still goes through to the seller).
func TestCanonicalOfferURL(t *testing.T) {
	t.Parallel()

	asset := &buy.CatalogEntry{Name: "aeon", Endpoint: "/services/aeon/v1/chat/completions"}
	tests := []struct {
		name    string
		user    string
		entry   *buy.CatalogEntry
		want    string
		wantErr string
	}{
		{
			name:  "storefront base + endpoint splice",
			user:  "https://inference.v1337.org/",
			entry: asset,
			want:  "https://inference.v1337.org/services/aeon",
		},
		{
			name:  "storefront base without trailing slash",
			user:  "https://inference.v1337.org",
			entry: asset,
			want:  "https://inference.v1337.org/services/aeon",
		},
		{
			name:  "service URL passed verbatim",
			user:  "https://seller.example/services/foo",
			entry: asset,
			want:  "https://seller.example/services/foo",
		},
		{
			name:  "service URL with trailing slash trimmed",
			user:  "https://seller.example/services/foo/",
			entry: asset,
			want:  "https://seller.example/services/foo",
		},
		{
			name:    "empty endpoint in catalog entry",
			user:    "https://inference.v1337.org/",
			entry:   &buy.CatalogEntry{Name: "x", Endpoint: ""},
			wantErr: "empty endpoint",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalOfferURL(tc.user, tc.entry)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("canonicalOfferURL err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalOfferURL unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("canonicalOfferURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// resolveBuyModel: flag wins when it matches, mismatch errors loudly,
// catalog entry's model is used when flag is empty, empty-both errors.
func TestResolveBuyModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		flag    string
		entry   *buy.CatalogEntry
		want    string
		wantErr string
	}{
		{name: "flag matches catalog", flag: "aeon", entry: &buy.CatalogEntry{Name: "x", Model: "aeon"}, want: "aeon"},
		{name: "flag case-insensitive", flag: "Aeon", entry: &buy.CatalogEntry{Name: "x", Model: "aeon"}, want: "Aeon"},
		{name: "flag mismatch errors", flag: "other", entry: &buy.CatalogEntry{Name: "x", Model: "aeon"}, wantErr: "does not match"},
		{name: "no flag uses catalog", flag: "", entry: &buy.CatalogEntry{Name: "x", Model: "aeon"}, want: "aeon"},
		{name: "no flag + no catalog model errors", flag: "", entry: &buy.CatalogEntry{Name: "x"}, wantErr: "advertises no model"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveBuyModel(tc.flag, tc.entry)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveBuyModel err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBuyModel unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveBuyModel = %q, want %q", got, tc.want)
			}
		})
	}
}

// resolveCostCap: explicit flag wins (in atomic units OR human decimal),
// otherwise we apply the costCapMarkupBps markup over price when
// auto-refill is on. No auto-refill means no auto cap.
func TestResolveCostCap(t *testing.T) {
	t.Parallel()
	price := big.NewInt(1000)
	tests := []struct {
		name       string
		flag       string
		token      string
		price      *big.Int
		autoRefill bool
		wantNil    bool
		want       *big.Int
		wantErr    string
	}{
		{name: "no flag + no auto-refill = nil", autoRefill: false, price: price, wantNil: true},
		{name: "no flag + auto-refill = 150% markup", autoRefill: true, price: price, want: big.NewInt(1500)},
		{name: "atomic-unit flag wins", flag: "42", autoRefill: true, price: price, want: big.NewInt(42)},
		{name: "human-decimal USDC fallback", flag: "0.001", token: "USDC", autoRefill: false, price: price, want: big.NewInt(1000)},
		{name: "invalid flag errors", flag: "not-a-number", token: "USDC", autoRefill: false, price: price, wantErr: "not a valid number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveCostCap(tc.flag, tc.token, tc.price, tc.autoRefill)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveCostCap err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCostCap unexpected err: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("resolveCostCap = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Cmp(tc.want) != 0 {
				t.Fatalf("resolveCostCap = %v, want %v", got, tc.want)
			}
		})
	}
}

// looksLikeURL keeps the positional URL detection in sync with the error
// message we surface when users pass a name-shaped positional.
func TestLooksLikeURL(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://example.com":        true,
		"http://localhost:8080":      true,
		"https://x.example/services": true,
		"my-buy":                     false,
		"":                           false,
		"ftp://example.com":          false,
	}
	for in, want := range cases {
		if got := looksLikeURL(in); got != want {
			t.Errorf("looksLikeURL(%q) = %v, want %v", in, got, want)
		}
	}
}
