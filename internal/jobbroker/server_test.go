package jobbroker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(store)
}

// submit posts a gated async request the way the verifier does: contract
// headers set, offer prefix already stripped.
func submit(t *testing.T, srv *Server, upstreamURL, payer, visibility, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderUpstreamURL, upstreamURL)
	req.Header.Set(HeaderOffer, "sec/audit")
	req.Header.Set(HeaderPayTo, "0xseller00000000000000000000000000000000ff")
	req.Header.Set(HeaderPaymentPayer, payer)
	req.Header.Set(HeaderVisibility, visibility)
	req.Header.Set(HeaderPublicPrefix, "/services/audit")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit = %d (%s), want 202", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/services/audit/jobs/") {
		t.Fatalf("Location = %q, want public-prefixed job path", loc)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("202 body: %v", err)
	}
	return resp
}

// waitState polls until the job reaches a terminal state.
func waitState(t *testing.T, srv *Server, id string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		job, err := srv.store.Get(id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.State == StateComplete || job.State == StateFailed {
			return job.State
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return ""
}

func TestBroker_SubmitRunResult_PayerGated(t *testing.T) {
	var gotPath, gotPayer atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		gotPayer.Store(r.Header.Get(HeaderPaymentPayer))
		w.Header().Set("Content-Type", "text/markdown")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# audit report\nall good"))
	}))
	defer upstream.Close()

	srv := testServer(t)
	resp := submit(t, srv, upstream.URL, "0xPayer0000000000000000000000000000000001", "payer", `{"source":"contract X {}"}`)
	id := resp["jobId"].(string)
	token := resp["jobToken"].(string)

	if waitState(t, srv, id) != StateComplete {
		t.Fatal("job failed")
	}
	if gotPath.Load() != "/submit" || gotPayer.Load() == "" {
		t.Errorf("upstream saw path=%v payer=%v", gotPath.Load(), gotPayer.Load())
	}

	// Status page: free JSON with resultUrl; HTML for browsers.
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var status map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if status["state"] != "complete" || status["resultUrl"] == nil {
		t.Fatalf("status = %v", status)
	}
	req = httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "Fetch the result") {
		t.Errorf("html status page missing result link")
	}

	// Result access: anonymous → 401; wrong wallet → 401; payer wallet →
	// 200; jobToken bearer → 200.
	fetch := func(mod func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/result", nil)
		if mod != nil {
			mod(req)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
	if w := fetch(nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous result = %d, want 401", w.Code)
	}
	if w := fetch(func(r *http.Request) { r.Header.Set(HeaderVerifiedWallet, "0xsomeoneelse") }); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong wallet result = %d, want 401", w.Code)
	}
	if w := fetch(func(r *http.Request) { r.Header.Set(HeaderVerifiedWallet, "0xpayer0000000000000000000000000000000001") }); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "audit report") {
		t.Errorf("payer wallet result = %d (%s)", w.Code, w.Body.String())
	}
	if w := fetch(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }); w.Code != http.StatusOK {
		t.Errorf("jobToken result = %d", w.Code)
	}
	if w := fetch(func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); w.Code != http.StatusUnauthorized {
		t.Errorf("bad token result = %d, want 401", w.Code)
	}

	// Prefer: redirect on a complete job → 303 to the result.
	req = httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	req.Header.Set("Prefer", "redirect")
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Errorf("Prefer: redirect = %d, want 303", w.Code)
	}
}

func TestBroker_FailedJob_PaidAndReported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	srv := testServer(t)
	resp := submit(t, srv, upstream.URL, "0xpayer", "payer", `{}`)
	id := resp["jobId"].(string)
	if waitState(t, srv, id) != StateFailed {
		t.Fatal("want failed state")
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/result", nil)
	req.Header.Set("Authorization", "Bearer "+resp["jobToken"].(string))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "info.contact") {
		t.Errorf("failed result = %d (%s), want 502 + operator-contact pointer", w.Code, w.Body.String())
	}
}

func TestBroker_PublicVisibilityAndListing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	srv := testServer(t)
	resp := submit(t, srv, upstream.URL, "0xbuyer", "public", `{}`)
	id := resp["jobId"].(string)
	waitState(t, srv, id)

	// Public visibility: the unguessable id is the capability.
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/result", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("public result = %d, want 200", w.Code)
	}

	// Listing: anonymous 401; buyer sees own; seller (payTo) sees all.
	list := func(wallet string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
		req.Header.Set(HeaderOffer, "sec/audit")
		if wallet != "" {
			req.Header.Set(HeaderVerifiedWallet, wallet)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
	if w := list(""); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d", w.Code)
	}
	var buyerList struct {
		Jobs []ListSummary `json:"jobs"`
	}
	if w := list("0xbuyer"); w.Code != http.StatusOK {
		t.Fatalf("buyer list = %d", w.Code)
	} else if json.Unmarshal(w.Body.Bytes(), &buyerList); len(buyerList.Jobs) != 1 {
		t.Errorf("buyer sees %d jobs, want 1", len(buyerList.Jobs))
	}
	if w := list("0xSELLER00000000000000000000000000000000ff"); w.Code != http.StatusOK {
		t.Errorf("seller list = %d", w.Code)
	}
	if w := list("0xstranger"); w.Code != http.StatusOK {
		t.Errorf("stranger list = %d", w.Code)
	} else {
		var l struct {
			Jobs []ListSummary `json:"jobs"`
		}
		json.Unmarshal(w.Body.Bytes(), &l)
		if len(l.Jobs) != 0 {
			t.Errorf("stranger sees %d jobs, want 0", len(l.Jobs))
		}
	}
}

func TestBroker_WebhookAndTTL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var hookCalls atomic.Int32
	var hookBody atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		hookBody.Store(string(b[:n]))
		hookCalls.Add(1)
	}))
	defer hook.Close()

	srv := testServer(t)
	resp := submit(t, srv, upstream.URL, "0xbuyer", "payer", `{"callbackUrl":"`+hook.URL+`"}`)
	id := resp["jobId"].(string)
	waitState(t, srv, id)
	for i := 0; i < 100 && hookCalls.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if hookCalls.Load() != 1 || !strings.Contains(hookBody.Load().(string), id) {
		t.Errorf("webhook calls=%d body=%v", hookCalls.Load(), hookBody.Load())
	}

	// TTL: sweep past expiry deletes; status turns 410 via the time check
	// even before the sweeper runs.
	srv.now = func() time.Time { return time.Now().Add(100 * 24 * time.Hour) }
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Errorf("expired status = %d, want 410", w.Code)
	}
	if n, err := srv.store.SweepExpired(srv.now()); err != nil || n != 1 {
		t.Errorf("sweep = (%d, %v), want 1 deleted", n, err)
	}
}

// TestBroker_SubmitWithoutContractHeaders guards the trust model: requests
// that didn't come through the payment gate are refused.
func TestBroker_SubmitWithoutContractHeaders(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ungated submit = %d, want 400", w.Code)
	}
}

// TestBroker_HMAC_RejectsForgedSubmit pins F1 defense-in-depth: with a shared
// secret set, a submit whose X-Obol-Broker-Sig doesn't match the contract
// headers is refused (403) — so a NetworkPolicy-allowed pod still can't forge
// an arbitrary-URL, attacker-credentialed job. A correctly-signed submit passes.
func TestBroker_HMAC_RejectsForgedSubmit(t *testing.T) {
	t.Setenv("JOB_BROKER_HMAC_SECRET", "test-shared-secret")
	dir := t.TempDir()
	store, err := OpenStore(dir + "/jobs.db")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	srv := NewServer(store)

	upstream := "http://upstream.internal/work"
	offer := "sec/audit"
	newReq := func(sig string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{}`))
		req.Header.Set(HeaderUpstreamURL, upstream)
		req.Header.Set(HeaderOffer, offer)
		if sig != "" {
			req.Header.Set(HeaderBrokerSig, sig)
		}
		return req
	}

	// Forged / missing signature → 403.
	w := httptest.NewRecorder()
	srv.handleSubmit(w, newReq("deadbeef"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("forged-sig submit = %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	srv.handleSubmit(w, newReq(""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing-sig submit = %d, want 403", w.Code)
	}

	// Correct signature (matching the verifier's computation) → accepted (202).
	good := brokerSignature("test-shared-secret", upstream, offer, "")
	w = httptest.NewRecorder()
	srv.handleSubmit(w, newReq(good))
	if w.Code != http.StatusAccepted {
		t.Fatalf("correctly-signed submit = %d (%s), want 202", w.Code, w.Body.String())
	}
}
