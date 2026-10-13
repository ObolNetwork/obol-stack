package x402

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// capturedRequest is what a fake upstream saw.
type capturedRequest struct {
	path, rawQuery, auth, contentType string
	header                            http.Header
	body                              map[string]any
	calls                             int
}

func captureUpstream(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.calls++
		got.path = r.URL.Path
		got.rawQuery = r.URL.RawQuery
		got.auth = r.Header.Get("Authorization")
		got.contentType = r.Header.Get("Content-Type")
		got.header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		got.body = nil
		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("upstream body is not JSON: %q", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func paidLiteLLMRequest(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	return req
}

// The buyer's model choice never reaches LiteLLM: the seller-pinned model
// replaces it on every allowlisted path, and every other channel LiteLLM
// reads a model from (query string, fallbacks, extra_body, x-litellm-*
// headers) is stripped.
func TestVerifier_HandleProxy_LiteLLMPinsSellerModel(t *testing.T) {
	cases := []struct {
		name, path, body string
		wantPath         string
		keep, drop       []string
	}{
		{
			name: "chat overrides buyer model",
			path: "/services/llm/v1/chat/completions?model=gpt-5",
			body: `{"model":"claude-opus","messages":[{"role":"user","content":"hi"}],"temperature":0.2,` +
				`"fallbacks":["gpt-5"],"context_window_fallbacks":[{"qwen3.5:9b":["gpt-5"]}],` +
				`"extra_body":{"model":"o1-pro"},"custom_llm_provider":"anthropic","metadata":{"tags":["x"]},` +
				`"user_config":{"model_list":[]},"api_base":"http://evil","n":8}`,
			wantPath: "/v1/chat/completions",
			keep:     []string{"messages", "temperature"},
			drop:     []string{"fallbacks", "context_window_fallbacks", "extra_body", "custom_llm_provider", "metadata", "user_config", "api_base", "n"},
		},
		{
			name:     "chat without v1, model missing",
			path:     "/services/llm/chat/completions",
			body:     `{"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			wantPath: "/chat/completions",
			keep:     []string{"messages", "stream"},
		},
		{
			name:     "embeddings",
			path:     "/services/llm/v1/embeddings",
			body:     `{"model":"text-embedding-3-large","input":["a","b"],"dimensions":256,"messages":[]}`,
			wantPath: "/v1/embeddings",
			keep:     []string{"input", "dimensions"},
			drop:     []string{"messages"},
		},
		{
			name:     "legacy completions, duplicate model keys",
			path:     "/services/llm/v1/completions",
			body:     `{"model":"qwen3.5:9b","prompt":"hi","model":"gpt-5","n":4,"best_of":16}`,
			wantPath: "/v1/completions",
			keep:     []string{"prompt"},
			drop:     []string{"n", "best_of"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fac := newMockFacilitator(t, mockFacilitatorOpts{})
			upstream, got := captureUpstream(t)
			v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule(upstream.URL)})

			req := paidLiteLLMRequest(t, tc.path, tc.body)
			req.Header.Set("X-LiteLLM-Tags", "premium")
			req.Header.Set("x-litellm-model", "gpt-5")
			w := httptest.NewRecorder()
			v.HandleProxy(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
			}
			if got.path != tc.wantPath {
				t.Fatalf("upstream path = %q, want %q", got.path, tc.wantPath)
			}
			if got.body["model"] != "qwen3.5:9b" {
				t.Fatalf("upstream model = %v, want the seller-pinned qwen3.5:9b", got.body["model"])
			}
			for _, k := range tc.keep {
				if _, ok := got.body[k]; !ok {
					t.Errorf("field %q must be forwarded", k)
				}
			}
			for _, k := range tc.drop {
				if _, ok := got.body[k]; ok {
					t.Errorf("field %q must be dropped, upstream saw %v", k, got.body[k])
				}
			}
			if got.rawQuery != "" {
				t.Errorf("query string must not reach LiteLLM, got %q", got.rawQuery)
			}
			for name := range got.header {
				if strings.HasPrefix(strings.ToLower(name), "x-litellm-") {
					t.Errorf("header %s must not reach LiteLLM", name)
				}
			}
			if got.auth != "Bearer sk-master" {
				t.Errorf("upstream auth = %q", got.auth)
			}
			if got.contentType != "application/json" {
				t.Errorf("upstream content-type = %q", got.contentType)
			}
		})
	}
}

// Requests the gateway can't pin are refused before the 402 challenge, so
// nobody is asked to pay for them and nothing reaches LiteLLM.
func TestVerifier_HandleProxy_LiteLLMBodyRefusedBeforePayment(t *testing.T) {
	cases := []struct {
		name, method, body, contentEncoding string
		model                               string
		want                                int
	}{
		{name: "offer without model", method: http.MethodPost, body: `{"messages":[]}`, model: "", want: http.StatusServiceUnavailable},
		{name: "non-JSON body", method: http.MethodPost, body: `model=gpt-5`, model: "m", want: http.StatusBadRequest},
		{name: "JSON array", method: http.MethodPost, body: `[{"model":"x"}]`, model: "m", want: http.StatusBadRequest},
		{name: "empty body", method: http.MethodPost, body: ``, model: "m", want: http.StatusBadRequest},
		{name: "compressed body", method: http.MethodPost, body: `{}`, contentEncoding: "gzip", model: "m", want: http.StatusBadRequest},
		// The path allowlist is POST-only, so a GET 404s before the body policy.
		{name: "GET", method: http.MethodGet, model: "m", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fac := newMockFacilitator(t, mockFacilitatorOpts{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("upstream must not be called, got %s %s", r.Method, r.URL.Path)
			}))
			defer upstream.Close()

			rule := liteLLMRule(upstream.URL)
			rule.Model = tc.model
			v := newTestVerifier(t, fac.URL, []RouteRule{rule})

			for _, paid := range []bool{false, true} {
				req := httptest.NewRequest(tc.method, "/services/llm/v1/chat/completions", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				if tc.contentEncoding != "" {
					req.Header.Set("Content-Encoding", tc.contentEncoding)
				}
				if paid {
					req.Header.Set("X-PAYMENT", testPaymentHeader(t))
				}
				w := httptest.NewRecorder()
				v.HandleProxy(w, req)
				if w.Code != tc.want {
					t.Fatalf("paid=%v: status = %d, want %d (body %q)", paid, w.Code, tc.want, w.Body.String())
				}
				if tc.model == "" && !strings.Contains(w.Body.String(), "obol sell update llm -n llm --model") {
					t.Fatalf("misconfigured-offer error must tell the seller how to fix it, got %q", w.Body.String())
				}
			}
			if n := fac.verifyCalls.Load(); n != 0 {
				t.Fatalf("facilitator verify called %d times for refused requests", n)
			}
		})
	}
}

func TestVerifier_HandleProxy_LiteLLMBodyTooLarge(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule("http://127.0.0.1:1")})

	big := `{"messages":"` + strings.Repeat("a", upstreamBodyLimit) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/services/llm/v1/chat/completions", strings.NewReader(big))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

// The 402 for a LiteLLM-backed offer advertises the pinned model: in
// accepts[].extra.model and in the bazaar chat example.
func TestVerifier_HandleProxy_LiteLLM402AdvertisesModel(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule("http://127.0.0.1:1")})

	req := httptest.NewRequest(http.MethodPost, "/services/llm/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	var body struct {
		Accepts []struct {
			Extra map[string]any `json:"extra"`
		} `json:"accepts"`
		Extensions map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 402: %v (%s)", err, w.Body.String())
	}
	if len(body.Accepts) == 0 || body.Accepts[0].Extra["model"] != "qwen3.5:9b" {
		t.Fatalf("accepts[0].extra.model missing: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"model":"qwen3.5:9b"`) || body.Extensions["bazaar"] == nil {
		t.Fatalf("bazaar extension should carry the pinned model: %s", w.Body.String())
	}
}

// ForwardAuth can't rewrite bodies, so a body-policy rule is never approved
// there (approval would forward the buyer's model with the master key).
func TestVerifier_HandleVerify_RefusesBodyPolicyRule(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule("http://litellm")})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/llm/v1/chat/completions")
	req.Header.Set("X-Forwarded-Method", http.MethodPost)
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if got := w.Header().Get("Authorization"); got != "" {
		t.Fatalf("refused request must not carry the upstream credential, got %q", got)
	}
	if n := fac.verifyCalls.Load(); n != 0 {
		t.Fatalf("facilitator verify called %d times", n)
	}
}

func TestRouteRuleFromOffer_LiteLLMModelPinned(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{
			Type:     "http",
			Model:    monetizeapi.ServiceOfferModel{Name: " qwen3.5:9b ", Runtime: "litellm"},
			Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm", Namespace: "llm", Port: 4000},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia", PayTo: "0x1111111111111111111111111111111111111111",
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
	}
	offer.Name, offer.Namespace = "llm", "llm"

	rule, err := routeRuleFromOffer(offer, "Bearer sk-master")
	if err != nil {
		t.Fatal(err)
	}
	if rule.BodyPolicy != BodyPolicyLiteLLM || rule.Model != "qwen3.5:9b" {
		t.Fatalf("rule BodyPolicy=%q Model=%q, want litellm/qwen3.5:9b", rule.BodyPolicy, rule.Model)
	}

	offer.Spec.Upstream.Service = "httpbin"
	if rule, _ := routeRuleFromOffer(offer, ""); rule.BodyPolicy != "" {
		t.Fatalf("non-LiteLLM offer got body policy %q", rule.BodyPolicy)
	}
}
