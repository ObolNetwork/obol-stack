package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

const (
	testFeeRecipient      = "0x1111111111111111111111111111111111111111"
	testCaptureAuthorizer = "0x2222222222222222222222222222222222222222"
	testUnlockPayer       = "0xAbCdEfabcdefABCDefAbcdefabCDefABcDefAbCd"
)

func validAuthCaptureConfig() AuthCaptureUnlockConfig {
	return AuthCaptureUnlockConfig{
		Enabled:      true,
		OfferPrefix:  "/services/agent",
		Price:        "1.00",
		FeeRecipient: testFeeRecipient,
		// min == max required when enabled (revenue metrics assume a fixed fee).
		MinFeeBps:         250,
		MaxFeeBps:         250,
		CaptureAuthorizer: testCaptureAuthorizer,
	}
}

func TestBuildAuthCaptureRequirement_ExtraShape(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := validAuthCaptureConfig()
	asset := AssetInfo{
		Address:        ChainBaseSepolia.USDCAddress,
		Symbol:         "USDC",
		Decimals:       6,
		TransferMethod: "eip3009",
		EIP712Name:     "USDC",
		EIP712Version:  "2",
	}
	req, err := BuildAuthCaptureRequirement(
		ChainBaseSepolia,
		asset,
		&config,
		"0x3333333333333333333333333333333333333333",
		DefaultMaxTimeoutSeconds,
		now,
	)
	if err != nil {
		t.Fatalf("BuildAuthCaptureRequirement: %v", err)
	}
	if req.MaxTimeoutSeconds != int(DefaultMaxTimeoutSeconds) {
		t.Errorf("MaxTimeoutSeconds = %d, want %d (signing window, not capture deadline)", req.MaxTimeoutSeconds, DefaultMaxTimeoutSeconds)
	}
	if req.Scheme != "auth-capture" {
		t.Errorf("Scheme = %q, want auth-capture", req.Scheme)
	}
	if req.Network != ChainBaseSepolia.CAIP2Network {
		t.Errorf("Network = %q, want %q", req.Network, ChainBaseSepolia.CAIP2Network)
	}
	if req.Amount != "1000000" {
		t.Errorf("Amount = %q, want 1000000", req.Amount)
	}
	if len(req.Extra) != 10 {
		t.Fatalf("Extra has %d keys, want 10: %#v", len(req.Extra), req.Extra)
	}

	stringValues := map[string]string{
		"name":                asset.EIP712Name,
		"version":             asset.EIP712Version,
		"captureAuthorizer":   testCaptureAuthorizer,
		"feeRecipient":        testFeeRecipient,
		"assetTransferMethod": asset.TransferMethod,
	}
	for key, want := range stringValues {
		got, ok := req.Extra[key].(string)
		if !ok || got != want {
			t.Errorf("Extra[%q] = %#v (%T), want string %q", key, req.Extra[key], req.Extra[key], want)
		}
	}
	captureDeadline, ok := req.Extra["captureDeadline"].(int64)
	if !ok {
		t.Fatalf("captureDeadline = %#v (%T), want int64", req.Extra["captureDeadline"], req.Extra["captureDeadline"])
	}
	refundDeadline, ok := req.Extra["refundDeadline"].(int64)
	if !ok {
		t.Fatalf("refundDeadline = %#v (%T), want int64", req.Extra["refundDeadline"], req.Extra["refundDeadline"])
	}
	if captureDeadline <= now.Unix() {
		t.Errorf("captureDeadline = %d, want after %d", captureDeadline, now.Unix())
	}
	if refundDeadline < captureDeadline {
		t.Errorf("refundDeadline = %d, want >= captureDeadline %d", refundDeadline, captureDeadline)
	}
	if got, ok := req.Extra["minFeeBps"].(uint16); !ok || got != config.MinFeeBps {
		t.Errorf("minFeeBps = %#v (%T), want uint16(%d)", req.Extra["minFeeBps"], req.Extra["minFeeBps"], config.MinFeeBps)
	}
	if got, ok := req.Extra["maxFeeBps"].(uint16); !ok || got != config.MaxFeeBps {
		t.Errorf("maxFeeBps = %#v (%T), want uint16(%d)", req.Extra["maxFeeBps"], req.Extra["maxFeeBps"], config.MaxFeeBps)
	}
	if got, ok := req.Extra["autoCapture"].(bool); !ok || !got {
		t.Errorf("autoCapture = %#v (%T), want bool(true)", req.Extra["autoCapture"], req.Extra["autoCapture"])
	}
}

func TestAuthCaptureUnlockConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*AuthCaptureUnlockConfig)
		wantErr bool
	}{
		{
			name: "rejects min fee above max fee",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.MinFeeBps = 251
				c.MaxFeeBps = 250
			},
			wantErr: true,
		},
		{
			// Revenue metrics assume a fixed fee; a range would silently corrupt
			// attribution until the facilitator reports the applied bps.
			name: "rejects fee range when enabled (min != max)",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.MinFeeBps = 0
				c.MaxFeeBps = 100
			},
			wantErr: true,
		},
		{
			name: "accepts fixed fee when enabled (min == max)",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.MinFeeBps = 50
				c.MaxFeeBps = 50
			},
			wantErr: false,
		},
		{
			// Disabled configs may still carry a range for future use; only
			// MinFeeBps > MaxFeeBps is rejected outside the enabled block.
			name: "allows fee range when disabled",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.Enabled = false
				c.MinFeeBps = 0
				c.MaxFeeBps = 100
			},
			wantErr: false,
		},
		{
			name: "rejects max fee above 10000",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.MaxFeeBps = 10001
			},
			wantErr: true,
		},
		{
			name: "rejects positive max fee with empty recipient",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.FeeRecipient = ""
			},
			wantErr: true,
		},
		{
			name: "rejects zero capture authorizer",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.CaptureAuthorizer = "0x0000000000000000000000000000000000000000"
			},
			wantErr: true,
		},
		{
			name: "rejects refund deadline before capture deadline",
			mutate: func(c *AuthCaptureUnlockConfig) {
				c.CaptureDeadlineSecs = 1000
				c.RefundDeadlineSecs = 500
			},
			wantErr: true,
		},
		{
			name:    "accepts good config and applies defaults",
			mutate:  func(*AuthCaptureUnlockConfig) {},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := validAuthCaptureConfig()
			tt.mutate(&config)
			wantCaptureDeadline := config.CaptureDeadlineSecs
			if wantCaptureDeadline == 0 {
				wantCaptureDeadline = defaultCaptureDeadlineSecs
			}
			wantRefundDeadline := config.RefundDeadlineSecs
			if wantRefundDeadline == 0 {
				wantRefundDeadline = defaultRefundDeadlineSecs
			}
			err := config.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if config.CaptureDeadlineSecs != wantCaptureDeadline {
				t.Errorf("CaptureDeadlineSecs = %d, want %d", config.CaptureDeadlineSecs, wantCaptureDeadline)
			}
			if config.RefundDeadlineSecs != wantRefundDeadline {
				t.Errorf("RefundDeadlineSecs = %d, want %d", config.RefundDeadlineSecs, wantRefundDeadline)
			}
		})
	}
}

func TestPaidUnlock_InlineFirstMessage(t *testing.T) {
	var facilitatorCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isValid":true,"payer":%q}`, testUnlockPayer)
	})
	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"payer":%q,"transaction":"0xabc","network":"eip155:84532"}`, testUnlockPayer)
	})
	facilitator := httptest.NewServer(mux)
	t.Cleanup(facilitator.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("CHAT_OK"))
	}))
	t.Cleanup(upstream.Close)

	unlockConfig := validAuthCaptureConfig()
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0x3333333333333333333333333333333333333333",
		Chain:          "base-sepolia",
		FacilitatorURL: facilitator.URL,
		Routes: []RouteRule{{
			Pattern:        "/services/agent/*",
			Gate:           "auth",
			StripPrefix:    unlockConfig.OfferPrefix,
			UpstreamURL:    upstream.URL,
			OfferNamespace: "test",
			OfferName:      "agent",
		}},
		AuthCaptureUnlock: &unlockConfig,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	path := unlockConfig.OfferPrefix + "/chat"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("challenge status = %d, want 402; body: %s", w.Code, w.Body.String())
	}
	var challenge struct {
		X402Version int                             `json:"x402Version"`
		Accepts     []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if challenge.X402Version != 2 {
		t.Errorf("x402Version = %d, want 2", challenge.X402Version)
	}
	if len(challenge.Accepts) != 1 || challenge.Accepts[0].Scheme != "auth-capture" {
		t.Errorf("challenge accepts = %#v, want one auth-capture requirement", challenge.Accepts)
	}
	if got := facilitatorCalls.Load(); got != 0 {
		t.Fatalf("facilitator calls after challenge = %d, want 0", got)
	}

	// A real v2 wire PaymentPayload carries the signed requirement in `accepted`
	// (scheme/network are read from it); the verifier settles against that, so it
	// must pass validateSignedAuthCapture (which it does — it IS the
	// challenge requirement the verifier just issued).
	wireJSON, _ := json.Marshal(map[string]any{
		"x402Version": 2,
		"payload":     map[string]any{"stub": true},
		"accepted":    challenge.Accepts[0],
	})
	paymentHeader := base64.StdEncoding.EncodeToString(wireJSON)
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-PAYMENT", paymentHeader)
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("paid message status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CHAT_OK") {
		t.Fatalf("paid message body = %q, want CHAT_OK", w.Body.String())
	}

	var sessionCookie *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == SIWXSessionCookie {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("unlock response did not set obol_siwx cookie")
	}
	wallet, err := v.siwx.VerifySession(sessionCookie.Value, time.Now())
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if want := strings.ToLower(testUnlockPayer); wallet != want {
		t.Errorf("session wallet = %q, want %q", wallet, want)
	}
	labels := prometheus.Labels{
		"network":       challenge.Accepts[0].Network,
		"asset":         challenge.Accepts[0].Asset,
		"fee_recipient": testFeeRecipient,
	}
	if got := testutil.ToFloat64(v.metrics.feeRevenueAtomic.With(labels)); got != 25000 {
		t.Errorf("fee revenue atomic = %v, want 25000", got)
	}
	if got := testutil.ToFloat64(v.metrics.settledVolumeAtomic.With(labels)); got != 1000000 {
		t.Errorf("settled volume atomic = %v, want 1000000", got)
	}

	paidCalls := facilitatorCalls.Load()
	if paidCalls == 0 {
		t.Fatal("paid message did not call facilitator")
	}

	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("free-ride status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CHAT_OK") {
		t.Fatalf("free-ride body = %q, want CHAT_OK", w.Body.String())
	}
	if got := facilitatorCalls.Load(); got != paidCalls {
		t.Errorf("facilitator calls after free ride = %d, want unchanged %d", got, paidCalls)
	}
}

// TestPaidUnlock_SettleErrorSurfacesTxHash locks in the forensic contract: when
// the facilitator submits the settle tx on-chain and THEN fails on the receipt
// path (non-200 with a tx hash in the body), the buyer must get the tx hash back
// (X-PAYMENT-RESPONSE + a "you may pay twice" hint) rather than a bare error —
// otherwise an on-chain debit goes silent with no way to reconcile.
func TestPaidUnlock_SettleErrorSurfacesTxHash(t *testing.T) {
	const settledTx = "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	mux := http.NewServeMux()
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isValid":true,"payer":%q}`, testUnlockPayer)
	})
	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		// tx landed, receipt path 5xx'd: return the tx hash with a non-200.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"success":false,"transaction":%q,"network":"eip155:84532","payer":%q,"errorReason":"receipt_timeout"}`, settledTx, testUnlockPayer)
	})
	facilitator := httptest.NewServer(mux)
	t.Cleanup(facilitator.Close)

	unlockConfig := validAuthCaptureConfig()
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0x3333333333333333333333333333333333333333",
		Chain:          "base-sepolia",
		FacilitatorURL: facilitator.URL,
		Routes: []RouteRule{{
			Pattern:        "/services/agent/*",
			Gate:           "auth",
			StripPrefix:    unlockConfig.OfferPrefix,
			UpstreamURL:    "http://upstream.invalid",
			OfferNamespace: "test",
			OfferName:      "agent",
		}},
		AuthCaptureUnlock: &unlockConfig,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	path := unlockConfig.OfferPrefix + "/chat"
	challengeReq := httptest.NewRequest(http.MethodGet, path, nil)
	cw := httptest.NewRecorder()
	v.HandleProxy(cw, challengeReq)
	var challenge struct {
		Accepts []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(cw.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	wireJSON, _ := json.Marshal(map[string]any{
		"x402Version": 2, "payload": map[string]any{"stub": true}, "accepted": challenge.Accepts[0],
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-PAYMENT", base64.StdEncoding.EncodeToString(wireJSON))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-PAYMENT-RESPONSE"); got == "" {
		t.Error("X-PAYMENT-RESPONSE header not set — the on-chain tx hash is lost")
	} else {
		decoded, derr := base64.StdEncoding.DecodeString(got)
		if derr != nil || !strings.Contains(string(decoded), settledTx) {
			t.Errorf("X-PAYMENT-RESPONSE does not carry the settle tx: %s", string(decoded))
		}
	}
	if !strings.Contains(w.Body.String(), "pay twice") {
		t.Errorf("error body missing the reconcile hint: %s", w.Body.String())
	}
	// A failed settle must NOT mint a session.
	for _, c := range w.Result().Cookies() {
		if c.Name == SIWXSessionCookie {
			t.Error("session cookie minted despite settle failure")
		}
	}
}

// TestValidateSignedAuthCapture_UnlockPath guards the unlock money path: the
// verifier settles the requirement the CLIENT signed (payload.accepted), so a
// client must not be able to redirect the fee, underpay, or swap the
// authorizer. Retargeted from the deleted validateSignedUnlockRequirement onto
// the shared validateSignedAuthCapture (stricter: whole Extra map + capture
// deadline upper bound). Each case JSON-round-trips a genuine requirement (so
// Extra numbers are float64 as via ToPaymentPayload), flips one field, and
// asserts rejection. Deadline drift within bounds is the permitted exception.
func TestValidateSignedAuthCapture_UnlockPath(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	cfg := validAuthCaptureConfig()
	asset := AssetInfo{
		Address:        ChainBaseSepolia.USDCAddress,
		Symbol:         "USDC",
		Decimals:       6,
		TransferMethod: "eip3009",
		EIP712Name:     "USDC",
		EIP712Version:  "2",
	}
	const payTo = "0x3333333333333333333333333333333333333333"
	const attacker = "0x9999999999999999999999999999999999999999"
	expected, err := BuildAuthCaptureRequirement(ChainBaseSepolia, asset, &cfg, payTo, DefaultMaxTimeoutSeconds, now)
	if err != nil {
		t.Fatalf("BuildAuthCaptureRequirement: %v", err)
	}
	// signedFrom returns `expected` after a JSON round-trip (Extra numbers ->
	// float64), then applies mutate — mirroring the real client->ToPaymentPayload
	// decode path so Extra numeric coercion is actually exercised.
	signedFrom := func(mutate func(*x402types.PaymentRequirements)) x402types.PaymentRequirements {
		raw, _ := json.Marshal(expected)
		var s x402types.PaymentRequirements
		_ = json.Unmarshal(raw, &s)
		if mutate != nil {
			mutate(&s)
		}
		return s
	}
	captureDeadline := func(s *x402types.PaymentRequirements) int64 {
		return int64(s.Extra["captureDeadline"].(float64))
	}

	tests := []struct {
		name    string
		mutate  func(*x402types.PaymentRequirements)
		wantErr string // substring; "" means expect nil
	}{
		{"accepts genuine requirement", nil, ""},
		{"accepts deadline drift within bounds", func(s *x402types.PaymentRequirements) {
			s.Extra["captureDeadline"] = float64(now.Unix() + 7)
		}, ""},
		{"rejects wrong scheme", func(s *x402types.PaymentRequirements) { s.Scheme = "exact" }, "scheme"},
		{"rejects wrong network", func(s *x402types.PaymentRequirements) { s.Network = "eip155:1" }, "network"},
		{"rejects wrong asset", func(s *x402types.PaymentRequirements) { s.Asset = attacker }, "asset"},
		{"rejects redirected payTo", func(s *x402types.PaymentRequirements) { s.PayTo = attacker }, "payTo"},
		{"rejects reduced amount", func(s *x402types.PaymentRequirements) { s.Amount = "1" }, "amount"},
		{"rejects swapped captureAuthorizer", func(s *x402types.PaymentRequirements) { s.Extra["captureAuthorizer"] = attacker }, "captureAuthorizer"},
		{"rejects redirected feeRecipient", func(s *x402types.PaymentRequirements) { s.Extra["feeRecipient"] = attacker }, "feeRecipient"},
		{"rejects lowered maxFeeBps", func(s *x402types.PaymentRequirements) { s.Extra["maxFeeBps"] = float64(1) }, "maxFeeBps"},
		{"rejects altered minFeeBps", func(s *x402types.PaymentRequirements) { s.Extra["minFeeBps"] = float64(0) }, "minFeeBps"},
		{"rejects autoCapture false", func(s *x402types.PaymentRequirements) { s.Extra["autoCapture"] = false }, "autoCapture"},
		{"rejects wrong assetTransferMethod", func(s *x402types.PaymentRequirements) { s.Extra["assetTransferMethod"] = "permit2" }, "assetTransferMethod"},
		{"rejects missing extra", func(s *x402types.PaymentRequirements) { s.Extra = nil }, "extra"},
		{"rejects expired captureDeadline", func(s *x402types.PaymentRequirements) { s.Extra["captureDeadline"] = float64(now.Unix() + 6) }, "captureDeadline"},
		{"rejects inverted refundDeadline", func(s *x402types.PaymentRequirements) { s.Extra["refundDeadline"] = float64(captureDeadline(s) - 1) }, "refundDeadline"},
		// validateSignedAuthCapture also upper-bounds captureDeadline (the old
		// unlock-only validator did not).
		{"rejects captureDeadline past configured window", func(s *x402types.PaymentRequirements) {
			s.Extra["captureDeadline"] = float64(now.Unix() + int64(cfg.CaptureDeadlineSecs) + 1)
		}, "captureDeadline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signed := signedFrom(tt.mutate)
			err := validateSignedAuthCapture(signed, expected, cfg.CaptureDeadlineSecs, now.Unix())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestPaidUnlock_RejectsTamperedPayment is the end-to-end guard: a client that
// resubmits a payment whose signed `accepted` redirects the fee to itself must
// be rejected BEFORE the facilitator is called and MUST NOT mint a session.
func TestPaidUnlock_RejectsTamperedPayment(t *testing.T) {
	var facilitatorCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		fmt.Fprint(w, `{"isValid":true}`)
	})
	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		fmt.Fprint(w, `{"success":true}`)
	})
	facilitator := httptest.NewServer(mux)
	t.Cleanup(facilitator.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("CHAT_OK"))
	}))
	t.Cleanup(upstream.Close)

	unlockConfig := validAuthCaptureConfig()
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0x3333333333333333333333333333333333333333",
		Chain:          "base-sepolia",
		FacilitatorURL: facilitator.URL,
		Routes: []RouteRule{{
			Pattern:        "/services/agent/*",
			Gate:           "auth",
			StripPrefix:    unlockConfig.OfferPrefix,
			UpstreamURL:    upstream.URL,
			OfferNamespace: "test",
			OfferName:      "agent",
		}},
		AuthCaptureUnlock: &unlockConfig,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	path := unlockConfig.OfferPrefix + "/chat"
	cw := httptest.NewRecorder()
	v.HandleProxy(cw, httptest.NewRequest(http.MethodGet, path, nil))
	var challenge struct {
		Accepts []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(cw.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}

	// Redirect the fee to an attacker in the signed requirement.
	tampered := challenge.Accepts[0]
	ex := make(map[string]any, len(tampered.Extra))
	for k, val := range tampered.Extra {
		ex[k] = val
	}
	ex["feeRecipient"] = "0x9999999999999999999999999999999999999999"
	tampered.Extra = ex
	wireJSON, _ := json.Marshal(map[string]any{
		"x402Version": 2,
		"payload":     map[string]any{"stub": true},
		"accepted":    tampered,
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-PAYMENT", base64.StdEncoding.EncodeToString(wireJSON))
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "payment_policy_mismatch") {
		t.Errorf("body = %q, want payment_policy_mismatch", w.Body.String())
	}
	if got := facilitatorCalls.Load(); got != 0 {
		t.Errorf("facilitator called %d times on tampered payment, want 0 (rejected pre-forward)", got)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == SIWXSessionCookie {
			t.Error("tampered payment minted a session cookie")
		}
	}
}
