package x402

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
	payload := x402types.PaymentPayloadV1{
		X402Version: 1,
		Scheme:      "exact",
		Network:     "base-sepolia",
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

func testRequirements() []x402types.PaymentRequirementsV1 {
	return []x402types.PaymentRequirementsV1{
		BuildV1Requirement(ChainBaseSepolia, "0.001", "0xWallet"),
	}
}

func TestForwardAuth_NoPayment_Returns402(t *testing.T) {
	var verifyCalled, settleCalled atomic.Int32
	fac := mockFacilitatorV1(true, true, &verifyCalled, &settleCalled)
	defer fac.Close()

	mw := NewForwardAuthMiddleware(ForwardAuthConfig{
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
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
	var body x402types.PaymentRequiredV1
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if len(body.Accepts) == 0 {
		t.Error("402 body has no accepts")
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
