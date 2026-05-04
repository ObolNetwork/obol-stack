package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

func setupHermesBackupInstance(t *testing.T, id string) (*config.Config, string, *WalletInfo) {
	t.Helper()
	cfg, deployDir := walletImportTestConfig(t, id)
	stubVolumeOwnership(t)

	keystorePath := filepath.Join(agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id), "test-keystore-"+id+".json")
	wallet := &WalletInfo{
		Address:      "0x1111111111111111111111111111111111111111",
		PublicKey:    "0x04abc",
		KeystoreUUID: "test-keystore-" + id,
		KeystorePath: keystorePath,
		CreatedAt:    "2026-05-04T00:00:00Z",
		Password:     "password-" + id,
	}
	if err := os.WriteFile(keystorePath, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	if err := WriteWalletMetadata(deployDir, wallet); err != nil {
		t.Fatalf("write wallet metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "values-remote-signer.yaml"), []byte(generateRemoteSignerValues(wallet)), 0o600); err != nil {
		t.Fatalf("write remote signer values: %v", err)
	}

	return cfg, id, wallet
}

func TestBackupRestoreWalletCmd_HermesRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		passphrase string
	}{
		{name: "plain"},
		{name: "encrypted", passphrase: "correct horse battery staple"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, id, original := setupHermesBackupInstance(t, "source-"+tt.name)

			backupPath := filepath.Join(t.TempDir(), "backup")
			if tt.passphrase != "" {
				backupPath += ".enc"
			} else {
				backupPath += ".json"
			}

			if err := BackupWalletCmd(cfg, id, BackupWalletOptions{
				Output:      backupPath,
				Passphrase:  tt.passphrase,
				HasPassFlag: true,
			}, newTestUI()); err != nil {
				t.Fatalf("backup: %v", err)
			}

			payload, err := os.ReadFile(backupPath)
			if err != nil {
				t.Fatalf("read backup: %v", err)
			}
			if gotEncrypted := walletbackup.IsEncrypted(payload); gotEncrypted != (tt.passphrase != "") {
				t.Fatalf("encrypted = %v, want %v", gotEncrypted, tt.passphrase != "")
			}
			decoded, err := walletbackup.Decode(payload, tt.passphrase)
			if err != nil {
				t.Fatalf("decode backup: %v", err)
			}
			if decoded.Instance != id {
				t.Fatalf("backup instance = %q, want %q", decoded.Instance, id)
			}
			if len(decoded.Wallets) != 1 || decoded.Wallets[0].Address != original.Address {
				t.Fatalf("backup wallet = %+v, want address %s", decoded.Wallets, original.Address)
			}

			restoreID := "restore-" + tt.name
			restoreDir := DeploymentPath(cfg, restoreID)
			if err := os.MkdirAll(restoreDir, 0o755); err != nil {
				t.Fatalf("mkdir restore dir: %v", err)
			}

			if err := RestoreWalletCmd(cfg, restoreID, RestoreWalletOptions{
				Input:       backupPath,
				Passphrase:  tt.passphrase,
				HasPassFlag: true,
			}, newTestUI()); err != nil {
				t.Fatalf("restore: %v", err)
			}

			restored, err := ReadWalletMetadata(restoreDir)
			if err != nil {
				t.Fatalf("read restored metadata: %v", err)
			}
			if restored.Address != original.Address {
				t.Fatalf("restored address = %q, want %q", restored.Address, original.Address)
			}
			if restored.KeystoreUUID != original.KeystoreUUID {
				t.Fatalf("restored keystore UUID = %q, want %q", restored.KeystoreUUID, original.KeystoreUUID)
			}

			keystorePath := filepath.Join(agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, restoreID), original.KeystoreUUID+".json")
			if _, err := os.Stat(keystorePath); err != nil {
				t.Fatalf("restored keystore missing: %v", err)
			}

			password, err := walletbackup.ReadKeystorePassword(restoreDir)
			if err != nil {
				t.Fatalf("read restored keystore password: %v", err)
			}
			if password != original.Password {
				t.Fatalf("restored password = %q, want original password", password)
			}
		})
	}
}

func TestRestoreWalletCmd_HermesRequiresForceForExistingWallet(t *testing.T) {
	cfg, id, _ := setupHermesBackupInstance(t, "existing")

	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if err := BackupWalletCmd(cfg, id, BackupWalletOptions{
		Output:      backupPath,
		HasPassFlag: true,
	}, newTestUI()); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if err := RestoreWalletCmd(cfg, id, RestoreWalletOptions{
		Input:       backupPath,
		HasPassFlag: true,
		Force:       false,
	}, newTestUI()); err == nil {
		t.Fatal("expected restore over existing Hermes wallet to require force")
	}

	if err := RestoreWalletCmd(cfg, id, RestoreWalletOptions{
		Input:       backupPath,
		HasPassFlag: true,
		Force:       true,
	}, newTestUI()); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
}

func TestRestoreWalletCmd_ApplyClusterPublishesWalletMetadataBeforeRestart(t *testing.T) {
	const id = "obol-agent"

	cfg, deployDir := walletImportTestConfig(t, id)
	stubVolumeOwnership(t)

	var calls []string
	origApply := applyWalletMetadataConfigMapFn
	origRestart := restartHermesRemoteSignerFn
	t.Cleanup(func() {
		applyWalletMetadataConfigMapFn = origApply
		restartHermesRemoteSignerFn = origRestart
	})

	applyWalletMetadataConfigMapFn = func(_ *config.Config, gotID, gotDeployDir string) {
		if gotID != id {
			t.Fatalf("applyWalletMetadataConfigMap id = %q, want %q", gotID, id)
		}
		if gotDeployDir != deployDir {
			t.Fatalf("applyWalletMetadataConfigMap deployDir = %q, want %q", gotDeployDir, deployDir)
		}
		calls = append(calls, "wallet-metadata")
	}
	restartHermesRemoteSignerFn = func(_ *config.Config, _ string, _ *ui.UI) {
		calls = append(calls, "restart")
	}

	backup := &walletbackup.File{
		Version:  walletbackup.Version,
		Instance: "source",
		Wallets: []walletbackup.Wallet{{
			Address:          "0x1111111111111111111111111111111111111111",
			PublicKey:        "0x04abc",
			KeystoreUUID:     "restored-wallet",
			CreatedAt:        "2026-05-04T00:00:00Z",
			Keystore:         json.RawMessage(`{"version":3}`),
			KeystorePassword: "secret",
		}},
	}
	payload, _, err := walletbackup.Encode(backup, "")
	if err != nil {
		t.Fatalf("encode backup: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(backupPath, payload, 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := RestoreWalletCmd(cfg, id, RestoreWalletOptions{
		Input:        backupPath,
		Force:        true,
		ApplyCluster: true,
	}, newTestUI()); err != nil {
		t.Fatalf("restore: %v", err)
	}

	want := []string{"wallet-metadata", "restart"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("cluster apply calls = %v, want %v", calls, want)
	}
}
