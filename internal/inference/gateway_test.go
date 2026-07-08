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

	x402pkg "github.com/ObolNetwork/obol-stack/internal/x402"
	x402types "github.com/x402-foundation/x402/go/v2/types"
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
	t.Cleanup(mf.Close)

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

	p := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: x402pkg.ChainBaseSepolia.CAIP2Network,
			Amount:  "1000",
			Asset:   x402pkg.ChainBaseSepolia.USDCAddress,
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

// newTestGateway starts a gateway backed by the given upstream and facilitator
// using httptest.NewServer. VMMode and EnclaveTag are always disabled.
func newTestGateway(t *testing.T, facilitatorURL, upstreamURL string, verifyOnly bool) *httptest.Server {
	t.Helper()

	gw, err := NewGateway(GatewayConfig{
		UpstreamURL:     upstreamURL,
		WalletAddress:   "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		PricePerRequest: "0.001",
		Chain:           x402pkg.ChainBaseSepolia,
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

func TestNewGateway_DefaultsToBaseMainnet(t *testing.T) {
	gw, err := NewGateway(GatewayConfig{
		UpstreamURL:   "http://localhost:11434",
		WalletAddress: "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	if gw.config.Chain.NetworkID != x402pkg.ChainBaseMainnet.NetworkID {
		t.Fatalf("default chain network = %q, want %q", gw.config.Chain.NetworkID, x402pkg.ChainBaseMainnet.NetworkID)
	}
	if gw.config.Chain.CAIP2Network != x402pkg.ChainBaseMainnet.CAIP2Network {
		t.Fatalf("default CAIP2 network = %q, want %q", gw.config.Chain.CAIP2Network, x402pkg.ChainBaseMainnet.CAIP2Network)
	}
	if gw.config.FacilitatorURL != x402pkg.DefaultFacilitatorURL {
		t.Fatalf("default facilitator = %q, want %q", gw.config.FacilitatorURL, x402pkg.DefaultFacilitatorURL)
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
	req.Header.Set("X-Payment", testPaymentHeader(t))

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
	req.Header.Set("X-Payment", testPaymentHeader(t))

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
	req.Header.Set("X-Payment", testPaymentHeader(t))

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
	req.Header.Set("X-Payment", testPaymentHeader(t))

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
	req.Header.Set("X-Payment", testPaymentHeader(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with payment, got %d", resp.StatusCode)
	}
}

// ── TEE Mode Tests ────────────────────────────────────────────────────────────

// newTestGatewayTEE starts a gateway with TEE stub mode enabled.
func newTestGatewayTEE(t *testing.T, facilitatorURL, upstreamURL string) *httptest.Server {
	t.Helper()

	gw, err := NewGateway(GatewayConfig{
		UpstreamURL:     upstreamURL,
		WalletAddress:   "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		PricePerRequest: "0.001",
		Chain:           x402pkg.ChainBaseSepolia,
		FacilitatorURL:  facilitatorURL,
		VerifyOnly:      true,
		TEEType:         "stub",
		ModelHash:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
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

func TestGateway_TEE_Attestation(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGatewayTEE(t, fac.URL, ollama.URL)

	resp, err := http.Get(gw.URL + "/v1/attestation")
	if err != nil {
		t.Fatalf("GET /v1/attestation: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var report struct {
		TEEType   string `json:"tee_type"`
		Pubkey    string `json:"pubkey"`
		ModelHash string `json:"model_hash"`
		Quote     []byte `json:"quote"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if report.TEEType != "stub" {
		t.Errorf("tee_type = %q, want %q", report.TEEType, "stub")
	}

	if report.Pubkey == "" {
		t.Error("pubkey should not be empty")
	}

	if report.ModelHash == "" {
		t.Error("model_hash should not be empty")
	}

	if len(report.Quote) == 0 {
		t.Error("quote should not be empty")
	}

	if report.Timestamp == 0 {
		t.Error("timestamp should not be zero")
	}
}

func TestGateway_TEE_PubkeyEndpoint(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGatewayTEE(t, fac.URL, ollama.URL)

	resp, err := http.Get(gw.URL + "/v1/enclave/pubkey")
	if err != nil {
		t.Fatalf("GET /v1/enclave/pubkey: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var pk struct {
		Pubkey    string `json:"pubkey"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pk); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if pk.Pubkey == "" {
		t.Error("pubkey should not be empty")
	}
}

func TestGateway_TEE_ECIES_RoundTrip(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGatewayTEE(t, fac.URL, ollama.URL)

	// Fetch pubkey.
	resp, err := http.Get(gw.URL + "/v1/enclave/pubkey")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var pk struct {
		Pubkey string `json:"pubkey"`
	}
	json.NewDecoder(resp.Body).Decode(&pk)

	if pk.Pubkey == "" {
		t.Fatal("empty pubkey from gateway")
	}

	// This confirms the TEE key is accessible via the standard enclave/pubkey endpoint.
	// Full ECIES encrypt→decrypt is tested in internal/tee/tee_test.go.
	t.Logf("TEE stub pubkey: %s...%s", pk.Pubkey[:16], pk.Pubkey[len(pk.Pubkey)-8:])
}

func TestGateway_NoTEE_NoAttestationEndpoint(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	// /v1/attestation should NOT be registered when TEE is disabled.
	resp, err := http.Get(gw.URL + "/v1/attestation")
	if err != nil {
		t.Fatalf("GET /v1/attestation: %v", err)
	}
	defer resp.Body.Close()

	// Without a registered handler, the default mux route (proxy) handles it.
	// It should NOT return 200 with an attestation report.
	if resp.StatusCode == http.StatusOK {
		var report struct {
			TEEType string `json:"tee_type"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&report); err == nil && report.TEEType != "" {
			t.Error("/v1/attestation should not be available when TEE mode is disabled")
		}
	}
}

// TestGateway_NoPaymentGate_AllowsWithoutPayment verifies that when
// NoPaymentGate is true the payment middleware is bypassed — requests reach
// the upstream without an X-PAYMENT header and get 200, not 402.
func TestGateway_NoPaymentGate_AllowsWithoutPayment(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)

	gw, err := NewGateway(GatewayConfig{
		UpstreamURL:     ollama.URL,
		WalletAddress:   "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		PricePerRequest: "0.001",
		Chain:           x402pkg.ChainBaseSepolia,
		FacilitatorURL:  fac.URL,
		NoPaymentGate:   true,
	})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	handler, err := gw.buildHandler(ollama.URL)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// No X-PAYMENT header — should pass straight through to upstream.
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("NoPaymentGate=true: expected 200 without payment, got %d", resp.StatusCode)
	}

	if fac.verifyCalls.Load() != 0 {
		t.Errorf("NoPaymentGate=true: facilitator verify should not be called, got %d calls", fac.verifyCalls.Load())
	}
}

// TestGateway_ValidateFacilitatorURL_RejectsHTTP verifies that NewGateway
// rejects a plain http:// facilitator URL (non-localhost) at construction
// time so bad configs are caught before any requests are served.
func TestGateway_ValidateFacilitatorURL_RejectsHTTP(t *testing.T) {
	_, err := NewGateway(GatewayConfig{
		UpstreamURL:    "http://localhost:11434",
		WalletAddress:  "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		FacilitatorURL: "http://some-remote-facilitator.example.com",
	})
	if err == nil {
		t.Error("expected error for non-https facilitator URL, got nil")
	}
}

// TestNormalizeServicePrefixedPath covers the path rewriting used when the
// cluster's Traefik gateway preserves the /services/<name>/ storefront prefix
// on requests forwarded to the inference gateway.
func TestNormalizeServicePrefixedPath(t *testing.T) {
	cases := []struct {
		input    string
		wantPath string
		wantOK   bool
	}{
		{"/services/my-offer/v1/chat/completions", "/v1/chat/completions", true},
		{"/services/my-offer/v1/models", "/v1/models", true},
		{"/services/my-offer/", "/", true},
		{"/services/my-offer", "/", true},
		{"/v1/chat/completions", "", false},
		{"/health", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		got, ok := normalizeServicePrefixedPath(tc.input)
		if ok != tc.wantOK {
			t.Errorf("normalizeServicePrefixedPath(%q): ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.wantPath {
			t.Errorf("normalizeServicePrefixedPath(%q): path=%q, want %q", tc.input, got, tc.wantPath)
		}
	}
}

// TestGateway_ServicePrefixedPath_RoutesCorrectly verifies that a request
// arriving with the /services/<name>/... prefix is rewritten and still hits
// the protected inference route (returns 402 without payment).
func TestGateway_ServicePrefixedPath_RoutesCorrectly(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	// Send without payment — expect 402, proving the path was normalised and
	// hit the protected route rather than falling through to the catch-all proxy.
	resp, err := http.Post(
		gw.URL+"/services/my-offer/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402 after prefix normalisation, got %d", resp.StatusCode)
	}
}

// TestGateway_ServicePrefixedPath_PreservesQuery verifies the storefront path
// rewrite does not drop the original query string when rebuilding RequestURI.
func TestGateway_ServicePrefixedPath_PreservesQuery(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	ollama := newMockOllama(t)
	gw := newTestGateway(t, fac.URL, ollama.URL, false)

	req, err := http.NewRequest(
		http.MethodPost,
		gw.URL+"/services/my-offer/v1/chat/completions?verbose=true",
		strings.NewReader(`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402 after prefix normalisation with query, got %d", resp.StatusCode)
	}
}

func TestFormatPriceLogLine(t *testing.T) {
	tests := []struct {
		name   string
		price  string
		symbol string
		want   string
	}{
		{"empty symbol defaults to USDC", "0.001", "", "0.001 USDC/request"},
		{"explicit USDC", "0.001", "USDC", "0.001 USDC/request"},
		{"OBOL", "0.023", "OBOL", "0.023 OBOL/request"},
		{"lowercase passed through", "0.5", "obol", "0.5 obol/request"},
		{"empty price stays empty", "", "OBOL", " OBOL/request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatPriceLogLine(tc.price, tc.symbol)
			if got != tc.want {
				t.Errorf("formatPriceLogLine(%q, %q) = %q; want %q", tc.price, tc.symbol, got, tc.want)
			}
		})
	}
}

func TestNewGateway_PreservesAssetSymbol(t *testing.T) {
	tests := []struct {
		name   string
		symbol string
		want   string
	}{
		{"explicit OBOL", "OBOL", "OBOL"},
		{"explicit USDC", "USDC", "USDC"},
		{"empty kept as empty (default applied at log site)", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, err := NewGateway(GatewayConfig{
				UpstreamURL:   "http://localhost:11434",
				WalletAddress: "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				AssetSymbol:   tc.symbol,
			})
			if err != nil {
				t.Fatalf("NewGateway: %v", err)
			}
			if gw.config.AssetSymbol != tc.want {
				t.Errorf("gw.config.AssetSymbol = %q; want %q", gw.config.AssetSymbol, tc.want)
			}
		})
	}
}
