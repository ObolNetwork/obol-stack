package x402

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"strings"

	x402types "github.com/coinbase/x402/go/types"
)

//go:embed templates/payment_required.html
var paymentRequiredHTMLSrc string

var paymentRequiredTmpl = template.Must(
	template.New("payment_required").Parse(paymentRequiredHTMLSrc),
)

// PaymentDisplay is the display-only context the verifier passes to the
// content-negotiated 402 renderer when it wants the HTML page (vs. raw JSON).
// All fields are pre-formatted for direct interpolation into the template;
// the renderer does no number formatting or address truncation itself.
type PaymentDisplay struct {
	// Endpoint is the human-friendly path the buyer is hitting (e.g. "/services/agent-quant").
	Endpoint string

	// Network is the chain ID used for matching (e.g. "base-sepolia").
	Network string

	// NetworkLabel is the human-friendly chain name (e.g. "Base Sepolia").
	NetworkLabel string

	// AssetSymbol is the token symbol (e.g. "USDC", "OBOL").
	AssetSymbol string

	// AssetAddress is the token contract address.
	AssetAddress string

	// PriceDisplay is the formatted price including symbol (e.g. "0.001 USDC per request").
	PriceDisplay string

	// PriceAtomic is the raw atomic-units amount as a string.
	PriceAtomic string

	// PayToFull is the full recipient wallet address (lowercased 0x...).
	PayToFull string

	// ExplorerURL is the block-explorer link for PayToFull on the matched
	// chain (e.g. https://basescan.org/address/0x...). Empty when the chain
	// isn't in the explorer registry.
	ExplorerURL string
}

// SendPaymentRequiredFunc is the renderer signature compatible with the
// existing JSON 402 path. Verifiers that want HTML responses inject a
// content-negotiated wrapper via ForwardAuthConfig.
type SendPaymentRequiredFunc func(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any)

// NewHTMLAwarePaymentRequired returns a renderer that emits HTML when the
// client's Accept header advertises text/html (browsers, social-media link
// preview scrapers), and falls back to the raw JSON 402 body otherwise
// (curl with no Accept, x402 buyer agents, default behaviour).
//
// The HTTP status remains 402 in both branches — only the body shape changes.
//
// display carries pre-formatted, route-specific copy. It must not be nil; pass
// a zero value if no per-route context is available and the template will
// degrade gracefully.
func NewHTMLAwarePaymentRequired(display PaymentDisplay) SendPaymentRequiredFunc {
	return func(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) {
		if !prefersHTML(r.Header.Get("Accept")) {
			sendPaymentRequiredJSON(w, r, requirements, extensions)
			return
		}
		sendPaymentRequiredHTML(w, r, requirements, extensions, display)
	}
}

// prefersHTML returns true when the Accept header advertises text/html as a
// type the client will accept, including */* with text/html present, but NOT
// when Accept is empty (default to JSON for unspecified clients — agents,
// curl, x402-buyer).
func prefersHTML(accept string) bool {
	if accept == "" {
		return false
	}
	for _, part := range strings.Split(accept, ",") {
		mt := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(mt, "text/html") || strings.EqualFold(mt, "application/xhtml+xml") {
			return true
		}
	}
	return false
}

// resolveSiteURL derives the public-facing origin (scheme + host) from the
// incoming request. Mirrors buildResourceURL but keeps just the origin so
// rendered HTML can reference sibling routes (storefront, OG image) on the
// same tunnel host the scraper or browser is currently hitting.
func resolveSiteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

// sendPaymentRequiredHTML writes a 402 status with an HTML body that includes
// full OG metadata, a service-info card, three "ways to pay" prompt cards
// (Obol Agent, other AI agent, raw JSON), and copy buttons.
func sendPaymentRequiredHTML(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any, display PaymentDisplay) {
	resource := &x402types.ResourceInfo{
		URL:         buildResourceURL(r),
		Description: "Payment required for " + r.URL.Path,
	}
	jsonBody := x402types.PaymentRequired{
		X402Version: 2,
		Error:       "Payment required for this resource",
		Resource:    resource,
		Accepts:     requirements,
		Extensions:  extensions,
	}
	indented, err := json.MarshalIndent(jsonBody, "", "  ")
	if err != nil {
		// Should not happen — fall back to JSON path.
		sendPaymentRequiredJSON(w, r, requirements, extensions)
		return
	}

	siteURL := resolveSiteURL(r)
	pageURL := buildResourceURL(r)
	endpoint := display.Endpoint
	if endpoint == "" {
		endpoint = r.URL.Path
	}
	networkLabel := display.NetworkLabel
	if networkLabel == "" {
		networkLabel = display.Network
	}

	priceDisplay := display.PriceDisplay
	if priceDisplay == "" && len(requirements) > 0 {
		priceDisplay = formatAmount(requirements[0].Amount, 0, "") + " (atomic units)"
	}

	payToFull := display.PayToFull
	if payToFull == "" && len(requirements) > 0 {
		payToFull = requirements[0].PayTo
	}
	payToDisplay := truncateAddress(payToFull)

	promptObol := buildObolPrompt(siteURL, endpoint, display)
	promptOther := buildOtherAgentPrompt(siteURL, endpoint, display)

	data := struct {
		Title         string
		Description   string
		PageURL       string
		StorefrontURL string
		WordmarkURL   string
		OGImageURL    string
		Endpoint      string
		NetworkLabel  string
		PriceDisplay  string
		PayToDisplay  string
		PayToFull     string
		ExplorerURL   string
		PromptObol    string
		PromptOther   string
		JSONBody      string
	}{
		Title:         "Payment required — Obol Stack",
		Description:   buildMetaDescription(display),
		PageURL:       pageURL,
		StorefrontURL: siteURL,
		WordmarkURL:   siteURL + "/obol-stack-logo.png",
		OGImageURL:    siteURL + "/og-payment-required.png",
		Endpoint:      endpoint,
		NetworkLabel:  networkLabel,
		PriceDisplay:  priceDisplay,
		PayToDisplay:  payToDisplay,
		PayToFull:     payToFull,
		ExplorerURL:   display.ExplorerURL,
		PromptObol:    promptObol,
		PromptOther:   promptOther,
		JSONBody:      string(indented),
	}

	var buf bytes.Buffer
	if err := paymentRequiredTmpl.Execute(&buf, data); err != nil {
		sendPaymentRequiredJSON(w, r, requirements, extensions)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(buf.Bytes())
}

// buildMetaDescription returns the shared og/twitter/description string. Uses
// the dynamic price+asset+network when available; otherwise the static fallback.
func buildMetaDescription(d PaymentDisplay) string {
	if d.PriceDisplay != "" && d.NetworkLabel != "" {
		return fmt.Sprintf("Unlock this Obol Agent service. Pay %s on %s, settled via x402.", d.PriceDisplay, d.NetworkLabel)
	}
	return "Unlock this Obol Agent service. Pay per call in USDC or OBOL, settled via x402."
}

// buildObolPrompt generates the natural-language instruction the user sends
// to their own Obol Agent. The agent already has the buy-x402 skill loaded,
// so the prompt only needs to identify the endpoint, price, asset, network.
func buildObolPrompt(siteURL, endpoint string, d PaymentDisplay) string {
	url := siteURL + endpoint
	priceClause := ""
	if d.PriceDisplay != "" {
		priceClause = " Pay " + d.PriceDisplay + "."
	}
	netClause := ""
	if d.NetworkLabel != "" {
		netClause = " Network: " + d.NetworkLabel + "."
	}
	return fmt.Sprintf("Use the buy-x402 skill to buy access to %s.%s%s", url, priceClause, netClause)
}

// buildOtherAgentPrompt generates a self-contained instruction for any
// generic AI agent (Claude, ChatGPT, Gemini, etc.) that does NOT have the
// Obol skills pre-loaded. It points the agent at obol.org/llms.txt and
// the public skills repo so it can self-orient before signing the payment.
func buildOtherAgentPrompt(siteURL, endpoint string, d PaymentDisplay) string {
	url := siteURL + endpoint
	priceClause := "the listed price"
	if d.PriceDisplay != "" {
		priceClause = d.PriceDisplay
	}
	netClause := ""
	if d.NetworkLabel != "" {
		netClause = " on " + d.NetworkLabel
	}
	return fmt.Sprintf(
		"Read https://obol.org/llms.txt and skim https://github.com/ObolNetwork/skills "+
			"to learn how Obol Agents pay for x402 services. Then help me buy access to %s "+
			"for %s%s. Sign the EIP-3009 or Permit2 authorisation and call the endpoint "+
			"with the X-PAYMENT header.",
		url, priceClause, netClause,
	)
}

// truncateAddress shortens a hex address for display: 0xa1b2c3...f9c0.
// Returns the input unchanged if it doesn't look like an 0x address.
func truncateAddress(addr string) string {
	if !strings.HasPrefix(addr, "0x") || len(addr) < 10 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-4:]
}

// formatAmount converts an atomic-unit amount string to a decimal-formatted
// string using the asset's decimals and symbol. Empty symbol yields just the
// number. Strips trailing zeros from the fractional part.
func formatAmount(atomic string, decimals int, symbol string) string {
	if atomic == "" {
		return ""
	}
	amount, ok := new(big.Int).SetString(atomic, 10)
	if !ok {
		if symbol != "" {
			return atomic + " " + symbol + " (atomic)"
		}
		return atomic + " (atomic)"
	}
	if decimals <= 0 {
		if symbol != "" {
			return amount.String() + " " + symbol
		}
		return amount.String()
	}
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Quo(amount, pow)
	frac := new(big.Int).Mod(amount, pow)

	fracStr := fmt.Sprintf("%0*d", decimals, frac)
	fracStr = strings.TrimRight(fracStr, "0")

	num := whole.String()
	if fracStr != "" {
		num = num + "." + fracStr
	}
	if symbol != "" {
		return num + " " + symbol
	}
	return num
}

// FormatPriceDisplay builds the "0.001 USDC per request" display string the
// verifier passes into PaymentDisplay.PriceDisplay. Exposed for tests and
// for callers that want to construct the display struct directly.
func FormatPriceDisplay(atomic string, decimals int, symbol string) string {
	formatted := formatAmount(atomic, decimals, symbol)
	if formatted == "" {
		return ""
	}
	return formatted + " per request"
}
