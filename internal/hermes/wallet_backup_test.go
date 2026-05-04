package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

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
