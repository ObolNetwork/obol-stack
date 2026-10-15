package x402

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Issue #821: a buyer was charged when an agent's upstream failed but still
// answered 200. These tests pin settle-on-delivery for both response shapes.

func TestJudgeBufferedBody(t *testing.T) {
	gz := func(s string) []byte {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		_, _ = zw.Write([]byte(s))
		_ = zw.Close()
		return b.Bytes()
	}
	cases := []struct {
		name     string
		body     []byte
		encoding string
		want     deliveryVerdict
	}{
		{"zero bytes", nil, "", deliveryNoValue},
		{"whitespace only", []byte(" \n"), "", deliveryNoValue},
		{"empty content", []byte(`{"choices":[{"message":{"content":""}}]}`), "", deliveryNoValue},
		{"null content role only", []byte(`{"choices":[{"message":{"role":"assistant","content":null}}]}`), "", deliveryNoValue},
		{"empty choices", []byte(`{"choices":[]}`), "", deliveryNoValue},
		{"error object", []byte(`{"error":{"message":"provider 404"}}`), "", deliveryNoValue},
		{"content", []byte(`{"choices":[{"message":{"content":"hi"}}]}`), "", deliveryValue},
		{"content parts", []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"hi"}]}}]}`), "", deliveryValue},
		{"tool calls only", []byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`), "", deliveryValue},
		{"legacy completions text", []byte(`{"choices":[{"text":"hi"}]}`), "", deliveryValue},
		{"reasoning only", []byte(`{"choices":[{"message":{"content":"","reasoning_content":"thinking..."}}]}`), "", deliveryValue},
		{"non-chat json fails open", []byte(`{"ok":true}`), "", deliveryUnknown},
		{"embeddings fail open", []byte(`{"data":[{"embedding":[0.1]}]}`), "", deliveryUnknown},
		{"non-json fails open", []byte("plain text"), "", deliveryUnknown},
		{"gzip empty completion", gz(`{"choices":[{"message":{"content":""}}]}`), "gzip", deliveryNoValue},
		{"gzip completion", gz(`{"choices":[{"message":{"content":"hi"}}]}`), "gzip", deliveryValue},
		{"brotli fails open", []byte{0x1, 0x2}, "br", deliveryUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.encoding != "" {
				h.Set("Content-Encoding", tc.encoding)
			}
			if got := judgeBufferedBody(tc.body, h); got != tc.want {
				t.Fatalf("judgeBufferedBody = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSSEValueTap(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   bool
	}{
		{"role only + DONE", []string{
			`data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		}, false},
		{"role only + usage chunk", []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}` + "\n\n",
			`data: {"choices":[],"usage":{"total_tokens":3}}` + "\n\n",
			"data: [DONE]\n\n",
		}, false},
		{"error event", []string{`data: {"error":{"message":"boom"}}` + "\n\n"}, false},
		{"content delta", []string{
			`data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		}, true},
		{"tool call delta", []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"f"}}]}}]}` + "\n\n",
		}, true},
		{"reasoning delta", []string{
			`data: {"choices":[{"delta":{"reasoning":"hmm"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		}, true},
		{"split across writes, CRLF", []string{
			"data: {\"choices\":[{\"del", "ta\":{\"content\":\"h", "i\"}}]}\r\n\r\n",
		}, true},
		{"unterminated final event", []string{`data: {"choices":[{"delta":{"content":"hi"}}]}`}, true},
		{"not chat-shaped fails open", []string{"event: ping\ndata: {\"status\":\"working\"}\n\n"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tap := &sseValueTap{}
			for _, w := range tc.writes {
				tap.feed([]byte(w))
			}
			tap.close()
			if got := tap.delivered(); got != tc.want {
				t.Fatalf("delivered = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSettlesOnDelivery(t *testing.T) {
	post := httptest.NewRequest(http.MethodPost, "/", nil)
	get := httptest.NewRequest(http.MethodGet, "/", nil)
	cases := []struct {
		name string
		rule RouteRule
		req  *http.Request
		want bool
	}{
		{"agent POST", RouteRule{OfferType: "agent"}, post, true},
		{"inference POST", RouteRule{OfferType: "inference"}, post, true},
		{"agent GET", RouteRule{OfferType: "agent"}, get, false},
		{"http litellm POST", RouteRule{OfferType: "http", UpstreamURL: "http://litellm.llm.svc.cluster.local:4000"}, post, true},
		{"http generic POST", RouteRule{OfferType: "http", UpstreamURL: "http://api.app.svc.cluster.local:8080"}, post, false},
		{"untyped POST", RouteRule{}, post, false},
		{"async agent POST", RouteRule{OfferType: "agent", Async: true}, post, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := tc.rule
			if got := settlesOnDelivery(&rule, tc.req); got != tc.want {
				t.Fatalf("settlesOnDelivery = %v, want %v", got, tc.want)
			}
		})
	}
}

// deliveryFixture stands up an upstream, a mock facilitator, and a real
// HandleProxy server (so ReverseProxy's flush behaviour is exercised).
type deliveryFixture struct {
	fac *mockFacilitator
	v   *Verifier
	srv *httptest.Server
}

func newDeliveryFixture(t *testing.T, offerType string, facOpts mockFacilitatorOpts, upstream http.HandlerFunc) *deliveryFixture {
	t.Helper()
	fac := newMockFacilitator(t, facOpts)
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: up.URL,
		StripPrefix: "/services/demo",
		OfferType:   offerType,
	}})
	srv := httptest.NewServer(http.HandlerFunc(v.HandleProxy))
	t.Cleanup(srv.Close)
	return &deliveryFixture{fac: fac, v: v, srv: srv}
}

// do sends a paid chat request and returns the response with its body fully
// read (trailers are populated once the body is drained).
func (f *deliveryFixture) do(t *testing.T) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/services/demo/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func receiptOf(resp *http.Response) string {
	if r := resp.Header.Get("X-PAYMENT-RESPONSE"); r != "" {
		return r
	}
	return resp.Trailer.Get("X-PAYMENT-RESPONSE")
}

func counterTotal(c prometheus.Collector) float64 {
	if testutil.CollectAndCount(c) == 0 {
		return 0
	}
	return testutil.ToFloat64(c)
}

// assertNotCharged checks the no-value outcome: no /settle, no receipt, not
// counted as charged, counted as an upstream failure after verify.
func (f *deliveryFixture) assertNotCharged(t *testing.T, resp *http.Response) {
	t.Helper()
	if n := f.fac.settleCalls.Load(); n != 0 {
		t.Fatalf("settle calls = %d, want 0 (no value delivered must not charge)", n)
	}
	if r := receiptOf(resp); r != "" {
		t.Fatalf("unexpected payment receipt %q on an unsettled response", r)
	}
	if got := counterTotal(f.v.metrics.chargedRequests); got != 0 {
		t.Fatalf("chargedRequests = %v, want 0", got)
	}
	if got := counterTotal(f.v.metrics.upstreamFailedAfterVerify); got != 1 {
		t.Fatalf("upstreamFailedAfterVerify = %v, want 1", got)
	}
}

func (f *deliveryFixture) assertCharged(t *testing.T, resp *http.Response) {
	t.Helper()
	if n := f.fac.settleCalls.Load(); n != 1 {
		t.Fatalf("settle calls = %d, want 1", n)
	}
	// The counter is bumped after the middleware returns; with a declared
	// Content-Length the client can finish reading the body first.
	deadline := time.Now().Add(2 * time.Second)
	for counterTotal(f.v.metrics.chargedRequests) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := counterTotal(f.v.metrics.chargedRequests); got != 1 {
		t.Fatalf("chargedRequests = %v, want 1", got)
	}
	if got := counterTotal(f.v.metrics.upstreamFailedAfterVerify); got != 0 {
		t.Fatalf("upstreamFailedAfterVerify = %v, want 0", got)
	}
}

func TestHandleProxy_Delivery_NonSSEEmptyBodyNotCharged(t *testing.T) {
	f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // agent aborted its loop: 200, zero bytes
	})
	resp, body := f.do(t)
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("status=%d body=%q, want the upstream's 200 + empty body passed through", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Payment-Settled") != "false" {
		t.Fatalf("X-Payment-Settled = %q, want false", resp.Header.Get("X-Payment-Settled"))
	}
	f.assertNotCharged(t, resp)
}

func TestHandleProxy_Delivery_NonSSEEmptyCompletionNotCharged(t *testing.T) {
	const empty = `{"choices":[{"message":{"content":""}}]}`
	f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(empty))
	})
	resp, body := f.do(t)
	if resp.StatusCode != http.StatusOK || string(body) != empty {
		t.Fatalf("status=%d body=%q, want the upstream response passed through", resp.StatusCode, body)
	}
	f.assertNotCharged(t, resp)
}

func TestHandleProxy_Delivery_SSERoleOnlyNotCharged(t *testing.T) {
	f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	resp, body := f.do(t)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("status=%d body=%q, want the stream passed through", resp.StatusCode, body)
	}
	f.assertNotCharged(t, resp)
}

func TestHandleProxy_Delivery_NonSSESuccessCharged(t *testing.T) {
	const ok = `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`
	f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ok))
	})
	resp, body := f.do(t)
	if resp.StatusCode != http.StatusOK || string(body) != ok {
		t.Fatalf("status=%d body=%q, want 200 + upstream body", resp.StatusCode, body)
	}
	// Receipt must be a real response header (not a trailer) on non-SSE.
	if resp.Header.Get("X-PAYMENT-RESPONSE") == "" || resp.Header.Get("PAYMENT-RESPONSE") == "" {
		t.Fatalf("receipt headers missing on a settled non-SSE response: %v", resp.Header)
	}
	f.assertCharged(t, resp)
}

// chunkedJSONUpstream answers application/json with no Content-Length and
// flushes explicitly, so ReverseProxy sees ContentLength=-1 and runs its
// flushInterval=-1 path (flush before the first body byte).
func chunkedJSONUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"choices":[{"message":`))
	w.(http.Flusher).Flush()
	time.Sleep(20 * time.Millisecond) // let the proxy's delayed flush fire
	_, _ = w.Write([]byte(`{"role":"assistant","content":"hello"}}]}`))
	w.(http.Flusher).Flush()
}

func TestHandleProxy_Delivery_ChunkedJSONReceiptInHeaders(t *testing.T) {
	f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, chunkedJSONUpstream)
	resp, body := f.do(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if want := `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`; string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	if resp.Header.Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatal("X-PAYMENT-RESPONSE missing: an implicit 200 was committed before settlement")
	}
	f.assertCharged(t, resp)
}

// A settle failure must still become a 503 — impossible if a Flush had
// already committed an implicit 200 — and the error body must be readable
// (the upstream's Content-Length must not describe it).
func TestHandleProxy_Delivery_SettleFailureBecomes503(t *testing.T) {
	upstreams := map[string]http.HandlerFunc{
		"chunked": chunkedJSONUpstream,
		"content-length": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
		},
	}
	for name, up := range upstreams {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{settleFail: true}, up)
			resp, body := f.do(t)
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %q)", resp.StatusCode, body)
			}
			var perr paymentErrorBody
			if err := json.Unmarshal(body, &perr); err != nil || perr.Reason != "settlement_failed" {
				t.Fatalf("body %q: err=%v reason=%q, want settlement_failed JSON", body, err, perr.Reason)
			}
			if got := counterTotal(f.v.metrics.chargedRequests); got != 0 {
				t.Fatalf("chargedRequests = %v, want 0", got)
			}
		})
	}
}

// Over the buffer cap the response falls back to settling at that point
// (status still uncommitted) and streams the rest through intact.
func TestHandleProxy_Delivery_OverCapFallsBackToSettle(t *testing.T) {
	big := `{"choices":[{"message":{"content":"` + strings.Repeat("x", deliveryBufferCap+1024) + `"}}]}`
	for _, declareLength := range []bool{false, true} {
		name := "chunked"
		if declareLength {
			name = "content-length"
		}
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t, "agent", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if declareLength {
					w.Header().Set("Content-Length", strconv.Itoa(len(big)))
				}
				w.WriteHeader(http.StatusOK)
				bw := bufio.NewWriterSize(w, 64<<10)
				_, _ = bw.WriteString(big)
				_ = bw.Flush()
			})
			resp, body := f.do(t)
			if resp.StatusCode != http.StatusOK || len(body) != len(big) {
				t.Fatalf("status=%d len(body)=%d, want 200 + %d bytes", resp.StatusCode, len(body), len(big))
			}
			if resp.Header.Get("X-PAYMENT-RESPONSE") == "" {
				t.Fatal("receipt header missing on an over-cap response")
			}
			f.assertCharged(t, resp)
		})
	}
}

// Generic `sell http` byte proxies keep status-based settlement: an empty
// 2xx can be the legitimate product there.
func TestHandleProxy_Delivery_GenericHTTPUnchanged(t *testing.T) {
	f := newDeliveryFixture(t, "http", mockFacilitatorOpts{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	resp, _ := f.do(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw := resp.Header.Get("X-PAYMENT-RESPONSE")
	if raw == "" {
		t.Fatal("generic http route must still settle an empty 200")
	}
	if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
		t.Fatalf("receipt not base64: %v", err)
	}
	f.assertCharged(t, resp)
}
