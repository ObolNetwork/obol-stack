// Package buy contains host-side helpers for `obol buy` commands.
//
// Today this is just the ERC-8004 identity pre-flight that
// `obol buy inference` runs before signing any payment authorizations.
// Signing, PurchaseRequest creation, facilitator interaction, and refill
// bookkeeping all live inside the agent pod's buy.py — see
// internal/embed/skills/buy-x402/scripts/buy.py.
package buy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
)

// wellKnownPath is the canonical ERC-8004 registration document path.
const wellKnownPath = "/.well-known/agent-registration.json"

// registrationFetchTimeout caps the .well-known GET. The pre-flight should
// not block a buy for more than a few seconds; on timeout the user can
// re-run with --no-verify-identity.
const registrationFetchTimeout = 5 * time.Second

// FetchSellerRegistration fetches and parses the seller's ERC-8004
// registration document. sellerURL may be either the seller's base URL
// (e.g. "https://demo-seller.obol.tech") or a service URL (e.g.
// ".../services/foo"); the path component is replaced with the
// well-known path so callers can pass either shape.
func FetchSellerRegistration(ctx context.Context, sellerURL string) (*erc8004.AgentRegistration, error) {
	wellKnown, err := wellKnownURL(sellerURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, registrationFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, fmt.Errorf("build registration request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", wellKnown, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", wellKnown, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read registration body: %w", err)
	}

	var reg erc8004.AgentRegistration
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, fmt.Errorf("parse registration JSON: %w", err)
	}
	return &reg, nil
}

// VerifyAgentID returns nil iff at least one of reg.Registrations matches
// the expected ERC-8004 tokenId. The seller may publish multiple
// registrations (one per chain); a match on any of them is sufficient.
func VerifyAgentID(reg *erc8004.AgentRegistration, expected int64) error {
	if reg == nil {
		return errors.New("nil registration")
	}
	if expected == 0 {
		return errors.New("expected agentId is 0; default seller is not yet provisioned — pass --seller and --no-verify-identity to bypass, or set DefaultBuySellerAgentID")
	}
	if len(reg.Registrations) == 0 {
		return errors.New("registration document has no on-chain registrations")
	}
	for _, r := range reg.Registrations {
		if r.AgentID == expected {
			return nil
		}
	}

	got := make([]string, 0, len(reg.Registrations))
	for _, r := range reg.Registrations {
		got = append(got, fmt.Sprintf("%d@%s", r.AgentID, r.AgentRegistry))
	}
	return fmt.Errorf("seller agentId mismatch: expected %d, got [%s] — re-run with --no-verify-identity to bypass if you trust the URL", expected, strings.Join(got, ", "))
}

// wellKnownURL replaces the path of sellerURL with the .well-known path,
// preserving scheme and host. Any query string is dropped.
func wellKnownURL(sellerURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(sellerURL))
	if err != nil {
		return "", fmt.Errorf("parse seller URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("seller URL must include scheme and host: %q", sellerURL)
	}
	u.Path = wellKnownPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
