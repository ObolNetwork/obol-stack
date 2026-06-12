//go:build darwin && cgo

package enclave

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>

// Helper: copy CFDataRef bytes into a caller-allocated buffer.
// Returns the number of bytes copied.
static CFIndex cfdata_to_bytes(CFDataRef data, uint8_t *buf, size_t maxlen) {
	CFIndex len = CFDataGetLength(data);
	if ((size_t)len > maxlen) len = (CFIndex)maxlen;
	CFDataGetBytes(data, CFRangeMake(0, len), buf);
	return len;
}

// create_se_key: generate a new P-256 key inside the Secure Enclave.
//
// Parameters:
//   tag         - keychain application tag (used to persist and look up the key)
//   is_permanent - 1 to persist in keychain, 0 for ephemeral (process-lifetime only)
//   err_code_out - receives the CFError code on failure (0 if no CFError)
//   err_out      - receives a CFStringRef description on failure (caller must CFRelease if non-NULL)
//
// Returns the SecKeyRef on success (caller must CFRelease), or NULL on failure.
static SecKeyRef create_se_key(const char *tag, int is_permanent,
                                CFIndex *err_code_out, CFStringRef *err_out) {
	*err_out = NULL;
	*err_code_out = 0;
	CFErrorRef cfErr = NULL;

	CFDataRef tagData = CFDataCreate(kCFAllocatorDefault,
		(const uint8_t *)tag, (CFIndex)strlen(tag));

	// Access control: private key usable after first unlock, this device only.
	SecAccessControlRef acl = SecAccessControlCreateWithFlags(
		kCFAllocatorDefault,
		kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
		kSecAccessControlPrivateKeyUsage,
		&cfErr);
	if (!acl) {
		if (cfErr) {
			*err_code_out = CFErrorGetCode(cfErr);
			*err_out = CFErrorCopyDescription(cfErr);
			CFRelease(cfErr);
		} else {
			*err_out = CFSTR("SecAccessControlCreateWithFlags failed");
		}
		CFRelease(tagData);
		return NULL;
	}

	CFMutableDictionaryRef privAttrs = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(privAttrs, kSecAttrApplicationTag, tagData);
	CFDictionarySetValue(privAttrs, kSecAttrAccessControl, acl);
	if (is_permanent) {
		CFDictionarySetValue(privAttrs, kSecAttrIsPermanent, kCFBooleanTrue);
	} else {
		CFDictionarySetValue(privAttrs, kSecAttrIsPermanent, kCFBooleanFalse);
	}

	CFMutableDictionaryRef params = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(params, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(params, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFNumberRef keySize = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &(int){256});
	CFDictionarySetValue(params, kSecAttrKeySizeInBits, keySize);
	CFDictionarySetValue(params, kSecPrivateKeyAttrs, privAttrs);

	SecKeyRef privKey = SecKeyCreateRandomKey(params, &cfErr);

	CFRelease(keySize);
	CFRelease(privAttrs);
	CFRelease(params);
	CFRelease(acl);
	CFRelease(tagData);

	if (!privKey) {
		if (cfErr) {
			*err_code_out = CFErrorGetCode(cfErr);
			*err_out = CFErrorCopyDescription(cfErr);
			CFRelease(cfErr);
		} else {
			*err_out = CFSTR("SecKeyCreateRandomKey failed");
		}
		return NULL;
	}
	if (cfErr) CFRelease(cfErr);
	return privKey;
}

// load_se_key: look up an existing SE-backed private key by application tag.
// Returns the SecKeyRef on success (caller must CFRelease), NULL if not found.
// Sets *found to 1 on success, 0 on not-found or error.
// Sets *err_out to a description CFStringRef on non-not-found errors (caller must CFRelease).
static SecKeyRef load_se_key(const char *tag, int *found, CFStringRef *err_out) {
	*err_out = NULL;
	*found = 0;

	CFDataRef tagData = CFDataCreate(kCFAllocatorDefault,
		(const uint8_t *)tag, (CFIndex)strlen(tag));

	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(query, kSecClass, kSecClassKey);
	CFDictionarySetValue(query, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(query, kSecAttrApplicationTag, tagData);
	CFDictionarySetValue(query, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
	CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);

	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);

	CFRelease(query);
	CFRelease(tagData);

	if (status == errSecItemNotFound) {
		return NULL;
	}
	if (status != errSecSuccess || !result) {
		*err_out = CFSTR("SecItemCopyMatching failed");
		return NULL;
	}

	*found = 1;
	return (SecKeyRef)result;
}

// delete_se_key: remove the SE key with the given tag from the keychain.
// Returns 0 on success, -1 on error.
static int delete_se_key(const char *tag) {
	CFDataRef tagData = CFDataCreate(kCFAllocatorDefault,
		(const uint8_t *)tag, (CFIndex)strlen(tag));

	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(query, kSecClass, kSecClassKey);
	CFDictionarySetValue(query, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(query, kSecAttrApplicationTag, tagData);

	OSStatus status = SecItemDelete(query);

	CFRelease(query);
	CFRelease(tagData);

	return (status == errSecSuccess || status == errSecItemNotFound) ? 0 : -1;
}

// get_public_key_bytes: copy the uncompressed (04 || X || Y) public key
// bytes into buf (must be >= 65 bytes). Returns the number of bytes written.
static CFIndex get_public_key_bytes(SecKeyRef privKey, uint8_t *buf, size_t maxlen) {
	SecKeyRef pubKey = SecKeyCopyPublicKey(privKey);
	if (!pubKey) return 0;

	CFErrorRef cfErr = NULL;
	CFDataRef data = SecKeyCopyExternalRepresentation(pubKey, &cfErr);
	CFRelease(pubKey);
	if (!data) {
		if (cfErr) CFRelease(cfErr);
		return 0;
	}

	CFIndex n = cfdata_to_bytes(data, buf, maxlen);
	CFRelease(data);
	if (cfErr) CFRelease(cfErr);
	return n;
}

// se_sign: sign digest (raw 32-byte SHA-256 hash) with the SE private key.
// Writes DER-encoded ECDSA signature to sig_buf (must be >= 128 bytes).
// Returns number of bytes written, or 0 on error.
static CFIndex se_sign(SecKeyRef privKey,
                       const uint8_t *digest, size_t digest_len,
                       uint8_t *sig_buf, size_t sig_maxlen,
                       CFStringRef *err_out) {
	*err_out = NULL;
	CFDataRef digestData = CFDataCreate(kCFAllocatorDefault, digest, (CFIndex)digest_len);
	CFErrorRef cfErr = NULL;

	CFDataRef sig = SecKeyCreateSignature(privKey,
		kSecKeyAlgorithmECDSASignatureDigestX962SHA256,
		digestData, &cfErr);
	CFRelease(digestData);

	if (!sig) {
		if (cfErr) {
			*err_out = CFErrorCopyDescription(cfErr);
			CFRelease(cfErr);
		} else {
			*err_out = CFSTR("SecKeyCreateSignature failed");
		}
		return 0;
	}
	if (cfErr) CFRelease(cfErr);

	CFIndex n = cfdata_to_bytes(sig, sig_buf, sig_maxlen);
	CFRelease(sig);
	return n;
}

// se_ecdh: perform ECDH between the SE private key and peerPub (uncompressed, 65 bytes).
// Writes raw shared secret bytes to out_buf (must be >= 32 bytes).
// Returns number of bytes written, or 0 on error.
static CFIndex se_ecdh(SecKeyRef privKey,
                       const uint8_t *peer_pub, size_t peer_pub_len,
                       uint8_t *out_buf, size_t out_maxlen,
                       CFStringRef *err_out) {
	*err_out = NULL;

	// Import peer public key.
	CFDataRef peerData = CFDataCreate(kCFAllocatorDefault, peer_pub, (CFIndex)peer_pub_len);

	CFMutableDictionaryRef pubAttrs = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(pubAttrs, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
	CFDictionarySetValue(pubAttrs, kSecAttrKeyClass, kSecAttrKeyClassPublic);
	CFNumberRef keySize = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &(int){256});
	CFDictionarySetValue(pubAttrs, kSecAttrKeySizeInBits, keySize);

	CFErrorRef cfErr = NULL;
	SecKeyRef peerKey = SecKeyCreateWithData(peerData, pubAttrs, &cfErr);
	CFRelease(peerData);
	CFRelease(pubAttrs);
	CFRelease(keySize);

	if (!peerKey) {
		if (cfErr) {
			*err_out = CFErrorCopyDescription(cfErr);
			CFRelease(cfErr);
		} else {
			*err_out = CFSTR("SecKeyCreateWithData failed");
		}
		return 0;
	}
	if (cfErr) CFRelease(cfErr);

	// ECDH exchange — empty parameters dictionary.
	CFMutableDictionaryRef ecdhParams = CFDictionaryCreateMutable(
		kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDataRef sharedSecret = SecKeyCopyKeyExchangeResult(
		privKey,
		kSecKeyAlgorithmECDHKeyExchangeStandard,
		peerKey,
		ecdhParams,
		&cfErr);
	CFRelease(ecdhParams);
	CFRelease(peerKey);

	if (!sharedSecret) {
		if (cfErr) {
			*err_out = CFErrorCopyDescription(cfErr);
			CFRelease(cfErr);
		} else {
			*err_out = CFSTR("SecKeyCopyKeyExchangeResult failed");
		}
		return 0;
	}
	if (cfErr) CFRelease(cfErr);

	CFIndex n = cfdata_to_bytes(sharedSecret, out_buf, out_maxlen);
	CFRelease(sharedSecret);
	return n;
}

// cfstring_to_c: copy a CFStringRef into a malloc'd C string.
// Returns NULL on failure. Caller must free().
static char *cfstring_to_c(CFStringRef s) {
	if (!s) return NULL;
	CFIndex len = CFStringGetLength(s);
	CFIndex maxBytes = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
	char *buf = (char *)malloc(maxBytes);
	if (!buf) return NULL;
	if (!CFStringGetCString(s, buf, maxBytes, kCFStringEncodingUTF8)) {
		free(buf);
		return NULL;
	}
	return buf;
}

// errSecMissingEntitlement constant — process lacks keychain access entitlement.
#define OBOL_ERR_SEC_MISSING_ENTITLEMENT (-34018)
*/
import "C"

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"sync"
	"unsafe"
)

// ephemeralCache stores ephemeral (non-persistent) keys by tag so that
// multiple calls to newKey with the same tag return the same key within a
// single process.  This is only populated when the process lacks keychain
// write entitlements (unsigned dev/test binary).
var ephemeralCache sync.Map // map[string]*seKey

// seKey holds a reference to a Security.framework SecKeyRef.
type seKey struct {
	privRef    C.SecKeyRef
	tag        string
	pubKey     []byte // cached uncompressed 65-byte public key
	persistent bool   // true if stored in keychain; false if ephemeral
}

// PublicKeyBytes returns the uncompressed 65-byte SEC1 public key.
func (k *seKey) PublicKeyBytes() []byte { return k.pubKey }

// Tag returns the keychain application tag.
func (k *seKey) Tag() string { return k.tag }

// Persistent reports whether this key is durably stored in the keychain.
func (k *seKey) Persistent() bool { return k.persistent }

// Sign signs a 32-byte SHA-256 digest using the SE private key.
// Returns a DER-encoded ECDSA signature.
func (k *seKey) Sign(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("enclave: Sign expects a 32-byte SHA-256 digest, got %d", len(digest))
	}
	sigBuf := make([]byte, 128)
	var errStr C.CFStringRef

	n := C.se_sign(
		k.privRef,
		(*C.uint8_t)(unsafe.Pointer(&digest[0])),
		C.size_t(len(digest)),
		(*C.uint8_t)(unsafe.Pointer(&sigBuf[0])),
		C.size_t(len(sigBuf)),
		&errStr, //nolint:gocritic // CGo pointer arguments, not duplicate subexpressions
	)
	if n == 0 {
		msg := cfStringToGo(errStr)
		if errStr != 0 {
			C.CFRelease(C.CFTypeRef(errStr))
		}
		return nil, fmt.Errorf("enclave: Sign failed: %s", msg)
	}
	return sigBuf[:n], nil
}

// ECDH performs a Diffie-Hellman exchange with the given peer uncompressed public key.
func (k *seKey) ECDH(peerPubKeyBytes []byte) ([]byte, error) {
	if len(peerPubKeyBytes) != 65 {
		return nil, fmt.Errorf("enclave: ECDH expects 65-byte uncompressed public key, got %d", len(peerPubKeyBytes))
	}
	outBuf := make([]byte, 64)
	var errStr C.CFStringRef

	n := C.se_ecdh(
		k.privRef,
		(*C.uint8_t)(unsafe.Pointer(&peerPubKeyBytes[0])),
		C.size_t(len(peerPubKeyBytes)),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
		C.size_t(len(outBuf)),
		&errStr, //nolint:gocritic // CGo pointer arguments, not duplicate subexpressions
	)
	if n == 0 {
		msg := cfStringToGo(errStr)
		if errStr != 0 {
			C.CFRelease(C.CFTypeRef(errStr))
		}
		return nil, fmt.Errorf("enclave: ECDH failed: %s", msg)
	}
	return outBuf[:n], nil
}

// Decrypt decrypts a ciphertext produced by Encrypt using this key's SE private component.
// Callers that already hold a Key handle should use this method directly rather than
// the package-level Decrypt (which re-loads the key from the keychain).
func (k *seKey) Decrypt(ciphertext []byte) ([]byte, error) {
	return decryptWithKey(k, ciphertext)
}

// Delete removes this key from the Secure Enclave / keychain.
func (k *seKey) Delete() error {
	return deleteKey(k.tag)
}

// newKey generates (or loads if already existing) a SE-backed P-256 key.
// It first tries to persist the key in the keychain.  If the process lacks
// keychain entitlements (e.g. unsigned test binary), it falls back to an
// ephemeral key that is valid for the lifetime of the process.
func newKey(tag string) (Key, error) {
	// 1. Return existing persistent key if already in keychain.
	if existing, err := loadKey(tag); err == nil {
		return existing, nil
	}

	// 2. Return cached ephemeral key if one exists for this tag.
	if cached, ok := ephemeralCache.Load(tag); ok {
		return cached.(*seKey), nil
	}

	ctag := C.CString(tag)
	defer C.free(unsafe.Pointer(ctag))

	// Attempt persistent (keychain-backed) creation first.
	var errCode C.CFIndex
	var errStr C.CFStringRef
	privRef := C.create_se_key(ctag, C.int(1), &errCode, &errStr) //nolint:gocritic // CGo pointer arguments, not duplicate subexpressions

	if privRef != 0 {
		// Success — key is in keychain.
		if errStr != 0 {
			C.CFRelease(C.CFTypeRef(errStr))
		}
		pub, err := extractPublicKey(privRef)
		if err != nil {
			C.CFRelease(C.CFTypeRef(privRef))
			return nil, err
		}
		return &seKey{privRef: privRef, tag: tag, pubKey: pub, persistent: true}, nil
	}

	// Persistent creation failed. If the error is errSecMissingEntitlement,
	// fall back to an ephemeral key (dev/test use without code-signing).
	if C.int(errCode) != C.OBOL_ERR_SEC_MISSING_ENTITLEMENT {
		msg := cfStringToGo(errStr)
		if errStr != 0 {
			C.CFRelease(C.CFTypeRef(errStr))
		}
		return nil, fmt.Errorf("enclave: create_se_key (persistent): %s", msg)
	}
	if errStr != 0 {
		C.CFRelease(C.CFTypeRef(errStr))
	}

	// Ephemeral fallback.
	var errStr2 C.CFStringRef
	privRef = C.create_se_key(ctag, C.int(0), &errCode, &errStr2) //nolint:gocritic // CGo pointer arguments, not duplicate subexpressions
	if privRef == 0 {
		msg := cfStringToGo(errStr2)
		if errStr2 != 0 {
			C.CFRelease(C.CFTypeRef(errStr2))
		}
		return nil, fmt.Errorf("enclave: create_se_key (ephemeral fallback): %s", msg)
	}
	if errStr2 != 0 {
		C.CFRelease(C.CFTypeRef(errStr2))
	}

	pub, err := extractPublicKey(privRef)
	if err != nil {
		C.CFRelease(C.CFTypeRef(privRef))
		return nil, err
	}
	k := &seKey{privRef: privRef, tag: tag, pubKey: pub, persistent: false}
	// Store in cache so subsequent newKey calls with the same tag reuse this key.
	ephemeralCache.Store(tag, k)
	return k, nil
}

// loadKey loads an existing SE-backed key from the keychain.
// Returns ErrKeyNotFound if no matching key exists.
func loadKey(tag string) (Key, error) {
	ctag := C.CString(tag)
	defer C.free(unsafe.Pointer(ctag))

	var found C.int
	var errStr C.CFStringRef
	privRef := C.load_se_key(ctag, &found, &errStr) //nolint:gocritic // CGo pointer arguments, not duplicate subexpressions

	if found == 0 {
		if errStr != 0 {
			msg := cfStringToGo(errStr)
			C.CFRelease(C.CFTypeRef(errStr))
			return nil, fmt.Errorf("enclave: load_se_key: %s", msg)
		}
		return nil, ErrKeyNotFound
	}
	if privRef == 0 {
		return nil, ErrKeyNotFound
	}
	if errStr != 0 {
		C.CFRelease(C.CFTypeRef(errStr))
	}

	pub, err := extractPublicKey(privRef)
	if err != nil {
		C.CFRelease(C.CFTypeRef(privRef))
		return nil, err
	}

	return &seKey{privRef: privRef, tag: tag, pubKey: pub, persistent: true}, nil
}

// deleteKey removes an SE-backed key from the keychain by tag.
func deleteKey(tag string) error {
	ctag := C.CString(tag)
	defer C.free(unsafe.Pointer(ctag))

	if C.delete_se_key(ctag) != 0 {
		return fmt.Errorf("enclave: delete_se_key failed for tag %q", tag)
	}
	return nil
}

// checkSIP reads kern.csr_active_config and returns ErrSIPDisabled if SIP is off.
func checkSIP() error {
	cfg, err := sysctlCsrActiveConfig()
	if err != nil {
		return fmt.Errorf("enclave: checkSIP: %w", err)
	}
	// Any value >= 0x7F indicates SIP is substantially or fully disabled.
	const sipFullyDisabled = uint32(0x7F)
	if cfg >= sipFullyDisabled {
		return ErrSIPDisabled
	}
	return nil
}

// decrypt loads the SE key by tag and decrypts the ciphertext.
func decrypt(tag string, ciphertext []byte) ([]byte, error) {
	k, err := loadKey(tag)
	if err != nil {
		return nil, fmt.Errorf("enclave: Decrypt: load key %q: %w", tag, err)
	}
	return decryptWithKey(k.(*seKey), ciphertext)
}

// decryptWithKey decrypts using a key already in hand (avoids keychain re-lookup).
func decryptWithKey(k *seKey, ciphertext []byte) ([]byte, error) {
	const headerLen = 1 + 65 + 12 // version + ephPub + nonce
	if len(ciphertext) < headerLen+16 {
		return nil, fmt.Errorf("enclave: Decrypt: ciphertext too short (%d bytes)", len(ciphertext))
	}
	if ciphertext[0] != 0x01 {
		return nil, fmt.Errorf("enclave: Decrypt: unsupported version 0x%02x", ciphertext[0])
	}

	ephPubBytes := ciphertext[1:66]
	nonce := ciphertext[66:78]
	ctext := ciphertext[78:]

	// ECDH with the SE private key.
	sharedPoint, err := k.ECDH(ephPubBytes)
	if err != nil {
		return nil, fmt.Errorf("enclave: Decrypt: ECDH: %w", err)
	}

	// HKDF.
	aesKey, err := deriveKey(sharedPoint, ephPubBytes, k.pubKey)
	if err != nil {
		return nil, err
	}

	// AES-256-GCM decrypt.
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("enclave: Decrypt: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("enclave: Decrypt: cipher.NewGCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ctext, nil)
	if err != nil {
		return nil, fmt.Errorf("enclave: Decrypt: gcm.Open: %w", err)
	}
	return plaintext, nil
}

// extractPublicKey reads the 65-byte uncompressed public key from a SecKeyRef.
func extractPublicKey(privRef C.SecKeyRef) ([]byte, error) {
	buf := make([]byte, 128)
	n := C.get_public_key_bytes(
		privRef,
		(*C.uint8_t)(unsafe.Pointer(&buf[0])),
		C.size_t(len(buf)),
	)
	if n != 65 {
		return nil, fmt.Errorf("enclave: unexpected public key length %d (expected 65)", int(n))
	}
	return buf[:65], nil
}

// cfStringToGo converts a CFStringRef to a Go string.
func cfStringToGo(s C.CFStringRef) string {
	if s == 0 {
		return "(no error description)"
	}
	cstr := C.cfstring_to_c(s)
	if cstr == nil {
		return "(cfstring_to_c failed)"
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}
