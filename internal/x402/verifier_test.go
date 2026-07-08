package x402

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	x402types "github.com/x402-foundation/x402/go/v2/types"
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
	return testPaymentHeaderFor(t,
		"0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"100",
	)
}

func testPaymentHeaderFor(t *testing.T, payTo, amount string) string {
	t.Helper()
	p := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: ChainBaseSepolia.CAIP2Network,
			Amount:  amount,
			Asset:   ChainBaseSepolia.USDCAddress,
			PayTo:   payTo,
		},
		Payload: map[string]any{
			"signature": "0xmocksignature",
			"authorization": map[string]any{
				"from":        "0x1234567890123456789012345678901234567890",
				"to":          payTo,
				"value":       amount,
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
// It also marks routes as loaded so /readyz returns 200 immediately, which
// matches what the production wire-up does once the route source warms up.
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
	v.MarkRoutesLoaded()
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

// TestVerifier_MultiPayment_AdvertisesAllAndSettlesChosen pins the Phase-1
// multi-currency behaviour: a route with two accepted payment options
// advertises BOTH in the 402 accepts[] array, and a buyer paying with the
// SECOND (non-primary) option verifies + settles against that option rather
// than silently being matched to the primary.
func TestVerifier_MultiPayment_AdvertisesAllAndSettlesChosen(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	const (
		payToPrimary = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		payToSecond  = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	route := RouteRule{
		Pattern:        "/services/multi/*",
		OfferNamespace: "demo",
		OfferName:      "multi",
		Payments: []RoutePayment{
			{Price: "0.001", PayTo: payToPrimary, Network: "base-sepolia", AssetSymbol: "USDC"},
			{Price: "0.002", PayTo: payToSecond, Network: "base-sepolia", AssetSymbol: "USDC"},
		},
	}

	// (1) No payment → 402 with BOTH options in accepts[].
	v := newTestVerifier(t, fac.URL, []RouteRule{route})
	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/multi/run")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}
	var got x402types.PaymentRequired
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse 402 body: %v (body=%s)", err, w.Body.String())
	}
	if len(got.Accepts) != 2 {
		t.Fatalf("accepts length = %d, want 2 (multi-currency offer)", len(got.Accepts))
	}
	amounts := map[string]string{got.Accepts[0].PayTo: got.Accepts[0].Amount, got.Accepts[1].PayTo: got.Accepts[1].Amount}
	if amounts[payToPrimary] != "1000" || amounts[payToSecond] != "2000" {
		t.Fatalf("accepts amounts = %v, want primary→1000 secondary→2000", amounts)
	}

	// (2) Pay with the SECOND option → settles. The buyer's X-PAYMENT carries
	// option 2's payTo + atomic amount; findMatchingRequirementV1 must select
	// it and the facilitator settle against it.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	route.UpstreamURL = upstream.URL
	route.StripPrefix = "/services/multi"
	vp := newTestVerifier(t, fac.URL, []RouteRule{route})

	preq := httptest.NewRequest(http.MethodPost, "/services/multi/run", strings.NewReader(`{}`))
	preq.Header.Set("Content-Type", "application/json")
	preq.Header.Set("X-PAYMENT", testPaymentHeaderFor(t, payToSecond, "2000"))
	pw := httptest.NewRecorder()
	vp.HandleProxy(pw, preq)

	if pw.Code != http.StatusOK {
		t.Fatalf("paying with second option: expected 200, got %d (body=%s)", pw.Code, pw.Body.String())
	}
	if fac.settleCalls.Load() == 0 {
		t.Fatal("expected the chosen (second) option to settle")
	}
	if pw.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("expected X-PAYMENT-RESPONSE on successful settlement of the chosen option")
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

func TestVerifier_HandleProxy_NoPayment_Returns402(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called without payment")
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.001",
		UpstreamURL: upstream.URL,
		StripPrefix: "/services/demo",
	}})

	req := httptest.NewRequest(http.MethodPost, "/services/demo/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Fatalf("verify should not be called without payment, got %d", fac.verifyCalls.Load())
	}
}

func TestVerifier_HandleProxy_ValidPayment_SettlesAndStripsPrefix(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var seenPath, seenAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:      "/services/demo/*",
		Price:        "0.0001",
		UpstreamURL:  upstream.URL,
		StripPrefix:  "/services/demo",
		UpstreamAuth: "Bearer sk-upstream",
	}})

	req := httptest.NewRequest(http.MethodPost, "/services/demo/v1/chat/completions", strings.NewReader(`{"model":"qwen3.5:9b"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"ok":true}` {
		t.Fatalf("body = %q, want %q", body, `{"ok":true}`)
	}
	if seenPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", seenPath)
	}
	if seenAuth != "Bearer sk-upstream" {
		t.Fatalf("upstream auth = %q, want Bearer sk-upstream", seenAuth)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Fatalf("verify calls = %d, want 1", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 1 {
		t.Fatalf("settle calls = %d, want 1", fac.settleCalls.Load())
	}
	if w.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("expected X-PAYMENT-RESPONSE header on successful settlement")
	}
}

// TestVerifier_HandleProxy_TolerantChatPathRewrite covers the forgiving path
// normalization for chat-completions-shaped offers: buyers who POST to the
// bare service base (as older 402-page prompts instructed) or to
// /chat/completions must still land on the upstream's /v1/chat/completions
// instead of paying for a 404. Non-chat offers and non-tolerated sub-paths
// must pass through untouched.
func TestVerifier_HandleProxy_TolerantChatPathRewrite(t *testing.T) {
	cases := []struct {
		name         string
		offerType    string
		requestPath  string
		wantUpstream string
	}{
		{"agent bare base", "agent", "/services/demo", "/v1/chat/completions"},
		{"agent trailing slash", "agent", "/services/demo/", "/v1/chat/completions"},
		{"agent missing v1", "agent", "/services/demo/chat/completions", "/v1/chat/completions"},
		{"agent canonical", "agent", "/services/demo/v1/chat/completions", "/v1/chat/completions"},
		{"inference bare base", "inference", "/services/demo", "/v1/chat/completions"},
		{"inference other v1 route untouched", "inference", "/services/demo/v1/embeddings", "/v1/embeddings"},
		{"http bare base untouched", "http", "/services/demo", "/"},
		{"http sub-path untouched", "http", "/services/demo/run", "/run"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fac := newMockFacilitator(t, mockFacilitatorOpts{})
			var seenPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			v := newTestVerifier(t, fac.URL, []RouteRule{{
				Pattern:     "/services/demo/*",
				Price:       "0.0001",
				UpstreamURL: upstream.URL,
				StripPrefix: "/services/demo",
				OfferType:   tc.offerType,
			}})

			req := httptest.NewRequest(http.MethodPost, tc.requestPath, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-PAYMENT", testPaymentHeader(t))
			w := httptest.NewRecorder()
			v.HandleProxy(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body %q)", w.Code, w.Body.String())
			}
			if seenPath != tc.wantUpstream {
				t.Fatalf("upstream path = %q, want %q", seenPath, tc.wantUpstream)
			}
		})
	}
}

// GET requests must never be rewritten — the tolerant rewrite is only for
// POSTed chat bodies.
func TestVerifier_HandleProxy_TolerantRewrite_SkipsGET(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var seenPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: upstream.URL,
		StripPrefix: "/services/demo",
		OfferType:   "agent",
	}})

	req := httptest.NewRequest(http.MethodGet, "/services/demo", nil)
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if seenPath != "/" {
		t.Fatalf("upstream path = %q, want / (GET must not be rewritten)", seenPath)
	}
}

func TestVerifier_HandleProxy_UpstreamFailure_DoesNotSettle(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: upstream.URL,
		StripPrefix: "/services/demo",
	}})

	req := httptest.NewRequest(http.MethodPost, "/services/demo/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Fatalf("verify calls = %d, want 1", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 0 {
		t.Fatalf("settle calls = %d, want 0", fac.settleCalls.Load())
	}
	if w.Header().Get("X-PAYMENT-RESPONSE") != "" {
		t.Fatal("did not expect X-PAYMENT-RESPONSE header on upstream failure")
	}
}

// TestVerifier_HandleProxy_StreamsSSEChunks proves the seller-gateway path
// (Traefik → x402-verifier → upstream) preserves Server-Sent Events streaming
// end-to-end. This is what makes `obol sell agent` usable as an OpenAI-
// compatible streaming backend for chat frontends.
//
// httptest.NewRecorder buffers writes, so it cannot catch a regression where
// the settlementInterceptor swallows flushes or where httputil.ReverseProxy
// fails to detect text/event-stream. We therefore stand up a real httptest
// server, time when each SSE chunk reaches the client, and assert that
// chunks arrive with the same pacing the upstream emitted them (which can
// only happen if every layer in the chain flushes per write).
func TestVerifier_HandleProxy_StreamsSSEChunks(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	const chunkGap = 80 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream ResponseWriter does not implement http.Flusher")
			return
		}

		// Three deltas + the terminating [DONE] marker. Hermes emits this
		// exact shape; mirroring it here keeps the test honest.
		chunks := []string{
			`data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":" world"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":"!"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		}
		for i, c := range chunks {
			if i > 0 {
				// Pace the chunks so we can assert the client sees them
				// arrive progressively rather than all at once.
				time.Sleep(chunkGap)
			}
			if _, err := w.Write([]byte(c)); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: upstream.URL,
		StripPrefix: "/services/demo",
	}})

	// Real HTTP server so Flush() actually reaches the wire. Recorder
	// would swallow flushes and give us a false positive.
	srv := httptest.NewServer(http.HandlerFunc(v.HandleProxy))
	defer srv.Close()

	reqBody := strings.NewReader(`{"model":"hermes-agent","stream":true,"messages":[]}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/services/demo/v1/chat/completions", reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	client := &http.Client{Timeout: 15 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream*", ct)
	}
	if resp.Header.Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("expected X-PAYMENT-RESPONSE on a streaming success")
	}

	// Read each SSE event ("data: ...\n\n") and capture the elapsed time
	// since the request started. If anything in the chain buffers the
	// response, all four events will land in a single tight cluster at
	// the end instead of being spread across the upstream's pacing.
	reader := bufio.NewReader(resp.Body)
	var got []string
	var arrivals []time.Duration
	for i := 0; i < 4; i++ {
		dataLine, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read chunk %d data line: %v", i, err)
		}
		arrivals = append(arrivals, time.Since(start))
		blank, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read chunk %d blank line: %v", i, err)
		}
		if strings.TrimSpace(blank) != "" {
			t.Fatalf("chunk %d separator was %q, want empty line", i, blank)
		}
		got = append(got, strings.TrimRight(dataLine, "\n"))
	}

	want := []string{
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		`data: {"choices":[{"delta":{"content":"!"}}]}`,
		"data: [DONE]",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Streaming assertion: the last chunk must arrive at least
	// 2 × chunkGap after the first. If the chain buffers, all chunks
	// land together at the upstream's End-of-Body, and arrivals[3] -
	// arrivals[0] is ≈ 0. Use 2× as the floor (out of 3 gaps) for
	// scheduler jitter slack.
	spread := arrivals[3] - arrivals[0]
	if spread < 2*chunkGap {
		t.Errorf("SSE chunks were buffered: arrivals[0]=%v arrivals[3]=%v spread=%v (want ≥ %v)\nfull timings=%v",
			arrivals[0], arrivals[3], spread, 2*chunkGap, arrivals)
	}

	if fac.verifyCalls.Load() != 1 {
		t.Fatalf("verify calls = %d, want 1", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 1 {
		t.Fatalf("settle calls = %d, want 1 (settle is one-shot per paid request, not per chunk)", fac.settleCalls.Load())
	}
}

// TestVerifier_HandleProxy_NonStreamingResponse confirms the same gateway
// path still handles the classic stream:false JSON response correctly —
// i.e. the streaming fix didn't accidentally chunk-encode replies that the
// upstream chose to deliver as a single buffered body.
func TestVerifier_HandleProxy_NonStreamingResponse(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: upstream.URL,
		StripPrefix: "/services/demo",
	}})

	srv := httptest.NewServer(http.HandlerFunc(v.HandleProxy))
	defer srv.Close()

	reqBody := strings.NewReader(`{"model":"hermes-agent","messages":[]}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/services/demo/v1/chat/completions", reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json*", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `"content":"hi"`) {
		t.Fatalf("body missing assistant content: %s", body)
	}
	if resp.Header.Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("expected X-PAYMENT-RESPONSE header on non-streaming success")
	}
	if fac.settleCalls.Load() != 1 {
		t.Fatalf("settle calls = %d, want 1", fac.settleCalls.Load())
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
	if got := w.Body.String(); !strings.Contains(got, "config not loaded") {
		t.Errorf("expected body to mention %q, got %q", "config not loaded", got)
	}
}

// TestVerifier_Readyz_BlocksUntilRoutesLoaded asserts the fix for
// CLAUDE.md pitfall #14: /readyz must return 503 between "config loaded"
// and "first route source apply completed" so kubelet keeps the pod out
// of the Service Endpoints during informer warm-up.
func TestVerifier_Readyz_BlocksUntilRoutesLoaded(t *testing.T) {
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "http://example.invalid",
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Config is loaded by NewVerifier, but routes have NOT been marked
	// loaded yet — /readyz must still 503 with a routes-specific message
	// so kubectl describe pod surfaces the actual cause.
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	v.HandleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before routes loaded, got %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, "routes not loaded") {
		t.Errorf("expected body to mention %q, got %q", "routes not loaded", got)
	}

	// After the route source signals first apply, /readyz flips to 200.
	v.MarkRoutesLoaded()

	w = httptest.NewRecorder()
	v.HandleReadyz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after MarkRoutesLoaded, got %d (body=%q)", w.Code, w.Body.String())
	}

	// MarkRoutesLoaded is idempotent — calling it again must not regress.
	v.MarkRoutesLoaded()
	w = httptest.NewRecorder()
	v.HandleReadyz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after second MarkRoutesLoaded, got %d", w.Code)
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
	req.Header.Set("X-PAYMENT", testPaymentHeaderFor(t, routeWallet, "1000"))
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
		"offer_namespace": "llm",
		"offer_name":      "paid-rpc",
		"chain":           "",
		"asset_symbol":    "unknown",
	}
	assertVerifierMetricValue(t, metrics["obol_x402_verifier_requests_total"], labels, 1)
	assertVerifierMetricValue(t, metrics["obol_x402_verifier_payment_required_total"], labels, 1)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_payment_verified_total"], labels)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_payment_failed_total"], labels)
	assertVerifierMetricMissing(t, metrics["obol_x402_verifier_charged_requests_total"], labels)
}

func TestVerifier_MetricsVerifiedAndRejectedPayments(t *testing.T) {
	labels := map[string]string{
		"offer_namespace": "llm",
		"offer_name":      "paid-rpc",
		"chain":           "",
		"asset_symbol":    "unknown",
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

// TestVerifier_LastPaymentSuccessGauge asserts that the
// obol_x402_verifier_last_payment_success_seconds gauge is stamped to the
// current wall-clock time when a paid request succeeds, and is NOT touched
// when an unpaid request is rejected with 402.
//
// The gauge is labeled identically to the verifier counters; for this rule
// `chain` is the empty string because the test RouteRule has no Network set,
// and `asset_symbol` is "unknown" because AssetSymbol is unset (the defensive
// fallback emitted by prometheusLabels).
func TestVerifier_LastPaymentSuccessGauge(t *testing.T) {
	labels := map[string]string{
		"offer_namespace": "llm",
		"offer_name":      "paid-rpc",
		"chain":           "",
		"asset_symbol":    "unknown",
	}

	tests := []struct {
		name           string
		setPayment     bool
		rejectPayment  bool
		wantStatus     int
		wantGaugeFresh bool // assert gauge ~= now()
	}{
		{
			name:           "successful paid request stamps gauge",
			setPayment:     true,
			rejectPayment:  false,
			wantStatus:     http.StatusOK,
			wantGaugeFresh: true,
		},
		{
			name:           "unpaid 402 leaves gauge untouched",
			setPayment:     false,
			rejectPayment:  false,
			wantStatus:     http.StatusPaymentRequired,
			wantGaugeFresh: false,
		},
		{
			name:           "rejected payment leaves gauge untouched",
			setPayment:     true,
			rejectPayment:  true,
			wantStatus:     http.StatusPaymentRequired,
			wantGaugeFresh: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fac := newMockFacilitator(t, mockFacilitatorOpts{rejectPayment: tt.rejectPayment})
			v := newTestVerifier(t, fac.URL, []RouteRule{{
				Pattern:        "/rpc/*",
				Price:          "0.0001",
				OfferNamespace: "llm",
				OfferName:      "paid-rpc",
			}})

			req := httptest.NewRequest(http.MethodPost, "/verify", nil)
			req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
			req.Header.Set("X-Forwarded-Host", "obol.stack")
			if tt.setPayment {
				req.Header.Set("X-PAYMENT", testPaymentHeader(t))
			}

			before := time.Now().Unix()
			rec := httptest.NewRecorder()
			v.HandleVerify(rec, req)
			after := time.Now().Unix()

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			families := scrapeVerifierMetrics(t, v)
			gauge := families["obol_x402_verifier_last_payment_success_seconds"]

			if !tt.wantGaugeFresh {
				// Either the family is absent (no series emitted) or no
				// series exists for these labels — both are acceptable for
				// an untouched gauge.
				assertVerifierMetricMissing(t, gauge, labels)
				return
			}

			if gauge == nil {
				t.Fatalf("missing metric family obol_x402_verifier_last_payment_success_seconds")
			}
			got := findVerifierMetricValue(t, gauge, labels)
			// Allow ±5s slack for clock skew / slow CI.
			if got < float64(before-5) || got > float64(after+5) {
				t.Fatalf("gauge = %v, want within [%d, %d]", got, before-5, after+5)
			}
		})
	}
}

// TestVerifier_Reload_PrunesDeletedOfferSeries asserts that when an offer is
// removed from the route set (via Reload, the same path used by both the
// file-config watcher and the kube ServiceOffer informer), its previously
// stamped metric series are dropped from the registry. Without this, deleted
// offers' last_payment_success_seconds gauge would survive forever and keep
// firing/silencing alerts on dead labels.
func TestVerifier_Reload_PrunesDeletedOfferSeries(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	keptRoute := RouteRule{
		Pattern:        "/keep/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "keep",
	}
	removedRoute := RouteRule{
		Pattern:        "/gone/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "gone",
	}
	v := newTestVerifier(t, fac.URL, []RouteRule{keptRoute, removedRoute})

	// Stamp metrics for both offers with a successful paid request each.
	for _, path := range []string{"/keep/x", "/gone/x"} {
		req := httptest.NewRequest(http.MethodPost, "/verify", nil)
		req.Header.Set("X-Forwarded-Uri", path)
		req.Header.Set("X-Forwarded-Host", "obol.stack")
		req.Header.Set("X-PAYMENT", testPaymentHeader(t))
		rec := httptest.NewRecorder()
		v.HandleVerify(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup paid request to %s: status=%d", path, rec.Code)
		}
	}

	keptLabels := map[string]string{"offer_namespace": "llm", "offer_name": "keep", "chain": "", "asset_symbol": "unknown"}
	goneLabels := map[string]string{"offer_namespace": "llm", "offer_name": "gone", "chain": "", "asset_symbol": "unknown"}

	families := scrapeVerifierMetrics(t, v)
	for _, name := range []string{
		"obol_x402_verifier_charged_requests_total",
		"obol_x402_verifier_last_payment_success_seconds",
	} {
		family := families[name]
		if family == nil {
			t.Fatalf("baseline: missing %s before reload", name)
		}
		findVerifierMetricValue(t, family, keptLabels)
		findVerifierMetricValue(t, family, goneLabels)
	}

	// Reload with the second offer dropped — the same path ServiceOffer
	// deletion takes through ConfigAccumulator.SetRoutes.
	if err := v.Reload(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes:         []RouteRule{keptRoute},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	families = scrapeVerifierMetrics(t, v)
	for _, name := range []string{
		"obol_x402_verifier_requests_total",
		"obol_x402_verifier_payment_required_total",
		"obol_x402_verifier_payment_verified_total",
		"obol_x402_verifier_payment_failed_total",
		"obol_x402_verifier_charged_requests_total",
		"obol_x402_verifier_last_payment_success_seconds",
	} {
		assertVerifierMetricMissing(t, families[name], goneLabels)
	}

	// Kept offer's series must survive the prune.
	if charged := families["obol_x402_verifier_charged_requests_total"]; charged != nil {
		findVerifierMetricValue(t, charged, keptLabels)
	}
	if gauge := families["obol_x402_verifier_last_payment_success_seconds"]; gauge != nil {
		findVerifierMetricValue(t, gauge, keptLabels)
	}
}

// TestVerifier_HandleVerify_FailClosed_ManualPrefixInjection sanity checks
// that an arbitrary prefix in paidPrefixes triggers fail-closed (403) when
// no rule matches. The manual prefix injection simulates the case where the
// verifier KNOWS about a paid prefix (because a route was previously loaded)
// but the matcher rejects the URI — config drift, code bug, etc.
func TestVerifier_HandleVerify_FailClosed_ManualPrefixInjection(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		// No rules; matchRoute will return nil for everything.
	})

	// Manually inject a paid prefix (simulating a stale prefix state).
	prefixes := []string{"/services/gated/"}
	v.paidPrefixes.Store(&prefixes)

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/gated/foo")
	rec := httptest.NewRecorder()
	v.HandleVerify(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 (fail-closed) for URI under tracked paid prefix, got %d", rec.Code)
	}
}

// TestVerifier_HandleVerify_FreeRoute_OutsidePrefixes asserts that URIs
// outside all tracked paid prefixes still return 200 (legitimate free pass).
// The verifier is mounted on routes that may or may not be paid; only URIs
// under a known paid prefix should fail closed.
func TestVerifier_HandleVerify_FreeRoute_OutsidePrefixes(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/services/known/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/health") // Not under any paid prefix.
	rec := httptest.NewRecorder()
	v.HandleVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for free route outside paid prefixes, got %d", rec.Code)
	}
}

// TestVerifier_HandleVerify_PrefixBoundary_NoFalseMatch verifies that the
// trailing slash on paid prefixes prevents false matches between siblings
// like /services/foo/ and /services/foobar/. Without the trailing slash,
// a request to /services/foobar/x would falsely match /services/foo/*.
func TestVerifier_HandleVerify_PrefixBoundary_NoFalseMatch(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/services/foo/*", Price: "0.0001"},
	})

	// /services/foobar/x is NOT under /services/foo/ — must return 200.
	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/foobar/x")
	rec := httptest.NewRecorder()
	v.HandleVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for sibling path not under prefix, got %d", rec.Code)
	}
}

func TestPatternToPrefix(t *testing.T) {
	cases := []struct{ pattern, want string }{
		{"/services/foo/*", "/services/foo/"},
		{"/rpc/*", "/rpc/"},
		{"/health", ""}, // No glob, returns empty.
		{"/*", "/"},
		{"", ""},
		{"/exact/match", ""}, // Exact pattern, not a prefix.
	}
	for _, c := range cases {
		if got := patternToPrefix(c.pattern); got != c.want {
			t.Errorf("patternToPrefix(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}

// findVerifierMetricValue returns the value of the series in `family` whose
// labels match `wantLabels` exactly, failing the test if no such series exists.
func findVerifierMetricValue(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string) float64 {
	t.Helper()

	for _, metric := range family.GetMetric() {
		if verifierLabelsMatch(metric, wantLabels) {
			return verifierMetricValue(metric)
		}
	}
	t.Fatalf("metric %s missing labels %v", family.GetName(), wantLabels)
	return 0
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

// TestVerifier_PrometheusLabels_IncludesAssetSymbol asserts that the
// asset_symbol label is emitted with the value from RouteRule.AssetSymbol
// (which the serviceoffer_source populates from
// offer.Spec.Payment.Asset.Symbol). This is what makes "what's my OBOL
// revenue?" a single PromQL aggregation instead of a metric × CR join.
func TestVerifier_PrometheusLabels_IncludesAssetSymbol(t *testing.T) {
	rule := &RouteRule{
		OfferNamespace: "llm",
		OfferName:      "demo-hello",
		Network:        "eip155:84532",
		AssetSymbol:    "USDC",
	}
	labels := prometheusLabels(rule)
	if got := labels["asset_symbol"]; got != "USDC" {
		t.Errorf("asset_symbol = %q, want %q (full labels: %v)", got, "USDC", labels)
	}
	if got := labels["chain"]; got != "eip155:84532" {
		t.Errorf("chain = %q, want %q", got, "eip155:84532")
	}
}

// TestVerifier_PrometheusLabels_DefaultsToUnknownIfEmpty asserts the
// defensive fallback: when AssetSymbol is empty (legacy offers, parsing
// hiccup, etc.) the label value is "unknown" rather than "" — empty-string
// labels are legal in Prometheus but render as bare selectors that are
// awkward to filter in dashboards.
func TestVerifier_PrometheusLabels_DefaultsToUnknownIfEmpty(t *testing.T) {
	rule := &RouteRule{
		OfferNamespace: "llm",
		OfferName:      "no-asset",
		Network:        "eip155:84532",
		AssetSymbol:    "",
	}
	labels := prometheusLabels(rule)
	if got := labels["asset_symbol"]; got != "unknown" {
		t.Errorf("asset_symbol = %q, want %q (full labels: %v)", got, "unknown", labels)
	}
}

// TestVerifier_PruneSeriesNotIn_DistinguishesAssetSymbol asserts that
// pruning treats asset_symbol as part of the series key, so an asset-repin
// scenario (USDC route gets dropped, OBOL route for the same offer is
// retained) prunes the dead USDC series without taking the live OBOL one
// with it. Without asset_symbol in the key, both series would map to the
// same (ns, name, chain) tuple and pruning would either drop both or
// neither — leaking a stale per-asset series.
func TestVerifier_PruneSeriesNotIn_DistinguishesAssetSymbol(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	usdcRoute := RouteRule{
		Pattern:        "/svc/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "demo",
		Network:        "base-sepolia",
		AssetSymbol:    "USDC",
	}
	obolRoute := RouteRule{
		Pattern:        "/svc-obol/*",
		Price:          "0.0001",
		OfferNamespace: "llm",
		OfferName:      "demo",
		Network:        "base-sepolia",
		AssetSymbol:    "OBOL",
	}
	v := newTestVerifier(t, fac.URL, []RouteRule{usdcRoute, obolRoute})

	// Stamp a successful paid request through each asset variant so both
	// series exist in the registry before pruning.
	for _, path := range []string{"/svc/x", "/svc-obol/x"} {
		req := httptest.NewRequest(http.MethodPost, "/verify", nil)
		req.Header.Set("X-Forwarded-Uri", path)
		req.Header.Set("X-Forwarded-Host", "obol.stack")
		req.Header.Set("X-PAYMENT", testPaymentHeader(t))
		rec := httptest.NewRecorder()
		v.HandleVerify(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup paid request to %s: status=%d", path, rec.Code)
		}
	}

	usdcLabels := map[string]string{
		"offer_namespace": "llm",
		"offer_name":      "demo",
		"chain":           "base-sepolia",
		"asset_symbol":    "USDC",
	}
	obolLabels := map[string]string{
		"offer_namespace": "llm",
		"offer_name":      "demo",
		"chain":           "base-sepolia",
		"asset_symbol":    "OBOL",
	}

	families := scrapeVerifierMetrics(t, v)
	for _, name := range []string{
		"obol_x402_verifier_charged_requests_total",
		"obol_x402_verifier_last_payment_success_seconds",
	} {
		family := families[name]
		if family == nil {
			t.Fatalf("baseline: missing %s before reload", name)
		}
		findVerifierMetricValue(t, family, usdcLabels)
		findVerifierMetricValue(t, family, obolLabels)
	}

	// Drop the USDC route, keep OBOL. If pruneSeriesNotIn ignored
	// asset_symbol, both series would key to (llm, demo, base-sepolia)
	// and the OBOL series would survive (because the OBOL route is in
	// the keep set) — masking the bug. Conversely, if the key didn't
	// distinguish at all, both could be wiped. Including asset_symbol
	// in the key keeps USDC prunable and OBOL alive.
	if err := v.Reload(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		Routes:         []RouteRule{obolRoute},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	families = scrapeVerifierMetrics(t, v)
	for _, name := range []string{
		"obol_x402_verifier_requests_total",
		"obol_x402_verifier_charged_requests_total",
		"obol_x402_verifier_last_payment_success_seconds",
	} {
		assertVerifierMetricMissing(t, families[name], usdcLabels)
	}

	if charged := families["obol_x402_verifier_charged_requests_total"]; charged != nil {
		findVerifierMetricValue(t, charged, obolLabels)
	} else {
		t.Errorf("OBOL charged series was pruned along with USDC — asset_symbol was ignored in prune key")
	}
}

// TestVerifier_QueryString_DoesNotBypassPaidRule pins the query-string fix:
// Traefik forwards the full request URI, so "/rpc?method=x" must still hit
// the "/rpc/*" rule instead of free-passing around it (ForwardAuth 200
// means "allow").
func TestVerifier_QueryString_DoesNotBypassPaidRule(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/rpc/*", Price: "0.0001"},
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc?method=eth_sendRawTransaction")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for paid route with query string, got %d", w.Code)
	}
}

// TestVerifier_FreeGateRule_AllowsAndInjectsUpstreamAuth covers the
// route-table free carve-out in ForwardAuth mode: the request is allowed
// through without touching the facilitator, and the upstream bearer is
// still injected so authenticated upstreams serve the free path.
func TestVerifier_FreeGateRule_AllowsAndInjectsUpstreamAuth(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{Pattern: "/services/foo/healthz", Gate: "free", UpstreamAuth: "Bearer sk-free"},
		{Pattern: "/services/foo/*", Price: "0.5"},
	})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/foo/healthz")
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for free carve-out, got %d", w.Code)
	}
	if got := w.Header().Get("Authorization"); got != "Bearer sk-free" {
		t.Errorf("Authorization = %q, want upstream auth injected", got)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Error("facilitator must not be called for free carve-outs")
	}

	// The paid sibling stays gated.
	req = httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/foo/run")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	w = httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for paid sibling, got %d", w.Code)
	}
}

// TestVerifier_HandleProxy_FreeGateRule_ProxiesWithoutPayment covers the
// free carve-out on the in-process seller gateway: the request reaches the
// upstream (prefix-stripped, auth-injected) with zero facilitator calls,
// while the sibling paid route still returns 402.
func TestVerifier_HandleProxy_FreeGateRule_ProxiesWithoutPayment(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"jobs":"ok"}`)
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{
		{
			Pattern:      "/services/foo/jobs/*",
			Gate:         "free",
			UpstreamURL:  upstream.URL,
			StripPrefix:  "/services/foo",
			UpstreamAuth: "Bearer sk-free",
		},
		{
			Pattern:     "/services/foo/*",
			Price:       "0.5",
			UpstreamURL: upstream.URL,
			StripPrefix: "/services/foo",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/services/foo/jobs/123", nil)
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from upstream via free route, got %d (%s)", w.Code, w.Body.String())
	}
	if gotPath != "/jobs/123" {
		t.Errorf("upstream path = %q, want /jobs/123 (prefix stripped)", gotPath)
	}
	if gotAuth != "Bearer sk-free" {
		t.Errorf("upstream Authorization = %q, want injected bearer", gotAuth)
	}
	if fac.verifyCalls.Load() != 0 || fac.settleCalls.Load() != 0 {
		t.Error("facilitator must not be called for free carve-outs")
	}

	// Sibling paid route still gates.
	req = httptest.NewRequest(http.MethodPost, "/services/foo/run", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for paid sibling, got %d", w.Code)
	}
}
