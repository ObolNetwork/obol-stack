package x402

// MPP credit-card (Stripe stripe.charge) settlement for the seller gateway.
//
// Plugs the MPP credit-card method into the existing x402 verifier without
// disturbing the crypto path:
//
//   - buildCardRequirement(): emits the card option as a 402 accepts[] entry,
//     mirroring the MPP stripe.charge challenge.request (amount in currency
//     minor units + currency/decimals + methodDetails{networkId,
//     paymentMethodTypes}).
//   - cardGateway / stripeCardGateway: a two-phase authorize -> capture/cancel
//     against Stripe PaymentIntents (manual capture). The buyer's pre-authorized
//     Shared Payment Token is AUTHORIZED before the upstream is served and only
//     CAPTURED after a successful (<400) upstream response; a failed upstream
//     CANCELS the authorization so the buyer is never charged for nothing.
//   - serveCardGated(): the in-process HandleProxy branch — authorize-before-
//     serve, capture-after-success, cancel-on-failure, with an in-memory SPT
//     replay guard so a Shared Payment Token cannot be reused.
//
// Productionization notes (see README "Credit-card payments (MPP)"):
//   - The Stripe secret is read from STRIPE_SECRET_KEY; the verifier Deployment
//     sources it from the x402-secrets Secret. A per-offer/per-namespace Secret
//     needs the verifier's resourceName-scoped secret RBAC to be widened
//     deliberately and is intentionally deferred.
//   - The replay guard is per-pod; the verifier runs single-replica, so this is
//     sufficient today. A multi-replica verifier would need shared replay state.
//   - The SPT is passed as the top-level form field shared_payment_granted_token
//     per the cp0x-org/mppx reference; validate against a live Stripe "machine
//     payments" account before relying on it in production.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	x402types "github.com/coinbase/x402/go/types"
)

const (
	// cardScheme is the PaymentRequirements.Scheme used for the card option so
	// a card-capable buyer can distinguish it from the x402 "exact" crypto
	// option co-offered on the same route.
	cardScheme = "card"
	// cardNetworkStripe identifies the Stripe rail in the card requirement.
	cardNetworkStripe = "stripe"
	// defaultCardCurrency is the fallback ISO-4217 currency for card offers.
	defaultCardCurrency = "usd"

	// stripeAPIBase is the default Stripe API base URL (overridable on the
	// gateway for tests).
	stripeAPIBase = "https://api.stripe.com/v1"

	// cardStripeTimeout bounds each Stripe API call. Authorize/capture/cancel
	// run on detached contexts so a client disconnect cannot cancel an
	// in-flight money operation.
	cardStripeTimeout = 20 * time.Second

	// sptReplayTTL is how long a seen Shared Payment Token stays blocked in the
	// per-pod replay guard. SPTs are single-use and short-lived, so an hour is
	// ample headroom over their validity window.
	sptReplayTTL = time.Hour
)

// IsCard reports whether this route is gated by the MPP credit-card method
// rather than x402 on-chain settlement.
func (r *RouteRule) IsCard() bool { return r != nil && r.Card != nil }

// currencyMinorUnits returns the ISO-4217 minor-unit exponent (decimal places)
// for a currency, defaulting to 2. Stripe expects PaymentIntent amounts in the
// currency's smallest unit, which is not always cents (JPY has 0, BHD has 3).
func currencyMinorUnits(currency string) int {
	switch strings.ToLower(strings.TrimSpace(currency)) {
	case "jpy", "krw", "vnd", "clp", "isk", "bif", "djf", "gnf", "kmf", "pyg", "rwf", "ugx", "vuv", "xaf", "xof", "xpf":
		return 0
	case "bhd", "iqd", "jod", "kwd", "omr", "tnd", "lyd":
		return 3
	default:
		return 2
	}
}

func (c *CardRoute) cardDecimals() int {
	if c == nil {
		return 2
	}
	if c.Decimals > 0 {
		return c.Decimals
	}
	return currencyMinorUnits(c.Currency)
}

func (c *CardRoute) cardCurrency() string {
	if c != nil && c.Currency != "" {
		return strings.ToLower(c.Currency)
	}
	return defaultCardCurrency
}

func (c *CardRoute) cardPaymentMethodTypes() []string {
	if c != nil && len(c.PaymentMethodTypes) > 0 {
		return c.PaymentMethodTypes
	}
	return []string{"card"}
}

// buildCardRequirement builds the 402 accepts[] entry advertising the MPP
// credit-card (Stripe) option for a card route. The Amount is in currency minor
// units (e.g. cents for usd, whole yen for jpy) to match Stripe's PaymentIntent
// API; the human decimal price is mirrored under Extra.request for MPP-aware
// clients that normalize against `decimals`.
func buildCardRequirement(rule *RouteRule) x402types.PaymentRequirements {
	card := rule.Card
	decimals := card.cardDecimals()
	currency := card.cardCurrency()
	pmt := card.cardPaymentMethodTypes()
	amountMinor := decimalToAtomic(rule.Price, decimals)

	return x402types.PaymentRequirements{
		Scheme:            cardScheme,
		Network:           cardNetworkStripe,
		Amount:            amountMinor,
		Asset:             "", // no on-chain asset for card settlement
		PayTo:             card.Account,
		MaxTimeoutSeconds: 300,
		Extra: map[string]any{
			"method":             cardNetworkStripe,
			"intent":             "charge",
			"currency":           currency,
			"decimals":           decimals,
			"networkId":          card.NetworkID,
			"paymentMethodTypes": pmt,
			// Mirror the MPP stripe.charge challenge.request so an MPP card
			// client can mint a Shared Payment Token against this offer.
			"request": map[string]any{
				"amount":   rule.Price,
				"currency": currency,
				"decimals": decimals,
				"methodDetails": map[string]any{
					"networkId":          card.NetworkID,
					"paymentMethodTypes": pmt,
				},
			},
		},
	}
}

// cardCredential is the buyer-supplied card payment payload carried (base64
// JSON) in the X-PAYMENT header: a Stripe Shared Payment Token plus an optional
// client-side external id for reconciliation.
type cardCredential struct {
	SPT        string `json:"spt"`
	ExternalID string `json:"externalId,omitempty"`
}

func (c cardCredential) normalize() (cardCredential, error) {
	c.SPT = strings.TrimSpace(c.SPT)
	if !strings.HasPrefix(c.SPT, "spt_") {
		return cardCredential{}, errors.New(`card credential spt must start with "spt_"`)
	}
	return c, nil
}

// parseCardCredential decodes the base64 X-PAYMENT card payload. It accepts both
// the bare payload ({spt,externalId}) and an x402-style wrapper ({payload:{...}}).
func parseCardCredential(header string) (cardCredential, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return cardCredential{}, fmt.Errorf("invalid card credential base64: %w", err)
	}
	var direct cardCredential
	if err := json.Unmarshal(raw, &direct); err == nil && direct.SPT != "" {
		return direct.normalize()
	}
	var wrapper struct {
		Payload cardCredential `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Payload.SPT != "" {
		return wrapper.Payload.normalize()
	}
	return cardCredential{}, errors.New("card credential missing spt")
}

// ── SPT replay guard ────────────────────────────────────────────────────────

// sptReplayGuard rejects reuse of a Shared Payment Token. A token is reserved
// for the duration of a request and either consumed (kept blocked for the TTL)
// on a captured charge or released (unblocked) when the charge does not land,
// so transient failures can be retried with the same token.
type sptReplayGuard struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func newSPTReplayGuard(ttl time.Duration) *sptReplayGuard {
	return &sptReplayGuard{seen: make(map[string]time.Time), ttl: ttl}
}

// tryReserve records the token as in-flight and returns false if it is already
// reserved or recently consumed.
func (g *sptReplayGuard) tryReserve(spt string) bool {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, t := range g.seen {
		if now.Sub(t) > g.ttl {
			delete(g.seen, k)
		}
	}
	if _, exists := g.seen[spt]; exists {
		return false
	}
	g.seen[spt] = now
	return true
}

// release unblocks a token so it can be retried (charge did not land).
func (g *sptReplayGuard) release(spt string) {
	g.mu.Lock()
	delete(g.seen, spt)
	g.mu.Unlock()
}

// consume keeps a token blocked for the TTL after a successful capture.
func (g *sptReplayGuard) consume(spt string) {
	g.mu.Lock()
	g.seen[spt] = time.Now()
	g.mu.Unlock()
}

// ── Stripe gateway ──────────────────────────────────────────────────────────

// cardGateway is the two-phase card settlement seam: authorize holds funds,
// capture takes them after the upstream serves successfully, cancel releases
// the hold on failure. Implementations must be safe to call on the request
// path (card settlement is synchronous and online).
type cardGateway interface {
	authorize(ctx context.Context, card *CardRoute, amountMinorUnits, currency string, cred cardCredential) (paymentIntentID string, err error)
	capture(ctx context.Context, card *CardRoute, paymentIntentID string) error
	cancel(ctx context.Context, card *CardRoute, paymentIntentID string) error
}

// stripeCardGateway implements cardGateway against the Stripe PaymentIntents
// API (manual capture), adapted from github.com/cp0x-org/mppx/stripe.
type stripeCardGateway struct {
	httpClient *http.Client
	baseURL    string
	// secretKey returns the seller's Stripe secret key.
	secretKey func() string
}

func newStripeCardGateway() *stripeCardGateway {
	return &stripeCardGateway{
		httpClient: &http.Client{Timeout: cardStripeTimeout},
		baseURL:    stripeAPIBase,
		secretKey:  func() string { return strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")) },
	}
}

// defaultCardGateway / defaultSPTGuard are the package defaults used by
// serveCardGated. Kept as package vars (not Verifier fields) so the card path
// does not disturb the Verifier constructor; serveCardGated takes both so tests
// can inject fakes.
var (
	defaultCardGateway cardGateway = newStripeCardGateway()
	defaultSPTGuard                = newSPTReplayGuard(sptReplayTTL)
)

// buildAuthorizeForm is the form body for a manual-capture Stripe PaymentIntent
// create+confirm (the authorization). Split out for unit testing.
func buildAuthorizeForm(amountMinorUnits, currency, spt string) url.Values {
	form := url.Values{}
	form.Set("amount", amountMinorUnits)
	form.Set("currency", currency)
	form.Set("confirm", "true")
	form.Set("capture_method", "manual")
	form.Set("shared_payment_granted_token", spt)
	form.Set("automatic_payment_methods[enabled]", "true")
	form.Set("automatic_payment_methods[allow_redirects]", "never")
	return form
}

func (s *stripeCardGateway) authorize(ctx context.Context, _ *CardRoute, amountMinorUnits, currency string, cred cardCredential) (string, error) {
	id, status, err := s.do(ctx, s.baseURL+"/payment_intents", buildAuthorizeForm(amountMinorUnits, currency, cred.SPT), "obol_auth_"+cred.SPT)
	if err != nil {
		return "", err
	}
	// Manual capture + confirm: a successful authorization yields
	// requires_capture (funds held, not taken). Accept succeeded defensively.
	switch status {
	case "requires_capture", "succeeded":
		return id, nil
	case "requires_action":
		return "", errors.New("stripe PaymentIntent requires action (3DS) — not supported for machine payments")
	default:
		return "", fmt.Errorf("stripe authorize status: %s", status)
	}
}

func (s *stripeCardGateway) capture(ctx context.Context, _ *CardRoute, paymentIntentID string) error {
	_, status, err := s.do(ctx, s.baseURL+"/payment_intents/"+url.PathEscape(paymentIntentID)+"/capture", url.Values{}, "obol_cap_"+paymentIntentID)
	if err != nil {
		return err
	}
	if status != "succeeded" {
		return fmt.Errorf("stripe capture status: %s", status)
	}
	return nil
}

func (s *stripeCardGateway) cancel(ctx context.Context, _ *CardRoute, paymentIntentID string) error {
	_, _, err := s.do(ctx, s.baseURL+"/payment_intents/"+url.PathEscape(paymentIntentID)+"/cancel", url.Values{}, "")
	return err
}

// do issues a form-encoded POST to Stripe and returns the PaymentIntent id and
// status. Stripe uses HTTP Basic with the secret key as the username.
func (s *stripeCardGateway) do(ctx context.Context, endpoint string, form url.Values, idempotencyKey string) (id, status string, err error) {
	key := s.secretKey()
	if key == "" {
		return "", "", errors.New("stripe secret key not configured (STRIPE_SECRET_KEY)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("build stripe request: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key+":")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("stripe request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("stripe API failed (HTTP %d)", resp.StatusCode)
	}
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("decode stripe response: %w", err)
	}
	return body.ID, body.Status, nil
}

// cardReceiptJSON builds the X-PAYMENT-RESPONSE body surfaced to the buyer after
// a captured card charge.
func cardReceiptJSON(reference string) []byte {
	b, err := json.Marshal(map[string]string{
		"method":    cardNetworkStripe,
		"intent":    "charge",
		"reference": reference,
	})
	if err != nil {
		return []byte("{}")
	}
	return b
}

func detachedCardContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cardStripeTimeout)
}

// cancelCardHold releases an authorized PaymentIntent and logs a failure. An
// uncancelled hold auto-expires at Stripe, but a swallowed error would leave no
// operator trail, so cancel failures are logged rather than ignored.
func cancelCardHold(gw cardGateway, rule *RouteRule, paymentIntentID string) {
	ctx, cancel := detachedCardContext()
	defer cancel()
	if err := gw.cancel(ctx, rule.Card, paymentIntentID); err != nil {
		log.Printf("x402-card: cancel authorization %s for %s/%s failed: %v", paymentIntentID, rule.OfferNamespace, rule.OfferName, err)
	}
}

// serveCardGated is the in-process seller gate for MPP credit-card offers,
// invoked from Verifier.HandleProxy when the matched route is a card route. It
// authorizes the buyer's SPT, proxies on a successful authorization, then
// captures after a <400 upstream response (cancelling the hold otherwise). Uses
// the JSON 402 (no HTML page). proxy is the already-built upstream handler.
func (v *Verifier) serveCardGated(
	w http.ResponseWriter,
	r *http.Request,
	rule *RouteRule,
	requirement x402types.PaymentRequirements,
	extensions map[string]any,
	proxy http.Handler,
	gw cardGateway,
	guard *sptReplayGuard,
) {
	reqs := []x402types.PaymentRequirements{requirement}

	paymentHeader := r.Header.Get("X-PAYMENT")
	if paymentHeader == "" {
		sendPaymentRequiredJSON(w, r, reqs, extensions)
		return
	}

	cred, err := parseCardCredential(paymentHeader)
	if err != nil {
		log.Printf("x402-card: bad credential for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		sendPaymentRequiredJSON(w, r, reqs, extensions)
		return
	}

	// Replay defense: a Shared Payment Token is single-use.
	if !guard.tryReserve(cred.SPT) {
		log.Printf("x402-card: replayed SPT rejected for %s/%s", rule.OfferNamespace, rule.OfferName)
		sendPaymentRequiredJSON(w, r, reqs, extensions)
		return
	}

	currency, _ := requirement.Extra["currency"].(string)

	authCtx, cancelAuth := detachedCardContext()
	paymentIntentID, err := gw.authorize(authCtx, rule.Card, requirement.Amount, currency, cred)
	cancelAuth()
	if err != nil {
		// Authorization failed — buyer not charged; allow a retry with the SPT.
		guard.release(cred.SPT)
		log.Printf("x402-card: authorize failed for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		sendPaymentRequiredJSON(w, r, reqs, extensions)
		return
	}

	// Authorized — wire capture-after-success / cancel-on-failure around the
	// upstream via the shared settlementInterceptor.
	interceptor := &settlementInterceptor{
		w: w,
		settleFunc: func() bool {
			cctx, cc := detachedCardContext()
			defer cc()
			if capErr := gw.capture(cctx, rule.Card, paymentIntentID); capErr != nil {
				log.Printf("x402-card: capture failed for %s/%s: %v", rule.OfferNamespace, rule.OfferName, capErr)
				// Release the authorization hold and unblock the SPT.
				cancelCardHold(gw, rule, paymentIntentID)
				guard.release(cred.SPT)
				http.Error(w, "card capture failed", http.StatusBadGateway)
				return false
			}
			guard.consume(cred.SPT)
			w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(cardReceiptJSON(paymentIntentID)))
			return true
		},
		onFailure: func(statusCode int) {
			// Upstream failed — cancel the hold; buyer is not charged.
			cancelCardHold(gw, rule, paymentIntentID)
			guard.release(cred.SPT)
			log.Printf("x402-card: upstream returned %d for %s/%s, authorization cancelled", statusCode, rule.OfferNamespace, rule.OfferName)
		},
	}

	// Defensive reconcile: settleFunc/onFailure only fire from the
	// interceptor's WriteHeader. If the upstream handler panics or returns
	// without ever writing a response (committed stays false), neither runs —
	// cancel the hold and release the SPT so the buyer is not left with funds
	// authorized for a request that was never served. Re-panic to preserve the
	// server's own panic handling (e.g. http.ErrAbortHandler).
	defer func() {
		rec := recover()
		if !interceptor.committed {
			cancelCardHold(gw, rule, paymentIntentID)
			guard.release(cred.SPT)
			log.Printf("x402-card: upstream produced no response for %s/%s, authorization cancelled", rule.OfferNamespace, rule.OfferName)
		}
		if rec != nil {
			panic(rec)
		}
	}()
	proxy.ServeHTTP(interceptor, r)
}
