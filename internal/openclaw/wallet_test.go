package openclaw

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestGenerateKeypair(t *testing.T) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		t.Fatalf("generateKeypair: %v", err)
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
	priv1, _, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	priv2, _, err := generateKeypair()
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
	_, pubKey, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	addr := addressFromPublicKey(pubKey)
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
		got := toChecksumAddress(tt.input)
		if got != tt.want {
			t.Errorf("toChecksumAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEncryptToV3Keystore(t *testing.T) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	password := "test-password-123"
	keystoreJSON, keystoreID, err := encryptToV3Keystore(privKey, pubKey, password)
	if err != nil {
		t.Fatalf("encryptToV3Keystore: %v", err)
	}

	if len(keystoreID) == 0 {
		t.Error("keystore ID is empty")
	}

	// Parse and validate JSON structure.
	var ks v3Keystore
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
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	password := "round-trip-test-password"
	keystoreJSON, _, err := encryptToV3Keystore(privKey, pubKey, password)
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt and verify.
	recovered, err := decryptV3Keystore(keystoreJSON, password)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if hex.EncodeToString(recovered) != hex.EncodeToString(privKey) {
		t.Errorf("recovered key does not match original")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	privKey, pubKey, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	keystoreJSON, _, err := encryptToV3Keystore(privKey, pubKey, "correct-password")
	if err != nil {
		t.Fatal(err)
	}

	_, err = decryptV3Keystore(keystoreJSON, "wrong-password")
	if err == nil {
		t.Error("expected error when decrypting with wrong password")
	}
	if !strings.Contains(err.Error(), "MAC mismatch") {
		t.Errorf("expected MAC mismatch error, got: %v", err)
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	p1, err := generateRandomPassword(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 32 {
		t.Errorf("password length = %d, want 32", len(p1))
	}

	// Verify charset (alphanumeric only).
	for _, c := range p1 {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			t.Errorf("password contains non-alphanumeric character: %c", c)
		}
	}

	// Two passwords should be different.
	p2, err := generateRandomPassword(32)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("two generated passwords are identical")
	}
}

func TestKeystoreVolumePath(t *testing.T) {
	cfg := &config.Config{
		DataDir: "/test/data",
	}
	path := KeystoreVolumePath(cfg, "my-agent")
	want := "/test/data/openclaw-my-agent/remote-signer-keystores"
	if path != want {
		t.Errorf("keystoreVolumePath = %q, want %q", path, want)
	}
}

func TestProvisionKeystoreToVolume(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{DataDir: tmpDir}

	keystoreJSON := []byte(`{"version": 3, "test": true}`)
	path, err := provisionKeystoreToVolume(cfg, "test-id", "my-uuid", keystoreJSON)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file exists.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keystore: %v", err)
	}
	if string(data) != string(keystoreJSON) {
		t.Error("keystore content mismatch")
	}

	// Verify path structure.
	wantPath := filepath.Join(tmpDir, "openclaw-test-id", "remote-signer-keystores", "my-uuid.json")
	if path != wantPath {
		t.Errorf("keystore path = %q, want %q", path, wantPath)
	}

	// Verify restrictive permissions on directory.
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("keystore dir permissions = %o, want 0700", info.Mode().Perm())
	}
}

func TestGenerateRemoteSignerValues(t *testing.T) {
	wallet := &WalletInfo{
		Address:      "0x1234567890abcdef1234567890abcdef12345678",
		KeystoreUUID: "test-uuid",
		Password:     "my-secret-password",
	}

	values := generateRemoteSignerValues(wallet)

	if !strings.Contains(values, `keystorePassword:`) {
		t.Error("values should contain keystorePassword section")
	}
	if !strings.Contains(values, `value: "my-secret-password"`) {
		t.Error("values should contain password value")
	}
	if !strings.Contains(values, "persistence:") {
		t.Error("values should contain persistence section")
	}
}

func TestWalletMetadataRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	wallet := &WalletInfo{
		Address:      "0xAbCd1234567890abcdef1234567890abcdef1234",
		KeystoreUUID: "test-uuid-123",
		KeystorePath: "/data/keystores/test.json",
		Password:     "should-not-serialize",
	}

	if err := WriteWalletMetadata(tmpDir, wallet); err != nil {
		t.Fatal(err)
	}

	recovered, err := ReadWalletMetadata(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if recovered.Address != wallet.Address {
		t.Errorf("address = %q, want %q", recovered.Address, wallet.Address)
	}
	if recovered.KeystoreUUID != wallet.KeystoreUUID {
		t.Errorf("UUID = %q, want %q", recovered.KeystoreUUID, wallet.KeystoreUUID)
	}
	// Password should NOT be in the serialized metadata.
	if recovered.Password != "" {
		t.Error("password should not be serialized in metadata")
	}
}
