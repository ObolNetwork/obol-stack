package walletbackup

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodeDecodePlain(t *testing.T) {
	in := &File{
		Version:  Version,
		Instance: "demo",
		Wallets: []Wallet{{
			Address:          "0xabc",
			PublicKey:        "0xpub",
			KeystoreUUID:     "uuid-1",
			CreatedAt:        "2026-01-01T00:00:00Z",
			Keystore:         json.RawMessage(`{"version":3}`),
			KeystorePassword: "hunter2",
		}},
	}

	payload, encrypted, err := Encode(in, "")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encrypted {
		t.Fatalf("expected plain payload")
	}
	if IsEncrypted(payload) {
		t.Fatalf("plain payload reported as encrypted")
	}

	out, err := Decode(payload, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Instance != in.Instance || out.Wallets[0].KeystorePassword != in.Wallets[0].KeystorePassword {
		t.Fatalf("round-trip mismatch: got %+v", out)
	}
}

func TestEncodeDecodeEncrypted(t *testing.T) {
	in := &File{
		Version: Version,
		Wallets: []Wallet{{Address: "0x1", KeystorePassword: "p"}},
	}
	payload, encrypted, err := Encode(in, "correct horse")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !encrypted {
		t.Fatalf("expected encrypted payload")
	}
	if !IsEncrypted(payload) {
		t.Fatalf("encrypted payload missing magic prefix")
	}

	if _, err := Decode(payload, "wrong"); err == nil {
		t.Fatalf("Decode with wrong passphrase should fail")
	}

	out, err := Decode(payload, "correct horse")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Wallets[0].Address != "0x1" {
		t.Fatalf("round-trip mismatch")
	}
}

func TestEncryptDecryptRawBytes(t *testing.T) {
	plain := []byte("hello world")
	cipher, err := Encrypt(plain, "passphrase")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !IsEncrypted(cipher) {
		t.Fatalf("ciphertext missing magic prefix")
	}
	out, err := Decrypt(cipher, "passphrase")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(plain, out) {
		t.Fatalf("got %q want %q", out, plain)
	}
}

func TestDecodeRejectsUnknownVersion(t *testing.T) {
	in := &File{Version: 99, Wallets: []Wallet{{}}}
	payload, _, err := Encode(in, "")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode(payload, ""); err == nil {
		t.Fatalf("Decode should reject unknown version")
	}
}
