package x402

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// ForwardAuthConfig configures the ForwardAuth x402 middleware.
type ForwardAuthConfig struct {
	// FacilitatorURL is the x402 facilitator service URL (e.g., "https://x402.org/facilitator").
	FacilitatorURL string

	// VerifyOnly skips blockchain settlement when true. Used by the Traefik
	// ForwardAuth verifier where only payment verification is needed.
	//
	// INVARIANT: VerifyOnly MUST be true whenever this middleware is used
	// behind Traefik ForwardAuth. The auth hop runs before the upstream is
	// contacted and cannot observe the upstream's status; settling there
	// debits the payer before the upstream has proven it served the request.
	// VerifyOnly=false is only safe for in-process middleware (e.g. the
	// standalone inference gateway) that sees the real upstream status.
	//
	// NewForwardAuthMiddleware logs a loud warning when VerifyOnly is false
	// so operators who flip this in x402-pricing.yaml notice in logs.
	VerifyOnly bool

	// Extensions, if non-nil, is emitted as the top-level `extensions` field
	// on 402 responses. Used to advertise capabilities like
	// `eip2612GasSponsoring` (gasless Permit2 approve) so buyers take the
	// matching flow. See BuildExtensionsForAsset for how this is populated.
	Extensions map[string]any

	// SendPaymentRequired, if non-nil, replaces the default JSON 402 renderer.
	// The verifier injects NewHTMLAwarePaymentRequired here so browsers and
	// link-preview scrapers receive an HTML page (with OG metadata + copyable
	// "ways to pay" prompts) while x402-aware clients keep getting JSON.
	// Nil keeps today's behaviour: every 402 is JSON.
	SendPaymentRequired SendPaymentRequiredFunc

	// OnPaymentMatched, if non-nil, is invoked with the requirement the
	// buyer's X-PAYMENT satisfied, as soon as it matches (before verify).
	// Lets the caller attribute metrics to the specific payment option used
	// in a multi-accept offer (OBOL vs USDC, mainnet vs Base, …). No-op when
	// the offer advertises a single option.
	OnPaymentMatched func(x402types.PaymentRequirements)

	// OnPaymentFailure, if non-nil, is invoked once per payment-flow failure
	// with the machine-readable reason (the same string written into the
	// response body / extensions.paymentFailure). Lets the caller attribute
	// funnel-leak metrics per failure stage.
	OnPaymentFailure func(reason string)

	// OnPaymentVerified, if non-nil, is invoked with the payer address the
	// facilitator recovered from the verified payment, immediately before
	// the inner handler runs. The verifier uses it to propagate
	// X-Payment-Payer to the upstream (payment identity = the wallet that
	// may later read payer-gated results). Empty when the facilitator
	// response omits the payer.
	OnPaymentVerified func(payer string)

	// SettlesInProcess marks the in-process seller-gateway path (HandleProxy /
	// obol sell inference) where VerifyOnly=false is correct BY DESIGN — the
	// middleware proxies to the real upstream and settles only after a <400
	// response, so the verifyOnly=false warning would be misleading noise on
	// every paid request. When true, that warning is suppressed. It does NOT
	// change settlement behaviour; the genuinely-dangerous Traefik ForwardAuth
	// path leaves this false and still warns if an operator flips VerifyOnly.
	SettlesInProcess bool
}

// facilitatorVerifyRequest is the JSON body sent to POST /verify and /settle.
// PaymentPayload is the decoded v1/v2 payment JSON (same bytes as inside the
// base64 X-PAYMENT header). Facilitators including https://x402.gcp.obol.tech
// expect a JSON object here; sending a base64 string makes them return
// unsupported_scheme.
type facilitatorVerifyRequest struct {
	X402Version         int                           `json:"x402Version"`
	PaymentPayload      json.RawMessage               `json:"paymentPayload"`
	PaymentRequirements x402types.PaymentRequirements `json:"paymentRequirements"`
}

// facilitatorVerifyResponse is the JSON response from POST /verify.
type facilitatorVerifyResponse struct {
	IsValid              bool   `json:"isValid"`
	InvalidReason        string `json:"invalidReason,omitempty"`
	InvalidMessage       string `json:"invalidMessage,omitempty"`
	InvalidReasonDetails string `json:"invalidReasonDetails,omitempty"`
	Payer                string `json:"payer,omitempty"`
}

// facilitatorSettleResponse is the JSON response from POST /settle.
type facilitatorSettleResponse struct {
	Success      bool   `json:"success"`
	ErrorReason  string `json:"errorReason,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Transaction  string `json:"transaction"`
	Network      string `json:"network"`
	Payer        string `json:"payer,omitempty"`
}

var (
	facilitatorVerifyTimeout = 5 * time.Second
	facilitatorSettleTimeout = 60 * time.Second
)

// facilitatorOpError classifies failures talking to the facilitator so buyers
// see facilitator_unreachable only for real transport problems. A reachable
// facilitator that returns HTTP 500 unexpected_error (common when a client
// builds a bad voucher) used to be mislabeled "unreachable", which burned
// buyer LLM credits on useless identical retries.
type facilitatorOpError struct {
	Op         string // "verify" or "settle"
	Kind       string // "unreachable" | "rejected" | "bad_response"
	StatusCode int
	Reason     string // facilitator's invalidReason / errorReason when known
	err        error
}

func (e *facilitatorOpError) Error() string {
	if e == nil {
		return "facilitator error"
	}
	if e.err != nil {
		return e.err.Error()
	}
	if e.Reason != "" {
		return fmt.Sprintf("facilitator %s failed (%d): %s", e.Op, e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("facilitator %s failed (%d)", e.Op, e.StatusCode)
}

func (e *facilitatorOpError) Unwrap() error { return e.err }

func isTransportFacilitatorErr(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "TLS handshake") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "EOF")
}

// paymentErrorFromVerifyErr maps a facilitator verify failure onto the
// structured buyer-facing body. Transport → facilitator_unreachable
// (retriable); facilitator HTTP/body rejection → facilitator_error with the
// facilitator's reason in detail (usually not worth identical retries).
func paymentErrorFromVerifyErr(err error) paymentErrorBody {
	body := paymentErrorBody{
		Error:     "Payment verification failed",
		Reason:    "facilitator_unreachable",
		Hint:      "transient facilitator error — retry the identical request in a few seconds; the payment authorization was not consumed",
		Retriable: true,
	}
	var fe *facilitatorOpError
	if !errors.As(err, &fe) || fe == nil {
		if isTransportFacilitatorErr(err) {
			return body
		}
		// Non-typed unexpected errors (marshal, empty payload) — still not a
		// "down" facilitator, but keep the legacy reason for blind callers.
		body.Detail = strings.TrimSpace(err.Error())
		return body
	}
	switch fe.Kind {
	case "unreachable":
		return body
	case "rejected", "bad_response":
		detail := strings.TrimSpace(fe.Reason)
		if detail == "" && fe.StatusCode != 0 {
			detail = fmt.Sprintf("HTTP %d", fe.StatusCode)
		}
		hint := "seller facilitator reachable but rejected verification — payment authorization was not consumed"
		retriable := fe.StatusCode == http.StatusBadGateway ||
			fe.StatusCode == http.StatusServiceUnavailable ||
			fe.StatusCode == http.StatusGatewayTimeout
		if strings.Contains(fe.Reason, "unexpected_error") || fe.StatusCode == http.StatusInternalServerError {
			// Observed with Bankr /wallet/x402-pay against CAIP-2 Obol offers:
			// facilitator is up; voucher construction makes it 500. Identical
			// retries will not help and burn buyer LLM credits.
			retriable = false
			hint = "seller facilitator rejected this payment voucher (auth not consumed) — do not spam identical retries; rebuild the voucher from the 402 accepts[] entry verbatim (network/amount/asset/payTo) and re-sign"
			lower := strings.ToLower(detail)
			if strings.Contains(lower, "not yet valid") || strings.Contains(lower, "validafter") {
				hint = "authorization validAfter is too close to wall-clock now (auth not consumed) — sign with a past buffer (e.g. validAfter=0 or now-600s), as AgentCash does; Bankr auto-pay often sets validAfter=now"
			}
			if strings.Contains(lower, "invalid signature 'v'") || strings.Contains(lower, `invalid signature "v"`) {
				hint = "signature recovery id (v) rejected on-chain (auth not consumed) — v must be 27 or 28, not 0/1"
			}
		}
		return paymentErrorBody{
			Error:     "Payment verification failed",
			Reason:    "facilitator_error",
			Detail:    detail,
			Hint:      hint,
			Retriable: retriable,
		}
	default:
		return body
	}
}

// paymentErrorBody is the structured JSON body written on terminal
// payment-flow failures (malformed header, facilitator unreachable,
// settlement error). Buying agents retry blind when a failure is an opaque
// plain-text line; giving them a machine-readable reason plus a
// next-action hint converts a dead retry loop into a self-correcting one.
// The `error` field keeps the exact legacy phrases ("Invalid payment
// header", "Payment verification failed", "Payment settlement failed") so
// existing greps and log matchers keep working.
type paymentErrorBody struct {
	Error     string `json:"error"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Retriable bool   `json:"retriable"`
}

// writePaymentError emits a structured JSON error. Headers already set on w
// (e.g. X-PAYMENT-RESPONSE with a settle tx hash) are preserved.
func writePaymentError(w http.ResponseWriter, status int, body paymentErrorBody) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, body.Error, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n"))
}

// paymentFailure carries the facilitator's rejection detail from the
// middleware to the 402 renderer. The x402 contract on an invalid payment is
// to re-issue the full PaymentRequired challenge (so the buyer can re-probe
// and re-sign); without this the facilitator's invalidReason was logged
// server-side and the buyer saw only the generic challenge — no way to tell
// a wrong-domain signature from an expired auth.
type paymentFailure struct {
	Reason string // machine-readable, e.g. "payment_invalid", "settlement_rejected"
	Detail string // facilitator invalidReason/invalidMessage or errorReason
	Hint   string // buyer's next action
}

type paymentFailureCtxKey struct{}

func withPaymentFailure(r *http.Request, f paymentFailure) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), paymentFailureCtxKey{}, f))
}

func paymentFailureFrom(r *http.Request) (paymentFailure, bool) {
	f, ok := r.Context().Value(paymentFailureCtxKey{}).(paymentFailure)
	return f, ok
}

type facilitatorURLCtxKey struct{}

func withFacilitatorURL(r *http.Request, facilitatorURL string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), facilitatorURLCtxKey{}, facilitatorURL))
}

func facilitatorURLFrom(r *http.Request) string {
	url, _ := r.Context().Value(facilitatorURLCtxKey{}).(string)
	return url
}

// legacyCompatRequirements augments the canonical v2 accepts[] entries with a
// second copy using the chain's legacy alias ("base", "base-sepolia", ...)
// when the canonical network is CAIP-2. Bankr's docs and some hosted examples
// still teach the legacy names, so advertising both gives buyers a compatible
// option without dropping the standard CAIP-2 form other clients already use.
func legacyCompatRequirements(requirements []x402types.PaymentRequirements) []x402types.PaymentRequirements {
	if len(requirements) == 0 {
		return nil
	}
	out := make([]x402types.PaymentRequirements, 0, len(requirements)*2)
	for _, req := range requirements {
		out = append(out, req)
		chain, err := ResolveChainInfo(req.Network)
		if err != nil || chain.Name == "" || chain.Name == req.Network {
			continue
		}
		alias := req
		alias.Network = chain.Name
		out = append(out, alias)
	}
	return out
}

func paymentRequiredBody(r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) map[string]any {
	resp := buildPaymentRequired(r, legacyCompatRequirements(requirements), extensions)
	body := map[string]any{
		"x402Version": resp.X402Version,
		"error":       resp.Error,
		"resource":    resp.Resource,
		"accepts":     resp.Accepts,
	}
	if len(resp.Extensions) > 0 {
		body["extensions"] = resp.Extensions
	}
	if facilitator := facilitatorURLFrom(r); facilitator != "" && facilitatorURLSafeToDisclose(facilitator) {
		// Legacy compatibility for Bankr Cloud-style readers; harmless for
		// standards-compliant v2 clients, which ignore unknown top-level fields.
		body["facilitator"] = facilitator
	}
	return body
}

// facilitatorURLSafeToDisclose reports whether a facilitator URL is safe to
// echo into a PUBLIC, unauthenticated 402 response for legacy Bankr-Cloud-
// style readers. The production default (x402.gcp.obol.tech) is already
// documented in CLAUDE.md, so disclosing it costs nothing — but an operator
// running a private/internal facilitator (VPN-only address, RFC1918 IP,
// .internal/.local hostname) should not have that topology handed to every
// anonymous internet caller who hits an unpaid /services/* route.
func facilitatorURLSafeToDisclose(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func parsePaymentPayloadCompat(data []byte) (*x402types.PaymentPayload, error) {
	payload, err := x402types.ToPaymentPayload(data)
	if err == nil {
		return payload, nil
	}

	var raw struct {
		X402Version int                           `json:"x402Version"`
		Payload     map[string]interface{}        `json:"payload"`
		Accepted    x402types.PaymentRequirements `json:"accepted"`
		Resource    json.RawMessage               `json:"resource,omitempty"`
		Extensions  map[string]interface{}        `json:"extensions,omitempty"`
	}
	if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil || raw.X402Version != 2 || len(raw.Resource) == 0 {
		return nil, err
	}
	if raw.Resource[0] != '"' {
		return nil, err
	}
	var resourceURL string
	if jsonErr := json.Unmarshal(raw.Resource, &resourceURL); jsonErr != nil || resourceURL == "" {
		return nil, err
	}
	log.Printf("x402: accepted compat v2 payment payload with string resource url=%q", resourceURL)
	return &x402types.PaymentPayload{
		X402Version: raw.X402Version,
		Payload:     raw.Payload,
		Accepted:    raw.Accepted,
		Resource:    &x402types.ResourceInfo{URL: resourceURL},
		Extensions:  raw.Extensions,
	}, nil
}

func mapKeysSorted(m map[string]interface{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func valueShape(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("string(len=%d)", len(t))
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	case []any:
		return fmt.Sprintf("array(len=%d)", len(t))
	case map[string]any:
		return fmt.Sprintf("object(keys=%v)", mapKeysSorted(t))
	default:
		return fmt.Sprintf("%T", v)
	}
}

func paymentPayloadSummary(payload x402types.PaymentPayload) string {
	// Compact, signature-free diagnostic for failure paths only (no-match /
	// verify reject). Happy-path paid requests stay quiet so steady-state
	// logs are not flooded with buyer addresses and nonces.
	summary := map[string]any{
		"x402Version": payload.X402Version,
		"accepted": map[string]any{
			"scheme":  payload.Accepted.Scheme,
			"network": payload.Accepted.Network,
			"amount":  payload.Accepted.Amount,
			"asset":   payload.Accepted.Asset,
			"payTo":   payload.Accepted.PayTo,
		},
		"payloadKeys":    mapKeysSorted(payload.Payload),
		"extensionsKeys": mapKeysSorted(payload.Extensions),
	}
	if payload.Resource != nil {
		summary["resource"] = map[string]any{
			"url":         payload.Resource.URL,
			"serviceName": payload.Resource.ServiceName,
			"mimeType":    payload.Resource.MimeType,
		}
	}
	if auth, ok := payload.Payload["authorization"].(map[string]any); ok {
		shapes := make(map[string]any, len(auth))
		for k, v := range auth {
			shapes[k] = valueShape(v)
		}
		summary["authorizationShape"] = shapes
		// Safe non-secret fields — needed to diagnose Bankr validAfter=now
		// vs AgentCash validAfter=0 without dumping the signature.
		safeAuth := map[string]any{}
		for _, k := range []string{"from", "to", "value", "validAfter", "validBefore", "nonce"} {
			if v, ok := auth[k]; ok {
				safeAuth[k] = v
			}
		}
		summary["authorization"] = safeAuth
	}
	if len(payload.Accepted.Extra) > 0 {
		summary["acceptedExtraKeys"] = mapKeysSorted(payload.Accepted.Extra)
		if name, ok := payload.Accepted.Extra["name"]; ok {
			summary["acceptedExtraName"] = name
		}
		if version, ok := payload.Accepted.Extra["version"]; ok {
			summary["acceptedExtraVersion"] = version
		}
		if method, ok := payload.Accepted.Extra["assetTransferMethod"]; ok {
			summary["acceptedExtraMethod"] = method
		}
	}
	if sig, ok := payload.Payload["signature"].(string); ok {
		summary["signature"] = map[string]any{
			"has0xPrefix": strings.HasPrefix(sig, "0x"),
			"len":         len(sig),
			"vByte":       signatureVByte(sig),
		}
	}
	b, err := json.Marshal(summary)
	if err != nil {
		return sanitizeForLog(fmt.Sprintf("marshal_error=%v", err))
	}
	return sanitizeForLog(string(b))
}

func samePaymentNetwork(a, b string) bool {
	if a == b {
		return true
	}
	chainA, errA := ResolveChainInfo(a)
	chainB, errB := ResolveChainInfo(b)
	if errA != nil || errB != nil {
		return false
	}
	return chainA.CAIP2Network == chainB.CAIP2Network
}

func signatureVByte(sig string) any {
	s := strings.TrimPrefix(strings.TrimSpace(sig), "0x")
	if len(s) < 2 || len(s)%2 != 0 {
		return nil
	}
	b, err := hex.DecodeString(s[len(s)-2:])
	if err != nil || len(b) != 1 {
		return nil
	}
	return int(b[0])
}

// normalizePaymentPayloadForVerify rewrites known buyer-compat quirks that do
// not change the signed authorization message: ECDSA recovery-id v=0/1 →
// v=27/28. Bankr auto-pay has been observed to produce the former; on-chain
// FiatToken ECRecover expects 27/28 and otherwise returns unexpected_error
// ("invalid signature 'v' value"). Returns the original bytes when no change.
func normalizePaymentPayloadForVerify(paymentPayloadJSON []byte) ([]byte, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(paymentPayloadJSON, &raw); err != nil {
		return paymentPayloadJSON, ""
	}
	payloadRaw, ok := raw["payload"]
	if !ok {
		return paymentPayloadJSON, ""
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return paymentPayloadJSON, ""
	}
	sig, _ := payload["signature"].(string)
	v := signatureVByte(sig)
	vi, ok := v.(int)
	if !ok || (vi != 0 && vi != 1) {
		return paymentPayloadJSON, ""
	}
	s := strings.TrimPrefix(strings.TrimSpace(sig), "0x")
	prefix := ""
	if strings.HasPrefix(strings.TrimSpace(sig), "0x") {
		prefix = "0x"
	}
	payload["signature"] = prefix + s[:len(s)-2] + fmt.Sprintf("%02x", vi+27)
	newPayload, err := json.Marshal(payload)
	if err != nil {
		return paymentPayloadJSON, ""
	}
	raw["payload"] = newPayload
	out, err := json.Marshal(raw)
	if err != nil {
		return paymentPayloadJSON, ""
	}
	return out, sanitizeForLog(fmt.Sprintf("signature_v_%d_to_%d", vi, vi+27))
}

func facilitatorRejectDetail(resp *facilitatorVerifyResponse) string {
	if resp == nil {
		return ""
	}
	parts := []string{resp.InvalidReason, resp.InvalidMessage, resp.InvalidReasonDetails}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return sanitizeForLog(strings.Join(out, " — "))
}

// sanitizeForLog strips CR/LF from strings that originate outside the process
// — the buyer's payment payload or the facilitator's response — so a crafted
// value cannot forge extra lines in the operator's log. Applied at the
// producers rather than at each log call so future call sites inherit it.
func sanitizeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	return strings.ReplaceAll(s, "\r", "")
}

func truncateForLog(s string, max int) string {
	s = sanitizeForLog(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// signatureFailureHint returns a targeted hint when the facilitator rejection
// looks like a signature problem. The #1 silent killer for external buyers is
// signing the wrong EIP-712 domain for the asset; the seller is the only
// party that knows the right answer, so say it in the response instead of
// making the buyer guess.
func signatureFailureHint(detail string, req x402types.PaymentRequirements) string {
	if !strings.Contains(strings.ToLower(detail), "signature") {
		return ""
	}
	name, _ := req.Extra["name"].(string)
	version, _ := req.Extra["version"].(string)
	if name == "" && version == "" {
		return "signature rejected — re-sign using the EIP-712 domain advertised in accepts[].extra for this asset"
	}
	return fmt.Sprintf(
		"signature rejected — sign the EIP-712 domain advertised in accepts[].extra (name=%q version=%q) for asset %s on %s",
		name, version, req.Asset, req.Network,
	)
}

// NewForwardAuthMiddleware creates an x402 payment-gating middleware that accepts
// both x402 wire versions. It reads the payment from the X-PAYMENT (v1) or
// PAYMENT-SIGNATURE (v2) header, verifies the payment with the facilitator, and
// optionally settles after a successful downstream response.
//
// When VerifyOnly is true (Traefik ForwardAuth path), settlement is skipped.
// When VerifyOnly is false (standalone gateway path), settlement runs only
// after the inner handler returns a success status (< 400).
func NewForwardAuthMiddleware(cfg ForwardAuthConfig, requirements []x402types.PaymentRequirements) func(http.Handler) http.Handler {
	// Verification is a cheap signature check and should fail fast. Settlement
	// can wait on live-chain confirmation, so it gets a separate budget.
	verifyClient := &http.Client{Timeout: facilitatorVerifyTimeout}
	settleClient := &http.Client{Timeout: facilitatorSettleTimeout}

	if !cfg.VerifyOnly && !cfg.SettlesInProcess {
		log.Printf("x402: WARNING verifyOnly=false — settlement will run after upstream success. " +
			"This is ONLY safe for in-process middleware (e.g. obol sell inference) that sees " +
			"the real upstream status. Behind Traefik ForwardAuth this debits the payer before " +
			"the upstream serves the request. Set verifyOnly=true in x402-pricing.yaml for the " +
			"cluster verifier.")
	}

	send := cfg.SendPaymentRequired
	if send == nil {
		send = sendPaymentRequiredJSON
	}
	reportFailure := cfg.OnPaymentFailure
	if reportFailure == nil {
		reportFailure = func(string) {}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = withFacilitatorURL(r, cfg.FacilitatorURL)
			// x402 v1 clients send the payment under X-PAYMENT; x402 v2 clients
			// (agentcash, poncho, coinbase SDK >= v2) send it under PAYMENT-SIGNATURE.
			// Our 402 challenge advertises x402Version 2, so spec-compliant v2 buyers
			// use PAYMENT-SIGNATURE. Accept both — otherwise a v2 payment is silently
			// ignored and the caller is re-challenged with no way to pay.
			paymentHeader := r.Header.Get("X-PAYMENT")
			if paymentHeader == "" {
				paymentHeader = r.Header.Get("PAYMENT-SIGNATURE")
			}
			if paymentHeader == "" {
				send(w, r, requirements, cfg.Extensions)
				return
			}

			// Decode the base64-encoded payment payload.
			payloadBytes, err := base64.StdEncoding.DecodeString(paymentHeader)
			if err != nil {
				log.Printf("x402: invalid payment header base64: %v", err)
				reportFailure("invalid_payment_header")
				writePaymentError(w, http.StatusBadRequest, paymentErrorBody{
					Error:  "Invalid payment header",
					Reason: "invalid_payment_header",
					Hint:   "X-PAYMENT / PAYMENT-SIGNATURE must be the base64-encoded x402 PaymentPayload JSON — re-encode and retry the identical request",
				})
				return
			}

			// Decode via the canonical x402 types helper (handles both wire
			// versions) rather than a local json.Unmarshal, so the payload
			// envelope stays in lockstep with the SDK.
			payloadPtr, err := parsePaymentPayloadCompat(payloadBytes)
			if err != nil {
				log.Printf("x402: invalid payment payload: %v", err)
				reportFailure("invalid_payment_header")
				writePaymentError(w, http.StatusBadRequest, paymentErrorBody{
					Error:  "Invalid payment header",
					Reason: "invalid_payment_header",
					Hint:   "payment header decoded but is not valid PaymentPayload JSON — re-fetch the 402 requirements and re-sign",
				})
				return
			}
			payload := *payloadPtr

			matchedReq, found := findMatchingRequirementV1(payload, requirements)
			if !found {
				log.Printf("x402: no matching requirement; payment payload summary %s", paymentPayloadSummary(payload))
				reportFailure("no_matching_requirement")
				send(w, withPaymentFailure(r, paymentFailure{
					Reason: "no_matching_requirement",
					Detail: fmt.Sprintf("payment offered scheme=%q network=%q, which matches none of the accepts[] entries", payload.Accepted.Scheme, payload.Accepted.Network),
					Hint:   "sign against one accepts[] entry verbatim — scheme and network must match exactly",
				}), requirements, cfg.Extensions)
				return
			}
			if cfg.OnPaymentMatched != nil {
				cfg.OnPaymentMatched(matchedReq)
			}

			verifyPayload := payloadBytes
			if normalized, note := normalizePaymentPayloadForVerify(payloadBytes); note != "" {
				verifyPayload = normalized
				log.Printf("x402: normalized payment payload before verify (%s)", note)
			}

			// Verify with facilitator.
			verifyResp, err := facilitatorVerify(r.Context(), verifyClient, cfg.FacilitatorURL, verifyPayload, matchedReq)
			if err != nil {
				log.Printf("x402: facilitator verify error: %v (payment x402Version=%d scheme=%q network=%q amount=%q); payload summary %s",
					err, payload.X402Version, payload.Accepted.Scheme, payload.Accepted.Network, payload.Accepted.Amount, paymentPayloadSummary(payload))
				errBody := paymentErrorFromVerifyErr(err)
				reportFailure(errBody.Reason)
				writePaymentError(w, http.StatusServiceUnavailable, errBody)
				return
			}

			if !verifyResp.IsValid {
				detail := facilitatorRejectDetail(verifyResp)
				log.Printf("x402: payment invalid: %s; payload summary %s", detail, paymentPayloadSummary(payload))
				hint := signatureFailureHint(detail, matchedReq)
				reportFailure("payment_invalid")
				send(w, withPaymentFailure(r, paymentFailure{
					Reason: "payment_invalid",
					Detail: detail,
					Hint:   hint,
				}), requirements, cfg.Extensions)
				return
			}

			if cfg.OnPaymentVerified != nil {
				cfg.OnPaymentVerified(verifyResp.Payer)
			}

			// Payment verified — wrap with settlement interceptor.
			// SSE settles in finalize() only after the stream completes with
			// evidence the client received bytes (Write errors / empty body /
			// canceled context → skip). Non-SSE still settles on WriteHeader
			// so settle failures can return 503 before the body. Verify ≠ charge.
			var interceptor *settlementInterceptor
			interceptor = &settlementInterceptor{
				w:           w,
				deferSettle: !cfg.VerifyOnly,
				settleFunc: func() bool {
					if cfg.VerifyOnly {
						return true
					}

					// Buyer already gone (client disconnect propagated) —
					// don't take their money for a response nobody receives.
					if err := r.Context().Err(); err != nil {
						log.Printf("x402: buyer disconnected before settlement, skipping settlement: %v", err)
						reportFailure("client_disconnected")
						return false
					}

					// Settle must see the same (possibly recovery-id-normalized)
					// bytes that verify saw. Sending the original v=0/1 signature
					// here after verify accepted the v=27/28-normalized copy meant
					// the exact buyer population this normalization targets
					// (Bankr auto-pay) always failed settlement on-chain — the
					// seller served the request for free every time.
					settleResp, err := facilitatorSettle(r.Context(), settleClient, cfg.FacilitatorURL, verifyPayload, matchedReq)
					if err != nil {
						log.Printf("x402: settlement failed: %v", err)
						// Even on facilitator error, the on-chain submission
						// may have succeeded — the rc13 mainnet OBOL incident
						// burned 0.001 OBOL from a payer whose request 503'd
						// because the facilitator returned 500 *after* the
						// Permit2 settle tx had mined. If the parsed response
						// carries a tx hash, surface it via X-PAYMENT-RESPONSE
						// before erroring so the buyer (or operator) can
						// reconcile against the chain. The header has to land
						// before http.Error commits the status code — only
						// possible when the upstream body has not started.
						settledOnChain := false
						if settleResp != nil && settleResp.Transaction != "" {
							settledOnChain = true
							settleJSON, _ := json.Marshal(settleResp)
							encodedSettle := base64.StdEncoding.EncodeToString(settleJSON)
							w.Header().Set("X-PAYMENT-RESPONSE", encodedSettle)
							w.Header().Set("PAYMENT-RESPONSE", encodedSettle)
							log.Printf("x402: facilitator returned tx %s with the error — verify on-chain (network=%s payer=%s)",
								settleResp.Transaction, settleResp.Network, settleResp.Payer)
						}
						reportFailure("settlement_failed")
						if interceptor != nil && interceptor.wroteStatus {
							// Body/status already flushed (SSE deferred path) —
							// cannot flip the status to 503. Receipt trailers
							// (if any) are the only signal left.
							return false
						}
						hint := "transient facilitator error — retry the same request in a few seconds"
						if settledOnChain {
							hint = "the settle tx in X-PAYMENT-RESPONSE may have landed on-chain — verify against the chain before retrying, or you may pay twice"
						}
						writePaymentError(w, http.StatusServiceUnavailable, paymentErrorBody{
							Error:     "Payment settlement failed",
							Reason:    "settlement_failed",
							Hint:      hint,
							Retriable: !settledOnChain,
						})
						return false
					}

					if !settleResp.Success {
						log.Printf("x402: settlement unsuccessful: %s", settleResp.ErrorReason)
						reportFailure("settlement_rejected")
						if interceptor != nil && interceptor.wroteStatus {
							return false
						}
						detail := strings.TrimSpace(strings.TrimSpace(settleResp.ErrorReason) + " " + strings.TrimSpace(settleResp.ErrorMessage))
						send(w, withPaymentFailure(r, paymentFailure{
							Reason: "settlement_rejected",
							Detail: detail,
							Hint:   signatureFailureHint(detail, matchedReq),
						}), requirements, cfg.Extensions)
						return false
					}

					// Encode the settlement receipt. v1 clients read it from
					// X-PAYMENT-RESPONSE; x402 v2 clients read PAYMENT-RESPONSE.
					// Emit both so either wire version can confirm the settle.
					// When deferSettle already committed the upstream status,
					// these land as HTTP trailers (Trailer: declared below).
					settleJSON, _ := json.Marshal(settleResp)
					encodedSettle := base64.StdEncoding.EncodeToString(settleJSON)
					w.Header().Set("X-PAYMENT-RESPONSE", encodedSettle)
					w.Header().Set("PAYMENT-RESPONSE", encodedSettle)
					return true
				},
				onFailure: func(statusCode int) {
					log.Printf("x402: handler returned %d, skipping settlement", statusCode)
				},
			}

			next.ServeHTTP(interceptor, r)
			interceptor.finalize(r.Context())
		})
	}
}

// sendPaymentRequiredJSON writes a 402 response with v2 payment requirements
// as a JSON body. This is the wire-level x402 contract that all buyer agents
// understand; it remains the default when ForwardAuthConfig.SendPaymentRequired
// is unset and the fallback when the renderer has nothing else to do.
func sendPaymentRequiredJSON(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) {
	setCatalogLinkHeader(w)
	bodyMap := paymentRequiredBody(r, requirements, extensions)

	body, err := json.Marshal(bodyMap)
	if err != nil {
		http.Error(w, "Payment required", http.StatusPaymentRequired)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	setPaymentRequiredHeader(w, body)
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(body)
}

// setCatalogLinkHeader advertises the seller's machine-readable service
// catalog on every 402 response (RFC 8288 web linking). An agent that lands
// on a paid endpoint directly — with no prior knowledge of the seller's
// layout — can follow the link to /api/services.json and self-serve
// discovery of every other offer. Header-only addition: the 402 body schema
// and the verification/settlement flow are unchanged.
func setCatalogLinkHeader(w http.ResponseWriter) {
	w.Header().Set("Link", `</api/services.json>; rel="catalog"`)
}

// buildPaymentRequired assembles the v2 PaymentRequired object for the
// incoming request, including the bazaar service metadata on the resource
// block (serviceName/iconUrl — see specs/extensions/bazaar.md, soft-drop
// rules apply facilitator-side).
func buildPaymentRequired(r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) x402types.PaymentRequired {
	errMsg := "Payment required for this resource"

	// When the middleware rejected an attempted payment, say WHY in the
	// re-issued challenge. The buyer already holds these requirements; the
	// only new information that helps them succeed on the retry is the
	// rejection reason and the corrective hint. A machine-readable copy
	// rides in extensions.paymentFailure for agents.
	if failure, ok := paymentFailureFrom(r); ok {
		errMsg = "Payment invalid"
		if failure.Detail != "" {
			errMsg += ": " + failure.Detail
		}
		if failure.Hint != "" {
			errMsg += " — " + failure.Hint
		}
		failureExt := map[string]any{"reason": failure.Reason}
		if failure.Detail != "" {
			failureExt["detail"] = failure.Detail
		}
		if failure.Hint != "" {
			failureExt["hint"] = failure.Hint
		}
		merged := make(map[string]any, len(extensions)+1)
		for k, v := range extensions {
			merged[k] = v
		}
		merged["paymentFailure"] = failureExt
		extensions = merged
	}

	return x402types.PaymentRequired{
		X402Version: 2,
		Error:       errMsg,
		Resource: &x402types.ResourceInfo{
			URL:         buildResourceURL(r),
			Description: "Payment required for " + r.URL.Path,
			MimeType:    "application/json",
			ServiceName: ResourceServiceName,
			IconUrl:     ResourceIconURL,
		},
		Accepts:    requirements,
		Extensions: extensions,
	}
}

// setPaymentRequiredHeader writes the canonical x402 v2 HTTP transport
// location for the PaymentRequired object: a base64-encoded copy in the
// PAYMENT-REQUIRED response header (specs/transports-v2/http.md). The JSON
// body remains for the de-facto ecosystem (buy.py, x402scan, x402-buyer)
// that reads the body.
func setPaymentRequiredHeader(w http.ResponseWriter, paymentRequiredJSON []byte) {
	w.Header().Set("PAYMENT-REQUIRED", base64.StdEncoding.EncodeToString(paymentRequiredJSON))
}

// findMatchingRequirementV1 finds the first requirement matching the payment's
// scheme and network. samePaymentNetwork lets a buyer echo back a legacy
// chain alias (e.g. "base-sepolia") instead of our canonical CAIP-2 form —
// the matched requirement forwarded to the facilitator always keeps OUR
// canonical network, never the buyer's alias. Facilitator calls are tagged
// x402Version:2 throughout (see facilitatorVerifyRequest); echoing a v1-style
// alias into that request risked an unresolvable/misresolved network on a
// strict facilitator, for no benefit — the facilitator only needs to know
// which of our own accepted requirements this payment satisfies.
func findMatchingRequirementV1(payment x402types.PaymentPayload, requirements []x402types.PaymentRequirements) (x402types.PaymentRequirements, bool) {
	for _, req := range requirements {
		if req.Scheme == payment.Accepted.Scheme &&
			samePaymentNetwork(req.Network, payment.Accepted.Network) &&
			req.Amount == payment.Accepted.Amount &&
			req.Asset == payment.Accepted.Asset &&
			req.PayTo == payment.Accepted.PayTo {
			return req, true
		}
	}
	return x402types.PaymentRequirements{}, false
}

// facilitatorVerify calls POST /verify on the facilitator.
// paymentPayloadJSON is the decoded payment object (bytes of JSON), not the
// base64 X-PAYMENT wrapper.
func facilitatorVerify(ctx context.Context, client *http.Client, facilitatorURL string, paymentPayloadJSON []byte, requirement x402types.PaymentRequirements) (*facilitatorVerifyResponse, error) {
	if len(paymentPayloadJSON) == 0 || !json.Valid(paymentPayloadJSON) {
		return nil, &facilitatorOpError{
			Op:     "verify",
			Kind:   "bad_response",
			Reason: "empty_or_invalid_payment_payload",
			err:    fmt.Errorf("payment payload is empty or not valid JSON"),
		}
	}

	body := facilitatorVerifyRequest{
		X402Version:         2,
		PaymentPayload:      json.RawMessage(paymentPayloadJSON),
		PaymentRequirements: requirement,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal verify request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", facilitatorURL+"/verify", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, &facilitatorOpError{
			Op:   "verify",
			Kind: "unreachable",
			err:  fmt.Errorf("facilitator verify: %w", err),
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &facilitatorOpError{
			Op:         "verify",
			Kind:       "unreachable",
			StatusCode: resp.StatusCode,
			err:        fmt.Errorf("read verify response: %w", err),
		}
	}

	var verifyResp facilitatorVerifyResponse
	if err := json.Unmarshal(respBody, &verifyResp); err != nil {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, &facilitatorOpError{
			Op:         "verify",
			Kind:       "bad_response",
			StatusCode: resp.StatusCode,
			Reason:     "non_json_response",
			err:        fmt.Errorf("facilitator verify (%d): %s", resp.StatusCode, snippet),
		}
	}

	if resp.StatusCode != http.StatusOK {
		detail := facilitatorRejectDetail(&verifyResp)
		log.Printf("x402: facilitator /verify rejected status=%d body=%s", resp.StatusCode, truncateForLog(string(respBody), 500))
		return nil, &facilitatorOpError{
			Op:         "verify",
			Kind:       "rejected",
			StatusCode: resp.StatusCode,
			Reason:     detail,
			err:        fmt.Errorf("facilitator verify failed (%d): %s", resp.StatusCode, detail),
		}
	}

	return &verifyResp, nil
}

// facilitatorSettle calls POST /settle on the facilitator.
// paymentPayloadJSON is the decoded payment object (bytes of JSON), not the
// base64 X-PAYMENT wrapper.
func facilitatorSettle(ctx context.Context, client *http.Client, facilitatorURL string, paymentPayloadJSON []byte, requirement x402types.PaymentRequirements) (*facilitatorSettleResponse, error) {
	if len(paymentPayloadJSON) == 0 || !json.Valid(paymentPayloadJSON) {
		return nil, &facilitatorOpError{
			Op:     "settle",
			Kind:   "bad_response",
			Reason: "empty_or_invalid_payment_payload",
			err:    fmt.Errorf("payment payload is empty or not valid JSON"),
		}
	}

	body := facilitatorVerifyRequest{
		X402Version:         2,
		PaymentPayload:      json.RawMessage(paymentPayloadJSON),
		PaymentRequirements: requirement,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal settle request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", facilitatorURL+"/settle", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create settle request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, &facilitatorOpError{
			Op:   "settle",
			Kind: "unreachable",
			err:  fmt.Errorf("facilitator settle: %w", err),
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &facilitatorOpError{
			Op:         "settle",
			Kind:       "unreachable",
			StatusCode: resp.StatusCode,
			err:        fmt.Errorf("read settle response: %w", err),
		}
	}

	var settleResp facilitatorSettleResponse
	if err := json.Unmarshal(respBody, &settleResp); err != nil {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, &facilitatorOpError{
			Op:         "settle",
			Kind:       "bad_response",
			StatusCode: resp.StatusCode,
			Reason:     "non_json_response",
			err:        fmt.Errorf("facilitator settle (%d): %s", resp.StatusCode, snippet),
		}
	}

	if resp.StatusCode != http.StatusOK {
		// Forensic-friendly: the facilitator can submit the settle tx on-chain
		// and then 5xx on the post-submit/receipt path. Return the parsed
		// response alongside the error so the caller can surface
		// settleResp.Transaction (the tx hash) to the buyer. Without this the
		// chain debit goes unnoticed — see docs/observability.md
		// ("Verify settlement against the chain, never the sidecar snapshot").
		return &settleResp, &facilitatorOpError{
			Op:         "settle",
			Kind:       "rejected",
			StatusCode: resp.StatusCode,
			Reason:     settleResp.ErrorReason,
			err:        fmt.Errorf("facilitator settle failed (%d): %s", resp.StatusCode, settleResp.ErrorReason),
		}
	}

	return &settleResp, nil
}

func buildResourceURL(r *http.Request) string {
	// Scheme matches resolveSiteURL: default https for public hosts so a
	// Cloudflare-terminated tunnel that forwards plaintext (X-Forwarded-Proto
	// often "http") still advertises https:// resource URLs that match what
	// discovery and x402scan probe.
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" && isLocalHost(host) {
		scheme = "http"
	}

	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path = path + "?" + r.URL.RawQuery
	}
	if forwardedURI := r.Header.Get("X-Forwarded-Uri"); forwardedURI != "" {
		path = forwardedURI
	}

	// Dedicated-origin offers rewrite /public → /services/<name>/public before
	// the verifier matches. Challenge resource.URL must use the public path
	// the buyer/discovery saw, not the internal rewritten path.
	if rule := routeRuleFrom(r.Context()); rule != nil && rule.Hostname != "" {
		if strings.EqualFold(stripHostPort(host), stripHostPort(rule.Hostname)) {
			rawPath := r.URL.Path
			if forwardedURI := r.Header.Get("X-Forwarded-Uri"); forwardedURI != "" {
				if u, err := parsePathOnly(forwardedURI); err == nil {
					rawPath = u
				}
			}
			pub := publicPrefix(rule, host) + stripRoutePrefix(rule.StripPrefix, rawPath)
			if r.URL.RawQuery != "" && !strings.Contains(pub, "?") {
				pub += "?" + r.URL.RawQuery
			}
			path = pub
		}
	}

	return scheme + "://" + host + path
}

func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// parsePathOnly returns the path (and optional query) from a URI that may be
// path-only (/foo) or absolute (https://h/foo?q=1).
func parsePathOnly(uri string) (string, error) {
	if strings.HasPrefix(uri, "/") {
		return uri, nil
	}
	// Absolute form — rare for X-Forwarded-Uri; fall back to as-is path parse.
	if i := strings.Index(uri, "://"); i >= 0 {
		rest := uri[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[j:], nil
		}
	}
	return uri, nil
}

// settlementInterceptor wraps a ResponseWriter to intercept the status code.
//
// Settlement policy (deferSettle=true on the seller HandleProxy path):
//   - text/event-stream (SSE): settle in finalize() ONLY after the stream
//     completes. Skip /settle when the client context is canceled, a body
//     Write fails (broken pipe — Cloudflare/Bankr timeouts often do NOT
//     cancel r.Context), or zero body bytes were written. Receipt rides
//     HTTP trailers.
//   - other responses: settle eagerly on WriteHeader(<400) so a settle
//     failure can still flip the status to 503 before the body starts
//     (in-process sell-inference path).
//
// deferSettle=false keeps the legacy eager path for all content types.
type settlementInterceptor struct {
	w              http.ResponseWriter
	settleFunc     func() bool
	onFailure      func(statusCode int)
	deferSettle    bool
	streamDefer    bool // SSE path: wait for finalize()
	status         int
	committed      bool // WriteHeader entered (guards re-entry)
	wroteStatus    bool // status already sent to the client
	settled        bool
	hijacked       bool
	bytesWritten   int
	clientWriteErr error
}

func (i *settlementInterceptor) Header() http.Header {
	return i.w.Header()
}

func (i *settlementInterceptor) Write(b []byte) (int, error) {
	if !i.committed {
		i.WriteHeader(http.StatusOK)
	}

	if i.hijacked {
		return len(b), nil
	}

	n, err := i.w.Write(b)
	i.bytesWritten += n
	if err != nil && i.clientWriteErr == nil {
		i.clientWriteErr = err
	}
	return n, err
}

func (i *settlementInterceptor) WriteHeader(statusCode int) {
	if i.committed {
		return
	}
	i.committed = true
	i.status = statusCode

	// Handler error — pass through, no settlement.
	if statusCode >= 400 {
		if i.onFailure != nil {
			i.onFailure(statusCode)
		}
		i.wroteStatus = true
		i.w.WriteHeader(statusCode)
		return
	}

	// SSE: defer settlement until finalize so a Bankr/client timeout cannot
	// debit the buyer after upstream finishes behind a dead connection.
	if i.deferSettle {
		ct := i.w.Header().Get("Content-Type")
		if strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
			i.streamDefer = true
			i.w.Header().Set("Trailer", "X-PAYMENT-RESPONSE, PAYMENT-RESPONSE")
			i.wroteStatus = true
			i.w.WriteHeader(statusCode)
			return
		}
	}

	// Non-SSE (and legacy eager path): settle BEFORE committing status to
	// the client so settle failures can still return 503 with
	// X-PAYMENT-RESPONSE. wroteStatus stays false during settleFunc.
	if !i.settleFunc() {
		i.hijacked = true
		return
	}
	i.settled = true
	i.wroteStatus = true
	i.w.WriteHeader(statusCode)
}

// finalize runs after the upstream handler returns. For SSE (streamDefer)
// this is the only settlement point: success + evidence the client received
// bytes → settle; cancel / write error / empty body → skip.
func (i *settlementInterceptor) finalize(ctx context.Context) {
	if i.settled || i.hijacked || !i.streamDefer {
		return
	}
	if i.status == 0 || i.status >= 400 {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		log.Printf("x402: buyer disconnected before settlement, skipping settlement: %v", ctx.Err())
		if i.onFailure != nil {
			i.onFailure(i.status)
		}
		return
	}
	// Cloudflare / Bankr timeouts often do NOT cancel r.Context. A failed
	// Write to the client is the signal that nobody received the body —
	// settling here is the "zombie settlement" bug (charged, UI shows failure).
	if i.clientWriteErr != nil {
		log.Printf("x402: client write failed before settlement, skipping settlement: %v (bytesWritten=%d)", i.clientWriteErr, i.bytesWritten)
		if i.onFailure != nil {
			i.onFailure(i.status)
		}
		return
	}
	if i.bytesWritten == 0 {
		log.Printf("x402: no response body reached the client before settlement, skipping settlement")
		if i.onFailure != nil {
			i.onFailure(i.status)
		}
		return
	}
	if i.settleFunc == nil {
		return
	}
	if i.settleFunc() {
		i.settled = true
	}
}

func (i *settlementInterceptor) Flush() {
	if flusher, ok := i.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (i *settlementInterceptor) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := i.w.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}
