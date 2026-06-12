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
