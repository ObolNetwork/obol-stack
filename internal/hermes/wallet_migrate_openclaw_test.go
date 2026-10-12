package hermes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// TestRestoreWalletCmd_MigratesOpenClawBackup pins the OpenClaw → Hermes
// migration that `obol stack up` still points leftover OpenClaw users at
// (OpenClaw was removed in v0.16):
//
//	obol v0.15:  obol agent wallet backup --runtime openclaw <id> --file w.json
//	obol v0.16+: obol agent wallet restore --input w.json --force
//
// The fixture is built the way v0.15's openclaw.BackupWalletCmd did (same
// walletbackup.File shape), so the restore must keep the address and a
// decryptable keystore.
func TestRestoreWalletCmd_MigratesOpenClawBackup(t *testing.T) {
	for _, passphrase := range []string{"", "migrate-me"} {
		cfg, hermesID, hermesWallet := setupHermesBackupInstance(t, agentruntime.DefaultInstanceID)

		oc, err := keystore.GenerateInMemory()
		if err != nil {
			t.Fatal(err)
		}

		data, _, err := walletbackup.Encode(&walletbackup.File{
			Version:  walletbackup.Version,
			Instance: "legacy-claw",
			Wallets: []walletbackup.Wallet{{
				Address:          oc.Address,
				PublicKey:        oc.PublicKey,
				KeystoreUUID:     oc.KeystoreUUID,
				CreatedAt:        time.Now().UTC().Format(time.RFC3339),
				Keystore:         oc.KeystoreJSON,
				KeystorePassword: oc.Password,
			}},
		}, passphrase)
		if err != nil {
			t.Fatal(err)
		}

		backupPath := filepath.Join(t.TempDir(), "openclaw-wallet.json")
		if err := os.WriteFile(backupPath, data, 0o600); err != nil {
			t.Fatal(err)
		}

		// Restoring over the existing Hermes wallet needs --force.
		opts := RestoreWalletOptions{Input: backupPath, Passphrase: passphrase, HasPassFlag: true, Force: false, ApplyCluster: false}
		if err := RestoreWalletCmd(cfg, hermesID, opts, newTestUI()); err == nil {
			t.Fatal("restore over existing Hermes wallet without --force should fail")
		}

		opts.Force = true
		if err := RestoreWalletCmd(cfg, hermesID, opts, newTestUI()); err != nil {
			t.Fatalf("hermes restore: %v", err)
		}

		deployDir := DeploymentPath(cfg, hermesID)

		restored, err := ReadWalletMetadata(deployDir)
		if err != nil {
			t.Fatal(err)
		}

		if restored.Address != oc.Address || restored.Address == hermesWallet.Address {
			t.Fatalf("restored address = %s, want OpenClaw address %s", restored.Address, oc.Address)
		}

		password, err := walletbackup.ReadKeystorePassword(deployDir)
		if err != nil {
			t.Fatal(err)
		}

		ksPath := filepath.Join(agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, hermesID), restored.KeystoreUUID+".json")

		ksJSON, err := os.ReadFile(ksPath)
		if err != nil {
			t.Fatalf("restored keystore: %v", err)
		}

		if _, err := keystore.DecryptV3(ksJSON, password); err != nil {
			t.Fatalf("restored keystore does not decrypt with restored password: %v", err)
		}
	}
}
