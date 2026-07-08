package x402

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/jobbroker"
)

// TestVerifier_AsyncOffer_EndToEnd runs the full M4 loop with a REAL broker
// behind the verifier: paid submit → verify + settle → broker 202 with
// job id → free status poll → payer-gated result — the buyer's whole
// journey, minus only Traefik.
func TestVerifier_AsyncOffer_EndToEnd(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-upstream" {
			t.Errorf("upstream auth = %q, want broker-injected bearer", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"report":"clean"}`))
	}))
	defer upstream.Close()

	store, err := jobbroker.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	broker := httptest.NewServer(jobbroker.NewServer(store).Handler())
	defer broker.Close()

	rules := []RouteRule{
		{
			// Price matches testPaymentHeader (100 atomic units).
			Pattern:         "/services/audit/*",
			Price:           "0.0001",
			UpstreamURL:     upstream.URL,
			UpstreamAuth:    "Bearer sk-upstream",
			StripPrefix:     "/services/audit",
			OfferNamespace:  "sec",
			OfferName:       "audit",
			Async:           true,
			BrokerURL:       broker.URL,
			AsyncTTL:        "72h0m0s",
			AsyncVisibility: "payer",
			PayTo:           "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
		{
			Pattern:        "/services/audit/jobs/*",
			Gate:           "free",
			UpstreamURL:    upstream.URL,
			StripPrefix:    "/services/audit",
			OfferNamespace: "sec",
			OfferName:      "audit",
			Async:          true,
			BrokerURL:      broker.URL,
		},
	}
	// Mirror the production route source: rules are specificity-sorted so
	// the free /jobs/* carve-out beats the paid catch-all.
	sortRoutesBySpecificity(rules)
	v := newTestVerifier(t, fac.URL, rules)

	// 1. Paid submit → 202 from the broker, settled by the verifier.
	req := httptest.NewRequest(http.MethodPost, "/services/audit/submit", strings.NewReader(`{"source":"contract X {}"}`))
	req.Host = "shop.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", testPaymentHeader(t))
	// Forged broker-contract headers must not survive the gate.
	req.Header.Set("X-Obol-Upstream-Url", "http://evil.example")
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit = %d (%s), want 202", w.Code, w.Body.String())
	}
	if fac.settleCalls.Load() != 1 {
		t.Fatalf("settleCalls = %d, want 1 (payment settles at acceptance)", fac.settleCalls.Load())
	}
	var accepted struct {
		JobID     string `json:"jobId"`
		StatusURL string `json:"statusUrl"`
		JobToken  string `json:"jobToken"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.JobID == "" {
		t.Fatalf("202 body: %v / %s", err, w.Body.String())
	}
	if accepted.StatusURL != "/services/audit/jobs/"+accepted.JobID {
		t.Errorf("statusUrl = %q, want public-prefixed", accepted.StatusURL)
	}

	// 2. Free status poll through the verifier until complete.
	var state string
	for i := 0; i < 100; i++ {
		req = httptest.NewRequest(http.MethodGet, accepted.StatusURL, nil)
		req.Host = "shop.example.com"
		w = httptest.NewRecorder()
		v.HandleProxy(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status poll = %d (%s)", w.Code, w.Body.String())
		}
		var status struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &status)
		state = status.State
		if state == "complete" || state == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "complete" {
		t.Fatalf("job state = %q, want complete", state)
	}
	if fac.verifyCalls.Load() != 1 {
		t.Errorf("status polls hit the facilitator (verifyCalls = %d)", fac.verifyCalls.Load())
	}

	// 3. Result: anonymous 401; jobToken bearer 200 (the broker checks the
	// token — the verifier's opportunistic SIWX pass leaves it alone);
	// SIWX wallet of the payer 200.
	resultURL := accepted.StatusURL + "/result"
	req = httptest.NewRequest(http.MethodGet, resultURL, nil)
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous result = %d, want 401", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, resultURL, nil)
	req.Host = "shop.example.com"
	req.Header.Set("Authorization", "Bearer "+accepted.JobToken)
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "clean") {
		t.Fatalf("jobToken result = %d (%s), want the stored upstream body", w.Code, w.Body.String())
	}

	// The mock facilitator's payer is "0xmockpayer" — a SIWX session for
	// that wallet opens the result via the verifier's opportunistic auth.
	token := v.siwx.MintSession("0xmockpayer", time.Now())
	req = httptest.NewRequest(http.MethodGet, resultURL, nil)
	req.Host = "shop.example.com"
	req.AddCookie(&http.Cookie{Name: SIWXSessionCookie, Value: token})
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("payer-wallet result = %d (%s), want 200", w.Code, w.Body.String())
	}
}

// TestVerifier_FreeQuota grants a SIWX wallet N free calls/day on a paid
// route, then falls back to the 402.
func TestVerifier_FreeQuota(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("served"))
	}))
	defer upstream.Close()

	v := newTestVerifier(t, fac.URL, []RouteRule{{
		Pattern:        "/services/data/*",
		Price:          "0.0001",
		UpstreamURL:    upstream.URL,
		StripPrefix:    "/services/data",
		OfferNamespace: "d",
		OfferName:      "data",
		FreeQuota:      2,
	}})

	msg, sig, _ := signSIWX(t, "shop.example.com", "fq-1", time.Now())
	cred := "SIWX " + base64.StdEncoding.EncodeToString([]byte(msg)) + "." + base64.StdEncoding.EncodeToString([]byte(sig))
	// One SIWX message is single-use (nonce); mint a session for repeats.
	req := httptest.NewRequest(http.MethodGet, "/services/data/x", nil)
	req.Host = "shop.example.com"
	req.Header.Set("Authorization", cred)
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("free-tier call 1 = %d (%s)", w.Code, w.Body.String())
	}

	wallet, _ := v.siwx.VerifySession(v.siwx.MintSession("0xanyone", time.Now()), time.Now())
	_ = wallet
	session := v.siwx.MintSession("0xfreeloader", time.Now())
	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/services/data/x", nil)
		req.Host = "shop.example.com"
		req.Header.Set("Authorization", "Bearer "+session)
		w := httptest.NewRecorder()
		v.HandleProxy(w, req)
		return w.Code
	}
	if c := call(); c != http.StatusOK {
		t.Fatalf("freeloader call 1 = %d", c)
	}
	if c := call(); c != http.StatusOK {
		t.Fatalf("freeloader call 2 = %d", c)
	}
	if c := call(); c != http.StatusPaymentRequired {
		t.Fatalf("freeloader call 3 = %d, want 402 (quota spent)", c)
	}
	if fac.verifyCalls.Load() != 0 {
		t.Errorf("free-tier calls hit the facilitator")
	}

	// Anonymous requests never ride the quota.
	req = httptest.NewRequest(http.MethodGet, "/services/data/x", nil)
	req.Host = "shop.example.com"
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("anonymous = %d, want 402", w.Code)
	}
}
