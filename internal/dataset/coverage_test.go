package dataset

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.jsonl")
	want := []byte("line1\nline2\n")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	fa := NewFileArtifacts()
	fa.Set(1, path)

	rc, _, closeFn, err := fa.Open(1)
	if err != nil {
		t.Fatalf("Open(1): %v", err)
	}
	defer closeFn()
	got := make([]byte, len(want))
	if _, err := rc.Read(got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("read %q, want %q", got, want)
	}
	if _, _, _, err := fa.Open(2); err == nil {
		t.Error("Open(2) should error — no artifact registered")
	}
	if _, _, _, err := fa.Open(0); err == nil {
		t.Error("Open(0) should error")
	}
}

func TestServer_InviteFlow_ApproveThenToken(t *testing.T) {
	ts := newTestServer(t, MembershipInvite, nil)
	h := ts.srv.Handler()

	// Worker requests a code (NOT auto-approved in invite mode).
	cw := do(t, h, "POST", "/auth/device/code", "", nil)
	var grant struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	_ = json.Unmarshal(cw.Body.Bytes(), &grant)

	// Polling before approval yields authorization_pending (no token).
	pw := postJSON(t, h, "/auth/device/token", `{"device_code":"`+grant.DeviceCode+`"}`, "")
	if pw.Code != http.StatusOK {
		t.Fatalf("pre-approval poll = %d", pw.Code)
	}

	// Non-owner cannot approve.
	if w := postJSON(t, h, "/auth/device/approve", `{"user_code":"`+grant.UserCode+`"}`, "not-owner"); w.Code != http.StatusUnauthorized {
		t.Errorf("non-owner approve = %d, want 401", w.Code)
	}
	// Owner approves.
	if w := postJSON(t, h, "/auth/device/approve", `{"user_code":"`+grant.UserCode+`"}`, ownerToken); w.Code != http.StatusOK {
		t.Fatalf("owner approve = %d, body %s", w.Code, w.Body.String())
	}
	// Bad approve body.
	if w := postJSON(t, h, "/auth/device/approve", `{}`, ownerToken); w.Code != http.StatusBadRequest {
		t.Errorf("empty approve = %d, want 400", w.Code)
	}

	// Now polling mints a token.
	tw := postJSON(t, h, "/auth/device/token", `{"device_code":"`+grant.DeviceCode+`"}`, "")
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tw.Body.Bytes(), &tok)
	if tok.Token == "" {
		t.Fatal("no token after approval")
	}

	// That member token can read the member-gated versions list.
	vw := do(t, h, "GET", "/dataset/ds/versions", tok.Token, nil)
	if vw.Code != http.StatusOK {
		t.Errorf("member versions = %d, want 200", vw.Code)
	}
	// Bad device token request.
	if w := postJSON(t, h, "/auth/device/token", `{}`, ""); w.Code != http.StatusBadRequest {
		t.Errorf("empty device token = %d, want 400", w.Code)
	}
}

func TestServer_ErrorPaths(t *testing.T) {
	t.Run("join paid disabled when nil", func(t *testing.T) {
		ts := newTestServer(t, MembershipInvite, nil)
		w := do(t, ts.srv.Handler(), "POST", "/dataset/ds/join/paid", "", nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("= %d, want 503", w.Code)
		}
	})
	t.Run("join paid rejects payment error", func(t *testing.T) {
		ts := newTestServer(t, MembershipInvite, fakePayments{err: errFake})
		w := do(t, ts.srv.Handler(), "POST", "/dataset/ds/join/paid", "", nil)
		if w.Code != http.StatusPaymentRequired {
			t.Errorf("= %d, want 402", w.Code)
		}
	})
	t.Run("join paid unknown version", func(t *testing.T) {
		ts := newTestServer(t, MembershipInvite, fakePayments{version: 99})
		w := do(t, ts.srv.Handler(), "POST", "/dataset/ds/join/paid", "", nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("= %d, want 400", w.Code)
		}
	})
	t.Run("unknown dataset id 404", func(t *testing.T) {
		ts := newTestServer(t, MembershipOpen, nil)
		w := do(t, ts.srv.Handler(), "GET", "/dataset/other/versions", ownerToken, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("= %d, want 404", w.Code)
		}
	})
	t.Run("download bad version 404", func(t *testing.T) {
		ts := newTestServer(t, MembershipOpen, nil)
		h := ts.srv.Handler()
		for _, q := range []string{"abc", "0", "99"} {
			w := do(t, h, "GET", "/dataset/ds/download?version="+q, ownerToken, nil)
			if w.Code != http.StatusNotFound {
				t.Errorf("version=%q = %d, want 404", q, w.Code)
			}
		}
	})
	t.Run("download artifact missing", func(t *testing.T) {
		// Log has v1 but the artifact source has nothing.
		ts := newTestServer(t, MembershipOpen, nil)
		ts.srv.artifacts = memArtifacts{data: map[int][]byte{}}
		w := do(t, ts.srv.Handler(), "GET", "/dataset/ds/download?version=1", ownerToken, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("= %d, want 404", w.Code)
		}
	})
}

func TestRecoverSigner_BadInputs(t *testing.T) {
	v := EthVerifier{}
	var d [32]byte
	if _, err := v.RecoverSigner(d, "zz"); err == nil {
		t.Error("non-hex sig accepted")
	}
	if _, err := v.RecoverSigner(d, "abcd"); err == nil {
		t.Error("wrong-length sig accepted")
	}
}

func TestLog_LenAndStorePath(t *testing.T) {
	l := NewLog()
	if l.Len() != 0 {
		t.Errorf("empty Len = %d", l.Len())
	}
	s := NewStore("/tmp/x.json")
	if s.Path() != "/tmp/x.json" {
		t.Errorf("Path = %q", s.Path())
	}
}

// helpers

var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "fake payment failure" }

func postJSON(t *testing.T, h http.Handler, target, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", target, bytes.NewReader([]byte(body)))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
