package dataset

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetch_DownloadsAndVerifies(t *testing.T) {
	ts := newTestServer(t, MembershipOpen, nil)
	httpSrv := httptest.NewServer(ts.srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds-v1.jsonl")
	res, err := Fetch(context.Background(), FetchOptions{
		BaseURL: httpSrv.URL,
		ID:      "ds",
		Version: 1,
		Token:   ownerToken, // owner is a download superuser
		OutPath: out,
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
	ts := newTestServer(t, MembershipOpen, nil)
	httpSrv := httptest.NewServer(ts.srv.Handler())
	defer httpSrv.Close()

	out := filepath.Join(t.TempDir(), "ds.jsonl")
	// Simulate an interrupted earlier run: the first 10 bytes already on disk.
	if err := os.WriteFile(out+".part", ts.bytesV1[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Fetch(context.Background(), FetchOptions{
		BaseURL: httpSrv.URL, ID: "ds", Version: 1, Token: ownerToken, OutPath: out,
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

func TestFetch_RejectsHashMismatch(t *testing.T) {
	// A malicious/buggy server that serves the wrong bytes but advertises the
	// real hash must be caught by the whole-file verification.
	real := []byte("the-real-bytes\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Dataset-File-Hash", sha256hex(real))
		w.Header().Set("X-Dataset-Version", "1")
		_, _ = w.Write([]byte("TAMPERED-DIFFERENT-BYTES\n"))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "ds.jsonl")
	_, err := Fetch(context.Background(), FetchOptions{BaseURL: srv.URL, ID: "ds", Version: 1, Token: "t", OutPath: out})
	if err == nil {
		t.Fatal("Fetch accepted bytes that don't match the advertised hash")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Error("a failed verification must not leave a finalized output file")
	}
}

func TestFetch_RefusesUnverifiableDownload(t *testing.T) {
	// No X-Dataset-File-Hash -> refuse (don't write an unverifiable file).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("anything"))
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "ds.jsonl")
	if _, err := Fetch(context.Background(), FetchOptions{BaseURL: srv.URL, ID: "ds", Token: "t", OutPath: out}); err == nil {
		t.Error("Fetch accepted a download with no file-hash commitment")
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
