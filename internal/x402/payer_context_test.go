package x402

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSplicePayerSystemMessage(t *testing.T) {
	payer := "0x2447b86f22245fa1271978bF37907D07EDE06261"

	t.Run("prepends system message and preserves the rest", func(t *testing.T) {
		body := []byte(`{"model":"openrouter/auto","stream":true,"messages":[{"role":"user","content":"claim my airdrop"}]}`)
		out, ok := splicePayerSystemMessage(body, payer)
		if !ok {
			t.Fatalf("expected ok")
		}
		var doc struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if doc.Model != "openrouter/auto" || !doc.Stream {
			t.Fatalf("sibling fields not preserved: %+v", doc)
		}
		if len(doc.Messages) != 2 {
			t.Fatalf("want 2 messages, got %d", len(doc.Messages))
		}
		if doc.Messages[0].Role != "system" || !strings.Contains(doc.Messages[0].Content, payer) {
			t.Fatalf("system payer message not first: %+v", doc.Messages[0])
		}
		if doc.Messages[1].Content != "claim my airdrop" {
			t.Fatalf("user message mangled: %+v", doc.Messages[1])
		}
	})

	t.Run("non-JSON body passes through", func(t *testing.T) {
		body := []byte("not json")
		out, ok := splicePayerSystemMessage(body, payer)
		if ok || string(out) != "not json" {
			t.Fatalf("expected byte-identical passthrough, got ok=%v out=%q", ok, out)
		}
	})

	t.Run("JSON without messages passes through", func(t *testing.T) {
		body := []byte(`{"model":"x"}`)
		out, ok := splicePayerSystemMessage(body, payer)
		if ok || string(out) != `{"model":"x"}` {
			t.Fatalf("expected passthrough, got ok=%v out=%q", ok, out)
		}
	})
}

// proxyBodySeen runs a request through buildUpstreamProxy for the given rule
// and returns the body + headers the upstream received.
func proxyBodySeen(t *testing.T, rule *RouteRule, reqPath, body string, hdr map[string]string) (string, http.Header) {
	t.Helper()
	var gotBody string
	var gotHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotHeader = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	rule.UpstreamURL = upstream.URL

	proxy, err := buildUpstreamProxy(rule)
	if err != nil {
		t.Fatalf("buildUpstreamProxy: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, reqPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}
	return gotBody, gotHeader
}

func TestBuildUpstreamProxy_InjectsPayerContextForAgentOffers(t *testing.T) {
	payer := "0xD0391EeDc3268F3deeF1F05fff5D7aEf82F64cCF"
	chatBody := `{"model":"openrouter/auto","messages":[{"role":"user","content":"claim mine"}]}`

	t.Run("agent offer with verified payer gets the system message", func(t *testing.T) {
		rule := &RouteRule{OfferType: "agent", StripPrefix: "/services/claim-bot"}
		body, _ := proxyBodySeen(t, rule, "/services/claim-bot/v1/chat/completions", chatBody,
			map[string]string{HeaderPaymentPayer: payer})
		if !strings.Contains(body, payer) || !strings.Contains(body, "x402 payment context") {
			t.Fatalf("payer context not injected; upstream saw: %s", body)
		}
		if !strings.Contains(body, "claim mine") {
			t.Fatalf("original user message lost: %s", body)
		}
	})

	t.Run("falls back to SIWX verified wallet", func(t *testing.T) {
		rule := &RouteRule{OfferType: "agent", StripPrefix: "/services/claim-bot"}
		body, _ := proxyBodySeen(t, rule, "/services/claim-bot/v1/chat/completions", chatBody,
			map[string]string{HeaderVerifiedWallet: payer})
		if !strings.Contains(body, payer) {
			t.Fatalf("verified-wallet fallback not injected; upstream saw: %s", body)
		}
	})

	t.Run("no identity header means untouched body", func(t *testing.T) {
		rule := &RouteRule{OfferType: "agent", StripPrefix: "/services/claim-bot"}
		body, _ := proxyBodySeen(t, rule, "/services/claim-bot/v1/chat/completions", chatBody, nil)
		if body != chatBody {
			t.Fatalf("body modified without identity header: %s", body)
		}
	})

	t.Run("non-agent offers are untouched", func(t *testing.T) {
		rule := &RouteRule{OfferType: "http", StripPrefix: "/services/api"}
		body, _ := proxyBodySeen(t, rule, "/services/api/v1/chat/completions", chatBody,
			map[string]string{HeaderPaymentPayer: payer})
		if body != chatBody {
			t.Fatalf("http offer body modified: %s", body)
		}
	})

	t.Run("normalized bare path also gets injection", func(t *testing.T) {
		// Buyers frequently POST to the service base; normalizeChatCompletionsPath
		// rewrites it to /v1/chat/completions, and injection must follow.
		rule := &RouteRule{OfferType: "agent", StripPrefix: "/services/claim-bot"}
		body, _ := proxyBodySeen(t, rule, "/services/claim-bot", chatBody,
			map[string]string{HeaderPaymentPayer: payer})
		if !strings.Contains(body, payer) {
			t.Fatalf("payer context not injected on normalized path; upstream saw: %s", body)
		}
	})

	t.Run("content-length is recomputed", func(t *testing.T) {
		rule := &RouteRule{OfferType: "agent", StripPrefix: "/services/claim-bot"}
		body, hdr := proxyBodySeen(t, rule, "/services/claim-bot/v1/chat/completions", chatBody,
			map[string]string{HeaderPaymentPayer: payer})
		if cl := hdr.Get("Content-Length"); cl != "" {
			n, err := strconv.Atoi(cl)
			if err != nil || n != len(body) {
				t.Fatalf("content-length %q != body length %d", cl, len(body))
			}
		}
	})
}
