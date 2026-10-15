package schemas

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// MaxPriceDecimals is the largest number of fractional digits a price may
// carry: ERC-20 tokens have at most 18 decimals (USDC 6, OBOL 18). A price
// for a specific token is further limited to that token's decimals.
const MaxPriceDecimals = 18

// MaxPriceWholeTokens bounds any single price (in whole tokens). Larger
// values are almost certainly a unit mistake (atomic units typed as a
// decimal amount) and are refused rather than advertised.
const MaxPriceWholeTokens = 1_000_000_000_000

// MaxPriceLength bounds the length of a price string. A plain decimal with
// 13 integer digits and 18 fractional digits needs 32 characters; the slack
// leaves room for leading zeros.
const MaxPriceLength = 64

// PriceDecimalPattern is the accepted price grammar: unsigned plain decimal
// digits with an optional single '.', at most MaxPriceDecimals fractional
// digits. No sign, exponent, separators, whitespace or currency symbols. It
// is mirrored into the ServiceOffer CRD schema (internal/monetizeapi).
const PriceDecimalPattern = `^([0-9]+(\.[0-9]{0,18})?|\.[0-9]{1,18})$`

// maxTokenDecimals matches the ServiceOffer asset.decimals schema bound
// (uint8). Prices never carry more than MaxPriceDecimals fractional digits,
// whatever the token's decimals.
const maxTokenDecimals = 255

var maxPriceWholeTokens = big.NewRat(MaxPriceWholeTokens, 1)

// ErrPriceNotPositive is returned when a paid price parses to zero.
var ErrPriceNotPositive = errors.New("price must be greater than zero (use gate=free for a free route)")

// ParseDecimalAmount parses s as an exact decimal (no floating point) using
// the PriceDecimalPattern grammar, allowing at most maxDecimals fractional
// digits. symbol names the token in the "max N decimal places" error; pass
// "" when the token is not known yet. Zero is accepted; callers that need a
// paid amount use ParsePositiveDecimalAmount.
func ParseDecimalAmount(s string, maxDecimals int, symbol string) (*big.Rat, error) {
	if s == "" {
		return nil, errors.New("price cannot be empty")
	}
	if len(s) > MaxPriceLength {
		return nil, fmt.Errorf("price %q is too long (max %d characters)", s, MaxPriceLength)
	}
	if maxDecimals < 0 || maxDecimals > MaxPriceDecimals {
		maxDecimals = MaxPriceDecimals
	}

	intPart, fracPart, _ := strings.Cut(s, ".")
	if !allDigits(intPart) || !allDigits(fracPart) || (intPart == "" && fracPart == "") {
		return nil, fmt.Errorf("invalid price %q: want a plain decimal number like 0.001 (digits and at most one '.', no sign, exponent, commas or spaces)", s)
	}
	if len(fracPart) > maxDecimals {
		return nil, fmt.Errorf("price %q has %d decimal places: max %d decimal places for %s", s, len(fracPart), maxDecimals, tokenLabel(symbol))
	}

	num, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return nil, fmt.Errorf("invalid price %q", s)
	}
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(fracPart))), nil)
	r := new(big.Rat).SetFrac(num, den)
	if r.Cmp(maxPriceWholeTokens) > 0 {
		return nil, fmt.Errorf("price %q exceeds the maximum of %d whole tokens", s, int64(MaxPriceWholeTokens))
	}
	return r, nil
}

// ParsePositiveDecimalAmount is ParseDecimalAmount for paid prices: zero is
// rejected with ErrPriceNotPositive.
func ParsePositiveDecimalAmount(s string, maxDecimals int, symbol string) (*big.Rat, error) {
	r, err := ParseDecimalAmount(s, maxDecimals, symbol)
	if err != nil {
		return nil, err
	}
	if r.Sign() <= 0 {
		return nil, fmt.Errorf("price %q: %w", s, ErrPriceNotPositive)
	}
	return r, nil
}

// ValidatePrice checks s as a paid price in any token: the decimal grammar,
// at most MaxPriceDecimals fractional digits, greater than zero and bounded.
func ValidatePrice(s string) error {
	_, err := ParsePositiveDecimalAmount(s, MaxPriceDecimals, "")
	return err
}

// ValidatePriceForToken is ValidatePrice limited to the token's decimals.
func ValidatePriceForToken(s string, decimals int, symbol string) error {
	_, err := ParsePositiveDecimalAmount(s, decimals, symbol)
	return err
}

// DecimalToAtomic converts a paid price to the token's atomic units exactly.
// The price may not carry more fractional digits than the token has, so the
// conversion never rounds.
func DecimalToAtomic(s string, decimals int, symbol string) (*big.Int, error) {
	if decimals < 0 || decimals > maxTokenDecimals {
		return nil, fmt.Errorf("unsupported token decimals %d (want 0-%d)", decimals, maxTokenDecimals)
	}
	r, err := ParsePositiveDecimalAmount(s, decimals, symbol)
	if err != nil {
		return nil, err
	}
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(pow10(decimals)))
	if !scaled.IsInt() {
		// Unreachable: fractional digits are bounded by decimals above.
		return nil, fmt.Errorf("price %q is not a whole number of atomic units", s)
	}
	return new(big.Int).Set(scaled.Num()), nil
}

// PerMTokToAtomicPerRequest converts a per-million-token price into the
// per-request charge in atomic units for a request estimated at
// tokensPerRequest tokens: ceil(perMTok * tokensPerRequest / 1e6 * 10^decimals).
// The result is computed exactly and rounds UP, so a tiny but non-zero
// per-MTok price charges at least one atomic unit instead of nothing.
func PerMTokToAtomicPerRequest(perMTok string, tokensPerRequest, decimals int, symbol string) (*big.Int, error) {
	if decimals < 0 || decimals > maxTokenDecimals {
		return nil, fmt.Errorf("unsupported token decimals %d (want 0-%d)", decimals, maxTokenDecimals)
	}
	if tokensPerRequest <= 0 {
		tokensPerRequest = ApproxTokensPerRequest
	}
	r, err := ParsePositiveDecimalAmount(perMTok, decimals, symbol)
	if err != nil {
		return nil, err
	}
	perRequest := new(big.Rat).Mul(r, big.NewRat(int64(tokensPerRequest), 1_000_000))
	return CeilAtomic(perRequest, decimals), nil
}

// CeilAtomic returns ceil(amount * 10^decimals) for a non-negative amount.
func CeilAtomic(amount *big.Rat, decimals int) *big.Int {
	scaled := new(big.Rat).Mul(amount, new(big.Rat).SetInt(pow10(decimals)))
	q, m := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// AtomicToDecimal renders an atomic amount as a plain decimal string in whole
// tokens, trimming trailing zeros ("1000" at 6 decimals → "0.001").
func AtomicToDecimal(atomic *big.Int, decimals int) string {
	s := atomic.String()
	if decimals <= 0 {
		return s
	}
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	intPart, frac := s[:len(s)-decimals], strings.TrimRight(s[len(s)-decimals:], "0")
	if frac == "" {
		return intPart
	}
	return intPart + "." + frac
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func tokenLabel(symbol string) string {
	if s := strings.TrimSpace(symbol); s != "" {
		return s
	}
	return "this token"
}

// trimDecimalString drops trailing fractional zeros (and a bare '.').
func trimDecimalString(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
