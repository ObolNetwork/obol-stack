package x402

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// The unlock offer is a gate:auth route whose session is minted only by the
// inline auth-capture payment on its first request; handleAuthEndpoints
// suppresses its free SIWX sign-in endpoints.

// isUnlockOffer reports whether rule is the configured auth-capture unlock offer.
func (v *Verifier) isUnlockOffer(cfg *PricingConfig, rule *RouteRule) bool {
	if cfg == nil || cfg.AuthCaptureUnlock == nil || !cfg.AuthCaptureUnlock.Enabled {
		return false
	}
	// An empty offerPrefix means no unlock offer, NOT "match everything". The
	// same config block also drives the per-request platform fee, which needs
	// no prefix — without this guard, enabling the fee would silently convert
	// every root-mounted gate:auth offer (StripPrefix "") into a paid unlock.
	prefix := strings.TrimSuffix(cfg.AuthCaptureUnlock.OfferPrefix, "/")
	if prefix == "" {
		return false
	}
	return strings.TrimSuffix(rule.StripPrefix, "/") == prefix
}

// handlePaidUnlock runs the auth-capture pay->settle->mint flow for the unlock
// offer and, on success, proxies the request to the upstream (the first paid
// message's answer). It fully handles the response in all branches.
func (v *Verifier) handlePaidUnlock(w http.ResponseWriter, r *http.Request, rule *RouteRule) {
	cfg := v.config.Load()
	if cfg == nil || cfg.AuthCaptureUnlock == nil || !cfg.AuthCaptureUnlock.Enabled {
		log.Printf("x402-verifier: auth-capture unlock config invalid: offer is disabled or missing")
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "unlock_misconfigured"})
		return
	}
	uc := *cfg.AuthCaptureUnlock

	if err := uc.Validate(); err != nil {
		log.Printf("x402-verifier: auth-capture unlock config invalid: %v", err)
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "unlock_misconfigured"})
		return
	}

	chainName := uc.Network
	if chainName == "" {
		chainName = cfg.Chain
	}
	chains := v.chains.Load()
	if chains == nil {
		log.Printf("x402-verifier: auth-capture unlock chain registry is not loaded")
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "unlock_misconfigured"})
		return
	}
	chain, ok := (*chains)[chainName]
	if !ok {
		log.Printf("x402-verifier: auth-capture unlock chain %q is not pre-resolved", chainName)
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "unlock_misconfigured"})
		return
	}

	asset := chain.DefaultAsset()
	if uc.Asset != "" {
		asset = ResolveAssetInfoForPayment(chain, RoutePayment{AssetAddress: uc.Asset})
	}
	payTo := uc.PayTo
	if payTo == "" {
		payTo = cfg.Wallet
	}
	// maxTimeoutSeconds=0 → ClampMaxTimeoutSeconds → DefaultMaxTimeoutSeconds.
	// Independent of CaptureDeadlineSecs (escrow hold written into Extra).
	req, err := BuildAuthCaptureRequirement(chain, asset, &uc, payTo, 0, time.Now())
	if err != nil {
		log.Printf("x402-verifier: build auth-capture unlock requirement: %v", err)
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "unlock_misconfigured"})
		return
	}

	paymentHeader := r.Header.Get("X-PAYMENT")
	if paymentHeader == "" {
		paymentHeader = r.Header.Get("PAYMENT-SIGNATURE")
	}
	if paymentHeader == "" {
		writeUnlockJSON(w, http.StatusPaymentRequired, struct {
			X402Version int                             `json:"x402Version"`
			Accepts     []x402types.PaymentRequirements `json:"accepts"`
			Error       string                          `json:"error"`
		}{
			// auth-capture is a v2 scheme (the verifier settles it against the
			// facilitator with x402Version 2); the challenge MUST advertise v2 so
			// a standard @x402 client builds a v2 payload (its auth-capture scheme
			// rejects x402Version != 2).
			X402Version: 2,
			Accepts:     []x402types.PaymentRequirements{req},
			Error:       "payment required to unlock this agent",
		})
		return
	}

	payloadBytes, err := base64.StdEncoding.DecodeString(paymentHeader)
	if err != nil {
		writeUnlockJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_payment_header"})
		return
	}
	// Settle against the requirement the client actually SIGNED
	// (payload.accepted), not the freshly rebuilt `req`: auth-capture's signed
	// PaymentInfo hash commits the server-issued captureDeadline/refundDeadline,
	// which drift between the 402 and this request (both call time.Now) and would
	// invalidate the signature. Validate the client did not tamper with the
	// economically-sensitive fields against our policy (`req`, built from config)
	// before forwarding — the facilitator does not know obol's intended
	// feeRecipient/payTo/amount, so a blind forward would let a client redirect
	// the fee or underpay.
	payload, err := x402types.ToPaymentPayload(payloadBytes)
	if err != nil {
		writeUnlockJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_payment_header"})
		return
	}
	signedReq := payload.Accepted
	if err := validateSignedAuthCapture(signedReq, req, uc.CaptureDeadlineSecs, time.Now().Unix()); err != nil {
		log.Printf("x402-verifier: unlock payment policy mismatch: %v", err)
		writeUnlockJSON(w, http.StatusPaymentRequired, map[string]any{"error": "payment_policy_mismatch", "detail": err.Error()})
		return
	}
	verifyResp, err := facilitatorVerify(r.Context(), &http.Client{Timeout: facilitatorVerifyTimeout}, cfg.FacilitatorURL, payloadBytes, signedReq)
	if err != nil {
		writeUnlockJSON(w, http.StatusPaymentRequired, map[string]any{"error": "payment_invalid", "detail": err.Error()})
		return
	}
	if !verifyResp.IsValid {
		writeUnlockJSON(w, http.StatusPaymentRequired, map[string]any{
			"error":  "payment_invalid",
			"detail": facilitatorDetail(verifyResp.InvalidReason, verifyResp.InvalidMessage),
		})
		return
	}

	settleResp, err := facilitatorSettle(r.Context(), &http.Client{Timeout: facilitatorSettleTimeout}, cfg.FacilitatorURL, payloadBytes, signedReq)
	if err != nil {
		// The facilitator can submit the settle tx on-chain and THEN fail on
		// the receipt path (facilitatorSettle returns the tx-bearing response
		// alongside the error). Surface the tx hash so a charged-but-unanswered
		// buyer can reconcile against the chain — same forensic contract as the
		// per-request paid path in HandleProxy. Without this the debit is silent.
		body := map[string]any{"error": "settle_failed", "detail": err.Error()}
		if v.surfaceOnChainSettle(w, settleResp) {
			body["hint"] = "the settle tx in X-PAYMENT-RESPONSE may have landed on-chain — verify against the chain before retrying, or you may pay twice"
		}
		writeUnlockJSON(w, http.StatusBadGateway, body)
		return
	}
	if !settleResp.Success {
		body := map[string]any{"error": "settle_failed", "detail": facilitatorDetail(settleResp.ErrorReason, settleResp.ErrorMessage)}
		if v.surfaceOnChainSettle(w, settleResp) {
			body["hint"] = "the settle tx in X-PAYMENT-RESPONSE may have landed on-chain — verify against the chain before retrying, or you may pay twice"
		}
		writeUnlockJSON(w, http.StatusBadGateway, body)
		return
	}

	v.recordFeeRevenue(req, uc.FeeRecipient, uc.MaxFeeBps)

	wallet := settleResp.Payer
	if wallet == "" {
		wallet = verifyResp.Payer
	}
	token := v.siwx.MintSession(wallet, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     SIWXSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(DefaultSIWXSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	r.Header.Set(HeaderVerifiedWallet, wallet)
	r.Header.Set(HeaderPaymentPayer, wallet)
	proxy, err := buildUpstreamProxy(rule)
	if err != nil {
		log.Printf("x402-verifier: build upstream proxy for %s/%s: %v", rule.OfferNamespace, rule.OfferName, err)
		writeUnlockJSON(w, http.StatusInternalServerError, map[string]any{"error": "upstream_unavailable"})
		return
	}
	proxy.ServeHTTP(&statusRecorder{ResponseWriter: w, status: http.StatusOK}, r)
}

// surfaceOnChainSettle sets X-PAYMENT-RESPONSE from a settle response that
// carries an on-chain tx hash (the facilitator submitted the tx then errored),
// so a charged buyer can reconcile. Must run before writeUnlockJSON commits the
// status. Returns true when a tx was surfaced. Mirrors the HandleProxy path.
func (v *Verifier) surfaceOnChainSettle(w http.ResponseWriter, settleResp *facilitatorSettleResponse) bool {
	if settleResp == nil || settleResp.Transaction == "" {
		return false
	}
	if encoded, err := json.Marshal(settleResp); err == nil {
		b64 := base64.StdEncoding.EncodeToString(encoded)
		w.Header().Set("X-PAYMENT-RESPONSE", b64)
		w.Header().Set("PAYMENT-RESPONSE", b64)
	}
	log.Printf("x402-verifier: auth-capture unlock settle returned tx %s with an error — verify on-chain (network=%s payer=%s)",
		settleResp.Transaction, settleResp.Network, settleResp.Payer)
	return true
}

func bigIntToFloat(x *big.Int) float64 {
	f, _ := new(big.Float).SetInt(x).Float64()
	return f
}

// exF64/exI64 coerce a JSON-decoded Extra value (numbers arrive as float64)
// to a concrete type; missing/wrong-typed keys yield the zero value.
// Used by validateSignedAuthCapture for deadline bounds.
func exF64(m map[string]interface{}, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func exI64(m map[string]interface{}, k string) int64 { return int64(exF64(m, k)) }

func facilitatorDetail(reason, message string) string {
	detail := strings.TrimSpace(strings.TrimSpace(reason) + " " + strings.TrimSpace(message))
	if detail == "" {
		return "facilitator rejected the request"
	}
	return detail
}

func writeUnlockJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("x402-verifier: encode auth-capture unlock response: %v", err)
	}
}
