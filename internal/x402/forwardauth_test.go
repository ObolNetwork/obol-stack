package x402

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

// mockFacilitator returns an httptest.Server that accepts /verify and /settle.
// verifyValid controls whether /verify returns isValid=true.
// settleOK controls whether /settle returns success=true.
// verifyCalled/settleCalled are incremented on each call.
func mockFacilitatorV1(verifyValid, settleOK bool, verifyCalled, settleCalled *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/verify":
			verifyCalled.Add(1)
			resp := facilitatorVerifyResponse{
				IsValid: verifyValid,
				Payer:   "0xPayer",
			}
			if !verifyValid {
				resp.InvalidReason = "test_invalid"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case "/settle":
			settleCalled.Add(1)
			resp := facilitatorSettleResponse{
				Success:     settleOK,
				Transaction: "0xTxHash",
				Network:     "base-sepolia",
				Payer:       "0xPayer",
			}
			if !settleOK {
				resp.ErrorReason = "test_settle_fail"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func validPaymentHeader() string {
	payload := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: "eip155:84532",
			Amount:  "1000",
			Asset:   ChainBaseSepolia.USDCAddress,
			PayTo:   "0xWallet",
		},
		Payload: map[string]interface{}{
			"signature": "0xSig",
			"authorization": map[string]interface{}{
				"from": "0xFrom", "to": "0xTo", "value": "1000",
				"validAfter": "0", "validBefore": "9999999999", "nonce": "0xNonce",
			},
		},
	}
	b, _ := json.Marshal(payload)
	return base64.StdEncoding.EncodeToString(b)
}

func testRequirements() []x402types.PaymentRequirements {
	return []x402types.PaymentRequirements{
		BuildV2Requirement(ChainBaseSepolia, "0.001", "0xWallet", 0),
	}
}

func TestForwardAuth_NoPayment_Returns402_AdvertisesExtensions(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
		Extensions: map[string]any{
			"eip2612GasSponsoring": map[string]any{},
		},
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}

	// Verify 402 body contains accepts array.
	var body x402types.PaymentRequired
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if len(body.Accepts) == 0 {
		t.Error("402 body has no accepts")
	}
	if _, ok := body.Extensions["eip2612GasSponsoring"]; !ok {
		t.Errorf("402 body extensions missing eip2612GasSponsoring, got %v", body.Extensions)
	}
	if verifyCalled.Load() != 0 {
		t.Error("facilitator.Verify should not be called when no payment header")
	}
}

func TestForwardAuth_ValidPayment_VerifyOnly(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	var innerCalled bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !innerCalled {
		t.Error("inner handler was not called")
	}
	if verifyCalled.Load() != 1 {
		t.Errorf("verify called %d times, want 1", verifyCalled.Load())
	}
	if settleCalled.Load() != 0 {
		t.Errorf("settle called %d times, want 0 (VerifyOnly=true)", settleCalled.Load())
	}
}

// TestForwardAuth_ValidPayment_PaymentSignatureHeader_V2 pins the x402 v2 wire
// fix: our 402 challenge advertises x402Version 2, so spec-compliant v2 buyers
// (agentcash, poncho, coinbase SDK >= v2) attach the payment under the
// PAYMENT-SIGNATURE header, not the legacy X-PAYMENT. Before the fix the verifier
// only read X-PAYMENT, so a v2 payment was silently ignored and re-challenged —
// no verify, no settle, no log. This asserts the v2 header is now honored.
func TestForwardAuth_ValidPayment_PaymentSignatureHeader_V2(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	var innerCalled bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("PAYMENT-SIGNATURE", validPaymentHeader()) // v2 header, no X-PAYMENT
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (v2 PAYMENT-SIGNATURE must be accepted)", rec.Code, http.StatusOK)
	}
	if !innerCalled {
		t.Error("inner handler was not called for a valid v2 PAYMENT-SIGNATURE payment")
	}
	if verifyCalled.Load() != 1 {
		t.Errorf("verify called %d times, want 1 (v2 header should reach the facilitator)", verifyCalled.Load())
	}
}

// TestForwardAuth_SettleOnSuccess_PaymentSignatureHeader_V2 asserts a v2 payment
// settles end-to-end and that the settlement receipt is mirrored onto BOTH the
// legacy X-PAYMENT-RESPONSE and the v2 PAYMENT-RESPONSE header so either wire
// version can read it.
func TestForwardAuth_SettleOnSuccess_PaymentSignatureHeader_V2(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     false,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("PAYMENT-SIGNATURE", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if settleCalled.Load() != 1 {
		t.Errorf("settle called %d times, want 1", settleCalled.Load())
	}
	// Both receipt headers must be present for cross-version clients.
	if rec.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Error("X-PAYMENT-RESPONSE header not set after settlement")
	}
	if rec.Header().Get("PAYMENT-RESPONSE") == "" {
		t.Error("PAYMENT-RESPONSE (v2) header not set after settlement")
	}
}

func TestForwardAuth_InvalidPayment_Returns402(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(false, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for invalid payment")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}
	if verifyCalled.Load() != 1 {
		t.Errorf("verify called %d times, want 1", verifyCalled.Load())
	}
}

// TestForwardAuth_MalformedPaymentHeader_StructuredJSON pins the structured
// error contract on the 400 path: an agent that mangles the base64 must get a
// machine-readable reason and a corrective hint, not an opaque text line.
func TestForwardAuth_MalformedPaymentHeader_StructuredJSON(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called with a malformed header")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", "%%%not-base64%%%")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body paymentErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (body %q)", err, rec.Body.String())
	}
	if body.Error != "Invalid payment header" {
		t.Errorf("error = %q, want the stable legacy phrase", body.Error)
	}
	if body.Reason != "invalid_payment_header" {
		t.Errorf("reason = %q, want invalid_payment_header", body.Reason)
	}
	if body.Hint == "" {
		t.Error("hint must tell the buyer what to do next")
	}
	if body.Retriable {
		t.Error("a malformed header is not retriable as-is")
	}
}

// TestForwardAuth_InvalidPayment_402CarriesFailureDetail pins the enriched
// re-issued challenge: when the facilitator rejects a payment, the 402 body
// must say why (error field) and carry a machine-readable copy in
// extensions.paymentFailure — the buyer already has the requirements; the
// rejection reason is the only new information that makes the retry succeed.
func TestForwardAuth_InvalidPayment_402CarriesFailureDetail(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(false, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for invalid payment")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
	var parsed x402types.PaymentRequired
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("402 body is not PaymentRequired JSON: %v", err)
	}
	if !strings.Contains(parsed.Error, "test_invalid") {
		t.Errorf("402 error = %q, must include the facilitator's invalidReason", parsed.Error)
	}
	failure, ok := parsed.Extensions["paymentFailure"].(map[string]any)
	if !ok {
		t.Fatalf("extensions.paymentFailure missing: %#v", parsed.Extensions)
	}
	if failure["reason"] != "payment_invalid" {
		t.Errorf("paymentFailure.reason = %v, want payment_invalid", failure["reason"])
	}
	if len(parsed.Accepts) == 0 {
		t.Error("the re-issued challenge must still carry accepts[] so the buyer can re-sign")
	}
}

// TestForwardAuth_SignatureRejection_HintsEIP712Domain pins the targeted
// signature hint: when the facilitator rejection mentions "signature", the
// seller must state the EIP-712 domain the buyer should have signed —
// wrong-domain signing is the top silent killer for external buyers and the
// seller is the only party that knows the right answer.
func TestForwardAuth_SignatureRejection_HintsEIP712Domain(t *testing.T) {
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(facilitatorVerifyResponse{
			IsValid:        false,
			InvalidReason:  "invalid_exact_evm_payload_signature",
			InvalidMessage: "FiatTokenV2: invalid signature",
		})
	}))
	defer fac.Close()

	reqs := testRequirements()
	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, reqs)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
	var parsed x402types.PaymentRequired
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("402 body is not PaymentRequired JSON: %v", err)
	}
	failure, ok := parsed.Extensions["paymentFailure"].(map[string]any)
	if !ok {
		t.Fatalf("extensions.paymentFailure missing: %#v", parsed.Extensions)
	}
	hint, _ := failure["hint"].(string)
	if !strings.Contains(hint, "EIP-712") {
		t.Errorf("hint = %q, must name the EIP-712 domain to sign", hint)
	}
	if wantName, _ := reqs[0].Extra["name"].(string); wantName != "" && !strings.Contains(hint, wantName) {
		t.Errorf("hint = %q, must include the domain name %q", hint, wantName)
	}
}

// TestForwardAuth_FacilitatorDown_StructuredRetriable503 pins the transient
// path: facilitator unreachable must produce a retriable JSON 503 so buying
// agents retry the identical request instead of re-signing (the auth was not
// consumed).
func TestForwardAuth_FacilitatorDown_StructuredRetriable503(t *testing.T) {
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	fac.Close() // deliberately down

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body paymentErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (body %q)", err, rec.Body.String())
	}
	if body.Error != "Payment verification failed" {
		t.Errorf("error = %q, want the stable legacy phrase (flows/lib.sh greps for it)", body.Error)
	}
	if body.Reason != "facilitator_unreachable" {
		t.Errorf("reason = %q, want facilitator_unreachable", body.Reason)
	}
	if !body.Retriable {
		t.Error("facilitator-down must be marked retriable")
	}
}

func TestForwardAuth_SettleOnSuccess(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     false,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if verifyCalled.Load() != 1 {
		t.Errorf("verify called %d times, want 1", verifyCalled.Load())
	}
	if settleCalled.Load() != 1 {
		t.Errorf("settle called %d times, want 1", settleCalled.Load())
	}

	// Check X-PAYMENT-RESPONSE header is set.
	if rec.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Error("X-PAYMENT-RESPONSE header not set after settlement")
	}

	// Check response body passes through.
	body, _ := io.ReadAll(rec.Body)
	if string(body) != `{"result":"ok"}` {
		t.Errorf("body = %q, want %q", string(body), `{"result":"ok"}`)
	}
}

func TestForwardAuth_SettleUsesLiveChainTimeoutBudget(t *testing.T) {
	origVerifyTimeout := facilitatorVerifyTimeout
	origSettleTimeout := facilitatorSettleTimeout
	facilitatorVerifyTimeout = 30 * time.Millisecond
	facilitatorSettleTimeout = 150 * time.Millisecond
	t.Cleanup(func() {
		facilitatorVerifyTimeout = origVerifyTimeout
		facilitatorSettleTimeout = origSettleTimeout
	})

	var verifyCalled, settleCalled atomic.Int32
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/verify":
			verifyCalled.Add(1)
			_, _ = w.Write([]byte(`{"isValid":true,"payer":"0xPayer"}`))
		case "/settle":
			settleCalled.Add(1)
			time.Sleep(75 * time.Millisecond)
			_, _ = w.Write([]byte(`{"success":true,"transaction":"0xTxHash","network":"base-sepolia","payer":"0xPayer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     false,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if verifyCalled.Load() != 1 {
		t.Fatalf("verify called %d times, want 1", verifyCalled.Load())
	}
	if settleCalled.Load() != 1 {
		t.Fatalf("settle called %d times, want 1", settleCalled.Load())
	}
	if rec.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("X-PAYMENT-RESPONSE header not set after delayed settlement")
	}
}

func TestForwardAuth_NoSettleOnHandlerError(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     false,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream error", http.StatusInternalServerError)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if settleCalled.Load() != 0 {
		t.Errorf("settle called %d times, want 0 (handler failed)", settleCalled.Load())
	}
}

func TestForwardAuth_NoSettleOnClientDisconnect(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     false,
	}, testRequirements())

	// Cancel after verify succeeds but before WriteHeader triggers
	// settlement. Cancelling before ServeHTTP aborts facilitator verify
	// (same r.Context()) so settleFunc never runs and verifyCalled stays 0.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // buyer disconnects after verify, before response commits
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req = req.WithContext(ctx)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if verifyCalled.Load() != 1 {
		t.Errorf("verify called %d times, want 1", verifyCalled.Load())
	}
	if settleCalled.Load() != 0 {
		t.Errorf("settle called %d times, want 0 (client disconnected)", settleCalled.Load())
	}
}

func TestForwardAuth_UpstreamAuthPropagation(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate what the verifier does: set Authorization for upstream.
		w.Header().Set("Authorization", "Bearer sk-litellm-key")
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// The Authorization header set by the inner handler should be in the response
	// (Traefik copies authResponseHeaders to the forwarded request).
	if got := rec.Header().Get("Authorization"); got != "Bearer sk-litellm-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-litellm-key")
	}
}

func TestForwardAuth_NoUpstreamAuth(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// No Authorization header should be set when inner handler doesn't set one.
	if got := rec.Header().Get("Authorization"); got != "" {
		t.Errorf("Authorization header = %q, want empty", got)
	}
}

// Obol's facilitator (and the Coinbase HTTP client) expect POST /verify JSON
// with paymentPayload as a JSON object. Sending the X-PAYMENT base64 string
// there produced invalidReason=unsupported_scheme.
func TestForwardAuth_FacilitatorVerifyBodyUsesJSONObjectPaymentPayload(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			http.NotFound(w, r)
			return
		}
		verifyCalled.Add(1)

		var envelope map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, ok := envelope["paymentPayload"]
		if !ok || len(raw) == 0 {
			http.Error(w, "missing paymentPayload", http.StatusBadRequest)
			return
		}
		if raw[0] != '{' {
			t.Errorf("facilitator /verify paymentPayload should be a JSON object, got first byte %q", raw[0])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"isValid":true,"payer":"0xPayer"}`))
	}))
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if verifyCalled.Load() != 1 {
		t.Fatalf("verify called %d times, want 1", verifyCalled.Load())
	}
	if settleCalled.Load() != 0 {
		t.Fatalf("settle called %d times, want 0", settleCalled.Load())
	}
}

// TestForwardAuth_VerifyOnlyFalse_EmitsStartupWarning asserts that constructing
// the middleware with VerifyOnly=false logs a loud warning. An operator who
// flips verifyOnly=false in x402-pricing.yaml may believe that enables real
// settlement under Traefik ForwardAuth — it does not, and the auth hop would
// debit the payer before the upstream serves the request. The warning is the
// only compile-time hook we have to surface this class of misconfiguration,
// so this test pins it down to prevent silent removal.
//
// Corresponds to W7 in the PR #343 review.
func TestForwardAuth_VerifyOnlyFalse_EmitsStartupWarning(t *testing.T) {
	var buf bytes.Buffer

	origFlags := log.Flags()
	origOutput := log.Writer()

	log.SetFlags(0)
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	})

	_ = NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: "http://example.invalid",
		VerifyOnly:     false,
	}, []x402types.PaymentRequirements{{
		Scheme:  "exact",
		Network: "eip155:84532",
	}})

	gotLog := buf.String()
	if !strings.Contains(gotLog, "verifyOnly=false") {
		t.Fatalf("expected verifyOnly=false warning in log output, got:\n%s", gotLog)
	}

	if !strings.Contains(gotLog, "WARNING") {
		t.Fatalf("expected WARNING level in log output, got:\n%s", gotLog)
	}
}

// TestForwardAuth_VerifyOnlyTrue_NoStartupWarning is the negative control:
// the sanctioned VerifyOnly=true path must NOT log the warning, otherwise
// operators would learn to filter it out and miss the unsafe case.
func TestForwardAuth_VerifyOnlyTrue_NoStartupWarning(t *testing.T) {
	var buf bytes.Buffer

	origFlags := log.Flags()
	origOutput := log.Writer()

	log.SetFlags(0)
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	})

	_ = NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: "http://example.invalid",
		VerifyOnly:     true,
	}, []x402types.PaymentRequirements{{
		Scheme:  "exact",
		Network: "eip155:84532",
	}})

	gotLog := buf.String()
	if strings.Contains(gotLog, "verifyOnly=false") {
		t.Fatalf("did not expect verifyOnly warning when VerifyOnly=true, got:\n%s", gotLog)
	}
}

// TestForwardAuth_SettleErrorPreservesTxHashInHeader pins the rc13-headline
// fix for the silent-money-loss class of bug: when the facilitator returns
// a 5xx AFTER successfully submitting the settle tx on-chain, the buyer
// must still see the tx hash so it can reconcile against the chain and
// flag the spent auth. Before this fix the verifier dropped the entire
// facilitator response on the floor when StatusCode != 200, the buyer
// released the held auth back into the pool, and 0.001 OBOL moved on
// mainnet with the user seeing only HTTP 503 "Payment settlement failed".
// See docs/observability.md ("Verify settlement against the chain").
func TestForwardAuth_SettleErrorPreservesTxHashInHeader(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/verify":
			verifyCalled.Add(1)
			_ = json.NewEncoder(w).Encode(facilitatorVerifyResponse{IsValid: true, Payer: "0xPayer"})
		case "/settle":
			settleCalled.Add(1)
			// Facilitator returns 500 — but the on-chain submission already
			// landed and the response carries the tx hash. This is the
			// rc13 mainnet OBOL incident (docs/observability.md).
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(facilitatorSettleResponse{
				Success:     false,
				ErrorReason: "unexpected_error",
				Transaction: "0xb5122d818a058e8bf529380260fa2584ba3d50bfc800f1e906faca34d3932307",
				Network:     "ethereum",
				Payer:       "0xPayer",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL:   fac.URL,
		VerifyOnly:       false,
		SettlesInProcess: true,
	}, testRequirements())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-PAYMENT", validPaymentHeader())
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (settle failed)", rec.Code)
	}
	gotHeader := rec.Header().Get("X-PAYMENT-RESPONSE")
	if gotHeader == "" {
		t.Fatal("X-PAYMENT-RESPONSE must be set even on settle error so the buyer can reconcile against the chain")
	}
	decoded, err := base64.StdEncoding.DecodeString(gotHeader)
	if err != nil {
		t.Fatalf("X-PAYMENT-RESPONSE not base64: %v", err)
	}
	var parsed facilitatorSettleResponse
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		t.Fatalf("X-PAYMENT-RESPONSE not JSON: %v", err)
	}
	const wantTx = "0xb5122d818a058e8bf529380260fa2584ba3d50bfc800f1e906faca34d3932307"
	if parsed.Transaction != wantTx {
		t.Errorf("Transaction in header = %q, want %q (the on-chain tx hash must survive the error path)", parsed.Transaction, wantTx)
	}
	if parsed.Success {
		t.Error("Success should remain false on the error path")
	}
}

// TestForwardAuth_SettlesInProcess_SuppressesWarning pins the fix for the
// per-request log spam on the in-process seller gateway (HandleProxy / obol
// sell inference): that path sets VerifyOnly=false BY DESIGN (it proxies to the
// real upstream and settles after a <400 response), so the verifyOnly=false
// warning is misleading there. SettlesInProcess=true must silence it while
// leaving the dangerous Traefik ForwardAuth path (SettlesInProcess=false) loud.
func TestForwardAuth_SettlesInProcess_SuppressesWarning(t *testing.T) {
	var buf bytes.Buffer

	origFlags := log.Flags()
	origOutput := log.Writer()

	log.SetFlags(0)
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	})

	_ = NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL:   "http://example.invalid",
		VerifyOnly:       false,
		SettlesInProcess: true,
	}, []x402types.PaymentRequirements{{
		Scheme:  "exact",
		Network: "eip155:84532",
	}})

	if gotLog := buf.String(); strings.Contains(gotLog, "verifyOnly=false") {
		t.Fatalf("SettlesInProcess=true must suppress the verifyOnly=false warning, got:\n%s", gotLog)
	}
}

// TestForwardAuth_402CarriesCatalogLinkHeader locks the discovery Link
// header on the default (JSON) 402 path through the middleware itself —
// both on the no-payment challenge and on the re-issued challenge after an
// invalid payment. Header-only: verification behaviour is asserted by the
// sibling tests and must not change.
func TestForwardAuth_402CarriesCatalogLinkHeader(t *testing.T) {
	const wantLink = `</api/services.json>; rel="catalog"`

	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(false, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
	}, testRequirements())
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called on a 402")
	})

	t.Run("no payment", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, req)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", rec.Code)
		}
		if got := rec.Header().Get("Link"); got != wantLink {
			t.Errorf("Link = %q, want %q", got, wantLink)
		}
	})

	t.Run("invalid payment rechallenge", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.Header.Set("X-PAYMENT", validPaymentHeader())
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, req)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", rec.Code)
		}
		if got := rec.Header().Get("Link"); got != wantLink {
			t.Errorf("Link = %q, want %q", got, wantLink)
		}
	})
}

func TestBuildResourceURL_Scheme(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		xfHost string
		xfp    string
		xfURI  string
		want   string
	}{
		// Public hosts default to https even when the TLS-terminating tunnel
		// forwards plaintext (XFP=http) and the route carries no
		// X-Forwarded-Proto filter — the shared-origin so-<name> case (#679).
		{"public host, no forwarded proto", "svc.example.org", "", "", "", "https://svc.example.org/services/x"},
		{"public host, xfp http from tunnel", "svc.example.org", "", "http", "", "https://svc.example.org/services/x"},
		{"forwarded public host", "10.42.0.5:8000", "svc.example.org", "", "", "https://svc.example.org/services/x"},
		{"local obol.stack stays http", "obol.stack:8080", "", "", "", "http://obol.stack:8080/services/x"},
		{"localhost stays http", "localhost:3000", "", "", "", "http://localhost:3000/services/x"},
		{"local host, explicit https honored", "obol.stack:8080", "", "https", "", "https://obol.stack:8080/services/x"},
		{"forwarded uri used", "svc.example.org", "", "", "/services/x/v1/chat", "https://svc.example.org/services/x/v1/chat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/services/x", nil)
			r.Host = tc.host
			if tc.xfHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.xfHost)
			}
			if tc.xfp != "" {
				r.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if tc.xfURI != "" {
				r.Header.Set("X-Forwarded-Uri", tc.xfURI)
			}
			if got := buildResourceURL(r); got != tc.want {
				t.Errorf("buildResourceURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
