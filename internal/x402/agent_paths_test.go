package x402

import (
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
