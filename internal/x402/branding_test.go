package x402

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

func TestResolveBranding_Defaults(t *testing.T) {
	SetStorefrontProfile(nil)
	b := resolveBranding("https://seller.example.com", nil)

	if b.SiteName != "Obol Stack" {
		t.Fatalf("SiteName = %q", b.SiteName)
	}
	// Default theme is light → the dark square mark + name, because the
	// default wordmark is light-on-dark and invisible on light pages.
	if b.LogoURL != "https://seller.example.com"+storefront.DefaultMarkPath {
		t.Fatalf("LogoURL = %q", b.LogoURL)
	}
	if !b.ShowName {
		t.Fatal("light default branding must show the site name")
	}
	if b.FaviconURL != "https://seller.example.com/favicon.png" {
		t.Fatalf("FaviconURL = %q", b.FaviconURL)
	}
	if b.OGImageURL != "https://seller.example.com/og-payment-required.png" {
		t.Fatalf("OGImageURL = %q", b.OGImageURL)
	}
	if b.ThemeColor != "#ffffff" {
		t.Fatalf("ThemeColor = %q, want light bg", b.ThemeColor)
	}
	if !strings.Contains(string(b.ThemeCSS), "--bg01:#ffffff;") {
		t.Fatalf("ThemeCSS missing light tokens: %q", b.ThemeCSS)
	}
}

func TestResolveBranding_DarkDefaultKeepsWordmark(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{Theme: storefront.ThemeDark})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	b := resolveBranding("https://seller.example.com", nil)
	if b.LogoURL != "https://seller.example.com"+storefront.DefaultLogoPath {
		t.Fatalf("LogoURL = %q, want default wordmark", b.LogoURL)
	}
	if b.ShowName {
		t.Fatal("wordmark already spells the name; ShowName must be false")
	}
	if b.ThemeColor != "#091011" {
		t.Fatalf("ThemeColor = %q", b.ThemeColor)
	}
}

func TestResolveBranding_CustomProfile(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{
		DisplayName: "Acme Labs",
		LogoURL:     "https://cdn.example.com/acme.png",
		Theme:       storefront.ThemeObol,
		AccentColor: "#a1b2c3",
		FaviconURL:  "/fav.svg",
		OGImageURL:  "https://cdn.example.com/og.png",
	})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	b := resolveBranding("https://seller.example.com", nil)
	if b.SiteName != "Acme Labs" || !b.ShowName {
		t.Fatalf("custom profile: SiteName=%q ShowName=%v", b.SiteName, b.ShowName)
	}
	if b.LogoURL != "https://cdn.example.com/acme.png" {
		t.Fatalf("LogoURL = %q", b.LogoURL)
	}
	if b.FaviconURL != "https://seller.example.com/fav.svg" {
		t.Fatalf("relative favicon not absolutized: %q", b.FaviconURL)
	}
	if b.OGImageURL != "https://cdn.example.com/og.png" {
		t.Fatalf("OGImageURL = %q", b.OGImageURL)
	}
	if !strings.Contains(string(b.ThemeCSS), "--green:#a1b2c3;") {
		t.Fatalf("accent not applied: %q", b.ThemeCSS)
	}
}

// TestPaymentRequiredHTML_Branded renders the full 402 HTML page with a
// custom profile and asserts the seller identity + theme reach the markup.
func TestPaymentRequiredHTML_Branded(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{
		DisplayName: "Acme Labs",
		LogoURL:     "https://cdn.example.com/acme.png",
		Theme:       storefront.ThemeDark,
	})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	send := NewHTMLAwarePaymentRequired(PaymentDisplay{
		Endpoint:     "/services/acme-audit",
		Network:      "base",
		NetworkLabel: "Base",
		PriceDisplay: "0.001 USDC per request",
		PayToFull:    "0x1111111111111111111111111111111111111111",
	})

	r := httptest.NewRequest("GET", "https://seller.example.com/services/acme-audit", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	send(w, r, []x402types.PaymentRequirements{{
		Scheme: "exact", PayTo: "0x1111111111111111111111111111111111111111", Amount: "1000",
	}}, nil)

	if w.Code != 402 {
		t.Fatalf("status = %d", w.Code)
	}
	html := w.Body.String()
	for _, want := range []string{
		"<title>Payment required — Acme Labs</title>",
		`og:site_name" content="Acme Labs"`,
		`src="https://cdn.example.com/acme.png"`,
		"<span>Acme Labs</span>",
		"--bg01:#091011;",
		`content="#091011"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("branded 402 HTML missing %q", want)
		}
	}
}

// TestPaymentRequiredHTML_PerOriginBranding asserts a hostname-bound offer's
// spec.branding patch (threaded through RouteRule → PaymentDisplay) overrides
// the storefront profile field-wise on the 402 page, with unset fields
// inheriting.
func TestPaymentRequiredHTML_PerOriginBranding(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{
		DisplayName: "Acme Labs",
		Theme:       storefront.ThemeDark,
	})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	send := NewHTMLAwarePaymentRequired(PaymentDisplay{
		Endpoint: "/services/audit",
		BrandingPatch: &schemas.StorefrontProfile{
			DisplayName: "AuditCo",
			Theme:       storefront.ThemeObol,
			AccentColor: "#a1b2c3",
			LogoURL:     "https://cdn.example.com/auditco.png",
		},
	})

	r := httptest.NewRequest("GET", "https://audit.acme.example/services/audit", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	send(w, r, []x402types.PaymentRequirements{{
		Scheme: "exact", PayTo: "0x1111111111111111111111111111111111111111", Amount: "1000",
	}}, nil)

	html := w.Body.String()
	for _, want := range []string{
		"<title>Payment required — AuditCo</title>", // patch wins over profile
		"--bg01:#05201a;",  // obol preset from the patch
		"--green:#a1b2c3;", // accent from the patch
		"<span>AuditCo</span>",
		`src="https://cdn.example.com/auditco.png"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("per-origin branded 402 missing %q", want)
		}
	}
	if strings.Contains(html, "Acme Labs") {
		t.Error("storefront displayName leaked onto the branded origin's 402")
	}
}

// TestPaymentRequiredHTML_LayoutContract pins the checkout layout's stable
// hooks: the data-obol attributes custom stylesheets target, the tabbed
// how-to-pay markup, the reserved wallet-checkout mount, and the operator
// custom-CSS injection point. Renaming/removing these is a breaking change
// for operator stylesheets — treat this test as the contract.
func TestPaymentRequiredHTML_LayoutContract(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{
		CustomCSS: `[data-obol="price"] { font-size: 44px; }`,
	})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	send := NewHTMLAwarePaymentRequired(PaymentDisplay{
		Endpoint:     "/services/acme-audit",
		OfferName:    "acme-audit",
		OfferType:    "agent",
		NetworkLabel: "Base",
		PriceDisplay: "0.5 USDC per request",
		PayToFull:    "0x1111111111111111111111111111111111111111",
		AgentSkills:  []string{"audit"},
	})

	r := httptest.NewRequest("GET", "https://seller.example.com/services/acme-audit", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	send(w, r, []x402types.PaymentRequirements{{
		Scheme: "exact", PayTo: "0x1111111111111111111111111111111111111111", Amount: "500000",
	}}, nil)

	html := w.Body.String()
	for _, want := range []string{
		`data-obol="page-402"`, `data-obol="header"`, `data-obol="brand"`,
		`data-obol="status-pill"`, `data-obol="title"`, `data-obol="lede"`,
		`data-obol="summary"`, `data-obol="offer-name"`, `data-obol="skills"`,
		`data-obol="price"`, `data-obol="payment-details"`, `data-obol="endpoint"`,
		`data-obol="network"`, `data-obol="pay-to"`, `data-obol="checkout"`,
		`data-obol="pay"`, `data-obol="pay-tabs"`, `data-obol="pay-obol"`,
		`data-obol="pay-other"`, `data-obol="pay-manual"`, `data-obol="footer"`,
		`data-obol="powered-by"`,
		// Tabs + stacked no-JS fallback labels.
		`role="tablist"`, `data-tab="obol"`, `data-tab="manual"`, `class="panel-label"`,
		// Operator stylesheet injected in its own style element.
		`<style data-obol="custom-css">[data-obol="price"] { font-size: 44px; }</style>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("402 layout missing %q", want)
		}
	}
	if strings.Contains(html, "</style><script>") {
		t.Fatal("custom css escaped its style element")
	}
}

// TestPaymentRequiredHTML_CustomCSSBreakoutDropped asserts a hostile stored
// stylesheet is dropped at render (the SafeCustomCSS guard), not inlined.
func TestPaymentRequiredHTML_CustomCSSBreakoutDropped(t *testing.T) {
	SetStorefrontProfile(&schemas.StorefrontProfile{
		CustomCSS: `a{}</style><script>alert(1)</script>`,
	})
	t.Cleanup(func() { SetStorefrontProfile(nil) })

	send := NewHTMLAwarePaymentRequired(PaymentDisplay{Endpoint: "/services/x"})
	r := httptest.NewRequest("GET", "https://seller.example.com/services/x", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	send(w, r, []x402types.PaymentRequirements{{
		Scheme: "exact", PayTo: "0x1111111111111111111111111111111111111111", Amount: "1000",
	}}, nil)

	html := w.Body.String()
	if strings.Contains(html, `data-obol="custom-css"`) || strings.Contains(html, "alert(1)</script>") {
		t.Fatal("hostile custom css reached the page")
	}
}

// TestPaymentRequiredHTML_MarkdownDescription asserts the offer description
// renders as sanitized rich HTML (single richtext path), not escaped text.
func TestPaymentRequiredHTML_MarkdownDescription(t *testing.T) {
	send := NewHTMLAwarePaymentRequired(PaymentDisplay{
		Endpoint:         "/services/acme-audit",
		OfferDescription: "We sell **audits**.\n\n- fast\n- [docs](https://docs.acme.io)\n\n<script>alert(1)</script>",
	})

	r := httptest.NewRequest("GET", "https://seller.example.com/services/acme-audit", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	send(w, r, []x402types.PaymentRequirements{{
		Scheme: "exact", PayTo: "0x1111111111111111111111111111111111111111", Amount: "1000",
	}}, nil)

	html := w.Body.String()
	for _, want := range []string{
		"<strong>audits</strong>",
		"<li>fast</li>",
		`href="https://docs.acme.io"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("402 HTML missing rich description fragment %q", want)
		}
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("raw script from offer description survived into 402 HTML")
	}
}
