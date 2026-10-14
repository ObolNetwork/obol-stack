// Package validate provides input validation functions for the obol CLI.
// Each function returns nil on success or a descriptive error.
package validate

import (
	"fmt"
	"net/url"
	"regexp"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

// nameRegex matches k8s-safe DNS labels: starts with lowercase alphanumeric,
// then lowercase alphanumeric or hyphens, max 63 chars.
var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Name validates a resource name (k8s-safe DNS label).
func Name(s string) error {
	if s == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !nameRegex.MatchString(s) {
		return fmt.Errorf("invalid name %q: must be lowercase alphanumeric with hyphens, 1-63 chars, starting with a letter or digit", s)
	}
	return nil
}

// Price validates a paid decimal price string exactly (no float parsing):
// plain digits with an optional single '.', at most 18 fractional digits
// (the most any ERC-20 token has), greater than zero and at most
// schemas.MaxPriceWholeTokens. Signs, exponents, Inf/NaN, commas and
// currency symbols are rejected. Free routes use gate=free, not a zero price.
func Price(s string) error {
	return schemas.ValidatePrice(s)
}

// PriceForToken is Price limited to the settlement token's decimals, e.g.
// "0.0000001" is rejected for USDC (6 decimals) with "max 6 decimal places
// for USDC".
func PriceForToken(s string, decimals int, symbol string) error {
	return schemas.ValidatePriceForToken(s, decimals, symbol)
}

// URL validates a URL string.
func URL(s string) error {
	if s == "" {
		return fmt.Errorf("URL cannot be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", s, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("URL missing scheme (http/https): %q", s)
	}
	if u.Host == "" {
		return fmt.Errorf("URL missing host: %q", s)
	}
	return nil
}
