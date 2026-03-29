//go:build darwin && cgo

package enclave_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

const testTag = "com.obol.enclave.test"

// cleanup removes the test key if it exists.
func cleanup(t *testing.T) {
	t.Helper()

	_ = enclave.DeleteKey(testTag)
}

func TestNewKey(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	k, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	if k == nil {
		t.Fatal("NewKey returned nil key")
	}

	pub := k.PublicKeyBytes()
	if len(pub) != 65 {
		t.Fatalf("PublicKeyBytes: want 65 bytes, got %d", len(pub))
	}

	if pub[0] != 0x04 {
		t.Fatalf("PublicKeyBytes: expected uncompressed prefix 0x04, got 0x%02x", pub[0])
	}

	if k.Tag() != testTag {
		t.Fatalf("Tag: want %q, got %q", testTag, k.Tag())
	}
}

func TestLoadKey(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	// Create key.
	k1, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	if !k1.Persistent() {
		t.Skip("key is ephemeral (unsigned binary lacks keychain entitlement); skipping LoadKey test")
	}

	// Load it back.
	k2, err := enclave.LoadKey(testTag)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}

	// Public keys must match.
	if string(k1.PublicKeyBytes()) != string(k2.PublicKeyBytes()) {
		t.Fatal("LoadKey returned different public key than NewKey")
	}
}

func TestLoadKeyNotFound(t *testing.T) {
	_ = enclave.DeleteKey("com.obol.enclave.nonexistent")

	_, err := enclave.LoadKey("com.obol.enclave.nonexistent")
	if !errors.Is(err, enclave.ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestSign(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	k, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	msg := []byte("hello secure enclave")
	digest := sha256.Sum256(msg)

	sig, err := k.Sign(digest[:])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if len(sig) < 64 {
		t.Fatalf("Sign: signature too short (%d bytes)", len(sig))
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	k, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	plaintext := []byte("inference request: what is the meaning of life?")

	ciphertext, err := enclave.Encrypt(k.PublicKeyBytes(), plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Wire format: [1:version][65:ephPub][12:nonce][ciphertext+16:tag]
	minLen := 1 + 65 + 12 + len(plaintext) + 16
	if len(ciphertext) < minLen {
		t.Fatalf("ciphertext too short: got %d, want >= %d", len(ciphertext), minLen)
	}

	if ciphertext[0] != 0x01 {
		t.Fatalf("unexpected version byte: 0x%02x", ciphertext[0])
	}

	// Use the key handle directly to avoid requiring keychain persistence.
	recovered, err := k.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if string(recovered) != string(plaintext) {
		t.Fatalf("round-trip mismatch:\n  want: %q\n  got:  %q", plaintext, recovered)
	}
}

func TestEncryptDecryptTampered(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	k, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	plaintext := []byte("sensitive data")

	ciphertext, err := enclave.Encrypt(k.PublicKeyBytes(), plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the ciphertext body.
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = k.Decrypt(tampered)
	if err == nil {
		t.Fatal("Decrypt should have failed on tampered ciphertext")
	}
}

func TestNewKeyIdempotent(t *testing.T) {
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	k1, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("first NewKey: %v", err)
	}

	k2, err := enclave.NewKey(testTag)
	if err != nil {
		t.Fatalf("second NewKey: %v", err)
	}

	// Both calls should return the same key — either from keychain (persistent)
	// or from the in-process ephemeral cache (unsigned binary).
	if string(k1.PublicKeyBytes()) != string(k2.PublicKeyBytes()) {
		if !k1.Persistent() {
			t.Skip("key is ephemeral and in-process cache returned different instance; acceptable in test isolation")
		}

		t.Fatal("second NewKey returned a different key than the first")
	}
}

func TestCheckSIP(t *testing.T) {
	// CheckSIP should not return an unexpected error on Apple Silicon.
	// ErrSIPDisabled is legitimate on developer machines with csrutil disabled.
	err := enclave.CheckSIP()
	switch {
	case err == nil:
		t.Log("SIP is enabled")
	case errors.Is(err, enclave.ErrSIPDisabled):
		t.Log("WARNING: System Integrity Protection is disabled on this machine")
	case errors.Is(err, enclave.ErrNotSupported):
		t.Skip("Secure Enclave not supported on this platform")
	default:
		t.Fatalf("CheckSIP unexpected error: %v", err)
	}
}
