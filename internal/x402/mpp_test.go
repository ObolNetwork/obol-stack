package x402

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tempoxyz/mpp-go/pkg/mpp"
	"github.com/tempoxyz/mpp-go/pkg/tempo"
)

func tempoTestRule() *RouteRule {
	return &RouteRule{
		Pattern:        "/services/tempo-foo/*",
		Price:          "0.50",
		Description:    "tempo test route",
		OfferNamespace: "default",
		OfferName:      "tempo-foo",
		MPPTempo: &TempoMPPRoute{
			PayTo:    "0x1111111111111111111111111111111111111111",
			Asset:    "0x2222222222222222222222222222222222222222",
			Decimals: 6,
			ChainID:  12345,
		},
	}
}

type fakeTempoGateway struct {
	preflightErr error
	settleErr    error
	preflightN   int
	settleN      int
	releaseN     int
}

func (f *fakeTempoGateway) preflight(_ *http.Request, _ *RouteRule) (*tempoMPPAuthorization, error) {
	f.preflightN++
	if f.preflightErr != nil {
		return nil, f.preflightErr
	}
	return &tempoMPPAuthorization{Authorization: "Payment test", ChallengeID: "challenge-1", Realm: "seller.example"}, nil
}

func (f *fakeTempoGateway) settle(context.Context, *tempoMPPAuthorization, *RouteRule) (*mpp.Receipt, error) {
	f.settleN++
	if f.settleErr != nil {
		return nil, f.settleErr
	}
	return mpp.Success("0xtx", mpp.WithReceiptMethod(mppMethodTempo)), nil
}

func (f *fakeTempoGateway) release(*tempoMPPAuthorization) { f.releaseN++ }

func gateTempoOnce(gw tempoMPPGateway, proxy http.Handler) *httptest.ResponseRecorder {
	rule := tempoTestRule()
	req := buildCardRequirement(cardTestRule())
	r := httptest.NewRequest(http.MethodPost, "/services/tempo-foo/x", nil)
	r.Host = "seller.example"
	r.Header.Set("Authorization", "Payment fake")
	w := httptest.NewRecorder()
	(&Verifier{}).serveTempoMPPGated(w, r, rule, req, nil, proxy, gw)
	return w
}

func TestServeTempoMPPGated_SettlesAfterUpstreamSuccess(t *testing.T) {
	gw := &fakeTempoGateway{}
	w := gateTempoOnce(gw, okProxy())
	if w.Code != http.StatusOK || w.Body.String() != "upstream-ok" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if gw.preflightN != 1 || gw.settleN != 1 || gw.releaseN != 0 {
		t.Fatalf("preflight=%d settle=%d release=%d", gw.preflightN, gw.settleN, gw.releaseN)
	}
	if w.Header().Get("Payment-Receipt") == "" || w.Header().Get("Authentication-Info") == "" {
		t.Fatalf("missing MPP receipt headers: %v", w.Header())
	}
}

func TestServeTempoMPPGated_UpstreamFailureDoesNotSettle(t *testing.T) {
	gw := &fakeTempoGateway{}
	w := gateTempoOnce(gw, failProxy())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", w.Code)
	}
	if gw.settleN != 0 || gw.releaseN != 1 {
		t.Fatalf("settle=%d release=%d", gw.settleN, gw.releaseN)
	}
}

func TestServeTempoMPPGated_PreflightFailureDoesNotReachUpstream(t *testing.T) {
	gw := &fakeTempoGateway{preflightErr: errors.New("bad credential")}
	called := false
	proxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, "should-not-run")
	})
	w := gateTempoOnce(gw, proxy)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d, want 402", w.Code)
	}
	if called {
		t.Fatal("upstream must not run after preflight failure")
	}
}

func TestServeTempoMPPGated_SettleFailureReleasesReservation(t *testing.T) {
	gw := &fakeTempoGateway{settleErr: errors.New("settle failed")}
	w := gateTempoOnce(gw, okProxy())
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", w.Code)
	}
	if gw.preflightN != 1 || gw.settleN != 1 || gw.releaseN != 1 {
		t.Fatalf("preflight=%d settle=%d release=%d", gw.preflightN, gw.settleN, gw.releaseN)
	}
	if w.Header().Get("Payment-Receipt") != "" || w.Header().Get("X-PAYMENT-RESPONSE") != "" {
		t.Fatalf("settlement failure must not emit receipt headers: %v", w.Header())
	}
}

func TestVerifierHandleProxyTempoAuthorizationUsesMPPGateway(t *testing.T) {
	t.Setenv(mppChallengeSecretEnv, "test-secret")
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/x" {
			t.Errorf("upstream path = %q, want /x", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "tempo-upstream-ok")
	}))
	defer upstream.Close()

	route := *tempoTestRule()
	route.UpstreamURL = upstream.URL
	route.StripPrefix = "/services/tempo-foo"
	challenge, err := tempoMPPChallenge(&route, "seller.example", mppChallengeSecret())
	if err != nil {
		t.Fatalf("tempoMPPChallenge: %v", err)
	}
	gw := &fakeTempoGateway{}
	oldGateway := defaultTempoMPPGateway
	defaultTempoMPPGateway = gw
	t.Cleanup(func() { defaultTempoMPPGateway = oldGateway })
	v := newTestVerifier(t, fac.URL, []RouteRule{route})

	r := httptest.NewRequest(http.MethodPost, "/services/tempo-foo/x", nil)
	r.Host = "seller.example"
	r.Header.Set("Authorization", challenge.NewCredential(map[string]any{
		"type":      string(tempo.CredentialTypeTransaction),
		"signature": "0x76",
	}).ToAuthorization())
	w := httptest.NewRecorder()
	v.HandleProxy(w, r)

	if w.Code != http.StatusOK || w.Body.String() != "tempo-upstream-ok" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if upstreamCalls != 1 || gw.preflightN != 1 || gw.settleN != 1 || gw.releaseN != 0 {
		t.Fatalf("upstream=%d preflight=%d settle=%d release=%d", upstreamCalls, gw.preflightN, gw.settleN, gw.releaseN)
	}
	if fac.verifyCalls.Load() != 0 || fac.settleCalls.Load() != 0 {
		t.Fatalf("Tempo MPP route must not use x402 facilitator: verify=%d settle=%d", fac.verifyCalls.Load(), fac.settleCalls.Load())
	}
}

func TestAddMPPAuthenticateHeadersAddsOneHeaderPerRail(t *testing.T) {
	t.Setenv(mppChallengeSecretEnv, "test-secret")
	rule := cardTestRule()
	rule.MPPTempo = tempoTestRule().MPPTempo
	rule.Price = "0.50"
	r := httptest.NewRequest(http.MethodGet, "/services/card-foo/x", nil)
	r.Host = "seller.example"
	w := httptest.NewRecorder()

	addMPPAuthenticateHeaders(w, r, rule)

	headers := w.Header().Values("WWW-Authenticate")
	if len(headers) != 2 {
		t.Fatalf("WWW-Authenticate headers = %v, want two rails", headers)
	}
	joined := strings.Join(headers, "\n")
	if !strings.Contains(joined, `method="stripe"`) || !strings.Contains(joined, `method="tempo"`) {
		t.Fatalf("WWW-Authenticate headers = %v, want stripe and tempo methods", headers)
	}
}

func TestMPPAuthorizationMethod(t *testing.T) {
	t.Setenv(mppChallengeSecretEnv, "test-secret")
	stripeChallenge, err := stripeMPPChallenge(cardTestRule(), "seller.example", mppChallengeSecret())
	if err != nil {
		t.Fatalf("stripeMPPChallenge: %v", err)
	}
	tempoChallenge, err := tempoMPPChallenge(tempoTestRule(), "seller.example", mppChallengeSecret())
	if err != nil {
		t.Fatalf("tempoMPPChallenge: %v", err)
	}
	tests := []struct {
		name string
		auth string
		want string
	}{
		{name: "stripe", auth: stripeChallenge.NewCredential(map[string]any{"spt": "spt_123"}).ToAuthorization(), want: mppMethodStripe},
		{name: "tempo", auth: tempoChallenge.NewCredential(map[string]any{"type": string(tempo.CredentialTypeTransaction), "signature": "0x76"}).ToAuthorization(), want: mppMethodTempo},
		{name: "missing", auth: "", want: ""},
		{name: "wrong scheme", auth: "Bearer token", want: ""},
		{name: "bad payment credential", auth: "Payment not-a-credential", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.auth != "" {
				r.Header.Set("Authorization", tt.auth)
			}
			if got := mppAuthorizationMethod(r); got != tt.want {
				t.Fatalf("mppAuthorizationMethod() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTempoMPPPreflightRejectsPushHashCredential(t *testing.T) {
	t.Setenv(mppChallengeSecretEnv, "test-secret")
	rule := tempoTestRule()
	challenge, err := tempoMPPChallenge(rule, "seller.example", mppChallengeSecret())
	if err != nil {
		t.Fatalf("tempoMPPChallenge: %v", err)
	}
	auth := challenge.NewCredential(map[string]any{"type": string(tempo.CredentialTypeHash), "hash": "0xabc"}).ToAuthorization()
	r := httptest.NewRequest(http.MethodPost, "/services/tempo-foo/x", nil)
	r.Host = "seller.example"
	r.Header.Set("Authorization", auth)

	gw := newTempoMPPGateway()
	if _, err := gw.preflight(r, rule); err == nil {
		t.Fatal("expected hash/push credential to be rejected")
	}
}

func TestTempoMPPPreflightRejectsBadTransactionCredential(t *testing.T) {
	t.Setenv(mppChallengeSecretEnv, "test-secret")
	rule := tempoTestRule()
	challenge, err := tempoMPPChallenge(rule, "seller.example", mppChallengeSecret())
	if err != nil {
		t.Fatalf("tempoMPPChallenge: %v", err)
	}
	tests := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name:    "missing signature",
			payload: map[string]any{"type": string(tempo.CredentialTypeTransaction)},
			want:    "missing signature",
		},
		{
			name:    "invalid serialized transaction",
			payload: map[string]any{"type": string(tempo.CredentialTypeTransaction), "signature": "0x76zz"},
			want:    "invalid Tempo transaction payload",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/services/tempo-foo/x", nil)
			r.Host = "seller.example"
			r.Header.Set("Authorization", challenge.NewCredential(tt.payload).ToAuthorization())
			_, err := newTempoMPPGateway().preflight(r, rule)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight err=%v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestTempoMPPGatewayReleaseAndSettleErrors(t *testing.T) {
	gw := newTempoMPPGateway()
	gw.reserved["challenge-1"] = time.Now()
	gw.release(&tempoMPPAuthorization{ChallengeID: "challenge-1"})
	if _, exists := gw.reserved["challenge-1"]; exists {
		t.Fatal("release should remove reserved challenge")
	}
	gw.release(nil)

	if receipt, err := gw.settle(context.Background(), nil, tempoTestRule()); err == nil || receipt != nil {
		t.Fatalf("settle(nil) receipt=%v err=%v, want error", receipt, err)
	}
	if receipt, err := gw.settle(context.Background(), &tempoMPPAuthorization{
		Authorization: "Payment invalid",
		Realm:         "seller.example",
	}, tempoTestRule()); err == nil || receipt != nil {
		t.Fatalf("settle(invalid auth) receipt=%v err=%v, want error", receipt, err)
	}
}
