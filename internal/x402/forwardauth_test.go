package x402

import (
	"bytes"
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

	x402types "github.com/coinbase/x402/go/types"
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
