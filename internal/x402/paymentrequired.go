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

	// OfferType is the ServiceOffer.spec.type that produced this route
	// (inference, agent, http, fine-tuning). Drives the type-aware lede,
	// Buy CTAs, and prompt copy. Empty means "http" semantics for the
	// renderer (the safest default — single-shot pay).
	OfferType string

	// OfferName is the originating ServiceOffer.metadata.name. The HTML
	// template surfaces it on the inference Buy card so users get a
	// concrete `obol buy inference <name>` they can paste.
	OfferName string

	// OfferDescription is the operator-supplied service description
	// (ServiceOffer.spec.registration.description). The renderer surfaces
	// it under the Service card and in the OG/meta description when set.
	OfferDescription string

	// AgentModel is the upstream model id (when known) — surfaced on the
	// inference Buy card so users can copy a full `obol buy inference`
	// command with --model pre-filled.
	AgentModel string

	// Model is the user-facing upstream model identifier (mirrors
	// ServiceOffer.spec.model.name for inference and AgentResolution.Model
	// for agent). Preferred over AgentModel for the inference Buy card
	// because it's populated for type=inference too.
	Model string

	// AgentSkills is the resolved skill allow-list for an agent-type
	// offer. Surfaced as pills under the description in the Service
	// card. Empty for non-agent offers and for agents that haven't yet
	// resolved their skill list.
	AgentSkills []string
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

	typeCopy := buildTypeCopy(siteURL, endpoint, display)

	data := struct {
		Title               string
		Description         string
		PageURL             string
		StorefrontURL       string
		WordmarkURL         string
		OGImageURL          string
		Endpoint            string
		NetworkLabel        string
		PriceDisplay        string
		PayToDisplay        string
		PayToFull           string
		ExplorerURL         string
		OfferDescription    string
		Skills              []string
		Lede                template.HTML
		ShowPrimary         bool
		PrimaryTitle        string
		PrimaryLede         string
		PrimaryIsCode       bool
		PrimaryPayload      string
		PromptObol          string
		PromptOther         string
		JSONBody            string
		ChatCompletionsNote string
		ChatCompletionsBody string
	}{
		Title:               "Payment required — Obol Stack",
		Description:         buildMetaDescription(display),
		PageURL:             pageURL,
		StorefrontURL:       siteURL,
		WordmarkURL:         siteURL + "/obol-stack-logo.png",
		OGImageURL:          siteURL + "/og-payment-required.png",
		Endpoint:            endpoint,
		NetworkLabel:        networkLabel,
		PriceDisplay:        priceDisplay,
		PayToDisplay:        payToDisplay,
		PayToFull:            payToFull,
		ExplorerURL:         display.ExplorerURL,
		OfferDescription:    display.OfferDescription,
		Skills:              display.AgentSkills,
		Lede:                typeCopy.Lede,
		ShowPrimary:         typeCopy.ShowPrimary,
		PrimaryTitle:        typeCopy.PrimaryTitle,
		PrimaryLede:         typeCopy.PrimaryLede,
		PrimaryIsCode:       typeCopy.PrimaryIsCode,
		PrimaryPayload:      typeCopy.PrimaryPayload,
		PromptObol:          typeCopy.PromptObol,
		PromptOther:         typeCopy.PromptOther,
		JSONBody:            string(indented),
		ChatCompletionsNote: typeCopy.ChatCompletionsNote,
		ChatCompletionsBody: typeCopy.ChatCompletionsBody,
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

// buildMetaDescription returns the shared og/twitter/description string. The
// operator-supplied description wins; otherwise we fall back to the dynamic
// price+asset+network blurb (or a static line when neither is available).
func buildMetaDescription(d PaymentDisplay) string {
	if d.OfferDescription != "" {
		return d.OfferDescription
	}
	if d.PriceDisplay != "" && d.NetworkLabel != "" {
		return fmt.Sprintf("Unlock this Obol Agent service. Pay %s on %s, settled via x402.", d.PriceDisplay, d.NetworkLabel)
	}
	return "Unlock this Obol Agent service. Pay per call in USDC or OBOL, settled via x402."
}

// typeCopy is the per-offer-type render payload. The renderer produces one
// of these from the PaymentDisplay so the template can stay branch-free.
type typeCopy struct {
	// Lede sits under <h1>Payment required</h1> and explains what the
	// service actually does at a high level. Typed as template.HTML so
	// branches can include trusted anchor tags (e.g. a docs link) — all
	// interpolated values must be either constant or first passed
	// through html/template's standard escaping; no PaymentDisplay
	// strings are interpolated raw.
	Lede template.HTML

	// ShowPrimary toggles the first "Buy" card. Inference and http
	// surface a primary CTA card (a copy-able CLI command or Obol-agent
	// prompt); agent type omits it because the actionable example lives
	// in the Pay-manually card alongside the raw 402 JSON.
	ShowPrimary bool

	// PrimaryTitle / PrimaryLede / PrimaryPayload drive the first "Buy"
	// card when ShowPrimary is true. For inference, PrimaryPayload is a
	// shell command and PrimaryIsCode is true so the template renders it
	// in <pre><code>; for http it's a natural-language prompt rendered
	// as a copy snippet.
	PrimaryTitle   string
	PrimaryLede    string
	PrimaryIsCode  bool
	PrimaryPayload string

	// PromptObol / PromptOther are the secondary copy cards (Obol agent
	// w/ buy-x402 skill, and generic AI agent). They follow the
	// pre-existing one-liner prompt shape.
	PromptObol  string
	PromptOther string

	// ChatCompletionsNote / ChatCompletionsBody render inside the "Pay
	// manually (raw HTTP 402)" card *after* the x402 wire JSON, and are
	// only populated for agent-type offers. The note is the explanatory
	// sentence; the body is the literal example request that buyers can
	// copy and adapt. Empty values mean "don't render the block".
	ChatCompletionsNote string
	ChatCompletionsBody string
}

func buildTypeCopy(siteURL, endpoint string, d PaymentDisplay) typeCopy {
	url := siteURL + endpoint
	switch normalizeOfferType(d.OfferType) {
	case "inference":
		return inferenceCopy(url, d)
	case "agent":
		return agentCopy(url, d)
	default:
		return httpCopy(url, d)
	}
}

// normalizeOfferType collapses the spec.type values into the three render
// branches. Empty falls back to "inference" historically (the original
// default), but the storefront defaults new offers to "http" — match that
// behavior here so unknown/unset types stay on the safest (single-shot pay)
// CTA.
func normalizeOfferType(t string) string {
	switch t {
	case "inference":
		return "inference"
	case "agent":
		return "agent"
	default:
		return "http"
	}
}

// inferenceCopy: primary CTA is `obol buy inference`, the CLI command that
// pre-pays the seller and registers the model as `paid/<model>` in the
// local LiteLLM gateway. Secondary cards still expose the agent-prompt and
// raw-JSON paths, but reframed so users understand they're buying remote
// model time, not an agent with tools/memory.
func inferenceCopy(url string, d PaymentDisplay) typeCopy {
	model := strings.TrimSpace(d.Model)
	if model == "" {
		model = "<model-id>"
	}
	name := strings.TrimSpace(d.OfferName)
	if name == "" {
		name = "remote-inference"
	}

	cmd := fmt.Sprintf(
		"obol buy inference %s \\\n  --seller %s \\\n  --model %s \\\n  --budget 1 \\\n  --no-verify-identity",
		name, url, model,
	)

	prompt := fmt.Sprintf(
		"Use the buy-x402 skill's `buy` command to pre-pay %s for the %s model. "+
			"This is remote inference — once the auths are signed, route requests "+
			"through LiteLLM as `paid/%s` and report the response.",
		url, model, model,
	)

	other := fmt.Sprintf(
		"Read https://obol.org/llms.txt to learn how Obol's x402 micropayments work. "+
			"I want to use the remote LLM at %s (model %s) as a paid OpenAI-compatible "+
			"chat-completions endpoint. Pre-sign a budget of EIP-3009/Permit2 authorisations "+
			"and POST chat-completions bodies with the X-PAYMENT header attached.",
		url, model,
	)

	return typeCopy{
		Lede: template.HTML("This is a paid inference endpoint — remote model time gated by x402 micropayments. " +
			"The cleanest way to consume it is the Obol CLI, which loads the model into your local LiteLLM " +
			"gateway as <code>paid/&lt;model&gt;</code> so your agent and tools can call it like any other OpenAI-compatible model."),
		ShowPrimary:    true,
		PrimaryTitle:   "Buy with the Obol CLI",
		PrimaryLede:    "Pre-pays the seller through your obol-agent's wallet and exposes the model as `paid/" + model + "` in your local LiteLLM gateway. Adjust `--budget`, drop `--no-verify-identity` once the seller is registered on ERC-8004.",
		PrimaryIsCode:  true,
		PrimaryPayload: cmd,
		PromptObol:     prompt,
		PromptOther:    other,
	}
}

// agentCopy: agents have skills + tools + memory, not just inference, so
// the buyer almost always wants to include an instruction with the
// purchase. The primary "Buy" card is suppressed; the Obol-Agent and
// Other-AI-Agent prompt cards drive the action, and a chat-completions
// example sits next to the raw x402 JSON in the Pay-manually card to
// make the wire shape obvious to readers walking the spec by hand.
func agentCopy(url string, d PaymentDisplay) typeCopy {
	model := strings.TrimSpace(d.Model)
	modelClause := ""
	modelLine := ""
	if model != "" {
		modelClause = fmt.Sprintf(`"model": "%s",`, model)
		modelLine = " (running " + model + ")"
	}

	body := fmt.Sprintf(`POST %s
Content-Type: application/json
X-PAYMENT: <pre-signed-EIP-3009-or-Permit2-voucher>

{
  %s
  "messages": [
    {"role": "user", "content": "<your prompt to this agent goes here>"}
  ]
}`, url, modelClause)

	prompt := fmt.Sprintf(
		"Use the buy-x402 skill's `pay` command to call the Obol Agent at %s%s. "+
			"This is an *agent*, not a raw model — it has its own skills, tools, and memory. "+
			"Include a clear instruction in the chat-completions body so the agent knows what to do.",
		url, modelLine,
	)

	other := fmt.Sprintf(
		"Read https://obol.org/llms.txt to learn how Obol's x402 micropayments work. "+
			"Help me call the Obol Agent at %s%s — it's an autonomous agent (tools + skills + memory), "+
			"not a raw LLM. POST OpenAI-style chat-completions JSON with a real prompt in `messages`, "+
			"attach a signed EIP-3009/Permit2 authorisation as `X-PAYMENT`, and report what the agent does.",
		url, modelLine,
	)

	return typeCopy{
		Lede: template.HTML(
			"This is a payment gate for an Obol Agent — it has tools, skills, and memory, it's " +
				"not just a model. You're buying one round of work from it, so plan to send it " +
				"a real prompt. The description below tells you what the agent specialises in. " +
				"The future of agentic commerce is agents paying for specialist work from " +
				`specialised agents. Learn more at <a href="https://docs.obol.org/obol-stack" ` +
				`target="_blank" rel="noreferrer">docs.obol.org/obol-stack</a>.`,
		),
		// Primary card is suppressed for agents — the actionable
		// example lives next to the raw x402 JSON instead.
		ShowPrimary:         false,
		PromptObol:          prompt,
		PromptOther:         other,
		ChatCompletionsNote: "Obol Agents accept OpenAI-style chat-completions bodies. A request like the following will get you an answer:",
		ChatCompletionsBody: body,
	}
}

// httpCopy: legacy default. Stateless single-shot pay; no model, no
// pre-payment, no LiteLLM mounting. Matches the pre-existing copy.
func httpCopy(url string, d PaymentDisplay) typeCopy {
	priceClause := ""
	if d.PriceDisplay != "" {
		priceClause = " Pay " + d.PriceDisplay + "."
	}
	netClause := ""
	if d.NetworkLabel != "" {
		netClause = " Network: " + d.NetworkLabel + "."
	}
	prompt := fmt.Sprintf("Use the buy-x402 skill's `pay` command to call %s once.%s%s", url, priceClause, netClause)

	priceWord := "the listed price"
	if d.PriceDisplay != "" {
		priceWord = d.PriceDisplay
	}
	onNet := ""
	if d.NetworkLabel != "" {
		onNet = " on " + d.NetworkLabel
	}
	other := fmt.Sprintf(
		"Read https://obol.org/llms.txt and skim https://github.com/ObolNetwork/skills "+
			"to learn how Obol Agents pay for x402 services. Then help me buy access to %s "+
			"for %s%s. Sign the EIP-3009 or Permit2 authorisation and call the endpoint "+
			"with the X-PAYMENT header.",
		url, priceWord, onNet,
	)

	return typeCopy{
		Lede:          template.HTML("This is a paid HTTP endpoint gated by x402 micropayments. Each call is a one-shot purchase — no subscription, no pre-payment, no LLM model behind it."),
		ShowPrimary:   true,
		PrimaryTitle:  "Pay with your Obol Agent",
		PrimaryLede:   "Paste this into your Obol Agent — it has the `buy-x402` skill pre-loaded and will sign one authorisation per request.",
		PrimaryIsCode: false,
		// PrimaryPayload doubles as PromptObol for http; keep both
		// populated so the template can stay symmetrical and the
		// "Pay with another AI agent" card still renders.
		PrimaryPayload: prompt,
		PromptObol:     prompt,
		PromptOther:    other,
	}
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
