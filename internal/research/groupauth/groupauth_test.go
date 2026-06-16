package groupauth

import (
	"strings"
	"testing"
	"time"
)

func TestDeviceFlow_CodeApproveTokenVerify(t *testing.T) {
	a := New()

	grant, err := a.RequestCode("0xworker")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if grant.DeviceCode == "" || grant.UserCode == "" {
		t.Fatal("empty grant")
	}
	if grant.Interval != PollInterval || grant.ExpiresIn <= 0 {
		t.Errorf("grant metadata = %+v", grant)
	}

	// Before approval: pending, no token.
	if r, err := a.Poll(grant.DeviceCode); err != nil || r.Status != "authorization_pending" || r.Token != "" {
		t.Fatalf("pre-approval poll = %+v err=%v", r, err)
	}

	// Owner approves the user_code (case/space-insensitive).
	if err := a.Approve("nanogpt-valbpb", " "+strings.ToLower(grant.UserCode)+" "); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// First post-approval poll mints the token.
	res, err := a.Poll(grant.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after approve: %v", err)
	}
	if res.Status != "authorized" || res.GroupID != "nanogpt-valbpb" {
		t.Fatalf("poll result = %+v", res)
	}
	if !strings.HasPrefix(res.Token, tokenPrefix) {
		t.Errorf("token = %q, want %s prefix", res.Token, tokenPrefix)
	}

	// The token verifies and names the group.
	if gid, ok := a.VerifyToken(res.Token); !ok || gid != "nanogpt-valbpb" {
		t.Errorf("VerifyToken = %q,%v", gid, ok)
	}

	// Token is single-issue: the code is consumed.
	if _, err := a.Poll(grant.DeviceCode); err != ErrNotFound {
		t.Errorf("second poll err = %v, want ErrNotFound", err)
	}

	// Revocation removes access.
	a.Revoke(res.Token)
	if _, ok := a.VerifyToken(res.Token); ok {
		t.Error("revoked token still verifies")
	}
}

func TestApprove_Errors(t *testing.T) {
	a := New()
	if err := a.Approve("g", "NOPE-NOPE"); err != ErrNotFound {
		t.Errorf("unknown code err = %v, want ErrNotFound", err)
	}

	grant, _ := a.RequestCode("")
	if err := a.Approve("g", grant.UserCode); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if err := a.Approve("g", grant.UserCode); err != ErrAlreadyUsed {
		t.Errorf("double approve err = %v, want ErrAlreadyUsed", err)
	}
}

func TestExpiry(t *testing.T) {
	a := New()
	base := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return base }

	grant, _ := a.RequestCode("w")
	a.now = func() time.Time { return base.Add(CodeExpiry + time.Second) }

	if err := a.Approve("g", grant.UserCode); err != ErrExpired {
		t.Errorf("approve expired err = %v, want ErrExpired", err)
	}
	if _, err := a.Poll(grant.DeviceCode); err != ErrExpired {
		t.Errorf("poll expired err = %v, want ErrExpired", err)
	}
}

func TestVerify_UnknownToken(t *testing.T) {
	a := New()
	if _, ok := a.VerifyToken("obol-research-mt-deadbeef"); ok {
		t.Error("unknown token must not verify")
	}
}

func TestRequestCode_SweepsExpired(t *testing.T) {
	a := New()
	clk := time.Now()
	a.now = func() time.Time { return clk }

	g, err := a.RequestCode("w1")
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	// Let the first code expire, then request another — the sweep evicts it.
	clk = clk.Add(CodeExpiry + time.Minute)
	if _, err := a.RequestCode("w2"); err != nil {
		t.Fatalf("RequestCode 2: %v", err)
	}

	// The expired code is gone from the indexes (not merely flagged expired).
	if _, err := a.Poll(g.DeviceCode); err != ErrNotFound {
		t.Fatalf("expired code poll = %v, want ErrNotFound (swept)", err)
	}
	a.mu.Lock()
	n := len(a.byDevice) + len(a.byUser)
	a.mu.Unlock()
	if n != 2 { // only the live w2 code remains, in both indexes
		t.Fatalf("index entries = %d, want 2 (expired code swept)", n)
	}
}

func TestWorkerID(t *testing.T) {
	id := WorkerID("tok-abc")
	if id == "" || id != WorkerID("tok-abc") {
		t.Fatalf("WorkerID not stable for the same token: %q", id)
	}
	if WorkerID("tok-abc") == WorkerID("tok-xyz") {
		t.Fatal("distinct tokens must produce distinct WorkerIDs (no impersonation)")
	}
	if id == "tok-abc" || strings.Contains(id, "tok-abc") {
		t.Fatalf("WorkerID %q leaked the raw token", id)
	}
	if WorkerID("") != "" {
		t.Fatal("empty token must yield empty WorkerID")
	}
	if !strings.HasPrefix(id, "w-") {
		t.Fatalf("WorkerID = %q, want a w- prefix", id)
	}
}
