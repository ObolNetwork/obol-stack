package hermes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// TestRestoreWalletCmd_MigratesOpenClawBackup pins the OpenClaw → Hermes
// migration path advertised by the v0.15 deprecation warning:
//
//	obol agent wallet backup --runtime openclaw <id> --file w.json
//	obol agent wallet restore --runtime hermes --input w.json --force
//
// An OpenClaw wallet backup must restore into an existing Hermes instance
// (replacing its wallet) and keep the same address + decryptable keystore.
// Delete together with internal/openclaw in v0.16.
func TestRestoreWalletCmd_MigratesOpenClawBackup(t *testing.T) {
	for _, passphrase := range []string{"", "migrate-me"} {
		cfg, hermesID, hermesWallet := setupHermesBackupInstance(t, agentruntime.DefaultInstanceID)

		// Source: a real OpenClaw instance wallet in the same data dir.
		const ocID = "legacy-claw"
		if err := os.MkdirAll(openclaw.DeploymentPath(cfg, ocID), 0o755); err != nil {
			t.Fatal(err)
		}

		ocWallet, err := openclaw.GenerateWallet(cfg, ocID, newTestUI())
		if err != nil {
			t.Fatalf("openclaw wallet: %v", err)
		}

		if err := openclaw.WriteWalletMetadata(openclaw.DeploymentPath(cfg, ocID), ocWallet); err != nil {
			t.Fatal(err)
		}

		values := "keystorePassword:\n  value: \"" + ocWallet.Password + "\"\n"
		if err := os.WriteFile(filepath.Join(openclaw.DeploymentPath(cfg, ocID), "values-remote-signer.yaml"), []byte(values), 0o600); err != nil {
			t.Fatal(err)
		}

		backupPath := filepath.Join(t.TempDir(), "openclaw-wallet.json")
		if err := openclaw.BackupWalletCmd(cfg, ocID, openclaw.BackupWalletOptions{
			Output: backupPath, Passphrase: passphrase, HasPassFlag: true,
		}, newTestUI()); err != nil {
			t.Fatalf("openclaw backup: %v", err)
		}

		// Restoring over the existing Hermes wallet needs --force.
		opts := RestoreWalletOptions{Input: backupPath, Passphrase: passphrase, HasPassFlag: true}
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

		if restored.Address != ocWallet.Address || restored.Address == hermesWallet.Address {
			t.Fatalf("restored address = %s, want OpenClaw address %s", restored.Address, ocWallet.Address)
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
