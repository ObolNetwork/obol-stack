package hermes

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ImportPrivateKeyWalletOptions holds options for importing a raw private key
// as a Hermes remote-signer wallet.
type ImportPrivateKeyWalletOptions struct {
	PrivateKeyFile string
	Force          bool
	ApplyCluster   bool
}

// ImportPrivateKeyWalletCmd imports an existing private key as the
// remote-signer wallet for a Hermes instance. Mirror of the OpenClaw path.
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
		return fmt.Errorf("instance %q not found — run 'obol hermes onboard --id %s --no-sync' first", id, id)
	}

	existingWallet, _ := ReadWalletMetadata(deployDir)
	if existingWallet != nil && !opts.Force {
		return fmt.Errorf("instance %q already has a wallet (address: %s)\nUse --force to overwrite", id, existingWallet.Address)
	}

	wallet, err := ImportWalletFromPrivateKey(cfg, id, privateKeyHex, u)
	if err != nil {
		return err
	}

	if err := WriteWalletMetadata(deployDir, wallet); err != nil {
		return fmt.Errorf("failed to write wallet metadata: %w", err)
	}

	rsValues := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(filepath.Join(deployDir, "values-remote-signer.yaml"), []byte(rsValues), 0o600); err != nil {
		return fmt.Errorf("failed to write values-remote-signer.yaml: %w", err)
	}

	if err := archiveReplacedHermesKeystore(cfg, id, existingWallet, wallet.KeystoreUUID, u); err != nil {
		return fmt.Errorf("failed to archive replaced keystore: %w", err)
	}

	u.Success("Wallet imported")
	u.Detail("Address", wallet.Address)
	u.Detail("Instance", id)

	return nil
}

// ImportWalletFromPrivateKey provisions an existing Ethereum private key as
// the remote-signer wallet for a Hermes instance.
func ImportWalletFromPrivateKey(cfg *config.Config, id, privateKeyHex string, u *ui.UI) (*WalletInfo, error) {
	privateKeyHex = strings.TrimSpace(strings.TrimPrefix(privateKeyHex, "0x"))
	if privateKeyHex == "" {
		return nil, errors.New("private key is empty")
	}

	key, err := ethcrypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	privKey := ethcrypto.FromECDSA(key)
	defer func() {
		for i := range privKey {
			privKey[i] = 0
		}
	}()

	pubKeyWithPrefix := ethcrypto.FromECDSAPub(&key.PublicKey)
	if len(privKey) != 32 || len(pubKeyWithPrefix) != 65 || pubKeyWithPrefix[0] != 0x04 {
		return nil, errors.New("invalid private key material")
	}

	pubKey := pubKeyWithPrefix[1:]
	address := ethcrypto.PubkeyToAddress(key.PublicKey).Hex()

	password, err := generateRandomPassword(32)
	if err != nil {
		return nil, fmt.Errorf("password generation failed: %w", err)
	}

	keystoreJSON, keystoreID, err := encryptToV3Keystore(privKey, pubKey, password)
	if err != nil {
		return nil, fmt.Errorf("keystore encryption failed: %w", err)
	}

	keystorePath, err := provisionKeystoreToVolume(cfg, id, keystoreID, keystoreJSON, u)
	if err != nil {
		return nil, fmt.Errorf("keystore provisioning failed: %w", err)
	}

	return &WalletInfo{
		Address:      address,
		PublicKey:    "0x04" + hex.EncodeToString(pubKey),
		KeystoreUUID: keystoreID,
		KeystorePath: keystorePath,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		Password:     password,
	}, nil
}

func archiveReplacedHermesKeystore(cfg *config.Config, id string, existingWallet *WalletInfo, keepUUID string, u *ui.UI) error {
	if existingWallet == nil || existingWallet.KeystoreUUID == "" || existingWallet.KeystoreUUID == keepUUID {
		return nil
	}

	dir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id)

	// The keystores volume is normally container-owned (uid 10000, mode 700)
	// after provisionKeystoreToVolume's fixRuntimeVolumeOwnership. Bookend the
	// stat/mkdir/rename with the same ownership flip provision uses, otherwise
	// the host process can't even traverse the directory.
	ensureVolumeWritable(cfg, dir, u)
	defer fixRuntimeVolumeOwnership(cfg, dir, u)

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
