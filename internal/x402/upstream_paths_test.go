package x402

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func liteLLMRule(upstreamURL string) RouteRule {
	return RouteRule{
		Pattern:       "/services/llm/*",
		Price:         "0.001",
		OfferType:     "http",
		UpstreamURL:   upstreamURL,
		StripPrefix:   "/services/llm",
		UpstreamAuth:  "Bearer sk-master",
		UpstreamPaths: liteLLMBuyerPaths,
	}
}

func TestUpstreamPathAllowed(t *testing.T) {
	rule := liteLLMRule("http://litellm")
	inference := rule
	inference.OfferType = "inference"

	cases := []struct {
		name   string
		rule   RouteRule
		method string
		path   string
		want   bool
	}{
		{"chat", rule, http.MethodPost, "/services/llm/v1/chat/completions", true},
		{"chat no v1", rule, http.MethodPost, "/services/llm/chat/completions", true},
		{"chat trailing slash", rule, http.MethodPost, "/services/llm/v1/chat/completions/", true},
		{"embeddings", rule, http.MethodPost, "/services/llm/v1/embeddings", true},
		{"completions", rule, http.MethodPost, "/services/llm/v1/completions", true},
		{"model new", rule, http.MethodPost, "/services/llm/model/new", false},
		{"key generate", rule, http.MethodPost, "/services/llm/key/generate", false},
		{"model info", rule, http.MethodGet, "/services/llm/model/info", false},
		{"config update", rule, http.MethodPost, "/services/llm/config/update", false},
		{"models list", rule, http.MethodGet, "/services/llm/v1/models", false},
		{"stored responses", rule, http.MethodGet, "/services/llm/v1/responses/resp_1", false},
		{"bare base", rule, http.MethodPost, "/services/llm", false},
		{"dot segments", rule, http.MethodPost, "/services/llm/v1/chat/completions/../../../model/new", false},
		{"double slash", rule, http.MethodPost, "/services/llm//v1/chat/completions", false},
		{"encoded slash", rule, http.MethodPost, "/services/llm/v1%2Fchat%2Fcompletions", false},
		{"encoded dots", rule, http.MethodPost, "/services/llm/v1/chat/completions/%2e%2e/%2e%2e/model/new", false},
		{"backslash", rule, http.MethodPost, `/services/llm/v1\chat\completions`, false},
		{"suffix", rule, http.MethodPost, "/services/llm/v1/chat/completionsX", false},
		{"child path", rule, http.MethodPost, "/services/llm/v1/chat/completions/extra", false},
		// Inference offers rewrite a bare-base POST to the chat path first.
		{"inference bare base", inference, http.MethodPost, "/services/llm", true},
		{"inference bare base GET", inference, http.MethodGet, "/services/llm", false},
		{"no allowlist", RouteRule{StripPrefix: "/services/x"}, http.MethodGet, "/services/x/anything/../at%2Fall", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamPathAllowed(&tc.rule, tc.method, tc.path); got != tc.want {
				t.Fatalf("upstreamPathAllowed(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestUpstreamPathsForOffer(t *testing.T) {
	offer := func(service, typ string) *monetizeapi.ServiceOffer {
		return &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
			Type:     typ,
			Upstream: monetizeapi.ServiceOfferUpstream{Service: service},
		}}
	}

	if got := upstreamPathsForOffer(offer("litellm", "http")); len(got) == 0 {
		t.Fatal("litellm http offer must get the LiteLLM path allowlist")
	}
	if got := upstreamPathsForOffer(offer("LiteLLM", "inference")); len(got) == 0 {
		t.Fatal("litellm match must be case-insensitive and cover inference offers")
	}
	if got := upstreamPathsForOffer(offer("httpbin", "http")); got != nil {
		t.Fatalf("non-litellm offer got allowlist %v", got)
	}
}

// A paying buyer must not be able to use the injected master key on LiteLLM
// admin endpoints: the request is refused before any payment challenge and
// never reaches the upstream.
func TestVerifier_HandleProxy_LiteLLMAdminPathRefused(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called, got %s %s", r.Method, r.URL.Path)
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule(upstream.URL)})

	for _, p := range []string{"/services/llm/model/new", "/services/llm/key/generate", "/services/llm/v1%2F..%2Fmodel%2Fnew"} {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PAYMENT", "anything")
		w := httptest.NewRecorder()
		v.HandleProxy(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", p, w.Code)
		}
	}
	if n := fac.verifyCalls.Load(); n != 0 {
		t.Fatalf("facilitator verify called %d times for refused paths", n)
	}
}

func TestVerifier_HandleProxy_LiteLLMChatStillGated(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called without payment")
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule(upstream.URL)})

	req := httptest.NewRequest(http.MethodPost, "/services/llm/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402 for an allowed path without payment, got %d", w.Code)
	}
}

func TestVerifier_HandleVerify_LiteLLMAdminPathRefused(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v := newTestVerifier(t, fac.URL, []RouteRule{liteLLMRule("http://litellm")})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/services/llm/model/new?x=1")
	req.Header.Set("X-Forwarded-Method", http.MethodPost)
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if got := w.Header().Get("Authorization"); got != "" {
		t.Fatalf("refused request must not carry the upstream credential, got %q", got)
	}
}
