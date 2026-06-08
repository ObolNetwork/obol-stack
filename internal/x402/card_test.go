package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
			NetworkID: "stripenet_abc",
		},
	}
}

func TestBuildCardRequirement(t *testing.T) {
	req := buildCardRequirement(cardTestRule())

	if req.Scheme != cardScheme {
		t.Errorf("scheme = %q, want %q", req.Scheme, cardScheme)
	}
	if req.Network != cardNetworkStripe {
		t.Errorf("network = %q, want %q", req.Network, cardNetworkStripe)
	}
	if req.PayTo != "acct_test123" {
		t.Errorf("payTo = %q, want acct_test123", req.PayTo)
	}
	// "0.01" usd (2 decimals) -> 1 minor unit (cent).
	if req.Amount != "1" {
		t.Errorf("amount = %q, want 1 (minor units)", req.Amount)
	}
	if req.Asset != "" {
		t.Errorf("asset = %q, want empty (no on-chain asset for card)", req.Asset)
	}
	if req.Extra["method"] != cardNetworkStripe {
		t.Errorf("extra.method = %v, want stripe", req.Extra["method"])
	}
	if req.Extra["currency"] != "usd" {
		t.Errorf("extra.currency = %v, want usd", req.Extra["currency"])
	}
	if req.Extra["networkId"] != "stripenet_abc" {
		t.Errorf("extra.networkId = %v, want stripenet_abc", req.Extra["networkId"])
	}
	// Defaulted payment-method types.
	pmt, ok := req.Extra["paymentMethodTypes"].([]string)
	if !ok || len(pmt) != 1 || pmt[0] != "card" {
		t.Errorf("extra.paymentMethodTypes = %v, want [card]", req.Extra["paymentMethodTypes"])
	}
}

func TestParseCardCredential(t *testing.T) {
	b64 := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.StdEncoding.EncodeToString(b)
	}

	t.Run("bare payload", func(t *testing.T) {
		cred, err := parseCardCredential(b64(map[string]string{"spt": "spt_abc", "externalId": "e1"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cred.SPT != "spt_abc" || cred.ExternalID != "e1" {
			t.Errorf("got %+v", cred)
		}
	})

	t.Run("wrapped payload", func(t *testing.T) {
		cred, err := parseCardCredential(b64(map[string]any{"payload": map[string]string{"spt": "spt_xyz"}}))
		if err != nil || cred.SPT != "spt_xyz" {
			t.Fatalf("got %+v err=%v", cred, err)
		}
	})

	for _, bad := range []struct {
		name, header string
	}{
		{"bad base64", "!!!not-base64!!!"},
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

func TestBuildPaymentIntentForm(t *testing.T) {
	form := buildPaymentIntentForm("1", "usd", "spt_abc")
	want := map[string]string{
		"amount":                             "1",
		"currency":                           "usd",
		"confirm":                            "true",
		"shared_payment_granted_token":       "spt_abc",
		"automatic_payment_methods[enabled]": "true",
		"automatic_payment_methods[allow_redirects]": "never",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("form[%q] = %q, want %q", k, form.Get(k), v)
		}
	}
}

func TestServeCardGated_NoPayment402(t *testing.T) {
	rule := cardTestRule()
	req := buildCardRequirement(rule)
	proxied := false
	proxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { proxied = true })

	r := httptest.NewRequest(http.MethodPost, "/services/card-foo/x", nil)
	w := httptest.NewRecorder()

	(&Verifier{}).serveCardGated(w, r, rule, req, nil, proxy, func(context.Context, *CardRoute, string, string, cardCredential) (string, error) {
		t.Fatal("settle must not be called without a credential")
		return "", nil
	})

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if proxied {
		t.Error("upstream must not be proxied on 402")
	}
	var body struct {
		Accepts []struct {
			Scheme string `json:"scheme"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if len(body.Accepts) != 1 || body.Accepts[0].Scheme != cardScheme {
		t.Errorf("402 accepts = %+v, want one card entry", body.Accepts)
	}
}

func TestServeCardGated_PaidProxies(t *testing.T) {
	rule := cardTestRule()
	req := buildCardRequirement(rule)
	proxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "upstream-ok")
	})

	cred, _ := json.Marshal(map[string]string{"spt": "spt_live", "externalId": "e9"})
	r := httptest.NewRequest(http.MethodPost, "/services/card-foo/x", nil)
	r.Header.Set("X-PAYMENT", base64.StdEncoding.EncodeToString(cred))
	w := httptest.NewRecorder()

	var gotAmount, gotCurrency, gotSPT, gotAccount string
	settle := func(_ context.Context, card *CardRoute, amount, currency string, c cardCredential) (string, error) {
		gotAmount, gotCurrency, gotSPT, gotAccount = amount, currency, c.SPT, card.Account
		return "pi_123", nil
	}

	(&Verifier{}).serveCardGated(w, r, rule, req, nil, proxy, settle)

	if w.Code != http.StatusOK || w.Body.String() != "upstream-ok" {
		t.Fatalf("status=%d body=%q, want 200/upstream-ok", w.Code, w.Body.String())
	}
	if gotAmount != "1" || gotCurrency != "usd" || gotSPT != "spt_live" || gotAccount != "acct_test123" {
		t.Errorf("settle args: amount=%q currency=%q spt=%q account=%q", gotAmount, gotCurrency, gotSPT, gotAccount)
	}
	// Receipt header references the PaymentIntent.
	hdr := w.Header().Get("X-PAYMENT-RESPONSE")
	if hdr == "" {
		t.Fatal("missing X-PAYMENT-RESPONSE header")
	}
	dec, _ := base64.StdEncoding.DecodeString(hdr)
	var receipt map[string]string
	_ = json.Unmarshal(dec, &receipt)
	if receipt["reference"] != "pi_123" || receipt["method"] != cardNetworkStripe {
		t.Errorf("receipt = %v, want reference pi_123 / method stripe", receipt)
	}
}

func TestServeCardGated_SettleFailure402(t *testing.T) {
	rule := cardTestRule()
	req := buildCardRequirement(rule)
	proxied := false
	proxy := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxied = true })

	cred, _ := json.Marshal(map[string]string{"spt": "spt_decline"})
	r := httptest.NewRequest(http.MethodPost, "/services/card-foo/x", nil)
	r.Header.Set("X-PAYMENT", base64.StdEncoding.EncodeToString(cred))
	w := httptest.NewRecorder()

	(&Verifier{}).serveCardGated(w, r, rule, req, nil, proxy, func(context.Context, *CardRoute, string, string, cardCredential) (string, error) {
		return "", io.ErrUnexpectedEOF // simulate a declined/erroring charge
	})

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 on settle failure", w.Code)
	}
	if proxied {
		t.Error("upstream must not be proxied when settlement fails")
	}
}
