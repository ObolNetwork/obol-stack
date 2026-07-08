package x402

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	signinwithx "github.com/x402-foundation/x402/go/v2/extensions/signinwithx"
)

// Identity propagation headers. Both are set by the verifier ONLY — any
// client-supplied value is stripped before the request reaches an upstream,
// so upstreams may treat them as authenticated facts.
const (
	// HeaderVerifiedWallet carries the SIWX-authenticated wallet (lowercase
	// 0x address) on gate:auth routes.
	HeaderVerifiedWallet = "X-Verified-Wallet"
	// HeaderPaymentPayer carries the payer address of the verified x402
	// payment on paid routes (the facilitator's recovered signer).
	HeaderPaymentPayer = "X-Payment-Payer"
)

//go:embed templates/siwx_challenge.html
var siwxChallengeHTMLSrc string

var siwxChallengeTmpl = template.Must(
	template.New("siwx_challenge").Parse(siwxChallengeHTMLSrc),
)

//go:embed templates/error_page.html
var errorPageHTMLSrc string

var errorPageTmpl = template.Must(
	template.New("error_page").Parse(errorPageHTMLSrc),
)

// writeErrorResponse emits an error with content negotiation: browsers
// (Accept: text/html) get a branded page pointing at the storefront root
// and the operator contact in /openapi.json; everything else keeps the
// plain-text body agents and tests already match on.
func writeErrorResponse(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Error(w, detail, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := errorPageTmpl.Execute(w, map[string]any{
		"Status": status,
		"Title":  title,
		"Detail": detail,
	}); err != nil {
		log.Printf("x402-verifier: render error page: %v", err)
	}
}

// requestHost returns the public host authority a SIWX message must bind
// to: the Traefik-forwarded host when present (ForwardAuth mode and
// controller-rendered hostname routes), else the request Host.
func requestHost(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		return h
	}
	return r.Host
}

// publicPrefix returns the path prefix the CLIENT sees for this rule's
// offer. On the offer's dedicated hostname the public path-world is rooted
// at "/" (Traefik rewrote it into /services/<name> before we matched), so
// public URLs must not leak the internal prefix; on the shared origin the
// prefix is the offer path itself.
func publicPrefix(rule *RouteRule, host string) string {
	if rule.Hostname != "" && strings.EqualFold(host, rule.Hostname) {
		return ""
	}
	return strings.TrimSuffix(rule.StripPrefix, "/")
}

// Broker contract headers (mirrors internal/jobbroker — see the comment on
// buildUpstreamProxy for why they aren't imported).
const (
	headerBrokerUpstreamURL  = "X-Obol-Upstream-Url"
	headerBrokerOffer        = "X-Obol-Offer"
	headerBrokerPayTo        = "X-Obol-Pay-To"
	headerBrokerJobTTL       = "X-Obol-Job-Ttl"
	headerBrokerVisibility   = "X-Obol-Result-Visibility"
	headerBrokerPublicPrefix = "X-Obol-Public-Prefix"
	headerBrokerUpstreamAuth = "X-Obol-Upstream-Auth"
	// headerBrokerSig carries the verifier's HMAC over the contract fields,
	// so the broker can reject forged submits even from a NetworkPolicy-
	// allowed pod (F1 defense in depth). Verified in internal/jobbroker.
	headerBrokerSig = "X-Obol-Broker-Sig"
)

// stripIdentityHeaders removes client-supplied identity and broker-contract
// headers. Called on every proxied request before the verifier decides
// whether to set its own — the downstream components trust these headers
// BECAUSE this ran.
func stripIdentityHeaders(h http.Header) {
	h.Del(HeaderVerifiedWallet)
	h.Del(HeaderPaymentPayer)
	h.Del(headerBrokerUpstreamURL)
	h.Del(headerBrokerOffer)
	h.Del(headerBrokerPayTo)
	h.Del(headerBrokerJobTTL)
	h.Del(headerBrokerVisibility)
	h.Del(headerBrokerPublicPrefix)
	h.Del(headerBrokerUpstreamAuth)
	h.Del(headerBrokerSig)
}

// brokerSignature is the HMAC the verifier sets and the broker checks over
// the security-critical contract fields (replay URL, offer, injected upstream
// credential). Byte-identical to internal/jobbroker.brokerSignature — the two
// packages deliberately don't share code (the broker pulls in SQLite).
func brokerSignature(secret, upstreamURL, offer, upstreamAuth string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(upstreamURL + "\n" + offer + "\n" + upstreamAuth))
	return hex.EncodeToString(mac.Sum(nil))
}

// authPageSuffix/authVerifySuffix are the verifier-served sign-in endpoints
// under each offer prefix (e.g. /services/audit/auth). They are handled by
// the verifier itself — never proxied — and only exist for offers whose
// route table declares at least one gate:auth route.
const (
	authPageSuffix   = "/auth"
	authVerifySuffix = "/auth/verify"
)

// writeSIWXChallenge emits the 401 for an unauthenticated request to a
// gate:auth route. Machines (default) get JSON describing how to construct
// the credential plus a WWW-Authenticate header; browsers (Accept:
// text/html) get the sign-in page with a post-auth redirect back to the
// original URL.
func (v *Verifier) writeSIWXChallenge(w http.ResponseWriter, r *http.Request, rule *RouteRule, reason error) {
	host := requestHost(r)
	windowSecs := int(v.siwx.Window().Seconds())
	authPath := publicPrefix(rule, host) + authPageSuffix
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`SIWX realm=%q, domain=%q, window="%d"`, rule.OfferName, host, windowSecs))

	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		// The redirect target must be the PUBLIC path — on a dedicated
		// hostname r.URL.Path is the rewritten internal path, which does
		// not exist in the browser's path-world.
		next := publicPrefix(rule, host) + stripRoutePrefix(rule.StripPrefix, r.URL.Path)
		if r.URL.RawQuery != "" {
			next += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		v.renderSIWXPage(w, r, rule, next, reason)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	resourceURI := "https://" + host + publicPrefix(rule, host) + stripRoutePrefix(rule.StripPrefix, r.URL.Path)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  "authentication_required",
		"detail": reason.Error(),
		"auth": map[string]any{
			"scheme":        "siwx",
			"version":       "eip4361",
			"domain":        host,
			"windowSeconds": windowSecs,
			"signInUrl":     "https://" + host + authPath,
			"verifyUrl":     "https://" + host + authPath + "/verify",
			"hint": "Sign an EIP-4361 message (domain must equal the host above, Version 1, fresh Nonce, " +
				"Issued At within the window) with EIP-191 personal_sign, then send " +
				"`Authorization: SIWX <base64 message>.<base64 signature>`. Or POST {message, signature} " +
				"to verifyUrl to mint a reusable session token (also set as the obol_siwx cookie).",
		},
		// x402 sign-in-with-x extension block, so a cold stock client can
		// construct the credential without prior knowledge of this server.
		// The nonce is freshly minted per challenge; obol accepts client
		// nonces, so advertising one is additive, not a tightening.
		"extensions": siwxChallengeExtension(host, resourceURI, "Sign in to "+rule.OfferName, v.siwx.Window()),
	})
}

// obolSIWXChains are the CAIP-2 chains obol advertises for SIWx. Signing is
// domain-bound (EIP-191), so chainId is a client hint only — obol verifies
// the same way regardless. eip191/EOA only.
var obolSIWXChains = []string{"eip155:8453", "eip155:84532"}

// siwxChallengeExtension builds the extensions["sign-in-with-x"] block for a
// 401/402 challenge per docs.x402.org/extensions/sign-in-with-x: message
// metadata (with a fresh server nonce), the supported chains, and the
// canonical schema from the x402 SDK.
func siwxChallengeExtension(domain, resourceURI, statement string, window time.Duration) map[string]any {
	now := time.Now().UTC()
	supported := make([]map[string]any, 0, len(obolSIWXChains))
	for _, c := range obolSIWXChains {
		supported = append(supported, map[string]any{"chainId": c, "type": "eip191"})
	}
	return map[string]any{
		"sign-in-with-x": map[string]any{
			"info": map[string]any{
				"domain":         domain,
				"uri":            resourceURI,
				"version":        "1",
				"nonce":          siwxNonce(),
				"issuedAt":       now.Format(time.RFC3339),
				"expirationTime": now.Add(window).Format(time.RFC3339),
				"statement":      statement,
				"resources":      []string{resourceURI},
			},
			"supportedChains": supported,
			"schema":          signinwithx.Schema(),
		},
	}
}

// siwxNonce mints a fresh alphanumeric nonce (siwe requires >=8 alphanumeric
// characters). 16 hex chars from crypto/rand; falls back to a timestamp-derived
// value only if the RNG is unavailable, which never happens in practice.
func siwxNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// handleAuthEndpoints intercepts the verifier-served sign-in endpoints
// before route matching. Returns true when the request was handled.
//
//	GET  <offer>/auth          → HTML sign-in page (or JSON challenge params)
//	POST <offer>/auth/verify   → verify {message, signature}; mint session
func (v *Verifier) handleAuthEndpoints(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	var prefix string
	switch {
	case strings.HasSuffix(path, authVerifySuffix):
		prefix = strings.TrimSuffix(path, authVerifySuffix)
	case strings.HasSuffix(path, authPageSuffix):
		prefix = strings.TrimSuffix(path, authPageSuffix)
	default:
		return false
	}

	// Only offers that actually declare an auth route get sign-in
	// endpoints — everything else falls through to normal route matching
	// (and its fail-closed handling).
	rule := v.authRuleForPrefix(prefix)
	if rule == nil {
		return false
	}

	if strings.HasSuffix(path, authVerifySuffix) {
		v.handleAuthVerify(w, r, rule)
		return true
	}

	// The sign-in page is a human surface: HTML unless JSON is asked for
	// explicitly (Accept or ?format=json).
	wantsJSON := r.URL.Query().Get("format") == "json" ||
		strings.Contains(r.Header.Get("Accept"), "json")
	if !wantsJSON {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		v.renderSIWXPage(w, r, rule, r.URL.Query().Get("next"), nil)
		return true
	}
	host := requestHost(r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"scheme":        "siwx",
		"version":       "eip4361",
		"domain":        host,
		"windowSeconds": int(v.siwx.Window().Seconds()),
		"verifyUrl":     "https://" + host + publicPrefix(rule, host) + authVerifySuffix,
	})
	return true
}

// handleAuthVerify verifies a POSTed {message, signature} pair, mints a
// session token, sets the browser cookie, and either redirects (?next=) or
// returns the token as JSON for API clients.
func (v *Verifier) handleAuthVerify(w http.ResponseWriter, r *http.Request, rule *RouteRule) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST {message, signature}", http.StatusMethodNotAllowed)
		return
	}
	// Login-CSRF defense. Minting a session cookie from a cross-origin POST
	// would let an attacker plant *their own* wallet's session in a victim's
	// browser (session fixation). Two guards, either sufficient:
	//   1. Require Content-Type: application/json. A cross-origin HTML form
	//      can only send text/plain|form-encoded as a CORS "simple" request;
	//      anything else triggers a preflight this endpoint never satisfies.
	//   2. Reject a browser-declared cross-site fetch. Non-browser API
	//      clients omit Sec-Fetch-Site, so this only ever blocks browsers.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		http.Error(w, "cross-site sign-in is not allowed", http.StatusForbidden)
		return
	}
	var body struct {
		Message   string `json:"message"`
		Signature string `json:"signature"`
		Next      string `json:"next"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: POST {message, signature}", http.StatusBadRequest)
		return
	}

	wallet, err := v.siwx.VerifyMessage(body.Message, body.Signature, requestHost(r), time.Now())
	if err != nil {
		log.Printf("x402-verifier: siwx verify failed for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "siwx_verification_failed", "detail": err.Error()})
		return
	}

	token := v.siwx.MintSession(wallet, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     SIWXSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(DefaultSIWXSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		// Strict, not Lax: the post-sign-in redirect is same-origin
		// (sanitizeNextPath enforces same-origin paths), so Strict never
		// blocks the legitimate flow, but it stops the session cookie from
		// riding along on any cross-site request — a second layer under the
		// Content-Type/Sec-Fetch-Site guards on the verify endpoint.
		SameSite: http.SameSiteStrictMode,
	})

	if next := sanitizeNextPath(firstNonEmptyStr(body.Next, r.URL.Query().Get("next"))); next != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"wallet": wallet, "sessionToken": token})
}

// renderSIWXPage renders the browser sign-in page.
func (v *Verifier) renderSIWXPage(w http.ResponseWriter, r *http.Request, rule *RouteRule, next string, reason error) {
	host := requestHost(r)
	data := map[string]any{
		"Host":      host,
		"OfferName": rule.OfferName,
		"VerifyURL": publicPrefix(rule, host) + authVerifySuffix,
		"Next":      sanitizeNextPath(next),
		"Reason":    "",
	}
	if reason != nil {
		data["Reason"] = reason.Error()
	}
	if err := siwxChallengeTmpl.Execute(w, data); err != nil {
		log.Printf("x402-verifier: render siwx page: %v", err)
	}
}

// authRuleForPrefix finds a gate:auth rule whose offer prefix matches, so
// the sign-in endpoints only exist where they're needed.
func (v *Verifier) authRuleForPrefix(prefix string) *RouteRule {
	cfg := v.config.Load()
	if cfg == nil {
		return nil
	}
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.IsAuth() && strings.TrimSuffix(r.StripPrefix, "/") == strings.TrimSuffix(prefix, "/") {
			return r
		}
	}
	return nil
}

// sanitizeNextPath allows only same-origin absolute paths for post-auth
// redirects — anything else (absolute URLs, scheme-relative //host, or
// javascript:) is an open-redirect vector and is dropped. A backslash is
// rejected outright: Go's url.Parse treats "\" as a path byte, but WHATWG
// browsers normalize it to "/", so "/\evil.com" would pass the checks here
// yet resolve to "//evil.com" → https://evil.com in the browser.
func sanitizeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return ""
	}
	if strings.Contains(next, "\\") {
		return ""
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return ""
	}
	return next
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
