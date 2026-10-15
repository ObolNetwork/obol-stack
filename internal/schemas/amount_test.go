package schemas

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
	"testing"
)

func TestDecimalToAtomic(t *testing.T) {
	tests := []struct {
		name     string
		price    string
		decimals int
		symbol   string
		want     string // atomic units; "" means an error is expected
		wantErr  string // substring of the expected error
	}{
		// USDC (6 decimals)
		{"usdc one atomic unit", "0.000001", 6, "USDC", "1", ""},
		{"usdc beyond precision", "0.0000001", 6, "USDC", "", "max 6 decimal places for USDC"},
		{"usdc typical", "0.001", 6, "USDC", "1000", ""},
		{"usdc whole", "12", 6, "USDC", "12000000", ""},
		{"usdc trailing dot", "5.", 6, "USDC", "5000000", ""},
		{"usdc leading dot", ".25", 6, "USDC", "250000", ""},
		{"usdc no float drift", "0.1", 6, "USDC", "100000", ""},
		{"usdc at cap", "1000000000000", 6, "USDC", "1000000000000000000", ""},
		// OBOL (18 decimals)
		{"obol one atomic unit", "0.000000000000000001", 18, "OBOL", "1", ""},
		{"obol max precision", "123.456789012345678901", 18, "OBOL", "123456789012345678901", ""},
		{"obol beyond precision", "123.4567890123456789012", 18, "OBOL", "", "max 18 decimal places for OBOL"},
		{"obol 18dp exact", "123.456789012345678", 18, "OBOL", "123456789012345678000", ""},
		{"obol at cap", "1000000000000.000000000000000000", 18, "OBOL", "1000000000000000000000000000000", ""},
		// Rejected grammar / values
		{"empty", "", 6, "USDC", "", "empty"},
		{"zero", "0", 6, "USDC", "", "greater than zero"},
		{"zero decimal", "0.000000", 6, "USDC", "", "greater than zero"},
		{"negative", "-1", 6, "USDC", "", "plain decimal"},
		{"plus sign", "+1", 6, "USDC", "", "plain decimal"},
		{"exponent", "1e3", 6, "USDC", "", "plain decimal"},
		{"inf", "Inf", 6, "USDC", "", "plain decimal"},
		{"nan", "NaN", 6, "USDC", "", "plain decimal"},
		{"comma", "1,5", 6, "USDC", "", "plain decimal"},
		{"dollar", "$1", 6, "USDC", "", "plain decimal"},
		{"space", " 1", 6, "USDC", "", "plain decimal"},
		{"two dots", "1.2.3", 6, "USDC", "", "plain decimal"},
		{"bare dot", ".", 6, "USDC", "", "plain decimal"},
		{"hex", "0x10", 6, "USDC", "", "plain decimal"},
		{"over cap", "1000000000000.000001", 6, "USDC", "", "exceeds the maximum"},
		{"huge", strings.Repeat("9", 40), 18, "OBOL", "", "exceeds the maximum"},
		{"too long", "0." + strings.Repeat("0", 70) + "1", 18, "OBOL", "", "too long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecimalToAtomic(tc.price, tc.decimals, tc.symbol)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("DecimalToAtomic(%q, %d) = %v, %v; want error containing %q", tc.price, tc.decimals, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecimalToAtomic(%q, %d): %v", tc.price, tc.decimals, err)
			}
			if got.String() != tc.want {
				t.Fatalf("DecimalToAtomic(%q, %d) = %s, want %s", tc.price, tc.decimals, got, tc.want)
			}
		})
	}
}

func TestParsePositiveDecimalAmount_ZeroIsErrPriceNotPositive(t *testing.T) {
	if _, err := ParsePositiveDecimalAmount("0.0", MaxPriceDecimals, ""); !errors.Is(err, ErrPriceNotPositive) {
		t.Fatalf("err = %v, want ErrPriceNotPositive", err)
	}
}

func TestPerMTokToAtomicPerRequest(t *testing.T) {
	tests := []struct {
		name     string
		perMTok  string
		decimals int
		want     string
		wantErr  bool
	}{
		{"usdc exact", "1.25", 6, "1250", false},
		{"usdc tiny rounds up to one unit", "0.000001", 6, "1", false},
		{"usdc remainder rounds up", "0.0015", 6, "2", false},
		{"usdc large", "1000", 6, "1000000", false},
		{"obol tiny rounds up", "0.000000000000000001", 18, "1", false},
		{"obol exact", "2", 18, "2000000000000000", false},
		{"zero rejected", "0", 6, "", true},
		{"usdc beyond precision rejected", "0.0000001", 6, "", true},
		{"exponent rejected", "1e-3", 6, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PerMTokToAtomicPerRequest(tc.perMTok, ApproxTokensPerRequest, tc.decimals, "TOKEN")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PerMTokToAtomicPerRequest(%q) = %s, want error", tc.perMTok, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PerMTokToAtomicPerRequest(%q): %v", tc.perMTok, err)
			}
			if got.String() != tc.want {
				t.Fatalf("PerMTokToAtomicPerRequest(%q, %d) = %s, want %s", tc.perMTok, tc.decimals, got, tc.want)
			}
			if got.Sign() <= 0 {
				t.Fatalf("a positive per-MTok price must charge at least one atomic unit, got %s", got)
			}
		})
	}
}

func TestPerRequestPriceFromPerMTok(t *testing.T) {
	tests := []struct {
		perMTok  string
		decimals int
		want     string
	}{
		{"1.25", 6, "0.00125"},
		{"0.000001", 6, "0.000001"}, // 1e-9 rounds up to one atomic unit
		{"1000", 6, "1"},
		{"0.000000000000000001", 18, "0.000000000000000001"},
	}
	for _, tc := range tests {
		got, err := PerRequestPriceFromPerMTok(tc.perMTok, tc.decimals, "TOKEN")
		if err != nil {
			t.Fatalf("PerRequestPriceFromPerMTok(%q): %v", tc.perMTok, err)
		}
		if got != tc.want {
			t.Fatalf("PerRequestPriceFromPerMTok(%q, %d) = %q, want %q", tc.perMTok, tc.decimals, got, tc.want)
		}
	}
}

func TestApproximateRequestPriceFromPerMTok_Exact(t *testing.T) {
	// 18 fractional digits / 1000 needs 21 digits; nothing may be lost.
	got, err := ApproximateRequestPriceFromPerMTok("0.000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.000000000000000000001" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"Inf", "NaN", "1e3", "0", "-1", ""} {
		if _, err := ApproximateRequestPriceFromPerMTok(bad); err == nil {
			t.Fatalf("ApproximateRequestPriceFromPerMTok(%q) = nil error", bad)
		}
	}
}

// PriceDecimalPattern (mirrored into the CRD schema) and ParseDecimalAmount
// must agree on the grammar.
func TestPriceDecimalPatternMatchesParser(t *testing.T) {
	re := regexp.MustCompile(PriceDecimalPattern)
	for _, s := range []string{
		"0", "0.001", "1.5", ".5", "5.", "0.000000000000000001", "100",
		"", ".", "-1", "+1", "1e3", "Inf", "NaN", "1,5", "$1", " 1", "1.2.3",
		"0.0000000000000000001",
	} {
		_, err := ParseDecimalAmount(s, MaxPriceDecimals, "")
		if re.MatchString(s) != (err == nil) {
			t.Errorf("%q: pattern match=%v, parser err=%v", s, re.MatchString(s), err)
		}
	}
}

func TestAtomicToDecimal(t *testing.T) {
	for _, tc := range []struct {
		atomic   string
		decimals int
		want     string
	}{
		{"1", 6, "0.000001"},
		{"1000", 6, "0.001"},
		{"1500000", 6, "1.5"},
		{"2000000", 6, "2"},
		{"1", 18, "0.000000000000000001"},
		{"7", 0, "7"},
	} {
		n, _ := new(big.Int).SetString(tc.atomic, 10)
		if got := AtomicToDecimal(n, tc.decimals); got != tc.want {
			t.Fatalf("AtomicToDecimal(%s, %d) = %q, want %q", tc.atomic, tc.decimals, got, tc.want)
		}
	}
}
