// Package validate provides input validation functions for the obol CLI.
// Each function returns nil on success or a descriptive error.
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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

// Namespace validates a Kubernetes namespace (same rules as Name).
func Namespace(s string) error {
	if err := Name(s); err != nil {
		return fmt.Errorf("invalid namespace: %w", err)
	}
	return nil
}

// WalletAddress validates an Ethereum wallet address (0x-prefixed, 42 hex chars).
func WalletAddress(s string) error {
	if s == "" {
		return fmt.Errorf("wallet address cannot be empty")
	}
	if !strings.HasPrefix(s, "0x") {
		return fmt.Errorf("wallet address must start with 0x: %q", s)
	}
	if len(s) != 42 {
		return fmt.Errorf("wallet address must be 42 characters (got %d): %q", len(s), s)
	}
	for _, c := range s[2:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("wallet address contains invalid hex character: %q", s)
		}
	}
	return nil
}

// knownChains is the set of valid chain names for x402 and ERC-8004.
var knownChains = map[string]bool{
	"base":             true,
	"base-mainnet":     true,
	"base-sepolia":     true,
	"ethereum":         true,
	"ethereum-mainnet": true,
	"mainnet":          true,
}

// ChainName validates a blockchain chain name.
func ChainName(s string) error {
	if s == "" {
		return fmt.Errorf("chain name cannot be empty")
	}
	if !knownChains[strings.ToLower(s)] {
		return fmt.Errorf("unknown chain %q (supported: base-sepolia, base, ethereum)", s)
	}
	return nil
}

// Price validates a decimal price string (positive, parseable as float).
func Price(s string) error {
	if s == "" {
		return fmt.Errorf("price cannot be empty")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid price %q: %w", s, err)
	}
	if f < 0 {
		return fmt.Errorf("price must be non-negative: %q", s)
	}
	return nil
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

// Path validates a URL path segment (no path traversal, no control chars).
func Path(s string) error {
	if s == "" {
		return nil // empty path is valid
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("path must not contain '..': %q", s)
	}
	if strings.Contains(s, "%2e") || strings.Contains(s, "%2E") {
		return fmt.Errorf("path must not contain encoded traversal: %q", s)
	}
	if err := NoControlChars(s); err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	return nil
}

// NoControlChars rejects strings containing control characters (except \n and \t).
func NoControlChars(s string) error {
	for i, c := range s {
		if c < 0x20 && c != '\n' && c != '\t' {
			return fmt.Errorf("contains control character at position %d (0x%02x)", i, c)
		}
	}
	return nil
}
