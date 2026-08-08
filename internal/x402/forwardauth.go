package x402

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
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
	IsValid        bool   `json:"isValid"`
	InvalidReason  string `json:"invalidReason,omitempty"`
	InvalidMessage string `json:"invalidMessage,omitempty"`
	Payer          string `json:"payer,omitempty"`
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
			payloadPtr, err := x402types.ToPaymentPayload(payloadBytes)
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

			// Verify with facilitator.
			verifyResp, err := facilitatorVerify(r.Context(), verifyClient, cfg.FacilitatorURL, payloadBytes, matchedReq)
			if err != nil {
				log.Printf("x402: facilitator verify error: %v", err)
				reportFailure("facilitator_unreachable")
				writePaymentError(w, http.StatusServiceUnavailable, paymentErrorBody{
					Error:     "Payment verification failed",
					Reason:    "facilitator_unreachable",
					Hint:      "transient facilitator error — retry the identical request in a few seconds; the payment authorization was not consumed",
					Retriable: true,
				})
				return
			}

			if !verifyResp.IsValid {
				log.Printf("x402: payment invalid: %s", verifyResp.InvalidReason)
				detail := strings.TrimSpace(strings.TrimSpace(verifyResp.InvalidReason) + " " + strings.TrimSpace(verifyResp.InvalidMessage))
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
			interceptor := &settlementInterceptor{
				w: w,
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

					settleResp, err := facilitatorSettle(r.Context(), settleClient, cfg.FacilitatorURL, payloadBytes, matchedReq)
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
						// before http.Error commits the status code.
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
		})
	}
}

// sendPaymentRequiredJSON writes a 402 response with v2 payment requirements
// as a JSON body. This is the wire-level x402 contract that all buyer agents
// understand; it remains the default when ForwardAuthConfig.SendPaymentRequired
// is unset and the fallback when the renderer has nothing else to do.
func sendPaymentRequiredJSON(w http.ResponseWriter, r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) {
	setCatalogLinkHeader(w)
	resp := buildPaymentRequired(r, requirements, extensions)

	body, err := json.Marshal(resp)
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
// scheme and network.
func findMatchingRequirementV1(payment x402types.PaymentPayload, requirements []x402types.PaymentRequirements) (x402types.PaymentRequirements, bool) {
	for _, req := range requirements {
		if req.Scheme == payment.Accepted.Scheme &&
			req.Network == payment.Accepted.Network &&
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
		return nil, fmt.Errorf("payment payload is empty or not valid JSON")
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
		return nil, fmt.Errorf("facilitator verify: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read verify response: %w", err)
	}

	var verifyResp facilitatorVerifyResponse
	if err := json.Unmarshal(respBody, &verifyResp); err != nil {
		return nil, fmt.Errorf("facilitator verify (%d): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("facilitator verify failed (%d): %s", resp.StatusCode, verifyResp.InvalidReason)
	}

	return &verifyResp, nil
}

// facilitatorSettle calls POST /settle on the facilitator.
// paymentPayloadJSON is the decoded payment object (bytes of JSON), not the
// base64 X-PAYMENT wrapper.
func facilitatorSettle(ctx context.Context, client *http.Client, facilitatorURL string, paymentPayloadJSON []byte, requirement x402types.PaymentRequirements) (*facilitatorSettleResponse, error) {
	if len(paymentPayloadJSON) == 0 || !json.Valid(paymentPayloadJSON) {
		return nil, fmt.Errorf("payment payload is empty or not valid JSON")
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
		return nil, fmt.Errorf("facilitator settle: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read settle response: %w", err)
	}

	var settleResp facilitatorSettleResponse
	if err := json.Unmarshal(respBody, &settleResp); err != nil {
		return nil, fmt.Errorf("facilitator settle (%d): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		// Forensic-friendly: the facilitator can submit the settle tx on-chain
		// and then 5xx on the post-submit/receipt path. Return the parsed
		// response alongside the error so the caller can surface
		// settleResp.Transaction (the tx hash) to the buyer. Without this the
		// chain debit goes unnoticed — see docs/observability.md
		// ("Verify settlement against the chain, never the sidecar snapshot").
		return &settleResp, fmt.Errorf("facilitator settle failed (%d): %s", resp.StatusCode, settleResp.ErrorReason)
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
// Settlement runs only when the inner handler succeeds (status < 400).
// Faithfully ported from mark3labs/x402-go/http/middleware.go.
type settlementInterceptor struct {
	w          http.ResponseWriter
	settleFunc func() bool
	onFailure  func(statusCode int)
	committed  bool
	hijacked   bool
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

	return i.w.Write(b)
}

func (i *settlementInterceptor) WriteHeader(statusCode int) {
	if i.committed {
		return
	}
	i.committed = true

	// Handler error — pass through, no settlement.
	if statusCode >= 400 {
		if i.onFailure != nil {
			i.onFailure(statusCode)
		}
		i.w.WriteHeader(statusCode)
		return
	}

	// Handler success — settle before writing status.
	if !i.settleFunc() {
		i.hijacked = true
		return
	}

	i.w.WriteHeader(statusCode)
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
