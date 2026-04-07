package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// setupTestInstance creates a minimal instance structure for testing.
func setupTestInstance(t *testing.T) (*config.Config, string, *WalletInfo) {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: filepath.Join(tmpDir, "config"),
		DataDir:   filepath.Join(tmpDir, "data"),
	}

	id := "test-instance"

	deployDir := DeploymentPath(cfg, id)
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Generate a wallet.
	wallet, err := GenerateWallet(cfg, id, testUI())
	if err != nil {
		t.Fatal(err)
	}

	// Write metadata.
	if err := WriteWalletMetadata(deployDir, wallet); err != nil {
		t.Fatal(err)
	}

	// Write values-remote-signer.yaml.
	values := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(filepath.Join(deployDir, "values-remote-signer.yaml"), []byte(values), 0o644); err != nil {
		t.Fatal(err)
	}

	return cfg, id, wallet
}

func TestBackupRestorePlainRoundTrip(t *testing.T) {
	cfg, id, origWallet := setupTestInstance(t)

	// Backup (no encryption).
	backupPath := filepath.Join(t.TempDir(), "backup.json")
	u := testUI()

	err := BackupWalletCmd(cfg, id, BackupWalletOptions{
		Output:      backupPath,
		Passphrase:  "",
		HasPassFlag: true, // skip prompt
	}, u)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Verify backup file is valid JSON.
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}

	var backup BackupFile
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatalf("backup is not valid JSON: %v", err)
	}

	if backup.Version != 1 {
		t.Errorf("version = %d, want 1", backup.Version)
	}

	if len(backup.Wallets) != 1 {
		t.Fatalf("wallets count = %d, want 1", len(backup.Wallets))
	}

	if backup.Wallets[0].Address != origWallet.Address {
		t.Errorf("address = %q, want %q", backup.Wallets[0].Address, origWallet.Address)
	}

	// Create a new instance to restore into.
	restoreID := "restore-instance"

	restoreDir := DeploymentPath(cfg, restoreID)
	if err := os.MkdirAll(restoreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a dummy values-remote-signer.yaml so deployment looks valid.
	if err := os.WriteFile(filepath.Join(restoreDir, "values-remote-signer.yaml"), []byte("dummy: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restore.
	err = RestoreWalletCmd(cfg, restoreID, RestoreWalletOptions{
		Input:       backupPath,
		Passphrase:  "",
		HasPassFlag: true,
		Force:       false,
	}, u)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Verify restored wallet metadata.
	restored, err := ReadWalletMetadata(restoreDir)
	if err != nil {
		t.Fatal(err)
	}

	if restored.Address != origWallet.Address {
		t.Errorf("restored address = %q, want %q", restored.Address, origWallet.Address)
	}

	if restored.KeystoreUUID != origWallet.KeystoreUUID {
		t.Errorf("restored UUID = %q, want %q", restored.KeystoreUUID, origWallet.KeystoreUUID)
	}

	// Verify restored keystore file exists.
	keystorePath := filepath.Join(KeystoreVolumePath(cfg, restoreID), origWallet.KeystoreUUID+".json")
	if _, err := os.Stat(keystorePath); os.IsNotExist(err) {
		t.Error("restored keystore file does not exist")
	}

	// Verify restored password.
	restoredPwd, err := readKeystorePassword(restoreDir)
	if err != nil {
		t.Fatal(err)
	}

	if restoredPwd != origWallet.Password {
		t.Errorf("restored password = %q, want %q", restoredPwd, origWallet.Password)
	}
}

func TestBackupRestoreEncryptedRoundTrip(t *testing.T) {
	cfg, id, origWallet := setupTestInstance(t)

	backupPath := filepath.Join(t.TempDir(), "backup.enc")
	passphrase := "test-secure-passphrase-123"
	u := testUI()

	// Backup with encryption.
	err := BackupWalletCmd(cfg, id, BackupWalletOptions{
		Output:      backupPath,
		Passphrase:  passphrase,
		HasPassFlag: true,
	}, u)
	if err != nil {
		t.Fatalf("encrypted backup: %v", err)
	}

	// Verify the file is encrypted (starts with OBOL magic).
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}

	if !isEncryptedBackup(data) {
		t.Error("backup file should be encrypted")
	}

	// Restore.
	restoreID := "restore-enc"

	restoreDir := DeploymentPath(cfg, restoreID)
	if err := os.MkdirAll(restoreDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(restoreDir, "values-remote-signer.yaml"), []byte("dummy: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = RestoreWalletCmd(cfg, restoreID, RestoreWalletOptions{
		Input:       backupPath,
		Passphrase:  passphrase,
		HasPassFlag: true,
	}, u)
	if err != nil {
		t.Fatalf("encrypted restore: %v", err)
	}

	// Verify.
	restored, err := ReadWalletMetadata(restoreDir)
	if err != nil {
		t.Fatal(err)
	}

	if restored.Address != origWallet.Address {
		t.Errorf("address = %q, want %q", restored.Address, origWallet.Address)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	plaintext := []byte(`{"version":1,"instance":"test","wallets":[]}`)

	encrypted, err := encryptBackup(plaintext, "correct-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	_, err = decryptBackup(encrypted, "wrong-passphrase")
	if err == nil {
		t.Error("expected error with wrong passphrase")
	}
}

func TestEncryptDecryptBackupRoundTrip(t *testing.T) {
	plaintext := []byte(`{"version":1,"instance":"test","wallets":[{"address":"0x1234"}]}`)
	passphrase := "my-secret"

	encrypted, err := encryptBackup(plaintext, passphrase)
	if err != nil {
		t.Fatal(err)
	}

	if !isEncryptedBackup(encrypted) {
		t.Error("encrypted data should start with OBOL magic")
	}

	decrypted, err := decryptBackup(encrypted, passphrase)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestIsEncryptedBackup(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"encrypted", []byte("OBOL\x01rest-of-data"), true},
		{"plain json", []byte(`{"version": 1}`), false},
		{"empty", []byte{}, false},
		{"short", []byte("OBO"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEncryptedBackup(tt.data); got != tt.want {
				t.Errorf("isEncryptedBackup = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCorruptEncryptedFile(t *testing.T) {
	plaintext := []byte(`{"version":1}`)

	encrypted, err := encryptBackup(plaintext, "pass")
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the ciphertext (last byte).
	encrypted[len(encrypted)-1] ^= 0xFF

	_, err = decryptBackup(encrypted, "pass")
	if err == nil {
		t.Error("expected error with corrupted ciphertext")
	}
}

func TestRestoreRequiresForceForExisting(t *testing.T) {
	cfg, id, _ := setupTestInstance(t)

	// Create a backup first.
	backupPath := filepath.Join(t.TempDir(), "backup.json")

	u := testUI()
	if err := BackupWalletCmd(cfg, id, BackupWalletOptions{
		Output:      backupPath,
		Passphrase:  "",
		HasPassFlag: true,
	}, u); err != nil {
		t.Fatal(err)
	}

	// Try to restore over existing wallet without --force.
	err := RestoreWalletCmd(cfg, id, RestoreWalletOptions{
		Input:       backupPath,
		Passphrase:  "",
		HasPassFlag: true,
		Force:       false,
	}, u)
	if err == nil {
		t.Error("expected error when restoring over existing wallet without --force")
	}

	// With --force should succeed.
	err = RestoreWalletCmd(cfg, id, RestoreWalletOptions{
		Input:       backupPath,
		Passphrase:  "",
		HasPassFlag: true,
		Force:       true,
	}, u)
	if err != nil {
		t.Fatalf("restore with --force: %v", err)
	}
}

func TestRestoreInvalidVersion(t *testing.T) {
	backup := `{"version":99,"instance":"test","wallets":[{"address":"0x1234"}]}`

	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(backupPath, []byte(backup), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: filepath.Join(tmpDir, "config"),
		DataDir:   filepath.Join(tmpDir, "data"),
	}

	deployDir := DeploymentPath(cfg, "test")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}

	u := testUI()

	err := RestoreWalletCmd(cfg, "test", RestoreWalletOptions{
		Input:       backupPath,
		Passphrase:  "",
		HasPassFlag: true,
	}, u)
	if err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestReadKeystorePassword(t *testing.T) {
	tmpDir := t.TempDir()

	yaml := `# Remote-signer configuration
keystorePassword:
  value: "my-password-123"

persistence:
  enabled: true
`
	if err := os.WriteFile(filepath.Join(tmpDir, "values-remote-signer.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	pwd, err := readKeystorePassword(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if pwd != "my-password-123" {
		t.Errorf("password = %q, want %q", pwd, "my-password-123")
	}
}

func TestFindInstancesWithWallets(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: filepath.Join(tmpDir, "config"),
		DataDir:   filepath.Join(tmpDir, "data"),
	}

	// Create two instances, one with wallet, one without.
	for _, id := range []string{"with-wallet", "no-wallet"} {
		dir := DeploymentPath(cfg, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	wallet := &WalletInfo{
		Address:      "0x1234",
		KeystoreUUID: "uuid-123",
	}
	if err := WriteWalletMetadata(DeploymentPath(cfg, "with-wallet"), wallet); err != nil {
		t.Fatal(err)
	}

	ids := FindInstancesWithWallets(cfg)
	if len(ids) != 1 {
		t.Fatalf("expected 1 instance with wallet, got %d", len(ids))
	}

	if ids[0] != "with-wallet" {
		t.Errorf("instance = %q, want %q", ids[0], "with-wallet")
	}
}

// testUI creates a UI for testing.
func testUI() *ui.UI {
	return ui.New(false)
}
