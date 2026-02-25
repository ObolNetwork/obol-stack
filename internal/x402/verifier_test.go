package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	x402lib "github.com/mark3labs/x402-go"
)

// ── Mock facilitator ────────────────────────────────────────────────────────

type mockFacilitatorOpts struct {
	rejectPayment bool
}

type mockFacilitator struct {
	*httptest.Server
	verifyCalls  atomic.Int32
	settleCalls  atomic.Int32
}

func newMockFacilitator(t *testing.T, opts mockFacilitatorOpts) *mockFacilitator {
	t.Helper()
	mf := &mockFacilitator{}

	mux := http.NewServeMux()

	mux.HandleFunc("/supported", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"kinds":[{"x402Version":1,"scheme":"exact","network":"base-sepolia"}]}`)
	})

	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		mf.verifyCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if opts.rejectPayment {
			fmt.Fprintf(w, `{"isValid":false,"invalidReason":"mock rejection"}`)
			return
		}
		fmt.Fprintf(w, `{"isValid":true,"payer":"0xmockpayer"}`)
	})

	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		mf.settleCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"transaction":"0xmocktxhash","network":"base-sepolia"}`)
	})

	mf.Server = httptest.NewServer(mux)
	t.Cleanup(mf.Server.Close)
	return mf
}

// testPaymentHeader returns a base64-encoded x402 PaymentPayload for BaseSepolia.
func testPaymentHeader(t *testing.T) string {
	t.Helper()
	p := x402lib.PaymentPayload{
		X402Version: 1,
		Scheme:      "exact",
		Network:     x402lib.BaseSepolia.NetworkID,
		Payload: map[string]any{
			"signature": "0xmocksignature",
			"authorization": map[string]any{
				"from":        "0x1234567890123456789012345678901234567890",
				"to":          "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				"value":       "1000",
				"validAfter":  "0",
				"validBefore": "9999999999",
				"nonce":       "0xabcdef",
			},
		},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payment: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// newTestVerifier creates a Verifier backed by the given facilitator URL.
func newTestVerifier(t *testing.T, facilitatorURL string, routes []RouteRule) *Verifier {
	t.Helper()
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: facilitatorURL,
		VerifyOnly:     false,
		Routes:         routes,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestVerifier_NoForwardedURI_Returns403(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	// No X-Forwarded-Uri header — fail-closed.
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 without X-Forwarded-Uri, got %d", w.Code)
	}
}

func TestVerifier_FreeRoute_Returns200(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/health")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for free route, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Error("facilitator should not be called for free routes")
	}
}

func TestVerifier_PaidRoute_NoPayment_Returns402(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	// No X-PAYMENT header.
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 without payment, got %d", w.Code)
	}

	// Verify the response body contains payment requirements.
	body, _ := io.ReadAll(w.Body)
	if len(body) == 0 {
		t.Error("expected non-empty 402 response body with payment requirements")
	}
}

func TestVerifier_PaidRoute_ValidPayment_Returns200(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid payment, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("expected 1 verify call, got %d", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 1 {
		t.Errorf("expected 1 settle call, got %d", fac.settleCalls.Load())
	}
}

func TestVerifier_PaidRoute_RejectedPayment_Returns402(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{rejectPayment: true})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for rejected payment, got %d", w.Code)
	}
	if fac.settleCalls.Load() != 0 {
		t.Error("settle should not be called when verify fails")
	}
}

func TestVerifier_VerifyOnly_SkipsSettle(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("expected 1 verify call, got %d", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 0 {
		t.Errorf("expected 0 settle calls (verifyOnly), got %d", fac.settleCalls.Load())
	}
}

func TestVerifier_MultipleRoutes_CorrectMatching(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001", Description: "rpc"},
		{Pattern: "/inference-*/v1/*", Price: "0.001", Description: "inference"},
	})

	// RPC route — should trigger payment.
	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("rpc route: expected 402, got %d", w.Code)
	}

	// Inference route — should trigger payment.
	req2 := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req2.Header.Set("X-Forwarded-Uri", "/inference-prod/v1/chat/completions")
	req2.Header.Set("X-Forwarded-Host", "obol.stack")
	w2 := httptest.NewRecorder()
	v.HandleVerify(w2, req2)
	if w2.Code != http.StatusPaymentRequired {
		t.Errorf("inference route: expected 402, got %d", w2.Code)
	}

	// Frontend route — should be free.
	req3 := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req3.Header.Set("X-Forwarded-Uri", "/dashboard")
	w3 := httptest.NewRecorder()
	v.HandleVerify(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("frontend route: expected 200 (free), got %d", w3.Code)
	}
}

func TestVerifier_ConfigReload(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	// Initially /api/* is free.
	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/api/data")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("before reload: expected 200 for /api/data, got %d", w.Code)
	}

	// Reload with new config that gates /api/*.
	err := v.Reload(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{
			{Pattern: "/rpc/*", Price: "0.0001"},
			{Pattern: "/api/*", Price: "0.005"},
		},
	})
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Now /api/* should require payment.
	req2 := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req2.Header.Set("X-Forwarded-Uri", "/api/data")
	req2.Header.Set("X-Forwarded-Host", "obol.stack")
	w2 := httptest.NewRecorder()
	v.HandleVerify(w2, req2)
	if w2.Code != http.StatusPaymentRequired {
		t.Errorf("after reload: expected 402 for /api/data, got %d", w2.Code)
	}
}

func TestVerifier_Healthz(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	v.HandleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestVerifier_Readyz(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	v.HandleReadyz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestVerifier_InvalidChain(t *testing.T) {
	_, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeef",
		Chain:          "unsupported-chain",
		FacilitatorURL: "http://localhost:9999",
		Routes:         nil,
	})
	if err == nil {
		t.Error("expected error for unsupported chain")
	}
}

func TestVerifier_SetRegistration(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, nil)

	reg := &erc8004.AgentRegistration{
		Type:        erc8004.RegistrationType,
		Name:        "test-agent",
		Description: "A test agent",
		Services: []erc8004.ServiceDef{
			{Name: "web", Endpoint: "https://example.com"},
		},
		X402Support: true,
		Active:      true,
	}

	v.SetRegistration(reg)

	// HandleWellKnown should now return the registration.
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-registration.json", nil)
	w := httptest.NewRecorder()
	v.HandleWellKnown(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 after SetRegistration, got %d", w.Code)
	}

	var got erc8004.AgentRegistration
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "test-agent" {
		t.Errorf("name = %q, want test-agent", got.Name)
	}
	if !got.X402Support {
		t.Error("x402Support should be true")
	}
}

func TestVerifier_HandleWellKnown_NoRegistration(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, nil)

	// No SetRegistration called — should return 404.
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-registration.json", nil)
	w := httptest.NewRecorder()
	v.HandleWellKnown(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 without registration, got %d", w.Code)
	}
}

func TestVerifier_HandleWellKnown_JSON(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, nil)

	reg := &erc8004.AgentRegistration{
		Type:        erc8004.RegistrationType,
		Name:        "json-test",
		Description: "Testing JSON response",
		Services: []erc8004.ServiceDef{
			{Name: "web", Endpoint: "https://example.com", Version: "1.0"},
			{Name: "A2A", Endpoint: "https://example.com/a2a"},
		},
		X402Support: true,
		Active:      true,
		Registrations: []erc8004.OnChainReg{
			{AgentID: 42, AgentRegistry: "eip155:84532:0x8004A818"},
		},
	}

	v.SetRegistration(reg)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-registration.json", nil)
	w := httptest.NewRecorder()
	v.HandleWellKnown(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Check Content-Type.
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Verify the response is valid JSON with expected fields.
	body, _ := io.ReadAll(w.Body)
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if raw["type"] != erc8004.RegistrationType {
		t.Errorf("type = %v, want %s", raw["type"], erc8004.RegistrationType)
	}
	if raw["name"] != "json-test" {
		t.Errorf("name = %v, want json-test", raw["name"])
	}
	if raw["x402Support"] != true {
		t.Errorf("x402Support = %v, want true", raw["x402Support"])
	}
	if raw["active"] != true {
		t.Errorf("active = %v, want true", raw["active"])
	}

	services, ok := raw["services"].([]any)
	if !ok || len(services) != 2 {
		t.Fatalf("services count = %d, want 2", len(services))
	}
}

func TestVerifier_ReadyzNotReady(t *testing.T) {
	// Create a Verifier with a nil config pointer to test 503 response.
	v := &Verifier{}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	v.HandleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when config is nil, got %d", w.Code)
	}
}
