package hermes

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
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

var readHostKeystoreFileFn = os.ReadFile

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
	keystoreData, err := readKeystoreFileForBackup(cfg, keystorePath)
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

	backup, err := decodeHermesWalletRestoreInput(raw, opts, id, u)
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
		applyWalletMetadataConfigMapFn(cfg, id, deployDir)
		applyHermesKeystorePasswordSecret(cfg, id, w.KeystorePassword, u)
		restartHermesRemoteSignerFn(cfg, id, u)
	}

	u.Success("Wallet restored")
	u.Detail("Address", w.Address)
	u.Detail("Instance", id)
	return nil
}

func decodeHermesWalletRestoreInput(raw []byte, opts RestoreWalletOptions, id string, u *ui.UI) (*walletbackup.File, error) {
	passphrase := opts.Passphrase
	if walletbackup.IsEncrypted(raw) && !opts.HasPassFlag {
		var err error
		passphrase, err = u.SecretInput("Backup passphrase")
		if err != nil {
			return nil, fmt.Errorf("failed to read passphrase: %w", err)
		}
	}

	backup, err := walletbackup.Decode(raw, passphrase)
	if err == nil {
		return backup, nil
	}
	if !isRawV3Keystore(raw) {
		return nil, err
	}

	keystorePassword := opts.Passphrase
	if !opts.HasPassFlag {
		keystorePassword, err = u.SecretInput("Ethereum keystore password")
		if err != nil {
			return nil, fmt.Errorf("failed to read Ethereum keystore password: %w", err)
		}
	}
	return backupFromRawV3Keystore(raw, keystorePassword, id)
}

func readKeystoreFileForBackup(cfg *config.Config, keystorePath string) ([]byte, error) {
	data, err := readHostKeystoreFileFn(keystorePath)
	if err == nil {
		return data, nil
	}
	if !os.IsPermission(err) || !isK3dBackend(cfg) {
		return nil, err
	}

	data, fallbackErr := k3dNodeExecOutputFn(cfg, keystorePath, "cat {}")
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w; k3d node read fallback failed: %v", err, fallbackErr)
	}
	return data, nil
}

func isK3dBackend(cfg *config.Config) bool {
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-backend"))
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(data)) == "k3d"
}

func isRawV3Keystore(raw []byte) bool {
	var probe struct {
		Version int             `json:"version"`
		Crypto  json.RawMessage `json:"crypto"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Version == 3 && len(probe.Crypto) > 0
}

func backupFromRawV3Keystore(raw []byte, pw, instanceID string) (*walletbackup.File, error) {
	var meta struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse raw Ethereum V3 keystore metadata: %w", err)
	}

	key, err := gethkeystore.DecryptKey(raw, pw)
	if err != nil {
		return nil, fmt.Errorf("decrypt raw Ethereum V3 keystore: %w", err)
	}

	keystoreID := strings.TrimSpace(meta.ID)
	if keystoreID == "" {
		keystoreID = key.Id.String()
	}
	if keystoreID == "" {
		return nil, errors.New("raw Ethereum V3 keystore missing id")
	}

	publicKey := ethcrypto.FromECDSAPub(&key.PrivateKey.PublicKey)
	if len(publicKey) != 65 || publicKey[0] != 0x04 {
		return nil, errors.New("raw Ethereum V3 keystore produced invalid public key")
	}

	return &walletbackup.File{
		Version:  walletbackup.Version,
		Instance: instanceID,
		Wallets: []walletbackup.Wallet{{
			Address:          key.Address.Hex(),
			PublicKey:        "0x" + hex.EncodeToString(publicKey),
			KeystoreUUID:     keystoreID,
			CreatedAt:        time.Now().UTC().Format(time.RFC3339),
			Keystore:         json.RawMessage(raw),
			KeystorePassword: pw,
		}},
	}, nil
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
