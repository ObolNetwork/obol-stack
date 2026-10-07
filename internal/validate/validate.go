// Package validate provides input validation functions for the obol CLI.
// Each function returns nil on success or a descriptive error.
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
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
