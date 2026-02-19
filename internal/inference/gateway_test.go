package inference

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	x402 "github.com/mark3labs/x402-go"
)

// ── Mock facilitator ──────────────────────────────────────────────────────────

type mockFacilitatorOpts struct {
	rejectPayment bool // /verify returns isValid:false
}

type mockFacilitator struct {
	*httptest.Server
	verifyCalls  atomic.Int32
	settleCalls  atomic.Int32
	supportCalls atomic.Int32
}

func newMockFacilitator(t *testing.T, opts mockFacilitatorOpts) *mockFacilitator {
	t.Helper()
	mf := &mockFacilitator{}

	mux := http.NewServeMux()

	mux.HandleFunc("/supported", func(w http.ResponseWriter, r *http.Request) {
		mf.supportCalls.Add(1)
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

// ── Mock upstream (Ollama) ────────────────────────────────────────────────────

func newMockOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop","index":0}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"object":"list","data":[{"id":"llama3.2","object":"model"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// testPaymentHeader returns a valid base64-encoded x402 PaymentPayload that
// satisfies the middleware's scheme+network matching for BaseSepolia/exact.
// The mock facilitator accepts any payload so no real signature is needed.
func testPaymentHeader(t *testing.T) string {
	t.Helper()
	p := x402.PaymentPayload{
		X402Version: 1,
		Scheme:      "exact",
		Network:     x402.BaseSepolia.NetworkID,
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

// newTestGateway starts a gateway backed by the given upstream and facilitator
// using httptest.NewServer. VMMode and EnclaveTag are always disabled.
func newTestGateway(t *testing.T, facilitatorURL, upstreamURL string, verifyOnly bool) *httptest.Server {
	t.Helper()
	gw, err := NewGateway(GatewayConfig{
		UpstreamURL:     upstreamURL,
		WalletAddress:   "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		PricePerRequest: "0.001",
		Chain:           x402.BaseSepolia,
		FacilitatorURL:  facilitatorURL,
		VerifyOnly:      verifyOnly,
	})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	handler, err := gw.buildHandler(upstreamURL)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGateway_Health(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	resp, err := http.Get(gw.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGateway_NoPayment_Returns402(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", resp.StatusCode)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Error("facilitator verify should not be called without X-PAYMENT header")
	}
}

func TestGateway_ValidPayment_Returns200(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("expected 1 verify call, got %d", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 1 {
		t.Errorf("expected 1 settle call, got %d", fac.settleCalls.Load())
	}
}

func TestGateway_VerifyOnly_SkipsSettle(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, true /* verifyOnly */)

	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("expected 1 verify call, got %d", fac.verifyCalls.Load())
	}
	if fac.settleCalls.Load() != 0 {
		t.Errorf("expected 0 settle calls (verifyOnly), got %d", fac.settleCalls.Load())
	}
}

func TestGateway_FacilitatorRejects_Returns402(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{rejectPayment: true})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402 on rejected payment, got %d", resp.StatusCode)
	}
	if fac.settleCalls.Load() != 0 {
		t.Error("settle should not be called when verify fails")
	}
}

func TestGateway_UpstreamDown_Returns502(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	// Start and immediately close a server to get a dead upstream URL.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	gw := newTestGateway(t, fac.URL, deadURL, true)

	req, _ := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestGateway_UnprotectedPassthrough(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	// GET /v1/models is protected — no payment → 402.
	resp, err := http.Get(gw.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("GET /v1/models without payment: expected 402, got %d", resp.StatusCode)
	}
}

func TestGateway_ModelsEndpoint_WithPayment(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, true)

	req, _ := http.NewRequest(http.MethodGet, gw.URL+"/v1/models", nil)
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with payment, got %d", resp.StatusCode)
	}
}
