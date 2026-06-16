package dataset

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetch_DownloadsAndVerifies(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, passGate)
	httpSrv := httptest.NewServer(ts.srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds-v1.jsonl")
	res, err := Fetch(context.Background(), FetchOptions{
		BaseURL:       httpSrv.URL,
		ID:            "ds",
		Version:       1,
		Token:         ownerToken, // owner is a download superuser
		OutPath:       out,
		ExpectedOwner: ts.signer.SignerID(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Version != 1 || res.Resumed {
		t.Errorf("result = %+v, want version 1, not resumed", res)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if !bytes.Equal(got, ts.bytesV1) {
		t.Error("downloaded file != artifact")
	}
	if res.FileHash != sha256hex(ts.bytesV1) {
		t.Errorf("result hash = %q, want %q", res.FileHash, sha256hex(ts.bytesV1))
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Error(".part file should be removed after a successful finalize")
	}
}

func TestFetch_ResumesFromPartial(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, passGate)
	httpSrv := httptest.NewServer(ts.srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds.jsonl")
	// Simulate an interrupted earlier run: the first 10 bytes already on disk.
	if err := os.WriteFile(out+".part", ts.bytesV1[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Fetch(context.Background(), FetchOptions{
		BaseURL: httpSrv.URL, ID: "ds", Version: 1, Token: ownerToken, OutPath: out,
		ExpectedOwner: ts.signer.SignerID(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Resumed {
		t.Error("expected Resumed=true from a pre-existing .part")
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, ts.bytesV1) {
		t.Errorf("resumed file = %q, want full artifact", got)
	}
}

func TestFetch_RejectsTamperedBytesAgainstSignedLog(t *testing.T) {
	// The signed version log commits the REAL hash, but the server serves
	// different bytes (and would happily advertise the real hash in a header).
	// Integrity is anchored in the signed log, so the swap is caught.
	signer := newTestSigner(t)
	real := []byte("the-real-bytes\n")
	log := NewLog()
	if _, err := log.Append(hashA, sha256hex(real), int64(len(real)), signer, fixedTime); err != nil {
		t.Fatalf("append: %v", err)
	}
	srv := NewServer(Config{
		ID: "ds", Membership: MembershipOpen, OwnerToken: ownerToken,
		OwnerSigner: signer.SignerID(),
		Log:         log,
		Ents:        NewEntitlements(),
		Store:       NewStore(filepath.Join(t.TempDir(), "ds.json")),
		Artifacts:   memArtifacts{data: map[int][]byte{1: []byte("TAMPERED-DIFFERENT-BYTES\n")}},
		PaidJoin:    passGate,
	})
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds.jsonl")
	_, err := Fetch(context.Background(), FetchOptions{
		BaseURL: httpSrv.URL, ID: "ds", Version: 1, Token: ownerToken, OutPath: out,
		ExpectedOwner: signer.SignerID(),
	})
	if err == nil {
		t.Fatal("Fetch accepted bytes that don't match the signed version log")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Error("a failed verification must not leave a finalized output file")
	}
}

func TestFetch_RejectsWrongExpectedOwner(t *testing.T) {
	// A seller that swapped in a different signing key is caught by pinning the
	// expected owner: every entry's recovered signer fails the identity check.
	ts := newTestServer(t, MembershipOpen, passGate)
	httpSrv := httptest.NewServer(ts.srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds.jsonl")
	_, err := Fetch(context.Background(), FetchOptions{
		BaseURL: httpSrv.URL, ID: "ds", Version: 1, Token: ownerToken, OutPath: out,
		ExpectedOwner: "0x000000000000000000000000000000000000dead",
	})
	if err == nil {
		t.Fatal("Fetch accepted a version log signed by an unexpected owner")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Error("a failed owner check must not leave a finalized output file")
	}
}

func TestVerifyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(path, []byte("abc"), 0o644)
	if err := VerifyFile(path, sha256hex([]byte("abc"))); err != nil {
		t.Errorf("VerifyFile good: %v", err)
	}
	if err := VerifyFile(path, sha256hex([]byte("xyz"))); err == nil {
		t.Error("VerifyFile should reject a wrong hash")
	}
}
