package x402

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func hermesRule(upstreamURL string) RouteRule {
	return RouteRule{
		Pattern:        "/services/bot/*",
		Price:          "0.0001",
		OfferType:      "agent",
		OfferName:      "bot",
		OfferNamespace: "agent-bot",
		UpstreamURL:    upstreamURL,
		StripPrefix:    "/services/bot",
		UpstreamAuth:   "Bearer hermes-api-key",
		UpstreamPaths:  hermesBuyerPaths,
		BodyPolicy:     BodyPolicyAgent,
	}
}

// Every Hermes API-server route other than a chat turn is refused before the
// 402 — paid or not — and the API key never reaches the agent on them.
func TestVerifier_HandleProxy_AgentRefusesNonChatHermesRoutes(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Hermes must not be called (auth %q), got %s %s", r.Header.Get("Authorization"), r.Method, r.URL.Path)
	}))
	defer upstream.Close()
	v := newTestVerifier(t, fac.URL, []RouteRule{hermesRule(upstream.URL)})

	refused := []struct{ method, path string }{
		{http.MethodPost, "/services/bot/v1/responses"},
		{http.MethodGet, "/services/bot/v1/responses/resp_abc"},
		{http.MethodDelete, "/services/bot/v1/responses/resp_abc"},
		{http.MethodGet, "/services/bot/api/sessions"},
		{http.MethodGet, "/services/bot/api/sessions/s1/messages"},
		{http.MethodPost, "/services/bot/api/sessions/s1/chat"},
		{http.MethodPost, "/services/bot/api/jobs"},
		{http.MethodPost, "/services/bot/api/jobs/j1/run"},
		{http.MethodPost, "/services/bot/v1/runs"},
		{http.MethodPost, "/services/bot/v1/runs/r1/approval"},
		{http.MethodGet, "/services/bot/v1/models"},
		{http.MethodGet, "/services/bot/v1/skills"},
		{http.MethodGet, "/services/bot/health/detailed"},
		{http.MethodPost, "/services/bot/v1/artifacts/upload"},
		{http.MethodPost, "/services/bot/v1/browser-control/register"},
		{http.MethodPost, "/services/bot/v1/room-members/invitations"},
		{http.MethodPost, "/services/bot/p/default/v1/chat/completions"},
		{http.MethodPost, "/services/bot/api/cron/fire"},
		{http.MethodGet, "/services/bot/v1/chat/completions"},
		{http.MethodPut, "/services/bot/v1/chat/completions"},
		{http.MethodPost, "/services/bot/v1/chat/completions/../../api/jobs"},
		{http.MethodPost, "/services/bot/v1%2Fresponses"},
	}
	for _, tc := range refused {
		for _, paid := range []bool{false, true} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"input":"hi"}`))
			req.Header.Set("Content-Type", "application/json")
			if paid {
				req.Header.Set("X-PAYMENT", testPaymentHeader(t))
			}
			w := httptest.NewRecorder()
			v.HandleProxy(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s (paid=%v): status %d, want 404", tc.method, tc.path, paid, w.Code)
			}
		}
	}
	if n := fac.verifyCalls.Load(); n != 0 {
		t.Fatalf("facilitator verify called %d times for refused Hermes routes", n)
	}
}

// The allowed chat turn reaches Hermes with the key, but with the buyer's
// provider/model selection and Hermes session headers stripped.
func TestVerifier_HandleProxy_AgentChatStripsModelAndSession(t *testing.T) {
	for _, path := range []string{"/services/bot/v1/chat/completions", "/services/bot", "/services/bot/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			fac := newMockFacilitator(t, mockFacilitatorOpts{})
			upstream, got := captureUpstream(t)
			v := newTestVerifier(t, fac.URL, []RouteRule{hermesRule(upstream.URL)})

			req := httptest.NewRequest(http.MethodPost, path+"?model=x",
				strings.NewReader(`{"model":"gpt-5","provider":"anthropic","model_options":{"base_url":"http://evil"},`+
					`"messages":[{"role":"user","content":"hi"}],"stream":false}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-PAYMENT", testPaymentHeader(t))
			req.Header.Set("X-Hermes-Session-Id", "someone-elses-session")
			req.Header.Set("X-Hermes-Session-Key", "someone-elses-memory")
			w := httptest.NewRecorder()
			v.HandleProxy(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
			}
			if got.path != "/v1/chat/completions" || got.auth != "Bearer hermes-api-key" {
				t.Fatalf("upstream saw %s with auth %q", got.path, got.auth)
			}
			for _, k := range agentModelSelectionFields {
				if _, ok := got.body[k]; ok {
					t.Errorf("buyer %q must not reach Hermes: %v", k, got.body[k])
				}
			}
			if _, ok := got.body["messages"]; !ok {
				t.Error("messages must be forwarded")
			}
			for name := range got.header {
				if strings.HasPrefix(strings.ToLower(name), "x-hermes-") {
					t.Errorf("header %s must not reach Hermes", name)
				}
			}
			if got.rawQuery != "" {
				t.Errorf("query %q must not reach Hermes", got.rawQuery)
			}
		})
	}
}

// An unpaid browser GET of the offer root still renders the paywall (the
// catalog links buyers there), but a paid GET is refused, and neither
// reaches Hermes.
func TestVerifier_HandleProxy_AgentRootPaywallPreview(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Hermes must not be called, got %s %s", r.Method, r.URL.Path)
	}))
	defer upstream.Close()
	rule := hermesRule(upstream.URL)
	rule.FreeQuota = 5 // a quota must not turn a preview into a proxied request
	v := newTestVerifier(t, fac.URL, []RouteRule{rule})

	for _, path := range []string{"/services/bot", "/services/bot/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		v.HandleProxy(w, req)
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("GET %s: status %d, want the 402 paywall", path, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/services/bot", nil)
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("paid GET of the root: status %d, want 404", w.Code)
	}
	if n := fac.verifyCalls.Load(); n != 0 {
		t.Fatalf("facilitator verify called %d times", n)
	}
}

func TestVerifier_HandleProxy_AgentNonJSONRefusedBeforePayment(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{hermesRule("http://127.0.0.1:1")})

	req := httptest.NewRequest(http.MethodPost, "/services/bot/v1/chat/completions", strings.NewReader("hello"))
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if n := fac.verifyCalls.Load(); n != 0 {
		t.Fatalf("facilitator verify called %d times", n)
	}
}

func TestRouteRuleFromOffer_AgentRestrictedToChat(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia", PayTo: "0x1111111111111111111111111111111111111111",
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{AgentResolution: &monetizeapi.ServiceOfferAgentResolution{
			Endpoint: "http://hermes.agent-bot.svc.cluster.local:8642", Model: "qwen3.5:9b",
		}},
	}
	offer.Name, offer.Namespace = "bot", "agent-bot"

	rule, err := routeRuleFromOffer(offer, "Bearer hermes-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if rule.BodyPolicy != BodyPolicyAgent {
		t.Fatalf("BodyPolicy = %q, want agent", rule.BodyPolicy)
	}
	if len(rule.UpstreamPaths) != 1 || rule.UpstreamPaths[0] != "POST /v1/chat/completions" {
		t.Fatalf("UpstreamPaths = %v", rule.UpstreamPaths)
	}
}

// hermesDerivedSessionID mirrors Hermes' _derive_chat_session_id: the
// session a header-less /v1/chat/completions request lands in.
func hermesDerivedSessionID(t *testing.T, body map[string]any) string {
	t.Helper()
	var system []string
	firstUser := ""
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		content, _ := mm["content"].(string)
		switch mm["role"] {
		case "system":
			system = append(system, content)
		case "user":
			if firstUser == "" {
				firstUser = content
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(system, "\n") + "\n" + firstUser))
	return "api-" + hex.EncodeToString(sum[:])[:16]
}

// Two buyers (or one buyer twice) sending the same opening turn must not
// share a Hermes session: every request carries a fresh random request id
// in the system prompt, behind the verified payer note, so Hermes derives a
// distinct, unguessable session per request.
func TestVerifier_HandleProxy_AgentSessionIsolatedPerRequest(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream, got := captureUpstream(t)
	v := newTestVerifier(t, fac.URL, []RouteRule{hermesRule(upstream.URL)})

	const body = `{"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}]}`
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/services/bot/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PAYMENT", testPaymentHeader(t))
		req.Header.Set("Idempotency-Key", "same-key")
		w := httptest.NewRecorder()
		v.HandleProxy(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d (%s)", w.Code, w.Body.String())
		}

		msgs, _ := got.body["messages"].([]any)
		if len(msgs) != 4 {
			t.Fatalf("want payer note + request id + 2 buyer messages, got %v", msgs)
		}
		first, _ := msgs[0].(map[string]any)
		second, _ := msgs[1].(map[string]any)
		if c, _ := first["content"].(string); first["role"] != "system" || !strings.Contains(c, "0xmockpayer") {
			t.Fatalf("payer note must stay first, got %v", first)
		}
		if c, _ := second["content"].(string); second["role"] != "system" || !strings.HasPrefix(c, "Request id ") {
			t.Fatalf("request-id system message missing, got %v", second)
		}
		if got.header.Get("Idempotency-Key") != "" {
			t.Fatal("Idempotency-Key must not reach Hermes")
		}
		id := hermesDerivedSessionID(t, got.body)
		if seen[id] {
			t.Fatalf("Hermes session %s reused across requests", id)
		}
		seen[id] = true
	}
}

// Session isolation and the payer note ride on the rewritten body, so a
// streamed agent turn must still flow through chunk by chunk.
func TestVerifier_HandleProxy_AgentStreamingWithIsolation(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &upstreamBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range []string{"hello", " world"} {
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + c + `"}}]}` + "\n\n"))
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()
	v := newTestVerifier(t, fac.URL, []RouteRule{hermesRule(upstream.URL)})
	srv := httptest.NewServer(http.HandlerFunc(v.HandleProxy))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/services/bot/v1/chat/completions",
		strings.NewReader(`{"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(out), `"hello"`) ||
		!strings.Contains(string(out), `" world"`) || !strings.Contains(string(out), "[DONE]") {
		t.Fatalf("stream broken: %d %q", resp.StatusCode, out)
	}
	msgs, _ := upstreamBody["messages"].([]any)
	if len(msgs) != 3 || upstreamBody["stream"] != true {
		t.Fatalf("upstream body = %v", upstreamBody)
	}
	if fac.settleCalls.Load() != 1 {
		t.Fatalf("settle calls = %d, want 1", fac.settleCalls.Load())
	}
}
