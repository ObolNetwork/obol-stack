package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tempoxyz/mpp-go/pkg/mpp"
	mppserver "github.com/tempoxyz/mpp-go/pkg/server"
	"github.com/tempoxyz/mpp-go/pkg/tempo"
	temposerver "github.com/tempoxyz/mpp-go/pkg/tempo/server"
	tempotx "github.com/tempoxyz/tempo-go/pkg/transaction"
	x402types "github.com/x402-foundation/x402/go/types"
)

const (
	mppMethodStripe = "stripe"
	mppMethodTempo  = "tempo"
	mppIntentCharge = "charge"

	mppChallengeSecretEnv = "MPP_CHALLENGE_SECRET"
	tempoMPPRPCURLEnv     = "TEMPO_MPP_RPC_URL"

	tempoMPPTimeout = 60 * time.Second
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func mppChallengeSecret() string {
	return strings.TrimSpace(os.Getenv(mppChallengeSecretEnv))
}

func mppRealm(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if host := r.Header.Get("X-Forwarded-Host"); host != "" {
		return host
	}
	return ResourceServiceName
}

func mppExpires(rule *RouteRule) string {
	seconds := rule.MaxTimeoutSeconds
	if seconds <= 0 {
		seconds = DefaultMaxTimeoutSeconds
	}
	return time.Now().UTC().Add(time.Duration(seconds) * time.Second).Format(time.RFC3339)
}

func stripeProfileID(card *CardRoute) string {
	if card == nil {
		return ""
	}
	return firstNonEmpty(card.ProfileID, os.Getenv("STRIPE_PROFILE_ID"))
}

func validStripeProfileID(profileID string) bool {
	return strings.HasPrefix(profileID, "profile_") || strings.HasPrefix(profileID, "profile_test_")
}

func stripeMPPRequest(rule *RouteRule) map[string]any {
	card := rule.Card
	decimals := card.cardDecimals()
	currency := card.cardCurrency()
	profileID := stripeProfileID(card)
	return map[string]any{
		"amount":   rule.Price,
		"currency": currency,
		"decimals": decimals,
		"methodDetails": map[string]any{
			"profileId":          profileID,
			"networkId":          profileID,
			"paymentMethodTypes": card.cardPaymentMethodTypes(),
		},
	}
}

func stripeMPPChallenge(rule *RouteRule, realm, secret string) (*mpp.Challenge, error) {
	if secret == "" {
		return nil, errors.New("missing MPP challenge secret")
	}
	profileID := stripeProfileID(rule.Card)
	if !validStripeProfileID(profileID) {
		return nil, fmt.Errorf("invalid Stripe profile id %q", profileID)
	}
	return mpp.NewChallenge(
		secret,
		realm,
		mppMethodStripe,
		mppIntentCharge,
		stripeMPPRequest(rule),
		mpp.WithExpires(mppExpires(rule)),
	), nil
}

func tempoMPPRequest(rule *RouteRule) map[string]any {
	tempoRoute := rule.MPPTempo
	decimals := tempoRoute.Decimals
	if decimals == 0 {
		decimals = tempo.DefaultDecimals
	}
	params := tempo.ChargeRequestParams{
		Amount:         rule.Price,
		Currency:       tempoRoute.Asset,
		Recipient:      tempoRoute.PayTo,
		Decimals:       decimals,
		Description:    rule.Description,
		ExternalID:     rule.OfferNamespace + "/" + rule.OfferName,
		ChainID:        tempoRoute.ChainID,
		SupportedModes: []tempo.ChargeMode{tempo.ChargeModePull},
	}
	req, err := tempo.NormalizeChargeRequest(params)
	if err != nil {
		return map[string]any{}
	}
	return req.Map()
}

func tempoMPPChallenge(rule *RouteRule, realm, secret string) (*mpp.Challenge, error) {
	if rule.MPPTempo == nil {
		return nil, errors.New("Tempo MPP not configured")
	}
	if secret == "" {
		return nil, errors.New("missing MPP challenge secret")
	}
	request := tempoMPPRequest(rule)
	if len(request) == 0 {
		return nil, errors.New("invalid Tempo MPP request")
	}
	return mpp.NewChallenge(
		secret,
		realm,
		mppMethodTempo,
		mppIntentCharge,
		request,
		mpp.WithExpires(mppExpires(rule)),
	), nil
}

func addMPPAuthenticateHeaders(w http.ResponseWriter, r *http.Request, rule *RouteRule) {
	secret := mppChallengeSecret()
	if secret == "" {
		if rule.IsCard() || rule.MPPTempo != nil {
			log.Printf("x402-mpp: %s unset; serving legacy x402/card 402 without WWW-Authenticate", mppChallengeSecretEnv)
		}
		return
	}
	realm := mppRealm(r)
	if rule.IsCard() {
		challenge, err := stripeMPPChallenge(rule, realm, secret)
		if err != nil {
			log.Printf("x402-mpp: skip stripe challenge for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		} else if header, err := challenge.ToAuthenticateStrict(realm); err == nil {
			w.Header().Add("WWW-Authenticate", header)
		}
	}
	if rule.MPPTempo != nil {
		challenge, err := tempoMPPChallenge(rule, realm, secret)
		if err != nil {
			log.Printf("x402-mpp: skip tempo challenge for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		} else if header, err := challenge.ToAuthenticateStrict(realm); err == nil {
			w.Header().Add("WWW-Authenticate", header)
		}
	}
}

func sendMPPPaymentRequiredJSON(w http.ResponseWriter, r *http.Request, rule *RouteRule, requirements []x402types.PaymentRequirements, extensions map[string]any) {
	addMPPAuthenticateHeaders(w, r, rule)
	sendPaymentRequiredJSON(w, r, requirements, extensions)
}

func mppPaymentAuthorization(r *http.Request) string {
	return mpp.FindPaymentAuthorization(r.Header.Get("Authorization"))
}

func mppAuthorizationMethod(r *http.Request) string {
	auth := mppPaymentAuthorization(r)
	if auth == "" {
		return ""
	}
	cred, err := mpp.ParseCredential(auth)
	if err != nil {
		return ""
	}
	return cred.Challenge.Method
}

func validateMPPCredential(auth, realm, secret, method, intent string, request map[string]any) (*mpp.Credential, error) {
	if secret == "" {
		return nil, fmt.Errorf("%s is required for MPP Authorization credentials", mppChallengeSecretEnv)
	}
	cred, err := mpp.ParseCredential(auth)
	if err != nil {
		return nil, err
	}
	echoedRequest, err := mpp.B64Decode(cred.Challenge.Request)
	if err != nil {
		return nil, fmt.Errorf("invalid echoed MPP request: %w", err)
	}
	expected := mpp.NewChallenge(
		secret,
		cred.Challenge.Realm,
		cred.Challenge.Method,
		cred.Challenge.Intent,
		echoedRequest,
		echoedChallengeOptions(cred)...,
	)
	if !mpp.ConstantTimeEqual(cred.Challenge.ID, expected.ID) {
		return nil, errors.New("MPP challenge was not issued by this verifier")
	}
	if cred.Challenge.Realm != realm {
		return nil, errors.New("MPP realm mismatch")
	}
	if cred.Challenge.Method != method || cred.Challenge.Intent != intent {
		return nil, fmt.Errorf("unsupported MPP credential %s/%s", cred.Challenge.Method, cred.Challenge.Intent)
	}
	if !mpp.JSONEqual(echoedRequest, request) {
		return nil, errors.New("MPP credential request does not match route")
	}
	if cred.Challenge.Expires == "" {
		return nil, errors.New("MPP credential missing expiry")
	}
	expires, err := time.Parse(time.RFC3339, cred.Challenge.Expires)
	if err != nil {
		return nil, fmt.Errorf("invalid MPP expiry: %w", err)
	}
	if time.Now().UTC().After(expires) {
		return nil, errors.New("MPP credential expired")
	}
	return cred, nil
}

func echoedChallengeOptions(cred *mpp.Credential) []mpp.ChallengeOption {
	var opts []mpp.ChallengeOption
	if cred.Challenge.Expires != "" {
		opts = append(opts, mpp.WithExpires(cred.Challenge.Expires))
	}
	if cred.Challenge.Digest != "" {
		opts = append(opts, mpp.WithDigest(cred.Challenge.Digest))
	}
	if cred.Challenge.Opaque != nil {
		opts = append(opts, mpp.WithMeta(cred.Challenge.Opaque))
	}
	return opts
}

func parseStripeMPPCredential(auth, realm string, rule *RouteRule) (cardCredential, error) {
	cred, err := validateMPPCredential(auth, realm, mppChallengeSecret(), mppMethodStripe, mppIntentCharge, stripeMPPRequest(rule))
	if err != nil {
		return cardCredential{}, err
	}
	spt := firstNonEmpty(
		anyString(cred.Payload["spt"]),
		anyString(cred.Payload["shared_payment_token"]),
		anyString(cred.Payload["sharedPaymentToken"]),
	)
	return cardCredential{SPT: spt, ExternalID: anyString(cred.Payload["externalId"])}.normalize()
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return ""
	}
}

func setMPPReceiptHeaders(w http.ResponseWriter, receipt *mpp.Receipt) {
	if receipt == nil {
		return
	}
	header := receipt.ToPaymentReceipt()
	w.Header().Set("Payment-Receipt", header)
	w.Header().Set("Authentication-Info", header)
}

func setCardReceiptHeaders(w http.ResponseWriter, reference string) {
	receiptJSON := cardReceiptJSON(reference)
	w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(receiptJSON))
	receipt := mpp.Success(reference, mpp.WithReceiptMethod(mppMethodStripe))
	setMPPReceiptHeaders(w, receipt)
}

// tempoMPPGateway settles a preflighted Tempo pull credential after the
// upstream succeeds.
type tempoMPPGateway interface {
	preflight(r *http.Request, rule *RouteRule) (*tempoMPPAuthorization, error)
	settle(ctx context.Context, auth *tempoMPPAuthorization, rule *RouteRule) (*mpp.Receipt, error)
	release(auth *tempoMPPAuthorization)
}

type tempoMPPAuthorization struct {
	Authorization string
	ChallengeID   string
	Realm         string
}

type tempoMPPGatewayImpl struct {
	mu       sync.Mutex
	reserved map[string]time.Time
}

func newTempoMPPGateway() *tempoMPPGatewayImpl {
	return &tempoMPPGatewayImpl{reserved: make(map[string]time.Time)}
}

var defaultTempoMPPGateway tempoMPPGateway = newTempoMPPGateway()

func (g *tempoMPPGatewayImpl) preflight(r *http.Request, rule *RouteRule) (*tempoMPPAuthorization, error) {
	auth := mppPaymentAuthorization(r)
	if auth == "" {
		return nil, errors.New("missing MPP Authorization")
	}
	cred, err := validateMPPCredential(auth, mppRealm(r), mppChallengeSecret(), mppMethodTempo, mppIntentCharge, tempoMPPRequest(rule))
	if err != nil {
		return nil, err
	}
	payloadType := anyString(cred.Payload["type"])
	if payloadType != string(tempo.CredentialTypeTransaction) {
		return nil, fmt.Errorf("Tempo MPP only supports pull transaction credentials; got %q", payloadType)
	}
	if err := preflightTempoTransactionSignature(cred); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for key, seen := range g.reserved {
		if now.Sub(seen) > time.Hour {
			delete(g.reserved, key)
		}
	}
	if _, exists := g.reserved[cred.Challenge.ID]; exists {
		return nil, errors.New("Tempo MPP challenge already in flight")
	}
	g.reserved[cred.Challenge.ID] = now
	return &tempoMPPAuthorization{Authorization: auth, ChallengeID: cred.Challenge.ID, Realm: mppRealm(r)}, nil
}

func preflightTempoTransactionSignature(cred *mpp.Credential) error {
	raw := anyString(cred.Payload["signature"])
	if raw == "" {
		return errors.New("Tempo MPP transaction credential missing signature")
	}
	tx, err := tempotx.Deserialize(raw)
	if err != nil {
		return fmt.Errorf("invalid Tempo transaction payload: %w", err)
	}
	if _, err := tempotx.VerifySignature(tx); err != nil {
		return fmt.Errorf("invalid Tempo transaction signature: %w", err)
	}
	return nil
}

func (g *tempoMPPGatewayImpl) release(auth *tempoMPPAuthorization) {
	if auth == nil {
		return
	}
	g.mu.Lock()
	delete(g.reserved, auth.ChallengeID)
	g.mu.Unlock()
}

func (g *tempoMPPGatewayImpl) settle(ctx context.Context, auth *tempoMPPAuthorization, rule *RouteRule) (*mpp.Receipt, error) {
	if auth == nil {
		return nil, errors.New("missing Tempo MPP authorization")
	}
	method, err := temposerver.MethodFromConfig(temposerver.Config{
		Currency:       rule.MPPTempo.Asset,
		Recipient:      rule.MPPTempo.PayTo,
		Decimals:       rule.MPPTempo.Decimals,
		ChainID:        rule.MPPTempo.ChainID,
		RPCURL:         os.Getenv(tempoMPPRPCURLEnv),
		SupportedModes: []tempo.ChargeMode{tempo.ChargeModePull},
	})
	if err != nil {
		return nil, err
	}
	server := mppserver.New(method, auth.Realm, mppChallengeSecret())
	ctx, cancel := context.WithTimeout(ctx, tempoMPPTimeout)
	defer cancel()
	result, err := server.Charge(ctx, mppserver.ChargeParams{
		Authorization:  auth.Authorization,
		Amount:         rule.Price,
		Currency:       rule.MPPTempo.Asset,
		Recipient:      rule.MPPTempo.PayTo,
		ExternalID:     rule.OfferNamespace + "/" + rule.OfferName,
		Description:    rule.Description,
		SupportedModes: []tempo.ChargeMode{tempo.ChargeModePull},
		ChainID:        int(rule.MPPTempo.ChainID),
	})
	if err != nil {
		return nil, err
	}
	if result.Receipt == nil {
		return nil, errors.New("Tempo MPP settlement did not return a receipt")
	}
	return result.Receipt, nil
}

func mppReceiptJSON(receipt *mpp.Receipt) []byte {
	b, err := json.Marshal(receipt)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func (v *Verifier) serveTempoMPPGated(
	w http.ResponseWriter,
	r *http.Request,
	rule *RouteRule,
	requirement x402types.PaymentRequirements,
	extensions map[string]any,
	proxy http.Handler,
	gw tempoMPPGateway,
) {
	reqs := []x402types.PaymentRequirements{requirement}
	auth, err := gw.preflight(r, rule)
	if err != nil {
		log.Printf("x402-mpp: bad Tempo credential for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		sendMPPPaymentRequiredJSON(w, r, rule, reqs, extensions)
		return
	}

	interceptor := &settlementInterceptor{
		w: w,
		settleFunc: func() bool {
			receipt, err := gw.settle(r.Context(), auth, rule)
			if err != nil {
				gw.release(auth)
				log.Printf("x402-mpp: Tempo settlement failed for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
				http.Error(w, "Tempo MPP settlement failed", http.StatusBadGateway)
				return false
			}
			setMPPReceiptHeaders(w, receipt)
			w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(mppReceiptJSON(receipt)))
			return true
		},
		onFailure: func(statusCode int) {
			gw.release(auth)
			log.Printf("x402-mpp: upstream returned %d for %s/%s, Tempo transaction not broadcast", statusCode, rule.OfferNamespace, rule.OfferName)
		},
	}

	defer func() {
		rec := recover()
		if !interceptor.committed {
			gw.release(auth)
			log.Printf("x402-mpp: upstream produced no response for %s/%s, Tempo transaction not broadcast", rule.OfferNamespace, rule.OfferName)
		}
		if rec != nil {
			panic(rec)
		}
	}()
	proxy.ServeHTTP(interceptor, r)
}
