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
