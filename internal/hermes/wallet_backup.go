package hermes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// BackupWalletOptions holds options for `obol agent wallet backup`.
type BackupWalletOptions struct {
	Output      string
	Passphrase  string
	HasPassFlag bool
}

// RestoreWalletOptions holds options for `obol agent wallet restore`.
type RestoreWalletOptions struct {
	Input        string
	Passphrase   string
	HasPassFlag  bool
	Force        bool
	ApplyCluster bool
}

// BackupWalletCmd creates a backup of the Hermes instance's remote-signer
// wallet. The on-disk format is identical to OpenClaw's, so a Hermes backup
// can be restored into an OpenClaw instance and vice versa — instance
// names and namespace scoping are not part of the backup payload.
func BackupWalletCmd(cfg *config.Config, id string, opts BackupWalletOptions, u *ui.UI) error {
	deployDir := DeploymentPath(cfg, id)

	wallet, err := ReadWalletMetadata(deployDir)
	if err != nil {
		return fmt.Errorf("no wallet found for instance %q: %w", id, err)
	}

	keystorePath := filepath.Join(agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id), wallet.KeystoreUUID+".json")
	keystoreData, err := os.ReadFile(keystorePath)
	if err != nil {
		return fmt.Errorf("failed to read keystore file: %w", err)
	}

	password, err := walletbackup.ReadKeystorePassword(deployDir)
	if err != nil {
		return fmt.Errorf("failed to read keystore password: %w", err)
	}

	backup := &walletbackup.File{
		Version:  walletbackup.Version,
		Instance: id,
		Wallets: []walletbackup.Wallet{{
			Address:          wallet.Address,
			PublicKey:        wallet.PublicKey,
			KeystoreUUID:     wallet.KeystoreUUID,
			CreatedAt:        wallet.CreatedAt,
			Keystore:         json.RawMessage(keystoreData),
			KeystorePassword: password,
		}},
	}

	passphrase, err := walletbackup.PromptPassphrase(opts.Passphrase, opts.HasPassFlag, u)
	if err != nil {
		return err
	}

	payload, encrypted, err := walletbackup.Encode(backup, passphrase)
	if err != nil {
		return err
	}

	addrSuffix := wallet.Address
	if len(addrSuffix) > 8 {
		addrSuffix = addrSuffix[len(addrSuffix)-8:]
	}
	outputPath := opts.Output
	if outputPath == "" {
		ext := "json"
		if encrypted {
			ext = "enc"
		}
		outputPath = fmt.Sprintf("obol-wallet-backup-%s.%s", addrSuffix, ext)
	}

	if err := os.WriteFile(outputPath, payload, 0o600); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	u.Success("Wallet backup created")
	u.Detail("Address", wallet.Address)
	u.Detail("Output", outputPath)
	if encrypted {
		u.Detail("Encrypted", "yes (AES-256-GCM)")
	} else {
		u.Detail("Encrypted", "no")
		u.Warn("Backup contains unencrypted keystore password — store securely")
	}
	return nil
}

// RestoreWalletCmd restores a Hermes wallet from a backup file. Mirrors
// openclaw.RestoreWalletCmd, sharing the wire format via walletbackup.
func RestoreWalletCmd(cfg *config.Config, id string, opts RestoreWalletOptions, u *ui.UI) error {
	raw, err := os.ReadFile(opts.Input)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	passphrase := opts.Passphrase
	if walletbackup.IsEncrypted(raw) && !opts.HasPassFlag {
		passphrase, err = u.SecretInput("Backup passphrase")
		if err != nil {
			return fmt.Errorf("failed to read passphrase: %w", err)
		}
	}

	backup, err := walletbackup.Decode(raw, passphrase)
	if err != nil {
		return err
	}

	w := backup.Wallets[0]

	deployDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deployDir); os.IsNotExist(err) {
		return fmt.Errorf("instance %q not found — run 'obol agent new --runtime hermes --id %s' first", id, id)
	}

	existingWallet, _ := ReadWalletMetadata(deployDir)
	if existingWallet != nil && !opts.Force {
		return fmt.Errorf("instance %q already has a wallet (address: %s)\nUse --force to overwrite", id, existingWallet.Address)
	}

	keystoreDir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id)
	ensureVolumeWritableFn(cfg, keystoreDir, u)
	if err := os.MkdirAll(keystoreDir, 0o700); err != nil {
		return fmt.Errorf("failed to create keystore directory: %w", err)
	}

	keystorePath := filepath.Join(keystoreDir, w.KeystoreUUID+".json")
	if err := os.WriteFile(keystorePath, []byte(w.Keystore), 0o600); err != nil {
		return fmt.Errorf("failed to write keystore: %w", err)
	}
	fixRuntimeVolumeOwnershipFn(cfg, keystoreDir, u)

	walletInfo := &WalletInfo{
		Address:      w.Address,
		PublicKey:    w.PublicKey,
		KeystoreUUID: w.KeystoreUUID,
		KeystorePath: keystorePath,
		CreatedAt:    w.CreatedAt,
		Password:     w.KeystorePassword,
	}

	rsValues := generateRemoteSignerValues(walletInfo)
	if err := walletbackup.WriteValuesRemoteSigner(deployDir, rsValues); err != nil {
		return fmt.Errorf("failed to write values-remote-signer.yaml: %w", err)
	}
	if err := WriteWalletMetadata(deployDir, walletInfo); err != nil {
		return fmt.Errorf("failed to write wallet metadata: %w", err)
	}
	if err := archiveReplacedHermesKeystore(cfg, id, existingWallet, w.KeystoreUUID, u); err != nil {
		return fmt.Errorf("failed to archive replaced keystore: %w", err)
	}

	if opts.ApplyCluster {
		applyHermesKeystorePasswordSecret(cfg, id, w.KeystorePassword, u)
		restartHermesRemoteSignerFn(cfg, id, u)
	}

	u.Success("Wallet restored")
	u.Detail("Address", w.Address)
	u.Detail("Instance", id)
	return nil
}

// FindInstancesWithWallets returns Hermes instance IDs that have wallet
// metadata on disk. Used by purge prompts.
func FindInstancesWithWallets(cfg *config.Config) []string {
	ids, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if _, err := ReadWalletMetadata(DeploymentPath(cfg, id)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// applyHermesKeystorePasswordSecret applies the remote-signer keystore
// password Secret in the instance namespace. Best-effort; if the cluster is
// down the caller is expected to re-sync later.
func applyHermesKeystorePasswordSecret(cfg *config.Config, id, password string, u *ui.UI) {
	if password == "" {
		return
	}
	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "remote-signer-keystore-password",
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/component":  "remote-signer",
				"app.kubernetes.io/managed-by": "obol",
			},
		},
		"type": "Opaque",
		"stringData": map[string]string{
			"password": password,
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		u.Warnf("Could not marshal remote-signer password Secret: %v", err)
		return
	}

	kubectlBin, kubeconfig := kubectl.Paths(cfg)
	cmd := exec.Command(kubectlBin, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		u.Blank()
		u.Warnf("Could not update remote-signer password Secret (cluster may not be running)")
		u.Printf("Run 'obol agent sync %s' to apply changes to the cluster.", id)
	}
}
