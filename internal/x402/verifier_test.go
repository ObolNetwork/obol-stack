package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	x402types "github.com/coinbase/x402/go/types"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// ── Mock facilitator ────────────────────────────────────────────────────────

type mockFacilitatorOpts struct {
	rejectPayment bool
}

type mockFacilitator struct {
	*httptest.Server
	verifyCalls atomic.Int32
	settleCalls atomic.Int32
}

func newMockFacilitator(t *testing.T, opts mockFacilitatorOpts) *mockFacilitator {
	t.Helper()
	mf := &mockFacilitator{}

	mux := http.NewServeMux()

	mux.HandleFunc("/supported", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"kinds":[{"x402Version":2,"scheme":"exact","network":"eip155:84532"}]}`)
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
	p := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: ChainBaseSepolia.CAIP2Network,
			Amount:  "1000",
			Asset:   ChainBaseSepolia.USDCAddress,
			PayTo:   "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
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

// ── Per-route PayTo / Network override tests ─────────────────────────────────

// parse402Accepts is a test helper that decodes a 402 response body and returns
// the first PaymentRequirement from the "accepts" array.
func parse402Accepts(t *testing.T, body []byte) x402types.PaymentRequirements {
	t.Helper()
	var resp struct {
		Accepts []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode 402 body: %v\nbody: %s", err, string(body))
	}
	if len(resp.Accepts) == 0 {
		t.Fatal("402 response has empty accepts array")
	}
	return resp.Accepts[0]
}

func TestVerifier_PerRoutePayTo_UsesRouteWallet(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	globalWallet := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	routeWallet := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	v, err := NewVerifier(&PricingConfig{
		Wallet:         globalWallet,
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{{
			Pattern: "/services/test/*",
			Price:   "0.001",
			PayTo:   routeWallet,
		}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/test/foo")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	pr := parse402Accepts(t, body)

	if pr.PayTo != routeWallet {
		t.Errorf("payTo = %q, want route wallet %q", pr.PayTo, routeWallet)
	}
	if pr.PayTo == globalWallet {
		t.Error("payTo should NOT be the global wallet — per-route override was ignored")
	}
}

func TestVerifier_PerRouteNetwork_ResolvesCorrectChain(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{{
			Pattern: "/services/mainnet/*",
			Price:   "0.001",
			Network: "base",
		}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/mainnet/rpc")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	pr := parse402Accepts(t, body)

	// BaseMainnet.NetworkID is "base"; BaseSepolia.NetworkID is "base-sepolia".
	if pr.Network != ChainBaseMainnet.CAIP2Network {
		t.Errorf("network = %q, want %q (base mainnet)", pr.Network, ChainBaseMainnet.CAIP2Network)
	}
	if pr.Network == ChainBaseSepolia.CAIP2Network {
		t.Error("network should NOT be base-sepolia — per-route override was ignored")
	}
}

func TestVerifier_PerRoutePayTo_WithValidPayment(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	routeWallet := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{{
			Pattern: "/services/test/*",
			Price:   "0.001",
			PayTo:   routeWallet,
		}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/test/foo")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid payment on per-route PayTo, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("expected 1 verify call, got %d", fac.verifyCalls.Load())
	}
}

func TestVerifier_PerRouteNetwork_InvalidChain_RejectsAtLoad(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	_, err := NewVerifier(&PricingConfig{
		Wallet:         "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{{
			Pattern: "/services/bad/*",
			Price:   "0.001",
			Network: "invalid-chain",
		}},
	})
	if err == nil {
		t.Fatal("expected NewVerifier to reject invalid per-route chain at load time")
	}
	if !strings.Contains(err.Error(), "unsupported chain") {
		t.Errorf("expected 'unsupported chain' error, got: %v", err)
	}
}

func TestVerifier_NoPerRouteOverride_UsesGlobalWallet(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	globalWallet := "0xcccccccccccccccccccccccccccccccccccccccc"

	v, err := NewVerifier(&PricingConfig{
		Wallet:         globalWallet,
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{{
			Pattern: "/rpc/*",
			Price:   "0.0001",
			// No PayTo — should use global wallet.
		}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	pr := parse402Accepts(t, body)

	if pr.PayTo != globalWallet {
		t.Errorf("payTo = %q, want global wallet %q", pr.PayTo, globalWallet)
	}
}

func TestVerifier_MixedRoutes_CorrectOverrides(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	globalWallet := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	customWallet := "0xdddddddddddddddddddddddddddddddddddddd"

	v, err := NewVerifier(&PricingConfig{
		Wallet:         globalWallet,
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes: []RouteRule{
			{Pattern: "/rpc/*", Price: "0.0001"},                                 // no PayTo — uses global
			{Pattern: "/services/custom/*", Price: "0.005", PayTo: customWallet}, // per-route PayTo
		},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Route 1: /rpc/* — should use global wallet.
	req1 := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req1.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req1.Header.Set("X-Forwarded-Host", "obol.stack")
	w1 := httptest.NewRecorder()
	v.HandleVerify(w1, req1)

	if w1.Code != http.StatusPaymentRequired {
		t.Fatalf("rpc route: expected 402, got %d", w1.Code)
	}
	body1, _ := io.ReadAll(w1.Body)
	pr1 := parse402Accepts(t, body1)
	if pr1.PayTo != globalWallet {
		t.Errorf("rpc route: payTo = %q, want global wallet %q", pr1.PayTo, globalWallet)
	}

	// Route 2: /services/custom/* — should use custom wallet.
	req2 := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req2.Header.Set("X-Forwarded-Uri", "/services/custom/endpoint")
	req2.Header.Set("X-Forwarded-Host", "obol.stack")
	w2 := httptest.NewRecorder()
	v.HandleVerify(w2, req2)

	if w2.Code != http.StatusPaymentRequired {
		t.Fatalf("custom route: expected 402, got %d", w2.Code)
	}
	body2, _ := io.ReadAll(w2.Body)
	pr2 := parse402Accepts(t, body2)
	if pr2.PayTo != customWallet {
		t.Errorf("custom route: payTo = %q, want custom wallet %q", pr2.PayTo, customWallet)
	}
}

func TestVerifier_MetricsPaymentRequired(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:        "/rpc/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "paid-rpc",
	}})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	metrics := scrapeVerifierMetrics(t, v)
	labels := map[string]string{
		"route":           "/rpc/*",
		"offer_namespace": "llm",
		"offer_name":      "paid-rpc",
	}
	assertVerifierMetricValue(t, metrics["obol_x402_verifier_requests_total"], labels, 1)
	assertVerifierMetricValue(t, metrics["obol_x402_verifier_payment_required_total"], labels, 1)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_payment_verified_total"], labels)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_payment_failed_total"], labels)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_charged_requests_total"], labels)
}

func TestVerifier_MetricsVerifiedAndRejectedPayments(t *testing.T) {
	labels := map[string]string{
		"route":           "/rpc/*",
		"offer_namespace": "llm",
		"offer_name":      "paid-rpc",
	}

	okFac := newMockFacilitator(t, mockFacilitatorOpts{})
	okVerifier := newTestVerifier(t, okFac.URL, []RouteRule{{
		Pattern:        "/rpc/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "paid-rpc",
	}})

	okReq := httptest.NewRequest(http.MethodPost, "/verify", nil)
	okReq.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	okReq.Header.Set("X-Forwarded-Host", "obol.stack")
	okReq.Header.Set("X-PAYMENT", testPaymentHeader(t))
	okResp := httptest.NewRecorder()
	okVerifier.HandleVerify(okResp, okReq)
	if okResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", okResp.Code)
	}

	okMetrics := scrapeVerifierMetrics(t, okVerifier)
	assertVerifierMetricValue(t, okMetrics["obol_x402_verifier_requests_total"], labels, 1)
	assertVerifierMetricValue(t, okMetrics["obol_x402_verifier_payment_verified_total"], labels, 1)
	assertVerifierMetricValue(t, okMetrics["obol_x402_verifier_charged_requests_total"], labels, 1)
	assertVerifierMetricMissing(t, okMetrics["obol_x402_verifier_payment_required_total"], labels)
	assertVerifierMetricMissing(t, okMetrics["obol_x402_verifier_payment_failed_total"], labels)

	rejectFac := newMockFacilitator(t, mockFacilitatorOpts{rejectPayment: true})
	rejectVerifier := newTestVerifier(t, rejectFac.URL, []RouteRule{{
		Pattern:        "/rpc/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "paid-rpc",
	}})

	rejectReq := httptest.NewRequest(http.MethodPost, "/verify", nil)
	rejectReq.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	rejectReq.Header.Set("X-Forwarded-Host", "obol.stack")
	rejectReq.Header.Set("X-PAYMENT", testPaymentHeader(t))
	rejectResp := httptest.NewRecorder()
	rejectVerifier.HandleVerify(rejectResp, rejectReq)
	if rejectResp.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", rejectResp.Code)
	}

	rejectMetrics := scrapeVerifierMetrics(t, rejectVerifier)
	assertVerifierMetricValue(t, rejectMetrics["obol_x402_verifier_requests_total"], labels, 1)
	assertVerifierMetricValue(t, rejectMetrics["obol_x402_verifier_payment_failed_total"], labels, 1)
	assertVerifierMetricMissing(t, rejectMetrics["obol_x402_verifier_payment_required_total"], labels)
	assertVerifierMetricMissing(t, rejectMetrics["obol_x402_verifier_payment_verified_total"], labels)
	assertVerifierMetricMissing(t, rejectMetrics["obol_x402_verifier_charged_requests_total"], labels)
}

func scrapeVerifierMetrics(t *testing.T, v *Verifier) map[string]*dto.MetricFamily {
	t.Helper()

	rec := httptest.NewRecorder()
	v.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rec.Code)
	}

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}

	return families
}

func assertVerifierMetricValue(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string, wantValue float64) {
	t.Helper()

	if family == nil {
		t.Fatalf("missing metric family")
	}

	for _, metric := range family.GetMetric() {
		if verifierLabelsMatch(metric, wantLabels) {
			got := verifierMetricValue(metric)
			if got != wantValue {
				t.Fatalf("%s labels %v = %v, want %v", family.GetName(), wantLabels, got, wantValue)
			}
			return
		}
	}

	t.Fatalf("metric %s missing labels %v", family.GetName(), wantLabels)
}

func assertVerifierMetricMissing(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string) {
	t.Helper()

	if family == nil {
		return
	}

	for _, metric := range family.GetMetric() {
		if verifierLabelsMatch(metric, wantLabels) {
			t.Fatalf("metric %s unexpectedly contained labels %v", family.GetName(), wantLabels)
		}
	}
}

func verifierLabelsMatch(metric *dto.Metric, want map[string]string) bool {
	if len(metric.GetLabel()) != len(want) {
		return false
	}
	for _, label := range metric.GetLabel() {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}

func verifierMetricValue(metric *dto.Metric) float64 {
	switch {
	case metric.Counter != nil:
		return metric.GetCounter().GetValue()
	case metric.Gauge != nil:
		return metric.GetGauge().GetValue()
	default:
		return 0
	}
}
