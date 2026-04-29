package openclaw

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
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
	"golang.org/x/crypto/scrypt"
	"gopkg.in/yaml.v3"
)

// backupMagic is the first 4 bytes of an encrypted backup file.
var backupMagic = []byte("OBOL")

const backupVersion byte = 1

// BackupFile is the JSON structure of a wallet backup.
type BackupFile struct {
	Version  int            `json:"version"`
	Instance string         `json:"instance"`
	Wallets  []BackupWallet `json:"wallets"`
}

// BackupWallet holds a single wallet's backup data.
type BackupWallet struct {
	Address          string          `json:"address"`
	PublicKey        string          `json:"publicKey"`
	KeystoreUUID     string          `json:"keystoreUUID"`
	CreatedAt        string          `json:"createdAt"`
	Keystore         json.RawMessage `json:"keystore"`
	KeystorePassword string          `json:"keystorePassword"`
}

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

	// Build backup structure.
	backup := BackupFile{
		Version:  1,
		Instance: id,
		Wallets: []BackupWallet{
			{
				Address:          wallet.Address,
				PublicKey:        wallet.PublicKey,
				KeystoreUUID:     wallet.KeystoreUUID,
				CreatedAt:        wallet.CreatedAt,
				Keystore:         json.RawMessage(keystoreData),
				KeystorePassword: password,
			},
		},
	}

	backupJSON, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal backup: %w", err)
	}

	// Determine passphrase.
	passphrase, err := resolvePassphrase(opts.Passphrase, opts.HasPassFlag, u)
	if err != nil {
		return err
	}

	// Determine output path and write.
	addrSuffix := wallet.Address
	if len(addrSuffix) > 8 {
		addrSuffix = addrSuffix[len(addrSuffix)-8:]
	}

	outputPath := opts.Output
	encrypted := passphrase != ""

	if outputPath == "" {
		if encrypted {
			outputPath = fmt.Sprintf("obol-wallet-backup-%s.enc", addrSuffix)
		} else {
			outputPath = fmt.Sprintf("obol-wallet-backup-%s.json", addrSuffix)
		}
	}

	if encrypted {
		ciphertext, err := encryptBackup(backupJSON, passphrase)
		if err != nil {
			return fmt.Errorf("encryption failed: %w", err)
		}

		if err := os.WriteFile(outputPath, ciphertext, 0o600); err != nil {
			return fmt.Errorf("failed to write backup: %w", err)
		}
	} else {
		if err := os.WriteFile(outputPath, backupJSON, 0o600); err != nil {
			return fmt.Errorf("failed to write backup: %w", err)
		}
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
	// Read backup file.
	raw, err := os.ReadFile(opts.Input)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// Detect format and decrypt if needed.
	var backupJSON []byte

	if isEncryptedBackup(raw) {
		passphrase := opts.Passphrase
		if !opts.HasPassFlag {
			passphrase, err = u.SecretInput("Backup passphrase")
			if err != nil {
				return fmt.Errorf("failed to read passphrase: %w", err)
			}
		}

		if passphrase == "" {
			return errors.New("passphrase required for encrypted backup")
		}

		backupJSON, err = decryptBackup(raw, passphrase)
		if err != nil {
			return fmt.Errorf("decryption failed (wrong passphrase?): %w", err)
		}
	} else {
		backupJSON = raw
	}

	// Parse backup.
	var backup BackupFile
	if err := json.Unmarshal(backupJSON, &backup); err != nil {
		return fmt.Errorf("invalid backup file: %w", err)
	}

	if backup.Version != 1 {
		return fmt.Errorf("unsupported backup version %d (expected 1)", backup.Version)
	}

	if len(backup.Wallets) == 0 {
		return errors.New("backup contains no wallets")
	}

	w := backup.Wallets[0]

	// Verify deployment dir exists.
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

// resolvePassphrase determines the passphrase via flag or interactive prompt.
func resolvePassphrase(flagValue string, hasFlag bool, u *ui.UI) (string, error) {
	if hasFlag {
		return flagValue, nil
	}

	passphrase, err := u.SecretInput("Backup passphrase (empty for no encryption)")
	if err != nil {
		return "", fmt.Errorf("failed to read passphrase: %w", err)
	}

	if passphrase != "" {
		confirm, err := u.SecretInput("Confirm passphrase")
		if err != nil {
			return "", fmt.Errorf("failed to read confirmation: %w", err)
		}

		if passphrase != confirm {
			return "", errors.New("passphrases do not match")
		}
	}

	return passphrase, nil
}

// readKeystorePassword extracts the keystore password from values-remote-signer.yaml.
func readKeystorePassword(deployDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(deployDir, "values-remote-signer.yaml"))
	if err != nil {
		return "", err
	}

	var values struct {
		KeystorePassword struct {
			Value string `yaml:"value"`
		} `yaml:"keystorePassword"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return "", fmt.Errorf("failed to parse values-remote-signer.yaml: %w", err)
	}

	if values.KeystorePassword.Value == "" {
		return "", errors.New("keystorePassword.value not found in values-remote-signer.yaml")
	}

	return values.KeystorePassword.Value, nil
}

// writeKeystorePassword writes the remote-signer values YAML with the given password.
func writeKeystorePassword(deployDir, password string) error {
	content := generateRemoteSignerValues(&WalletInfo{Password: password})
	return os.WriteFile(filepath.Join(deployDir, "values-remote-signer.yaml"), []byte(content), 0o600)
}

// encryptBackup encrypts plaintext using AES-256-GCM with a scrypt-derived key.
// Format: magic(4) || version(1) || salt(32) || nonce(12) || ciphertext+tag
func encryptBackup(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("salt generation: %w", err)
	}

	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt key derivation: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce generation: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Assemble: magic || version || salt || nonce || ciphertext
	result := make([]byte, 0, len(backupMagic)+1+len(salt)+len(nonce)+len(ciphertext))
	result = append(result, backupMagic...)
	result = append(result, backupVersion)
	result = append(result, salt...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)

	return result, nil
}

// decryptBackup decrypts an encrypted backup file.
func decryptBackup(data []byte, passphrase string) ([]byte, error) {
	minLen := len(backupMagic) + 1 + 32 + 12 // magic + version + salt + nonce
	if len(data) < minLen {
		return nil, errors.New("encrypted file too short")
	}

	offset := 0

	// Verify magic.
	if string(data[offset:offset+len(backupMagic)]) != string(backupMagic) {
		return nil, errors.New("not an encrypted backup file")
	}

	offset += len(backupMagic)

	// Check version.
	version := data[offset]
	offset++

	if version != backupVersion {
		return nil, fmt.Errorf("unsupported encryption version %d", version)
	}

	// Extract salt.
	salt := data[offset : offset+32]
	offset += 32

	// Derive key.
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt key derivation: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	// Extract nonce.
	nonceSize := gcm.NonceSize()
	if len(data) < offset+nonceSize {
		return nil, errors.New("encrypted file too short for nonce")
	}

	nonce := data[offset : offset+nonceSize]
	offset += nonceSize

	// Decrypt.
	ciphertext := data[offset:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// isEncryptedBackup checks if data starts with the OBOL magic bytes.
func isEncryptedBackup(data []byte) bool {
	if len(data) < len(backupMagic) {
		return false
	}

	return string(data[:len(backupMagic)]) == string(backupMagic)
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
