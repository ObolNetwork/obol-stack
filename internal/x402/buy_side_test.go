package x402

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"

	"github.com/ObolNetwork/obol-stack/internal/testutil"
	"github.com/ObolNetwork/obol-stack/internal/x402/buyer"
)

// TestBuySidecar_EndToEnd verifies the complete buyer sidecar flow:
//
//  1. Mock seller returns 402 on unpaid requests, 200 on paid
//  2. Buyer proxy has pre-signed auths (real EIP-712 signatures)
//  3. Request through proxy → 402 → signer pops auth → X-PAYMENT → retry → 200
//  4. Payment envelope is valid base64 JSON with correct wire format
func TestBuySidecar_EndToEnd(t *testing.T) {
	seller := startMockX402Seller(t)
	t.Logf("Mock seller on port %d", seller.port)

	// Create pre-signed auth with real EIP-712 signature.
	buyerKey := "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80" // Anvil #0
	payTo := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	auth := signPreSignedAuth(t, buyerKey, payTo, "1000", 84532)

	cfg := &buyer.Config{
		Upstreams: map[string]buyer.UpstreamConfig{
			"test-seller": {
				URL:     fmt.Sprintf("http://127.0.0.1:%d", seller.port),
				Network: "base-sepolia",
				PayTo:   payTo,
				Asset:   testutil.USDCBaseSepolia,
				Price:   "1000",
			},
		},
	}
	auths := buyer.AuthsFile{"test-seller": {auth}}

	proxy, err := buyer.NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Send request through proxy → should get 200 (proxy handles 402 internally).
	resp, err := http.Post(
		srv.URL+"/upstream/test-seller/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Parse response.
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, string(body))
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content != "paid response" {
		t.Errorf("unexpected response: %s", string(body))
	}

	// Verify seller received both unpaid and paid requests.
	if seller.unpaidRequests.Load() == 0 {
		t.Error("seller received 0 unpaid requests (should have gotten initial 402)")
	}

	if seller.paidRequests.Load() == 0 {
		t.Error("seller received 0 paid requests")
	}

	t.Logf("Seller requests: unpaid=%d, paid=%d",
		seller.unpaidRequests.Load(), seller.paidRequests.Load())

	// Verify payment envelope structure from the seller's perspective.
	seller.mu.Lock()
	lastPayment := seller.lastPaymentJSON
	seller.mu.Unlock()

	if lastPayment == "" {
		t.Fatal("seller did not record a payment")
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(lastPayment), &envelope); err != nil {
		t.Fatalf("parse payment envelope: %v", err)
	}

	// Check wire format fields.
	if v, ok := envelope["x402Version"]; !ok || v != float64(2) {
		t.Errorf("x402Version = %v, want 2", v)
	}

	accepted, ok := envelope["accepted"].(map[string]any)
	if !ok {
		t.Fatal("accepted missing or wrong type")
	}

	if v, ok := accepted["scheme"]; !ok || v != "exact" {
		t.Errorf("accepted.scheme = %v, want exact", v)
	}

	if v, ok := accepted["network"]; !ok || v != "eip155:84532" {
		t.Errorf("accepted.network = %v, want eip155:84532", v)
	}

	if v, ok := accepted["amount"]; !ok || v != "1000" {
		t.Errorf("accepted.amount = %v, want 1000", v)
	}

	if v, ok := accepted["payTo"]; !ok || v != payTo {
		t.Errorf("accepted.payTo = %v, want %s", v, payTo)
	}

	// Check payload has authorization with correct fields.
	payload, ok := envelope["payload"].(map[string]any)
	if !ok {
		t.Fatal("payload missing or wrong type")
	}

	if _, ok := payload["signature"]; !ok {
		t.Error("payload.signature missing")
	}

	authz, ok := payload["authorization"].(map[string]any)
	if !ok {
		t.Fatal("payload.authorization missing or wrong type")
	}

	if authz["to"] != payTo {
		t.Errorf("authorization.to = %v, want %s", authz["to"], payTo)
	}

	t.Log("End-to-end sidecar flow complete: request → 402 → sign → retry → 200")
}

// TestBuySidecar_MultiplePayments sends multiple requests through the sidecar,
// each consuming one pre-signed auth, and verifies unique nonces.
func TestBuySidecar_MultiplePayments(t *testing.T) {
	seller := startMockX402Seller(t)

	buyerKey := "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	payTo := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	// Create 3 unique pre-signed auths.
	var authPool []*buyer.PreSignedAuth
	for range 3 {
		authPool = append(authPool, signPreSignedAuth(t, buyerKey, payTo, "1000", 84532))
	}

	cfg := &buyer.Config{
		Upstreams: map[string]buyer.UpstreamConfig{
			"multi": {
				URL:     fmt.Sprintf("http://127.0.0.1:%d", seller.port),
				Network: "base-sepolia",
				PayTo:   payTo,
				Asset:   testutil.USDCBaseSepolia,
				Price:   "1000",
			},
		},
	}
	auths := buyer.AuthsFile{"multi": authPool}

	proxy, err := buyer.NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Send 3 requests, each should succeed.
	for i := range 3 {
		resp, err := http.Post(
			srv.URL+"/upstream/multi/v1/chat/completions",
			"application/json",
			strings.NewReader(fmt.Sprintf(`{"model":"test","messages":[{"role":"user","content":"msg%d"}]}`, i)),
		)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i+1, resp.StatusCode, string(body))
		}
	}

	if seller.paidRequests.Load() != 3 {
		t.Errorf("expected 3 paid requests, got %d", seller.paidRequests.Load())
	}

	t.Log("Multiple payments through sidecar: 3 unique auths consumed")
}

// TestBuySidecar_PoolExhaustion verifies that when all pre-signed auths are
// consumed, the proxy returns 402 (the upstream's 402 passes through).
func TestBuySidecar_PoolExhaustion(t *testing.T) {
	seller := startMockX402Seller(t)

	buyerKey := "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	payTo := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	// Only 1 auth in the pool.
	auth := signPreSignedAuth(t, buyerKey, payTo, "1000", 84532)

	cfg := &buyer.Config{
		Upstreams: map[string]buyer.UpstreamConfig{
			"exhaust": {
				URL:     fmt.Sprintf("http://127.0.0.1:%d", seller.port),
				Network: "base-sepolia",
				PayTo:   payTo,
				Asset:   testutil.USDCBaseSepolia,
				Price:   "1000",
			},
		},
	}
	auths := buyer.AuthsFile{"exhaust": {auth}}

	proxy, err := buyer.NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// First request: should succeed.
	resp1, err := http.Post(srv.URL+"/upstream/exhaust/v1/chat/completions",
		"application/json", strings.NewReader(`{"model":"test"}`))
	if err != nil {
		t.Fatalf("request 1: %v", err)
	}

	resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("request 1: expected 200, got %d", resp1.StatusCode)
	}

	// Second request: pool exhausted. X402Transport gets "no signer can
	// satisfy" error → reverse proxy returns 502.
	resp2, err := http.Post(srv.URL+"/upstream/exhaust/v1/chat/completions",
		"application/json", strings.NewReader(`{"model":"test"}`))
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("request 2: expected 502 (exhausted), got %d: %s", resp2.StatusCode, string(body))
	}

	t.Log("Pool exhaustion verified: 1 auth consumed, 2nd request returns 502")
}

// TestBuySidecar_Probe verifies that probing a seller without the sidecar
// returns a well-formed 402 with pricing info.
func TestBuySidecar_Probe(t *testing.T) {
	seller := startMockX402Seller(t)

	// Direct probe (no sidecar, no payment).
	resp := httpPost(t,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", seller.port),
		`{"model":"test","messages":[{"role":"user","content":"probe"}]}`,
		nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 402, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)

	var pricing struct {
		X402Version int `json:"x402Version"`
		Accepts     []struct {
			Scheme  string `json:"scheme"`
			PayTo   string `json:"payTo"`
			Network string `json:"network"`
			Amount  string `json:"maxAmountRequired"`
			Asset   string `json:"asset"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(body, &pricing); err != nil {
		t.Fatalf("parse 402: %v\n%s", err, string(body))
	}

	if pricing.X402Version != 1 {
		t.Errorf("x402Version = %d, want 1", pricing.X402Version)
	}

	if len(pricing.Accepts) == 0 {
		t.Fatal("no accepts[] in 402 response")
	}

	a := pricing.Accepts[0]
	if a.PayTo != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
		t.Errorf("payTo = %q", a.PayTo)
	}

	if a.Amount != "1000" {
		t.Errorf("amount = %q", a.Amount)
	}

	t.Logf("Probe: payTo=%s, amount=%s, network=%s, asset=%s", a.PayTo, a.Amount, a.Network, a.Asset)
}

// ── Mock x402 seller ────────────────────────────────────────────────────────

type mockX402Seller struct {
	port           int
	unpaidRequests atomic.Int32
	paidRequests   atomic.Int32

	mu              sync.Mutex
	lastPaymentJSON string
}

// startMockX402Seller starts a simple HTTP server that:
//   - Returns 402 with pricing if X-PAYMENT header is absent
//   - Returns 200 with a mock OpenAI response if X-PAYMENT is present and valid base64 JSON
func startMockX402Seller(t *testing.T) *mockX402Seller {
	t.Helper()

	ms := &mockX402Seller{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		paymentHeader := r.Header.Get("X-Payment")

		if paymentHeader == "" {
			// No payment → 402.
			ms.unpaidRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprintf(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": %q
				}]
			}`, testutil.USDCBaseSepolia)

			return
		}

		// Validate payment header is well-formed base64 JSON.
		decoded, err := base64.StdEncoding.DecodeString(paymentHeader)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid base64 in X-PAYMENT: %v"}`, err)

			return
		}

		var envelope map[string]any
		if err := json.Unmarshal(decoded, &envelope); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid JSON in X-PAYMENT: %v"}`, err)

			return
		}

		// Verify required fields.
		if _, ok := envelope["x402Version"]; !ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"missing x402Version in payment"}`)

			return
		}

		if _, ok := envelope["payload"]; !ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"missing payload in payment"}`)

			return
		}

		ms.mu.Lock()
		ms.lastPaymentJSON = string(decoded)
		ms.mu.Unlock()
		ms.paidRequests.Add(1)

		// Return mock OpenAI-compatible response.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "mock-buy-test",
			"object": "chat.completion",
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "paid response"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}
		}`)
	})

	// Listen on 0.0.0.0 so k3d containers can reach us.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ms.port = l.Addr().(*net.TCPAddr).Port

	srv := &http.Server{Handler: mux, ReadTimeout: 30 * time.Second}

	go func() { _ = srv.Serve(l) }()

	t.Cleanup(func() { srv.Close() })

	return ms
}

// ── EIP-712 auth signing helper ─────────────────────────────────────────────

// signPreSignedAuth creates a buyer.PreSignedAuth with a real EIP-712
// TransferWithAuthorization signature, matching the x402 wire format.
func signPreSignedAuth(t *testing.T, signerKeyHex, payTo, amount string, chainID int64) *buyer.PreSignedAuth {
	t.Helper()

	key, err := crypto.HexToECDSA(signerKeyHex)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	fromAddr := crypto.PubkeyToAddress(key.PublicKey)

	// Generate random 32-byte nonce.
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}

	nonceHex := fmt.Sprintf("0x%x", nonce)

	// Build EIP-712 typed data for TransferWithAuthorization (ERC-3009).
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"TransferWithAuthorization": {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: "TransferWithAuthorization",
		Domain: apitypes.TypedDataDomain{
			Name:              "USDC",
			Version:           "2",
			ChainId:           math.NewHexOrDecimal256(chainID),
			VerifyingContract: testutil.USDCBaseSepolia,
		},
		Message: apitypes.TypedDataMessage{
			"from":        fromAddr.Hex(),
			"to":          payTo,
			"value":       amount,
			"validAfter":  "0",
			"validBefore": "4294967295",
			"nonce":       nonceHex,
		},
	}

	hash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatalf("TypedDataAndHash: %v", err)
	}

	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	sig[64] += 27 // Ethereum v convention
	sigHex := fmt.Sprintf("0x%x", sig)

	return &buyer.PreSignedAuth{
		Signature:   sigHex,
		From:        fromAddr.Hex(),
		To:          payTo,
		Value:       amount,
		ValidAfter:  "0",
		ValidBefore: "4294967295",
		Nonce:       nonceHex,
	}
}
