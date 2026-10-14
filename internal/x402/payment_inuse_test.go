package x402

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

const inUseTestPayTo = "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

func inUseProxyRule(upstreamURL string) RouteRule {
	return RouteRule{
		Pattern:     "/services/demo/*",
		Price:       "0.0001",
		UpstreamURL: upstreamURL,
		StripPrefix: "/services/demo",
	}
}

func inUseProxyRequest(header string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/services/demo/v1/run", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", header)
	return req
}

// A signed authorization presented on two concurrent requests reaches the
// upstream once; the second request is refused with 409 payment_in_use.
func TestPaymentInUse_ConcurrentDuplicate_OneUpstreamCall(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamCalls atomic.Int32
	entered := make(chan struct{}, 4)
	unblock := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		entered <- struct{}{}
		<-unblock
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("11", 32))

	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.HandleProxy(first, inUseProxyRequest(header))
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never reached the upstream")
	}

	second := httptest.NewRecorder()
	v.HandleProxy(second, inUseProxyRequest(header))
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409 (body %s)", second.Code, second.Body.String())
	}
	var body paymentErrorBody
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("409 body is not JSON: %v (%s)", err, second.Body.String())
	}
	if body.Reason != "payment_in_use" {
		t.Fatalf("409 reason = %q, want payment_in_use", body.Reason)
	}

	close(unblock)
	<-done
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	if got := fac.settleCalls.Load(); got != 1 {
		t.Fatalf("settle calls = %d, want 1", got)
	}
}

// Many simultaneous copies of one authorization: exactly one is served.
func TestPaymentInUse_ConcurrentBurst_ExactlyOneServed(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("22", 32))

	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			v.HandleProxy(w, inUseProxyRequest(header))
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()

	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected status %d in %v", c, codes)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("codes = %v, want one 200 and %d 409s", codes, n-1)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

// A request that finishes without settlement (upstream error) frees the
// authorization so the buyer can retry with the same signed payment.
func TestPaymentInUse_ReleasedWhenNotSettled(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upstreamCalls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("33", 32))

	first := httptest.NewRecorder()
	v.HandleProxy(first, inUseProxyRequest(header))
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status = %d, want 502", first.Code)
	}
	if fac.settleCalls.Load() != 0 {
		t.Fatalf("settle calls = %d, want 0 after upstream failure", fac.settleCalls.Load())
	}

	retry := httptest.NewRecorder()
	v.HandleProxy(retry, inUseProxyRequest(header))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (body %s)", retry.Code, retry.Body.String())
	}
	if got := upstreamCalls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if got := fac.settleCalls.Load(); got != 1 {
		t.Fatalf("settle calls = %d, want 1", got)
	}
}

// A failed settlement (facilitator error, no tx) also frees the
// authorization: nothing was charged.
func TestPaymentInUse_ReleasedWhenSettlementFails(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{settleFail: true})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("44", 32))

	for i := range 2 {
		w := httptest.NewRecorder()
		v.HandleProxy(w, inUseProxyRequest(header))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("attempt %d status = %d, want 503 settlement_failed (body %s)", i, w.Code, w.Body.String())
		}
	}
	if got := fac.settleCalls.Load(); got != 2 {
		t.Fatalf("settle calls = %d, want 2", got)
	}
}

// A settled authorization stays blocked: replaying it gets 409 and never
// reaches the upstream.
func TestPaymentInUse_SettledBlocksReuse(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("55", 32))

	first := httptest.NewRecorder()
	v.HandleProxy(first, inUseProxyRequest(header))
	if first.Code != http.StatusOK || first.Header().Get("X-PAYMENT-RESPONSE") == "" {
		t.Fatalf("first status = %d (settled receipt %q), want settled 200", first.Code, first.Header().Get("X-PAYMENT-RESPONSE"))
	}

	replay := httptest.NewRecorder()
	v.HandleProxy(replay, inUseProxyRequest(header))
	if replay.Code != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", replay.Code)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	if got := fac.settleCalls.Load(); got != 1 {
		t.Fatalf("settle calls = %d, want 1", got)
	}
}

// Distinct authorizations from the same payer are independent, even
// concurrently.
func TestPaymentInUse_DistinctNoncesDoNotInterfere(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{inUseProxyRule(upstream.URL)})

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		header := testPaymentHeader(t) // fresh nonce each call
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			v.HandleProxy(w, inUseProxyRequest(header))
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (all: %v)", i, c, codes)
		}
	}
	if got := upstreamCalls.Load(); got != n {
		t.Fatalf("upstream calls = %d, want %d", got, n)
	}
}

// The ForwardAuth (verify-only) path never observes settlement, so a
// verified authorization counts as used: the same payment cannot open a
// second request through /verify.
func TestPaymentInUse_HandleVerify_RefusesReplay(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	v, err := NewVerifier(&PricingConfig{
		Wallet:         inUseTestPayTo,
		Chain:          "base-sepolia",
		FacilitatorURL: fac.URL,
		VerifyOnly:     true,
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	header := testPaymentHeaderWithNonce(t, inUseTestPayTo, "100", "0x"+strings.Repeat("66", 32))

	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/verify", nil)
		req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
		req.Header.Set("X-Forwarded-Host", "obol.stack")
		req.Header.Set("X-PAYMENT", header)
		w := httptest.NewRecorder()
		v.HandleVerify(w, req)
		return w.Code
	}
	if got := send(); got != http.StatusOK {
		t.Fatalf("first /verify = %d, want 200", got)
	}
	if got := send(); got != http.StatusConflict {
		t.Fatalf("replayed /verify = %d, want 409", got)
	}
	// A different authorization is unaffected.
	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpc/mainnet")
	req.Header.Set("X-Forwarded-Host", "obol.stack")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	w := httptest.NewRecorder()
	v.HandleVerify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fresh authorization /verify = %d, want 200", w.Code)
	}
}

func TestPaymentInUseRegistry_SettledExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg := NewPaymentInUseRegistry()
	reg.now = func() time.Time { return now }

	// Deadline sooner than the cap wins.
	if reg.claim("a") != claimOK {
		t.Fatal("first claim of a failed")
	}
	reg.release("a", true, now.Add(time.Minute))
	if reg.claim("a") != claimInUse {
		t.Fatal("settled key must stay blocked before its deadline")
	}
	now = now.Add(time.Minute)
	if reg.claim("a") != claimOK {
		t.Fatal("settled key must be reusable once its deadline passes")
	}
	reg.release("a", false, time.Time{})

	// No deadline (or a later one): the cap applies.
	if reg.claim("b") != claimOK {
		t.Fatal("claim b failed")
	}
	reg.release("b", true, time.Time{})
	now = now.Add(paymentInUseSettledCap - time.Second)
	if reg.claim("b") != claimInUse {
		t.Fatal("settled key must stay blocked within the cap")
	}
	now = now.Add(time.Second)
	if reg.claim("b") != claimOK {
		t.Fatal("settled key must expire at the cap")
	}
	reg.release("b", false, time.Time{})

	// An already-expired deadline is not retained.
	if reg.claim("c") != claimOK {
		t.Fatal("claim c failed")
	}
	reg.release("c", true, now.Add(-time.Second))
	if reg.len() != 0 {
		t.Fatalf("registry len = %d, want 0", reg.len())
	}
}

func TestPaymentInUseRegistry_BoundedAndSwept(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg := NewPaymentInUseRegistry()
	reg.now = func() time.Time { return now }
	reg.maxEntries = 2

	if reg.claim("a") != claimOK || reg.claim("b") != claimOK {
		t.Fatal("claims under the cap must succeed")
	}
	if reg.claim("c") != claimFull {
		t.Fatal("claim over the cap must report full")
	}
	reg.release("a", true, now.Add(time.Second))
	if reg.claim("c") != claimFull {
		t.Fatal("unexpired settled entry still counts toward the cap")
	}
	now = now.Add(2 * time.Second)
	if reg.claim("c") != claimOK {
		t.Fatal("expired entries must be swept to make room")
	}
	if reg.len() != 2 {
		t.Fatalf("registry len = %d, want 2", reg.len())
	}
}

func inUsePayload(t *testing.T, network string, payload map[string]any) (x402types.PaymentPayload, string) {
	t.Helper()
	p := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: network,
			Amount:  "100",
			Asset:   ChainBaseSepolia.USDCAddress,
			PayTo:   inUseTestPayTo,
		},
		Payload: payload,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.StdEncoding.EncodeToString(b)
	decoded, err := parsePaymentPayloadCompat(b)
	if err != nil {
		t.Fatal(err)
	}
	return *decoded, header
}

func TestPaymentInUseKey(t *testing.T) {
	eip3009 := func(from, nonce, sig string) map[string]any {
		return map[string]any{
			"signature": sig,
			"authorization": map[string]any{
				"from": from, "to": inUseTestPayTo, "value": "100",
				"validAfter": "0", "validBefore": "1700000600", "nonce": nonce,
			},
		}
	}
	permit2 := func(from string, nonce any) map[string]any {
		return map[string]any{
			"signature": "0xsig",
			"permit2Authorization": map[string]any{
				"from":      from,
				"permitted": map[string]any{"token": ChainBaseSepolia.USDCAddress, "amount": "100"},
				"spender":   "0xproxy",
				"nonce":     nonce,
				"deadline":  "1700000300",
				"witness":   map[string]any{"to": inUseTestPayTo, "validAfter": "0"},
			},
		}
	}

	a, ha := inUsePayload(t, "eip155:84532", eip3009("0xAbC", "0xFF01", "0xsig1"))
	// Same authorization: different signature bytes / address casing /
	// legacy network alias must map to the same key.
	b, hb := inUsePayload(t, "base-sepolia", eip3009("0xabc", "0xff01", "0xsig2"))
	if ka, kb := paymentInUseKey(a, ha), paymentInUseKey(b, hb); ka != kb {
		t.Fatalf("same EIP-3009 authorization produced different keys:\n%s\n%s", ka, kb)
	}
	if !strings.HasPrefix(paymentInUseKey(a, ha), "eip3009|") {
		t.Fatalf("EIP-3009 key = %q", paymentInUseKey(a, ha))
	}
	c, hc := inUsePayload(t, "eip155:84532", eip3009("0xabc", "0xff02", "0xsig1"))
	if paymentInUseKey(a, ha) == paymentInUseKey(c, hc) {
		t.Fatal("different nonces must produce different keys")
	}
	if got := paymentDeadline(a); got.Unix() != 1700000600 {
		t.Fatalf("EIP-3009 deadline = %v, want validBefore", got)
	}

	p1, hp1 := inUsePayload(t, "eip155:84532", permit2("0xDEF", "12345"))
	p2, hp2 := inUsePayload(t, "eip155:84532", permit2("0xdef", float64(12345)))
	if k1, k2 := paymentInUseKey(p1, hp1), paymentInUseKey(p2, hp2); k1 != k2 || !strings.HasPrefix(k1, "permit2|") {
		t.Fatalf("Permit2 keys = %q / %q, want equal permit2 keys", k1, k2)
	}
	if got := paymentDeadline(p1); got.Unix() != 1700000300 {
		t.Fatalf("Permit2 deadline = %v, want deadline", got)
	}

	// Unrecognised payload shape falls back to the raw header hash.
	r1, hr1 := inUsePayload(t, "eip155:84532", map[string]any{"signature": "0xsig"})
	r2, hr2 := inUsePayload(t, "eip155:84532", map[string]any{"signature": "0xother"})
	k1, k2 := paymentInUseKey(r1, hr1), paymentInUseKey(r2, hr2)
	if !strings.HasPrefix(k1, "raw|") || k1 == k2 {
		t.Fatalf("raw fallback keys = %q / %q", k1, k2)
	}
	if k1 != paymentInUseKey(r1, hr1) {
		t.Fatal("raw fallback key must be stable for identical headers")
	}
	if !paymentDeadline(r1).IsZero() {
		t.Fatal("payload without an authorization has no deadline")
	}
}
