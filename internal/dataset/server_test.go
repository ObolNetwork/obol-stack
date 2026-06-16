package dataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

const ownerToken = "owner-secret-token"

// passGate is a stub paid-join gate that treats every request as already paid.
// The real x402 verify/settle path is covered by the inference-gateway tests;
// here we exercise the server's post-payment behaviour (version from the URL,
// never from a client header) and that the route is gated at all.
func passGate(next http.Handler) http.Handler { return next }

// gateOnHeader simulates the x402 gate: it passes only when X-Test-Paid is set,
// otherwise returns 402 — so a test can prove the route fails closed.
func gateOnHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Paid") != "yes" {
			writeErr(w, http.StatusPaymentRequired, "payment_required", "pay first")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// memArtifacts serves version bytes from memory (seekable for Range).
type memArtifacts struct{ data map[int][]byte }

func (m memArtifacts) Open(version int) (io.ReadSeeker, time.Time, func() error, error) {
	b, ok := m.data[version]
	if !ok {
		return nil, time.Time{}, nil, fmt.Errorf("no artifact v%d", version)
	}
	return bytes.NewReader(b), fixedTime, func() error { return nil }, nil
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type testServer struct {
	srv     *Server
	bytesV1 []byte
	signer  *EthSigner
	store   *Store
}

func newTestServer(t *testing.T, membership string, gate func(http.Handler) http.Handler) testServer {
	t.Helper()
	signer := newTestSigner(t)
	artifact := []byte(`{"messages":[{"role":"user","content":"hi"}]}` + "\n")
	fileHash := sha256hex(artifact)

	log := NewLog()
	if _, err := log.Append(hashA, fileHash, int64(len(artifact)), signer, fixedTime); err != nil {
		t.Fatalf("append v1: %v", err)
	}
	store := NewStore(filepath.Join(t.TempDir(), "ds.json"))
	srv := NewServer(Config{
		ID:              "ds",
		Membership:      membership,
		OwnerToken:      ownerToken,
		OwnerSigner:     signer.SignerID(),
		Log:             log,
		Ents:            NewEntitlements(),
		Store:           store,
		Artifacts:       memArtifacts{data: map[int][]byte{1: artifact}},
		PaidJoin:        gate,
		JoinPriceAtomic: "1000",
	})
	return testServer{srv: srv, bytesV1: artifact, signer: signer, store: store}
}

func do(t *testing.T, h http.Handler, method, target, token string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServer_PaidJoinThenDownload(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, passGate)
	h := ts.srv.Handler()

	// Pay -> mint a version-1 token.
	w := do(t, h, "POST", "/dataset/ds/join/paid", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("join/paid = %d, body %s", w.Code, w.Body.String())
	}
	var join struct {
		Token   string `json:"token"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &join); err != nil {
		t.Fatalf("join body: %v", err)
	}
	if join.Token == "" || join.Version != 1 {
		t.Fatalf("join = %+v", join)
	}

	// Download v1 with the minted token; verify whole-file hash matches.
	dw := do(t, h, "GET", "/dataset/ds/download?version=1", join.Token, nil)
	if dw.Code != http.StatusOK {
		t.Fatalf("download = %d, body %s", dw.Code, dw.Body.String())
	}
	if !bytes.Equal(dw.Body.Bytes(), ts.bytesV1) {
		t.Error("downloaded bytes != artifact")
	}
	if got := dw.Header().Get("X-Dataset-File-Hash"); got != sha256hex(ts.bytesV1) {
		t.Errorf("X-Dataset-File-Hash = %q, want %q", got, sha256hex(ts.bytesV1))
	}
	if sha256hex(dw.Body.Bytes()) != dw.Header().Get("X-Dataset-File-Hash") {
		t.Error("recomputed body hash != advertised file hash")
	}
	if dw.Header().Get("Accept-Ranges") != "bytes" {
		t.Error("download did not advertise Range support")
	}
}

func TestServer_VersionScopeEnforced(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, passGate)
	h := ts.srv.Handler()

	// Append a v2 to the log so ?version=2 is a real (but unpaid) version.
	if _, err := ts.srv.log.Append(hashB, hashB, 5, ts.signer, fixedTime); err != nil {
		t.Fatalf("append v2: %v", err)
	}

	w := do(t, h, "POST", "/dataset/ds/join/paid?version=1", "", nil) // pays for v1 (version from URL)
	var join struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &join)

	// v1 token may fetch v1...
	if dw := do(t, h, "GET", "/dataset/ds/download?version=1", join.Token, nil); dw.Code != http.StatusOK {
		t.Errorf("v1 token download v1 = %d, want 200", dw.Code)
	}
	// ...but is forbidden from v2.
	dw := do(t, h, "GET", "/dataset/ds/download?version=2", join.Token, nil)
	if dw.Code != http.StatusForbidden {
		t.Fatalf("v1 token download v2 = %d, want 403", dw.Code)
	}
	if got := errorKind(t, dw.Body.Bytes()); got != "version_not_entitled" {
		t.Errorf("error = %q, want version_not_entitled", got)
	}
}

func TestServer_RangeReturns206WithWholeFileHash(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, passGate)
	h := ts.srv.Handler()
	token := ownerToken // owner is a download superuser

	dw := do(t, h, "GET", "/dataset/ds/download?version=1", token, map[string]string{"Range": "bytes=0-3"})
	if dw.Code != http.StatusPartialContent {
		t.Fatalf("range request = %d, want 206", dw.Code)
	}
	if !bytes.Equal(dw.Body.Bytes(), ts.bytesV1[:4]) {
		t.Errorf("partial body = %q, want first 4 bytes %q", dw.Body.Bytes(), ts.bytesV1[:4])
	}
	// Whole-file hash header must be present on 206 (commits to the full file).
	if got := dw.Header().Get("X-Dataset-File-Hash"); got != sha256hex(ts.bytesV1) {
		t.Errorf("206 X-Dataset-File-Hash = %q, want whole-file %q", got, sha256hex(ts.bytesV1))
	}
}

func TestServer_GatesRejectNonMembersAndAnonymous(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, passGate)
	h := ts.srv.Handler()

	if w := do(t, h, "GET", "/dataset/ds/download?version=1", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous download = %d, want 401", w.Code)
	}
	if w := do(t, h, "GET", "/dataset/ds/download?version=1", "obol-research-mt-bogus", nil); w.Code != http.StatusForbidden {
		t.Errorf("non-member download = %d, want 403", w.Code)
	}
	if w := do(t, h, "GET", "/dataset/ds/status", "not-the-owner", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("non-owner status = %d, want 401", w.Code)
	}
	if w := do(t, h, "GET", "/dataset/ds/status", ownerToken, nil); w.Code != http.StatusOK {
		t.Errorf("owner status = %d, want 200", w.Code)
	}
}

func TestServer_DeviceAuthAdmitGetsHeadAccess(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, passGate) // open: auto-approved on code request
	h := ts.srv.Handler()

	// device code (auto-approved) -> token
	cw := do(t, h, "POST", "/auth/device/code", "", nil)
	var grant struct {
		DeviceCode string `json:"device_code"`
	}
	_ = json.Unmarshal(cw.Body.Bytes(), &grant)

	r := httptest.NewRequest("POST", "/auth/device/token", bytes.NewReader([]byte(`{"device_code":"`+grant.DeviceCode+`"}`)))
	tw := httptest.NewRecorder()
	h.ServeHTTP(tw, r)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tw.Body.Bytes(), &tok)
	if tok.Token == "" {
		t.Fatalf("no token issued: %s", tw.Body.String())
	}

	if dw := do(t, h, "GET", "/dataset/ds/download?version=1", tok.Token, nil); dw.Code != http.StatusOK {
		t.Errorf("admitted worker download = %d, want 200", dw.Code)
	}
}

func TestServer_VerifyReportsChainHealth(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, passGate)
	h := ts.srv.Handler()

	w := do(t, h, "GET", "/dataset/ds/verify", ownerToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("verify = %d", w.Code)
	}
	var res struct {
		Valid bool `json:"valid"`
		Head  int  `json:"head"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if !res.Valid || res.Head != 1 {
		t.Errorf("verify = %+v, want valid head=1", res)
	}
}

func TestServer_RehydratesPaidMemberAfterRestart(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, passGate)
	h := ts.srv.Handler()

	jw := do(t, h, "POST", "/dataset/ds/join/paid", "", nil)
	var join struct{ Token string }
	_ = json.Unmarshal(jw.Body.Bytes(), &join)

	// Simulate a restart: load persisted state into a brand-new server.
	st, err := ts.store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(st.Entitlements) != 1 {
		t.Fatalf("persisted entitlements = %d, want 1", len(st.Entitlements))
	}
	restarted := NewServer(Config{
		ID: "ds", Membership: MembershipInvite, OwnerToken: ownerToken,
		OwnerSigner:     ts.signer.SignerID(),
		Log:             LogFromVersions(st.Versions),
		Ents:            loadEnts(st.Entitlements),
		Store:           ts.store,
		Artifacts:       memArtifacts{data: map[int][]byte{1: ts.bytesV1}},
		PaidJoin:        passGate,
		JoinPriceAtomic: "1000",
	})

	// The pre-restart token still works — the member did not have to re-pay.
	if dw := do(t, restarted.Handler(), "GET", "/dataset/ds/download?version=1", join.Token, nil); dw.Code != http.StatusOK {
		t.Errorf("post-restart download = %d, want 200 (rehydration failed)", dw.Code)
	}
}

// TestServer_PaidJoinGatedAndVersionFromURL is the C1 regression: the paid-join
// route fails closed without the x402 gate's approval, and the minted version
// is taken from the URL — a spoofed X-Dataset-Version header is ignored.
func TestServer_PaidJoinGatedAndVersionFromURL(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, gateOnHeader)
	h := ts.srv.Handler()
	// Append a v2 so head=2; we still pay for v1 via the URL.
	if _, err := ts.srv.log.Append(hashB, hashB, 5, ts.signer, fixedTime); err != nil {
		t.Fatalf("append v2: %v", err)
	}

	// No payment -> the gate fails closed (NOT a free token).
	if w := do(t, h, "POST", "/dataset/ds/join/paid?version=1", "", nil); w.Code != http.StatusPaymentRequired {
		t.Fatalf("unpaid join = %d, want 402", w.Code)
	}

	// Paid: version is taken from the URL (=1); a spoofed X-Dataset-Version:99
	// header must be ignored (this was the header-trust bug).
	w := do(t, h, "POST", "/dataset/ds/join/paid?version=1", "",
		map[string]string{"X-Test-Paid": "yes", "X-Dataset-Version": "99"})
	if w.Code != http.StatusOK {
		t.Fatalf("paid join = %d, body %s", w.Code, w.Body.String())
	}
	var join struct {
		Token   string `json:"token"`
		Version int    `json:"version"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &join)
	if join.Version != 1 {
		t.Fatalf("minted version = %d, want 1 (from URL, not the spoofed header 99)", join.Version)
	}
	// The v1-scoped token must still not reach v2.
	if dw := do(t, h, "GET", "/dataset/ds/download?version=2", join.Token, nil); dw.Code != http.StatusForbidden {
		t.Errorf("v1-paid token download v2 = %d, want 403", dw.Code)
	}
}

func loadEnts(ents []Entitlement) *Entitlements {
	e := NewEntitlements()
	e.Load(ents)
	return e
}

func errorKind(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Error
}
