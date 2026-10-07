package keystore

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestGenerateKeypair(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	if len(privKey) != 32 {
		t.Errorf("private key length = %d, want 32", len(privKey))
	}

	if len(pubKey) != 64 {
		t.Errorf("public key length = %d, want 64 (uncompressed without prefix)", len(pubKey))
	}

	// Keys should be non-zero.
	allZero := true

	for _, b := range privKey {
		if b != 0 {
			allZero = false
			break
		}
	}

	if allZero {
		t.Error("private key is all zeros")
	}
}

func TestGenerateKeypairUniqueness(t *testing.T) {
	priv1, _, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	priv2, _, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	if hex.EncodeToString(priv1) == hex.EncodeToString(priv2) {
		t.Error("two generated keys are identical")
	}
}

func TestAddressFromPublicKey(t *testing.T) {
	// Known test vector: private key 0x01 on secp256k1.
	// Public key (uncompressed, no prefix): well-known value.
	// We test that the output is a valid 0x-prefixed 42-char hex string.
	_, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	addr := AddressFromPublicKey(pubKey)
	if !strings.HasPrefix(addr, "0x") {
		t.Errorf("address should start with 0x, got %s", addr)
	}

	if len(addr) != 42 {
		t.Errorf("address length = %d, want 42", len(addr))
	}

	// Verify it's valid hex (after removing 0x).
	_, err = hex.DecodeString(strings.ToLower(addr[2:]))
	if err != nil {
		t.Errorf("address is not valid hex: %v", err)
	}
}

func TestToChecksumAddress(t *testing.T) {
	// EIP-55 test vectors.
	tests := []struct {
		input string
		want  string
	}{
		{"5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{"fb6916095ca1df60bb79ce92ce3ea74c37c5d359", "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"},
	}
	for _, tt := range tests {
		got := ToChecksumAddress(tt.input)
		if got != tt.want {
			t.Errorf("ToChecksumAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEncryptV3(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	password := "test-password-123"

	keystoreJSON, keystoreID, err := EncryptV3(privKey, pubKey, password)
	if err != nil {
		t.Fatalf("EncryptV3: %v", err)
	}

	if len(keystoreID) == 0 {
		t.Error("keystore ID is empty")
	}

	// Parse and validate JSON structure.
	var ks V3
	if err := json.Unmarshal(keystoreJSON, &ks); err != nil {
		t.Fatalf("unmarshal keystore: %v", err)
	}

	if ks.Version != 3 {
		t.Errorf("version = %d, want 3", ks.Version)
	}

	if ks.Crypto.Cipher != "aes-128-ctr" {
		t.Errorf("cipher = %q, want aes-128-ctr", ks.Crypto.Cipher)
	}

	if ks.Crypto.KDF != "scrypt" {
		t.Errorf("kdf = %q, want scrypt", ks.Crypto.KDF)
	}

	if ks.Crypto.KDFParams.N != 262144 {
		t.Errorf("scrypt N = %d, want 262144", ks.Crypto.KDFParams.N)
	}

	if ks.Crypto.KDFParams.R != 8 {
		t.Errorf("scrypt r = %d, want 8", ks.Crypto.KDFParams.R)
	}

	if ks.Crypto.KDFParams.P != 1 {
		t.Errorf("scrypt p = %d, want 1", ks.Crypto.KDFParams.P)
	}

	if ks.Crypto.KDFParams.DKLen != 32 {
		t.Errorf("scrypt dklen = %d, want 32", ks.Crypto.KDFParams.DKLen)
	}

	if len(ks.Address) != 40 {
		t.Errorf("address length = %d, want 40 (hex without 0x)", len(ks.Address))
	}

	if ks.ID != keystoreID {
		t.Errorf("ID = %q, want %q", ks.ID, keystoreID)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	password := "round-trip-test-password"

	keystoreJSON, _, err := EncryptV3(privKey, pubKey, password)
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt and verify.
	recovered, err := DecryptV3(keystoreJSON, password)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if hex.EncodeToString(recovered) != hex.EncodeToString(privKey) {
		t.Errorf("recovered key does not match original")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	keystoreJSON, _, err := EncryptV3(privKey, pubKey, "correct-password")
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptV3(keystoreJSON, "wrong-password")
	if err == nil {
		t.Error("expected error when decrypting with wrong password")
	}

	if !strings.Contains(err.Error(), "MAC mismatch") {
		t.Errorf("expected MAC mismatch error, got: %v", err)
	}
}

func TestRandomPassword(t *testing.T) {
	p1, err := RandomPassword(32)
	if err != nil {
		t.Fatal(err)
	}

	if len(p1) != 32 {
		t.Errorf("password length = %d, want 32", len(p1))
	}

	// Verify charset (alphanumeric only).
	for _, c := range p1 {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			t.Errorf("password contains non-alphanumeric character: %c", c)
		}
	}

	// Two passwords should be different.
	p2, err := RandomPassword(32)
	if err != nil {
		t.Fatal(err)
	}

	if p1 == p2 {
		t.Error("two generated passwords are identical")
	}
}

// TestEncryptV3InteropGeth pins the wire format against go-ethereum's
// keystore decoder: the remote-signer and `obol agent wallet restore`
// (which uses gethkeystore.DecryptKey for raw V3 imports) must be able to
// read every keystore this package writes.
func TestEncryptV3InteropGeth(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	keystoreJSON, keystoreID, err := EncryptV3(privKey, pubKey, "interop")
	if err != nil {
		t.Fatal(err)
	}

	key, err := gethkeystore.DecryptKey(keystoreJSON, "interop")
	if err != nil {
		t.Fatalf("geth DecryptKey: %v", err)
	}

	if got := hex.EncodeToString(ethcrypto.FromECDSA(key.PrivateKey)); got != hex.EncodeToString(privKey) {
		t.Error("geth recovered a different private key")
	}

	if key.Address.Hex() != AddressFromPublicKey(pubKey) {
		t.Errorf("geth address %s != %s", key.Address.Hex(), AddressFromPublicKey(pubKey))
	}

	if key.Id.String() != keystoreID {
		t.Errorf("geth id %s != %s", key.Id, keystoreID)
	}

	var ks V3
	if err := json.Unmarshal(keystoreJSON, &ks); err != nil {
		t.Fatal(err)
	}

	// Address field is lowercase hex without 0x (go-ethereum convention).
	if want := strings.ToLower(strings.TrimPrefix(key.Address.Hex(), "0x")); ks.Address != want {
		t.Errorf("keystore address field = %q, want %q", ks.Address, want)
	}
}

// TestToChecksumAddressMatchesGeth cross-checks EIP-55 encoding against
// go-ethereum for random addresses, with and without a 0x prefix.
func TestToChecksumAddressMatchesGeth(t *testing.T) {
	for range 200 {
		key, err := ethcrypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}

		want := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
		lower := strings.ToLower(want)

		if got := ToChecksumAddress(lower); got != want {
			t.Fatalf("ToChecksumAddress(%s) = %s, want %s", lower, got, want)
		}

		if got := ToChecksumAddress(lower[2:]); got != want {
			t.Fatalf("ToChecksumAddress(%s) = %s, want %s", lower[2:], got, want)
		}
	}
}

func TestGenerateInMemory(t *testing.T) {
	mat, err := GenerateInMemory()
	if err != nil {
		t.Fatal(err)
	}

	if len(mat.Password) != PasswordLength {
		t.Errorf("password length = %d, want %d", len(mat.Password), PasswordLength)
	}

	if !strings.HasPrefix(mat.PublicKey, "0x04") || len(mat.PublicKey) != 132 {
		t.Errorf("public key = %q, want 0x04-prefixed uncompressed key", mat.PublicKey)
	}

	key, err := gethkeystore.DecryptKey(mat.KeystoreJSON, mat.Password)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if key.Address.Hex() != mat.Address {
		t.Errorf("address = %s, keystore decrypts to %s", mat.Address, key.Address.Hex())
	}

	if key.Id.String() != mat.KeystoreUUID {
		t.Errorf("uuid = %s, keystore id %s", mat.KeystoreUUID, key.Id)
	}
}
