package serviceoffercontroller

import (
	_ "embed"
	"html/template"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
)

// The agent chat widget: a self-contained browser chat client served free on
// every agent-type offer's dedicated origin at /chat (and embedded on the
// offer's landing page). The page discovers pricing at runtime from its own
// origin — price, model, payment network and asset come from the 402
// challenge on POST /v1/chat/completions — while identity and theme are
// rendered per offer: the template receives the offer's display name and the
// same resolved storefront theme tokens as its landing page, so default and
// branded designs flow through identically.
//
// Payment is fully client-side: the visitor connects an injected wallet,
// signs one fixed message ("sign in with Ethereum") whose keccak256 becomes
// a deterministic local session key, funds that session address with a small
// USDC transfer, and every chat turn is then paid silently via x402
// (EIP-3009 transferWithAuthorization signed by the session key — gasless
// for the payer). The session key never leaves the page and is re-derived by
// re-signing the same message, so nothing is persisted.
//
//go:embed assets/chat.html
var chatWidgetTmplSrc string

var chatWidgetTmpl = template.Must(template.New("chat_widget").Parse(chatWidgetTmplSrc))

// chatWidgetVendorJS is the widget's only dependency: viem 2.21.25 +
// @x402/fetch 2.18.0 + @x402/evm 2.18.0 bundled into one ESM file so the
// page loads with zero external requests (no CDN, works on air-gapped
// stacks). Served once at the catalog httpd root — per-offer /chat pages
// import it behind a content-hash ?v= cache-buster. Rebuild: see
// assets/README.md.
//
//go:embed assets/chat-vendor.js
var chatWidgetVendorJS string

// buildOfferChatHTML renders the offer's /chat page with the same title and
// resolved theme as its landing page.
func buildOfferChatHTML(offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) string {
	title := strings.TrimSpace(offer.Spec.Registration.Name)
	if title == "" {
		title = offer.Name
	}
	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)
	var out strings.Builder
	err := chatWidgetTmpl.Execute(&out, map[string]any{
		"Title":     title,
		"OfferName": offer.Name,
		"ThemeCSS":  template.CSS(theme.CSSVars()),
	})
	if err != nil {
		return "<!doctype html><title>" + template.HTMLEscapeString(title) + "</title>"
	}
	return out.String()
}
