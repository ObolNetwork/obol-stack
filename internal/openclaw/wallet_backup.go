package openclaw

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// BackupFile is re-exported from walletbackup for backwards-compatibility
// with existing OpenClaw callers. Both runtimes share the same on-disk shape.
type BackupFile = walletbackup.File

// BackupWallet is re-exported from walletbackup so the OpenClaw subcommand
// surface stays unchanged.
type BackupWallet = walletbackup.Wallet

// BackupWalletOptions holds options for the backup command.
type BackupWalletOptions struct {
	Output      string // Output file path (empty = auto-generate)
	Passphrase  string // Encryption passphrase (empty = no encryption)
	HasPassFlag bool   // Whether --passphrase was explicitly set
}

// RestoreWalletOptions holds options for the restore command.
type RestoreWalletOptions struct {
	Input        string // Input file path
	Passphrase   string // Decryption passphrase
	HasPassFlag  bool   // Whether --passphrase was explicitly set
	Force        bool   // Overwrite existing wallet
	ApplyCluster bool   // Update live cluster resources and restart remote-signer
}

// ImportPrivateKeyWalletOptions holds options for importing a raw private key.
type ImportPrivateKeyWalletOptions struct {
	PrivateKeyFile string // File containing a 0x-prefixed private key
	Force          bool   // Overwrite existing wallet
	ApplyCluster   bool   // Update live cluster resources and restart remote-signer
}

// BackupWallet creates a backup of the wallet for the given instance.
func BackupWalletCmd(cfg *config.Config, id string, opts BackupWalletOptions, u *ui.UI) error {
	deployDir := DeploymentPath(cfg, id)

	// Read wallet metadata.
	wallet, err := ReadWalletMetadata(deployDir)
	if err != nil {
		return fmt.Errorf("no wallet found for instance %q: %w", id, err)
	}

	// Read keystore JSON.
	keystorePath := filepath.Join(KeystoreVolumePath(cfg, id), wallet.KeystoreUUID+".json")

	keystoreData, err := os.ReadFile(keystorePath)
	if err != nil {
		return fmt.Errorf("failed to read keystore file: %w", err)
	}

	// Read keystore password from values-remote-signer.yaml.
	password, err := readKeystorePassword(deployDir)
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

// ImportPrivateKeyWalletCmd imports an existing private key as an OpenClaw
// remote-signer wallet.
func ImportPrivateKeyWalletCmd(cfg *config.Config, id string, opts ImportPrivateKeyWalletOptions, u *ui.UI) error {
	raw, err := os.ReadFile(opts.PrivateKeyFile)
	if err != nil {
		return fmt.Errorf("failed to read private key file: %w", err)
	}

	privateKeyHex := strings.TrimSpace(string(raw))
	if privateKeyHex == "" {
		return errors.New("private key file is empty")
	}

	deployDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deployDir); os.IsNotExist(err) {
		return fmt.Errorf("instance %q not found — run 'obol openclaw onboard --id %s' first", id, id)
	}

	existingWallet, _ := ReadWalletMetadata(deployDir)
	if existingWallet != nil && !opts.Force {
		return fmt.Errorf("instance %q already has a wallet (address: %s)\nUse --force to overwrite", id, existingWallet.Address)
	}

	wallet, err := ImportWalletFromPrivateKey(cfg, id, privateKeyHex, u)
	if err != nil {
		return err
	}

	if err := finalizeWalletProvision(cfg, id, deployDir, existingWallet, wallet, wallet.Password, opts.ApplyCluster, u); err != nil {
		return err
	}

	u.Success("Wallet imported")
	u.Detail("Address", wallet.Address)
	u.Detail("Instance", id)

	return nil
}

// RestoreWalletCmd restores a wallet from a backup file.
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
		return fmt.Errorf("instance %q not found — run 'obol openclaw onboard --id %s' first", id, id)
	}

	// Check for existing wallet.
	existingWallet, _ := ReadWalletMetadata(deployDir)
	if existingWallet != nil && !opts.Force {
		return fmt.Errorf("instance %q already has a wallet (address: %s)\nUse --force to overwrite", id, existingWallet.Address)
	}

	// Write keystore file.
	keystoreDir := KeystoreVolumePath(cfg, id)
	ensureVolumeWritable(cfg, keystoreDir, u)
	if err := os.MkdirAll(keystoreDir, 0o700); err != nil {
		return fmt.Errorf("failed to create keystore directory: %w", err)
	}

	keystorePath := filepath.Join(keystoreDir, w.KeystoreUUID+".json")
	if err := os.WriteFile(keystorePath, []byte(w.Keystore), 0o600); err != nil {
		return fmt.Errorf("failed to write keystore: %w", err)
	}
	fixVolumeOwnership(cfg, keystoreDir, u)

	// Update wallet.json metadata.
	walletInfo := &WalletInfo{
		Address:      w.Address,
		PublicKey:    w.PublicKey,
		KeystoreUUID: w.KeystoreUUID,
		KeystorePath: keystorePath,
		CreatedAt:    w.CreatedAt,
	}
	if err := finalizeWalletProvision(cfg, id, deployDir, existingWallet, walletInfo, w.KeystorePassword, opts.ApplyCluster, u); err != nil {
		return err
	}

	u.Success("Wallet restored")
	u.Detail("Address", w.Address)
	u.Detail("Instance", id)

	return nil
}

// ListWallets displays wallet information for one or all instances.
func ListWallets(cfg *config.Config, id string, u *ui.UI) error {
	var ids []string

	if id != "" {
		ids = []string{id}
	} else {
		var err error

		ids, err = ListInstanceIDs(cfg)
		if err != nil {
			return err
		}

		if len(ids) == 0 {
			u.Info("No OpenClaw instances found")
			return nil
		}
	}

	found := false

	for _, instanceID := range ids {
		deployDir := DeploymentPath(cfg, instanceID)

		wallet, err := ReadWalletMetadata(deployDir)
		if err != nil {
			continue
		}

		found = true

		u.Detail("Instance", instanceID)
		u.Detail("  Address", wallet.Address)
		u.Detail("  Keystore UUID", wallet.KeystoreUUID)

		if wallet.CreatedAt != "" {
			u.Detail("  Created", wallet.CreatedAt)
		}

		u.Blank()
	}

	if !found {
		u.Info("No wallets found")
	}

	return nil
}

// FindInstancesWithWallets returns instance IDs that have wallet metadata.
func FindInstancesWithWallets(cfg *config.Config) []string {
	ids, err := ListInstanceIDs(cfg)
	if err != nil {
		return nil
	}

	var result []string

	for _, id := range ids {
		deployDir := DeploymentPath(cfg, id)
		if _, err := ReadWalletMetadata(deployDir); err == nil {
			result = append(result, id)
		}
	}

	return result
}

func restartRemoteSigner(cfg *config.Config, id string, u *ui.UI) {
	// Best-effort: the cluster may not be running when a wallet is pre-seeded.
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubectlBin, kubeconfig := kubectl.Paths(cfg)
	if err := kubectl.RunSilent(kubectlBin, kubeconfig,
		"rollout", "restart", "deployment/remote-signer", "-n", namespace,
	); err != nil {
		u.Blank()
		u.Warnf("Could not restart remote-signer (cluster may not be running)")
		u.Printf("Run 'obol openclaw sync %s' to apply changes to the cluster.", id)
	} else {
		u.Success("Remote-signer restarted")
	}
}

func finalizeWalletProvision(cfg *config.Config, id, deployDir string, existingWallet, wallet *WalletInfo, password string, applyCluster bool, u *ui.UI) error {
	if err := writeKeystorePassword(deployDir, password); err != nil {
		return fmt.Errorf("failed to write keystore password: %w", err)
	}
	if err := WriteWalletMetadata(deployDir, wallet); err != nil {
		return fmt.Errorf("failed to write wallet metadata: %w", err)
	}
	if err := archiveReplacedKeystore(cfg, id, existingWallet, wallet.KeystoreUUID, u); err != nil {
		return fmt.Errorf("failed to archive replaced keystore: %w", err)
	}
	if !applyCluster {
		return nil
	}

	applyWalletMetadataConfigMap(cfg, id, deployDir)
	applyKeystorePasswordSecret(cfg, id, password, u)
	restartRemoteSigner(cfg, id, u)
	return nil
}

func archiveReplacedKeystore(cfg *config.Config, id string, existingWallet *WalletInfo, keepUUID string, u *ui.UI) error {
	if existingWallet == nil || existingWallet.KeystoreUUID == "" || existingWallet.KeystoreUUID == keepUUID {
		return nil
	}

	dir := KeystoreVolumePath(cfg, id)
	oldPath := filepath.Join(dir, existingWallet.KeystoreUUID+".json")
	if _, err := os.Stat(oldPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	archiveDir := filepath.Join(dir, "replaced")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return err
	}
	archivePath := filepath.Join(
		archiveDir,
		fmt.Sprintf("%s-%s.json", existingWallet.KeystoreUUID, time.Now().UTC().Format("20060102T150405Z")),
	)
	if err := os.Rename(oldPath, archivePath); err != nil {
		return err
	}
	if u != nil {
		u.Warnf("Archived replaced keystore instead of deleting it: %s", archivePath)
	}
	return nil
}

func applyKeystorePasswordSecret(cfg *config.Config, id, password string, u *ui.UI) {
	if password == "" {
		return
	}

	raw, err := keystorePasswordSecretManifest(id, password)
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
		u.Printf("Run 'obol openclaw sync %s' to apply changes to the cluster.", id)
	}
}

func keystorePasswordSecretManifest(id, password string) ([]byte, error) {
	namespace := fmt.Sprintf("%s-%s", appName, id)
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

	return json.Marshal(manifest)
}

// resolvePassphrase delegates to walletbackup.PromptPassphrase. Kept as a
// package-private wrapper so existing OpenClaw call sites stay compact.
func resolvePassphrase(flagValue string, hasFlag bool, u *ui.UI) (string, error) {
	return walletbackup.PromptPassphrase(flagValue, hasFlag, u)
}

// readKeystorePassword delegates to walletbackup.ReadKeystorePassword.
func readKeystorePassword(deployDir string) (string, error) {
	return walletbackup.ReadKeystorePassword(deployDir)
}

// writeKeystorePassword renders the remote-signer values YAML for the given
// password and writes it under deployDir.
func writeKeystorePassword(deployDir, password string) error {
	content := generateRemoteSignerValues(&WalletInfo{Password: password})
	return walletbackup.WriteValuesRemoteSigner(deployDir, content)
}

// Compat shims for tests in this package that exercise the crypto envelope
// directly. New code should call walletbackup.Encrypt/Decrypt/IsEncrypted.
func isEncryptedBackup(data []byte) bool { return walletbackup.IsEncrypted(data) }

func encryptBackup(plaintext []byte, passphrase string) ([]byte, error) {
	return walletbackup.Encrypt(plaintext, passphrase)
}

func decryptBackup(data []byte, passphrase string) ([]byte, error) {
	return walletbackup.Decrypt(data, passphrase)
}

// walletAddressesForPurgeWarning returns addresses of wallets that would be lost.
func walletAddressesForPurgeWarning(cfg *config.Config) []string {
	ids := FindInstancesWithWallets(cfg)

	var addresses []string

	for _, id := range ids {
		deployDir := DeploymentPath(cfg, id)

		w, err := ReadWalletMetadata(deployDir)
		if err == nil {
			addresses = append(addresses, fmt.Sprintf("  %s (instance: %s)", w.Address, id))
		}
	}

	return addresses
}

// PromptBackupBeforePurge checks for wallets and offers to back them up.
// Non-interactive (piped/scripted): prints a warning but does not block.
func PromptBackupBeforePurge(cfg *config.Config, u *ui.UI) {
	ids := FindInstancesWithWallets(cfg)
	if len(ids) == 0 {
		return
	}

	addresses := walletAddressesForPurgeWarning(cfg)

	u.Warn("The following wallets will be destroyed:")

	for _, a := range addresses {
		u.Print(a)
	}

	// Non-interactive: warn but don't block scripts.
	if !u.IsTTY() {
		u.Warn("Run 'obol openclaw wallet backup' first to save wallet keys")
		return
	}

	u.Blank()

	if !u.Confirm("Back up wallets before purging?", true) {
		return
	}

	// Get a single passphrase for all backups.
	passphrase, err := resolvePassphrase("", false, u)
	if err != nil {
		u.Warnf("Failed to get passphrase: %v", err)
		return
	}

	for _, id := range ids {
		err := BackupWalletCmd(cfg, id, BackupWalletOptions{
			Passphrase:  passphrase,
			HasPassFlag: true,
		}, u)
		if err != nil {
			u.Warnf("Failed to backup wallet for instance %s: %v", id, err)
		}
	}

	u.Blank()
}
