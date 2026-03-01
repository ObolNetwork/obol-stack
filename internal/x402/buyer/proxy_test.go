package buyer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

	proxy, err := NewProxy(cfg, auths)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	// Health check.
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: got %d, want 200", rec.Code)
	}

	// Status endpoint.
	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
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

	proxy, err := NewProxy(cfg, auths)
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
		payment := r.Header.Get("X-PAYMENT")
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
		var envelope map[string]interface{}
		if err := json.Unmarshal(decoded, &envelope); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"bad json: %v"}`, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
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

	proxy, err := NewProxy(cfg, auths)
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

func TestProxy_UnknownUpstream(t *testing.T) {
	cfg := &Config{Upstreams: map[string]UpstreamConfig{}}
	auths := AuthsFile{}

	proxy, err := NewProxy(cfg, auths)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("POST", "/upstream/nonexistent/v1/chat/completions", nil))
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
		if r.Header.Get("X-PAYMENT") == "" {
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

	proxy, err := NewProxy(cfg, auths)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// First 2 requests should succeed (each consumes one auth).
	for i := 0; i < 2; i++ {
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
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
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

	proxy, err := NewProxy(cfg, auths)
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
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
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
		if r.Header.Get("X-PAYMENT") == "" {
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

	proxy, err := NewProxy(cfg, auths)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	srv := httptest.NewServer(proxy)
	defer srv.Close()

	// Check initial status.
	checkStatus := func(wantRemaining, wantSpent int) {
		t.Helper()
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
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

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0644)
}
