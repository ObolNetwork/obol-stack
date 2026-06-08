package x402

// SPIKE — MPP credit-card (Stripe stripe.charge) seam for the seller gateway.
//
// This file demonstrates how the MPP credit-card payment method plugs into
// the existing x402 verifier without disturbing the crypto path:
//
//   - buildCardRequirement(): emits the card option as a 402 `accepts[]`
//     entry, mirroring the MPP stripe.charge challenge.request shape
//     (amount/currency/decimals + methodDetails{networkId,paymentMethodTypes}).
//   - cardSettleFunc / stripeCardSettler: charges the buyer's pre-authorized
//     Shared Payment Token (SPT) by creating+confirming a Stripe
//     PaymentIntent, adapted from github.com/cp0x-org/mppx/stripe.
//   - serveCardGated(): the in-process HandleProxy branch that gates a card
//     route — 402 when unpaid, Stripe charge then proxy when paid.
//
// What is intentionally STUBBED for this spike (called out inline):
//   - RouteRule.Card is not yet populated by the ServiceOffer route source
//     (serviceoffer_source.go); wiring that is the integration follow-up.
//   - The Stripe secret key is read from STRIPE_SECRET_KEY; production must
//     read a per-offer Kubernetes Secret so one verifier can gate many
//     sellers without sharing a key.
//   - Charge happens before proxying (mppx Verify semantics). A production
//     build should split authorize-before-serve from capture-after-success
//     and persist the PaymentIntent reference for idempotent retries.

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
	"time"

	x402types "github.com/coinbase/x402/go/types"
)

const (
	// cardScheme is the PaymentRequirements.Scheme used for the card option
	// so a card-capable buyer can distinguish it from the x402 "exact"
	// crypto option co-offered on the same route.
	cardScheme = "card"
	// cardNetworkStripe identifies the Stripe rail in the card requirement.
	cardNetworkStripe = "stripe"

	stripePaymentIntentsURL = "https://api.stripe.com/v1/payment_intents"
)

// IsCard reports whether this route is gated by the MPP credit-card method
// rather than x402 on-chain settlement.
func (r *RouteRule) IsCard() bool { return r != nil && r.Card != nil }

// cardDecimals returns the currency minor-unit precision, defaulting to 2.
func (c *CardRoute) cardDecimals() int {
	if c != nil && c.Decimals > 0 {
		return c.Decimals
	}
	return 2
}

// cardCurrency returns the ISO-4217 currency, defaulting to "usd".
func (c *CardRoute) cardCurrency() string {
	if c != nil && c.Currency != "" {
		return strings.ToLower(c.Currency)
	}
	return "usd"
}

// cardPaymentMethodTypes returns the advertised Stripe payment-method types,
// defaulting to ["card"].
func (c *CardRoute) cardPaymentMethodTypes() []string {
	if c != nil && len(c.PaymentMethodTypes) > 0 {
		return c.PaymentMethodTypes
	}
	return []string{"card"}
}

// buildCardRequirement builds the 402 `accepts[]` entry advertising the MPP
// credit-card (Stripe) option for a card route. The Amount is in currency
// minor units (e.g. cents) to match Stripe's PaymentIntent API; the human
// decimal price is mirrored under Extra.request for MPP-aware clients that
// normalize against `decimals`.
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
// JSON) in the X-PAYMENT header: a Stripe Shared Payment Token plus an
// optional client-side external id for reconciliation.
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

// parseCardCredential decodes the base64 X-PAYMENT card payload. It accepts
// both the bare payload ({spt,externalId}) and an x402-style wrapper
// ({payload:{...}}) so the spike is flexible about the buyer encoding.
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

// cardSettleFunc charges a buyer's pre-authorized card credential for one
// request and returns an opaque settlement reference (the Stripe
// PaymentIntent id) on success. Unlike the deferred, offline x402 facilitator
// settle, a card charge is synchronous and online, so implementations must be
// safe to call on the request path.
type cardSettleFunc func(ctx context.Context, card *CardRoute, amountMinorUnits, currency string, cred cardCredential) (reference string, err error)

// stripeCardSettler implements cardSettleFunc against the Stripe
// PaymentIntents API, adapted from github.com/cp0x-org/mppx/stripe.
type stripeCardSettler struct {
	httpClient *http.Client
	// secretKey returns the seller's Stripe secret key. SPIKE: sourced from
	// STRIPE_SECRET_KEY; production must read a per-offer Kubernetes Secret.
	secretKey func() string
}

func newStripeCardSettler() *stripeCardSettler {
	return &stripeCardSettler{
		httpClient: &http.Client{Timeout: 20 * time.Second},
		secretKey:  func() string { return strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")) },
	}
}

// defaultCardSettler is the package default used by serveCardGated. Kept as a
// package var (not a Verifier field) so the spike doesn't disturb the
// Verifier constructor; serveCardGated takes the settleFunc so tests can
// inject a fake.
var defaultCardSettler = newStripeCardSettler()

// buildPaymentIntentForm is the form body for Stripe PaymentIntent
// create+confirm. Split out so it can be unit-tested without network.
func buildPaymentIntentForm(amountMinorUnits, currency, spt string) url.Values {
	form := url.Values{}
	form.Set("amount", amountMinorUnits)
	form.Set("currency", currency)
	form.Set("confirm", "true")
	form.Set("shared_payment_granted_token", spt)
	form.Set("automatic_payment_methods[enabled]", "true")
	form.Set("automatic_payment_methods[allow_redirects]", "never")
	return form
}

// settle creates+confirms a Stripe PaymentIntent from the buyer's SPT.
func (s *stripeCardSettler) settle(ctx context.Context, card *CardRoute, amountMinorUnits, currency string, cred cardCredential) (string, error) {
	key := s.secretKey()
	if key == "" {
		return "", errors.New("stripe secret key not configured (STRIPE_SECRET_KEY)")
	}
	form := buildPaymentIntentForm(amountMinorUnits, currency, cred.SPT)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stripePaymentIntentsURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build stripe request: %w", err)
	}
	// Stripe uses HTTP Basic with the secret key as the username, no password.
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key+":")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Idempotency keyed on the single-use SPT so retries don't double-charge.
	req.Header.Set("Idempotency-Key", "obol_"+cred.SPT)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stripe request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("stripe PaymentIntent failed (HTTP %d)", resp.StatusCode)
	}
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode stripe response: %w", err)
	}
	if body.Status != "succeeded" {
		return "", fmt.Errorf("stripe PaymentIntent status: %s", body.Status)
	}
	return body.ID, nil
}

// cardReceiptJSON builds the X-PAYMENT-RESPONSE body surfaced to the buyer
// after a successful card charge.
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

// serveCardGated is the in-process seller gate for MPP credit-card offers,
// invoked from Verifier.HandleProxy when the matched route is a card route.
// SPIKE: charges before proxying (mppx Verify semantics) and uses the JSON
// 402 (no HTML page); see the file header for the production refinements.
func (v *Verifier) serveCardGated(
	w http.ResponseWriter,
	r *http.Request,
	rule *RouteRule,
	requirement x402types.PaymentRequirements,
	extensions map[string]any,
	proxy http.Handler,
	settle cardSettleFunc,
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

	currency, _ := requirement.Extra["currency"].(string)
	reference, err := settle(r.Context(), rule.Card, requirement.Amount, currency, cred)
	if err != nil {
		log.Printf("x402-card: settle failed for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		sendPaymentRequiredJSON(w, r, reqs, extensions)
		return
	}

	// Charge succeeded — surface the receipt and proxy upstream.
	w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(cardReceiptJSON(reference)))
	proxy.ServeHTTP(w, r)
}
