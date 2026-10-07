// Package keystore mints secp256k1 wallets and encrypts/decrypts them as
// Web3 Secret Storage v3 keystores (scrypt + AES-128-CTR), the format the
// remote-signer loads. It is shared by the Hermes runtime, the
// serviceoffer-controller (sub-agent wallets) and the deprecated OpenClaw
// runtime so there is exactly one implementation of the wallet crypto path.
package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

// V3 is a keystore document matching the Web3 Secret Storage Definition v3.
type V3 struct {
	Address string   `json:"address"` // hex address without 0x prefix
	Crypto  V3Crypto `json:"crypto"`
	ID      string   `json:"id"`
	Version int      `json:"version"`
}

// V3Crypto is the "crypto" section of a V3 keystore.
type V3Crypto struct {
	Cipher       string       `json:"cipher"`
	CipherText   string       `json:"ciphertext"`
	CipherParams CipherParams `json:"cipherparams"`
	KDF          string       `json:"kdf"`
	KDFParams    KDFParams    `json:"kdfparams"`
	MAC          string       `json:"mac"`
}

// CipherParams holds the AES-CTR IV.
type CipherParams struct {
	IV string `json:"iv"`
}

// KDFParams holds the scrypt parameters.
type KDFParams struct {
	DKLen int    `json:"dklen"`
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	Salt  string `json:"salt"`
}

// scrypt parameters matching go-ethereum's "standard" defaults.
const (
	ScryptN     = 262144
	ScryptR     = 8
	ScryptP     = 1
	ScryptDKLen = 32
)

// PasswordLength is the length of passwords minted by GenerateInMemory and
// the runtime wallet provisioners.
const PasswordLength = 32

// Material bundles the in-memory output of GenerateInMemory: a freshly
// minted secp256k1 wallet encrypted as a Web3 V3 keystore plus the password
// needed to decrypt it. Used by callers that persist the keystore via
// mechanisms other than a host-side PVC (e.g. a K8s Secret created by an
// in-cluster controller).
type Material struct {
	Address      string // EIP-55 checksummed
	PublicKey    string // 0x04 || X || Y (130 hex chars)
	KeystoreUUID string // V3 keystore "id" field
	KeystoreJSON []byte // Encrypted V3 keystore document
	Password     string // Random 32-char password
}

// GenerateInMemory mints a fresh wallet keypair, V3-encrypts it in memory
// with a random password, and returns everything the caller needs to
// persist the keystore wherever it wants.
func GenerateInMemory() (*Material, error) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	defer Zero(privKey)

	password, err := RandomPassword(PasswordLength)
	if err != nil {
		return nil, fmt.Errorf("password generation failed: %w", err)
	}

	keystoreJSON, keystoreID, err := EncryptV3(privKey, pubKey, password)
	if err != nil {
		return nil, fmt.Errorf("keystore encryption failed: %w", err)
	}

	return &Material{
		Address:      AddressFromPublicKey(pubKey),
		PublicKey:    "0x04" + hex.EncodeToString(pubKey),
		KeystoreUUID: keystoreID,
		KeystoreJSON: keystoreJSON,
		Password:     password,
	}, nil
}

// GenerateKeypair creates a random secp256k1 private key using crypto/rand.
// Returns the 32-byte private key and the 64-byte uncompressed public key
// (X || Y, without the 0x04 prefix).
func GenerateKeypair() (privKeyBytes, pubKeyUncompressed []byte, err error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("secp256k1 key generation: %w", err)
	}

	privKeyBytes = privKey.Serialize() // 32 bytes
	// Uncompressed public key is 04 || X || Y (65 bytes); address derivation
	// needs X || Y.
	pubKeyUncompressed = privKey.PubKey().SerializeUncompressed()[1:]

	return privKeyBytes, pubKeyUncompressed, nil
}

// AddressFromPublicKey computes the EIP-55 checksummed, 0x-prefixed
// Ethereum address of a 64-byte uncompressed public key (no 0x04 prefix).
func AddressFromPublicKey(pubKey []byte) string {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(pubKey)
	hash := h.Sum(nil)

	return ToChecksumAddress(hex.EncodeToString(hash[12:])) // last 20 bytes
}

// ToChecksumAddress applies EIP-55 mixed-case checksum encoding to a hex
// address (with or without 0x prefix) and returns it 0x-prefixed.
func ToChecksumAddress(addr string) string {
	addr = strings.ToLower(strings.TrimPrefix(addr, "0x"))
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(addr))
	hash := hex.EncodeToString(h.Sum(nil))

	var result strings.Builder
	result.WriteString("0x")

	for i, c := range addr {
		// Letters whose corresponding hash nibble is >= 8 are uppercased.
		if c >= '0' && c <= '9' || hash[i] < '8' {
			result.WriteRune(c)
		} else {
			result.WriteRune(c - 32) // lowercase to uppercase
		}
	}

	return result.String()
}

// EncryptV3 encrypts a private key using the Web3 Secret Storage v3 format:
// scrypt KDF (N=262144, r=8, p=1) + AES-128-CTR. Returns the JSON-encoded
// keystore and its UUID.
func EncryptV3(privKey, pubKey []byte, password string) ([]byte, string, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", fmt.Errorf("salt generation: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, "", fmt.Errorf("iv generation: %w", err)
	}

	derivedKey, err := scrypt.Key([]byte(password), salt, ScryptN, ScryptR, ScryptP, ScryptDKLen)
	if err != nil {
		return nil, "", fmt.Errorf("scrypt key derivation: %w", err)
	}
	defer Zero(derivedKey)

	// AES-128-CTR with the first 16 bytes of the derived key.
	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return nil, "", fmt.Errorf("aes cipher: %w", err)
	}

	cipherText := make([]byte, len(privKey))
	cipher.NewCTR(block, iv).XORKeyStream(cipherText, privKey)

	// MAC = Keccak-256(derivedKey[16:32] || cipherText).
	mac := sha3.NewLegacyKeccak256()
	_, _ = mac.Write(derivedKey[16:32])
	_, _ = mac.Write(cipherText)

	keystoreID := uuid.New().String()

	ks := V3{
		Address: strings.TrimPrefix(strings.ToLower(AddressFromPublicKey(pubKey)), "0x"),
		Crypto: V3Crypto{
			Cipher:       "aes-128-ctr",
			CipherText:   hex.EncodeToString(cipherText),
			CipherParams: CipherParams{IV: hex.EncodeToString(iv)},
			KDF:          "scrypt",
			KDFParams: KDFParams{
				DKLen: ScryptDKLen,
				N:     ScryptN,
				R:     ScryptR,
				P:     ScryptP,
				Salt:  hex.EncodeToString(salt),
			},
			MAC: hex.EncodeToString(mac.Sum(nil)),
		},
		ID:      keystoreID,
		Version: 3,
	}

	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("json marshal: %w", err)
	}

	return data, keystoreID, nil
}

// DecryptV3 decrypts a scrypt/AES-128-CTR V3 keystore and returns the raw
// private key bytes. It returns an error on a MAC mismatch (wrong password
// or corrupted keystore).
func DecryptV3(keystoreJSON []byte, password string) ([]byte, error) {
	var ks V3
	if err := json.Unmarshal(keystoreJSON, &ks); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	salt, err := hex.DecodeString(ks.Crypto.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	iv, err := hex.DecodeString(ks.Crypto.CipherParams.IV)
	if err != nil {
		return nil, fmt.Errorf("decode iv: %w", err)
	}

	cipherText, err := hex.DecodeString(ks.Crypto.CipherText)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	storedMAC, err := hex.DecodeString(ks.Crypto.MAC)
	if err != nil {
		return nil, fmt.Errorf("decode mac: %w", err)
	}

	p := ks.Crypto.KDFParams

	derivedKey, err := scrypt.Key([]byte(password), salt, p.N, p.R, p.P, p.DKLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt: %w", err)
	}
	defer Zero(derivedKey)

	if len(derivedKey) < 32 {
		return nil, errors.New("scrypt dklen must be at least 32")
	}

	mac := sha3.NewLegacyKeccak256()
	_, _ = mac.Write(derivedKey[16:32])
	_, _ = mac.Write(cipherText)

	if !constantTimeEqual(mac.Sum(nil), storedMAC) {
		return nil, errors.New("MAC mismatch: wrong password or corrupted keystore")
	}

	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}

	plaintext := make([]byte, len(cipherText))
	cipher.NewCTR(block, iv).XORKeyStream(plaintext, cipherText)

	return plaintext, nil
}

// RandomPassword returns a cryptographically random alphanumeric
// (a-z, A-Z, 0-9) password of the given length.
func RandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	charsetLen := big.NewInt(int64(len(charset)))

	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("random int: %w", err)
		}

		result[i] = charset[n.Int64()]
	}

	return string(result), nil
}

// Zero overwrites b with zeros (best-effort scrubbing of key material).
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}

	return result == 0
}
