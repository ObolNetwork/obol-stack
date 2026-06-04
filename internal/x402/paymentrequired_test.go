package x402

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	x402types "github.com/coinbase/x402/go/types"
)

const (
	testAmount = "1000"
	testAsset  = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	testPayTo  = "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8f9c0"
)

func sampleRequirement() x402types.PaymentRequirements {
	return x402types.PaymentRequirements{
		Scheme:  "exact",
		Network: "base-sepolia",
		Asset:   testAsset,
		Amount:  testAmount,
		PayTo:   testPayTo,
	}
}

func sampleDisplay() PaymentDisplay {
	return PaymentDisplay{
		Endpoint:     "/services/agent-quant",
		Network:      "base-sepolia",
		NetworkLabel: "Base Sepolia",
		AssetSymbol:  "USDC",
		AssetAddress: testAsset,
		PriceDisplay: "0.001 USDC per request",
		PriceAtomic:  testAmount,
		PayToFull:    testPayTo,
		ExplorerURL:  "https://sepolia.basescan.org/address/" + testPayTo,
	}
}

func TestPrefersHTML(t *testing.T) {
	cases := []struct {
		accept string
		want   bool
	}{
		// Defaults to JSON when Accept is unset (curl, x402 buyer, agents).
		{"", false},
		{"*/*", false},
		{"application/json", false},
		{"application/json, */*", false},

		// Browsers and link-preview scrapers advertise text/html.
		{"text/html", true},
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", true},
		{"text/html;q=0.9,application/json;q=0.8", true},
		{"application/xhtml+xml", true},

		// Whitespace and case insensitivity.
		{"  TEXT/HTML  ", true},
	}
	for _, tc := range cases {
		if got := prefersHTML(tc.accept); got != tc.want {
			t.Errorf("prefersHTML(%q) = %v, want %v", tc.accept, got, tc.want)
		}
	}
}

// JSON is the default when Accept is unset — agents and the existing wire
// contract must keep working byte-for-byte.
func TestHTMLAware_DefaultsToJSONWhenAcceptMissing(t *testing.T) {
	render := NewHTMLAwarePaymentRequired(sampleDisplay())
	r := httptest.NewRequest("GET", "/services/agent-quant", nil)
	w := httptest.NewRecorder()

	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var parsed x402types.PaymentRequired
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, w.Body.String())
	}
	if parsed.X402Version != 2 {
		t.Errorf("x402Version = %d, want 2", parsed.X402Version)
	}
	if len(parsed.Accepts) != 1 || parsed.Accepts[0].Amount != testAmount {
		t.Errorf("accepts mismatch: %+v", parsed.Accepts)
	}
}

// HTML rendered when Accept advertises text/html — but status is still 402.
func TestHTMLAware_RendersHTMLOnTextHTML(t *testing.T) {
	render := NewHTMLAwarePaymentRequired(sampleDisplay())
	r := httptest.NewRequest("GET", "/services/agent-quant", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	r.Header.Set("X-Forwarded-Host", "agent.example.tunnel.dev")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (status must stay 402 even with HTML body)", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}

	body := w.Body.String()

	// OG metadata must be present, with absolute URLs derived from forwarded host.
	mustContain(t, body, `<title>Payment required — Obol Stack</title>`)
	mustContain(t, body, `property="og:title"`)
	mustContain(t, body, `property="og:image" content="https://agent.example.tunnel.dev/og-payment-required.png"`)
	mustContain(t, body, `property="og:url" content="https://agent.example.tunnel.dev/services/agent-quant"`)
	mustContain(t, body, `name="twitter:card" content="summary_large_image"`)

	// Service-info card must render the dynamic display values.
	mustContain(t, body, "/services/agent-quant")
	mustContain(t, body, "Base Sepolia")
	mustContain(t, body, "0.001 USDC per request")
	// The service-info card shows a truncated address; the embedded raw
	// JSON necessarily still contains the full one.
	mustContain(t, body, "0xa1b2…f9c0")

	// Copy + explorer-link buttons on the Pay-To row, with the full address
	// only delivered to the JS clipboard handler (not the visible truncated text).
	mustContain(t, body, `data-copy="`+testPayTo+`"`)
	mustContain(t, body, `href="https://sepolia.basescan.org/address/`+testPayTo+`"`)
	mustContain(t, body, "View on block explorer")

	// All three "ways to pay" prompts must be present, including the agent
	// instructions referencing the buy-x402 skill, llms.txt, and the public
	// skills repo.
	mustContain(t, body, "Pay with your Obol Agent")
	mustContain(t, body, "buy-x402 skill")
	mustContain(t, body, "Pay with another AI agent")
	mustContain(t, body, "https://obol.org/llms.txt")
	mustContain(t, body, "https://github.com/ObolNetwork/skills")
	mustContain(t, body, "Pay manually (raw HTTP 402)")

	// Footer link back to the same tunnel root (storefront).
	mustContain(t, body, `href="https://agent.example.tunnel.dev"`)
	// "What is x402?" link.
	mustContain(t, body, "https://x402.org")

	// Raw JSON body embedded for x402-aware tools.
	mustContain(t, body, `&#34;x402Version&#34;: 2`)
}

// When the rule context is empty (e.g. in-process gateway with no display
// data), the renderer must still produce HTML and not crash.
func TestHTMLAware_DegradeWithoutDisplay(t *testing.T) {
	render := NewHTMLAwarePaymentRequired(PaymentDisplay{})
	r := httptest.NewRequest("GET", "/anything", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()

	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	mustContain(t, body, "Payment required")
	mustContain(t, body, "/anything") // endpoint falls back to URL.Path
	mustContain(t, body, "1000 (atomic units)") // price falls back to atomic units
}

// Inference offers should surface the canonical `obol buy inference` CLI
// command as the primary Buy card with the seller URL and model id
// pre-filled, plus the operator-supplied description on the Service card.
func TestHTMLAware_InferenceShowsCLIPrimaryAndDescription(t *testing.T) {
	d := sampleDisplay()
	d.OfferType = "inference"
	d.OfferName = "aeon7"
	d.Model = "AEON-7"
	d.OfferDescription = "Remote 35B reasoning model with 32k context."

	render := NewHTMLAwarePaymentRequired(d)
	r := httptest.NewRequest("GET", "/services/aeon7", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("X-Forwarded-Host", "agent.example.tunnel.dev")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	body := w.Body.String()
	mustContain(t, body, "Buy with the Obol CLI")
	mustContain(t, body, "obol buy inference aeon7")
	mustContain(t, body, "--model AEON-7")
	mustContain(t, body, "https://agent.example.tunnel.dev/services/agent-quant")
	mustContain(t, body, "paid/AEON-7")
	// Operator description bubbles into Service card + OG.
	mustContain(t, body, "Remote 35B reasoning model with 32k context.")
	// Lede explains that you're paying for remote inference, not an agent.
	mustContain(t, body, "remote model time")
}

// Agent offers should explain that the buyer is paying an autonomous
// agent (not a model), surface its skills as pills under the description,
// and append a chat-completions example next to the raw x402 JSON in the
// "Pay manually" card rather than a separate primary CTA.
func TestHTMLAware_AgentShowsChatCompletionsInPayManually(t *testing.T) {
	d := sampleDisplay()
	d.OfferType = "agent"
	d.OfferName = "quant"
	d.Model = "qwen3.5:9b"
	d.OfferDescription = "A simple example agent that can analyse Ethereum and Base for you"
	d.AgentSkills = []string{"ethereum-networks", "gas", "addresses"}

	render := NewHTMLAwarePaymentRequired(d)
	r := httptest.NewRequest("GET", "/services/quant", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	body := w.Body.String()

	// Primary "Send a prompt" card is gone — the example body lives in
	// the Pay-manually card instead, after the raw x402 JSON.
	if strings.Contains(body, "Send a prompt (OpenAI chat-completions)") {
		t.Errorf("agent-type 402 page should NOT render a primary 'Send a prompt' card")
	}
	mustContain(t, body, "Pay manually (raw HTTP 402)")
	mustContain(t, body, "Obol Agents accept OpenAI-style chat-completions bodies")
	// Example chat-completions body (JSON snippet inside <pre>; html/template
	// escapes the quotes).
	mustContain(t, body, `&#34;model&#34;: &#34;qwen3.5:9b&#34;`)
	mustContain(t, body, `&#34;messages&#34;:`)

	// Lede uses the operator-facing copy and links to docs.obol.org.
	mustContain(t, body, "payment gate for an Obol Agent")
	mustContain(t, body, "future of agentic commerce")
	mustContain(t, body, `href="https://docs.obol.org/obol-stack"`)

	// Description renders in the Service card.
	mustContain(t, body, "A simple example agent that can analyse Ethereum and Base for you")

	// Skills render as pills under the description.
	mustContain(t, body, `<span class="skill-pill">ethereum-networks</span>`)
	mustContain(t, body, `<span class="skill-pill">gas</span>`)
	mustContain(t, body, `<span class="skill-pill">addresses</span>`)
}

// HTTP offers (the default) keep the existing single-prompt Pay-with-Obol
// CTA — no CLI command, no chat-completions body.
func TestHTMLAware_HTTPKeepsLegacyCopy(t *testing.T) {
	d := sampleDisplay()
	d.OfferType = "http"

	render := NewHTMLAwarePaymentRequired(d)
	r := httptest.NewRequest("GET", "/services/agent-quant", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	render(w, r, []x402types.PaymentRequirements{sampleRequirement()}, nil)

	body := w.Body.String()
	mustContain(t, body, "Pay with your Obol Agent")
	mustContain(t, body, "buy-x402 skill")
	if strings.Contains(body, "obol buy inference") {
		t.Errorf("http-type 402 page should NOT show the inference CLI primary card")
	}
}

func TestFormatAmount(t *testing.T) {
	cases := []struct {
		atomic   string
		decimals int
		symbol   string
		want     string
	}{
		{"1000", 6, "USDC", "0.001 USDC"},
		{"1000000", 6, "USDC", "1 USDC"},
		{"1500000", 6, "USDC", "1.5 USDC"},
		{"1234567", 6, "USDC", "1.234567 USDC"},
		{"100", 0, "WEI", "100 WEI"},
		{"", 6, "USDC", ""},
		{"abc", 6, "USDC", "abc USDC (atomic)"},
	}
	for _, tc := range cases {
		if got := formatAmount(tc.atomic, tc.decimals, tc.symbol); got != tc.want {
			t.Errorf("formatAmount(%q, %d, %q) = %q, want %q", tc.atomic, tc.decimals, tc.symbol, got, tc.want)
		}
	}
}

func TestTruncateAddress(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8f9c0", "0xa1b2…f9c0"},
		{"0xshort", "0xshort"}, // too short, returned as-is
		{"not-an-address", "not-an-address"},
	}
	for _, tc := range cases {
		if got := truncateAddress(tc.in); got != tc.want {
			t.Errorf("truncateAddress(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Regression: previously buildPaymentDisplay passed the decimal rule.Price
// into formatAmount (which expects atomic), so 1 OBOL with 18 decimals
// rendered as "0.000000000000000001 OBOL per request" while the JSON
// correctly showed 1e18 atomic. Display must mirror the wire amount.
func TestFormatPriceDisplay_HighDecimals(t *testing.T) {
	got := FormatPriceDisplay("1000000000000000000", 18, "OBOL")
	want := "1 OBOL per request"
	if got != want {
		t.Errorf("FormatPriceDisplay(1e18, 18, OBOL) = %q, want %q", got, want)
	}
}

func TestExplorerAddressURL(t *testing.T) {
	addr := "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8f9c0"
	cases := []struct {
		network, want string
	}{
		{"base", "https://basescan.org/address/" + addr},
		{"base-sepolia", "https://sepolia.basescan.org/address/" + addr},
		{"ethereum", "https://etherscan.io/address/" + addr},
		{"mainnet", "https://etherscan.io/address/" + addr},
		{"polygon", "https://polygonscan.com/address/" + addr},
		{"arbitrum", "https://arbiscan.io/address/" + addr},
		{"unknown-chain", ""},
	}
	for _, tc := range cases {
		if got := explorerAddressURL(tc.network, addr); got != tc.want {
			t.Errorf("explorerAddressURL(%q) = %q, want %q", tc.network, got, tc.want)
		}
	}
	if got := explorerAddressURL("base", "not-an-address"); got != "" {
		t.Errorf("explorerAddressURL with bad address = %q, want empty", got)
	}
}

func TestHumanizeNetwork(t *testing.T) {
	cases := map[string]string{
		"base":             "Base",
		"base-sepolia":     "Base Sepolia",
		"polygon-amoy":     "Polygon Amoy",
		"arbitrum-sepolia": "Arbitrum Sepolia",
		"unknown-chain":    "Unknown Chain", // best-effort title-case
		"":                 "",
	}
	for in, want := range cases {
		if got := humanizeNetwork(in); got != want {
			t.Errorf("humanizeNetwork(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("body does not contain %q", needle)
	}
}
