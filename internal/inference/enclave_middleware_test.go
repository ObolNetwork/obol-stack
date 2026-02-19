//go:build darwin && cgo

package inference

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

func testEnclaveTag(t *testing.T) string {
	t.Helper()
	tag := "com.obol.inference.test." + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "."))
	t.Cleanup(func() { _ = enclave.DeleteKey(tag) })
	_ = enclave.DeleteKey(tag)
	return tag
}

func testReplyTag(t *testing.T) string {
	t.Helper()
	tag := testEnclaveTag(t) + ".reply"
	t.Cleanup(func() { _ = enclave.DeleteKey(tag) })
	_ = enclave.DeleteKey(tag)
	return tag
}

func TestEnclavePubkeyEndpoint(t *testing.T) {
	em, err := newEnclaveMiddleware(testEnclaveTag(t))
	if err != nil {
		t.Fatalf("newEnclaveMiddleware: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/enclave/pubkey", nil)
	rr := httptest.NewRecorder()
	em.handlePubkey(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}

	var got pubkeyJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Tag != em.key.Tag() {
		t.Fatalf("tag mismatch: want %q, got %q", em.key.Tag(), got.Tag)
	}
	if got.Algorithm != "ECIES-P256-HKDF-SHA256-AES256GCM" {
		t.Fatalf("algorithm mismatch: got %q", got.Algorithm)
	}
	if _, err := hex.DecodeString(got.Pubkey); err != nil {
		t.Fatalf("pubkey is not valid hex: %v", err)
	}
	if got.Pubkey != hex.EncodeToString(em.key.PublicKeyBytes()) {
		t.Fatalf("pubkey mismatch")
	}
}

func TestEnclaveWrapDecryptsEncryptedRequest(t *testing.T) {
	em, err := newEnclaveMiddleware(testEnclaveTag(t))
	if err != nil {
		t.Fatalf("newEnclaveMiddleware: %v", err)
	}

	plaintextReq := `{"model":"llama3","messages":[{"role":"user","content":"hello"}]}`
	encReq, err := enclave.Encrypt(em.key.PublicKeyBytes(), []byte(plaintextReq))
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != plaintextReq {
			t.Fatalf("plaintext mismatch: want %s got %s", plaintextReq, got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected content-type: %q", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encReq))
	req.Header.Set("Content-Type", contentTypeEncrypted)
	rr := httptest.NewRecorder()

	em.wrap(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != plaintextReq {
		t.Fatalf("response mismatch: want %s got %s", plaintextReq, got)
	}
}

func TestEnclaveWrapPassesPlaintextThrough(t *testing.T) {
	em, err := newEnclaveMiddleware(testEnclaveTag(t))
	if err != nil {
		t.Fatalf("newEnclaveMiddleware: %v", err)
	}

	want := `{"model":"llama3","messages":[]}`
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if got := string(body); got != want {
			t.Fatalf("body mismatch: want %s got %s", want, got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected content-type: %q", ct)
		}
		w.WriteHeader(http.StatusAccepted)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(want))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	em.wrap(next).ServeHTTP(rr, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d", rr.Code)
	}
}

func TestEnclaveWrapEncryptsReplyAndRefreshesHeaders(t *testing.T) {
	em, err := newEnclaveMiddleware(testEnclaveTag(t))
	if err != nil {
		t.Fatalf("newEnclaveMiddleware: %v", err)
	}
	replyKey, err := enclave.NewKey(testReplyTag(t))
	if err != nil {
		t.Fatalf("reply NewKey: %v", err)
	}

	plaintextReq := `{"model":"llama3","messages":[{"role":"user","content":"secret"}]}`
	plaintextResp := `{"choices":[{"message":{"content":"42"}}]}`
	encReq, err := enclave.Encrypt(em.key.PublicKeyBytes(), []byte(plaintextReq))
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(headerReplyPubkey); v != "" {
			t.Fatalf("reply pubkey header should be stripped before upstream, got %q", v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(plaintextResp)))
		w.Header().Set("Content-Encoding", "identity")
		w.Header().Set("ETag", `"upstream-etag"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(plaintextResp))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encReq))
	req.Header.Set("Content-Type", contentTypeEncrypted)
	req.Header.Set(headerReplyPubkey, hex.EncodeToString(replyKey.PublicKeyBytes()))
	rr := httptest.NewRecorder()

	em.wrap(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != contentTypeEncrypted {
		t.Fatalf("content-type: want %q, got %q", contentTypeEncrypted, got)
	}
	if rr.Header().Get("Content-Encoding") != "" {
		t.Fatalf("content-encoding should be cleared, got %q", rr.Header().Get("Content-Encoding"))
	}
	if rr.Header().Get("ETag") != "" {
		t.Fatalf("etag should be cleared, got %q", rr.Header().Get("ETag"))
	}
	wantLen := strconv.Itoa(rr.Body.Len())
	if got := rr.Header().Get("Content-Length"); got != wantLen {
		t.Fatalf("content-length: want %q, got %q", wantLen, got)
	}

	decrypted, err := replyKey.Decrypt(rr.Body.Bytes())
	if err != nil {
		t.Fatalf("decrypt response: %v", err)
	}
	if got := string(decrypted); got != plaintextResp {
		t.Fatalf("decrypted response mismatch: want %s got %s", plaintextResp, got)
	}
}

func TestEnclaveWrapRejectsInvalidReplyPubkey(t *testing.T) {
	em, err := newEnclaveMiddleware(testEnclaveTag(t))
	if err != nil {
		t.Fatalf("newEnclaveMiddleware: %v", err)
	}

	encReq, err := enclave.Encrypt(em.key.PublicKeyBytes(), []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encReq))
	req.Header.Set("Content-Type", contentTypeEncrypted)
	req.Header.Set(headerReplyPubkey, "not-hex")
	rr := httptest.NewRecorder()

	em.wrap(next).ServeHTTP(rr, req)

	if called {
		t.Fatal("next handler should not run when reply pubkey is invalid")
	}
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rr.Code)
	}
}
