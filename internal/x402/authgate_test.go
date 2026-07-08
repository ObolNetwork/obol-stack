package x402

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// authGateVerifier builds a verifier with one auth-gated route, one paid
// sibling, and an upstream that echoes the identity headers it received.
func authGateVerifier(t *testing.T, facURL string) (*Verifier, *httptest.Server, *http.Header) {
	t.Helper()
	var lastHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	v := newTestVerifier(t, facURL, []RouteRule{
		{
			Pattern:        "/services/audit/reports/*",
			Gate:           "auth",
			UpstreamURL:    upstream.URL,
			StripPrefix:    "/services/audit",
			OfferNamespace: "sec",
			OfferName:      "audit",
		},
		{
			// Price matches testPaymentHeader's amount (100 atomic units).
			Pattern:        "/services/audit/*",
			Price:          "0.0001",
			UpstreamURL:    upstream.URL,
			StripPrefix:    "/services/audit",
			OfferNamespace: "sec",
			OfferName:      "audit",
		},
	})
	return v, upstream, &lastHeaders
}

func TestVerifier_AuthGate_ChallengesAndAuthenticates(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, _, lastHeaders := authGateVerifier(t, fac.URL)

	// (1) No credential → 401 with a machine-readable SIWX challenge.
	req := httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-credential status = %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, `SIWX`) || !strings.Contains(got, "shop.example.com") {
		t.Errorf("WWW-Authenticate = %q, want SIWX challenge with domain", got)
	}
	var challenge struct {
		Error string `json:"error"`
		Auth  struct {
			Scheme    string `json:"scheme"`
			Domain    string `json:"domain"`
			SignInURL string `json:"signInUrl"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("challenge body not JSON: %v\n%s", err, w.Body.String())
	}
	if challenge.Error != "authentication_required" || challenge.Auth.Scheme != "siwx" ||
		challenge.Auth.SignInURL != "https://shop.example.com/services/audit/auth" {
		t.Errorf("challenge = %+v", challenge)
	}

	// (2) Browser (Accept: text/html) → HTML sign-in page.
	req = httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "personal_sign") {
		t.Errorf("html challenge = %d, body lacks sign-in page", w.Code)
	}

	// (3) Valid SIWX header → proxied with X-Verified-Wallet, no facilitator.
	msg, sig, wallet := signSIWX(t, "shop.example.com", "ag-1", time.Now())
	req = httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	req.Header.Set("Authorization", "SIWX "+
		base64.StdEncoding.EncodeToString([]byte(msg))+"."+
		base64.StdEncoding.EncodeToString([]byte(sig)))
	// Forged identity headers must be stripped, not forwarded.
	req.Header.Set(HeaderPaymentPayer, "0xforged")
	req.Header.Set(HeaderVerifiedWallet, "0xforged")
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated request = %d (%s), want 200", w.Code, w.Body.String())
	}
	if got := lastHeaders.Get(HeaderVerifiedWallet); got != wallet {
		t.Errorf("upstream %s = %q, want %q", HeaderVerifiedWallet, got, wallet)
	}
	if got := lastHeaders.Get(HeaderPaymentPayer); got != "" {
		t.Errorf("forged %s reached upstream: %q", HeaderPaymentPayer, got)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Error("facilitator called for an auth-gated route")
	}
}

func TestVerifier_AuthEndpoints_SignInFlow(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, _, _ := authGateVerifier(t, fac.URL)

	// GET <offer>/auth → HTML sign-in page (default), JSON on request.
	req := httptest.NewRequest(http.MethodGet, "/services/audit/auth?next=/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Sign in to audit") {
		t.Fatalf("auth page = %d, want 200 HTML sign-in (body: %.120s)", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/services/audit/auth?format=json", nil)
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	var params struct {
		Scheme    string `json:"scheme"`
		VerifyURL string `json:"verifyUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &params); err != nil || params.Scheme != "siwx" {
		t.Fatalf("auth params = %v / %s", err, w.Body.String())
	}

	// POST /auth/verify with a valid signature → session cookie + redirect.
	msg, sig, wallet := signSIWX(t, "shop.example.com", "ag-2", time.Now())
	body, _ := json.Marshal(map[string]string{
		"message":   msg,
		"signature": sig,
		"next":      "/services/audit/reports/42",
	})
	req = httptest.NewRequest(http.MethodPost, "/services/audit/auth/verify", bytes.NewReader(body))
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("verify = %d (%s), want 303", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/services/audit/reports/42" {
		t.Errorf("redirect = %q", loc)
	}
	var session string
	for _, c := range w.Result().Cookies() {
		if c.Name == SIWXSessionCookie {
			session = c.Value
			if !c.HttpOnly || !c.Secure {
				t.Error("session cookie must be HttpOnly + Secure")
			}
		}
	}
	if session == "" {
		t.Fatal("no session cookie set")
	}

	// The minted session opens the gated route.
	req = httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	req.AddCookie(&http.Cookie{Name: SIWXSessionCookie, Value: session})
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("session request = %d, want 200", w.Code)
	}
	if got, _ := v.siwx.VerifySession(session, time.Now()); got != wallet {
		t.Errorf("session wallet = %q, want %q", got, wallet)
	}

	// Open-redirect guard: absolute/scheme-relative next is dropped.
	body, _ = json.Marshal(map[string]string{"message": msg, "signature": sig, "next": "https://evil.example.com/"})
	req = httptest.NewRequest(http.MethodPost, "/services/audit/auth/verify", bytes.NewReader(body))
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code == http.StatusSeeOther {
		t.Errorf("external redirect followed: %s", w.Header().Get("Location"))
	}

	// Offers WITHOUT an auth route get no sign-in endpoints: the path
	// falls through to route matching (here: the paid catch-all's 402).
	req = httptest.NewRequest(http.MethodGet, "/services/other/auth", nil)
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("auth endpoint for unknown offer = %d, want 404", w.Code)
	}
}

// TestVerifier_HandleProxy_PropagatesPaymentPayer pins the payer identity
// fact: the facilitator-recovered payer of a verified payment reaches the
// upstream as X-Payment-Payer, and forged values never do.
func TestVerifier_HandleProxy_PropagatesPaymentPayer(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, _, lastHeaders := authGateVerifier(t, fac.URL)

	req := httptest.NewRequest(http.MethodPost, "/services/audit/run", strings.NewReader(`{}`))
	req.Host = "shop.example.com"
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	req.Header.Set(HeaderPaymentPayer, "0xforged")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("paid request = %d (%s), want 200", w.Code, w.Body.String())
	}
	got := lastHeaders.Get(HeaderPaymentPayer)
	if got == "0xforged" {
		t.Fatal("forged payer header reached upstream")
	}
	if got == "" {
		t.Fatal("verified payer not propagated to upstream")
	}
}

// TestVerifier_ErrorPages_ContentNegotiated pins the human/agent split on
// proxy errors: browsers get the branded HTML error page, agents keep the
// plain-text bodies existing tooling matches on.
func TestVerifier_ErrorPages_ContentNegotiated(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, _, _ := authGateVerifier(t, fac.URL)

	// Browser 404 → branded page with the report hint.
	req := httptest.NewRequest(http.MethodGet, "/services/nope", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "/openapi.json") {
		t.Errorf("browser 404 = %d, want branded page with contact pointer (body: %.120s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("browser 404 content-type = %q", ct)
	}

	// Agent 404 → plain text, no HTML.
	req = httptest.NewRequest(http.MethodGet, "/services/nope", nil)
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "<html") {
		t.Errorf("agent 404 = %d, body must stay plain (body: %.80s)", w.Code, w.Body.String())
	}
}

// TestVerifier_AuthGate_HostnameOffer_PublicURLs pins the dedicated-origin
// path-world: when a request arrives via the offer's own hostname (Traefik
// rewrote /reports/42 → /services/audit/reports/42 and set
// X-Forwarded-Host), every public URL the verifier emits — sign-in URL,
// verify URL, post-auth redirect — must be rooted at "/", never leaking
// the internal /services/<name> prefix.
func TestVerifier_AuthGate_HostnameOffer_PublicURLs(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	v := newTestVerifier(t, fac.URL, []RouteRule{
		{
			Pattern:     "/services/audit/reports/*",
			Gate:        "auth",
			Hostname:    "audit.shop.example",
			UpstreamURL: upstream.URL,
			StripPrefix: "/services/audit",
			OfferName:   "audit",
		},
	})

	// Challenge JSON: sign-in/verify URLs rooted at the hostname origin.
	req := httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Header.Set("X-Forwarded-Host", "audit.shop.example")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	var challenge struct {
		Auth struct {
			SignInURL string `json:"signInUrl"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if challenge.Auth.SignInURL != "https://audit.shop.example/auth" {
		t.Errorf("signInUrl = %q, want hostname-rooted /auth", challenge.Auth.SignInURL)
	}

	// Browser challenge: verify URL and redirect target use public paths.
	req = httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Header.Set("X-Forwarded-Host", "audit.shop.example")
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	body := w.Body.String()
	if strings.Contains(body, "/services/audit") {
		t.Errorf("hostname sign-in page leaks internal prefix:\n%.300s", body)
	}
	if !strings.Contains(body, `"/auth/verify"`) && !strings.Contains(body, "/auth/verify") {
		t.Errorf("sign-in page missing public verify URL:\n%.300s", body)
	}

	// Shared-origin access to the SAME rule keeps the prefixed URLs.
	req = httptest.NewRequest(http.MethodGet, "/services/audit/reports/42", nil)
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("shared-origin challenge: %v", err)
	}
	if challenge.Auth.SignInURL != "https://shop.example.com/services/audit/auth" {
		t.Errorf("shared-origin signInUrl = %q, want prefixed path", challenge.Auth.SignInURL)
	}
}
