// Package buy contains host-side helpers for `obol buy` commands.
//
// Today this is primarily pre-flight logic for `obol buy inference`:
//   - probe the seller's 402 pricing response
//   - verify the seller's ERC-8004 registration document against the priced chain
//   - reject budgets that would exceed the advertised one-request price floor
//
// Signing, PurchaseRequest creation, facilitator interaction, and refill
// bookkeeping all live inside the agent pod's buy.py — see
// internal/embed/skills/buy-x402/scripts/buy.py.
package buy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	internalx402 "github.com/ObolNetwork/obol-stack/internal/x402"
)

const (
	// wellKnownPath is the canonical ERC-8004 registration document path.
	wellKnownPath = "/.well-known/agent-registration.json"

	// registrationFetchTimeout caps the .well-known GET. The pre-flight should
	// not block a buy for more than a few seconds; on timeout the user can
	// re-run with --no-verify-identity.
	registrationFetchTimeout = 5 * time.Second

	// pricingProbeTimeout caps the host-side 402 probe. buy.py will probe again
	// in-pod, but the host pre-flight should fail fast when the seller URL is
	// free, malformed, or otherwise not x402-gated.
	pricingProbeTimeout = 15 * time.Second
)

// PricingResponse is the subset of the seller's x402 402 body that the host
// pre-flight needs before dispatching into buy.py.
type PricingResponse struct {
	X402Version int             `json:"x402Version"`
	Accepts     []PaymentOption `json:"accepts"`
}

// PaymentOption is one entry from the seller's x402 accepts array.
type PaymentOption struct {
	PayTo             string `json:"payTo"`
	Network           string `json:"network"`
	Asset             string `json:"asset"`
	Amount            string `json:"amount"`
	MaxAmountRequired string `json:"maxAmountRequired"`
}

func (p PaymentOption) requiredAmount() string {
	if strings.TrimSpace(p.Amount) != "" {
		return strings.TrimSpace(p.Amount)
	}
	return strings.TrimSpace(p.MaxAmountRequired)
}

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

// FetchSellerPricing probes the seller's chat-completions endpoint and returns
// the parsed x402 402 body. sellerURL may be either the service root
// (`.../services/foo`) or the final chat-completions URL.
func FetchSellerPricing(ctx context.Context, sellerURL, model string) (*PricingResponse, error) {
	pricingURL, err := pricingURL(sellerURL)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"model": strings.TrimSpace(model),
		"messages": []map[string]string{{
			"role":    "user",
			"content": "ping",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal pricing probe: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, pricingProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pricingURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build pricing probe: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", pricingURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read pricing body: %w", err)
	}

	if resp.StatusCode != http.StatusPaymentRequired {
		preview := strings.TrimSpace(string(body))
		if preview == "" {
			return nil, fmt.Errorf("probe %s: expected HTTP 402, got HTTP %d", pricingURL, resp.StatusCode)
		}
		return nil, fmt.Errorf("probe %s: expected HTTP 402, got HTTP %d: %s", pricingURL, resp.StatusCode, preview)
	}

	var pricing PricingResponse
	if err := json.Unmarshal(body, &pricing); err != nil {
		return nil, fmt.Errorf("parse pricing JSON: %w", err)
	}
	if _, err := firstPaymentOption(&pricing); err != nil {
		return nil, err
	}
	return &pricing, nil
}

// VerifyAgentID returns nil iff at least one of reg.Registrations matches the
// expected ERC-8004 tokenId. The seller may publish multiple registrations
// (one per chain); a match on any of them is sufficient.
func VerifyAgentID(reg *erc8004.AgentRegistration, expected int64) error {
	return verifyAgentID(reg, expected, "")
}

// VerifyAgentIDOnRegistry requires the expected agentId to appear on the
// specific CAIP-10 registry identifier resolved from the seller's priced
// payment network.
func VerifyAgentIDOnRegistry(reg *erc8004.AgentRegistration, expected int64, expectedRegistry string) error {
	return verifyAgentID(reg, expected, expectedRegistry)
}

// VerifyAgentIDForPricing pins the ERC-8004 identity check to the seller's
// priced network so a matching tokenId on an unrelated registry does not pass.
func VerifyAgentIDForPricing(reg *erc8004.AgentRegistration, expected int64, pricing *PricingResponse) error {
	payment, err := firstPaymentOption(pricing)
	if err != nil {
		return err
	}
	registry, err := registryForPaymentNetwork(payment.Network)
	if err != nil {
		return fmt.Errorf("resolve agent registry for payment network %q: %w", payment.Network, err)
	}
	return VerifyAgentIDOnRegistry(reg, expected, registry)
}

// VerifySellerEndpoint ensures the requested seller URL is one of the service
// endpoints advertised in the seller's ERC-8004 registration document.
func VerifySellerEndpoint(reg *erc8004.AgentRegistration, sellerURL string) error {
	if reg == nil {
		return errors.New("nil registration")
	}
	want, err := normalizedEndpointIdentity(sellerURL)
	if err != nil {
		return err
	}

	advertised := make([]string, 0, len(reg.Services))
	for _, svc := range reg.Services {
		endpoint := strings.TrimSpace(svc.Endpoint)
		if endpoint == "" {
			continue
		}
		advertised = append(advertised, endpoint)
		have, err := normalizedEndpointIdentity(endpoint)
		if err != nil {
			continue
		}
		if have == want {
			return nil
		}
	}

	if len(advertised) == 0 {
		return errors.New("registration document has no service endpoints")
	}
	return fmt.Errorf("seller endpoint mismatch: requested %s, registration advertises [%s]", want, strings.Join(advertised, ", "))
}

// ValidateBudgetAgainstPricing rejects budgets smaller than one request price.
// buy.py currently rounds `budget // price` up to at least one auth; the host
// CLI advertises --budget as a spending cap, so we fail early instead of
// silently overspending the requested cap.
func ValidateBudgetAgainstPricing(budgetMicro string, pricing *PricingResponse) error {
	payment, err := firstPaymentOption(pricing)
	if err != nil {
		return err
	}

	networkName, err := canonicalPaymentNetworkName(payment.Network)
	if err != nil {
		return fmt.Errorf("resolve payment network %q: %w", payment.Network, err)
	}
	canonicalUSDC, ok := internalx402.ResolveToken("USDC", networkName)
	if !ok {
		return fmt.Errorf("host-side --budget currently supports only USDC-priced sellers; no canonical USDC metadata is configured for %s", payment.Network)
	}
	if asset := strings.TrimSpace(payment.Asset); asset != "" && !strings.EqualFold(asset, canonicalUSDC.Address) {
		return fmt.Errorf("host-side --budget currently supports only USDC-priced sellers; seller requires asset %s on %s", asset, payment.Network)
	}

	budget, ok := new(big.Int).SetString(strings.TrimSpace(budgetMicro), 10)
	if !ok || budget.Sign() <= 0 {
		return fmt.Errorf("invalid budget micro-units %q", budgetMicro)
	}

	priceRaw := payment.requiredAmount()
	if priceRaw == "" {
		return errors.New("pricing response missing amount/maxAmountRequired")
	}
	price, ok := new(big.Int).SetString(priceRaw, 10)
	if !ok || price.Sign() <= 0 {
		return fmt.Errorf("pricing response has invalid amount %q", priceRaw)
	}
	if budget.Cmp(price) < 0 {
		return fmt.Errorf("--budget %s is smaller than one request price %s on %s; raise the budget or buy.py will round up to one auth and exceed the requested cap", budget.String(), price.String(), payment.Network)
	}
	return nil
}

func verifyAgentID(reg *erc8004.AgentRegistration, expected int64, expectedRegistry string) error {
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
		if r.AgentID != expected {
			continue
		}
		if expectedRegistry == "" || strings.EqualFold(strings.TrimSpace(r.AgentRegistry), strings.TrimSpace(expectedRegistry)) {
			return nil
		}
	}

	got := make([]string, 0, len(reg.Registrations))
	for _, r := range reg.Registrations {
		got = append(got, fmt.Sprintf("%d@%s", r.AgentID, r.AgentRegistry))
	}
	if expectedRegistry == "" {
		return fmt.Errorf("seller agentId mismatch: expected %d, got [%s] — re-run with --no-verify-identity to bypass if you trust the URL", expected, strings.Join(got, ", "))
	}
	return fmt.Errorf("seller registration mismatch: expected agentId %d on %s, got [%s] — re-run with --no-verify-identity to bypass if you trust the URL", expected, expectedRegistry, strings.Join(got, ", "))
}

func firstPaymentOption(pricing *PricingResponse) (*PaymentOption, error) {
	if pricing == nil {
		return nil, errors.New("nil pricing response")
	}
	if len(pricing.Accepts) == 0 {
		return nil, errors.New("pricing response has no payment options")
	}
	return &pricing.Accepts[0], nil
}

func registryForPaymentNetwork(network string) (string, error) {
	networkName, err := canonicalPaymentNetworkName(network)
	if err != nil {
		return "", err
	}
	cfg, err := erc8004.ResolveNetwork(networkName)
	if err != nil {
		return "", err
	}
	return cfg.CAIP10Registry(), nil
}

func canonicalPaymentNetworkName(network string) (string, error) {
	net := strings.TrimSpace(network)
	if net == "" {
		return "", errors.New("empty payment network")
	}
	if strings.HasPrefix(net, "eip155:") {
		parts := strings.Split(net, ":")
		if len(parts) != 2 {
			return "", fmt.Errorf("unsupported CAIP-2 payment network %q", network)
		}
		switch parts[1] {
		case "1":
			return erc8004.Ethereum.Name, nil
		case "8453":
			return erc8004.Base.Name, nil
		case "84532":
			return erc8004.BaseSepolia.Name, nil
		default:
			return "", fmt.Errorf("unsupported EIP-155 chain id %q", parts[1])
		}
	}
	cfg, err := erc8004.ResolveNetwork(net)
	if err != nil {
		return "", err
	}
	return cfg.Name, nil
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

func pricingURL(sellerURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(sellerURL))
	if err != nil {
		return "", fmt.Errorf("parse seller URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("seller URL must include scheme and host: %q", sellerURL)
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	if path == "" {
		u.Path = "/v1/chat/completions"
	} else {
		u.Path = path + "/v1/chat/completions"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizedEndpointIdentity(raw string) (string, error) {
	normalized, err := pricingURL(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
