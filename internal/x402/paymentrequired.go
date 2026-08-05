package x402

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strings"

	x402types "github.com/x402-foundation/x402/go/v2/types"

	"github.com/ObolNetwork/obol-stack/internal/buyprompts"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

// displayTokenRe is the allowed charset for ServiceOffer-sourced strings
// (offer name, model id) that get interpolated into copy-pasteable CLI
// commands and prompts on the 402 page. It permits the model-id / k8s-name
// vocabulary — alphanumerics plus `. _ : / -` — and nothing else, so shell
// metacharacters can never reach a command a reader might paste.
var displayTokenRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

// sanitizeDisplayToken guards a CR-sourced string before it lands in a
// copy-pasteable command on the public 402 page. A ServiceOffer is
// operator-authored, but the page is served over the public tunnel, so a
// hostile or fat-fingered spec.model.name / metadata.name must not smuggle
// shell metacharacters into the rendered command. Anything that isn't a clean
// model-id/k8s-name token (including empty/whitespace) collapses to the
// caller's placeholder.
func sanitizeDisplayToken(s, placeholder string) string {
	s = strings.TrimSpace(s)
	if s == "" || !displayTokenRe.MatchString(s) {
		return placeholder
	}
	return s
}

// defaultAgentTaskExample is the concrete sample task baked into the
// agent-type copy so a buyer can paste the pay-agent command (or the
// chat-completions body) and have it run as-is, then edit the message.
// Aliased from buyprompts (the single authoring point for buyer copy) so the
// public storefront, /api/services.json, and the 402 page hand out the same
// example.
const defaultAgentTaskExample = buyprompts.DefaultTaskExample

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

	// BrandingPatch is the originating rule's per-origin identity
	// override (RouteRule.Branding). Nil renders storefront-wide
	// branding.
	BrandingPatch *schemas.StorefrontProfile
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
//
// Scheme resolution defaults to https and only downgrades to http for the
// hosts the stack serves locally over plain HTTP (obol.stack, loopback,
// *.localhost/*.local). This matters because the public 402 page is served
// over the Cloudflare tunnel, which terminates TLS at the edge and forwards
// plaintext to cloudflared -> Traefik -> verifier: the X-Forwarded-Proto we
// observe on the ForwardAuth hop is "http", so keying off it alone rendered
// http:// tunnel links (broken/mixed-content in the copy-paste prompts and
// OG/asset URLs). An explicit https signal (direct TLS or X-Forwarded-Proto:
// https) still forces https for any host.
func resolveSiteURL(r *http.Request) string {
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" && isLocalHost(host) {
		scheme = "http"
	}
	return scheme + "://" + host
}

// isLocalHost reports whether host (optionally with :port) is one the stack
// serves locally over plain HTTP: obol.stack, loopback, or a developer
// *.localhost/*.local name. Any other host is treated as a public tunnel
// hostname (https).
func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(strings.TrimSuffix(host, "."), "[]"))
	switch host {
	case "obol.stack", "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

// sendPaymentRequiredHTML writes a 402 status with an HTML body that includes
// full OG metadata, a service-info card, three "ways to pay" prompt cards
// (Obol Agent, other AI agent, raw JSON), and copy buttons.
func sendPaymentRequiredHTML(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any, display PaymentDisplay) {
	// Catalog discovery link rides on the HTML branch too (and survives the
	// JSON fallbacks below — Header().Set is idempotent).
	setCatalogLinkHeader(w)
	jsonBody := paymentRequiredBody(r, requirements, extensions)
	indented, err := json.MarshalIndent(jsonBody, "", "  ")
	if err != nil {
		// Should not happen — fall back to JSON path.
		sendPaymentRequiredJSON(w, r, requirements, extensions)
		return
	}
	// The canonical PAYMENT-REQUIRED header rides along even when the body
	// is HTML for a browser — protocol-aware clients that sent Accept:
	// text/html still get the machine-readable challenge.
	if compact, err := json.Marshal(jsonBody); err == nil {
		setPaymentRequiredHeader(w, compact)
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
	branding := resolveBranding(siteURL, display.BrandingPatch)

	data := struct {
		Title               string
		Description         string
		PageURL             string
		StorefrontURL       string
		Branding            Branding
		Endpoint            string
		NetworkLabel        string
		PriceDisplay        string
		PayToDisplay        string
		PayToFull           string
		ExplorerURL         string
		OfferName           string
		OfferDescription    template.HTML
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
		Title:               "Payment required — " + branding.SiteName,
		Description:         buildMetaDescription(display),
		PageURL:             pageURL,
		StorefrontURL:       siteURL,
		Branding:            branding,
		Endpoint:            endpoint,
		NetworkLabel:        networkLabel,
		PriceDisplay:        priceDisplay,
		PayToDisplay:        payToDisplay,
		PayToFull:           payToFull,
		ExplorerURL:         display.ExplorerURL,
		OfferName:           display.OfferName,
		OfferDescription:    storefront.RenderRichText(display.OfferDescription),
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
		return inferenceCopy(url, siteURL, d)
	case "agent":
		return agentCopy(url, siteURL, d)
	default:
		return httpCopy(url, siteURL, d)
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
// pre-authorizes the seller and registers the model as `paid/<model>` in the
// local LiteLLM gateway. Secondary cards still expose the agent-prompt and
// raw-JSON paths, but reframed so users understand they're buying remote
// model time, not an agent with tools/memory.
func inferenceCopy(url, siteURL string, d PaymentDisplay) typeCopy {
	block := buyprompts.Build(buyprompts.Input{
		Type:    "inference",
		URL:     url,
		SiteURL: siteURL,
		Model:   sanitizeDisplayToken(d.Model, ""),
	})

	return typeCopy{
		Lede: template.HTML(
			"This is a paid inference provider. Your Obol agent runs locally; the model itself runs " +
				"on the remote operator's hardware and is gated by x402 micropayments. The CLI below " +
				"pre-authorizes the provider through your agent's wallet and registers the model as " +
				"<code>paid/&lt;model&gt;</code> in your local LiteLLM gateway, so every agent in your stack " +
				"can call it like any other OpenAI-compatible model."),
		ShowPrimary:   true,
		PrimaryTitle:  "Use this service for your Obol Agent's model",
		PrimaryLede:   "Run this from your obol-stack host. The CLI walks `/api/services.json`, prompts for auto-refill + a request count, and pre-signs the authorizations from your master agent's wallet. Pass `--yes --count <N>` for non-interactive runs.",
		PrimaryIsCode: true,
		// Positional seller URL, no required --model/--budget. Identity check
		// is opt-in via --expected-agent-id, so the copy stays clean.
		PrimaryPayload:      block.Prompts[buyprompts.PromptCLI],
		PromptObol:          block.Prompts[buyprompts.PromptObolAgent],
		PromptOther:         block.Prompts[buyprompts.PromptGenericLLM],
		ChatCompletionsNote: "Direct HTTP buyers use OpenAI-style chat-completions. A minimal paid request looks like:",
		ChatCompletionsBody: block.Example,
	}
}

// agentCopy: agents have skills + tools + memory, not just inference, so
// the buyer almost always wants to include an instruction with the
// purchase. The primary "Buy" card is suppressed; the Obol-Agent and
// Other-AI-Agent prompt cards drive the action, and a chat-completions
// example sits next to the raw x402 JSON in the Pay-manually card to
// make the wire shape obvious to readers walking the spec by hand.
func agentCopy(url, siteURL string, d PaymentDisplay) typeCopy {
	// pay-agent requires a --model value, but an agent runs its own pinned
	// model server-side and ignores the field, so we don't editorialize about
	// which model the seller uses — buyprompts fills the required flag (the
	// seller's model when known, a placeholder otherwise) and hands the buyer
	// a command that runs as-is with a concrete example task they can edit.
	block := buyprompts.Build(buyprompts.Input{
		Type:    "agent",
		URL:     url,
		SiteURL: siteURL,
		Model:   sanitizeDisplayToken(d.Model, ""),
	})

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
		PromptObol:          block.Prompts[buyprompts.PromptObolAgent],
		PromptOther:         block.Prompts[buyprompts.PromptGenericLLM],
		ChatCompletionsNote: "Obol Agents accept OpenAI-style chat-completions bodies. A request like the following will get you an answer:",
		ChatCompletionsBody: block.Example,
	}
}

// httpCopy: legacy default. Stateless single-shot pay; no model, no
// pre-payment, no LiteLLM mounting. Matches the pre-existing copy.
func httpCopy(url, siteURL string, d PaymentDisplay) typeCopy {
	block := buyprompts.Build(buyprompts.Input{
		Type:         "http",
		URL:          url,
		SiteURL:      siteURL,
		PriceDisplay: d.PriceDisplay,
		NetworkLabel: d.NetworkLabel,
	})
	prompt := block.Prompts[buyprompts.PromptObolAgent]

	return typeCopy{
		Lede:          template.HTML("This is a paid HTTP endpoint gated by x402 micropayments. Each call is a one-shot purchase — no subscription, no pre-authorization, no LLM model behind it."),
		ShowPrimary:   true,
		PrimaryTitle:  "Pay with your Obol Agent",
		PrimaryLede:   "Paste this into your Obol Agent — it has the `buy-x402` skill pre-loaded and will sign one authorization per request.",
		PrimaryIsCode: false,
		// PrimaryPayload doubles as PromptObol for http; keep both
		// populated so the template can stay symmetrical and the
		// "Pay with another AI agent" card still renders.
		PrimaryPayload: prompt,
		PromptObol:     prompt,
		PromptOther:    block.Prompts[buyprompts.PromptGenericLLM],
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
