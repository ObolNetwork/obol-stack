package buyer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func TestProxy_HealthAndStatus(t *testing.T) {
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"test": {
				URL:     "http://localhost:9999",
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Asset:   "0xasset",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{
		"test": {makeAuth("0xsig1"), makeAuth("0xsig2")},
	}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	// Health check.
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("healthz: got %d, want 200", rec.Code)
	}

	// Status endpoint.
	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}

	var status map[string]struct {
		URL       string `json:"url"`
		Remaining int    `json:"remaining"`
		Spent     int    `json:"spent"`
		Network   string `json:"network"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	s, ok := status["test"]
	if !ok {
		t.Fatal("status missing 'test' upstream")
	}

	if s.Remaining != 2 {
		t.Errorf("remaining = %d, want 2", s.Remaining)
	}

	if s.Spent != 0 {
		t.Errorf("spent = %d, want 0", s.Spent)
	}

	if s.Network != "base-sepolia" {
		t.Errorf("network = %q, want base-sepolia", s.Network)
	}
}

func TestProxy_ForwardsToUpstream(t *testing.T) {
	// Mock upstream that returns 200 immediately (no payment required).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"test","path":%q}`, r.URL.Path)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"free": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Asset:   "0xasset",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"free": {makeAuth("0xsig1")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/free/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("got %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Model string `json:"model"`
		Path  string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if result.Path != "/v1/chat/completions" {
		t.Errorf("upstream saw path %q, want /v1/chat/completions", result.Path)
	}
}

func TestProxy_Handles402WithPayment(t *testing.T) {
	// Mock upstream: first request → 402, second request (with X-PAYMENT) → 200.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payment := r.Header.Get("X-Payment")
		if payment == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		// Verify payment header is valid base64 JSON.
		decoded, err := base64.StdEncoding.DecodeString(payment)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"bad base64: %v"}`, err)

			return
		}

		var envelope map[string]any
		if err := json.Unmarshal(decoded, &envelope); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"bad json: %v"}`, err)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{
			"id": "test-paid",
			"object": "chat.completion",
			"model": "test-model",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "paid"}, "finish_reason": "stop"}]
		}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {makeAuth("0xrealsig")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content != "paid" {
		t.Errorf("unexpected response: %s", string(body))
	}
}

func TestProxy_SuccessfulPaidRequestPersistsConsumeWithoutSettlementHeader(t *testing.T) {
	dir := t.TempDir()
	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	auth := makeAuth("0xsettle-ok")
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	statusRec := httptest.NewRecorder()
	proxy.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := status["paid"]; got.Remaining != 0 || got.Spent != 1 {
		t.Fatalf("remaining/spent = %d/%d, want 0/1", got.Remaining, got.Spent)
	}
	if !st.IsConsumed("paid", auth.Nonce) {
		t.Fatalf("nonce %s should be persisted as consumed after successful upstream response", auth.Nonce)
	}
}

func TestProxy_SettlementFailureHeaderIsIgnoredAndSuccessfulResponsePassesThrough(t *testing.T) {
	dir := t.TempDir()
	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settle" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"success":false,"errorReason":"settle_failed"}`)
	}))
	defer facilitator.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	auth := makeAuth("0xsettle-fail")
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// With main-style behavior, upstream success controls the client-visible
	// result and local consume is persisted even if settlement metadata is absent
	// or would have failed on a separate path.
	statusRec := httptest.NewRecorder()
	proxy.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := status["paid"]; got.Remaining != 0 || got.Spent != 1 {
		t.Fatalf("remaining/spent = %d/%d, want 0/1", got.Remaining, got.Spent)
	}
	if !st.IsConsumed("paid", auth.Nonce) {
		t.Fatalf("nonce %s should be persisted as consumed after successful upstream response", auth.Nonce)
	}
}

func TestProxy_VerifyFailureReleasesAuth(t *testing.T) {
	dir := t.TempDir()
	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	auth := makeAuth("0xverify-fail")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Payment") == "" {
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprint(w, `{
			"x402Version": 1,
			"error": "already_used",
			"accepts": [{
				"scheme": "exact",
				"network": "base-sepolia",
				"maxAmountRequired": "1000",
				"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
			}]
		}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 402, got %d: %s", resp.StatusCode, string(body))
	}

	statusRec := httptest.NewRecorder()
	proxy.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := status["paid"]; got.Remaining != 1 || got.Spent != 0 {
		t.Fatalf("remaining/spent = %d/%d, want 1/0", got.Remaining, got.Spent)
	}
	if st.IsConsumed("paid", auth.Nonce) {
		t.Fatalf("nonce %s should not be persisted as consumed on verify failure", auth.Nonce)
	}
}

func TestProxy_UpstreamErrorAfterPaymentDoesNotPersistConsume(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "consumed.json")

	st, err := LoadStateStore(statePath)
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	auth := makeAuth("0xupstream500")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		http.Error(w, "upstream failed", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 from upstream, got %d", resp.StatusCode)
	}

	raw, rerr := os.ReadFile(statePath)
	if rerr != nil && !os.IsNotExist(rerr) {
		t.Fatalf("read state: %v", rerr)
	}
	if strings.Contains(string(raw), auth.Nonce) {
		t.Fatalf("nonce should not be persisted after failed upstream; state=%q", string(raw))
	}
}

func TestProxy_UnknownUpstream(t *testing.T) {
	cfg := &Config{Upstreams: map[string]UpstreamConfig{}}
	auths := AuthsFile{}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/upstream/nonexistent/v1/chat/completions", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for unknown upstream", rec.Code)
	}
}

func TestLoadConfig(t *testing.T) {
	f := t.TempDir() + "/config.json"

	data := `{
		"upstreams": {
			"test": {
				"url": "http://seller.example.com",
				"network": "base-sepolia",
				"payTo": "0xaddr",
				"asset": "0xusdc",
				"price": "1000"
			}
		}
	}`
	if err := writeFile(t, f, data); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(f)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams = %d, want 1", len(cfg.Upstreams))
	}

	u := cfg.Upstreams["test"]
	if u.URL != "http://seller.example.com" || u.Price != "1000" {
		t.Errorf("unexpected upstream: %+v", u)
	}
}

func TestLoadAuths(t *testing.T) {
	f := t.TempDir() + "/auths.json"

	data := `{
		"test": [
			{
				"signature": "0xabc",
				"from": "0xfrom",
				"to": "0xto",
				"value": "1000",
				"validAfter": "0",
				"validBefore": "4294967295",
				"nonce": "0xdeadbeef"
			}
		]
	}`
	if err := writeFile(t, f, data); err != nil {
		t.Fatal(err)
	}

	auths, err := LoadAuths(f)
	if err != nil {
		t.Fatalf("LoadAuths: %v", err)
	}

	if len(auths["test"]) != 1 {
		t.Fatalf("auths[test] = %d, want 1", len(auths["test"]))
	}

	a := auths["test"][0]
	if a.Signature != "0xabc" || a.Nonce != "0xdeadbeef" {
		t.Errorf("unexpected auth: %+v", a)
	}
}

func TestProxy_AuthPoolExhaustion(t *testing.T) {
	// Mock upstream: always 402 first, 200 with payment.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"limited": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	// Only 2 auths in the pool.
	auths := AuthsFile{"limited": {makeAuth("0xsig1"), makeAuth("0xsig2")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// First 2 requests should succeed (each consumes one auth).
	for i := range 2 {
		resp, err := http.Post(
			srv.URL+"/upstream/limited/v1/chat/completions",
			"application/json",
			strings.NewReader(`{"model":"test"}`),
		)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}

		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	// 3rd request: pool exhausted. X402Transport gets "no signer can satisfy"
	// error from the selector, which the reverse proxy surfaces as 502.
	resp, err := http.Post(
		srv.URL+"/upstream/limited/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request 3: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("request 3: expected 502 (pool exhausted), got %d: %s", resp.StatusCode, string(body))
	}

	// Status should reflect 2 spent, 0 remaining.
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var status map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if s := status["limited"]; s.Remaining != 0 || s.Spent != 2 {
		t.Errorf("status: remaining=%d spent=%d, want 0/2", s.Remaining, s.Spent)
	}
}

func TestProxy_MultipleUpstreams(t *testing.T) {
	// Two different upstreams, each returning their own identifier.
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"upstream1"}`)
	}))
	defer upstream1.Close()

	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"upstream2"}`)
	}))
	defer upstream2.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"alpha": {URL: upstream1.URL, Network: "base-sepolia", PayTo: "0xa", Asset: "0xusdc", Price: "1000"},
			"beta":  {URL: upstream2.URL, Network: "base", PayTo: "0xb", Asset: "0xusdc2", Price: "2000"},
		},
	}
	auths := AuthsFile{
		"alpha": {makeAuth("0xsig_a")},
		"beta":  {makeAuth("0xsig_b")},
	}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Request to upstream alpha.
	resp1, err := http.Post(srv.URL+"/upstream/alpha/v1/completions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("upstream alpha: %v", err)
	}

	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	if !strings.Contains(string(body1), "upstream1") {
		t.Errorf("upstream alpha: got %s, want upstream1", string(body1))
	}

	// Request to upstream beta.
	resp2, err := http.Post(srv.URL+"/upstream/beta/v1/completions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("upstream beta: %v", err)
	}

	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if !strings.Contains(string(body2), "upstream2") {
		t.Errorf("upstream beta: got %s, want upstream2", string(body2))
	}

	// Status should show both upstreams.
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var status map[string]struct {
		URL     string `json:"url"`
		Network string `json:"network"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if len(status) != 2 {
		t.Errorf("status upstreams = %d, want 2", len(status))
	}

	if status["alpha"].Network != "base-sepolia" {
		t.Errorf("alpha network = %q", status["alpha"].Network)
	}

	if status["beta"].Network != "base" {
		t.Errorf("beta network = %q", status["beta"].Network)
	}
}

func TestProxy_StatusAfterPayments(t *testing.T) {
	// Mock upstream: 402 then 200 on payment.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"tracked": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"tracked": {makeAuth("0xs1"), makeAuth("0xs2"), makeAuth("0xs3")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Check initial status.
	checkStatus := func(wantRemaining, wantSpent int) {
		t.Helper()

		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

		var status map[string]struct {
			Remaining int `json:"remaining"`
			Spent     int `json:"spent"`
		}
		json.NewDecoder(rec.Body).Decode(&status)

		s := status["tracked"]
		if s.Remaining != wantRemaining || s.Spent != wantSpent {
			t.Errorf("status: remaining=%d spent=%d, want %d/%d",
				s.Remaining, s.Spent, wantRemaining, wantSpent)
		}
	}

	checkStatus(3, 0)

	// Make one paid request.
	resp, _ := http.Post(srv.URL+"/upstream/tracked/v1/chat/completions",
		"application/json", strings.NewReader(`{}`))
	resp.Body.Close()

	checkStatus(2, 1)

	// Make another.
	resp, _ = http.Post(srv.URL+"/upstream/tracked/v1/chat/completions",
		"application/json", strings.NewReader(`{}`))
	resp.Body.Close()

	checkStatus(1, 2)
}

func TestProxy_ModelRoutingAndMetrics(t *testing.T) {
	var seenModel string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		seenModel = payload.Model

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"seller-qwen": {
				URL:         upstream.URL,
				RemoteModel: "qwen3:32b",
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"seller-qwen": {makeAuth("0xpaid1")}}

	state, err := LoadStateStore(t.TempDir() + "/consumed.json")
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	proxy, err := NewProxy(cfg, auths, state)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/qwen3:32b","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if seenModel != "qwen3:32b" {
		t.Fatalf("upstream model = %q, want qwen3:32b", seenModel)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var status map[string]struct {
		RemoteModel string `json:"remote_model"`
		PublicModel string `json:"public_model"`
		Remaining   int    `json:"remaining"`
		Spent       int    `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	gotStatus := status["seller-qwen"]
	if gotStatus.RemoteModel != "qwen3:32b" {
		t.Fatalf("remote_model = %q, want qwen3:32b", gotStatus.RemoteModel)
	}

	if gotStatus.PublicModel != "paid/qwen3:32b" {
		t.Fatalf("public_model = %q, want paid/qwen3:32b", gotStatus.PublicModel)
	}

	if gotStatus.Remaining != 0 || gotStatus.Spent != 1 {
		t.Fatalf("status remaining/spent = %d/%d, want 0/1", gotStatus.Remaining, gotStatus.Spent)
	}

	metrics := scrapeMetricFamilies(t, proxy)
	labels := map[string]string{"upstream": "seller-qwen", "remote_model": "qwen3:32b"}
	assertMetricValue(t, metrics["obol_x402_buyer_requests_total"], labels, 1)
	assertMetricValue(t, metrics["obol_x402_buyer_payment_attempts_total"], labels, 1)
	assertMetricValue(t, metrics["obol_x402_buyer_payment_success_total"], labels, 1)
	assertMetricValue(t, metrics["obol_x402_buyer_auth_remaining"], labels, 0)
	assertMetricValue(t, metrics["obol_x402_buyer_auth_spent"], labels, 1)
	assertMetricValue(t, metrics["obol_x402_buyer_active_model_mappings"], labels, 1)
}

func TestProxy_ModelRoutingSupportsChatCompletionsAlias(t *testing.T) {
	var (
		seenPath  string
		seenModel string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		seenPath = r.URL.Path

		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		seenModel = payload.Model

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"seller-qwen": {
				URL:         upstream.URL,
				RemoteModel: "qwen3.5:9b",
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"seller-qwen": {makeAuth("0xpaid1")}}

	state, err := LoadStateStore(t.TempDir() + "/consumed.json")
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	proxy, err := NewProxy(cfg, auths, state)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/qwen3.5:9b","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if seenPath != "/chat/completions" {
		t.Fatalf("upstream path = %q, want /chat/completions", seenPath)
	}

	if seenModel != "qwen3.5:9b" {
		t.Fatalf("upstream model = %q, want qwen3.5:9b", seenModel)
	}
}

func TestProxy_ReloadSkipsConsumedAuthsAndReplacesModelMapping(t *testing.T) {
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer oldUpstream.Close()

	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":"new"}`)
	}))
	defer newUpstream.Close()

	state, err := LoadStateStore(t.TempDir() + "/consumed.json")
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"seller-old": {
				URL:         oldUpstream.URL,
				RemoteModel: "old-model",
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"seller-old": {makeAuth("0xold1"), makeAuth("0xold2")}}

	proxy, err := NewProxy(cfg, auths, state)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/old-model","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("old-model request: %v", err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("old-model expected 200, got %d", resp.StatusCode)
	}

	if err := proxy.Reload(cfg, auths); err != nil {
		t.Fatalf("Reload same config: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var status map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if status["seller-old"].Remaining != 1 || status["seller-old"].Spent != 1 {
		t.Fatalf("reload should preserve consumed state, got remaining/spent %d/%d",
			status["seller-old"].Remaining, status["seller-old"].Spent)
	}

	newCfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"seller-new": {
				URL:         newUpstream.URL,
				RemoteModel: "new-model",
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
			},
		},
	}

	newAuths := AuthsFile{"seller-new": {makeAuth("0xnew1")}}
	if err := proxy.Reload(newCfg, newAuths); err != nil {
		t.Fatalf("Reload new config: %v", err)
	}

	oldResp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/old-model","messages":[{"role":"user","content":"old"}]}`),
	)
	if err != nil {
		t.Fatalf("old model after reload: %v", err)
	}

	oldResp.Body.Close()

	if oldResp.StatusCode != http.StatusNotFound {
		t.Fatalf("old model after reload expected 404, got %d", oldResp.StatusCode)
	}

	newResp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/new-model","messages":[{"role":"user","content":"new"}]}`),
	)
	if err != nil {
		t.Fatalf("new model after reload: %v", err)
	}

	newResp.Body.Close()

	if newResp.StatusCode != http.StatusOK {
		t.Fatalf("new model after reload expected 200, got %d", newResp.StatusCode)
	}

	metrics := scrapeMetricFamilies(t, proxy)

	activeMappings := metrics["obol_x402_buyer_active_model_mappings"]
	if metricFamilyLen(activeMappings) != 1 {
		t.Fatalf("active model mapping series = %d, want 1", metricFamilyLen(activeMappings))
	}

	assertMetricValue(t, activeMappings, map[string]string{"upstream": "seller-new", "remote_model": "new-model"}, 1)
	assertMetricMissing(t, activeMappings, map[string]string{"upstream": "seller-old", "remote_model": "old-model"})
	assertMetricValue(t, metrics["obol_x402_buyer_auth_remaining"], map[string]string{"upstream": "seller-new", "remote_model": "new-model"}, 1)
	assertMetricMissing(t, metrics["obol_x402_buyer_auth_remaining"], map[string]string{"upstream": "seller-old", "remote_model": "old-model"})
}

func TestProxy_ReloadSamePurchasePreservesSpentAndAppendsAuthPool(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString([]byte(`{"success":true,"transaction":"0xtest","network":"base-sepolia","payer":"0xpayer"}`)))
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	state, err := LoadStateStore(t.TempDir() + "/consumed.json")
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"solo": {
				URL:         upstream.URL,
				RemoteModel: "qwen3.5:9b",
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"solo": {makeAuth("0xold1"), makeAuth("0xold2"), makeAuth("0xold3")}}

	proxy, err := NewProxy(cfg, auths, state)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Post(
			srv.URL+"/v1/chat/completions",
			"application/json",
			strings.NewReader(`{"model":"paid/qwen3.5:9b","messages":[{"role":"user","content":"hi"}]}`),
		)
		if err != nil {
			t.Fatalf("paid request %d: %v", i+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("paid request %d expected 200, got %d", i+1, resp.StatusCode)
		}
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var before map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode status before reload: %v", err)
	}
	if before["solo"].Remaining != 1 || before["solo"].Spent != 2 {
		t.Fatalf("status before reload = %d/%d, want 1/2", before["solo"].Remaining, before["solo"].Spent)
	}

	topUpAuths := AuthsFile{"solo": {makeAuth("0xold3"), makeAuth("0xnew1"), makeAuth("0xnew2")}}
	if err := proxy.Reload(cfg, topUpAuths); err != nil {
		t.Fatalf("Reload same purchase top-up: %v", err)
	}

	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var after map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatalf("decode status after reload: %v", err)
	}
	if after["solo"].Remaining != 3 || after["solo"].Spent != 2 {
		t.Fatalf("status after reload = %d/%d, want 3/2", after["solo"].Remaining, after["solo"].Spent)
	}
}

func TestProxy_ReloadRemovingUpstreamDropsStatusEntry(t *testing.T) {
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"solo": {
				URL:         "http://seller.example.com",
				RemoteModel: "qwen3.5:9b",
				Network:     "base-sepolia",
				PayTo:       "0xpayto",
				Asset:       "0xasset",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"solo": {makeAuth("0xone"), makeAuth("0xtwo")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var before map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode status before removal: %v", err)
	}
	if _, ok := before["solo"]; !ok {
		t.Fatal("status missing solo upstream before removal")
	}

	if err := proxy.Reload(&Config{Upstreams: map[string]UpstreamConfig{}}, AuthsFile{}); err != nil {
		t.Fatalf("Reload empty config: %v", err)
	}

	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	var after map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatalf("decode status after removal: %v", err)
	}
	if _, ok := after["solo"]; ok {
		t.Fatalf("status still contains solo after removal: %#v", after["solo"])
	}
}

func TestProxy_AdminRemoveDropsStatusEntry(t *testing.T) {
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"solo": {
				URL:         "http://seller.example.com",
				RemoteModel: "qwen3.5:9b",
				Network:     "base-sepolia",
				PayTo:       "0xpayto",
				Asset:       "0xasset",
				Price:       "1000",
			},
		},
	}
	auths := AuthsFile{"solo": {makeAuth("0xone"), makeAuth("0xtwo")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var before map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode status before admin remove: %v", err)
	}
	if _, ok := before["solo"]; !ok {
		t.Fatal("status missing solo before admin remove")
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/remove?name=solo", nil)
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin remove status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var after map[string]struct {
		Remaining int `json:"remaining"`
		Spent     int `json:"spent"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&after); err != nil {
		t.Fatalf("decode status after admin remove: %v", err)
	}
	if _, ok := after["solo"]; ok {
		t.Fatalf("status still contains solo after admin remove: %#v", after["solo"])
	}
}

func scrapeMetricFamilies(t *testing.T, proxy *Proxy) map[string]*dto.MetricFamily {
	t.Helper()

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

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

func assertMetricValue(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string, wantValue float64) {
	t.Helper()

	if family == nil {
		t.Fatalf("missing metric family")
	}

	for _, metric := range family.GetMetric() {
		if labelsMatch(metric, wantLabels) {
			got := metricValue(metric)
			if got != wantValue {
				t.Fatalf("%s labels %v = %v, want %v", family.GetName(), wantLabels, got, wantValue)
			}

			return
		}
	}

	t.Fatalf("metric %s missing labels %v", family.GetName(), wantLabels)
}

func assertMetricMissing(t *testing.T, family *dto.MetricFamily, wantLabels map[string]string) {
	t.Helper()

	if family == nil {
		return
	}

	for _, metric := range family.GetMetric() {
		if labelsMatch(metric, wantLabels) {
			t.Fatalf("metric %s unexpectedly contained labels %v", family.GetName(), wantLabels)
		}
	}
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
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

func metricValue(metric *dto.Metric) float64 {
	switch {
	case metric.GetCounter() != nil:
		return metric.GetCounter().GetValue()
	case metric.GetGauge() != nil:
		return metric.GetGauge().GetValue()
	default:
		return 0
	}
}

func metricFamilyLen(family *dto.MetricFamily) int {
	if family == nil {
		return 0
	}

	return len(family.GetMetric())
}

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestProxy_AdminReload(t *testing.T) {
	cfg := &Config{Upstreams: map[string]UpstreamConfig{}}
	auths := AuthsFile{}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("admin/reload: got %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "reload triggered") {
		t.Errorf("body = %q, want 'reload triggered'", body)
	}

	// Channel should have a signal.
	select {
	case <-proxy.ReloadCh():
		// expected
	default:
		t.Error("expected reload signal on channel")
	}
}

func TestProxy_AdminReloadIdempotent(t *testing.T) {
	cfg := &Config{Upstreams: map[string]UpstreamConfig{}}
	auths := AuthsFile{}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	// First request: should get "reload triggered".
	rec1 := httptest.NewRecorder()
	proxy.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))
	if rec1.Code != http.StatusOK {
		t.Errorf("first admin/reload: got %d, want 200", rec1.Code)
	}

	// Second request without draining the channel: "already pending".
	rec2 := httptest.NewRecorder()
	proxy.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("second admin/reload: got %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "already pending") {
		t.Errorf("body = %q, want 'already pending'", rec2.Body.String())
	}

	// Drain the channel.
	<-proxy.ReloadCh()

	// Third request: "reload triggered" again.
	rec3 := httptest.NewRecorder()
	proxy.ServeHTTP(rec3, httptest.NewRequest(http.MethodPost, "/admin/reload", nil))
	if !strings.Contains(rec3.Body.String(), "reload triggered") {
		t.Errorf("body = %q, want 'reload triggered'", rec3.Body.String())
	}
}

// TestProxy_UpstreamSuccessNoSettlementHeader_IncrementsUnsettledMetric verifies
// that a seller returning 2xx without X-PAYMENT-RESPONSE surfaces the condition
// via the paymentUnsettledConfirmations counter. Current post-#343 semantics
// consume the auth locally regardless of settlement, so the metric is the only
// operator-visible signal that no on-chain settlement was observed.
//
// This is the signal that was missing in the initial PR review (W2/W9): a
// misconfigured or malicious seller could otherwise collect payment without
// settling, with no alarm bell for the buyer.
func TestProxy_UpstreamSuccessNoSettlementHeader_IncrementsUnsettledMetric(t *testing.T) {
	dir := t.TempDir()

	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"x402Version":1,"accepts":[{"scheme":"exact","network":"base-sepolia","maxAmountRequired":"1000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"}]}`)

			return
		}
		// No X-PAYMENT-RESPONSE set — simulates a seller that forgot to
		// emit the header or runs a VerifyOnly ForwardAuth gate (Traefik).
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	auth := makeAuth("0xunsettled")
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// The auth should have been consumed (payment went through).
	if !st.IsConsumed("paid", auth.Nonce) {
		t.Fatalf("nonce %s should be persisted as consumed after successful upstream response", auth.Nonce)
	}

	// The unsettled metric should have incremented exactly once.
	metrics := scrapeMetricFamilies(t, proxy)

	family := metrics["obol_x402_buyer_payment_unsettled_confirmations_total"]
	if family == nil {
		t.Fatalf("metric obol_x402_buyer_payment_unsettled_confirmations_total not registered")
	}

	assertMetricValue(t, family, map[string]string{
		"upstream":     "paid",
		"remote_model": "paid",
	}, 1)
}

// TestProxy_UpstreamSuccessWithSettlementHeader_DoesNotIncrementUnsettledMetric
// is the negative control: when the seller does emit a successful
// X-PAYMENT-RESPONSE, the unsettled counter must remain zero. Paired with the
// previous test, this pins down the invariant that unsettled is fired exclusively
// when the buyer consumes an auth without observing on-chain settlement.
func TestProxy_UpstreamSuccessWithSettlementHeader_DoesNotIncrementUnsettledMetric(t *testing.T) {
	dir := t.TempDir()

	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"x402Version":1,"accepts":[{"scheme":"exact","network":"base-sepolia","maxAmountRequired":"1000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"}]}`)

			return
		}
		// Seller emits X-PAYMENT-RESPONSE encoding Success=true — the happy
		// settle-aware path.
		settleJSON := `{"success":true,"transaction":"0xabc","network":"base-sepolia","payer":"0xdef"}`
		w.Header().Set("X-Payment-Response", base64.StdEncoding.EncodeToString([]byte(settleJSON)))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	auth := makeAuth("0xsettled")
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Unsettled counter must NOT have incremented.
	metrics := scrapeMetricFamilies(t, proxy)
	assertMetricMissing(t, metrics["obol_x402_buyer_payment_unsettled_confirmations_total"], map[string]string{
		"upstream":     "paid",
		"remote_model": "paid",
	})
}

func TestProxy_ConfirmSpendFailure_IncrementsMetric(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "consumed.json")

	st, err := LoadStateStore(statePath)
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	// Force StateStore.writeLocked to fail deterministically by pre-creating
	// the target state path as a directory: os.Rename(tmpfile, dir) returns
	// EISDIR, which root cannot bypass. The previous 0o500-dir approach was
	// silently skipped under CAP_DAC_OVERRIDE when tests run as uid 0.
	if err := os.Mkdir(statePath, 0o755); err != nil {
		t.Fatalf("block state path: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"x402Version":1,"accepts":[{"scheme":"exact","network":"base-sepolia","maxAmountRequired":"1000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"}]}`)
			return
		}

		settleJSON := `{"success":true,"transaction":"0xabc","network":"base-sepolia","payer":"0xdef"}`
		w.Header().Set("X-Payment-Response", base64.StdEncoding.EncodeToString([]byte(settleJSON)))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	auth := makeAuth("0xconfirmfail")
	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {auth}}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"test"}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	metrics := scrapeMetricFamilies(t, proxy)
	assertMetricValue(t, metrics["obol_x402_buyer_confirm_spend_failure_total"], map[string]string{
		"upstream":     "paid",
		"remote_model": "paid",
	}, 1)
}

// TestProxy_OpenAIMuxSymmetry_BothV1AndBarePathsRouteIdentically pins down the
// invariant that the buyer sidecar serves /chat/completions AND
// /v1/chat/completions. The LiteLLM templates hardcode api_base with /v1
// (internal/embed/infrastructure/base/templates/llm.yaml) as a defence against
// stale :latest buyer images, but if either path is ever dropped from the
// mux the template hardcode becomes load-bearing on a technicality — this
// test catches that regression early.
//
// Corresponds to W4 / "Buyer mux symmetry" in the PR #343 review.
func TestProxy_OpenAIMuxSymmetry_BothV1AndBarePathsRouteIdentically(t *testing.T) {
	dir := t.TempDir()

	st, err := LoadStateStore(filepath.Join(dir, "consumed.json"))
	if err != nil {
		t.Fatalf("LoadStateStore: %v", err)
	}

	upstreamCalls := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++

		if r.Header.Get("X-Payment") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"x402Version":1,"accepts":[{"scheme":"exact","network":"base-sepolia","maxAmountRequired":"1000","asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","payTo":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"}]}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid-upstream": {
				URL:         upstream.URL,
				Network:     "base-sepolia",
				PayTo:       "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:       "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:       "1000",
				RemoteModel: "qwen3.5:4b",
			},
		},
	}
	auths := AuthsFile{
		"paid-upstream": {makeAuth("0xbare"), makeAuth("0xv1")},
	}

	proxy, err := NewProxy(cfg, auths, st)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Hit the bare path (no /v1 prefix) — simulates LiteLLM configured with
	// api_base="http://127.0.0.1:8402".
	bareResp, err := http.Post(
		srv.URL+"/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/qwen3.5:4b"}`),
	)
	if err != nil {
		t.Fatalf("bare request: %v", err)
	}

	bareResp.Body.Close()

	if bareResp.StatusCode != http.StatusOK {
		t.Fatalf("bare /chat/completions: got %d, want 200", bareResp.StatusCode)
	}

	// Hit the /v1 path — simulates LiteLLM configured with
	// api_base="http://127.0.0.1:8402/v1".
	v1Resp, err := http.Post(
		srv.URL+"/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid/qwen3.5:4b"}`),
	)
	if err != nil {
		t.Fatalf("v1 request: %v", err)
	}

	v1Resp.Body.Close()

	if v1Resp.StatusCode != http.StatusOK {
		t.Fatalf("/v1/chat/completions: got %d, want 200", v1Resp.StatusCode)
	}
}

// TestProxy_UserAgentOnProbeRequest asserts that the initial (unpaid) request
// to an upstream carries the obol-buy-x402 User-Agent. Sellers behind
// Cloudflare WAF block the Go stdlib default UA ("Go-http-client/1.1") with
// HTTP 403 + error code 1010; this was the root cause of the v1337 demo
// failure fixed alongside buy.py in c2dddc1.
func TestProxy_UserAgentOnProbeRequest(t *testing.T) {
	var capturedUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"free": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Asset:   "0xasset",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"free": {makeAuth("0xsig1")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/free/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"free","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	const wantUA = "obol-buy-x402/1.0 (+https://github.com/ObolNetwork/obol-stack)"
	if capturedUA != wantUA {
		t.Errorf("probe User-Agent = %q, want %q", capturedUA, wantUA)
	}
}

// TestProxy_UserAgentOnPaidRequest asserts that the paid retry request
// (the one carrying X-PAYMENT) also carries the obol-buy-x402 User-Agent.
// Both the probe and the paid request must pass Cloudflare WAF bot-filtering.
func TestProxy_UserAgentOnPaidRequest(t *testing.T) {
	var capturedProbeUA, capturedPaidUA string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Payment") == "" {
			capturedProbeUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{
				"x402Version": 1,
				"accepts": [{
					"scheme": "exact",
					"network": "base-sepolia",
					"maxAmountRequired": "1000",
					"asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
					"payTo": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
				}]
			}`)
			return
		}
		capturedPaidUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PAYMENT-RESPONSE", base64.StdEncoding.EncodeToString(
			[]byte(`{"success":true,"transaction":"0xtx","network":"base-sepolia","payer":"0xpayer"}`),
		))
		fmt.Fprint(w, `{"id":"paid","choices":[{"message":{"content":"paid"}}]}`)
	}))
	defer upstream.Close()

	cfg := &Config{
		Upstreams: map[string]UpstreamConfig{
			"paid": {
				URL:     upstream.URL,
				Network: "base-sepolia",
				PayTo:   "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
				Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				Price:   "1000",
			},
		},
	}
	auths := AuthsFile{"paid": {makeAuth("0xuasig")}}

	proxy, err := NewProxy(cfg, auths, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	srv := httptest.NewServer(proxy)
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/upstream/paid/v1/chat/completions",
		"application/json",
		strings.NewReader(`{"model":"paid","messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	const wantUA = "obol-buy-x402/1.0 (+https://github.com/ObolNetwork/obol-stack)"
	if capturedProbeUA != wantUA {
		t.Errorf("probe User-Agent = %q, want %q", capturedProbeUA, wantUA)
	}
	if capturedPaidUA != wantUA {
		t.Errorf("paid User-Agent = %q, want %q", capturedPaidUA, wantUA)
	}
}
