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

// NewForwardAuthMiddleware creates an x402 payment-gating middleware compatible
// with the v1 wire format. It checks the X-PAYMENT header, verifies the payment
// with the facilitator, and optionally settles after a successful downstream
// response.
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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paymentHeader := r.Header.Get("X-PAYMENT")
			if paymentHeader == "" {
				send(w, r, requirements, cfg.Extensions)
				return
			}

			// Decode the base64-encoded payment payload.
			payloadBytes, err := base64.StdEncoding.DecodeString(paymentHeader)
			if err != nil {
				log.Printf("x402: invalid X-PAYMENT base64: %v", err)
				http.Error(w, "Invalid payment header", http.StatusBadRequest)
				return
			}

			// Find matching requirement by scheme+network.
			var payload x402types.PaymentPayload
			if err := json.Unmarshal(payloadBytes, &payload); err != nil {
				log.Printf("x402: invalid payment JSON: %v", err)
				http.Error(w, "Invalid payment header", http.StatusBadRequest)
				return
			}

			matchedReq, found := findMatchingRequirementV1(payload, requirements)
			if !found {
				send(w, r, requirements, cfg.Extensions)
				return
			}
			if cfg.OnPaymentMatched != nil {
				cfg.OnPaymentMatched(matchedReq)
			}

			// Verify with facilitator.
			verifyResp, err := facilitatorVerify(r.Context(), verifyClient, cfg.FacilitatorURL, payloadBytes, matchedReq)
			if err != nil {
				log.Printf("x402: facilitator verify error: %v", err)
				http.Error(w, "Payment verification failed", http.StatusServiceUnavailable)
				return
			}

			if !verifyResp.IsValid {
				log.Printf("x402: payment invalid: %s", verifyResp.InvalidReason)
				send(w, r, requirements, cfg.Extensions)
				return
			}

			// Payment verified — wrap with settlement interceptor.
			interceptor := &settlementInterceptor{
				w: w,
				settleFunc: func() bool {
					if cfg.VerifyOnly {
						return true
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
						if settleResp != nil && settleResp.Transaction != "" {
							settleJSON, _ := json.Marshal(settleResp)
							w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(settleJSON))
							log.Printf("x402: facilitator returned tx %s with the error — verify on-chain (network=%s payer=%s)",
								settleResp.Transaction, settleResp.Network, settleResp.Payer)
						}
						http.Error(w, "Payment settlement failed", http.StatusServiceUnavailable)
						return false
					}

					if !settleResp.Success {
						log.Printf("x402: settlement unsuccessful: %s", settleResp.ErrorReason)
						send(w, r, requirements, cfg.Extensions)
						return false
					}

					// Encode settlement response as X-PAYMENT-RESPONSE header.
					settleJSON, _ := json.Marshal(settleResp)
					w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(settleJSON))
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

// buildPaymentRequired assembles the v2 PaymentRequired object for the
// incoming request, including the bazaar service metadata on the resource
// block (serviceName/iconUrl — see specs/extensions/bazaar.md, soft-drop
// rules apply facilitator-side).
func buildPaymentRequired(r *http.Request, requirements []x402types.PaymentRequirements, extensions map[string]any) x402types.PaymentRequired {
	return x402types.PaymentRequired{
		X402Version: 2,
		Error:       "Payment required for this resource",
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
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	uri := r.RequestURI
	if forwardedURI := r.Header.Get("X-Forwarded-Uri"); forwardedURI != "" {
		uri = forwardedURI
	}
	return scheme + "://" + host + uri
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
