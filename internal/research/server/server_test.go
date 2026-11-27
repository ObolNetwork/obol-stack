package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/research/kb"
)

func testProgram() kb.Program {
	base := 1.20
	return kb.Program{
		ID:       "nanogpt-valbpb",
		Criteria: kb.Criteria{Metric: "val_bpb", Direction: kb.Minimize, Accept: kb.BeatsChampion},
		Baseline: &base,
		Pool:     100, Token: "OBOL", Network: "base-sepolia", Split: kb.ByImpact,
	}
}

func do(t *testing.T, h http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestServer_InviteFlow_EndToEnd(t *testing.T) {
	s := New(testProgram(), MembershipInvite, "owner-secret", nil)
	h := s.Handler()

	// KB is gated before membership.
	if w, _ := do(t, h, "GET", "/task", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("ungated /task = %d, want 401", w.Code)
	}

	// Worker requests a device code.
	_, code := do(t, h, "POST", "/auth/device/code", "", map[string]string{"worker": "spark1"})
	deviceCode, _ := code["device_code"].(string)
	userCode, _ := code["user_code"].(string)
	if deviceCode == "" || userCode == "" {
		t.Fatalf("device code grant = %+v", code)
	}

	// Pre-approval poll is pending.
	if _, tok := do(t, h, "POST", "/auth/device/token", "", map[string]string{"device_code": deviceCode}); tok["status"] != "authorization_pending" {
		t.Fatalf("pre-approval poll = %+v", tok)
	}

	// Approve requires the owner token.
	if w, _ := do(t, h, "POST", "/auth/device/approve", "wrong", map[string]string{"user_code": userCode}); w.Code != http.StatusUnauthorized {
		t.Fatalf("approve w/o owner token = %d, want 401", w.Code)
	}
	if w, _ := do(t, h, "POST", "/auth/device/approve", "owner-secret", map[string]string{"user_code": userCode}); w.Code != http.StatusOK {
		t.Fatalf("owner approve = %d, want 200", w.Code)
	}

	// Worker polls and gets a member token.
	_, tok := do(t, h, "POST", "/auth/device/token", "", map[string]string{"device_code": deviceCode})
	if tok["status"] != "authorized" {
		t.Fatalf("post-approval poll = %+v", tok)
	}
	member, _ := tok["token"].(string)
	if !strings.HasPrefix(member, "obol-research-mt-") {
		t.Fatalf("member token = %q", member)
	}

	// Member can read the task.
	if w, task := do(t, h, "GET", "/task", member, nil); w.Code != http.StatusOK || task["program"] == nil {
		t.Fatalf("member /task = %d %+v", w.Code, task)
	}

	// A bogus token is forbidden.
	if w, _ := do(t, h, "GET", "/task", "obol-research-mt-bogus", nil); w.Code != http.StatusForbidden {
		t.Fatalf("bogus token /task = %d, want 403", w.Code)
	}

	// Member submits a result that beats the baseline → accepted champion.
	w, res := do(t, h, "POST", "/results", member, map[string]any{"worker": "spark1", "value": 1.10})
	if w.Code != http.StatusOK || res["accepted"] != true || res["champion"] != true {
		t.Fatalf("submit = %d %+v", w.Code, res)
	}

	// Status reflects roster + champion + payout.
	_, st := do(t, h, "GET", "/status", member, nil)
	if st["champion"] == nil {
		t.Fatalf("status champion missing: %+v", st)
	}
}

func TestServer_OpenMembershipAutoApproves(t *testing.T) {
	s := New(testProgram(), MembershipOpen, "owner", nil)
	h := s.Handler()

	_, code := do(t, h, "POST", "/auth/device/code", "", map[string]string{"worker": "w"})
	deviceCode := code["device_code"].(string)

	// No owner approve step — open membership auto-approved; first poll mints.
	_, tok := do(t, h, "POST", "/auth/device/token", "", map[string]string{"device_code": deviceCode})
	if tok["status"] != "authorized" || tok["token"] == "" {
		t.Fatalf("open-membership poll = %+v", tok)
	}
}
