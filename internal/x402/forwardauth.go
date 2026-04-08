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

	x402types "github.com/coinbase/x402/go/types"
)

// ForwardAuthConfig configures the ForwardAuth x402 middleware.
type ForwardAuthConfig struct {
	// FacilitatorURL is the x402 facilitator service URL (e.g., "https://x402.org/facilitator").
	FacilitatorURL string

	// VerifyOnly skips blockchain settlement when true. Used by the Traefik
	// ForwardAuth verifier where only payment verification is needed.
	VerifyOnly bool
}

// facilitatorVerifyRequest is the JSON body sent to POST /verify and /settle.
type facilitatorVerifyRequest struct {
	X402Version         int                             `json:"x402Version"`
	PaymentPayload      json.RawMessage                 `json:"paymentPayload"`
	PaymentRequirements x402types.PaymentRequirementsV1 `json:"paymentRequirements"`
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

// NewForwardAuthMiddleware creates an x402 payment-gating middleware compatible
// with the v1 wire format. It checks the X-PAYMENT header, verifies the payment
// with the facilitator, and optionally settles after a successful downstream
// response.
//
// When VerifyOnly is true (Traefik ForwardAuth path), settlement is skipped.
// When VerifyOnly is false (standalone gateway path), settlement runs only
// after the inner handler returns a success status (< 400).
func NewForwardAuthMiddleware(cfg ForwardAuthConfig, requirements []x402types.PaymentRequirementsV1) func(http.Handler) http.Handler {
	client := &http.Client{Timeout: 30 * time.Second}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paymentHeader := r.Header.Get("X-PAYMENT")
			if paymentHeader == "" {
				sendPaymentRequiredV1(w, requirements)
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
			var payload x402types.PaymentPayloadV1
			if err := json.Unmarshal(payloadBytes, &payload); err != nil {
				log.Printf("x402: invalid payment JSON: %v", err)
				http.Error(w, "Invalid payment header", http.StatusBadRequest)
				return
			}

			matchedReq, found := findMatchingRequirementV1(payload, requirements)
			if !found {
				sendPaymentRequiredV1(w, requirements)
				return
			}

			// Verify with facilitator.
			verifyResp, err := facilitatorVerify(r.Context(), client, cfg.FacilitatorURL, payloadBytes, matchedReq)
			if err != nil {
				log.Printf("x402: facilitator verify error: %v", err)
				http.Error(w, "Payment verification failed", http.StatusServiceUnavailable)
				return
			}

			if !verifyResp.IsValid {
				log.Printf("x402: payment invalid: %s", verifyResp.InvalidReason)
				sendPaymentRequiredV1(w, requirements)
				return
			}

			// Payment verified — wrap with settlement interceptor.
			interceptor := &settlementInterceptor{
				w: w,
				settleFunc: func() bool {
					if cfg.VerifyOnly {
						return true
					}

					settleResp, err := facilitatorSettle(r.Context(), client, cfg.FacilitatorURL, payloadBytes, matchedReq)
					if err != nil {
						log.Printf("x402: settlement failed: %v", err)
						http.Error(w, "Payment settlement failed", http.StatusServiceUnavailable)
						return false
					}

					if !settleResp.Success {
						log.Printf("x402: settlement unsuccessful: %s", settleResp.ErrorReason)
						sendPaymentRequiredV1(w, requirements)
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

// sendPaymentRequiredV1 writes a 402 response with v1 payment requirements.
func sendPaymentRequiredV1(w http.ResponseWriter, requirements []x402types.PaymentRequirementsV1) {
	resp := x402types.PaymentRequiredV1{
		X402Version: 1,
		Error:       "Payment required for this resource",
		Accepts:     requirements,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(resp)
}

// findMatchingRequirementV1 finds the first requirement matching the payment's
// scheme and network.
func findMatchingRequirementV1(payment x402types.PaymentPayloadV1, requirements []x402types.PaymentRequirementsV1) (x402types.PaymentRequirementsV1, bool) {
	for _, req := range requirements {
		if req.Scheme == payment.Scheme && req.Network == payment.Network {
			return req, true
		}
	}
	return x402types.PaymentRequirementsV1{}, false
}

// facilitatorVerify calls POST /verify on the facilitator.
func facilitatorVerify(ctx context.Context, client *http.Client, facilitatorURL string, payloadBytes []byte, requirement x402types.PaymentRequirementsV1) (*facilitatorVerifyResponse, error) {
	body := facilitatorVerifyRequest{
		X402Version:         1,
		PaymentPayload:      json.RawMessage(payloadBytes),
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
func facilitatorSettle(ctx context.Context, client *http.Client, facilitatorURL string, payloadBytes []byte, requirement x402types.PaymentRequirementsV1) (*facilitatorSettleResponse, error) {
	body := facilitatorVerifyRequest{
		X402Version:         1,
		PaymentPayload:      json.RawMessage(payloadBytes),
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
		return nil, fmt.Errorf("facilitator settle failed (%d): %s", resp.StatusCode, settleResp.ErrorReason)
	}

	return &settleResp, nil
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
