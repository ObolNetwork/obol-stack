package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func cardTestRule() *RouteRule {
	return &RouteRule{
		Pattern:        "/services/card-foo/*",
		Price:          "0.01",
		OfferNamespace: "default",
		OfferName:      "card-foo",
		Card: &CardRoute{
			Provider:  "stripe",
			Account:   "acct_test123",
			Currency:  "usd",
			Decimals:  2,
			NetworkID: "stripenet_abc",
		},
	}
}

func cardCredHeader(spt string) string {
	b, _ := json.Marshal(map[string]string{"spt": spt})
	return base64.StdEncoding.EncodeToString(b)
}

func TestCurrencyMinorUnits(t *testing.T) {
	cases := map[string]int{"usd": 2, "USD": 2, "eur": 2, "jpy": 0, "krw": 0, "bhd": 3, "kwd": 3, "zzz": 2, "": 2}
	for in, want := range cases {
		if got := currencyMinorUnits(in); got != want {
			t.Errorf("currencyMinorUnits(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBuildCardRequirement(t *testing.T) {
	req := buildCardRequirement(cardTestRule())

	if req.Scheme != cardScheme || req.Network != cardNetworkStripe {
		t.Errorf("scheme/network = %q/%q", req.Scheme, req.Network)
	}
	if req.PayTo != "acct_test123" {
		t.Errorf("payTo = %q, want acct_test123", req.PayTo)
	}
	if req.Amount != "1" { // "0.01" usd (2 decimals) -> 1 cent
		t.Errorf("amount = %q, want 1 (minor units)", req.Amount)
	}
	if req.Asset != "" {
		t.Errorf("asset = %q, want empty", req.Asset)
	}
	if req.Extra["currency"] != "usd" || req.Extra["networkId"] != "stripenet_abc" {
		t.Errorf("extra = %v", req.Extra)
	}
	pmt, ok := req.Extra["paymentMethodTypes"].([]string)
	if !ok || len(pmt) != 1 || pmt[0] != "card" {
		t.Errorf("extra.paymentMethodTypes = %v, want [card]", req.Extra["paymentMethodTypes"])
	}
}

func TestBuildCardRequirement_NonTwoDecimalCurrency(t *testing.T) {
	rule := &RouteRule{Price: "100", Card: &CardRoute{Account: "acct_x", Currency: "jpy"}}
	req := buildCardRequirement(rule)
	// jpy has 0 minor-unit decimals: ¥100 -> amount "100".
	if req.Amount != "100" {
		t.Errorf("jpy amount = %q, want 100", req.Amount)
	}
	if req.Extra["decimals"] != 0 {
		t.Errorf("jpy decimals = %v, want 0", req.Extra["decimals"])
	}
}

func TestParseCardCredential(t *testing.T) {
	b64 := func(v any) string { b, _ := json.Marshal(v); return base64.StdEncoding.EncodeToString(b) }

	t.Run("bare", func(t *testing.T) {
		cred, err := parseCardCredential(b64(map[string]string{"spt": "spt_abc", "externalId": "e1"}))
		if err != nil || cred.SPT != "spt_abc" || cred.ExternalID != "e1" {
			t.Fatalf("got %+v err=%v", cred, err)
		}
	})
	t.Run("wrapped", func(t *testing.T) {
		cred, err := parseCardCredential(b64(map[string]any{"payload": map[string]string{"spt": "spt_xyz"}}))
		if err != nil || cred.SPT != "spt_xyz" {
			t.Fatalf("got %+v err=%v", cred, err)
		}
	})
	for _, bad := range []struct{ name, header string }{
		{"bad base64", "!!!"},
		{"missing spt", b64(map[string]string{"externalId": "e1"})},
		{"wrong prefix", b64(map[string]string{"spt": "tok_abc"})},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if _, err := parseCardCredential(bad.header); err == nil {
				t.Errorf("expected error for %s", bad.name)
			}
		})
	}
}

func TestBuildAuthorizeForm(t *testing.T) {
	form := buildAuthorizeForm("1", "usd", "spt_abc")
	want := map[string]string{
		"amount":                       "1",
		"currency":                     "usd",
		"confirm":                      "true",
		"capture_method":               "manual",
		"shared_payment_granted_token": "spt_abc",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("form[%q] = %q, want %q", k, form.Get(k), v)
		}
	}
}

func TestSPTReplayGuard(t *testing.T) {
	g := newSPTReplayGuard(time.Hour)
	if !g.tryReserve("spt_a") {
		t.Fatal("first reserve should succeed")
	}
	if g.tryReserve("spt_a") {
		t.Fatal("second reserve of in-flight token must fail")
	}
	g.release("spt_a")
	if !g.tryReserve("spt_a") {
		t.Fatal("after release, reserve should succeed again")
	}
	g.consume("spt_a")
	if g.tryReserve("spt_a") {
		t.Fatal("consumed token must stay blocked")
	}
	// TTL expiry: a guard with a 0 TTL forgets immediately.
	g0 := newSPTReplayGuard(0)
	g0.consume("spt_b")
	if !g0.tryReserve("spt_b") {
		t.Fatal("token past TTL should be reservable")
	}
}

// ── stripeCardGateway against a mock Stripe server ──────────────────────────

func TestStripeCardGateway_Lifecycle(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Errorf("missing Basic auth on %s", r.URL.Path)
		}
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/capture"):
			_, _ = io.WriteString(w, `{"id":"pi_x","status":"succeeded"}`)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			_, _ = io.WriteString(w, `{"id":"pi_x","status":"canceled"}`)
		default: // authorize
			if r.FormValue("capture_method") != "manual" {
				t.Errorf("authorize capture_method = %q, want manual", r.FormValue("capture_method"))
			}
			if r.FormValue("shared_payment_granted_token") != "spt_live" {
				t.Errorf("authorize spt = %q", r.FormValue("shared_payment_granted_token"))
			}
			_, _ = io.WriteString(w, `{"id":"pi_x","status":"requires_capture"}`)
		}
	}))
	defer srv.Close()

	gw := &stripeCardGateway{httpClient: srv.Client(), baseURL: srv.URL, secretKey: func() string { return "sk_test" }}
	ctx := context.Background()

	id, err := gw.authorize(ctx, nil, "100", "usd", cardCredential{SPT: "spt_live"})
	if err != nil || id != "pi_x" {
		t.Fatalf("authorize id=%q err=%v", id, err)
	}
	if err := gw.capture(ctx, nil, id); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := gw.cancel(ctx, nil, id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(paths) != 3 {
		t.Errorf("expected 3 Stripe calls, got %v", paths)
	}
}

func TestStripeCardGateway_NoKey(t *testing.T) {
	gw := &stripeCardGateway{httpClient: http.DefaultClient, baseURL: stripeAPIBase, secretKey: func() string { return "" }}
	if _, err := gw.authorize(context.Background(), nil, "1", "usd", cardCredential{SPT: "spt_a"}); err == nil {
		t.Fatal("expected error when secret key unset")
	}
}

func TestStripeCardGateway_AuthorizeRequiresAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"pi_y","status":"requires_action"}`)
	}))
	defer srv.Close()
	gw := &stripeCardGateway{httpClient: srv.Client(), baseURL: srv.URL, secretKey: func() string { return "sk_test" }}
	if _, err := gw.authorize(context.Background(), nil, "1", "usd", cardCredential{SPT: "spt_a"}); err == nil {
		t.Fatal("requires_action must be an error (3DS not supported)")
	}
}

// ── serveCardGated with a fake gateway ──────────────────────────────────────

type fakeGateway struct {
	mu        sync.Mutex
	authErr   error
	capErr    error
	authCalls int
	captured  []string
	canceled  []string
	pi        string
}

func (f *fakeGateway) authorize(_ context.Context, _ *CardRoute, _, _ string, _ cardCredential) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls++
	if f.authErr != nil {
		return "", f.authErr
	}
	return f.pi, nil
}

func (f *fakeGateway) capture(_ context.Context, _ *CardRoute, pi string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capErr != nil {
		return f.capErr
	}
	f.captured = append(f.captured, pi)
	return nil
}

func (f *fakeGateway) cancel(_ context.Context, _ *CardRoute, pi string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled = append(f.canceled, pi)
	return nil
}

func okProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "upstream-ok")
	})
}

func failProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
}

func gateOnce(gw cardGateway, guard *sptReplayGuard, sptHeader string, proxy http.Handler) *httptest.ResponseRecorder {
	rule := cardTestRule()
	req := buildCardRequirement(rule)
	r := httptest.NewRequest(http.MethodPost, "/services/card-foo/x", nil)
	if sptHeader != "" {
		r.Header.Set("X-PAYMENT", sptHeader)
	}
	w := httptest.NewRecorder()
	(&Verifier{}).serveCardGated(w, r, rule, req, nil, proxy, gw, guard)
	return w
}

func TestServeCardGated_NoPayment402(t *testing.T) {
	gw := &fakeGateway{pi: "pi_1"}
	w := gateOnce(gw, newSPTReplayGuard(time.Hour), "", okProxy())
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if gw.authCalls != 0 {
		t.Error("authorize must not be called without a credential")
	}
}

func TestServeCardGated_PaidAuthorizeCaptureProxy(t *testing.T) {
	gw := &fakeGateway{pi: "pi_1"}
	guard := newSPTReplayGuard(time.Hour)
	w := gateOnce(gw, guard, cardCredHeader("spt_a"), okProxy())

	if w.Code != http.StatusOK || w.Body.String() != "upstream-ok" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if gw.authCalls != 1 || len(gw.captured) != 1 || gw.captured[0] != "pi_1" {
		t.Fatalf("auth=%d captured=%v", gw.authCalls, gw.captured)
	}
	if len(gw.canceled) != 0 {
		t.Errorf("must not cancel on success: %v", gw.canceled)
	}
	hdr := w.Header().Get("X-PAYMENT-RESPONSE")
	dec, _ := base64.StdEncoding.DecodeString(hdr)
	var receipt map[string]string
	_ = json.Unmarshal(dec, &receipt)
	if receipt["reference"] != "pi_1" {
		t.Errorf("receipt = %v, want reference pi_1", receipt)
	}
	// SPT now consumed: a replay is rejected and does not re-authorize.
	w2 := gateOnce(gw, guard, cardCredHeader("spt_a"), okProxy())
	if w2.Code != http.StatusPaymentRequired {
		t.Errorf("replay status = %d, want 402", w2.Code)
	}
	if gw.authCalls != 1 {
		t.Errorf("replay must not re-authorize: authCalls=%d", gw.authCalls)
	}
}

func TestServeCardGated_AuthorizeFailure402(t *testing.T) {
	gw := &fakeGateway{authErr: io.ErrUnexpectedEOF}
	guard := newSPTReplayGuard(time.Hour)
	w := gateOnce(gw, guard, cardCredHeader("spt_a"), okProxy())
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if len(gw.captured) != 0 {
		t.Error("must not capture when authorize fails")
	}
	// Authorization failure releases the SPT for retry.
	if !guard.tryReserve("spt_a") {
		t.Error("SPT should be released after authorize failure")
	}
}

func TestServeCardGated_UpstreamFailureCancels(t *testing.T) {
	gw := &fakeGateway{pi: "pi_2"}
	guard := newSPTReplayGuard(time.Hour)
	w := gateOnce(gw, guard, cardCredHeader("spt_a"), failProxy())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 passthrough", w.Code)
	}
	if len(gw.captured) != 0 {
		t.Errorf("must not capture on upstream failure: %v", gw.captured)
	}
	if len(gw.canceled) != 1 || gw.canceled[0] != "pi_2" {
		t.Errorf("must cancel authorization on upstream failure: %v", gw.canceled)
	}
	if !guard.tryReserve("spt_a") {
		t.Error("SPT should be released after upstream failure")
	}
}

func TestServeCardGated_UpstreamPanicCancels(t *testing.T) {
	gw := &fakeGateway{pi: "pi_panic"}
	guard := newSPTReplayGuard(time.Hour)
	panicProxy := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("upstream blew up") })

	// serveCardGated re-panics to preserve server panic handling; recover here.
	func() {
		defer func() { _ = recover() }()
		gateOnce(gw, guard, cardCredHeader("spt_a"), panicProxy)
	}()

	if len(gw.captured) != 0 {
		t.Errorf("must not capture when upstream panics: %v", gw.captured)
	}
	if len(gw.canceled) != 1 || gw.canceled[0] != "pi_panic" {
		t.Errorf("panic must cancel the authorization hold: %v", gw.canceled)
	}
	if !guard.tryReserve("spt_a") {
		t.Error("SPT should be released after a panic")
	}
}

func TestServeCardGated_CaptureFailure(t *testing.T) {
	gw := &fakeGateway{pi: "pi_3", capErr: io.ErrUnexpectedEOF}
	guard := newSPTReplayGuard(time.Hour)
	w := gateOnce(gw, guard, cardCredHeader("spt_a"), okProxy())
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 on capture failure", w.Code)
	}
	if len(gw.canceled) != 1 || gw.canceled[0] != "pi_3" {
		t.Errorf("capture failure must cancel the hold: %v", gw.canceled)
	}
	if !guard.tryReserve("spt_a") {
		t.Error("SPT should be released after capture failure")
	}
}
