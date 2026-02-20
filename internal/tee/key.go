package tee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
	"golang.org/x/crypto/hkdf"
)

// backend is the internal interface that each TEE-specific implementation
// (tdx, snp, nitro, stub) must satisfy.
type backend interface {
	sign(digest []byte) ([]byte, error)
	ecdh(peerPubKeyBytes []byte) ([]byte, error)
	attest(userData []byte) ([]byte, error)
	delete() error
}

// teeKey satisfies enclave.Key using a TEE-backed (or stub) private key.
// The ECIES decryption reuses the same wire format as the macOS Secure
// Enclave backend — only the ECDH step delegates to the TEE; AES-GCM
// runs in-process.
type teeKey struct {
	tag       string
	teeType   TEEType
	pubBytes  []byte // cached 65-byte uncompressed P-256 (0x04 || X || Y)
	modelHash string
	backend   backend
}

// Compile-time check: teeKey implements enclave.Key.
var _ enclave.Key = (*teeKey)(nil)

func (k *teeKey) PublicKeyBytes() []byte { return k.pubBytes }
func (k *teeKey) Tag() string           { return k.tag }
func (k *teeKey) Persistent() bool      { return true }

func (k *teeKey) Sign(digest []byte) ([]byte, error) {
	return k.backend.sign(digest)
}

func (k *teeKey) ECDH(peerPubKeyBytes []byte) ([]byte, error) {
	return k.backend.ecdh(peerPubKeyBytes)
}

// Decrypt decrypts a ciphertext produced by enclave.Encrypt. The wire
// format is:
//
//	[1:version=0x01][65:ephemeral pubkey][12:GCM nonce][ciphertext+16:GCM tag]
//
// The ECDH step uses the TEE-backed private key; AES-GCM runs in-process.
func (k *teeKey) Decrypt(ciphertext []byte) ([]byte, error) {
	const (
		versionLen = 1
		pubkeyLen  = 65
		nonceLen   = 12
		tagLen     = 16
		headerLen  = versionLen + pubkeyLen + nonceLen
	)

	if len(ciphertext) < headerLen+tagLen {
		return nil, fmt.Errorf("tee: ciphertext too short (%d bytes)", len(ciphertext))
	}
	if ciphertext[0] != 0x01 {
		return nil, fmt.Errorf("tee: unsupported ciphertext version 0x%02x", ciphertext[0])
	}

	ephPub := ciphertext[versionLen : versionLen+pubkeyLen]
	nonce := ciphertext[versionLen+pubkeyLen : headerLen]
	ct := ciphertext[headerLen:]

	// ECDH via TEE backend.
	sharedPoint, err := k.backend.ecdh(ephPub)
	if err != nil {
		return nil, fmt.Errorf("tee: ECDH failed: %w", err)
	}

	// Derive AES key using HKDF-SHA256 (same as enclave/ecies.go).
	aesKey, err := deriveKey(sharedPoint, ephPub, k.pubBytes)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("tee: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("tee: cipher.NewGCM: %w", err)
	}

	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("tee: AES-GCM decrypt failed: %w", err)
	}
	return plain, nil
}

func (k *teeKey) Delete() error {
	return k.backend.delete()
}

// deriveKey is identical to enclave/ecies.go's deriveKey — HKDF-SHA256
// over the ECDH shared point, binding ephemeral and recipient public keys.
func deriveKey(sharedPoint, ephPubBytes, recipPubBytes []byte) ([]byte, error) {
	info := make([]byte, 0, len(ephPubBytes)+len(recipPubBytes))
	info = append(info, ephPubBytes...)
	info = append(info, recipPubBytes...)

	kdf := hkdf.New(sha256.New, sharedPoint, nil, info)
	key := make([]byte, 32)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, fmt.Errorf("tee: HKDF: %w", err)
	}
	return key, nil
}
