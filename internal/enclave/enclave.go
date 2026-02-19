// Package enclave provides access to the Apple Secure Enclave (SEP) for
// hardware-backed key management and ECIES encryption.
//
// On darwin with CGO enabled, keys are generated inside the Secure Enclave
// co-processor and never leave hardware. On all other platforms the package
// returns ErrNotSupported for every operation so the rest of the codebase
// can compile and be tested cross-platform.
//
// Encryption scheme:
//
//	ephemeral P-256 key pair  →  ECDH with SE public key
//	  →  HKDF-SHA-256 (32-byte key)
//	  →  AES-256-GCM
//
// Wire format (Encrypt / Decrypt):
//
//	[1  byte  ] version (0x01)
//	[65 bytes ] uncompressed ephemeral public key
//	[12 bytes ] AES-GCM nonce
//	[n  bytes ] ciphertext
//	[16 bytes ] AES-GCM authentication tag
package enclave

import "errors"

// ErrNotSupported is returned on non-darwin builds or when CGO is disabled.
var ErrNotSupported = errors.New("enclave: Secure Enclave not supported on this platform")

// ErrSIPDisabled is returned by CheckSIP when System Integrity Protection
// is not active, indicating the host cannot provide the expected isolation
// guarantees.
var ErrSIPDisabled = errors.New("enclave: System Integrity Protection (SIP) is disabled")

// ErrKeyNotFound is returned when no key with the requested tag exists in
// the keychain.
var ErrKeyNotFound = errors.New("enclave: key not found in keychain")

// Key is a handle to a P-256 key whose private component lives inside the
// Secure Enclave.  The public key is accessible; the private key is not.
type Key interface {
	// PublicKeyBytes returns the uncompressed (65-byte) SEC1 encoding of the
	// public key: 0x04 || X || Y.
	PublicKeyBytes() []byte

	// Sign signs digest (a raw SHA-256 hash) using the SE private key and
	// returns the DER-encoded ECDSA signature.
	Sign(digest []byte) ([]byte, error)

	// ECDH performs a Diffie-Hellman key exchange with the provided
	// uncompressed peer public key and returns the raw shared secret bytes.
	ECDH(peerPubKeyBytes []byte) ([]byte, error)

	// Decrypt decrypts a ciphertext produced by Encrypt using this key's
	// SE-backed private component directly.  Callers that hold a Key handle
	// should prefer this over the package-level Decrypt so that the key
	// does not need to be re-loaded from the keychain.
	Decrypt(ciphertext []byte) ([]byte, error)

	// Tag returns the keychain application tag that identifies this key.
	Tag() string

	// Persistent reports whether this key is durably stored in the keychain.
	// An ephemeral key (created when keychain write permissions are absent)
	// is valid for the lifetime of the process only.
	Persistent() bool

	// Delete removes the key from the Secure Enclave / keychain permanently.
	Delete() error
}

// NewKey generates a new P-256 key backed by the Secure Enclave and
// persists it in the keychain under the given application tag.
//
// If a key with the same tag already exists it is returned as-is without
// generating a new key.
func NewKey(tag string) (Key, error) {
	return newKey(tag)
}

// LoadKey loads an existing SE-backed key from the keychain by application tag.
// Returns ErrKeyNotFound if no matching key exists.
func LoadKey(tag string) (Key, error) {
	return loadKey(tag)
}

// DeleteKey removes the SE-backed key identified by tag from the keychain.
func DeleteKey(tag string) error {
	return deleteKey(tag)
}

// CheckSIP verifies that System Integrity Protection is enabled on the host.
// Returns nil when SIP is active, ErrSIPDisabled when it is not, or another
// error when the check itself fails.
func CheckSIP() error {
	return checkSIP()
}

// Encrypt encrypts plaintext so that only the holder of the SE key identified
// by recipientPubKey can decrypt it.
//
// recipientPubKey must be the uncompressed (65-byte) SEC1 public key
// returned by Key.PublicKeyBytes().
//
// The returned ciphertext uses the wire format described in the package doc.
func Encrypt(recipientPubKey, plaintext []byte) ([]byte, error) {
	return encrypt(recipientPubKey, plaintext)
}

// Decrypt decrypts a ciphertext produced by Encrypt using the SE private key
// identified by tag.
func Decrypt(tag string, ciphertext []byte) ([]byte, error) {
	return decrypt(tag, ciphertext)
}
