package openclaw

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
)

func TestImportWalletFromPrivateKey(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{DataDir: tmpDir}

	keyHex := "0x0000000000000000000000000000000000000000000000000000000000000001"
	wallet, err := ImportWalletFromPrivateKey(cfg, "imported", keyHex, testUI())
	if err != nil {
		t.Fatalf("import wallet: %v", err)
	}

	wantAddr := "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"
	if wallet.Address != wantAddr {
		t.Errorf("address = %q, want %q", wallet.Address, wantAddr)
	}

	if !strings.HasPrefix(wallet.PublicKey, "0x04") || len(wallet.PublicKey) != 132 {
		t.Errorf("public key should be uncompressed 0x04-prefixed key, got %q", wallet.PublicKey)
	}

	keystoreJSON, err := os.ReadFile(wallet.KeystorePath)
	if err != nil {
		t.Fatalf("read keystore: %v", err)
	}

	recovered, err := keystore.DecryptV3(keystoreJSON, wallet.Password)
	if err != nil {
		t.Fatalf("decrypt imported keystore: %v", err)
	}

	if got := "0x" + hex.EncodeToString(recovered); got != keyHex {
		t.Errorf("recovered key = %q, want %q", got, keyHex)
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
	path, err := provisionKeystoreToVolume(cfg, "test-id", "my-uuid", keystoreJSON, testUI())
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

	if info.Mode().Perm() != 0o700 {
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

	if !strings.Contains(values, "podSecurityContext:") {
		t.Error("values should contain podSecurityContext section")
	}

	if !strings.Contains(values, "fsGroup: 1000") {
		t.Error("values should set fsGroup to 1000 for remote-signer PVC write access")
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
