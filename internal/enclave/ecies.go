package enclave

// ecies.go — pure Go ECIES implementation used by both the darwin Secure
// Enclave backend and cross-platform callers (e.g. the inference client SDK).
//
// Scheme: ephemeral ECDH (P-256) + HKDF-SHA256 + AES-256-GCM.
//
// Wire format produced by encrypt():
//
//	[1:version=0x01][65:ephPub uncompressed SEC1][12:GCM nonce][ciphertext+16:GCM tag]

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// encrypt encrypts plaintext to recipientPubKey using ECIES.
// recipientPubKey must be a 65-byte uncompressed SEC1 P-256 public key (0x04 prefix).
func encrypt(recipientPubKey, plaintext []byte) ([]byte, error) {
	if len(recipientPubKey) != 65 || recipientPubKey[0] != 0x04 {
		return nil, errors.New("enclave: Encrypt: recipientPubKey must be 65-byte uncompressed SEC1")
	}

	// Parse recipient public key.
	curve := ecdh.P256()

	recipKey, err := curve.NewPublicKey(recipientPubKey)
	if err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: invalid recipient public key: %w", err)
	}

	// Generate ephemeral key pair.
	ephKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: GenerateKey: %w", err)
	}

	ephPubBytes := ephKey.PublicKey().Bytes() // 65-byte uncompressed

	// ECDH shared secret.
	sharedPoint, err := ephKey.ECDH(recipKey)
	if err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: ECDH: %w", err)
	}

	// HKDF-SHA256 → 32-byte AES key.
	aesKey, err := deriveKey(sharedPoint, ephPubBytes, recipientPubKey)
	if err != nil {
		return nil, err
	}

	// AES-256-GCM encrypt.
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("enclave: Encrypt: rand nonce: %w", err)
	}

	ct := gcm.Seal(nil, nonce, plaintext, nil)

	// Wire format: [1:version][65:ephPub][12:nonce][ciphertext+tag]
	out := make([]byte, 0, 1+65+12+len(ct))
	out = append(out, 0x01)
	out = append(out, ephPubBytes...)
	out = append(out, nonce...)
	out = append(out, ct...)

	return out, nil
}

// deriveKey runs HKDF-SHA256 over the ECDH shared point to produce a 32-byte
// AES key, binding the context with the ephemeral and recipient public keys.
func deriveKey(sharedPoint, ephPubBytes, recipPubBytes []byte) ([]byte, error) {
	info := make([]byte, 0, len(ephPubBytes)+len(recipPubBytes))
	info = append(info, ephPubBytes...)
	info = append(info, recipPubBytes...)

	kdf := hkdf.New(sha256.New, sharedPoint, nil, info)

	key := make([]byte, 32)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, fmt.Errorf("enclave: HKDF: %w", err)
	}

	return key, nil
}
