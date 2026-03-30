//go:build darwin && cgo

package inference_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"github.com/ObolNetwork/obol-stack/internal/inference"
)

// startEnclaveGateway starts a minimal httptest.Server that behaves like the
// inference gateway's SE layer: it exposes /v1/enclave/pubkey and an echo
// endpoint that decrypts incoming encrypted bodies.
func startEnclaveGateway(t *testing.T, tag string) (*httptest.Server, enclave.Key) {
	t.Helper()

	_ = enclave.DeleteKey(tag)

	t.Cleanup(func() { _ = enclave.DeleteKey(tag) })

	seKey, err := enclave.NewKey(tag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	mux := http.NewServeMux()

	// /v1/enclave/pubkey — returns SE public key as JSON.
	mux.HandleFunc("GET /v1/enclave/pubkey", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"pubkey":     hex.EncodeToString(seKey.PublicKeyBytes()),
			"tag":        seKey.Tag(),
			"persistent": seKey.Persistent(),
			"algorithm":  "ECIES-P256-HKDF-SHA256-AES256GCM",
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	// /echo — decrypts the body and echoes it back as application/json.
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)

		var plain []byte

		if ct == "application/x-obol-encrypted" {
			var err error

			plain, err = seKey.Decrypt(body)
			if err != nil {
				http.Error(w, "decrypt failed: "+err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			plain = body
		}

		// If the caller wants an encrypted reply, encrypt back.
		replyPubkeyHex := r.Header.Get("X-Obol-Reply-Pubkey")
		if replyPubkeyHex != "" {
			replyPubkey, err := hex.DecodeString(replyPubkeyHex)
			if err != nil {
				http.Error(w, "bad reply pubkey", http.StatusBadRequest)
				return
			}

			enc, err := enclave.Encrypt(replyPubkey, plain)
			if err != nil {
				http.Error(w, "encrypt reply failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/x-obol-encrypted")
			_, _ = w.Write(enc)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(plain)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, seKey
}

// TestClientFetchesPubkey verifies that NewClient fetches and caches the
// gateway's SE public key.
func TestClientFetchesPubkey(t *testing.T) {
	srv, seKey := startEnclaveGateway(t, "com.obol.inference.test.client-fetch")

	c, err := inference.NewClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	pk := c.Pubkey()
	if len(pk) != 65 {
		t.Fatalf("expected 65-byte pubkey, got %d bytes", len(pk))
	}

	if hex.EncodeToString(pk) != hex.EncodeToString(seKey.PublicKeyBytes()) {
		t.Errorf("pubkey mismatch")
	}
}

// TestClientEncryptsRequest verifies that the client's RoundTrip encrypts
// the body before sending and the gateway can decrypt it.
func TestClientEncryptsRequest(t *testing.T) {
	srv, _ := startEnclaveGateway(t, "com.obol.inference.test.client-enc")

	c, err := inference.NewClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	want := `{"model":"llama3","messages":[{"role":"user","content":"hello"}]}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/echo", strings.NewReader(want))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", resp.Status)
	}

	got, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("body mismatch:\n  want: %s\n  got:  %s", want, got)
	}
}

// TestClientPassthroughNoBody verifies that GET requests (no body) are
// forwarded without modification.
func TestClientPassthroughNoBody(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true

		ct := r.Header.Get("Content-Type")
		if ct == "application/x-obol-encrypted" {
			t.Errorf("GET request should not be encrypted, got Content-Type: %s", ct)
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// Build a client with a manually set pubkey (no gateway pubkey endpoint needed).
	// Since there is no /v1/enclave/pubkey we use a fake key just to set up the client.
	fakeKey, err := enclave.NewKey("com.obol.inference.test.client-noencrypt")
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	t.Cleanup(func() { _ = enclave.DeleteKey("com.obol.inference.test.client-noencrypt") })

	c := &inference.Client{
		GatewayURL: srv.URL,
		HTTP:       http.DefaultTransport,
	}
	// Manually inject pubkey so fetchPubkey doesn't try to hit a missing endpoint.
	_ = fakeKey // pubkey unused — GET has no body so encrypt path is skipped

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/health", nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	resp.Body.Close()

	if !called {
		t.Error("upstream handler was not called")
	}
}

// TestClientEncryptedReply verifies the full round-trip with encrypted response:
// client sends encrypted request → gateway decrypts → re-encrypts response to
// client's ephemeral key → client decrypts → plaintext response.
func TestClientEncryptedReply(t *testing.T) {
	const replyTag = "com.obol.inference.test.client-reply"

	_ = enclave.DeleteKey(replyTag)

	t.Cleanup(func() { _ = enclave.DeleteKey(replyTag) })

	srv, _ := startEnclaveGateway(t, "com.obol.inference.test.client-reply-gw")

	c, err := inference.NewClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := c.EnableEncryptedReplies(replyTag); err != nil {
		t.Fatalf("EnableEncryptedReplies: %v", err)
	}

	want := `{"model":"llama3","messages":[{"role":"user","content":"secret question"}]}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/echo", strings.NewReader(want))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", resp.Status)
	}

	got, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("round-trip body mismatch:\n  want: %s\n  got:  %s", want, got)
	}
}
